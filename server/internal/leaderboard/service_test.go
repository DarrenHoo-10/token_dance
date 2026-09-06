package leaderboard

import (
	"context"
	"strings"
	"testing"
	"time"

	"tokendance/internal/domain"
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
