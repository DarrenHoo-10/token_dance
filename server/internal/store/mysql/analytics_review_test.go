package mysql

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/domain"
)

// TestReviewPersonalStatsReadTelemetryOnly reproduces review blocker #5:
// personal summary / trend / breakdown / skills / live board must work when
// only telemetry_* rows exist and daily_* tables are empty.
func TestReviewPersonalStatsReadTelemetryOnly(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := "usr_review_telemetry"
	seedTestUser(t, db, st, userID, "review_tele", "Review Tele", "review-tele@tokendance.dev", true, now)

	var dailyCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM daily_user_agent_metrics`).Scan(&dailyCount); err != nil {
		t.Fatal(err)
	}
	if dailyCount != 0 {
		t.Fatalf("precondition: daily_* must be empty, got %d", dailyCount)
	}

	seedTelemetryPersonalDay(t, db, userID, "2026-08-29", "codex",
		struct {
			Exact, Derived, InputContext, Output, CacheRead, CacheWrite, Reasoning int64
		}{900, 100, 800, 200, 100, 0, 0},
		struct {
			CodeLines, DurationMs, Messages, UserMessages int64
		}{40, 1200, 6, 2},
		250000000, // 2.5 USD
	)

	r := domain.TimeRange{Key: domain.TimeRange30d, From: now.AddDate(0, 0, -30), To: now, Timezone: "UTC"}
	sum, err := st.Analytics().GetPersonalSummary(ctx, userID, r)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Metrics.TotalTokens.Value == nil || *sum.Metrics.TotalTokens.Value != "1000" {
		t.Fatalf("summary tokens: %+v", sum.Metrics.TotalTokens)
	}
	if sum.Metrics.EstimatedCost.Amount == nil || *sum.Metrics.EstimatedCost.Amount != "2.50000000" {
		t.Fatalf("summary cost: %+v", sum.Metrics.EstimatedCost)
	}

	trend, err := st.Analytics().GetTokenTrend(ctx, userID, r, "total", nil, nil, nil)
	if err != nil || len(trend.Points) != 1 || trend.Points[0].TokenTotal == nil || *trend.Points[0].TokenTotal != "1000" {
		t.Fatalf("trend: %+v err=%v", trend, err)
	}
	ab, err := st.Analytics().GetAgentBreakdown(ctx, userID, r)
	if err != nil || len(ab.Items) != 1 || ab.Items[0].Key != "codex" || ab.Items[0].TokenTotal != "1000" {
		t.Fatalf("agent breakdown: %+v err=%v", ab, err)
	}
	cal, err := st.Analytics().GetActivityCalendar(ctx, userID, r)
	if err != nil || cal.TotalActiveDays != 1 {
		t.Fatalf("calendar: %+v err=%v", cal, err)
	}

	if err := db.QueryRow(`SELECT COUNT(*) FROM daily_user_agent_metrics`).Scan(&dailyCount); err != nil {
		t.Fatal(err)
	}
	if dailyCount != 0 {
		t.Fatalf("daily_* must stay empty after reads, got %d", dailyCount)
	}

	board, err := (&leaderboardStore{db: db}).getLiveTokenLeaderboard(ctx, "30d", nil, 20, now)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range board.Entries {
		if e.Handle == "review_tele" && e.MetricValue == "1000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("live leaderboard missing telemetry user: %+v", board.Entries)
	}
}
