package mysql

import (
	"context"
	"errors"
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

// compile-time check that Store still satisfies store.Store after new methods.
var _ store.Store = (*Store)(nil)

func teamTestCfg() *config.Config {
	cfg := config.DefaultConfig()
	cfg.TeamsEnabled = true
	cfg.TeamsCreateEnabled = true
	cfg.TeamsJoinEnabled = true
	cfg.TeamsAnalysisEnabled = true
	return cfg
}

func TestMySQLTeamStaticMetrics_MigrateCommentsAndDuplicateSnapshots(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()

	var missing int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM information_schema.COLUMNS
		WHERE TABLE_SCHEMA = DATABASE()
		  AND TABLE_NAME IN ('team_usage_contributors', 'team_member_day_metrics')
		  AND (COLUMN_COMMENT IS NULL OR COLUMN_COMMENT = '')`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Fatalf("new columns missing COMMENT: %d", missing)
	}

	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	alice := teamUserID("s21a")
	seedMySQLTeamUser(t, db, alice, "s21_alice", now)
	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, alice, "S21 Team", domain.SharingFlags{}, now, teamIdem("create_team", "s21", "s21")))
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	if _, err := db.ExecContext(ctx, `
		INSERT INTO team_analysis_snapshots (
			snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision,
			rule_version, status, active_request_key, as_of, next_attempt_at, expires_at
		) VALUES
		('tas_s21readyxxxxxxxxxxxxxxxxx', ?, '2026-09-01', '2026-09-02', 1, 0, '5', 'ready', NULL, ?, ?, ?),
		('tas_s21queuedxxxxxxxxxxxxxxxx', ?, '2026-09-01', '2026-09-02', 1, 0, '5', 'queued', 'req-s21', ?, ?, ?)`,
		teamID, now, now, now.Add(time.Hour),
		teamID, now, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("duplicate rule-5 snapshots must still insert: %v", err)
	}
}

func TestMySQLTeamStaticMetrics_JoinLeaveRejoinFailpointGet(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	owner := teamUserID("own01")
	member := teamUserID("mem01")
	seedMySQLTeamUser(t, db, owner, "owner_l", now)
	memberHash := seedMySQLTeamUser(t, db, member, "member_l", now)
	installID := (teamUserID("ins01")[:4] + "ins01xxxxxxxxxxxxxxxxxxxxxx")[:30]
	pk := crypto.SHA256([]byte("pk:" + installID))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO installations (installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, registered_at, updated_at)
		VALUES (?, ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		installID, member, pk[:], now, now); err != nil {
		t.Fatal(err)
	}

	insertModelDay := func(day string, tokens int64) {
		t.Helper()
		start, err := domain.DayBucketStartMs(day)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `
			INSERT INTO telemetry_model_metrics (
				created_at, updated_at, extra, user_id, installation_id, grain, bucket_start, harness_id, model_key,
				exact_token_total, usage_observed_count, metric_semantics_version
			) VALUES (?, ?, JSON_OBJECT(), ?, ?, 'day', ?, 'codex', 1, ?, 1, 1)
			ON DUPLICATE KEY UPDATE exact_token_total = exact_token_total + VALUES(exact_token_total), updated_at = VALUES(updated_at)`,
			now.UnixMilli(), now.UnixMilli(), member, installID, start, tokens); err != nil {
			t.Fatalf("insert model day %s: %v", day, err)
		}
	}
	insertModelDay("2026-09-01", 100)

	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, owner, "L Team", domain.SharingFlags{}, now, teamIdem("create_team", "lteam", "l")))
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	inviteID := ("tiv_" + "invite01xxxxxxxxxxxxxxxxxx")[:30]
	if _, err := st.Teams().CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: inviteID, RecipientLookupHash: memberHash, RecipientCiphertext: []byte("c"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: teamIdem("create_invitation:"+teamID, "invl", "m"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	accepted, err := st.Teams().AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: inviteID, ExpectedVersion: 1,
		VerifiedEmailLookupHash: memberHash, Idempotency: teamIdem("accept_invitation:"+inviteID, "accl", "a"), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	svc := teams.NewService(st, teamTestCfg(), clock.NewMockClock(now), nil, nil)
	analyze := func() *teams.AnalysisDTO {
		t.Helper()
		dto, _, err := svc.GetAnalysis(ctx, owner, teamID, teams.AnalysisQuery{RangeKey: "custom", From: "2026-09-01", To: "2026-09-12"})
		if err != nil {
			t.Fatal(err)
		}
		return dto
	}
	dto := analyze()
	if dto.State != "ready" || dto.Summary.Tokens.Value != "100" {
		t.Fatalf("L01 join-before history: state=%s tokens=%s", dto.State, dto.Summary.Tokens.Value)
	}
	if len(dto.Contributions.Items) != 1 {
		t.Fatalf("ranking want 1 current member with tokens, got %d", len(dto.Contributions.Items))
	}

	insertModelDay("2026-09-10", 40)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, member, nil, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "140" {
		t.Fatalf("L01 join-after extra: want 140 got %s", dto.Summary.Tokens.Value)
	}

	var rowsBefore int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_analysis_rows`).Scan(&rowsBefore); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetAnalysis(ctx, owner, teamID, teams.AnalysisQuery{RangeKey: "today"}); err != nil {
		t.Fatal(err)
	}
	var rowsAfter int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_analysis_rows`).Scan(&rowsAfter); err != nil {
		t.Fatal(err)
	}
	if rowsAfter != rowsBefore {
		t.Fatalf("S01 GET inserted team_analysis_rows (%d -> %d)", rowsBefore, rowsAfter)
	}

	teammetrics.AfterPersonalBeforeTeam = func() error { return errors.New("injected team write failure") }
	t.Cleanup(func() { teammetrics.AfterPersonalBeforeTeam = nil })
	insertModelDay("2026-09-11", 7)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	failErr := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, member, []string{"2026-09-11"}, now.UnixMilli())
	if failErr == nil {
		t.Fatal("expected failpoint error")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "140" {
		t.Fatalf("A04 failpoint must not commit: got %s", dto.Summary.Tokens.Value)
	}
	teammetrics.AfterPersonalBeforeTeam = nil
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, member, []string{"2026-09-11"}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "147" {
		t.Fatalf("retry after failpoint want 147 got %s", dto.Summary.Tokens.Value)
	}

	team, err := st.Teams().GetTeam(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Teams().RemoveMemberTx(ctx, store.RemoveMemberTxInput{
		ActorUserID: owner, TeamID: teamID, MembershipID: accepted.Context.Membership.MembershipID,
		ExpectedAuthRevision: team.AuthRevision,
		Idempotency:          teamIdem("remove_member:"+teamID, "rm1", "rm"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	dto = analyze()
	if len(dto.Contributions.Items) != 0 {
		t.Fatalf("L02 ranking must exclude leaver, got %d", len(dto.Contributions.Items))
	}
	if dto.Summary.Tokens.Value != "147" {
		t.Fatalf("L02 totals stay 147, got %s", dto.Summary.Tokens.Value)
	}
	if dto.Quality == nil || !dto.Quality.IncludesHistoricalUsers {
		t.Fatal("L05 historical flag")
	}
	if dto.Contributions.Historical == nil || dto.Contributions.Historical.Tokens != "147" {
		t.Fatalf("historical subtotal %+v", dto.Contributions.Historical)
	}

	insertModelDay("2026-09-12", 60)
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, member, []string{"2026-09-12"}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "147" {
		t.Fatalf("leave then +60 personal must not update old team, got %s", dto.Summary.Tokens.Value)
	}

	invite2 := ("tiv_" + "invite02xxxxxxxxxxxxxxxxxx")[:30]
	if _, err := st.Teams().CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: invite2, RecipientLookupHash: memberHash, RecipientCiphertext: []byte("c2"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: teamIdem("create_invitation:"+teamID, "invl2", "m2"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Teams().AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: invite2, ExpectedVersion: 1,
		VerifiedEmailLookupHash: memberHash, Idempotency: teamIdem("accept_invitation:"+invite2, "acc2", "a2"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "207" {
		t.Fatalf("L03 rejoin overwrite want 207 (147+60) got %s", dto.Summary.Tokens.Value)
	}
	if len(dto.Contributions.Items) != 1 {
		t.Fatalf("rejoin named ranking want 1 got %d", len(dto.Contributions.Items))
	}
	if dto.Contributions.Historical != nil && dto.Contributions.Historical.Tokens != "0" && dto.Quality.IncludesHistoricalUsers {
		t.Fatalf("rejoin should move quantity back to named, hist=%v", dto.Contributions.Historical)
	}
}

func TestMySQLTeamStaticMetrics_PrivacyDeleteAndDissolve(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	owner := teamUserID("ownpd")
	member := teamUserID("mempd")
	seedMySQLTeamUser(t, db, owner, "own_pd", now)
	memberHash := seedMySQLTeamUser(t, db, member, "mem_pd", now)
	installID := (teamUserID("inspd")[:4] + "inspdxxxxxxxxxxxxxxxxxxxxxx")[:30]
	pk := crypto.SHA256([]byte("pk:" + installID))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO installations (installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, registered_at, updated_at)
		VALUES (?, ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		installID, member, pk[:], now, now); err != nil {
		t.Fatal(err)
	}
	start, err := domain.DayBucketStartMs("2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, extra, user_id, installation_id, grain, bucket_start, harness_id, model_key,
			exact_token_total, usage_observed_count, metric_semantics_version
		) VALUES (?, ?, JSON_OBJECT(), ?, ?, 'day', ?, 'codex', 1, 100, 1, 1)`,
		now.UnixMilli(), now.UnixMilli(), member, installID, start); err != nil {
		t.Fatal(err)
	}

	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, owner, "PD Team", domain.SharingFlags{}, now, teamIdem("create_team", "pdteam", "pd")))
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	inviteID := ("tiv_" + "invitepdxxxxxxxxxxxxxxxxxx")[:30]
	if _, err := st.Teams().CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: inviteID, RecipientLookupHash: memberHash, RecipientCiphertext: []byte("c"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: teamIdem("create_invitation:"+teamID, "invpd", "m"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	accepted, err := st.Teams().AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: inviteID, ExpectedVersion: 1,
		VerifiedEmailLookupHash: memberHash, Idempotency: teamIdem("accept_invitation:"+inviteID, "accpd", "a"), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	team, err := st.Teams().GetTeam(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Teams().RemoveMemberTx(ctx, store.RemoveMemberTxInput{
		ActorUserID: owner, TeamID: teamID, MembershipID: accepted.Context.Membership.MembershipID,
		ExpectedAuthRevision: team.AuthRevision,
		Idempotency:          teamIdem("remove_member:"+teamID, "rmpd", "rm"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	var hist int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&hist); err != nil {
		t.Fatal(err)
	}
	if hist == 0 {
		t.Fatal("leave must keep historical static rows")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := closeUserTeamOnAccountDeletion(ctx, tx, member, now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var afterPrivacy int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&afterPrivacy); err != nil {
		t.Fatal(err)
	}
	if afterPrivacy != 0 {
		t.Fatalf("privacy delete must locate historical rows, got %d", afterPrivacy)
	}
	var contribs int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_usage_contributors WHERE user_id = ?`, member).Scan(&contribs); err != nil {
		t.Fatal(err)
	}
	if contribs != 0 {
		t.Fatalf("privacy delete must remove contributor identity, got %d", contribs)
	}
	var personal string
	if err := db.QueryRowContext(ctx, `SELECT CAST(COALESCE(SUM(exact_token_total),0) AS CHAR) FROM telemetry_model_metrics WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, member).Scan(&personal); err != nil {
		t.Fatal(err)
	}
	if personal != "100" {
		t.Fatalf("account deletion of team stats must not wipe personal day metrics, got %s", personal)
	}

	ownerInstall := (teamUserID("insow")[:4] + "insowxxxxxxxxxxxxxxxxxxxxxx")[:30]
	pk2 := crypto.SHA256([]byte("pk:" + ownerInstall))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO installations (installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, registered_at, updated_at)
		VALUES (?, ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		ownerInstall, owner, pk2[:], now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, extra, user_id, installation_id, grain, bucket_start, harness_id, model_key,
			exact_token_total, usage_observed_count, metric_semantics_version
		) VALUES (?, ?, JSON_OBJECT(), ?, ?, 'day', ?, 'codex', 1, 40, 1, 1)`,
		now.UnixMilli(), now.UnixMilli(), owner, ownerInstall, start); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, owner, nil, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var beforeDissolve int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_member_day_metrics WHERE team_id = ? AND delete_at IS NULL`, teamID).Scan(&beforeDissolve); err != nil {
		t.Fatal(err)
	}
	if beforeDissolve == 0 {
		t.Fatal("owner refresh must write static rows before dissolve")
	}
	fresh, err := st.Teams().GetTeam(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Teams().DissolveTeamTx(ctx, store.DissolveTeamTxInput{
		ActorUserID: owner, TeamID: teamID, ConfirmTeamName: fresh.Name,
		ExpectedAuthRevision: fresh.AuthRevision,
		Idempotency:          teamIdem("dissolve_team:"+teamID, "ds1", "ds"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	var afterDissolve int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM team_member_day_metrics WHERE team_id = ?`, teamID).Scan(&afterDissolve); err != nil {
		t.Fatal(err)
	}
	if afterDissolve != 0 {
		t.Fatalf("dissolve must clear team static rows, got %d", afterDissolve)
	}
	var ownerPersonal string
	if err := db.QueryRowContext(ctx, `SELECT CAST(COALESCE(SUM(exact_token_total),0) AS CHAR) FROM telemetry_model_metrics WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, owner).Scan(&ownerPersonal); err != nil {
		t.Fatal(err)
	}
	if ownerPersonal != "40" {
		t.Fatalf("dissolve must keep personal usage, got %s", ownerPersonal)
	}
}

func TestMySQLTeamStaticMetrics_GetFilterOptionsListsRealAgents(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	owner := teamUserID("ownfo")
	member := teamUserID("memfo")
	seedMySQLTeamUser(t, db, owner, "own_fo", now)
	memberHash := seedMySQLTeamUser(t, db, member, "mem_fo", now)
	installID := (teamUserID("insfo")[:4] + "insfoxxxxxxxxxxxxxxxxxxxxxx")[:30]
	pk := crypto.SHA256([]byte("pk:" + installID))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO installations (installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, registered_at, updated_at)
		VALUES (?, ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		installID, member, pk[:], now, now); err != nil {
		t.Fatal(err)
	}
	res, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_models (created_at, updated_at, extra, provider_id, model_id)
		VALUES (?, ?, JSON_OBJECT(), 'openai', 'gpt-5')`, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	modelKey, err := res.LastInsertId()
	if err != nil || modelKey == 0 {
		t.Fatalf("model key %d err %v", modelKey, err)
	}
	start, err := domain.DayBucketStartMs("2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO telemetry_model_metrics (
			created_at, updated_at, extra, user_id, installation_id, grain, bucket_start, harness_id, model_key,
			exact_token_total, usage_observed_count, metric_semantics_version
		) VALUES (?, ?, JSON_OBJECT(), ?, ?, 'day', ?, 'codex', ?, 100, 1, 1)`,
		now.UnixMilli(), now.UnixMilli(), member, installID, start, modelKey); err != nil {
		t.Fatal(err)
	}

	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, owner, "FO Team", domain.SharingFlags{}, now, teamIdem("create_team", "foteam", "fo")))
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	inviteID := ("tiv_" + "invitefoxxxxxxxxxxxxxxxxxx")[:30]
	if _, err := st.Teams().CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: inviteID, RecipientLookupHash: memberHash, RecipientCiphertext: []byte("c"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: teamIdem("create_invitation:"+teamID, "invfo", "m"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Teams().AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: inviteID, ExpectedVersion: 1,
		VerifiedEmailLookupHash: memberHash, Idempotency: teamIdem("accept_invitation:"+inviteID, "accfo", "a"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	svc := teams.NewService(st, teamTestCfg(), clock.NewMockClock(now), nil, nil)
	dto, _, err := svc.GetAnalysis(ctx, owner, teamID, teams.AnalysisQuery{RangeKey: "custom", From: "2026-09-01", To: "2026-09-12"})
	if err != nil {
		t.Fatal(err)
	}
	if dto.Snapshot == nil || dto.Snapshot.ID == "" {
		t.Fatal("ready analysis must carry snapshot id")
	}
	opts, err := svc.GetFilterOptions(ctx, owner, teamID, dto.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	has := func(items []map[string]string, id string) bool {
		for _, item := range items {
			if item["id"] == id {
				return true
			}
		}
		return false
	}
	if !has(opts.Agents, "codex") {
		t.Fatalf("agents want codex, got %+v", opts.Agents)
	}
	if !has(opts.Providers, "openai") {
		t.Fatalf("providers want openai, got %+v", opts.Providers)
	}
	if !has(opts.Models, "gpt-5") {
		t.Fatalf("models want gpt-5, got %+v", opts.Models)
	}
	if has(opts.Agents, "unshared_classification") {
		t.Fatalf("auto-share must not list unshared_classification, got %+v", opts.Agents)
	}
}

func TestMySQLTeamStaticMetrics_LegacyModelAndSkillProjection(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	owner := teamUserID("ownlg")
	member := teamUserID("memlg")
	seedMySQLTeamUser(t, db, owner, "own_lg", now)
	memberHash := seedMySQLTeamUser(t, db, member, "mem_lg", now)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			aggregation_version, computed_at, updated_at
		) VALUES ('2026-09-10', ?, 'codex', 100, 0, 2, ?, ?)`, member, now, now); err != nil {
		t.Fatal(err)
	}

	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, owner, "LG Team", domain.SharingFlags{}, now, teamIdem("create_team", "lgteam", "lg")))
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	inviteID := ("tiv_" + "invitelgxxxxxxxxxxxxxxxxxx")[:30]
	if _, err := st.Teams().CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: inviteID, RecipientLookupHash: memberHash, RecipientCiphertext: []byte("c"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: teamIdem("create_invitation:"+teamID, "invlg", "m"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Teams().AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: inviteID, ExpectedVersion: 1,
		VerifiedEmailLookupHash: memberHash, Idempotency: teamIdem("accept_invitation:"+inviteID, "acclg", "a"), Now: now,
	}); err != nil {
		t.Fatal(err)
	}

	svc := teams.NewService(st, teamTestCfg(), clock.NewMockClock(now), nil, nil)
	analyze := func() *teams.AnalysisDTO {
		t.Helper()
		dto, _, err := svc.GetAnalysis(ctx, owner, teamID, teams.AnalysisQuery{RangeKey: "custom", From: "2026-09-10", To: "2026-09-12"})
		if err != nil {
			t.Fatal(err)
		}
		return dto
	}
	dto := analyze()
	if dto.Summary.Tokens.Value != "100" || len(dto.Models.Items) != 0 {
		t.Fatalf("agent-only first pass tokens=%s models=%d", dto.Summary.Tokens.Value, len(dto.Models.Items))
	}
	stale, err := teammetrics.ListStaleTeamProjectionUsers(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("no personal models yet, stale=%v", stale)
	}

	if _, err := db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_model_metrics (
			metric_date, user_id, agent_id, provider_id, model_id,
			exact_token_total, derived_token_total, aggregation_version, computed_at, updated_at
		) VALUES
		('2026-09-10', ?, 'codex', 'openai', 'gpt-5', 60, 0, 2, ?, ?),
		('2026-09-10', ?, 'codex', 'openai', 'gpt-4.1', 40, 0, 2, ?, ?)`,
		member, now, now, member, now, now); err != nil {
		t.Fatal(err)
	}
	skillKey := crypto.SHA256([]byte("skill.frontend-design"))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO daily_skill_metrics (
			metric_date, user_id, agent_id, skill_key, skill_public_name,
			use_count, exact_use_count, success_count, failure_count,
			source_max_event_pk, aggregation_version, computed_at, updated_at
		) VALUES ('2026-09-10', ?, 'codex', ?, 'frontend-design', 12, 12, 12, 0, 1, 2, ?, ?)`,
		member, skillKey[:], now, now); err != nil {
		t.Fatal(err)
	}
	stale, err = teammetrics.ListStaleTeamProjectionUsers(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range stale {
		if id == member {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected stale member after personal model/skill insert, got %v", stale)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, member, nil, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "100" {
		t.Fatalf("model rows must replace agent row, not double-count, got %s", dto.Summary.Tokens.Value)
	}
	if len(dto.Agents.Items) != 1 || dto.Agents.Items[0]["id"] != "codex" {
		t.Fatalf("agents %+v", dto.Agents.Items)
	}
	if len(dto.Models.Items) != 2 {
		t.Fatalf("models want 2, got %+v", dto.Models.Items)
	}
	modelMembers, _ := dto.Models.Items[0]["members"].([]map[string]any)
	if len(modelMembers) != 1 {
		t.Fatalf("model members %+v", dto.Models.Items[0])
	}
	if dto.Skills == nil || len(dto.Skills.Items) != 1 || dto.Skills.Items[0]["label"] != "frontend-design" || dto.Skills.Items[0]["useCount"] != "12" {
		t.Fatalf("skills %+v", dto.Skills)
	}
	skillMembers, _ := dto.Skills.Items[0]["members"].([]map[string]any)
	if len(skillMembers) != 1 {
		t.Fatalf("skill members %+v", dto.Skills.Items[0])
	}
	stale, err = teammetrics.ListStaleTeamProjectionUsers(ctx, db, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range stale {
		if id == member {
			t.Fatal("member must not stay stale after refresh")
		}
	}
}
