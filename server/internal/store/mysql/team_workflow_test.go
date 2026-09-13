package mysql

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

func TestMySQLTeams_OwnershipLeaveAndDissolveWorkflow(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx, now := context.Background(), time.Now().UTC().Truncate(time.Millisecond)
	owner, member := teamUserID("workflow_owner"), teamUserID("workflow_member")
	seedMySQLTeamUser(t, db, owner, "workflow_owner", now)
	seedMySQLTeamUser(t, db, member, "workflow_member", now)
	teamStore := st.Teams()
	created, err := teamStore.CreateTeamTx(ctx, mysqlCreateTeam(t, owner, "Workflow Team", domain.SharingFlags{}, now, teamIdem("create_team", "workflow", "create")))
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	revision := func() uint64 {
		t.Helper()
		team, err := teamStore.GetTeam(ctx, teamID)
		if err != nil {
			t.Fatal(err)
		}
		return team.AuthRevision
	}
	linkID, err := newTeamID(domain.InviteLinkIDPrefix)
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("workflow-fixture-token"))
	_, err = teamStore.CreateInviteLinkTx(ctx, store.CreateInviteLinkTxInput{
		ActorUserID: owner, TeamID: teamID, Now: now,
		Idempotency: teamIdem("create_invite_link:"+teamID, "workflow-link", "create"),
		Link:        domain.TeamInviteLink{LinkID: linkID, TokenHash: tokenHash, TokenCiphertext: []byte("fixture"), EncryptionKeyVersion: 1, MaxUses: 2, ExpiresAt: now.Add(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined, err := teamStore.AcceptInviteLinkTx(ctx, store.AcceptInviteLinkTxInput{
		ActorUserID: member, LinkID: linkID, TokenHash: tokenHash, ExpectedVersion: 1, Now: now,
		Idempotency: teamIdem("accept_invite_link:"+linkID, "workflow-join", "join"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if joined.Context.Role != domain.TeamRoleMember {
		t.Fatalf("link must join as member, got %s", joined.Context.Role)
	}
	if err := teamStore.LeaveTeamTx(ctx, store.LeaveTeamTxInput{ActorUserID: owner, TeamID: teamID, ExpectedAuthRevision: revision(), Now: now,
		Idempotency: teamIdem("leave:"+teamID, "owner-leave", "leave")}); err == nil {
		t.Fatal("owner must transfer ownership before leaving")
	}
	if err := teamStore.ChangeMemberRoleTx(ctx, store.ChangeMemberRoleTxInput{ActorUserID: member, TeamID: teamID,
		MembershipID: joined.Context.Membership.MembershipID, NewRole: domain.TeamBaseRoleAdmin, ExpectedAuthRevision: revision(), Now: now}); err == nil {
		t.Fatal("member must not promote themselves")
	}
	_, err = teamStore.TransferOwnershipTx(ctx, store.TransferOwnershipTxInput{ActorUserID: owner, TeamID: teamID,
		TargetMembershipID: joined.Context.Membership.MembershipID, ConfirmTeamName: "Workflow Team", ExpectedAuthRevision: revision(), Now: now,
		Idempotency: teamIdem("transfer:"+teamID, "transfer", "transfer")})
	if err != nil {
		t.Fatal(err)
	}
	if _, team, _, err := teamStore.GetCurrentTeam(ctx, member); err != nil || team == nil || team.OwnerUserID != member {
		t.Fatalf("ownership not transferred: team=%+v err=%v", team, err)
	}
	if err := teamStore.LeaveTeamTx(ctx, store.LeaveTeamTxInput{ActorUserID: owner, TeamID: teamID, ExpectedAuthRevision: revision(), Now: now,
		Idempotency: teamIdem("leave:"+teamID, "former-owner-leave", "leave")}); err != nil {
		t.Fatal(err)
	}
	if err := teamStore.DissolveTeamTx(ctx, store.DissolveTeamTxInput{ActorUserID: member, TeamID: teamID, ConfirmTeamName: "Workflow Team",
		ExpectedAuthRevision: revision(), Now: now, Idempotency: teamIdem("dissolve:"+teamID, "dissolve", "dissolve")}); err != nil {
		t.Fatal(err)
	}
	var current, activeLinks int
	if err := db.QueryRow(`SELECT COUNT(*) FROM user_current_teams WHERE team_id = ?`, teamID).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM team_invite_links WHERE team_id = ? AND status = 'active'`, teamID).Scan(&activeLinks); err != nil {
		t.Fatal(err)
	}
	if current != 0 || activeLinks != 0 {
		t.Fatalf("dissolve left current members=%d active links=%d", current, activeLinks)
	}
}
