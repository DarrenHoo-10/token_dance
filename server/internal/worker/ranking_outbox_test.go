package worker

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"tokendance/internal/clock"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/provider"
	"tokendance/internal/ranking"
	"tokendance/internal/store/mysql"
)

func TestProcessRankingOutboxAppliesAbsoluteScores(t *testing.T) {
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
		VALUES ('usr_rank_a', UNHEX(SHA2('rank-a', 256)), 'Rank A', 'active', 'private', 'UTC', 'en-US', ?, ?)`,
		now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (metric_date, user_id, agent_id, exact_token_total, derived_token_total, aggregation_version)
		VALUES ('2026-09-06', 'usr_rank_a', 'codex', 40, 2, 2)`); err != nil {
		t.Fatalf("seed metrics: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	idx := ranking.NewIndex(rdb)

	w := NewWorkerWithFull(db, clock.NewMockClock(now), nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	if _, err := w.BackfillWindowScores(ctx); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n, err := w.ProcessRankingOutbox(ctx); err != nil || n != 0 {
		t.Fatalf("outbox without redis must stay pending: n=%d err=%v", n, err)
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ranking_outbox WHERE task_status='pending'`).Scan(&pending); err != nil || pending == 0 {
		t.Fatalf("expected pending outbox before redis apply: %d %v", pending, err)
	}
	w.SetRanking(idx)
	processed, err := w.ProcessRankingOutbox(ctx)
	if err != nil {
		t.Fatalf("apply outbox: %v", err)
	}
	if processed == 0 {
		t.Fatal("expected ranking outbox to apply")
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ranking_outbox WHERE task_status='pending'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending after apply: %d %v", pending, err)
	}
	var applied int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ranking_outbox WHERE task_status='applied'`).Scan(&applied); err != nil || applied == 0 {
		t.Fatalf("applied count: %d %v", applied, err)
	}
	n, err := idx.Cardinality(ctx, "today", "2026-09-06")
	if err != nil || n != 1 {
		t.Fatalf("redis members: %d %v", n, err)
	}
	again, err := w.ProcessRankingOutbox(ctx)
	if err != nil || again != 0 {
		t.Fatalf("duplicate outbox apply: %d %v", again, err)
	}

	st := mysql.NewStore(db)
	st.SetRanking(idx)
	if _, err := idx.PublishHot(ctx, "today", now); err != nil {
		t.Fatalf("publish: %v", err)
	}
	board, err := st.Leaderboard().GetLeaderboard(ctx, "global", "today", "tokens", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if board.ViewKind != ranking.ViewHot || board.TotalParticipants == nil || *board.TotalParticipants != 1 {
		t.Fatalf("hot read: %+v", board)
	}
	if len(board.Entries) != 1 || board.Entries[0].MetricValue != "42" {
		t.Fatalf("hydrated entry: %+v", board.Entries)
	}
}
