package mysql

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/leaderboard"
)

func TestCommunityStatsWindowCountsUniqueDevelopers(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, domain.DayTZ)
	userA := "usr_comm_win_a"
	userB := "usr_comm_win_b"
	seedTestUser(t, db, st, userA, "win_a", "Win A", "win-a@tokendance.dev", true, now)
	seedTestUser(t, db, st, userB, "win_b", "Win B", "win-b@tokendance.dev", true, now)

	tokens := struct {
		Exact, Derived, InputContext, Output, CacheRead, CacheWrite, Reasoning int64
	}{100, 0, 100, 0, 0, 0, 0}
	harness := struct {
		CodeLines, DurationMs, Messages, UserMessages int64
	}{4, 10, 2, 1}
	seedTelemetryPersonalDay(t, db, userA, "2026-09-08", "codex", tokens, harness, 100000000)
	seedTelemetryPersonalDay(t, db, userA, "2026-09-09", "codex", tokens, harness, 100000000)
	seedTelemetryPersonalDay(t, db, userB, "2026-09-09", "codex", tokens, harness, 100000000)

	for _, date := range []string{"2026-09-08", "2026-09-09"} {
		totals, err := st.CommunityStats().SumCommunityDay(ctx, date)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CommunityStats().UpsertCommunityDailyStats(ctx, totals); err != nil {
			t.Fatal(err)
		}
	}

	today, err := leaderboard.NewService(st).GetCommunityStats(ctx, now, "today")
	if err != nil {
		t.Fatal(err)
	}
	if today.Developers == nil || *today.Developers != 2 {
		t.Fatalf("today unique developers: %+v", today)
	}

	week, err := leaderboard.NewService(st).GetCommunityStats(ctx, now, "7d")
	if err != nil {
		t.Fatal(err)
	}
	if week.Window != "7d" {
		t.Fatalf("window: %+v", week)
	}
	if week.Developers == nil || *week.Developers != 2 {
		t.Fatalf("7d must count unique developers, not 1+2 daily actives: %+v", week)
	}
	if week.Tokens == nil || *week.Tokens != "300" {
		t.Fatalf("7d tokens 100+200: %+v", week)
	}
	if week.CodeLines == nil || *week.CodeLines != "12" {
		t.Fatalf("7d code lines: %+v", week)
	}
}
