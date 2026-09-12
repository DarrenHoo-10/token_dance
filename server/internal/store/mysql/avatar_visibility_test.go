package mysql

import (
	"context"
	"errors"
	"testing"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

func TestAvatarVisibilityMySQL(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	ctx, now := context.Background(), time.Now().UTC()
	seedTestUser(t, db, st, "usr_avatar_test", "avatar_test", "Avatar", "avatar@test.example", true, now)
	media := st.Media()
	_, err := media.CreateAvatarUploadIntent(ctx, domain.UserUploadObject{
		ObjectID: "avatar_visibility_test", UserID: "usr_avatar_test", ObjectType: "avatar",
		ObjectKey: "avatars/test.png", UploadStatus: domain.UploadStatusPending,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = media.CompleteAvatarUploadIntent(ctx, "avatar_visibility_test", "usr_avatar_test", store.AvatarReadyMeta{
		ByteSize: 100, ImageWidth: 32, ImageHeight: 32, ContentType: "image/png",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	var url string
	if err := db.QueryRow("SELECT avatar_url FROM users WHERE user_id='usr_avatar_test'").Scan(&url); err != nil || url != "/api/v1/public/avatars/avatar_visibility_test" {
		t.Fatalf("avatar URL: %q, %v", url, err)
	}
	if _, err := media.GetVisibleAvatar(ctx, "avatar_visibility_test", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE user_privacy_settings SET public_profile_enabled=FALSE WHERE user_id='usr_avatar_test'"); err != nil {
		t.Fatal(err)
	}
	if _, err := media.GetVisibleAvatar(ctx, "avatar_visibility_test", ""); err != nil {
		t.Fatalf("leaderboard avatar unavailable for private profile: %v", err)
	}
	if _, err := db.Exec("UPDATE users SET leaderboard_visibility='private' WHERE user_id='usr_avatar_test'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM public_user_profiles WHERE user_id='usr_avatar_test'"); err != nil {
		t.Fatal(err)
	}
	if _, err := media.GetVisibleAvatar(ctx, "avatar_visibility_test", ""); err != nil {
		t.Fatalf("unpublished account avatar unavailable: %v", err)
	}
	if _, err := media.GetVisibleAvatar(ctx, "avatar_visibility_test", "usr_avatar_test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE users SET account_status='suspended' WHERE user_id='usr_avatar_test'"); err != nil {
		t.Fatal(err)
	}
	if _, err := media.GetVisibleAvatar(ctx, "avatar_visibility_test", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("suspended account avatar exposed: %v", err)
	}
	if _, err := db.Exec("UPDATE users SET account_status='active' WHERE user_id='usr_avatar_test'"); err != nil {
		t.Fatal(err)
	}
	if err := media.ClearAvatar(ctx, "usr_avatar_test", now); err != nil {
		t.Fatal(err)
	}
	if _, err := media.GetVisibleAvatar(ctx, "avatar_visibility_test", "usr_avatar_test"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("removed avatar visible: %v", err)
	}
}
