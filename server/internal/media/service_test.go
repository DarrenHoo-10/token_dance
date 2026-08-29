package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	"tokendance/internal/provider"
	"tokendance/internal/store/memory"
)

func createTestPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	buf := new(bytes.Buffer)
	_ = png.Encode(buf, img)
	return buf.Bytes()
}

func createTestJPEG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}
	buf := new(bytes.Buffer)
	_ = jpeg.Encode(buf, img, nil)
	return buf.Bytes()
}

func TestMediaService(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	storage := provider.NewMemoryObjectStorage("")
	cfg := config.DefaultConfig()
	clk := clock.NewMockClock(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
	svc := NewService(st, cfg, clk, storage)

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

	// 3. Attempt complete before object is uploaded -> 404 / 400
	_, err = svc.CompleteAvatarIntent(ctx, res.ObjectID, userID)
	if err == nil {
		t.Fatalf("expected error completing non-existent storage object")
	}

	// 4. Upload corrupt payload (invalid magic bytes)
	fakeData := []byte("plain text not an image")
	_ = storage.PutObject(ctx, "users/"+userID+"/avatars/"+res.ObjectID, bytes.NewReader(fakeData), int64(len(fakeData)), "image/png")
	_, err = svc.CompleteAvatarIntent(ctx, res.ObjectID, userID)
	if err == nil {
		t.Fatalf("expected error for payload with invalid magic bytes")
	}

	// 5. Upload valid PNG image
	validPNG := createTestPNG(256, 256)
	validPNGHash := sha256.Sum256(validPNG)
	// Create fresh intent
	res2, err := svc.CreateAvatarIntent(ctx, userID, CreateAvatarIntentInput{
		ContentType: "image/png",
		ByteSize:    uint64(len(validPNG)),
		Sha256:      hex.EncodeToString(validPNGHash[:]),
	})
	if err != nil {
		t.Fatalf("failed to create avatar intent 2: %v", err)
	}

	_ = storage.PutObject(ctx, "users/"+userID+"/avatars/"+res2.ObjectID, bytes.NewReader(validPNG), int64(len(validPNG)), "image/png")

	obj, err := svc.CompleteAvatarIntent(ctx, res2.ObjectID, userID)
	if err != nil {
		t.Fatalf("failed to complete valid avatar intent: %v", err)
	}
	if obj.UploadStatus != domain.UploadStatusReady {
		t.Errorf("expected upload status ready, got %s", obj.UploadStatus)
	}
	if obj.ImageWidth == nil || *obj.ImageWidth != 256 || obj.ImageHeight == nil || *obj.ImageHeight != 256 {
		t.Errorf("expected dimensions 256x256, got %v x %v", obj.ImageWidth, obj.ImageHeight)
	}

	// Verify user avatar pointer was switched
	u, _ := st.FindUserByID(ctx, userID)
	if u.AvatarObjectID == nil || *u.AvatarObjectID != res2.ObjectID {
		t.Errorf("expected avatar object ID to match %s", res2.ObjectID)
	}

	// 6. Upload valid JPEG image
	validJPEG := createTestJPEG(128, 128)
	validJPEGHash := sha256.Sum256(validJPEG)
	res3, err := svc.CreateAvatarIntent(ctx, userID, CreateAvatarIntentInput{
		ContentType: "image/jpeg",
		ByteSize:    uint64(len(validJPEG)),
		Sha256:      hex.EncodeToString(validJPEGHash[:]),
	})
	if err != nil {
		t.Fatalf("failed to create avatar intent 3: %v", err)
	}
	_ = storage.PutObject(ctx, "users/"+userID+"/avatars/"+res3.ObjectID, bytes.NewReader(validJPEG), int64(len(validJPEG)), "image/jpeg")

	obj3, err := svc.CompleteAvatarIntent(ctx, res3.ObjectID, userID)
	if err != nil {
		t.Fatalf("failed to complete JPEG avatar intent: %v", err)
	}
	if obj3.UploadStatus != domain.UploadStatusReady {
		t.Errorf("expected upload status ready, got %s", obj3.UploadStatus)
	}

	// 7. Enforce declared hash and configured pixel budget.
	pixelPNG := createTestPNG(100, 100)
	pixelHash := sha256.Sum256(pixelPNG)
	cfg.MediaAvatarMaxPixels = 5000
	pixelIntent, err := svc.CreateAvatarIntent(ctx, userID, CreateAvatarIntentInput{ContentType: "image/png", ByteSize: uint64(len(pixelPNG)), Sha256: hex.EncodeToString(pixelHash[:])})
	if err != nil {
		t.Fatal(err)
	}
	_ = storage.PutObject(ctx, "users/"+userID+"/avatars/"+pixelIntent.ObjectID, bytes.NewReader(pixelPNG), int64(len(pixelPNG)), "image/png")
	if _, err := svc.CompleteAvatarIntent(ctx, pixelIntent.ObjectID, userID); err == nil {
		t.Fatal("expected configured pixel budget rejection")
	}
	cfg.MediaAvatarMaxPixels = 16000000

	// 8. Clear avatar
	err = svc.ClearAvatar(ctx, userID)
	if err != nil {
		t.Fatalf("failed to clear avatar: %v", err)
	}

	uAfter, _ := st.FindUserByID(ctx, userID)
	if uAfter.AvatarURL != nil || uAfter.AvatarObjectID != nil {
		t.Errorf("avatar pointer must be nil after clear")
	}
}
