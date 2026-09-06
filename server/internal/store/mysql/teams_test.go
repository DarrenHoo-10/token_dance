package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

func mysqlTeamsEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("TOKENDANCE_TEST_MYSQL_DSN") == "" {
		t.Skip("skipping MySQL repository integration test: TOKENDANCE_TEST_MYSQL_DSN not set")
	}
}

func teamUserID(tag string) string {
	s := "usr_" + tag
	if len(s) >= 30 {
		return s[:30]
	}
	return s + strings.Repeat("x", 30-len(s))
}

func teamIdem(scope, key, req string) store.TeamsIdempotency {
	return store.TeamsIdempotency{
		Scope:       scope,
		KeyHash:     sha256.Sum256([]byte(key)),
		RequestHash: sha256.Sum256([]byte(req)),
	}
}

func seedMySQLTeamUser(t *testing.T, db *sql.DB, userID, handle string, now time.Time) [32]byte {
	t.Helper()
	hash := sha256.Sum256([]byte("email:" + userID))
	if _, err := db.Exec(`
		INSERT INTO users (
			user_id, auth_subject_hash, email_lookup_hash, email_ciphertext, handle,
			email_verified_at, display_name, account_status, leaderboard_visibility,
			timezone_name, locale, onboarding_completed_at, profile_version, created_at, updated_at
		) VALUES (?, UNHEX(SHA2(?, 256)), ?, ?, ?, ?, ?, 'active', 'private', 'UTC', 'en-US', ?, 1, ?, ?)`,
		userID, "subject:"+userID, hash[:], []byte("cipher:"+userID), handle, now, now, now, now, now,
	); err != nil {
		t.Fatalf("seed user %s: %v", userID, err)
	}
	return hash
}

func mysqlCreateTeam(t *testing.T, actor, name string, sharing domain.SharingFlags, now time.Time, idem store.TeamsIdempotency) store.CreateTeamTxInput {
	t.Helper()
	teamID, err := newTeamID(domain.TeamIDPrefix)
	if err != nil {
		t.Fatal(err)
	}
	memID, err := newTeamID(domain.MembershipIDPrefix)
	if err != nil {
		t.Fatal(err)
	}
	return store.CreateTeamTxInput{
		ActorUserID: actor,
		Team: domain.Team{
			TeamID:       teamID,
			Name:         name,
			TimezoneName: "UTC",
		},
		Membership:  domain.TeamMembership{MembershipID: memID},
		Sharing:     sharing,
		Idempotency: idem,
		Now:         now,
	}
}

func TestMySQLTeams_CreateAcceptConflict(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	alice := teamUserID("alice")
	bob := teamUserID("bob")
	aliceHash := seedMySQLTeamUser(t, db, alice, "alice_mysql", now)
	_ = seedMySQLTeamUser(t, db, bob, "bob_mysql", now)

	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, alice, "Alice Team", domain.SharingFlags{}, now, teamIdem("create_team", "k1", "alice-team")))
	if err != nil {
		t.Fatalf("create alice team: %v", err)
	}
	if created.Context.Role != domain.TeamRoleOwner || created.Context.Membership.BaseRole != domain.TeamBaseRoleAdmin {
		t.Fatalf("owner stored incorrectly: role=%s base=%s", created.Context.Role, created.Context.Membership.BaseRole)
	}
	_, err = st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, alice, "Second Team", domain.SharingFlags{}, now, teamIdem("create_team", "k2", "alice-second")))
	if err == nil {
		t.Fatal("expected membership conflict")
	}
	var app *domain.AppError
	if !errors.As(err, &app) || app.Code != "TEAM_MEMBERSHIP_EXISTS" {
		t.Fatalf("second create err=%v", err)
	}

	bobTeam, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, bob, "Bob Team", domain.SharingFlags{}, now, teamIdem("create_team", "k3", "bob-team")))
	if err != nil {
		t.Fatalf("create bob team: %v", err)
	}
	inviteID := teamUserID("tivinvite")
	inviteID = "tiv_" + strings.TrimPrefix(inviteID, "usr_")
	if len(inviteID) != 30 {
		inviteID = inviteID + strings.Repeat("x", 30-len(inviteID))
		inviteID = inviteID[:30]
	}
	if _, err := st.Teams().CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: bob,
		TeamID:      bobTeam.Context.Team.TeamID,
		InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID:         inviteID,
			RecipientLookupHash:  aliceHash,
			RecipientCiphertext:  []byte("cipher"),
			LookupKeyVersion:     1,
			EncryptionKeyVersion: 1,
			ExpiresAt:            now.Add(7 * 24 * time.Hour),
		},
		Idempotency: teamIdem("create_invitation:"+bobTeam.Context.Team.TeamID, "inv1", "alice"),
		Now:         now,
	}); err != nil {
		t.Fatalf("invite alice: %v", err)
	}
	_, err = st.Teams().AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID:             alice,
		InvitationID:            inviteID,
		ExpectedVersion:         1,
		VerifiedEmailLookupHash: aliceHash,
		Idempotency:             teamIdem("accept_invitation:"+inviteID, "acc1", "accept"),
		Now:                     now,
	})
	if err == nil {
		t.Fatal("expected accept conflict")
	}
	if !errors.As(err, &app) || app.Code != "TEAM_MEMBERSHIP_EXISTS" {
		t.Fatalf("accept err=%v", err)
	}
}

