package analytics

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/store/memory"
)

func TestTimeRangesDSTAllAndCustom(t *testing.T) {
	clk := clock.NewMockClock(time.Date(2026, 3, 8, 16, 0, 0, 0, time.UTC))
	svc := NewService(memory.NewMemoryStore(), clk)
	custom, err := svc.ResolveTimeRange("custom", "America/New_York", "2026-03-08", "2026-03-08")
	if err != nil {
		t.Fatal(err)
	}
	if duration := custom.To.Sub(custom.From) + time.Nanosecond; duration != 23*time.Hour {
		t.Fatalf("expected 23-hour DST day, got %v", duration)
	}
	all, err := svc.ResolveTimeRange("all", "Asia/Tokyo", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if all.From.In(time.FixedZone("JST", 9*3600)).Year() != 1970 {
		t.Fatalf("all range did not start at epoch calendar boundary")
	}
	if _, err := svc.ResolveTimeRange("custom", "UTC", "2026-04-01", "2026-03-01"); err == nil {
		t.Fatal("expected invalid reversed custom range")
	}
	tenWeeks, err := svc.ResolveTimeRange("10w", "UTC", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if days := int(tenWeeks.To.Sub(tenWeeks.From).Hours()/24) + 1; days != 70 {
		t.Fatalf("expected 70-day calendar range, got %d", days)
	}
}

func TestActivityFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)
	_, _, _ = st.SeedUserForTest("usr_activity", "activity", "activity@example.com", clk.Now())

	// No collected data: empty activity page without a cursor.
	first, err := svc.GetActivity(ctx, "usr_activity", "7d", "", "", "", "", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 0 || first.NextCursor != nil {
		t.Fatalf("expected empty activity page, got %+v", first)
	}

	// Filters the user has no data for are rejected.
	if _, err := svc.GetActivity(ctx, "usr_activity", "7d", "", "", "claude-code", "", "", "", 20); err == nil {
		t.Fatal("expected unavailable filter rejection")
	}
	if _, err := svc.GetActivity(ctx, "usr_activity", "7d", "", "", "", "", "", "tampered", 20); err == nil {
		t.Fatal("expected tampered cursor rejection")
	}
}

func TestAnalyticsService(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)

	userID := "usr_analytictest"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "anuser", "an@tokendance.dev", now)

	// 1. Personal Summary: empty metrics until collector data arrives
	summary, err := svc.GetPersonalSummary(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get personal summary: %v", err)
	}
	if summary.Metrics.TotalTokens.Supported {
		t.Errorf("expected total tokens to be unsupported without data")
	}
	if summary.Metrics.TotalTokens.Value != nil {
		t.Errorf("expected nil total tokens value without data")
	}
	if summary.Metrics.EstimatedCost.Amount != nil {
		t.Errorf("expected nil cost amount without data")
	}

	// 2. Token Trends: empty until data arrives
	trendTotal, err := svc.GetTokenTrend(ctx, userID, "7d", "total", nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to get token trends: %v", err)
	}
	if len(trendTotal.Points) != 0 {
		t.Errorf("expected empty trend points without data, got %d", len(trendTotal.Points))
	}

	// 3. Agent & Model Breakdowns: empty until data arrives
	agentBd, err := svc.GetAgentBreakdown(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get agent breakdown: %v", err)
	}
	if len(agentBd.Items) != 0 {
		t.Errorf("expected empty agent breakdown without data, got %d", len(agentBd.Items))
	}

	modelBd, err := svc.GetModelBreakdown(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get model breakdown: %v", err)
	}
	if len(modelBd.Items) != 0 {
		t.Errorf("expected empty model breakdown without data, got %d", len(modelBd.Items))
	}

	// 4. Skills & Calendar: empty/zero until data arrives
	skills, err := svc.GetSkillRanking(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get skills: %v", err)
	}
	if len(skills.Skills) != 0 {
		t.Errorf("expected empty skills without data, got %d", len(skills.Skills))
	}

	cal, err := svc.GetActivityCalendar(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get calendar: %v", err)
	}
	if len(cal.Days) != 30 {
		t.Errorf("expected 30 days in calendar, got %d", len(cal.Days))
	}
	for _, day := range cal.Days {
		if day.Active || day.Level != 0 {
			t.Errorf("expected inactive calendar day without data, got %+v", day)
		}
	}
	if cal.CurrentStreak != 0 || cal.LongestStreak != 0 {
		t.Errorf("expected zero streaks without data")
	}
}
