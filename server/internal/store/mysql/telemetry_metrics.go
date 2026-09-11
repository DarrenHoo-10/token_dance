package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"strings"
	"time"

	"tokendance/internal/domain"
)

// SumTrustedTokensForWindow sums exact+derived token totals from day-grain model metrics.
func SumTrustedTokensForWindow(ctx context.Context, tx *sql.Tx, userID string, fromDate, toDate string) (uint64, error) {
	fromMs, err := domain.DayBucketStartMs(fromDate)
	if err != nil {
		return 0, err
	}
	toMs, err := domain.DayBucketStartMs(toDate)
	if err != nil {
		return 0, err
	}
	var raw sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS CHAR)
		FROM telemetry_model_metrics
		WHERE user_id = ?
		  AND grain = 'day'
		  AND delete_at IS NULL
		  AND bucket_start >= ?
		  AND bucket_start <= ?`,
		userID, fromMs, toMs,
	).Scan(&raw); err != nil {
		return 0, fmt.Errorf("sum trusted telemetry tokens: %w", err)
	}
	if !raw.Valid || raw.String == "" || raw.String == "0" {
		return 0, nil
	}
	n := new(big.Int)
	if _, ok := n.SetString(raw.String, 10); !ok {
		return 0, fmt.Errorf("parse token sum %q", raw.String)
	}
	if !n.IsUint64() {
		return 0, fmt.Errorf("token sum overflows uint64")
	}
	return n.Uint64(), nil
}

// ApplyHarnessMetricDeltaTx upserts additive harness metric fields (duration may be signed).
func ApplyHarnessMetricDeltaTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID string, semantics uint16, nowMs int64, d map[string]int64) error {
	if len(d) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_harness_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id, metric_semantics_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at)`,
		nowMs, nowMs, userID, installationID, grain, bucketStart, harnessID, semantics,
	); err != nil {
		return fmt.Errorf("ensure harness metric row: %w", err)
	}
	setParts := []string{"updated_at = ?"}
	args := []interface{}{nowMs}
	for _, col := range []string{
		"session_count", "child_session_count", "interaction_turn_count",
		"turn_started_count", "turn_completed_count", "user_turn_started_count",
		"tool_call_count", "skill_use_count",
		"code_generated_lines", "code_accepted_lines", "code_added_lines", "code_removed_lines",
		"code_file_touch_count", "correlated_code_lines",
		"active_duration_ms", "code_known_count", "duration_known_count", "message_known_count",
	} {
		v := d[col]
		if v == 0 {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("%s = CAST(%s AS SIGNED) + ?", col, col))
		args = append(args, v)
	}
	if len(args) == 1 {
		return nil
	}
	args = append(args, userID, grain, bucketStart, installationID, harnessID)
	query := fmt.Sprintf(`
		UPDATE telemetry_harness_metrics
		SET %s
		WHERE user_id = ? AND grain = ? AND bucket_start = ? AND installation_id = ? AND harness_id = ?`,
		strings.Join(setParts, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply harness metric delta: %w", err)
	}
	return nil
}

// ApplyModelMetricDeltaTx upserts additive model metric fields.
func ApplyModelMetricDeltaTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID string, modelKey uint64, semantics uint16, nowMs int64, d map[string]int64) error {
	if len(d) == 0 {
		return nil
	}
	cols := []string{
		"exact_token_total", "derived_token_total",
		"input_context_tokens", "input_uncached_tokens", "output_tokens",
		"cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "tool_extra_tokens",
		"model_request_count", "usage_observed_count",
		"token_total_known_count", "input_context_known_count", "input_uncached_known_count",
		"output_known_count", "cache_read_known_count", "cache_write_known_count",
		"reasoning_known_count", "tool_extra_known_count",
		"cache_eligible_input_tokens", "cache_eligible_read_tokens", "cache_pair_known_count",
	}
	args := []interface{}{nowMs, nowMs, userID, installationID, grain, bucketStart, harnessID, modelKey, semantics}
	placeholders := "?, ?, ?, ?, ?, ?, ?, ?, ?"
	updates := "updated_at = VALUES(updated_at)"
	insertCols := "created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id, model_key, metric_semantics_version"
	for _, col := range cols {
		v := d[col]
		insertCols += ", " + col
		placeholders += ", ?"
		args = append(args, v)
		if v != 0 {
			updates += fmt.Sprintf(", %s = %s + VALUES(%s)", col, col, col)
		}
	}
	query := fmt.Sprintf(`
		INSERT INTO telemetry_model_metrics (%s)
		VALUES (%s)
		ON DUPLICATE KEY UPDATE %s`, insertCols, placeholders, updates)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply model metric delta: %w", err)
	}
	return nil
}

