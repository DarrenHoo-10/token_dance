package memory

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

func memID(prefix, tag string) string {
	s := prefix + tag
	if len(s) >= 30 {
		return s[:30]
	}
	return s + strings.Repeat("x", 30-len(s))
}

func TestAnalysisCacheUsesRevisionsAndRefreshesWithoutHidingReady(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	from, to := now.Add(-24*time.Hour), now.Add(24*time.Hour)
	m.teams["team"] = &domain.Team{TeamID: "team", Status: domain.TeamStatusActive, TimezoneName: "UTC", AuthRevision: 1}
	m.teamRevisions["team"] = &domain.TeamSourceRevision{SourceRevision: 1}
	m.teamSnapshots["ready"] = &domain.TeamAnalysisSnapshot{SnapshotID: "ready", TeamID: "team", FromDate: from, ToDateExclusive: to, AuthRevision: 1, SourceRevision: 1, RuleVersion: domain.TeamAnalysisRuleVersion, Status: domain.SnapshotReady, AsOf: now.Add(-time.Minute), ExpiresAt: now.Add(time.Minute)}
	snap, queued, err := m.GetOrQueueAnalysis(ctx, "team", from, to, 1, domain.TeamAnalysisRuleVersion, now)
	if err != nil || queued || snap.SnapshotID != "ready" || len(m.teamSnapshots) != 1 {
		t.Fatalf("unchanged data must reuse snapshots older than 30s: %+v %v %v", snap, queued, err)
	}
	m.teamRevisions["team"].SourceRevision++
	for i := 0; i < 2; i++ {
		snap, queued, err = m.GetOrQueueAnalysis(ctx, "team", from, to, 1, domain.TeamAnalysisRuleVersion, now)
		if err != nil || queued || snap.SnapshotID != "ready" || !snap.Refreshing || len(m.teamSnapshots) != 2 {
			t.Fatalf("refresh must retain ready data and deduplicate jobs: %+v %v %v", snap, queued, err)
		}
	}
	m.teams["team"].AuthRevision = 2
	snap, queued, err = m.GetOrQueueAnalysis(ctx, "team", from, to, 2, domain.TeamAnalysisRuleVersion, now)
	if err != nil || !queued || snap.SnapshotID == "ready" {
		t.Fatal("changed authorization must never reuse previous data")
	}
	snap, queued, err = m.GetOrQueueAnalysis(ctx, "team", from, to, 1, domain.TeamAnalysisRuleVersion, now.Add(2*time.Minute))
	if err != nil || !queued || snap.SnapshotID == "ready" {
		t.Fatal("expired snapshot must not be served")
	}
}

func TestMemoryTeamsRejectOldAnalysisRules(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	for _, rule := range []string{"1", domain.TeamAnalysisRuleVersion} {
		m.teamSnapshots["snap"] = &domain.TeamAnalysisSnapshot{SnapshotID: "snap", TeamID: "team", Status: domain.SnapshotReady, RuleVersion: rule}
		m.teamExports["export"] = &domain.TeamExportJob{ExportID: "export", TeamID: "team", SnapshotID: "snap", Status: domain.TeamExportCompleted}
		_, snapErr := m.GetReadySnapshot(ctx, "team", "snap")
		_, exportErr := m.GetExport(ctx, "team", "export")
		if rule == domain.TeamAnalysisRuleVersion {
			if snapErr != nil || exportErr != nil {
				t.Fatalf("current rule rejected: %v %v", snapErr, exportErr)
			}
		} else if snapErr == nil || exportErr == nil {
			t.Fatal("old rule must reject snapshots and exports")
		}
	}
}

func memIdem(scope, key, req string) store.TeamsIdempotency {
	return store.TeamsIdempotency{
		Scope:       scope,
		KeyHash:     sha256.Sum256([]byte(key)),
		RequestHash: sha256.Sum256([]byte(req)),
	}
}

