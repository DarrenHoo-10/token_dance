package worker

import (
	"context"
	"encoding/csv"
	"math/big"
	"strings"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
	"tokendance/internal/store"
)

func TestTeamTelemetryUploadDuringSnapshotBuildMySQL(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx, now := context.Background(), w.clk.Now()
	user, installation, team, membership := "usr_team_midbuild", "ins_team_midbuild", "tem_team_midbuild", "tmb_team_midbuild"
	seedAggUserInstall(t, st, user, installation, now.Add(-time.Hour))
	seedTeamGraph(t, st.DB(), user, team, membership, now.Add(-time.Hour))
	if _, err := st.DB().Exec(`INSERT INTO team_sharing_grants
		(grant_id,membership_id,dimension,starts_at,active_dimension) VALUES ('tgr_midbuild',?,'base',?,'base')`, membership, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	commitAggEvents(t, st, user, installation, "midbuild-before", now, makeAggEvent(t, "midbuild-before", now.Add(-time.Minute).UnixMilli(), nil))
	_, _, err := st.Teams().GetOrQueueAnalysis(ctx, team, now.AddDate(0, 0, -1), now.AddDate(0, 0, 1), 1, domain.TeamAnalysisRuleVersion, now)
	if err != nil {
		t.Fatal(err)
	}
	w.clk.(*clock.MockClock).Set(time.Now().UTC().Add(time.Second))
	claim, err := w.claimTeamAnalysis(ctx)
	if err != nil || claim == nil {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	source, err := w.readTeamAnalysisSource(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	if source.sourceGrew || source.capturedSource != 1 {
		t.Fatalf("unexpected captured source: %+v", source)
	}
	// A late upload arrives after the repeatable-read snapshot has finished.
	commitAggEvents(t, st, user, installation, "midbuild-after", now, makeAggEvent(t, "midbuild-after", now.Add(-30*time.Second).UnixMilli(), nil))
	if err := w.writeTeamAnalysisRows(ctx, claim, source.rows); err != nil {
		t.Fatal(err)
	}
	published, err := w.publishTeamAnalysis(ctx, claim, source)
	if err != nil || !published || !source.sourceGrew {
		t.Fatalf("late upload must schedule refresh: published=%v sourceGrew=%v err=%v", published, source.sourceGrew, err)
	}
	if err := w.queueTeamAnalysisRefresh(ctx, claim, source.capturedAuth, source.capturedSource); err != nil {
		t.Fatal(err)
	}
	if n, err := w.ProcessTeamAnalysis(ctx); err != nil || n != 2 {
		t.Fatalf("catch-up refresh=%d err=%v", n, err)
	}
	if n, err := w.ProcessTeamAnalysis(ctx); err != nil || n != 0 {
		t.Fatalf("refresh did not settle: %d %v", n, err)
	}
	var total string
	if err := st.DB().QueryRow(`SELECT CAST(SUM(r.token_exact_total) AS CHAR)
		FROM team_analysis_rows r WHERE r.snapshot_id = (
			SELECT snapshot_id FROM team_analysis_snapshots WHERE team_id = ? AND source_revision = 2 AND status = 'ready'
			ORDER BY as_of DESC LIMIT 1)`, team).Scan(&total); err != nil || total != "20" {
		t.Fatalf("refreshed total=%s err=%v", total, err)
	}
}

func TestTeamTelemetrySharingCostExportMySQL(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx := context.Background()
	now := w.clk.Now()
	user, installation, team, membership := "usr_team_v2_flow", "ins_team_v2_flow", "tem_team_v2_flow", "tmb_team_v2_flow"
	seedAggUserInstall(t, st, user, installation, now.Add(-72*time.Hour))
	seedTeamGraph(t, st.DB(), user, team, membership, now.Add(-72*time.Hour))
	for _, dim := range []string{"base", "named", "classification", "cost"} {
		if _, err := st.DB().Exec(`INSERT INTO team_sharing_grants
			(grant_id, membership_id, dimension, starts_at, active_dimension) VALUES (?, ?, ?, ?, ?)`,
			"tgr_v2_"+dim, membership, dim, now.Add(-48*time.Hour), dim); err != nil {
			t.Fatal(err)
		}
	}
	cost := func(seed, scope, units, source, model string, at time.Time) v2.EventEnvelope {
		return makeAggEvent(t, seed, at.UnixMilli(), func(e map[string]any) {
			e["eventType"] = "cost_recorded"
			e["costScopeKey"] = string(b64url32Seed(scope))
			e["model"] = map[string]any{"providerId": "openai", "modelId": model}
			p := e["payload"].(map[string]any)
			delete(p, "usage")
			p["cost"] = map[string]any{"units": units, "currency": "USD", "source": source}
		})
	}
	usage := makeAggEvent(t, "team-v2-usage", now.Add(-2*time.Minute).UnixMilli(), func(e map[string]any) { e["costScopeKey"] = string(b64url32Seed("secret-scope")) })
	events := []v2.EventEnvelope{
		usage,
		cost("team-v2-private-cost", "secret-scope", "99900000000", "provider_reported", "private-model", now.Add(-60*time.Hour)),
		cost("team-v2-estimate", "bill-scope", "100000000", "calculated_price", "estimated-model", now.Add(-24*time.Hour)),
		cost("team-v2-bill", "bill-scope", "200000000", "provider_reported", "billed-model", now.Add(-time.Minute)),
	}
	commitAggEvents(t, st, user, installation, "team-flow-upload", now, events...)
	commitAggEvents(t, st, user, installation, "team-flow-retry", now, events...)
	var source uint64
	if err := st.DB().QueryRow(`SELECT source_revision FROM team_source_revisions WHERE team_id = ?`, team).Scan(&source); err != nil || source != 1 {
		t.Fatalf("batch/retry source=%d err=%v", source, err)
	}

	queue := func(from, to time.Time) *domain.TeamAnalysisSnapshot {
		t.Helper()
		var auth uint64
		if err := st.DB().QueryRow(`SELECT auth_revision FROM teams WHERE team_id = ?`, team).Scan(&auth); err != nil {
			t.Fatal(err)
		}
		snap, _, err := st.Teams().GetOrQueueAnalysis(ctx, team, from, to, auth, domain.TeamAnalysisRuleVersion, w.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		w.clk.(*clock.MockClock).Set(time.Now().UTC().Add(time.Minute))
		if _, err := w.ProcessTeamAnalysis(ctx); err != nil {
			t.Fatal(err)
		}
		snap, err = st.Teams().GetReadySnapshot(ctx, team, snap.SnapshotID)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	assertTotals := func(snap *domain.TeamAnalysisSnapshot, tokens, reported, estimated string) {
		t.Helper()
		rows, err := st.Teams().ListAnalysisRows(ctx, snap.SnapshotID, snap.PublishedGeneration)
		if err != nil {
			t.Fatal(err)
		}
		tok, rep, est := new(big.Int), new(big.Rat), new(big.Rat)
		for _, row := range rows {
			n, _ := new(big.Int).SetString(row.TokenExactTotal, 10)
			tok.Add(tok, n)
			r, _ := new(big.Rat).SetString(row.ReportedCostAmount)
			rep.Add(rep, r)
			e, _ := new(big.Rat).SetString(row.EstimatedCostAmount)
			est.Add(est, e)
		}
		if tok.String() != tokens || rep.RatString() != reported || est.RatString() != estimated {
			t.Fatalf("totals tokens=%s reported=%s estimated=%s", tok, rep.RatString(), est.RatString())
		}
	}
	from, to := now.AddDate(0, 0, -3), now.AddDate(0, 0, 1)
	snap := queue(from, to)
	// Current sharing includes history; the earlier independent bill is visible.
	assertTotals(snap, "10", "1001", "0")
	// Querying an earlier day must not bring back the replaced estimate.
	earlier := queue(from, domain.StartOfDay(now.Add(-time.Minute)))
	assertTotals(earlier, "0", "999", "0")

	claim := &teamExportClaim{teamID: team, snapshotID: snap.SnapshotID, requesterUserID: user, requesterMembershipID: membership, authRevision: snap.AuthRevision, kind: string(domain.TeamExportDaily)}
	auth, err := w.authorizeTeamExport(ctx, claim)
	if err != nil || !auth.ok {
		t.Fatalf("export authorization: %+v %v", auth, err)
	}
	payload, err := w.buildTeamExportCSV(ctx, claim)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(string(payload))).ReadAll()
	if err != nil || len(records) < 2 || !strings.Contains(string(payload), "999.") {
		t.Fatalf("CSV must include currently shared historical cost: rows=%d err=%v", len(records), err)
	}
	job, err := st.Teams().QueueTeamExportTx(ctx, store.QueueTeamExportTxInput{ActorUserID: user,
		Job:         domain.TeamExportJob{TeamID: team, SnapshotID: snap.SnapshotID, Kind: domain.TeamExportDaily},
		Idempotency: store.TeamsIdempotency{Scope: "export_create", KeyHash: crypto.SHA256([]byte("team-v2-export")), RequestHash: crypto.SHA256([]byte("team-v2-export-body"))}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := w.ProcessTeamExports(ctx); err != nil || n != 1 {
		t.Fatalf("export processing=%d err=%v", n, err)
	}
	job, err = st.Teams().GetExport(ctx, team, job.ExportID)
	if err != nil || job.Status != domain.TeamExportCompleted || job.ObjectKey == nil {
		t.Fatalf("completed export=%+v err=%v", job, err)
	}

	// Existing files and ready snapshots must be inaccessible after a rule bump.
	if _, err := st.DB().Exec(`UPDATE team_analysis_snapshots SET rule_version = '1' WHERE snapshot_id = ?`, snap.SnapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Teams().GetReadySnapshot(ctx, team, snap.SnapshotID); err == nil {
		t.Fatal("old snapshot remained readable")
	}
	if _, err := st.Teams().GetExport(ctx, team, job.ExportID); err == nil {
		t.Fatal("old export remained downloadable")
	}
	if auth, err := w.authorizeTeamExport(ctx, claim); err != nil || !auth.revoke {
		t.Fatalf("old export must be revoked: %+v %v", auth, err)
	}
	if _, err := w.buildTeamExportCSV(ctx, claim); err == nil {
		t.Fatal("old snapshot remained exportable")
	}
	if _, err := st.DB().Exec(`UPDATE team_analysis_snapshots SET rule_version = ? WHERE snapshot_id = ?`, domain.TeamAnalysisRuleVersion, snap.SnapshotID); err != nil {
		t.Fatal(err)
	}

	sharing, err := st.Teams().UpdateSharingTx(ctx, store.UpdateSharingTxInput{ActorUserID: user, TeamID: team, ExpectedVersion: 1, Sharing: domain.SharingFlags{Base: true, Named: true, Classification: true}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	assertTotals(queue(from, to), "10", "0", "0")
	if auth, err := w.authorizeTeamExport(ctx, claim); err != nil || !auth.revoke {
		t.Fatalf("withdrawal must revoke prior export: %+v %v", auth, err)
	}
	if _, err := st.Teams().UpdateSharingTx(ctx, store.UpdateSharingTxInput{ActorUserID: user, TeamID: team, ExpectedVersion: sharing.SharingVersion, Sharing: domain.SharingFlags{Base: true, Named: true, Classification: true, Cost: true}, Now: now}); err != nil {
		t.Fatal(err)
	}
	assertTotals(queue(from, to), "10", "1001", "0")
	// A tombstone cannot reappear in a newly computed snapshot.
	if _, err := st.DB().Exec(`UPDATE telemetry_events SET delete_at = ? WHERE user_id = ? AND event_type = 'model_usage_recorded'`, now.UnixMilli(), user); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE team_analysis_snapshots SET status = 'obsolete' WHERE team_id = ?`, team); err != nil {
		t.Fatal(err)
	}
	assertTotals(queue(from, to), "0", "1001", "0")
}
