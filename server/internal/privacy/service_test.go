package privacy

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/store/memory"
)

func TestPrivacyAndPublicProjection(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)

	userID := "usr_privtest"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "privuser", "priv@tokendance.dev", now)

	// 1. Get initial privacy
	p, err := svc.GetPrivacy(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get privacy: %v", err)
	}
	if !p.PublicProfileEnabled || !p.ShowTrends {
		t.Errorf("privacy is always public, got %+v", p)
	}

	pub, redirect, err := svc.GetPublicProfileByHandle(ctx, "privuser")
	if err != nil {
		t.Fatalf("hidden projection must still be readable: %v", err)
	}
	if redirect != "" {
		t.Errorf("expected no redirect")
	}
	if pub.Handle != "privuser" || !pub.ShowTokenTotal || !pub.ShowTrends {
		t.Errorf("public profile fields mismatch: %+v", pub)
	}

	updated, err := svc.UpdatePrivacy(ctx, userID, UpdatePrivacyInput{
		PublicProfileEnabled:  false,
		LeaderboardVisibility: domain.LeaderboardVisibilityPrivate,
		ExpectedVersion:       p.PrivacyVersion,
	})
	if err != nil {
		t.Fatalf("failed to update privacy: %v", err)
	}
	if !updated.PublicProfileEnabled || !updated.ShowTrends {
		t.Errorf("privacy updates stay public, got %+v", updated)
	}

	pub, _, err = svc.GetPublicProfileByHandle(ctx, "privuser")
	if err != nil {
		t.Fatalf("profile must stay public after a private write: %v", err)
	}
	if !pub.ShowTrends {
		t.Errorf("expected trends to stay visible")
	}
}

func TestDeletionRequestFlow(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)

	userID := "usr_deltest"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "deluser", "del@tokendance.dev", now)

	// 1. Request deletion without confirmation -> fails
	_, err := svc.RequestDeletion(ctx, userID, "account", false)
	if err == nil {
		t.Errorf("expected error when confirmation=false")
	}

	// 2. Request deletion with confirmation -> succeeds
	delReq, err := svc.RequestDeletion(ctx, userID, "account", true)
	if err != nil {
		t.Fatalf("failed to request deletion: %v", err)
	}
	if delReq.RequestStatus != domain.DeletionStatusPending {
		t.Errorf("expected pending status")
	}
	if delReq.CancelBefore == nil {
		t.Errorf("expected cancelBefore to be populated")
	}

	// 3. User status should be deletion_pending
	u, _ := st.FindUserByID(ctx, userID)
	if u.AccountStatus != domain.AccountStatusDeletionPending {
		t.Errorf("expected deletion_pending status, got %s", u.AccountStatus)
	}

	// 4. Cancel deletion before window expires
	err = svc.CancelDeletion(ctx, delReq.RequestID, userID)
	if err != nil {
		t.Fatalf("failed to cancel deletion: %v", err)
	}

	// 5. User should be active again
	u, _ = st.FindUserByID(ctx, userID)
	if u.AccountStatus != domain.AccountStatusActive {
		t.Errorf("expected active status after cancel, got %s", u.AccountStatus)
	}
}
