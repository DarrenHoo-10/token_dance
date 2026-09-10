package leaderboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
	"tokendance/internal/store/memory"
)

func TestRegisterAllAndPrivacyIndependent(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	svc := NewService(st)
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	_, _, _ = st.SeedUserForTest("usr_priv_zero", "", "zero@tokendance.dev", now)
	_, _, _ = st.SeedUserForTest("usr_priv_named", "named", "named@tokendance.dev", now)

	resp, err := svc.GetLeaderboards(ctx, "global", "today", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("leaderboard: %v", err)
	}
	if resp.TotalParticipants == nil || *resp.TotalParticipants != 2 || len(resp.Entries) != 2 {
		t.Fatalf("unpublished zero-token users must rank: %+v", resp)
	}
	_, _ = st.UpdatePrivacyTx(ctx, "usr_priv_named", domain.UserPrivacySettings{PublicProfileEnabled: true}, 0, domain.UserSecurityEvent{}, now)
	after, err := svc.GetLeaderboards(ctx, "global", "today", "tokens", nil, 50)
	if err != nil || after.TotalParticipants == nil || *after.TotalParticipants != 2 {
		t.Fatalf("privacy toggle changed membership: %+v %v", after, err)
	}
	for _, entry := range after.Entries {
		if entry.MetricValue != "0" {
			t.Fatalf("expected zero tokens: %+v", entry)
		}
		if strings.Contains(entry.DisplayName, "@") {
			t.Fatalf("email used as display name: %+v", entry)
		}
	}
}

func TestLeaderboardService(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	svc := NewService(st)

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	// Seed users
	_, _, _ = st.SeedUserForTest("usr_lb1", "alice", "alice@tokendance.dev", now)
	_, _, _ = st.SeedUserForTest("usr_lb2", "bob", "bob@tokendance.dev", now)
	_, _ = st.UpdatePrivacyTx(ctx, "usr_lb1", domain.UserPrivacySettings{PublicProfileEnabled: true}, 0, domain.UserSecurityEvent{}, now)
	_, _ = st.UpdatePrivacyTx(ctx, "usr_lb2", domain.UserPrivacySettings{PublicProfileEnabled: true}, 0, domain.UserSecurityEvent{}, now)

	// Publish snapshot
	st.SeedLeaderboardSnapshot(domain.LeaderboardResponse{
		SnapshotID: "snp_lb_test",
		BoardKey:   "global",
		Window:     "30d",
		Metric:     "tokens",
		Entries: []domain.LeaderboardEntry{
			{RankNo: 1, Handle: "alice", DisplayName: "Alice", MetricValue: "1000000"},
			{RankNo: 2, Handle: "bob", DisplayName: "Bob", MetricValue: "800000"},
		},
	})

	resp, err := svc.GetLeaderboards(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboards: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("expected 2 leaderboard entries, got %d", len(resp.Entries))
	}
	if resp.Entries[0].Handle != "alice" || resp.Entries[1].Handle != "bob" {
		t.Errorf("leaderboard entries order mismatch: %+v", resp.Entries)
	}

	_, _ = st.UpdatePrivacyTx(ctx, "usr_lb2", domain.UserPrivacySettings{PublicProfileEnabled: false}, 0, domain.UserSecurityEvent{}, now)

	respAfter, err := svc.GetLeaderboards(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboards after privacy update: %v", err)
	}
	if len(respAfter.Entries) != 2 || respAfter.Entries[0].Handle != "alice" || respAfter.Entries[1].Handle != "bob" {
		t.Errorf("privacy toggle must not change membership: %+v", respAfter.Entries)
	}
}

func TestGetCommunityStatsServesPrecomputedRowsWithDeltas(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	svc := NewService(st)
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	if err := st.CommunityStats().UpsertCommunityDailyStats(ctx, store.CommunityDailyTotals{
		MetricDate: "2026-09-08", TokensTotal: 100, Developers: 4, CodeLines: 200, Interactions: 40, CostAmount: 20, IsFinal: true,
	}); err != nil {
		t.Fatalf("seed yesterday: %v", err)
	}
	if err := st.CommunityStats().UpsertCommunityDailyStats(ctx, store.CommunityDailyTotals{
		MetricDate: "2026-09-09", TokensTotal: 112, Developers: 5, CodeLines: 100, Interactions: 50, CostAmount: 0,
	}); err != nil {
		t.Fatalf("seed today: %v", err)
	}

	res, err := svc.GetCommunityStats(ctx, now)
	if err != nil {
		t.Fatalf("community stats: %v", err)
	}
	if res.Tokens == nil || *res.Tokens != "112" || res.Developers == nil || *res.Developers != 5 {
		t.Fatalf("today totals missing: %+v", res)
	}
	if res.Deltas == nil || res.Deltas.Tokens == nil || *res.Deltas.Tokens != 12 {
		t.Fatalf("expected +12%% tokens delta: %+v", res.Deltas)
	}
	if res.Deltas.CodeLines == nil || *res.Deltas.CodeLines != -50 {
		t.Fatalf("expected -50%% code lines delta: %+v", res.Deltas)
	}
	if res.Deltas.CostAmount == nil || *res.Deltas.CostAmount != -100 {
		t.Fatalf("expected -100%% cost delta: %+v", res.Deltas)
	}
}

func TestGetCommunityStatsWithoutPrecomputedRowsStaysEmpty(t *testing.T) {
	st := memory.NewMemoryStore()
	svc := NewService(st)
	res, err := svc.GetCommunityStats(context.Background(), time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("community stats: %v", err)
	}
	if res.Tokens != nil || res.Developers != nil || res.Deltas != nil {
		t.Fatalf("cold day must not fabricate zeros: %+v", res)
	}
	if res.MetricDate != "2026-09-09" || res.Timezone != domain.DayTZName {
		t.Fatalf("unexpected envelope: %+v", res)
	}
}
