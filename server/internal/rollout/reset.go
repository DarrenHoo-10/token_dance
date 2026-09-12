package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	PhasePending    = "pending"
	PhaseResetting  = "resetting"
	PhaseReady      = "ready"
	PhaseRolledBack = "rolled_back"

	// ClosedBetaGeneration is the one-shot empty-DB generation for this cutover.
	ClosedBetaGeneration uint64 = 1
)

// State is the durable rollout row (id=1).
type State struct {
	Generation       uint64
	Phase            string
	ResetStartedAt   *time.Time
	ResetCompletedAt *time.Time
	NotesJSON        string
}

// ResetOptions controls the empty-DB cutover.
type ResetOptions struct {
	// TargetGeneration defaults to ClosedBetaGeneration.
	TargetGeneration uint64
	// Force re-runs reset even when already ready for TargetGeneration.
	// Closed-beta only; forbidden once formal releases rely on live stats.
	Force bool
	// AllowMissing skips tables that do not exist yet (partial migrate).
	AllowMissing bool
	// DryRun reports the plan without mutating.
	DryRun bool
	// Notes optional operator annotation stored in notes_json.
	Notes map[string]any
	// AfterTruncate optional hook (e.g. Redis cache clear) run inside success path.
	AfterTruncate func(ctx context.Context) error
}

// Result summarizes one reset attempt.
type Result struct {
	Generation     uint64   `json:"generation"`
	Phase          string   `json:"phase"`
	AlreadyReady   bool     `json:"alreadyReady"`
	Truncated      []string `json:"truncated"`
	SkippedMissing []string `json:"skippedMissing,omitempty"`
	DryRun         bool     `json:"dryRun"`
}

// LoadState reads the singleton rollout row.
func LoadState(ctx context.Context, db *sql.DB) (*State, error) {
	row := db.QueryRowContext(ctx, `
		SELECT generation, phase, reset_started_at, reset_completed_at, CAST(notes_json AS CHAR)
		FROM event_pipeline_rollout_state WHERE id = 1`)
	st := &State{}
	var started, completed sql.NullTime
	var notes sql.NullString
	if err := row.Scan(&st.Generation, &st.Phase, &started, &completed, &notes); err != nil {
		return nil, fmt.Errorf("load rollout state: %w", err)
	}
	if started.Valid {
		t := started.Time
		st.ResetStartedAt = &t
	}
	if completed.Valid {
		t := completed.Time
		st.ResetCompletedAt = &t
	}
	if notes.Valid {
		st.NotesJSON = notes.String
	} else {
		st.NotesJSON = "{}"
	}
	return st, nil
}

