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
	if sum.Metrics.EstimatedCost.Currency == nil || *sum.Metrics.EstimatedCost.Currency != "USD" {
		t.Fatalf("single-currency summary must keep USD label: %+v", sum.Metrics.EstimatedCost)
	}
	if len(sum.Metrics.EstimatedCosts) != 1 {
		t.Fatalf("expected 1 estimatedCosts entry, got %+v", sum.Metrics.EstimatedCosts)
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

// TestReview2MultiCurrencyCostsNotMergedAsUSD reproduces review2 blocker #8:
// personal summary must not silently SUM multi-currency costs and label USD.
func TestReview2MultiCurrencyCostsNotMergedAsUSD(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := "usr_review2_fx"
	seedTestUser(t, db, st, userID, "review2_fx", "Review2 FX", "review2-fx@tokendance.dev", true, now)

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

	r := domain.TimeRange{Key: domain.TimeRange30d, From: now.AddDate(0, 0, -30), To: now, Timezone: "UTC"}
	sum, err := st.Analytics().GetPersonalSummary(ctx, userID, r)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Metrics.EstimatedCost.Amount != nil {
		t.Fatalf("multi-currency must not set scalar amount (would be a silent merge), got %+v", sum.Metrics.EstimatedCost)
	}
	if sum.Metrics.EstimatedCost.Currency != nil {
		t.Fatalf("multi-currency must not label scalar currency (esp. not USD), got %+v", sum.Metrics.EstimatedCost)
	}
	if !sum.Metrics.EstimatedCost.Supported {
		t.Fatalf("multi-currency costs should remain supported via breakdown: %+v", sum.Metrics.EstimatedCost)
	}
	if len(sum.Metrics.EstimatedCosts) != 2 {
		t.Fatalf("expected CNY+USD breakdown, got %+v", sum.Metrics.EstimatedCosts)
	}
	byCurr := map[string]string{}
	for _, c := range sum.Metrics.EstimatedCosts {
		if c.Currency == nil || c.Amount == nil {
			t.Fatalf("breakdown entry missing currency/amount: %+v", c)
		}
		byCurr[*c.Currency] = *c.Amount
	}
	if byCurr["USD"] != "1.00000000" || byCurr["CNY"] != "7.00000000" {
		t.Fatalf("unexpected per-currency amounts: %+v", byCurr)
	}
}

// TestReview2CacheHitRateUsesPairedEligibleColumns reproduces review2 blocker #9:
// cache hit rate must use cache_eligible_* paired samples, return null when denom
// is 0, and expose coverage.
func TestReview2CacheHitRateUsesPairedEligibleColumns(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	userID := "usr_review2_cache"
	seedTestUser(t, db, st, userID, "review2_cache", "Review2 Cache", "review2-cache@tokendance.dev", true, now)

	installationID := "ins_" + userID
	ensureTestInstallation(t, db, userID, installationID, now)
	bucket, err := domain.DayBucketStartMs("2026-08-29")
	if err != nil {
		t.Fatal(err)
	}
	nowMs := now.UnixMilli()

	// Paired samples: 90/100 + 10/1000 → 100/1100 ≈ 0.091
	_, err = db.Exec(`
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start,
			harness_id, model_key, exact_token_total, derived_token_total,
			input_context_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
			cache_eligible_input_tokens, cache_eligible_read_tokens, cache_pair_known_count,
			model_request_count, usage_observed_count, token_total_known_count,
			input_context_known_count, cache_read_known_count, metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, 'codex', 1, 1100, 0, 1100, 0, 100, 0, 0, 1100, 100, 2, 2, 2, 2, 2, 2, 1)`,
		nowMs, nowMs, userID, installationID, bucket)
	if err != nil {
		t.Fatalf("seed paired: %v", err)
	}
	// Unpaired pollution: large unpaired cache_read must not enter the rate.
	_, err = db.Exec(`
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start,
			harness_id, model_key, exact_token_total, derived_token_total,
			input_context_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens,
			cache_eligible_input_tokens, cache_eligible_read_tokens, cache_pair_known_count,
			model_request_count, usage_observed_count, token_total_known_count,
			cache_read_known_count, metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, 'cursor', 1, 0, 0, 0, 0, 999999, 0, 0, 0, 0, 0, 1, 1, 0, 1, 1)`,
		nowMs, nowMs, userID, installationID, bucket)
	if err != nil {
		t.Fatalf("seed unpaired: %v", err)
	}

	r := domain.TimeRange{Key: domain.TimeRange30d, From: now.AddDate(0, 0, -30), To: now, Timezone: "UTC"}
	sum, err := st.Analytics().GetPersonalSummary(ctx, userID, r)
	if err != nil {
		t.Fatal(err)
	}
	hit := sum.Metrics.CacheHitRate
	if !hit.Supported || hit.Value == nil || *hit.Value != "0.091" {
		t.Fatalf("expected paired cache hit 0.091, got %+v", hit)
	}
	if hit.Coverage == nil || *hit.Coverage != domain.MetricCoveragePartial {
		t.Fatalf("expected partial coverage (2 paired of 3 observed), got %+v", hit)
	}
	if hit.KnownCount == nil || *hit.KnownCount != 2 || hit.ObservedCount == nil || *hit.ObservedCount != 3 {
		t.Fatalf("expected known=2 observed=3, got %+v", hit)
	}

	// Empty range / no eligible denominator → null value.
	emptyUser := "usr_review2_cache_empty"
	seedTestUser(t, db, st, emptyUser, "review2_empty", "Review2 Empty", "review2-empty@tokendance.dev", true, now)
	emptyInstall := "ins_" + emptyUser
	ensureTestInstallation(t, db, emptyUser, emptyInstall, now)
	_, err = db.Exec(`
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, user_id, installation_id, grain, bucket_start,
			harness_id, model_key, exact_token_total, derived_token_total,
			input_context_tokens, cache_read_tokens,
			cache_eligible_input_tokens, cache_eligible_read_tokens, cache_pair_known_count,
			model_request_count, usage_observed_count, token_total_known_count,
			metric_semantics_version
		) VALUES (?, ?, ?, ?, 'day', ?, 'codex', 1, 10, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1)`,
		nowMs, nowMs, emptyUser, emptyInstall, bucket)
	if err != nil {
		t.Fatalf("seed empty denom: %v", err)
	}
	emptySum, err := st.Analytics().GetPersonalSummary(ctx, emptyUser, r)
	if err != nil {
		t.Fatal(err)
	}
	if emptySum.Metrics.CacheHitRate.Value != nil {
		t.Fatalf("denom 0 must return null cacheHitRate, got %+v", emptySum.Metrics.CacheHitRate)
	}
	if emptySum.Metrics.CacheHitRate.Coverage == nil || *emptySum.Metrics.CacheHitRate.Coverage != domain.MetricCoverageNone {
		t.Fatalf("expected coverage none for denom 0, got %+v", emptySum.Metrics.CacheHitRate)
	}
}
