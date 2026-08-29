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
	first, err := svc.GetActivity(ctx, "usr_activity", "7d", "", "", "claude-code", "", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("expected first activity page with cursor: %+v", first)
	}
	second, err := svc.GetActivity(ctx, "usr_activity", "7d", "", "", "claude-code", "", "", *first.NextCursor, 2)
	if err != nil || len(second.Items) == 0 || second.Items[0].Date == first.Items[0].Date {
		t.Fatalf("expected distinct second page: %+v %v", second, err)
	}
	if _, err := svc.GetActivity(ctx, "usr_activity", "7d", "", "", "unknown-agent", "", "", "", 20); err == nil {
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

	// 1. Personal Summary (10 core metrics)
	summary, err := svc.GetPersonalSummary(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get personal summary: %v", err)
	}
	if summary.Metrics.EstimatedCost.Amount == nil || *summary.Metrics.EstimatedCost.Amount != "1428.60000000" {
		t.Errorf("expected cost 1428.60000000")
	}
	if summary.Metrics.TotalTokens.Value == nil || *summary.Metrics.TotalTokens.Value != "325700000" {
		t.Errorf("expected total tokens 325700000")
	}
	if summary.Metrics.GeneratedCodeLines.Value == nil || *summary.Metrics.GeneratedCodeLines.Value != "864200" {
		t.Errorf("expected generated code lines 864200")
	}
	if summary.Metrics.TokensPerCodeLine.Value == nil || *summary.Metrics.TokensPerCodeLine.Value != "376.88" {
		t.Errorf("expected tokens per code line 376.88")
	}

	// 2. Token Trends
	trendTotal, err := svc.GetTokenTrend(ctx, userID, "7d", "total", nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to get token trends: %v", err)
	}
	if len(trendTotal.Points) != 7 {
		t.Errorf("expected 7 points for 7d range, got %d", len(trendTotal.Points))
	}

	trendStruct, err := svc.GetTokenTrend(ctx, userID, "7d", "structure", nil, nil, nil)
	if err != nil {
		t.Fatalf("failed to get structure trend: %v", err)
	}
	if trendStruct.Points[0].InputTokens == nil {
		t.Errorf("expected inputTokens to be present in structure mode")
	}

	// 3. Agent & Model Breakdowns
	agentBd, err := svc.GetAgentBreakdown(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get agent breakdown: %v", err)
	}
	if len(agentBd.Items) == 0 {
		t.Errorf("expected agent breakdown items")
	}

	modelBd, err := svc.GetModelBreakdown(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get model breakdown: %v", err)
	}
	if len(modelBd.Items) == 0 {
		t.Errorf("expected model breakdown items")
	}

	// 4. Skills & Calendar
	skills, err := svc.GetSkillRanking(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get skills: %v", err)
	}
	if len(skills.Skills) == 0 {
		t.Errorf("expected skills to be populated")
	}

	cal, err := svc.GetActivityCalendar(ctx, userID, "30d")
	if err != nil {
		t.Fatalf("failed to get calendar: %v", err)
	}
	if len(cal.Days) != 30 {
		t.Errorf("expected 30 days in calendar, got %d", len(cal.Days))
	}
}