// ApplySkillMetricDeltaTx upserts additive skill metric fields.
func ApplySkillMetricDeltaTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID string, skillID int64, semantics uint16, nowMs int64, d map[string]int64) error {
	if len(d) == 0 {
		return nil
	}
	cols := []string{
		"use_count", "exact_use_count", "derived_use_count", "correlated_use_count",
		"success_count", "failure_count", "duration_ms", "duration_known_count",
	}
	args := []interface{}{nowMs, nowMs, userID, installationID, grain, bucketStart, harnessID, skillID, semantics}
	placeholders := "?, ?, ?, ?, ?, ?, ?, ?, ?"
	updates := "updated_at = VALUES(updated_at)"
	insertCols := "created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id, skill_id, metric_semantics_version"
	for _, col := range cols {
		v := d[col]
		insertCols += ", " + col
		placeholders += ", ?"
		args = append(args, v)
		if v != 0 {
			updates += fmt.Sprintf(", %s = CAST(%s AS SIGNED) + VALUES(%s)", col, col, col)
		}
	}
	query := fmt.Sprintf(`
		INSERT INTO telemetry_skill_metrics (%s)
		VALUES (%s)
		ON DUPLICATE KEY UPDATE %s`, insertCols, placeholders, updates)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply skill metric delta: %w", err)
	}
	return nil
}

// ApplyCostMetricDeltaTx upserts signed cost metric fields (may subtract on replace).
func ApplyCostMetricDeltaTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID string, modelKey uint64, currency string, semantics uint16, nowMs int64, d map[string]int64) error {
	if len(d) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_cost_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id,
			model_key, currency, metric_semantics_version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at)`,
		nowMs, nowMs, userID, installationID, grain, bucketStart, harnessID, modelKey, currency, semantics,
	); err != nil {
		return fmt.Errorf("ensure cost metric row: %w", err)
	}
	setParts := []string{"updated_at = ?"}
	args := []interface{}{nowMs}
	for _, col := range []string{
		"reported_cost_units", "estimated_cost_units",
		"reported_request_count", "estimated_request_count", "unpriced_request_count", "cost_known_count",
	} {
		v := d[col]
		if v == 0 {
			continue
		}
		setParts = append(setParts, fmt.Sprintf("%s = CAST(%s AS SIGNED) + ?", col, col))
		args = append(args, v)
	}
	if len(args) == 1 {
		return nil
	}
	args = append(args, userID, grain, bucketStart, installationID, harnessID, modelKey, currency)
	query := fmt.Sprintf(`
		UPDATE telemetry_cost_metrics
		SET %s
		WHERE user_id = ? AND grain = ? AND bucket_start = ? AND installation_id = ?
		  AND harness_id = ? AND model_key = ? AND currency = ?`, strings.Join(setParts, ", "))
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("apply cost metric delta: %w", err)
	}
	return nil
}

// UpsertBucketEntityTx inserts or updates an entity anchor row.
func UpsertBucketEntityTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID, kind string, key []byte, parentKey []byte, hasStarted, hasCompleted, hasUserStart bool, sessionDur, turnDur sql.NullInt64, nowMs int64) error {
	var parent interface{}
	if parentKey != nil {
		parent = parentKey
	}
	var sess interface{}
	if sessionDur.Valid {
		sess = sessionDur.Int64
	}
	var turn interface{}
	if turnDur.Valid {
		turn = turnDur.Int64
	}
	started, completed, userStart := 0, 0, 0
	if hasStarted {
		started = 1
	}
	if hasCompleted {
		completed = 1
	}
	if hasUserStart {
		userStart = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_bucket_entities (
			created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id,
			entity_kind, entity_key, parent_key, has_started, has_completed, has_user_start,
			session_duration_ms, turn_duration_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			parent_key = COALESCE(VALUES(parent_key), parent_key),
			has_started = GREATEST(has_started, VALUES(has_started)),
			has_completed = GREATEST(has_completed, VALUES(has_completed)),
			has_user_start = GREATEST(has_user_start, VALUES(has_user_start)),
			session_duration_ms = COALESCE(VALUES(session_duration_ms), session_duration_ms),
			turn_duration_ms = COALESCE(VALUES(turn_duration_ms), turn_duration_ms),
			updated_at = VALUES(updated_at)`,
		nowMs, nowMs, userID, installationID, grain, bucketStart, harnessID,
		kind, key, parent, started, completed, userStart, sess, turn,
	); err != nil {
		return fmt.Errorf("upsert bucket entity: %w", err)
	}
	return nil
}

