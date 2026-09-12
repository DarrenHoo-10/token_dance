package mysql

import (
	"database/sql"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

// ensureTestInstallation inserts an installation row when missing (telemetry FKs require it).
func ensureTestInstallation(t *testing.T, db *sql.DB, userID, installationID string, now time.Time) {
	t.Helper()
	pk := crypto.SHA256([]byte("pk:" + installationID))
	_, err := db.Exec(`
		INSERT INTO installations (
			installation_id, user_id, device_public_key, os_type, architecture,
			collector_version, installation_status, registered_at, updated_at
		) VALUES (?, ?, ?, 'windows', 'x86_64', 'test', 'active', ?, ?)
		ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at)`,
		installationID, userID, pk[:], now, now)
	if err != nil {
		t.Fatalf("ensure installation %s for %s: %v", installationID, userID, err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM installations WHERE installation_id = ?`, installationID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("installation %s missing after insert (n=%d err=%v)", installationID, n, err)
	}
}

// seedTelemetryDayModelTokens inserts day-grain trusted tokens into telemetry_model_metrics.
// Old daily_* tables are intentionally left empty.
func seedTelemetryDayModelTokens(t *testing.T, db *sql.DB, userID, date, harness string, exact, derived int64) {
	t.Helper()
	now := time.Now().UTC()
	installationID := "ins_" + userID
	if len(installationID) > 30 {
		installationID = installationID[:30]
	}
	ensureTestInstallation(t, db, userID, installationID, now)
	bucket, err := domain.DayBucketStartMs(date)
	if err != nil {
		t.Fatal(err)
	}
	nowMs := now.UnixMilli()
	_, err = db.Exec(`
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start,
			harness_id, model_key, exact_token_total, derived_token_total,
			input_context_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
			model_request_count, usage_observed_count, token_total_known_count,
			metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, ?, 1, ?, ?, 0, 0, 0, 0, 0, 1, 1, 1, 1)
		ON DUPLICATE KEY UPDATE
			exact_token_total = exact_token_total + VALUES(exact_token_total),
			derived_token_total = derived_token_total + VALUES(derived_token_total),
			updated_at = VALUES(updated_at)`,
		nowMs, nowMs, userID, installationID, bucket, harness, exact, derived)
	if err != nil {
		t.Fatalf("seed telemetry model metrics: %v", err)
	}
}

// seedTelemetryPersonalDay seeds harness + model + cost rows for personal summary tests.
func seedTelemetryPersonalDay(t *testing.T, db *sql.DB, userID, date, harness string, tokens struct {
	Exact, Derived, InputContext, Output, CacheRead, CacheWrite, Reasoning int64
}, harnessVals struct {
	CodeLines, DurationMs, Messages, UserMessages int64
}, costUnits int64) {
	t.Helper()
	now := time.Now().UTC()
	installationID := "ins_" + userID
	if len(installationID) > 30 {
		installationID = installationID[:30]
	}
	ensureTestInstallation(t, db, userID, installationID, now)
	bucket, err := domain.DayBucketStartMs(date)
	if err != nil {
		t.Fatal(err)
	}
	nowMs := now.UnixMilli()
	eligibleIn, eligibleRead, pairKnown := int64(0), int64(0), int64(0)
	// Mirror aggregation: only paired samples where both fields are known and valid.
	if tokens.CacheRead <= tokens.InputContext {
		eligibleIn = tokens.InputContext
		eligibleRead = tokens.CacheRead
		pairKnown = 1
	}
	_, err = db.Exec(`
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start,
			harness_id, model_key, exact_token_total, derived_token_total,
			input_context_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
			cache_eligible_input_tokens, cache_eligible_read_tokens, cache_pair_known_count,
			model_request_count, usage_observed_count, token_total_known_count,
			input_context_known_count, output_known_count, cache_read_known_count,
			metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, 1, 1, 1, 1, 1)`,
		nowMs, nowMs, userID, installationID, bucket, harness,
		tokens.Exact, tokens.Derived, tokens.InputContext, tokens.Output,
		tokens.CacheRead, tokens.CacheWrite, tokens.Reasoning,
		eligibleIn, eligibleRead, pairKnown)
	if err != nil {
		t.Fatalf("seed model: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO telemetry_harness_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start, harness_id,
			code_generated_lines, active_duration_ms, turn_started_count, turn_completed_count,
			user_turn_started_count, code_known_count, duration_known_count, message_known_count,
			metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, ?, ?, ?, ?, 0, ?, 1, 1, 1, 1)`,
		nowMs, nowMs, userID, installationID, bucket, harness,
		harnessVals.CodeLines, harnessVals.DurationMs, harnessVals.Messages, harnessVals.UserMessages)
	if err != nil {
		t.Fatalf("seed harness: %v", err)
	}
	if costUnits > 0 {
		seedTelemetryCost(t, db, userID, date, harness, "USD", costUnits)
	}
}

func seedTelemetryCost(t *testing.T, db *sql.DB, userID, date, harness, currency string, costUnits int64) {
	t.Helper()
	now := time.Now().UTC()
	installationID := "ins_" + userID
	if len(installationID) > 30 {
		installationID = installationID[:30]
	}
	ensureTestInstallation(t, db, userID, installationID, now)
	bucket, err := domain.DayBucketStartMs(date)
	if err != nil {
		t.Fatal(err)
	}
	nowMs := now.UnixMilli()
	_, err = db.Exec(`
		INSERT INTO telemetry_cost_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start,
			harness_id, model_key, currency, estimated_cost_units, estimated_request_count,
			cost_known_count, metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, ?, 1, ?, ?, 1, 1, 1)`,
		nowMs, nowMs, userID, installationID, bucket, harness, currency, costUnits)
	if err != nil {
		t.Fatalf("seed cost %s: %v", currency, err)
	}
}
