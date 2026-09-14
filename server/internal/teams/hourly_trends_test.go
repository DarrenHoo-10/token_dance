package teams

import (
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestTodayTrendsUseActualHourBucketsAndHourlyCodeDenominator(t *testing.T) {
	from := time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC) // Shanghai midnight.
	to := from.Add(24 * time.Hour)
	day, member := "2026-09-15", "m1"
	rows := []domain.TeamAnalysisRow{
		{MembershipID: &member, MetricDate: &day, MetricKind: "usage", TokenExactTotal: "999", TokenDerivedTotal: "0",
			HourlyJSON: []byte(`{"schemaVersion":1,"coverage":"partial","unbucketedTokenTotal":"699","buckets":[{"startMs":"1789401600000","exact":"100","derived":"0"},{"startMs":"1789405200000","exact":"200","derived":"0"}]}`)},
		{MembershipID: &member, MetricDate: &day, MetricKind: "activity", TokenExactTotal: "0", TokenDerivedTotal: "0",
			ActivityJSON: []byte(`{"schemaVersion":1,"generatedCodeLines":{"sum":"30"}}`),
			HourlyJSON:   []byte(`{"schemaVersion":1,"coverage":"complete","unbucketedTokenTotal":"0","buckets":[{"startMs":"1789401600000","exact":"0","derived":"0","codeLines":"10"},{"startMs":"1789405200000","exact":"0","derived":"0","codeLines":"20"}]}`)},
	}
	// Verify the test fixture really refers to the first two UTC hours of the range.
	if time.UnixMilli(1789401600000).UTC() != from {
		t.Fatal("invalid hour fixture")
	}
	teamU, teamE, memberU, memberE, partial := hourlyAnalysisTrends(rows, from, to, from.Add(90*time.Minute))
	if !partial {
		t.Fatal("unbucketed historical tokens should mark hourly trend partial")
	}
	if len(teamU) != 2 || teamU[0].Date != "2026-09-14T16:00:00Z" || teamU[0].Tokens.Value != "100" || teamU[1].Tokens.Value != "200" {
		t.Fatalf("hourly token trend: %+v", teamU)
	}
	if len(teamE) != 2 || teamE[0].Tokens.Value != "10" || teamE[1].Tokens.Value != "10" {
		t.Fatalf("hourly efficiency: %+v", teamE)
	}
	if memberU[member][0].Tokens.Value != "100" || memberE[member][1].Tokens.Value != "10" {
		t.Fatalf("member hours: %+v %+v", memberU, memberE)
	}
	// The unbucketed 699 tokens must remain in the day total, never be
	// assigned to an arbitrary hour.
	if teamU[0].Tokens.Value == "999" {
		t.Fatal("unbucketed day total leaked into hour")
	}
	team := &domain.Team{TimezoneName: "Asia/Shanghai"}
	snap := &domain.TeamAnalysisSnapshot{AsOf: from.Add(90 * time.Minute)}
	members := []domain.TeamMembership{{MembershipID: member, UserID: "u1"}}
	users := []domain.User{{UserID: "u1", DisplayName: "Ada"}}
	dto := assembleAnalysis(team, snap, rows, members, users, 1, from, to, domain.TeamAnalysisFilters{}, AnalysisQuery{RangeKey: "today"})
	if dto.TrendGrain != "hour" || len(dto.Trend) != 2 || dto.EfficiencyTrend[0].Tokens.Value != "10" {
		t.Fatalf("today analysis did not select hour grain: %+v", dto)
	}
	item := dto.Contributions.Items[0]
	if item["activeDays"] != "1" || item["trend"].([]domain.TeamTrendPoint)[1].Tokens.Value != "200" {
		t.Fatalf("member day count or hourly trend: %+v", item)
	}
}
