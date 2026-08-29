package leaderboard

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store/memory"
)

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

	// Disable Bob's public profile -> Bob filtered immediately
	_, _ = st.UpdatePrivacyTx(ctx, "usr_lb2", domain.UserPrivacySettings{PublicProfileEnabled: false}, 0, domain.UserSecurityEvent{}, now)

	respAfter, err := svc.GetLeaderboards(ctx, "global", "30d", "tokens", nil, 50)
	if err != nil {
		t.Fatalf("failed to get leaderboards after privacy update: %v", err)
	}
	if len(respAfter.Entries) != 1 || respAfter.Entries[0].Handle != "alice" {
		t.Errorf("expected only Alice in leaderboard after Bob disabled privacy: %+v", respAfter.Entries)
	}
}
