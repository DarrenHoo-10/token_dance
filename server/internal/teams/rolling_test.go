package teams

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestRollingTeamRowsExcludeEarlierHoursAcrossMidnight(t *testing.T) {
	from := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC) // 18:00 in Shanghai.
	to := from.Add(24 * time.Hour)
	member, yesterday, today, agent := "m1", "2026-09-28", "2026-09-29", "codex"
	hourly := func(buckets ...rollingHourBucket) []byte {
		data, err := json.Marshal(rollingHourly{SchemaVersion: 1, Coverage: "complete", UnbucketedTokenTotal: "0", Buckets: buckets})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	bucket := func(at time.Time, exact, code string) rollingHourBucket {
		return rollingHourBucket{StartMs: strconv.FormatInt(at.UnixMilli(), 10), Exact: exact, Derived: "0", CodeLines: code}
	}
	rows := []domain.TeamAnalysisRow{
		{MembershipID: &member, MetricDate: &yesterday, AgentID: &agent, MetricKind: "usage", TokenExactTotal: "900", TokenDerivedTotal: "0",
			ResourcesJSON: []byte(`{"inputContextTokens":{"sum":"900"}}`),
			HourlyJSON:    hourly(bucket(from.Add(-time.Hour), "600", ""), bucket(from.Add(time.Hour), "300", ""))},
		{MembershipID: &member, MetricDate: &today, AgentID: &agent, MetricKind: "usage", TokenExactTotal: "50", TokenDerivedTotal: "0",
			HourlyJSON: hourly(bucket(from.Add(8*time.Hour), "50", ""))},
		{MembershipID: &member, MetricDate: &yesterday, AgentID: &agent, MetricKind: "activity", TokenExactTotal: "0", TokenDerivedTotal: "0",
			ActivityJSON: []byte(`{"generatedCodeLines":{"sum":"30"}}`),
			HourlyJSON:   hourly(bucket(from.Add(-time.Hour), "0", "20"), bucket(from.Add(time.Hour), "0", "10"))},
		{MembershipID: &member, MetricDate: &yesterday, MetricKind: "cost", TokenExactTotal: "0", TokenDerivedTotal: "0", ReportedCostAmount: "1.00"},
	}
	clipped, partial := ClipTeamRowsToRollingHours(rows, from, to)
	if partial || len(clipped) != 3 {
		t.Fatalf("unexpected rolling rows: partial=%v rows=%+v", partial, clipped)
	}
	snap := &domain.TeamAnalysisSnapshot{AsOf: to.Add(-time.Hour)}
	team := &domain.Team{TimezoneName: "Asia/Shanghai"}
	members := []domain.TeamMembership{{MembershipID: member, UserID: "u1"}}
	users := []domain.User{{UserID: "u1", DisplayName: "Ada"}}
	dto := assembleAnalysis(team, snap, clipped, members, users, 1, from, to, domain.TeamAnalysisFilters{}, AnalysisQuery{RangeKey: "today"})
	if dto.Summary.Tokens.Value != "350" || dto.Summary.ActiveMembers != "1" || dto.TrendGrain != "hour" {
		t.Fatalf("rolling totals: %+v", dto.Summary)
	}
	if got := staticTenMetrics(clipped, dto.Summary.Tokens); got["generatedCodeLines"].Value != "10" || got["inputContextTokens"].State != domain.MetricEmpty {
		t.Fatalf("whole-day fields leaked into rolling metrics: %+v", got)
	}
	if len(dto.Costs.Reported) != 0 || len(dto.Contributions.Items) != 1 || dto.Contributions.Items[0]["tokens"].(domain.DecimalMetric).Value != "350" {
		t.Fatalf("rolling breakdown leaked excluded hours: %+v", dto)
	}
}
