package teams

import (
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestAssembleHistoricalShareDenominator(t *testing.T) {
	from := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)
	d1 := "2026-09-01"
	a, b := "tmb_a", "tmb_b"
	rows := []domain.TeamAnalysisRow{
		{MembershipID: &a, MetricDate: &d1, TokenExactTotal: "60", TokenDerivedTotal: "0"},
		{MembershipID: &b, MetricDate: &d1, TokenExactTotal: "30", TokenDerivedTotal: "0"},
		{MembershipID: nil, MetricDate: &d1, TokenExactTotal: "10", TokenDerivedTotal: "0"},
	}
	members := []domain.TeamMembership{
		{MembershipID: a, UserID: "usr_a", TeamID: "tem_x"},
		{MembershipID: b, UserID: "usr_b", TeamID: "tem_x"},
	}
	users := []domain.User{{UserID: "usr_a", DisplayName: "A"}, {UserID: "usr_b", DisplayName: "B"}}
	team := &domain.Team{TeamID: "tem_x", TimezoneName: "UTC"}
	snap := &domain.TeamAnalysisSnapshot{SnapshotID: "tas_x", AuthRevision: 1, RuleVersion: "6", AsOf: from, Status: domain.SnapshotReady}
	dto := assembleAnalysis(team, snap, rows, members, users, 2, from, to, domain.TeamAnalysisFilters{}, AnalysisQuery{})
	if dto.Summary.Tokens.Value != "100" {
		t.Fatalf("total %s", dto.Summary.Tokens.Value)
	}
	if len(dto.Contributions.Items) != 2 {
		t.Fatalf("ranking must exclude historical, got %d", len(dto.Contributions.Items))
	}
	share := tokenShare("60", "100")
	if share == nil || *share != "60" {
		t.Fatalf("share of 60/100 want 60 (percent helper), got %v", share)
	}
	hist := tokenShare("10", "100")
	if hist == nil || *hist != "10" {
		t.Fatalf("historical share %v", hist)
	}
}

func TestStaticTenMetricsCostsCacheSkills(t *testing.T) {
	usd := "USD"
	agent := "codex"
	cacheJSON := []byte(`{"schemaVersion":1,"inputContextTokens":{"sum":"1000","known":"2","observed":"2"},"outputTokens":{"sum":"40","known":"2","observed":"2"},"cachePairs":{"input":"1000","read":"90","known":"2","observed":"2"}}`)
	actJSON := []byte(`{"schemaVersion":1,"generatedCodeLines":{"sum":"10","known":"1","observed":"1"},"activeDurationMs":{"sum":"1000","known":"1","observed":"1"},"messageCount":{"sum":"4","known":"4","observed":"4"},"userMessageCount":{"sum":"2","known":"2","observed":"2"}}`)
	rows := []domain.TeamAnalysisRow{
		{TokenExactTotal: "100", TokenDerivedTotal: "0", ResourcesJSON: cacheJSON},
		{EstimatedCostAmount: "1.25000000", Currency: &usd},
		{SkillUseCount: "6", AgentID: &agent},
		{SkillUseCount: "4", AgentID: &agent},
		{ActivityJSON: actJSON},
	}
	got := staticTenMetrics(rows, domain.DecimalMetric{Value: "100", State: domain.MetricAvailable})
	if got["cacheHitRate"].Value != "0.0900" {
		t.Fatalf("M01 cache hit want 0.0900 got %+v", got["cacheHitRate"])
	}
	if got["estimatedCosts"].Value != "1.25000000" {
		t.Fatalf("estimatedCosts %+v", got["estimatedCosts"])
	}
	if got["inputContextTokens"].Value != "1000" || got["outputTokens"].Value != "40" {
		t.Fatalf("resource metrics %+v %+v", got["inputContextTokens"], got["outputTokens"])
	}
	if got["generatedCodeLines"].Value != "10" || got["messageCount"].Value != "4" || got["userMessageCount"].Value != "2" {
		t.Fatalf("activity metrics %+v", got)
	}
	if got["skillUseCount"].Value != "10" {
		t.Fatalf("skillUseCount %+v", got["skillUseCount"])
	}
	if got["tokensPerCodeLine"].Value != "10" {
		t.Fatalf("tokensPerCodeLine %+v", got["tokensPerCodeLine"])
	}
	skills := assembleStaticSkills(rows, nil, nil, AnalysisQuery{})
	if skills == nil || len(skills.Items) != 1 {
		t.Fatalf("skills grouping %+v", skills)
	}
}

func TestAssembleStaticSkillsRanksPublicNameAndMembers(t *testing.T) {
	a, b := "tmb_a", "tmb_b"
	agent := "codex"
	id1, id2 := int64(11), int64(12)
	rows := []domain.TeamAnalysisRow{
		{MembershipID: &a, AgentID: &agent, SkillID: &id1, SkillPublicName: "code-review", SkillUseCount: "8"},
		{MembershipID: &b, AgentID: &agent, SkillID: &id1, SkillPublicName: "code-review", SkillUseCount: "2"},
		{MembershipID: &a, AgentID: &agent, SkillID: &id2, SkillPublicName: "frontend-design", SkillUseCount: "3"},
	}
	members := []domain.TeamMembership{
		{MembershipID: a, UserID: "usr_a"},
		{MembershipID: b, UserID: "usr_b"},
	}
	users := []domain.User{{UserID: "usr_a", DisplayName: "Ada"}, {UserID: "usr_b", DisplayName: "Bo"}}
	got := assembleStaticSkills(rows, members, users, AnalysisQuery{})
	if got == nil || len(got.Items) != 2 {
		t.Fatalf("want 2 skill groups, got %+v", got)
	}
	if got.Items[0]["label"] != "code-review" || got.Items[0]["useCount"] != "10" {
		t.Fatalf("top skill %+v", got.Items[0])
	}
	if got.Items[0]["memberCount"] != "2" {
		t.Fatalf("memberCount %+v", got.Items[0])
	}
	dist, _ := got.Items[0]["members"].([]map[string]any)
	if len(dist) != 2 || dist[0]["displayName"] != "Ada" {
		t.Fatalf("distribution %+v", dist)
	}
}