func TestMySQLTeams_IdempotencyReplay(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	alice := teamUserID("alice2")
	seedMySQLTeamUser(t, db, alice, "alice_idemp", now)
	idem := teamIdem("create_team", "same-key", "same-body")
	in := mysqlCreateTeam(t, alice, "Idem Team", domain.SharingFlags{}, now, idem)
	first, err := st.Teams().CreateTeamTx(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	replay, err := st.Teams().CreateTeamTx(ctx, in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Outcome != store.TeamsTxReplay || replay.Context.Team.TeamID != first.Context.Team.TeamID {
		t.Fatalf("replay outcome=%s team=%s", replay.Outcome, replay.Context.Team.TeamID)
	}
	conflict := in
	conflict.Idempotency.RequestHash = sha256.Sum256([]byte("different-body"))
	_, err = st.Teams().CreateTeamTx(ctx, conflict)
	if err == nil {
		t.Fatal("expected idempotency reuse")
	}
	var app *domain.AppError
	if !errors.As(err, &app) || app.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reuse err=%v", err)
	}
}

func TestMySQLTeams_InviteLinkLastSlot(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	owner := teamUserID("owner")
	u1 := teamUserID("join1")
	u2 := teamUserID("join2")
	seedMySQLTeamUser(t, db, owner, "owner_link", now)
	seedMySQLTeamUser(t, db, u1, "join_one", now)
	seedMySQLTeamUser(t, db, u2, "join_two", now)
	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, owner, "Link Team", domain.SharingFlags{}, now, teamIdem("create_team", "link-team", "body")))
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	tokenHash := sha256.Sum256([]byte("token-one"))
	linkID, err := newTeamID(domain.InviteLinkIDPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Teams().CreateInviteLinkTx(ctx, store.CreateInviteLinkTxInput{
		ActorUserID: owner,
		TeamID:      created.Context.Team.TeamID,
		Link: domain.TeamInviteLink{
			LinkID:               linkID,
			TokenHash:            tokenHash,
			TokenCiphertext:      []byte("cipher"),
			EncryptionKeyVersion: 1,
			MaxUses:              1,
			ExpiresAt:            now.Add(7 * 24 * time.Hour),
		},
		Idempotency: teamIdem("create_invite_link:"+created.Context.Team.TeamID, "link1", "link"),
		Now:         now,
	}); err != nil {
		t.Fatalf("create link: %v", err)
	}
	if _, err := st.Teams().AcceptInviteLinkTx(ctx, store.AcceptInviteLinkTxInput{
		ActorUserID:     u1,
		LinkID:          linkID,
		TokenHash:       tokenHash,
		ExpectedVersion: 1,
		Sharing:         domain.SharingFlags{},
		Idempotency:     teamIdem("accept_invite_link:"+linkID, "u1", "accept"),
		Now:             now,
	}); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	_, err = st.Teams().AcceptInviteLinkTx(ctx, store.AcceptInviteLinkTxInput{
		ActorUserID:     u2,
		LinkID:          linkID,
		TokenHash:       tokenHash,
		ExpectedVersion: 1,
		Sharing:         domain.SharingFlags{},
		Idempotency:     teamIdem("accept_invite_link:"+linkID, "u2", "accept"),
		Now:             now,
	})
	if err == nil {
		t.Fatal("expected exhausted link")
	}
	var app *domain.AppError
	if !errors.As(err, &app) || app.Code != "TEAM_INVITE_LINK_EXHAUSTED" {
		t.Fatalf("second accept err=%v", err)
	}
	got, err := st.Teams().GetInviteLink(ctx, linkID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsedCount != 1 {
		t.Fatalf("used_count=%d", got.UsedCount)
	}
}

func TestMySQLTeams_SharingOpenClose(t *testing.T) {
	mysqlTeamsEnabled(t)
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	alice := teamUserID("share")
	seedMySQLTeamUser(t, db, alice, "alice_share", now)
	created, err := st.Teams().CreateTeamTx(ctx, mysqlCreateTeam(t, alice, "Share Team", domain.SharingFlags{}, now, teamIdem("create_team", "share", "body")))
	if err != nil {
		t.Fatal(err)
	}
	t10 := now.Add(time.Hour)
	state, err := st.Teams().UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: 1,
		Sharing:         domain.SharingFlags{Base: true},
		Now:             t10,
	})
	if err != nil {
		t.Fatalf("open base: %v", err)
	}
	if !state.Sharing.Base || state.SharingVersion != 2 {
		t.Fatalf("after open base: %+v", state)
	}
	baseFrom := state.EffectiveFrom["base"]
	t11 := now.Add(2 * time.Hour)
	state, err = st.Teams().UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: 2,
		Sharing:         domain.SharingFlags{Base: true, Named: true},
		Now:             t11,
	})
	if err != nil {
		t.Fatalf("open named: %v", err)
	}
	if !state.EffectiveFrom["base"].Equal(baseFrom) {
		t.Fatalf("base starts_at reset %v -> %v", baseFrom, state.EffectiveFrom["base"])
	}
	state, err = st.Teams().UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: state.SharingVersion,
		Sharing:         domain.SharingFlags{Base: true},
		Now:             now.Add(3 * time.Hour),
	})
	if err != nil {
		t.Fatalf("close named: %v", err)
	}
	if state.Sharing.Named {
		t.Fatal("named still open")
	}
	state, err = st.Teams().UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: state.SharingVersion,
		Sharing:         domain.SharingFlags{},
		Now:             now.Add(4 * time.Hour),
	})
	if err != nil {
		t.Fatalf("close base: %v", err)
	}
	if state.Sharing.Base || state.Sharing.Named {
		t.Fatalf("expected closed sharing, got %+v", state.Sharing)
	}
}