func seedActiveUser(t *testing.T, m *MemoryStore, userID, handle, email string, now time.Time) {
	t.Helper()
	if _, _, err := m.SeedUserForTest(userID, handle, email, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if !m.MutateUser(userID, func(u *domain.User) {
		verified := now
		u.EmailVerifiedAt = &verified
	}) {
		t.Fatal("mutate user")
	}
}

func createTeamInput(t *testing.T, actor, name string, sharing domain.SharingFlags, now time.Time, idem store.TeamsIdempotency) store.CreateTeamTxInput {
	t.Helper()
	teamID, err := memNewID(domain.TeamIDPrefix)
	if err != nil {
		t.Fatal(err)
	}
	memID, err := memNewID(domain.MembershipIDPrefix)
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

func TestMemoryTeams_CreateAcceptConflict(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	m := NewMemoryStore()
	alice := memID("usr_", "alice")
	bob := memID("usr_", "bob")
	seedActiveUser(t, m, alice, "alice_mem", "alice@example.com", now)
	seedActiveUser(t, m, bob, "bob_mem", "bob@example.com", now)

	created, err := m.CreateTeamTx(ctx, createTeamInput(t, alice, "Alice Team", domain.SharingFlags{}, now, memIdem("create_team", "k1", "alice-team")))
	if err != nil {
		t.Fatalf("create alice team: %v", err)
	}
	if created.Outcome != store.TeamsTxChanged {
		t.Fatalf("outcome=%s", created.Outcome)
	}
	if created.Context.Role != domain.TeamRoleOwner {
		t.Fatalf("owner role=%s", created.Context.Role)
	}
	if created.Context.Membership.BaseRole != domain.TeamBaseRoleAdmin {
		t.Fatalf("owner base role stored as %s", created.Context.Membership.BaseRole)
	}

	_, err = m.CreateTeamTx(ctx, createTeamInput(t, alice, "Second Team", domain.SharingFlags{}, now, memIdem("create_team", "k2", "alice-second")))
	if err == nil {
		t.Fatal("expected membership conflict on second create")
	}
	var app *domain.AppError
	if !errors.As(err, &app) || app.Code != "TEAM_MEMBERSHIP_EXISTS" {
		t.Fatalf("second create err=%v", err)
	}

	bobTeam, err := m.CreateTeamTx(ctx, createTeamInput(t, bob, "Bob Team", domain.SharingFlags{}, now, memIdem("create_team", "k3", "bob-team")))
	if err != nil {
		t.Fatalf("create bob team: %v", err)
	}
	invite := domain.TeamInvitation{
		InvitationID:         memID("tiv_", "invite1"),
		RecipientLookupHash:  *mustEmailHash(t, m, alice),
		RecipientCiphertext:  []byte("cipher"),
		LookupKeyVersion:     1,
		EncryptionKeyVersion: 1,
		ExpiresAt:            now.Add(7 * 24 * time.Hour),
	}
	if _, err := m.CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: bob,
		TeamID:      bobTeam.Context.Team.TeamID,
		InvitedRole: domain.TeamBaseRoleMember,
		Invitation:  invite,
		Idempotency: memIdem("create_invitation:"+bobTeam.Context.Team.TeamID, "inv1", "alice"),
		Now:         now,
	}); err != nil {
		t.Fatalf("invite alice: %v", err)
	}
	_, err = m.AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID:             alice,
		InvitationID:            invite.InvitationID,
		ExpectedVersion:         1,
		VerifiedEmailLookupHash: invite.RecipientLookupHash,
		Idempotency:             memIdem("accept_invitation:"+invite.InvitationID, "acc1", "accept"),
		Now:                     now,
	})
	if err == nil {
		t.Fatal("expected accept conflict")
	}
	if !errors.As(err, &app) || app.Code != "TEAM_MEMBERSHIP_EXISTS" {
		t.Fatalf("accept err=%v", err)
	}
}

func TestMemoryTeams_IdempotencyReplay(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	m := NewMemoryStore()
	alice := memID("usr_", "alice2")
	seedActiveUser(t, m, alice, "alice_idemp", "alice2@example.com", now)
	idem := memIdem("create_team", "same-key", "same-body")
	in := createTeamInput(t, alice, "Idem Team", domain.SharingFlags{}, now, idem)
	first, err := m.CreateTeamTx(ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	replay, err := m.CreateTeamTx(ctx, in)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Outcome != store.TeamsTxReplay {
		t.Fatalf("replay outcome=%s", replay.Outcome)
	}
	if replay.Context.Team.TeamID != first.Context.Team.TeamID {
		t.Fatalf("replay team id changed")
	}
	conflict := in
	conflict.Idempotency.RequestHash = sha256.Sum256([]byte("different-body"))
	_, err = m.CreateTeamTx(ctx, conflict)
	if err == nil {
		t.Fatal("expected idempotency reuse")
	}
	var app *domain.AppError
	if !errors.As(err, &app) || app.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reuse err=%v", err)
	}
}

