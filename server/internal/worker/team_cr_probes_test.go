package worker

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

// Regression coverage for CR-015 and CR-016 in docs/tokendance-teams-cr-report.md.
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
	commitAggEvents(t, st, user, installation, "cr015-retry", now, makeAggEvent(t, "cr015", now.Add(-time.Minute).UnixMilli(), nil))
	conflict := makeAggEvent(t, "cr015", now.Add(-time.Minute).UnixMilli(), func(e map[string]any) {
		e["payload"].(map[string]any)["usage"].(map[string]any)["token_total"] = "20"
	})
	result, err := st.Ingest().CommitTelemetryEventsV2(ctx, domain.TelemetryEventsV2Input{
		UserID: user, InstallationID: installation, BindingStatusVersion: 1, ReceivedAt: now,
		NonceHash: crypto.SHA256([]byte("cr015-conflict")), NonceExpiresAt: now.Add(time.Minute),
		Events: []v2.EventEnvelope{conflict},
	})
	if err != nil || len(result.Acks) != 1 || result.Acks[0].Result != v2.AckResultConflict {
		t.Fatalf("immutable fact content conflict: result=%+v err=%v", result, err)
	}
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
		VALUES ('tas_cr015', ?, ?, ?, 1, 0, '2', 'queued', 'cr015', ?, ?, ?)`, team,
		now.Add(-time.Hour).UTC().Format("2006-01-02"), now.AddDate(0, 0, 1).UTC().Format("2006-01-02"),
		now, now, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n, err := w.ProcessTeamAnalysis(ctx); err != nil || n != 2 {
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
	if revision != 1 {
		t.Errorf("accepted batch should advance source once; retry must not: got %d", revision)
	}
	if n, err := w.ProcessTeamAnalysis(ctx); err != nil || n != 0 {
		t.Fatalf("refresh must settle after catching up: processed=%d err=%v", n, err)
	}
	if _, err := st.DB().Exec(`UPDATE team_analysis_snapshots
		SET status = 'queued', rule_version = '1', active_request_key = 'cr015-legacy' WHERE snapshot_id = 'tas_cr015'`); err != nil {
		t.Fatal(err)
	}
	if n, err := w.ProcessTeamAnalysis(ctx); err != nil || n != 1 {
		t.Fatalf("old queued snapshot: processed=%d err=%v", n, err)
	}
	var status string
	if err := st.DB().QueryRow(`SELECT status FROM team_analysis_snapshots WHERE snapshot_id = 'tas_cr015'`).Scan(&status); err != nil || status != "obsolete" {
		t.Fatalf("old queued snapshot must be obsolete: status=%s err=%v", status, err)
	}
}

func TestTeamRevokedCostSharingIsNotExposed(t *testing.T) {
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
					grants[i].revokedAt = sql.NullTime{Time: at.Add(-time.Minute), Valid: true}
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
