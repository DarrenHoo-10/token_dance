package worker

import (
	"context"
	"encoding/csv"
	"math/big"
	"strings"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
)

func TestTeamLegacySummaryMySQL(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx, now := context.Background(), w.clk.Now()
	user, installation, team, membership := "usr_legacy_team", "ins_legacy_team", "tem_legacy_team", "tmb_legacy_team"
	seedAggUserInstall(t, st, user, installation, now)
	seedTeamGraph(t, st.DB(), user, team, membership, now)
	day := time.Date(now.Year(), now.Month(), now.Day()-2, 0, 0, 0, 0, time.UTC)
	for _, agent := range []string{"codex", "claude"} {
		if _, err := st.DB().Exec(`INSERT INTO daily_user_agent_metrics
			(metric_date,user_id,agent_id,exact_token_total,derived_token_total,estimated_token_total,model_request_count,cost_amount,aggregation_version,computed_at,updated_at)
			VALUES (?,?,?,100,50,999,7,99,2,?,?)`, day, user, agent, now, now); err != nil {
			t.Fatal(err)
		}
	}
	member := teamMemberSource{membershipID: membership, userID: user, joinedAt: now, accountOK: true}
	grants := []teamGrantWindow{{dimension: "base", startsAt: now}}
	read := func(from, to time.Time, gs []teamGrantWindow) []analysisAggRow {
		t.Helper()
		tx, err := st.DB().BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		rows, err := aggregateMemberAnalysis(ctx, tx, member, gs, from, to, now.Add(time.Hour), time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	check := func(rows []analysisAggRow, tokens string, events int64, legacy bool) {
		t.Helper()
		total, count := new(big.Int), new(big.Int)
		hasLegacy := false
		for _, row := range rows {
			total.Add(total, row.tokenExact).Add(total, row.tokenDerived)
			count.Add(count, row.usageEvents)
			hasLegacy = hasLegacy || row.legacyAggregate
			if row.reportedCost.Sign() != 0 || row.estimatedCost.Sign() != 0 {
				t.Fatal("invented cost provenance")
			}
			if deref(row.agentID) != unsharedClassificationBucket {
				t.Fatal("unshared classification leaked")
			}
		}
		if total.String() != tokens || count.Int64() != events || hasLegacy != legacy {
			t.Fatalf("tokens=%s events=%s legacy=%v", total, count, hasLegacy)
		}
	}
	rows := read(day, day.AddDate(0, 0, 1), grants)
	check(rows, "300", 0, true)
	if len(rows) != 1 {
		t.Fatal("unshared legacy tools must coalesce")
	}
	if len(read(day, now, nil)) != 0 {
		t.Fatal("base sharing required")
	}
	revoked := append([]teamGrantWindow(nil), grants...)
	revoked[0].revokedAt.Time, revoked[0].revokedAt.Valid = now, true
	if len(read(day, now, revoked)) != 0 {
		t.Fatal("revoked sharing leaked history")
	}
	if len(read(day.AddDate(0, 0, 1), day.AddDate(0, 0, 2), grants)) != 0 {
		t.Fatal("out-of-range history included")
	}
	// The v2 fact replaces only its source partition; another tool remains.
	commitAggEvents(t, st, user, installation, "legacy-v2", now, makeAggEvent(t, "legacy-v2", day.Add(12*time.Hour).UnixMilli(), nil))
	rows = read(day, day.AddDate(0, 0, 1), grants)
	check(rows, "160", 1, true)
	if len(rows) != 1 {
		t.Fatal("colliding legacy/v2 row keys were not merged")
	}
	// Snapshot persistence retains provenance without fake event counts.
	if _, _, err := st.Teams().GetOrQueueAnalysis(ctx, team, day, day.AddDate(0, 0, 1), 1, domain.TeamAnalysisRuleVersion, now); err != nil {
		t.Fatal(err)
	}
	w.clk.(*clock.MockClock).Set(time.Now().UTC().Add(time.Second))
	claim, err := w.claimTeamAnalysis(ctx)
	if err != nil || claim == nil {
		t.Fatalf("claim=%v err=%v", claim, err)
	}
	if err := w.writeTeamAnalysisRows(ctx, claim, rows); err != nil {
		t.Fatal(err)
	}
	saved, err := st.Teams().ListAnalysisRows(ctx, claim.snapshotID, claim.leaseGeneration)
	if err != nil || len(saved) != 1 || !saved[0].LegacyAggregate || saved[0].UsageEventCount != "1" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
	if ok, err := w.publishTeamAnalysis(ctx, claim, &teamAnalysisSource{capturedAuth: 1, capturedSource: 1}); err != nil || !ok {
		t.Fatalf("publish=%v err=%v", ok, err)
	}
	data, err := w.buildTeamExportCSV(ctx, &teamExportClaim{teamID: team, snapshotID: claim.snapshotID, authRevision: 1, kind: string(domain.TeamExportDaily)})
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
	if err != nil || len(records) != 2 || records[0][len(records[0])-1] != "data_source" || records[1][len(records[1])-1] != "includes_legacy_daily_utc" {
		t.Fatalf("export provenance missing: %s err=%v", data, err)
	}
	if _, err := st.DB().Exec(`UPDATE telemetry_events SET delete_at=? WHERE user_id=?`, now.UnixMilli(), user); err != nil {
		t.Fatal(err)
	}
	check(read(day, day.AddDate(0, 0, 1), grants), "150", 0, true)
	member.accountOK = false
	if len(read(day, now, grants)) != 0 {
		t.Fatal("inactive account leaked history")
	}
}

func TestTeamLegacySummaryLargeTotalsAndCalendarMySQL(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx, now := context.Background(), w.clk.Now()
	user, installation := "usr_legacy_large", "ins_legacy_large"
	seedAggUserInstall(t, st, user, installation, now)
	loc, _ := time.LoadLocation("Asia/Shanghai")
	localNow := now.In(loc)
	day := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	if _, err := st.DB().Exec(`INSERT INTO daily_user_agent_metrics
		(metric_date,user_id,agent_id,exact_token_total,derived_token_total,aggregation_version,computed_at,updated_at)
		VALUES (?,?,'codex',18446744073709551615,18446744073709551615,2,?,?)`, day.Format("2006-01-02"), user, now, now); err != nil {
		t.Fatal(err)
	}
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := readTeamLegacyRows(ctx, tx, teamMemberSource{userID: user, membershipID: "member", accountOK: true}, []teamGrantWindow{{dimension: "base"}, {dimension: "classification"}}, day, localNow, now)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	row := rows[0]
	if new(big.Int).Add(row.tokenExact, row.tokenDerived).String() != "36893488147419103230" || deref(row.metricDate) != day.Format("2006-01-02") || deref(row.agentID) != "codex" {
		t.Fatalf("UTC calendar labels or large totals lost: %+v", row)
	}
}
