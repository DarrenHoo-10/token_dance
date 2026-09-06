package worker

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/provider"
)

func TestWindowScoreBackfillIdempotentMySQL(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()
	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, display_name, account_status, leaderboard_visibility, timezone_name, locale, created_at, updated_at)
		VALUES ('usr_backfill_a', UNHEX(SHA2('backfill-a', 256)), 'Backfill A', 'active', 'private', 'UTC', 'en-US', ?, ?),
		       ('usr_backfill_b', UNHEX(SHA2('backfill-b', 256)), 'Backfill B', 'active', 'private', 'UTC', 'en-US', ?, ?)`,
		now, now, now, now); err != nil {
		t.Fatalf("seed users: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (metric_date, user_id, agent_id, exact_token_total, derived_token_total, aggregation_version)
		VALUES ('2026-09-06', 'usr_backfill_a', 'codex', 40, 2, 2)`); err != nil {
		t.Fatalf("seed metrics: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO user_window_scores (user_id, window_key, generation, token_total, revision, eligible, registered_at, updated_at)
		VALUES ('usr_backfill_b', 'today', '2026-09-06', 99, 3, TRUE, ?, ?)`, now, now); err != nil {
		t.Fatalf("seed existing positive score: %v", err)
	}

	w := NewWorkerWithFull(db, clock.NewMockClock(now), nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	processed, err := w.BackfillWindowScores(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if processed == 0 {
		t.Fatal("expected backfill to initialize missing scores")
	}
	var tokens, revision uint64
	if err := db.QueryRowContext(ctx, `SELECT token_total, revision FROM user_window_scores WHERE user_id='usr_backfill_a' AND window_key='today' AND generation='2026-09-06'`).Scan(&tokens, &revision); err != nil {
		t.Fatalf("read backfilled score: %v", err)
	}
	if tokens != 42 || revision == 0 {
		t.Fatalf("expected real daily sum 42, got tokens=%d revision=%d", tokens, revision)
	}
	var windows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_window_scores WHERE user_id='usr_backfill_a'`).Scan(&windows); err != nil || windows != 4 {
		t.Fatalf("expected 4 windows, got %d %v", windows, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT token_total FROM user_window_scores WHERE user_id='usr_backfill_b' AND window_key='today'`).Scan(&tokens); err != nil || tokens != 99 {
		t.Fatalf("backfill zeroed existing positive score: %d %v", tokens, err)
	}

	again, err := w.BackfillWindowScores(ctx)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if again != 0 {
		t.Fatalf("idempotent backfill should not reprocess initialized users, got %d", again)
	}
	if err := db.QueryRowContext(ctx, `SELECT token_total FROM user_window_scores WHERE user_id='usr_backfill_a' AND window_key='today'`).Scan(&tokens); err != nil || tokens != 42 {
		t.Fatalf("second backfill changed tokens: %d %v", tokens, err)
	}

	if _, err := w.ProcessRankingOutbox(ctx); err != nil {
		t.Fatalf("ranking outbox without redis must not fail: %v", err)
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ranking_outbox WHERE task_status='pending'`).Scan(&pending); err != nil || pending == 0 {
		t.Fatalf("outbox tasks must stay pending without redis: %d %v", pending, err)
	}
}