// ResetStats truncates statistics tables once per generation (idempotent).
// PreservedTables are never touched. Re-entry after PhaseReady is a no-op unless Force.
func ResetStats(ctx context.Context, db *sql.DB, opts ResetOptions) (*Result, error) {
	gen := opts.TargetGeneration
	if gen == 0 {
		gen = ClosedBetaGeneration
	}
	if err := assertPreservedInventory(); err != nil {
		return nil, err
	}

	st, err := LoadState(ctx, db)
	if err != nil {
		return nil, err
	}
	if st.Phase == PhaseReady && st.Generation >= gen && !opts.Force {
		return &Result{
			Generation:   st.Generation,
			Phase:        st.Phase,
			AlreadyReady: true,
		}, nil
	}
	if opts.DryRun {
		present, missing, err := classifyTables(ctx, db, StatsClearTables)
		if err != nil {
			return nil, err
		}
		return &Result{
			Generation:     gen,
			Phase:          st.Phase,
			Truncated:      present,
			SkippedMissing: missing,
			DryRun:         true,
		}, nil
	}

	notes, err := marshalNotes(opts.Notes)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin reset tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE event_pipeline_rollout_state
		SET generation = ?,
		    phase = ?,
		    reset_started_at = CURRENT_TIMESTAMP(3),
		    reset_completed_at = NULL,
		    notes_json = CAST(? AS JSON)
		WHERE id = 1`, gen, PhaseResetting, notes); err != nil {
		return nil, fmt.Errorf("mark resetting: %w", err)
	}

	// Disable FK checks for truncate order safety across optional tables.
	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
		return nil, fmt.Errorf("disable fk checks: %w", err)
	}

	truncated := make([]string, 0, len(StatsClearTables))
	skipped := make([]string, 0)
	for _, table := range StatsClearTables {
		exists, err := tableExistsTx(ctx, tx, table)
		if err != nil {
			_, _ = tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
			return nil, err
		}
		if !exists {
			if opts.AllowMissing {
				skipped = append(skipped, table)
				continue
			}
			_, _ = tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
			return nil, fmt.Errorf("required stats table missing: %s", table)
		}
		if _, err := tx.ExecContext(ctx, "TRUNCATE TABLE "+quoteIdent(table)); err != nil {
			_, _ = tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1")
			return nil, fmt.Errorf("truncate %s: %w", table, err)
		}
		truncated = append(truncated, table)
	}

	if _, err := tx.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1"); err != nil {
		return nil, fmt.Errorf("re-enable fk checks: %w", err)
	}

	// Re-seed unknown model dimension required by ingest FK (0011 insert).
	if exists, _ := tableExistsTx(ctx, tx, "telemetry_models"); exists {
		nowMs := time.Now().UTC().UnixMilli()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO telemetry_models(id,created_at,updated_at,provider_id,model_id)
			VALUES(1,?,?, 'unknown','unknown')
			ON DUPLICATE KEY UPDATE provider_id=VALUES(provider_id), model_id=VALUES(model_id)`,
			nowMs, nowMs); err != nil {
			return nil, fmt.Errorf("seed unknown telemetry model: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE event_pipeline_rollout_state
		SET phase = ?,
		    reset_completed_at = CURRENT_TIMESTAMP(3),
		    notes_json = CAST(? AS JSON)
		WHERE id = 1`, PhaseReady, notes); err != nil {
		return nil, fmt.Errorf("mark ready: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit reset: %w", err)
	}

	if opts.AfterTruncate != nil {
		if err := opts.AfterTruncate(ctx); err != nil {
			return nil, fmt.Errorf("after truncate hook: %w", err)
		}
	}

	return &Result{
		Generation:     gen,
		Phase:          PhaseReady,
		Truncated:      truncated,
		SkippedMissing: skipped,
	}, nil
}

// MarkRolledBack records operator rollback without restoring legacy ingest.
func MarkRolledBack(ctx context.Context, db *sql.DB, notes map[string]any) error {
	payload, err := marshalNotes(notes)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		UPDATE event_pipeline_rollout_state
		SET phase = ?, notes_json = CAST(? AS JSON)
		WHERE id = 1`, PhaseRolledBack, payload)
	return err
}

func assertPreservedInventory() error {
	clear := make(map[string]struct{}, len(StatsClearTables))
	for _, t := range StatsClearTables {
		clear[t] = struct{}{}
	}
	for _, t := range PreservedTables {
		if _, ok := clear[t]; ok {
			return fmt.Errorf("preserved table %s incorrectly listed in StatsClearTables", t)
		}
	}
	return nil
}

func classifyTables(ctx context.Context, db *sql.DB, tables []string) (present, missing []string, err error) {
	for _, table := range tables {
		ok, e := tableExists(ctx, db, table)
		if e != nil {
			return nil, nil, e
		}
		if ok {
			present = append(present, table)
		} else {
			missing = append(missing, table)
		}
	}
	return present, missing, nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var name string
	err := db.QueryRowContext(ctx, `
		SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func tableExistsTx(ctx context.Context, tx *sql.Tx, table string) (bool, error) {
	var name string
	err := tx.QueryRowContext(ctx, `
		SELECT TABLE_NAME FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?`, table).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func quoteIdent(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "") + "`"
}

func marshalNotes(notes map[string]any) (string, error) {
	if notes == nil {
		notes = map[string]any{}
	}
	b, err := json.Marshal(notes)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
