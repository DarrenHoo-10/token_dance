package mysql

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestDimensionIDTreatsBlankAndAllAsUnfiltered(t *testing.T) {
	if _, ok := dimensionID(nil); ok {
		t.Fatal("nil filter must be unfiltered")
	}
	blank, all := "  ", "all"
	if _, ok := dimensionID(&blank); ok {
		t.Fatal("blank filter must be unfiltered")
	}
	if _, ok := dimensionID(&all); ok {
		t.Fatal("all filter must be unfiltered")
	}
	codex := " codex "
	got, ok := dimensionID(&codex)
	if !ok || got != "codex" {
		t.Fatalf("want codex, got %q ok=%v", got, ok)
	}
}

func TestPersonalSliceFollowsRangeHarnessAndModel(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 15, 30, 0, 0, domain.DayTZ)
	userID := "usr_slice_filter"
	seedTestUser(t, db, st, userID, "slice_filter", "Slice Filter", "slice-filter@tokendance.dev", true, now)
	installID := "ins_" + userID
	ensureTestInstallation(t, db, userID, installID, now)
	nowMs := now.UnixMilli()

	ensureModel := func(provider, model string) int64 {
		t.Helper()
		res, err := db.ExecContext(ctx, `
			INSERT INTO telemetry_models (created_at, updated_at, provider_id, model_id)
			VALUES (?, ?, ?, ?)`, nowMs, nowMs, provider, model)
		if err != nil {
			t.Fatalf("insert model %s/%s: %v", provider, model, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	gpt := ensureModel("openai", "gpt-4o")
	claude := ensureModel("anthropic", "claude-3-7-sonnet")

	insertModel := func(grain string, bucket int64, harness string, modelKey, tokens int64) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO telemetry_model_metrics (
				created_at, updated_at, installation_id, grain, bucket_start,
				harness_id, model_key, exact_token_total, derived_token_total,
				input_context_tokens, output_tokens,
				model_request_count, usage_observed_count, token_total_known_count,
				input_context_known_count, output_known_count, metric_semantics_version
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 1, 1, 1, 1, 1, 1)`,
			nowMs, nowMs, installID, grain, bucket, harness, modelKey, tokens, tokens, tokens/5)
		if err != nil {
			t.Fatalf("insert model metric %s %s: %v", grain, harness, err)
		}
	}
	insertHarness := func(bucket int64, harness string, lines, messages, userMessages int64) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO telemetry_harness_metrics (
				created_at, updated_at, installation_id, grain, bucket_start, harness_id,
				code_generated_lines, active_duration_ms, turn_started_count, turn_completed_count,
				user_turn_started_count, code_known_count, duration_known_count, message_known_count,
				metric_semantics_version
			) VALUES (?, ?, ?, 'day', ?, ?, ?, ?, ?, 0, ?, 1, 1, 1, 1)`,
			nowMs, nowMs, installID, bucket, harness, lines, lines*1000, messages, userMessages)
		if err != nil {
			t.Fatalf("insert harness %s: %v", harness, err)
		}
	}
	insertCost := func(bucket int64, harness string, modelKey, units int64) {
		t.Helper()
		_, err := db.ExecContext(ctx, `
			INSERT INTO telemetry_cost_metrics (
				created_at, updated_at, installation_id, grain, bucket_start,
				harness_id, model_key, currency, estimated_cost_units, estimated_request_count,
				cost_known_count, metric_semantics_version
			) VALUES (?, ?, ?, 'day', ?, ?, ?, 'USD', ?, 1, 1, 1)`,
			nowMs, nowMs, installID, bucket, harness, modelKey, units)
		if err != nil {
			t.Fatalf("insert cost %s: %v", harness, err)
		}
	}

	day29, err := domain.DayBucketStartMs("2026-08-29")
	if err != nil {
		t.Fatal(err)
	}
	day01, err := domain.DayBucketStartMs("2026-08-01")
	if err != nil {
		t.Fatal(err)
	}
	insertModel("day", day29, "codex", gpt, 100)
	insertModel("day", day29, "cursor", claude, 400)
	insertModel("day", day01, "opencode", gpt, 900)
	insertHarness(day29, "codex", 10, 2, 1)
	insertHarness(day29, "cursor", 40, 8, 3)
	insertHarness(day01, "opencode", 90, 9, 4)
	insertCost(day29, "codex", gpt, 100000000)
	insertCost(day29, "cursor", claude, 300000000)
	insertCost(day01, "opencode", gpt, 900000000)

	pubName := "Review Skill"
	res, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_skills (created_at, updated_at, installation_id, skill_key, public_name)
		VALUES (?, ?, ?, UNHEX(SHA2('slice-skill', 256)), ?)`, nowMs, nowMs, installID, pubName)
	if err != nil {
		t.Fatal(err)
	}
	skillID, _ := res.LastInsertId()
	for _, row := range []struct {
		bucket  int64
		harness string
		uses    int64
	}{{day29, "codex", 4}, {day29, "cursor", 7}} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO telemetry_skill_metrics (
				created_at, updated_at, installation_id, grain, bucket_start,
				harness_id, skill_id, use_count, exact_use_count, success_count, failure_count,
				duration_ms, metric_semantics_version
			) VALUES (?, ?, ?, 'day', ?, ?, ?, ?, ?, ?, 0, 10, 1)`,
			nowMs, nowMs, installID, row.bucket, row.harness, skillID, row.uses, row.uses, row.uses); err != nil {
			t.Fatal(err)
		}
	}

	fromHour, toHour := domain.Rolling24HourBuckets(now)
	insertModel(domain.TelemetryGrainHour, toHour.UnixMilli(), "cursor", gpt, 50)
	insertModel(domain.TelemetryGrainHour, fromHour.Add(-time.Hour).UnixMilli(), "opencode", claude, 9)

	end := now
	r7 := domain.TimeRange{Key: domain.TimeRange7d, From: time.Date(2026, 8, 24, 0, 0, 0, 0, domain.DayTZ), To: end, Timezone: domain.DayTZName}
	r30 := domain.TimeRange{Key: domain.TimeRange30d, From: time.Date(2026, 8, 1, 0, 0, 0, 0, domain.DayTZ), To: end, Timezone: domain.DayTZName}
	rToday := domain.TimeRange{Key: domain.TimeRangeToday, From: fromHour, To: toHour, Timezone: domain.DayTZName}
	analytics := st.Analytics()

	weekOpts, err := analytics.GetFilterOptionsInRange(ctx, userID, r7)
	if err != nil {
		t.Fatal(err)
	}
	if got := joinIDs(weekOpts.Agents); got != "codex,cursor" {
		t.Fatalf("7d harnesses: %s", got)
	}
	if got := joinIDs(weekOpts.Models); got != "claude-3-7-sonnet,gpt-4o" {
		t.Fatalf("7d models: %s", got)
	}
	monthOpts, err := analytics.GetFilterOptionsInRange(ctx, userID, r30)
	if err != nil {
		t.Fatal(err)
	}
	if got := joinIDs(monthOpts.Agents); got != "codex,cursor,opencode" {
		t.Fatalf("30d harnesses: %s", got)
	}
	todayOpts, err := analytics.GetFilterOptionsInRange(ctx, userID, rToday)
	if err != nil {
		t.Fatal(err)
	}
	if got := joinIDs(todayOpts.Agents); got != "cursor" {
		t.Fatalf("today harnesses: %s", got)
	}
	if got := joinIDs(todayOpts.Models); got != "gpt-4o" {
		t.Fatalf("today models: %s", got)
	}

	all, err := analytics.GetPersonalSummary(ctx, userID, r7)
	if err != nil {
		t.Fatal(err)
	}
	if metricValue(t, all.Metrics.TotalTokens) != "500" || metricValue(t, all.Metrics.GeneratedCodeLines) != "50" || metricValue(t, all.Metrics.MessageCount) != "10" {
		t.Fatalf("unfiltered 7d summary: tokens=%s lines=%s messages=%s", metricValue(t, all.Metrics.TotalTokens), metricValue(t, all.Metrics.GeneratedCodeLines), metricValue(t, all.Metrics.MessageCount))
	}

	codex := "codex"
	byAgent, err := analytics.GetPersonalSummaryFiltered(ctx, userID, r7, &codex, nil)
	if err != nil {
		t.Fatal(err)
	}
	if metricValue(t, byAgent.Metrics.TotalTokens) != "100" || metricValue(t, byAgent.Metrics.GeneratedCodeLines) != "10" || metricValue(t, byAgent.Metrics.MessageCount) != "2" || metricValue(t, byAgent.Metrics.UserMessageCount) != "1" {
		t.Fatalf("codex summary: %+v", byAgent.Metrics)
	}
	if byAgent.Metrics.EstimatedCost.Amount == nil || *byAgent.Metrics.EstimatedCost.Amount != "1.00000000" {
		t.Fatalf("codex cost: %+v", byAgent.Metrics.EstimatedCost)
	}

	gptID := "gpt-4o"
	byModel, err := analytics.GetPersonalSummaryFiltered(ctx, userID, r7, nil, &gptID)
	if err != nil {
		t.Fatal(err)
	}
	if metricValue(t, byModel.Metrics.TotalTokens) != "100" || metricValue(t, byModel.Metrics.GeneratedCodeLines) != "10" || metricValue(t, byModel.Metrics.MessageCount) != "2" || metricValue(t, byModel.Metrics.UserMessageCount) != "1" || metricValue(t, byModel.Metrics.ActiveDurationMs) != "10000" {
		t.Fatalf("gpt-4o exclusive harness summary: %+v", byModel.Metrics)
	}
	if byModel.Metrics.TokensPerCodeLine.Value == nil || *byModel.Metrics.TokensPerCodeLine.Value != "10.00" {
		t.Fatalf("gpt-4o tokens per line: %+v", byModel.Metrics.TokensPerCodeLine)
	}
	if byModel.Metrics.EstimatedCost.Amount == nil || *byModel.Metrics.EstimatedCost.Amount != "1.00000000" {
		t.Fatalf("gpt-4o cost: %+v", byModel.Metrics.EstimatedCost)
	}

	breakdown, err := analytics.GetAgentBreakdownFiltered(ctx, userID, r7, nil, &gptID)
	if err != nil {
		t.Fatal(err)
	}
	if len(breakdown.Items) != 1 || breakdown.Items[0].Key != "codex" || breakdown.Items[0].TokenTotal != "100" || breakdown.Items[0].Percentage != 100 {
		t.Fatalf("model breakdown: %+v", breakdown.Items)
	}
	cursor := "cursor"
	cursorBreakdown, err := analytics.GetAgentBreakdownFiltered(ctx, userID, r7, &cursor, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cursorBreakdown.Items) != 1 || cursorBreakdown.Items[0].Key != "cursor" || cursorBreakdown.Items[0].TokenTotal != "400" {
		t.Fatalf("cursor breakdown: %+v", cursorBreakdown.Items)
	}

	skills, err := analytics.GetSkillRankingFiltered(ctx, userID, r7, &cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills.Skills) != 1 || skills.Skills[0].UseCount != "7" || skills.Skills[0].SkillPublicName != "Review Skill" {
		t.Fatalf("cursor skills: %+v", skills.Skills)
	}
	allSkills, err := analytics.GetSkillRanking(ctx, userID, r7)
	if err != nil {
		t.Fatal(err)
	}
	if len(allSkills.Skills) != 1 || allSkills.Skills[0].UseCount != "11" {
		t.Fatalf("unfiltered skills: %+v", allSkills.Skills)
	}

	extra := ensureModel("openai", "extra")
	insertModel("day", day29, "pi", gpt, 50)
	insertModel("day", day29, "pi", extra, 20)
	insertHarness(day29, "pi", 99, 5, 2)
	mixed, err := analytics.GetPersonalSummaryFiltered(ctx, userID, r7, nil, &gptID)
	if err != nil {
		t.Fatal(err)
	}
	if metricValue(t, mixed.Metrics.TotalTokens) != "150" || metricValue(t, mixed.Metrics.GeneratedCodeLines) != "10" || metricValue(t, mixed.Metrics.MessageCount) != "2" {
		t.Fatalf("mixed harness lines must stay with the exclusive harness: %+v", mixed.Metrics)
	}
	if mixed.Metrics.TokensPerCodeLine.Value == nil || *mixed.Metrics.TokensPerCodeLine.Value != "10.00" {
		t.Fatalf("tokens per line must ignore the mixed harness tokens: %+v", mixed.Metrics.TokensPerCodeLine)
	}
	extraID := "extra"
	onlyMixed, err := analytics.GetPersonalSummaryFiltered(ctx, userID, r7, nil, &extraID)
	if err != nil {
		t.Fatal(err)
	}
	if metricValue(t, onlyMixed.Metrics.TotalTokens) != "20" {
		t.Fatalf("extra tokens: %s", metricValue(t, onlyMixed.Metrics.TotalTokens))
	}
	for _, metric := range []domain.MetricBigInt{onlyMixed.Metrics.GeneratedCodeLines, onlyMixed.Metrics.MessageCount, onlyMixed.Metrics.UserMessageCount, onlyMixed.Metrics.ActiveDurationMs} {
		if metric.Supported || metric.Value != nil {
			t.Fatalf("mixed-only model must not reuse harness activity: %+v", metric)
		}
	}
	if onlyMixed.Metrics.TokensPerCodeLine.Supported {
		t.Fatalf("mixed-only tokens per line: %+v", onlyMixed.Metrics.TokensPerCodeLine)
	}
}

func joinIDs(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ","
		}
		out += value
	}
	return out
}

func metricValue(t *testing.T, metric domain.MetricBigInt) string {
	t.Helper()
	if !metric.Supported || metric.Value == nil {
		t.Fatalf("expected supported metric, got %+v", metric)
	}
	return *metric.Value
}
