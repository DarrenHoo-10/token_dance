//go:build team_cr_probes

package worker

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"tokendance/internal/domain"
)

// These acceptance probes reproduce open findings in docs/tokendance-teams-cr-report.md.
// Run explicitly with -tags team_cr_probes; they should fail until those findings are fixed.
func TestTeamCR015V2UploadReachesTeamAnalysis(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx := context.Background()
	now := w.clk.Now()
	user, installation, team, membership := "usr_cr015", "ins_cr015", "tem_cr015", "tmb_cr015"
	seedAggUserInstall(t, st, user, installation, now.Add(-time.Hour))
	seedTeamGraph(t, st.DB(), user, team, membership, now.Add(-time.Hour))
	if _, err := st.DB().Exec(`INSERT INTO team_sharing_grants
		(grant_id, membership_id, dimension, starts_at, active_dimension)
		VALUES ('tgr_cr015', ?, 'base', ?, 'base')`, membership, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	commitAggEvents(t, st, user, installation, "cr015", now, makeAggEvent(t, "cr015", now.Add(-time.Minute).UnixMilli(), nil))
	if _, err := w.ProcessTelemetryAggregation(ctx); err != nil {
		t.Fatal(err)
	}
	var personalTokens string
	if err := st.DB().QueryRow(`SELECT CAST(SUM(exact_token_total) AS CHAR)
		FROM telemetry_model_metrics WHERE user_id = ? AND grain = 'day'`, user).Scan(&personalTokens); err != nil {
		t.Fatal(err)
	}
	if personalTokens != "10" {
		t.Fatalf("v2 ingestion prerequisite failed: personal tokens = %s", personalTokens)
	}
	if _, err := st.DB().Exec(`INSERT INTO team_analysis_snapshots
		(snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
		 rule_version, status, active_request_key, as_of, next_attempt_at, expires_at)
		VALUES ('tas_cr015', ?, ?, ?, 1, 0, '1', 'queued', 'cr015', ?, ?, ?)`, team,
		now.UTC().Format("2006-01-02"), now.AddDate(0, 0, 1).UTC().Format("2006-01-02"),
		now, now, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, err := w.ProcessTeamAnalysis(ctx); err != nil || n != 1 {
		t.Fatalf("analysis: processed=%d err=%v", n, err)
	}
	var teamTokens string
	if err := st.DB().QueryRow(`SELECT CAST(COALESCE(SUM(token_exact_total), 0) AS CHAR)
		FROM team_analysis_rows WHERE snapshot_id = 'tas_cr015'`).Scan(&teamTokens); err != nil {
		t.Fatal(err)
	}
	if teamTokens != personalTokens {
		t.Errorf("new upload has personal tokens=%s but team tokens=%s", personalTokens, teamTokens)
	}
	var revision uint64
	if err := st.DB().QueryRow(`SELECT source_revision FROM team_source_revisions WHERE team_id = ?`, team).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision == 0 {
		t.Error("new v2 upload did not advance the team's source revision")
	}
}

func TestTeamCR016CostBeforeSharingStartIsNotExposed(t *testing.T) {
	at := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	member := teamMemberSource{membershipID: "tmb_cr016", userID: "usr_cr016", joinedAt: at.Add(-time.Hour), accountOK: true}
	for _, dimension := range []domain.SharingDimension{domain.SharingBase, domain.SharingCost} {
		t.Run(string(dimension), func(t *testing.T) {
			grants := []teamGrantWindow{
				{dimension: string(domain.SharingBase), startsAt: at.Add(-time.Hour)},
				{dimension: string(domain.SharingCost), startsAt: at.Add(-time.Hour)},
			}
			for i := range grants {
				if grants[i].dimension == string(dimension) {
					grants[i].startsAt = at.Add(-time.Minute)
				}
			}
			usage := teamFactEvent{eventPK: 1, userID: member.userID, installationID: "ins_cr016", agentID: "codex",
				eventType: "model_usage_recorded", accuracy: "exact", occurredAt: at,
				sessionHash: []byte{1}, turnHash: []byte{2}, tokenTotal: sql.NullInt64{Int64: 10, Valid: true}}
			cost := usage
			cost.eventPK, cost.eventType, cost.occurredAt = 2, "cost_recorded", at.Add(-2*time.Minute)
			cost.costAmount = sql.NullString{String: "2.00000000", Valid: true}
			cost.costCurrency = sql.NullString{String: "USD", Valid: true}
			cost.costSource = sql.NullString{String: "provider_reported", Valid: true}
			for _, row := range buildMemberAnalysisRows(member, grants, []teamFactEvent{cost, usage}, time.UTC) {
				if row.reportedCost.Sign() != 0 {
					t.Errorf("cost outside %s sharing window leaked: %s USD", dimension, row.reportedCost.FloatString(8))
				}
			}
		})
	}
}
