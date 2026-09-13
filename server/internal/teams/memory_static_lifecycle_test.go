package teams

import (
	"context"
	"errors"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	"tokendance/internal/store"
	"tokendance/internal/store/memory"
	"tokendance/internal/teammetrics"
)

func memUserID(tag string) string {
	s := "usr_" + tag
	for len(s) < 30 {
		s += "x"
	}
	return s[:30]
}

func usageDay(date, tokens string) domain.TeamAnalysisRow {
	d := date
	agent, provider, model := "codex", "openai", "gpt-5"
	return domain.TeamAnalysisRow{
		MetricDate:          &d,
		AgentID:             &agent,
		ProviderID:          &provider,
		ModelID:             &model,
		TokenExactTotal:     tokens,
		TokenDerivedTotal:   "0",
		EstimatedCostAmount: "0.50000000",
		ResourcesJSON:       []byte(`{"schemaVersion":1,"inputContextTokens":{"sum":"` + tokens + `","known":"1","observed":"1"},"outputTokens":{"sum":"0","known":"1","observed":"1"},"cachePairs":{"input":"100","read":"9","known":"1","observed":"1"}}`),
	}
}

func filterOptionIDs(opts []map[string]string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, o := range opts {
		if id := o["id"]; id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func TestMemoryJoinLeaveRejoinGetAnalysis(t *testing.T) {
	ctx := context.Background()
	joinAt := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	m := memory.NewMemoryStore()
	owner := memUserID("own")
	member := memUserID("mem")
	if _, _, err := m.SeedUserForTest(owner, "owner", "owner@example.com", joinAt); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.SeedUserForTest(member, "member", "member@example.com", joinAt); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{owner, member} {
		if !m.MutateUser(id, func(u *domain.User) {
			v := joinAt
			u.EmailVerifiedAt = &v
		}) {
			t.Fatal("mutate")
		}
	}
	m.SeedTeamPersonalDays(member, []domain.TeamAnalysisRow{usageDay("2026-09-01", "100")})

	cfg := config.DefaultConfig()
	cfg.TeamsEnabled = true
	cfg.TeamsCreateEnabled = true
	cfg.TeamsJoinEnabled = true
	cfg.TeamsAnalysisEnabled = true
	svc := NewService(m, cfg, clock.NewMockClock(now), nil, nil)

	created, err := m.CreateTeamTx(ctx, store.CreateTeamTxInput{
		ActorUserID: owner,
		Team:        domain.Team{TeamID: memUserID("tem"), Name: "Life", TimezoneName: "UTC"},
		Membership:  domain.TeamMembership{MembershipID: memUserID("tmbown")},
		Sharing:     domain.SharingFlags{},
		Idempotency: store.TeamsIdempotency{Scope: "create_team", KeyHash: [32]byte{1}, RequestHash: [32]byte{1}},
		Now:         joinAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	teamID := created.Context.Team.TeamID
	memberUser, err := m.FindUserByID(ctx, member)
	if err != nil || memberUser.EmailLookupHash == nil {
		t.Fatal(err)
	}
	hash := *memberUser.EmailLookupHash
	inviteID := memUserID("tiv1")
	if _, err := m.CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: inviteID, RecipientLookupHash: hash, RecipientCiphertext: []byte("c"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: store.TeamsIdempotency{Scope: "create_invitation:" + teamID, KeyHash: [32]byte{2}, RequestHash: [32]byte{2}},
		Now:         joinAt,
	}); err != nil {
		t.Fatal(err)
	}
	accepted, err := m.AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: inviteID, ExpectedVersion: 1, VerifiedEmailLookupHash: hash,
		Idempotency: store.TeamsIdempotency{Scope: "accept_invitation:" + inviteID, KeyHash: [32]byte{3}, RequestHash: [32]byte{3}},
		Now:         joinAt,
	})
	if err != nil {
		t.Fatal(err)
	}

	analyze := func() *AnalysisDTO {
		t.Helper()
		dto, _, err := svc.GetAnalysis(ctx, owner, teamID, AnalysisQuery{RangeKey: "custom", From: "2026-09-01", To: "2026-09-12"})
		if err != nil {
			t.Fatal(err)
		}
		if dto.State != "ready" {
			t.Fatalf("state %s", dto.State)
		}
		return dto
	}

	dto := analyze()
	if dto.Summary.Tokens.Value != "0" {
		t.Fatalf("join must not copy pre-join personal days, got %s", dto.Summary.Tokens.Value)
	}
	if len(dto.Contributions.Items) != 0 {
		t.Fatalf("pre-join days must not rank, got %d", len(dto.Contributions.Items))
	}
	if dto.Snapshot == nil || dto.Snapshot.ID == "" {
		t.Fatal("ready analysis must carry a snapshot id")
	}

	m.SeedTeamPersonalDays(member, []domain.TeamAnalysisRow{usageDay("2026-09-01", "100"), usageDay("2026-09-10", "100"), usageDay("2026-09-11", "40")})
	if err := m.RefreshCurrentTeamDays(member); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "140" {
		t.Fatalf("post-join days want 140 got %s", dto.Summary.Tokens.Value)
	}
	opts, err := svc.GetFilterOptions(ctx, owner, teamID, dto.Snapshot.ID)
	if err != nil {
		t.Fatal(err)
	}
	agents, providers, models := filterOptionIDs(opts.Agents), filterOptionIDs(opts.Providers), filterOptionIDs(opts.Models)
	if _, ok := agents["codex"]; !ok {
		t.Fatalf("GetFilterOptions agents want codex, got %+v", opts.Agents)
	}
	if _, ok := providers["openai"]; !ok {
		t.Fatalf("GetFilterOptions providers want openai, got %+v", opts.Providers)
	}
	if _, ok := models["gpt-5"]; !ok {
		t.Fatalf("GetFilterOptions models want gpt-5, got %+v", opts.Models)
	}
	if _, ok := agents[BucketUnsharedClassification]; ok {
		t.Fatalf("auto-share must not list unshared_classification, got %+v", opts.Agents)
	}

	teammetrics.AfterPersonalBeforeTeam = func() error { return errors.New("injected team write failure") }
	t.Cleanup(func() { teammetrics.AfterPersonalBeforeTeam = nil })
	m.SeedTeamPersonalDays(member, []domain.TeamAnalysisRow{usageDay("2026-09-01", "100"), usageDay("2026-09-10", "100"), usageDay("2026-09-11", "40"), usageDay("2026-09-12", "7")})
	if err := m.RefreshCurrentTeamDays(member); err == nil {
		t.Fatal("expected failpoint")
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "140" {
		t.Fatalf("failpoint must not commit, got %s", dto.Summary.Tokens.Value)
	}
	teammetrics.AfterPersonalBeforeTeam = nil
	if err := m.RefreshCurrentTeamDays(member); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "147" {
		t.Fatalf("retry after failpoint want 147 got %s", dto.Summary.Tokens.Value)
	}

	team, err := m.GetTeam(ctx, teamID)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveMemberTx(ctx, store.RemoveMemberTxInput{
		ActorUserID: owner, TeamID: teamID, MembershipID: accepted.Context.Membership.MembershipID,
		ExpectedAuthRevision: team.AuthRevision,
		Idempotency:          store.TeamsIdempotency{Scope: "remove_member:" + teamID, KeyHash: [32]byte{4}, RequestHash: [32]byte{4}},
		Now:                  now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.GetAnalysis(ctx, member, teamID, AnalysisQuery{RangeKey: "custom", From: "2026-09-01", To: "2026-09-12"}); err == nil {
		t.Fatal("leaver must lose GET access")
	}
	dto = analyze()
	if len(dto.Contributions.Items) != 0 {
		t.Fatalf("L02 ranking must exclude leaver, got %d", len(dto.Contributions.Items))
	}
	if dto.Summary.Tokens.Value != "147" {
		t.Fatalf("L02 totals stay 147, got %s", dto.Summary.Tokens.Value)
	}
	if dto.Contributions.Historical == nil || dto.Contributions.Historical.Tokens != "147" {
		t.Fatalf("L02 historical %+v", dto.Contributions.Historical)
	}

	m.SeedTeamPersonalDays(member, []domain.TeamAnalysisRow{
		usageDay("2026-09-01", "100"), usageDay("2026-09-10", "100"), usageDay("2026-09-11", "40"), usageDay("2026-09-12", "60"),
	})
	if err := m.RefreshCurrentTeamDays(member); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "147" {
		t.Fatalf("leave then +60 personal must not update old team, got %s", dto.Summary.Tokens.Value)
	}

	invite2 := memUserID("tiv2")
	if _, err := m.CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: owner, TeamID: teamID, InvitedRole: domain.TeamBaseRoleMember,
		Invitation: domain.TeamInvitation{
			InvitationID: invite2, RecipientLookupHash: hash, RecipientCiphertext: []byte("c2"),
			LookupKeyVersion: 1, EncryptionKeyVersion: 1, ExpiresAt: now.Add(24 * time.Hour),
		},
		Idempotency: store.TeamsIdempotency{Scope: "create_invitation:" + teamID, KeyHash: [32]byte{5}, RequestHash: [32]byte{5}},
		Now:         now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: member, InvitationID: invite2, ExpectedVersion: 1, VerifiedEmailLookupHash: hash,
		Idempotency: store.TeamsIdempotency{Scope: "accept_invitation:" + invite2, KeyHash: [32]byte{6}, RequestHash: [32]byte{6}},
		Now:         now,
	}); err != nil {
		t.Fatal(err)
	}
	dto = analyze()
	if dto.Summary.Tokens.Value != "60" {
		t.Fatalf("rejoin only counts days on/after new joined_at, got %s", dto.Summary.Tokens.Value)
	}
	if len(dto.Contributions.Items) != 1 {
		t.Fatalf("rejoin named ranking want 1 got %d", len(dto.Contributions.Items))
	}
	if dto.Quality != nil && dto.Quality.IncludesHistoricalUsers {
		t.Fatal("rejoin must move quantity back to named")
	}
}
