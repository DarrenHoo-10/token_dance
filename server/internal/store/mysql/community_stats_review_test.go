package mysql

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/leaderboard"
	"tokendance/internal/store"
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

	res, err := leaderboard.NewService(st).GetCommunityStats(ctx, now, "today")
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

func TestCommunityModelAndSkillSharesFromDayGrain(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, domain.DayTZ)
	userID := "usr_community_shares"
	seedTestUser(t, db, st, userID, "community_shares", "Community Shares", "community-shares@tokendance.dev", true, now)
	installID := "ins_" + userID
	ensureTestInstallation(t, db, userID, installID, now)
	nowMs := now.UnixMilli()
	bucket, err := domain.DayBucketStartMs("2026-09-09")
	if err != nil {
		t.Fatal(err)
	}

	res, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_models (created_at, updated_at, provider_id, model_id)
		VALUES (?, ?, 'openai', 'gpt-5')`, nowMs, nowMs)
	if err != nil {
		t.Fatal(err)
	}
	modelKey, _ := res.LastInsertId()
	_, err = db.ExecContext(ctx, `
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, installation_id, grain, bucket_start,
			harness_id, model_key, exact_token_total, derived_token_total,
			model_request_count, usage_observed_count, token_total_known_count,
			metric_semantics_version
		) VALUES (?, ?, ?, 'day', ?, 'codex', ?, 80, 0, 1, 1, 1, 1)`,
		nowMs, nowMs, installID, bucket, modelKey)
	if err != nil {
		t.Fatal(err)
	}

	publicName := "review"
	publicKey := crypto.SHA256([]byte("community-public-skill"))
	privateKey := crypto.SHA256([]byte("community-private-skill"))
	pubSkill, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_skills (created_at, updated_at, installation_id, skill_key, public_name)
		VALUES (?, ?, ?, ?, ?)`, nowMs, nowMs, installID, publicKey[:], publicName)
	if err != nil {
		t.Fatal(err)
	}
	pubSkillID, _ := pubSkill.LastInsertId()
	privSkill, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_skills (created_at, updated_at, installation_id, skill_key, public_name)
		VALUES (?, ?, ?, ?, NULL)`, nowMs, nowMs, installID, privateKey[:])
	if err != nil {
		t.Fatal(err)
	}
	privSkillID, _ := privSkill.LastInsertId()
	_, err = db.ExecContext(ctx, `
		INSERT INTO telemetry_skill_metrics (
			created_at, updated_at, installation_id, grain, bucket_start,
			harness_id, skill_id, use_count, exact_use_count, success_count, failure_count,
			duration_ms, metric_semantics_version
		) VALUES (?, ?, ?, 'day', ?, 'codex', ?, 9, 9, 8, 1, 100, 1)`,
		nowMs, nowMs, installID, bucket, pubSkillID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO telemetry_skill_metrics (
			created_at, updated_at, installation_id, grain, bucket_start,
			harness_id, skill_id, use_count, exact_use_count, success_count, failure_count,
			duration_ms, metric_semantics_version
		) VALUES (?, ?, ?, 'day', ?, 'codex', ?, 4, 4, 4, 0, 40, 1)`,
		nowMs, nowMs, installID, bucket, privSkillID)
	if err != nil {
		t.Fatal(err)
	}

	models, err := st.CommunityStats().SumCommunityModelShares(ctx, "2026-09-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ModelID != "gpt-5" || models[0].Tokens != 80 {
		t.Fatalf("model shares: %+v", models)
	}
	skills, err := st.CommunityStats().SumCommunitySkillShares(ctx, "2026-09-09")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].SkillID != "review" || skills[0].Uses != 9 {
		t.Fatalf("skill shares should exclude unnamed skills: %+v", skills)
	}

	if err := st.CommunityStats().UpsertCommunityDailyStats(ctx, store.CommunityDailyTotals{
		MetricDate: "2026-09-09", TokensTotal: 80, ModelShares: models, SkillShares: skills, ComputedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := st.CommunityStats().GetCommunityDailyStats(ctx, "2026-09-09")
	if err != nil || got == nil {
		t.Fatalf("round-trip: %+v %v", got, err)
	}
	if len(got.ModelShares) != 1 || got.ModelShares[0].ModelID != "gpt-5" {
		t.Fatalf("persisted models: %+v", got.ModelShares)
	}
	if len(got.SkillShares) != 1 || got.SkillShares[0].SkillID != "review" {
		t.Fatalf("persisted skills: %+v", got.SkillShares)
	}

	stats, err := leaderboard.NewService(st).GetCommunityStats(ctx, now, "today")
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Models) != 1 || stats.Models[0].ModelID != "gpt-5" || stats.Models[0].Tokens == nil || *stats.Models[0].Tokens != "80" {
		t.Fatalf("API models: %+v", stats.Models)
	}
	if len(stats.Skills) != 1 || stats.Skills[0].SkillID != "review" || stats.Skills[0].Uses == nil || *stats.Skills[0].Uses != "9" {
		t.Fatalf("API skills: %+v", stats.Skills)
	}
}
