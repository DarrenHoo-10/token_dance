package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
	"tokendance/internal/teammetrics"
	"tokendance/internal/teams"
)

func TestTeamDayConsumerSameTxWritesStaticMetricsMySQL(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	ctx := context.Background()
	now := w.clk.Now()
	userID := "usr_dayconsumer_owner00000001"
	installID := "ins_dayconsumer_device00000001"
	seedAggUserInstall(t, st, userID, installID, now.Add(-time.Hour))
	if _, err := st.DB().Exec(`
		UPDATE users SET email_verified_at = ?, onboarding_completed_at = ?, handle = 'dayc' WHERE user_id = ?`,
		now, now, userID); err != nil {
		t.Fatal(err)
	}

	created, err := st.Teams().CreateTeamTx(ctx, store.CreateTeamTxInput{
		ActorUserID: userID,
		Team: domain.Team{
			TeamID:       "tem_dayconsumer00000000000001",
			Name:         "Day Consumer",
			TimezoneName: "Asia/Shanghai",
		},
		Membership:  domain.TeamMembership{MembershipID: "tmb_dayconsumer00000000000001"},
		Sharing:     domain.SharingFlags{},
		Idempotency: store.TeamsIdempotency{Scope: "create_team", KeyHash: crypto.SHA256([]byte("dayc-create")), RequestHash: crypto.SHA256([]byte("dayc-create"))},
		Now:         now,
	})
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID

	ev := makeAggEvent(t, "dayc-1", now.Add(-time.Minute).UnixMilli(), nil)
	commitAggEvents(t, st, userID, installID, "dayc-nonce-1", now, ev)
	if _, err := w.ProcessTelemetryAggregation(ctx); err != nil {
		t.Fatal(err)
	}

	var personal, teamTokens string
	if err := st.DB().QueryRow(`SELECT CAST(COALESCE(SUM(exact_token_total),0) AS CHAR)
		FROM telemetry_model_metrics WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, userID).Scan(&personal); err != nil {
		t.Fatal(err)
	}
	if personal != "10" {
		t.Fatalf("personal day tokens=%s", personal)
	}
	if err := st.DB().QueryRow(`SELECT CAST(COALESCE(SUM(token_exact_total),0) AS CHAR)
		FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&teamTokens); err != nil {
		t.Fatal(err)
	}
	if teamTokens != personal {
		t.Fatalf("same-tx static write: personal=%s team=%s", personal, teamTokens)
	}

	var analysisRows int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM team_analysis_rows`).Scan(&analysisRows); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.TeamsEnabled = true
	cfg.TeamsAnalysisEnabled = true
	svc := teams.NewService(st, cfg, clock.NewMockClock(now), nil, nil)
	analysisQ := teams.AnalysisQuery{RangeKey: "custom", From: domain.DayDate(now.Add(-24 * time.Hour)), To: domain.DayDate(now)}
	dto, _, err := svc.GetAnalysis(ctx, userID, teamID, analysisQ)
	if err != nil {
		t.Fatal(err)
	}
	if dto.State != "ready" || dto.Summary.Tokens.Value != "10" {
		t.Fatalf("GET static: state=%s tokens=%s", dto.State, dto.Summary.Tokens.Value)
	}
	var analysisRowsAfter int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM team_analysis_rows`).Scan(&analysisRowsAfter); err != nil {
		t.Fatal(err)
	}
	if analysisRowsAfter != analysisRows {
		t.Fatalf("S01 GET must not insert team_analysis_rows (%d -> %d)", analysisRows, analysisRowsAfter)
	}

	var handleAsOf time.Time
	var maxUpdated int64
	if err := st.DB().QueryRow(`SELECT as_of FROM team_analysis_snapshots WHERE team_id = ? AND rule_version = ? AND status = 'ready' LIMIT 1`,
		teamID, domain.TeamAnalysisRuleVersion).Scan(&handleAsOf); err != nil {
		t.Fatal(err)
	}
	if err := st.DB().QueryRow(`SELECT COALESCE(MAX(updated_at),0) FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&maxUpdated); err != nil {
		t.Fatal(err)
	}
	if maxUpdated > 0 {
		commit := time.UnixMilli(maxUpdated).UTC()
		if handleAsOf.Unix() != commit.Unix() {
			t.Fatalf("asOf must be last static commit %s, got %s", commit, handleAsOf)
		}
	}
	later := teams.NewService(st, cfg, clock.NewMockClock(now.Add(time.Hour)), nil, nil)
	dto2, _, err := later.GetAnalysis(ctx, userID, teamID, analysisQ)
	if err != nil {
		t.Fatal(err)
	}
	if dto2.Snapshot.AsOf != dto.Snapshot.AsOf {
		t.Fatalf("second GET must not stamp request time: first=%s second=%s", dto.Snapshot.AsOf, dto2.Snapshot.AsOf)
	}

	teammetrics.AfterPersonalBeforeTeam = func() error { return fmt.Errorf("injected team write failure") }
	t.Cleanup(func() { teammetrics.AfterPersonalBeforeTeam = nil })
	ev2 := makeAggEvent(t, "dayc-2", now.Add(-30*time.Second).UnixMilli(), func(e map[string]any) {
		e["payload"].(map[string]any)["usage"].(map[string]any)["token_total"] = "7"
	})
	commitAggEvents(t, st, userID, installID, "dayc-nonce-2", now, ev2)
	if _, err := w.ProcessTelemetryAggregation(ctx); err != nil {
		t.Logf("failpoint pass err (expected requeue): %v", err)
	}
	var afterFail string
	if err := st.DB().QueryRow(`SELECT CAST(COALESCE(SUM(token_exact_total),0) AS CHAR)
		FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&afterFail); err != nil {
		t.Fatal(err)
	}
	if afterFail != "10" {
		t.Fatalf("A04 failpoint must roll back team write, got %s", afterFail)
	}
	var personalAfterFail string
	if err := st.DB().QueryRow(`SELECT CAST(COALESCE(SUM(exact_token_total),0) AS CHAR)
		FROM telemetry_model_metrics WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, userID).Scan(&personalAfterFail); err != nil {
		t.Fatal(err)
	}
	if personalAfterFail != "10" {
		t.Fatalf("A04 failpoint must roll back personal day apply, got %s", personalAfterFail)
	}
	teammetrics.AfterPersonalBeforeTeam = nil
	if mc, ok := w.clk.(*clock.MockClock); ok {
		mc.Add(6 * time.Second)
	}
	if _, err := w.ProcessTelemetryAggregation(ctx); err != nil {
		t.Fatal(err)
	}
	var afterRetry string
	if err := st.DB().QueryRow(`SELECT CAST(COALESCE(SUM(token_exact_total),0) AS CHAR)
		FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&afterRetry); err != nil {
		t.Fatal(err)
	}
	if afterRetry != "17" {
		t.Fatalf("retry after failpoint want 17 got %s", afterRetry)
	}
}
