package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"tokendance/internal/domain"
)

// UsageIdentityRepairReport contains only counts and a resumable row cursor.
type UsageIdentityRepairReport struct {
	NextID                         uint64
	Examined, Normalized, Requeued int
}

// RepairUsageIdentity requeues non-applied, retained additive facts using the
// corrected ownership rule. It never clears aggregates or replays applied rows.
// Run with the fixed API/worker deployed; pause ingest, workers and pruning for
// a stable dry-run/backup/apply audit. afterID is a scan cursor, not a sync watermark.
func (w *Worker) RepairUsageIdentity(ctx context.Context, afterID uint64, limit int, dryRun bool, now time.Time) (UsageIdentityRepairReport, error) {
	r := UsageIdentityRepairReport{NextID: afterID}
	if limit <= 0 || limit > 1000 {
		return r, fmt.Errorf("limit must be 1..1000")
	}
	rows, err := w.db.QueryContext(ctx, `SELECT id FROM telemetry_events
		WHERE id > ? AND delete_at IS NULL
		AND event_type IN ('model_usage_recorded','cost_recorded')
		AND (usage_identity_hash=UNHEX(REPEAT('00',32))
		 OR JSON_EXTRACT(status_json,'$.hour')=4 OR JSON_EXTRACT(status_json,'$.day')=4 OR JSON_EXTRACT(status_json,'$.month')=4)
		ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return r, err
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return r, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return r, err
	}
	for _, id := range ids {
		normalized, requeued, err := w.repairUsageIdentityEvent(ctx, id, dryRun, now.UnixMilli())
		if err != nil {
			return r, err
		}
		r.Examined++
		r.Normalized += normalized
		r.Requeued += requeued
		r.NextID = id
	}
	return r, nil
}

func (w *Worker) repairUsageIdentityEvent(ctx context.Context, id uint64, dryRun bool, nowMs int64) (int, int, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var user, installation, harness, kind string
	var hash, fact, raw []byte
	var revision uint64
	err = tx.QueryRowContext(ctx, `SELECT i.user_id,e.installation_id,e.harness_id,e.event_type,e.usage_identity_hash,e.fact_key,e.fact_revision,e.status_json
		FROM telemetry_events e JOIN installations i ON i.installation_id=e.installation_id
		WHERE e.id=? AND e.delete_at IS NULL FOR UPDATE`, id).Scan(&user, &installation, &harness, &kind, &hash, &fact, &revision, &raw)
	if err == sql.ErrNoRows {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	var states map[string]int
	if err = json.Unmarshal(raw, &states); err != nil {
		return 0, 0, err
	}
	normalized := 0
	if len(hash) == 32 && !validUsageIdentityHash(hash) {
		normalized = 1
		if !dryRun {
			if _, err = tx.ExecContext(ctx, `UPDATE telemetry_events SET usage_identity_hash=NULL,usage_identity_source=NULL,updated_at=? WHERE id=?`, nowMs, id); err != nil {
				return 0, 0, err
			}
		}
	}
	shadowed, err := usageIdentityShadowed(ctx, tx, user, installation, harness, kind, hash, fact)
	if err != nil {
		return 0, 0, err
	}
	// Older revisions remain inapplicable even if an earlier bug shadowed the
	// latest one too. The latest retained revision alone is eligible for replay.
	var newer bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM telemetry_events WHERE installation_id=? AND fact_key=? AND fact_revision>? AND delete_at IS NULL)`, installation, fact, revision).Scan(&newer)
	if err != nil {
		return 0, 0, err
	}
	requeued := 0
	if !shadowed && !newer {
		for _, grain := range []string{domain.TelemetryGrainHour, domain.TelemetryGrainDay, domain.TelemetryGrainMonth} {
			if states[grain] != domain.TelemetryStatusNotApplicable {
				continue
			}
			requeued++
			if dryRun {
				continue
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO telemetry_tasks(created_at,updated_at,event_row_id,consumer,runnable_at) VALUES(?,?,?,?,?)`, nowMs, nowMs, id, grain, nowMs)
			if err != nil {
				return 0, 0, err
			}
			_, err = tx.ExecContext(ctx, `UPDATE telemetry_events SET status_json=JSON_SET(status_json,?,0),updated_at=? WHERE id=?`, "$."+grain, nowMs, id)
			if err != nil {
				return 0, 0, err
			}
		}
	}
	if dryRun {
		return normalized, requeued, nil
	}
	return normalized, requeued, tx.Commit()
}
