package mysql

import (
	"context"
	"testing"
	"time"
)

func TestPublicProfileFallsBackToActiveOnboardedUserMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC()
	userID := "usr_public_profile_fallback"
	handle := "profile_fallback"
	if _, err := db.ExecContext(context.Background(), `
		INSERT INTO users (
			user_id, auth_subject_hash, handle, display_name, account_status,
			leaderboard_visibility, timezone_name, locale, onboarding_completed_at,
			profile_version, created_at, updated_at
		) VALUES (?, UNHEX(SHA2(?, 256)), ?, 'Profile Fallback', 'active',
			'public', 'UTC', 'en-US', ?, 3, ?, ?)`,
		userID, "subject:"+userID, handle, now, now, now); err != nil {
		t.Fatalf("seed active user: %v", err)
	}

	profile, err := st.Privacy().GetPublicProfileByHandle(context.Background(), handle, now)
	if err != nil {
		t.Fatalf("active user without projection should remain public: %v", err)
	}
	if profile.UserID != userID || profile.Handle != handle || profile.DisplayName != "Profile Fallback" {
		t.Fatalf("unexpected fallback profile: %+v", profile)
	}
	if !profile.ShowTrends || !profile.ShowTokenTotal || profile.ProjectionVersion != 3 {
		t.Fatalf("fallback profile did not receive public capabilities: %+v", profile)
	}
}
