package profile

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/store/memory"
)

func TestProfileAndOnboarding(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, clk)

	// Pre-populate user
	userID := "usr_testprofile"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "", "user@tokendance.dev", now)

	// 1. Invalid Handle checks
	err := ValidateHandle("ab") // too short
	if err == nil {
		t.Errorf("expected error for 2-char handle")
	}
	err = ValidateHandle("123abc") // doesn't start with letter
	if err == nil {
		t.Errorf("expected error for handle starting with digit")
	}
	err = ValidateHandle("admin") // reserved
	if err == nil {
		t.Errorf("expected error for reserved handle")
	}

	// 2. Complete Onboarding
	in := OnboardingInput{
		DisplayName: "Max Bauer",
		Handle:      "maxbauer",
		Timezone:    "Asia/Shanghai",
		Locale:      "zh-CN",
		Privacy: domain.UserPrivacySettings{
			PublicProfileEnabled: true,
			ShowBio:              true,
			ShowTokenTotal:       true,
		},
	}

	u, p, err := svc.CompleteOnboarding(ctx, userID, in)
	if err != nil {
		t.Fatalf("failed onboarding: %v", err)
	}

	if *u.Handle != "maxbauer" {
		t.Errorf("expected handle maxbauer, got %s", *u.Handle)
	}
	if u.DisplayName != "Max Bauer" {
		t.Errorf("expected Max Bauer, got %s", u.DisplayName)
	}
	if !p.PublicProfileEnabled {
		t.Errorf("expected public profile enabled")
	}

	// 3. Update Profile
	newBio := "AI Engineer & Token Enthusiast"
	newDisplayName := "Max B."
	upIn := UpdateProfileInput{
		DisplayName:     &newDisplayName,
		Bio:             &newBio,
		ExpectedVersion: u.ProfileVersion,
	}

	updatedUser, err := svc.UpdateProfile(ctx, userID, upIn)
	if err != nil {
		t.Fatalf("failed to update profile: %v", err)
	}
	if updatedUser.DisplayName != "Max B." || *updatedUser.Bio != newBio {
		t.Errorf("profile update fields mismatch")
	}

	// 4. Optimistic locking failure with stale version
	staleIn := UpdateProfileInput{
		DisplayName:     &newDisplayName,
		ExpectedVersion: 1, // now is 3
	}
	_, err = svc.UpdateProfile(ctx, userID, staleIn)
	if err == nil {
		t.Errorf("expected version conflict error")
	}
}
