package mysql

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/leaderboard"
)

// TestReview2CommunityMultiCurrencyCostsNotMergedAsUSD reproduces the
// TokenBoard community-fee bug: USD 1 + CNY 7 must not become costAmount=8.
func TestReview2CommunityMultiCurrencyCostsNotMergedAsUSD(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, domain.DayTZ)
	userID := "usr_community_fx"
	seedTestUser(t, db, st, userID, "community_fx", "Community FX", "community-fx@tokendance.dev", true, now)

	seedTelemetryPersonalDay(t, db, userID, "2026-08-29", "codex",
		struct {
			Exact, Derived, InputContext, Output, CacheRead, CacheWrite, Reasoning int64
		}{100, 0, 100, 0, 0, 0, 0},
		struct {
			CodeLines, DurationMs, Messages, UserMessages int64
		}{1, 10, 1, 1},
		100000000, // 1.0 USD
	)
	seedTelemetryCost(t, db, userID, "2026-08-29", "codex", "CNY", 700000000) // 7.0 CNY

	totals, err := st.CommunityStats().SumCommunityDay(ctx, "2026-08-29")
	if err != nil {
		t.Fatal(err)
	}
	if totals.CostAmount != 0 {
		t.Fatalf("mixed community costs must not store scalar sum 8, got %v", totals.CostAmount)
	}
	if len(totals.Costs) != 2 {
		t.Fatalf("expected CNY+USD breakdown, got %+v", totals.Costs)
	}
	byCurr := map[string]float64{}
	for _, c := range totals.Costs {
		byCurr[c.Currency] = c.Amount
	}
	if byCurr["USD"] != 1 || byCurr["CNY"] != 7 {
		t.Fatalf("unexpected per-currency amounts: %+v", byCurr)
	}

	if err := st.CommunityStats().UpsertCommunityDailyStats(ctx, totals); err != nil {
		t.Fatal(err)
	}
	got, err := st.CommunityStats().GetCommunityDailyStats(ctx, "2026-08-29")
	if err != nil || got == nil {
		t.Fatalf("round-trip: %+v %v", got, err)
	}
	if got.CostAmount != 0 || len(got.Costs) != 2 {
		t.Fatalf("persisted mixed costs: %+v", got)
	}

	res, err := leaderboard.NewService(st).GetCommunityStats(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.CostAmount != nil {
		t.Fatalf("API must omit mixed scalar costAmount, got %v", *res.CostAmount)
	}
	if len(res.Costs) != 2 {
		t.Fatalf("API costs: %+v", res.Costs)
	}
	apiByCurr := map[string]float64{}
	for _, c := range res.Costs {
		apiByCurr[c.Currency] = c.Amount
	}
	if apiByCurr["USD"] != 1 || apiByCurr["CNY"] != 7 {
		t.Fatalf("API per-currency: %+v", apiByCurr)
	}
}