// LockBucketEntityTx locks or creates an empty entity anchor (sorted by caller).
func LockBucketEntityTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID, kind string, key []byte, nowMs int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO telemetry_bucket_entities (
			created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id,
			entity_kind, entity_key, has_started, has_completed, has_user_start
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0)
		ON DUPLICATE KEY UPDATE id = id`,
		nowMs, nowMs, userID, installationID, grain, bucketStart, harnessID, kind, key,
	); err != nil {
		return fmt.Errorf("ensure bucket entity: %w", err)
	}
	var id uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM telemetry_bucket_entities
		WHERE user_id = ? AND installation_id = ? AND grain = ? AND bucket_start = ?
		  AND harness_id = ? AND entity_kind = ? AND entity_key = ?
		FOR UPDATE`,
		userID, installationID, grain, bucketStart, harnessID, kind, key,
	).Scan(&id); err != nil {
		return fmt.Errorf("lock bucket entity: %w", err)
	}
	return nil
}

// LoadBucketEntityTx loads entity state after lock.
func LoadBucketEntityTx(ctx context.Context, tx *sql.Tx, userID, installationID, grain string, bucketStart int64, harnessID, kind string, key []byte) (
	hasStarted, hasCompleted, hasUserStart bool,
	sessionDur, turnDur sql.NullInt64,
	parentKey []byte,
	err error,
) {
	var started, completed, userStart uint8
	err = tx.QueryRowContext(ctx, `
		SELECT has_started, has_completed, has_user_start, session_duration_ms, turn_duration_ms, parent_key
		FROM telemetry_bucket_entities
		WHERE user_id = ? AND installation_id = ? AND grain = ? AND bucket_start = ?
		  AND harness_id = ? AND entity_kind = ? AND entity_key = ?`,
		userID, installationID, grain, bucketStart, harnessID, kind, key,
	).Scan(&started, &completed, &userStart, &sessionDur, &turnDur, &parentKey)
	if err != nil {
		return
	}
	hasStarted = started == 1
	hasCompleted = completed == 1
	hasUserStart = userStart == 1
	return
}

// MarkEventConsumerAppliedTx sets status_json path and deletes the task under lease.
func MarkEventConsumerAppliedTx(ctx context.Context, tx *sql.Tx, eventRowID, taskID uint64, consumer string, leaseToken []byte, nowMs int64) error {
	path := "$." + consumer
	// Pass a plain integer so MySQL stores JSON INTEGER (CHECK requires JSON_TYPE=INTEGER).
	res, err := tx.ExecContext(ctx, `
		UPDATE telemetry_events
		SET status_json = JSON_SET(status_json, ?, CAST(? AS SIGNED)),
		    updated_at = ?
		WHERE id = ?
		  AND delete_at IS NULL
		  AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json, ?)) AS UNSIGNED) <> ?`,
		path, domain.TelemetryStatusApplied, nowMs, eventRowID, path, domain.TelemetryStatusApplied,
	)
	if err != nil {
		return fmt.Errorf("mark consumer applied: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("mark consumer applied: event missing or already applied")
	}
	del, err := tx.ExecContext(ctx, `
		DELETE FROM telemetry_tasks
		WHERE id = ? AND lease_token = ? AND consumer = ?`,
		taskID, leaseToken, consumer,
	)
	if err != nil {
		return fmt.Errorf("delete telemetry task: %w", err)
	}
	if n, _ := del.RowsAffected(); n != 1 {
		return fmt.Errorf("delete telemetry task: lease mismatch")
	}
	return nil
}

// FailTelemetryTaskTx releases lease and schedules retry.
func FailTelemetryTaskTx(ctx context.Context, tx *sql.Tx, taskID uint64, leaseToken []byte, errorCode string, retryAt time.Time, nowMs int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE telemetry_tasks
		SET runnable_at = ?,
		    lease_token = NULL,
		    lease_until = NULL,
		    last_error_code = ?,
		    updated_at = ?
		WHERE id = ? AND lease_token = ?`,
		retryAt.UnixMilli(), errorCode, nowMs, taskID, leaseToken,
	)
	return err
}

func uintPtrFromNull(n sql.NullInt64) *uint64 {
	if !n.Valid {
		return nil
	}
	v := uint64(n.Int64)
	return &v
}
