package media

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	"tokendance/internal/store/memory"
)

func TestMediaService(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, cfg, clk)

	userID := "usr_media_test"
	now := clk.Now()
	_, _, _ = st.SeedUserForTest(userID, "mediauser", "media@tokendance.dev", now)

	// 1. Create intent with invalid MIME -> 400
	_, err := svc.CreateAvatarIntent(ctx, userID, CreateAvatarIntentInput{
		ContentType: "text/html",
		ByteSize:    1024,
		Sha256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	if err == nil {
		t.Fatalf("expected error on invalid content type")
	}

	// 2. Create intent with valid PNG
	res, err := svc.CreateAvatarIntent(ctx, userID, CreateAvatarIntentInput{
		ContentType: "image/png",
		ByteSize:    1024 * 500,
		Sha256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	if err != nil {
		t.Fatalf("failed to create valid avatar intent: %v", err)
	}
	if res.ObjectID == "" || res.UploadURL == "" {
		t.Errorf("invalid intent response: %+v", res)
	}

	// 3. Complete intent
	obj, err := svc.CompleteAvatarIntent(ctx, res.ObjectID, userID)
	if err != nil {
		t.Fatalf("failed to complete avatar intent: %v", err)
	}
	if obj.UploadStatus != domain.UploadStatusReady {
		t.Errorf("expected upload status ready, got %s", obj.UploadStatus)
	}

	// 4. Clear avatar
	err = svc.ClearAvatar(ctx, userID)
	if err != nil {
		t.Fatalf("failed to clear avatar: %v", err)
	}

	u, _ := st.FindUserByID(ctx, userID)
	if u.AvatarURL != nil || u.AvatarObjectID != nil {
		t.Errorf("avatar pointer must be nil after clear")
	}
}