func TestMemoryTeams_InviteLinkLastSlot(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	m := NewMemoryStore()
	owner := memID("usr_", "owner")
	u1 := memID("usr_", "join1")
	u2 := memID("usr_", "join2")
	seedActiveUser(t, m, owner, "owner_link", "owner@example.com", now)
	seedActiveUser(t, m, u1, "join_one", "j1@example.com", now)
	seedActiveUser(t, m, u2, "join_two", "j2@example.com", now)
	created, err := m.CreateTeamTx(ctx, createTeamInput(t, owner, "Link Team", domain.SharingFlags{}, now, memIdem("create_team", "link-team", "body")))
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	tokenHash := sha256.Sum256([]byte("tokendance.team-invite-link.v1\x00token-one"))
	link := domain.TeamInviteLink{
		LinkID:               memID("tln_", "lastslot"),
		TokenHash:            tokenHash,
		TokenCiphertext:      []byte("cipher"),
		EncryptionKeyVersion: 1,
		MaxUses:              1,
		ExpiresAt:            now.Add(7 * 24 * time.Hour),
	}
	if _, err := m.CreateInviteLinkTx(ctx, store.CreateInviteLinkTxInput{
		ActorUserID: owner,
		TeamID:      created.Context.Team.TeamID,
		Link:        link,
		Idempotency: memIdem("create_invite_link:"+created.Context.Team.TeamID, "link1", "link"),
		Now:         now,
	}); err != nil {
		t.Fatalf("create link: %v", err)
	}
	first, err := m.AcceptInviteLinkTx(ctx, store.AcceptInviteLinkTxInput{
		ActorUserID:     u1,
		LinkID:          link.LinkID,
		TokenHash:       tokenHash,
		ExpectedVersion: 1,
		Sharing:         domain.SharingFlags{},
		Idempotency:     memIdem("accept_invite_link:"+link.LinkID, "u1", "accept"),
		Now:             now,
	})
	if err != nil {
		t.Fatalf("first accept: %v", err)
	}
	if first.Outcome != store.TeamsTxChanged || first.Context.Role != domain.TeamRoleMember {
		t.Fatalf("first accept outcome=%s role=%s", first.Outcome, first.Context.Role)
	}
	_, err = m.AcceptInviteLinkTx(ctx, store.AcceptInviteLinkTxInput{
		ActorUserID:     u2,
		LinkID:          link.LinkID,
		TokenHash:       tokenHash,
		ExpectedVersion: 1,
		Sharing:         domain.SharingFlags{},
		Idempotency:     memIdem("accept_invite_link:"+link.LinkID, "u2", "accept"),
		Now:             now,
	})
	if err == nil {
		t.Fatal("expected exhausted link")
	}
	var app *domain.AppError
	if !errors.As(err, &app) || app.Code != "TEAM_INVITE_LINK_EXHAUSTED" {
		t.Fatalf("second accept err=%v", err)
	}
	got, err := m.GetInviteLink(ctx, link.LinkID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UsedCount != 1 {
		t.Fatalf("used_count=%d", got.UsedCount)
	}
}

func TestMemoryTeams_SharingOpenClose(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	m := NewMemoryStore()
	alice := memID("usr_", "share")
	seedActiveUser(t, m, alice, "alice_share", "share@example.com", now)
	created, err := m.CreateTeamTx(ctx, createTeamInput(t, alice, "Share Team", domain.SharingFlags{}, now, memIdem("create_team", "share", "body")))
	if err != nil {
		t.Fatal(err)
	}
	t10 := now.Add(time.Hour)
	state, err := m.UpdateSharingTx(ctx, store.UpdateSharingTxInput{
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
	if !baseFrom.Equal(t10) {
		t.Fatalf("base starts_at=%v", baseFrom)
	}
	t11 := now.Add(2 * time.Hour)
	state, err = m.UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: 2,
		Sharing:         domain.SharingFlags{Base: true, Named: true},
		Now:             t11,
	})
	if err != nil {
		t.Fatalf("open named: %v", err)
	}
	if !state.EffectiveFrom["base"].Equal(t10) {
		t.Fatalf("base starts_at reset to %v", state.EffectiveFrom["base"])
	}
	if !state.EffectiveFrom["named"].Equal(t11) {
		t.Fatalf("named starts_at=%v", state.EffectiveFrom["named"])
	}
	t13 := now.Add(3 * time.Hour)
	state, err = m.UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: state.SharingVersion,
		Sharing:         domain.SharingFlags{Base: true, Named: false},
		Now:             t13,
	})
	if err != nil {
		t.Fatalf("close named: %v", err)
	}
	if state.Sharing.Named {
		t.Fatal("named still open")
	}
	t15 := now.Add(4 * time.Hour)
	state, err = m.UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID:     alice,
		TeamID:          created.Context.Team.TeamID,
		ExpectedVersion: state.SharingVersion,
		Sharing:         domain.SharingFlags{},
		Now:             t15,
	})
	if err != nil {
		t.Fatalf("close base: %v", err)
	}
	if state.Sharing.Base || state.Sharing.Named || state.Sharing.Classification || state.Sharing.Cost {
		t.Fatalf("expected all closed, got %+v", state.Sharing)
	}
}

func mustEmailHash(t *testing.T, m *MemoryStore, userID string) *[32]byte {
	t.Helper()
	u, err := m.FindUserByID(context.Background(), userID)
	if err != nil || u.EmailLookupHash == nil {
		t.Fatalf("email hash: %v", err)
	}
	return u.EmailLookupHash
}
