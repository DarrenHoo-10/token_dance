package teams

import (
	"testing"
	"time"
	"tokendance/internal/domain"
)

func TestMemberInsightsDailyPrecisionAndCalendar(t *testing.T) {
	loc, _ := time.LoadLocation("America/New_York")
	from := time.Date(2026, 3, 7, 0, 0, 0, 0, loc)
	mem, other, day, agent := "m1", "m2", "2026-03-09", "codex"
	rows := []domain.TeamAnalysisRow{
		{MembershipID: &mem, MetricDate: &day, AgentID: &agent, TokenExactTotal: "9007199254740993", TokenDerivedTotal: "7"},
		{MembershipID: &mem, MetricDate: &day, AgentID: &agent, TokenExactTotal: "10"},
		{MembershipID: &other, MetricDate: &day, TokenExactTotal: "30"},
	}
	members := []domain.TeamMembership{{MembershipID: mem, UserID: "u1"}, {MembershipID: other, UserID: "u2"}}
	users := []domain.User{{UserID: "u1", DisplayName: "Ada"}, {UserID: "u2", DisplayName: "Bo"}}
	snap := &domain.TeamAnalysisSnapshot{AsOf: from.AddDate(0, 0, 2).Add(time.Hour)}
	run := func(filters domain.TeamAnalysisFilters) *AnalysisDTO {
		return assembleAnalysis(&domain.Team{TimezoneName: "America/New_York"}, snap, rows, members, users, 2, from, from.AddDate(0, 0, 100), filters, AnalysisQuery{Limit: 1, Collection: "contributions"})
	}
	got := run(domain.TeamAnalysisFilters{})
	item := got.Contributions.Items[0]
	points := item["trend"].([]domain.TeamTrendPoint)
	if len(points) != 3 || points[0].Date != "2026-03-07" || points[1].Date != "2026-03-08" || points[1].Tokens.Value != "0" || points[2].Tokens.Value != "9007199254741010" {
		t.Fatalf("precision, DST calendar, zero fill or asOf: %+v", points)
	}
	if len(got.Contributions.Items) != 1 || got.Contributions.NextCursor == nil {
		t.Fatalf("pagination: %+v", got.Contributions)
	}
	if *item["share"].(*string) != *tokenShare("9007199254741010", "9007199254741040") {
		t.Fatalf("share must use the entire team denominator: %+v", item)
	}
	filtered := run(domain.TeamAnalysisFilters{Agent: &agent})
	if filtered.Summary.Tokens.Value != "9007199254741010" || *filtered.Contributions.Items[0]["share"].(*string) != *tokenShare("1", "1") {
		t.Fatalf("filtered contribution: %+v", filtered)
	}
	snap.AsOf = time.Time{}
	if len(run(domain.TeamAnalysisFilters{}).Contributions.Items[0]["trend"].([]domain.TeamTrendPoint)) != 90 {
		t.Fatal("missing 90 day cap")
	}
}

func TestMemberInsightsFiltersIdentityBeforePagination(t *testing.T) {
	ended := time.Now()
	members := []domain.TeamMembership{{MembershipID: "valid", UserID: "u1"}, {MembershipID: "ended", UserID: "u1", EndedAt: &ended}, {MembershipID: "unknown-user", UserID: "u2"}}
	users := []domain.User{{UserID: "u1", DisplayName: "Ada"}}
	page := pageContributions([]kv{{Key: "orphan", Value: "100"}, {Key: "ended", Value: "90"}, {Key: "unknown-user", Value: "80"}, {Key: "valid", Value: "1"}}, members, users, true, "", 1)
	if len(page.Items) != 1 || page.Items[0]["membershipId"] != "valid" || page.Items[0]["rank"] != "1" || page.NextCursor != nil {
		t.Fatalf("identity pagination: %+v", page)
	}
}
