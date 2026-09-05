package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	"tokendance/internal/provider"
	"tokendance/internal/store/memory"
)

func TestAvatarRelayAndVisibility(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	now := time.Now().UTC()
	_, _, _ = st.SeedUserForTest("usr_avatar", "avatar", "avatar@test.dev", now)
	svc := NewService(st, config.DefaultConfig(), clock.NewMockClock(now), provider.NewMemoryObjectStorage(""))
	data := createTestPNG(32, 32)
	hash := sha256.Sum256(data)
	intent, err := svc.CreateAvatarIntent(ctx, "usr_avatar", CreateAvatarIntentInput{ContentType: "image/png", ByteSize: uint64(len(data)), Sha256: hex.EncodeToString(hash[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UploadAvatarContent(ctx, intent.ObjectID, "another_user", bytes.NewReader(data)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-user upload: %v", err)
	}
	corrupt := append([]byte(nil), data...)
	corrupt[0] ^= 1
	if err := svc.UploadAvatarContent(ctx, intent.ObjectID, "usr_avatar", bytes.NewReader(corrupt)); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if err := svc.UploadAvatarContent(ctx, intent.ObjectID, "usr_avatar", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReadAvatar(ctx, intent.ObjectID, "usr_avatar"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("unvalidated image was readable")
	}
	if _, err := svc.CompleteAvatarIntent(ctx, intent.ObjectID, "usr_avatar"); err != nil {
		t.Fatal(err)
	}
	u, err := st.FindUserByID(ctx, "usr_avatar")
	if err != nil || u.AvatarURL == nil || *u.AvatarURL != "/api/v1/public/avatars/"+intent.ObjectID {
		t.Fatalf("invalid avatar URL: %+v %v", u, err)
	}
	got, contentType, err := svc.ReadAvatar(ctx, intent.ObjectID, "usr_avatar")
	if err != nil || contentType != "image/png" || !bytes.Equal(data, got) {
		t.Fatalf("avatar content mismatch: %v", err)
	}
	priv := domain.UserPrivacySettings{PublicProfileEnabled: false, LeaderboardVisibility: domain.LeaderboardVisibilityPrivate}
	if _, err := st.UpdatePrivacyTx(ctx, "usr_avatar", priv, 0, domain.UserSecurityEvent{}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReadAvatar(ctx, intent.ObjectID, ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("private avatar exposed")
	}
	priv.PublicProfileEnabled = true
	priv.LeaderboardVisibility = domain.LeaderboardVisibilityPublic
	if _, err := st.UpdatePrivacyTx(ctx, "usr_avatar", priv, 0, domain.UserSecurityEvent{}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReadAvatar(ctx, intent.ObjectID, ""); err != nil {
		t.Fatalf("public avatar unavailable: %v", err)
	}
	if err := svc.ClearAvatar(ctx, "usr_avatar"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReadAvatar(ctx, intent.ObjectID, "usr_avatar"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("removed avatar remained readable")
	}
}
