package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/media"
)

func TestPublicResourcesCacheAndRevalidate(t *testing.T) {
	router, _, _ := setupTestRouter(t)
	for _, path := range []string{"/api/v1/public/leaderboards?window=today&limit=100", "/api/v1/public/leaderboards/stats"} {
		first := httptest.NewRecorder()
		router.ServeHTTP(first, httptest.NewRequest("GET", path, nil))
		if first.Code != 200 || first.Header().Get("Cache-Control") != "public, max-age=30, must-revalidate" || first.Header().Get("ETag") == "" {
			t.Fatalf("unexpected response for %s: %d %v", path, first.Code, first.Header())
		}
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("If-None-Match", `"other", W/`+first.Header().Get("ETag"))
		second := httptest.NewRecorder()
		router.ServeHTTP(second, req)
		if second.Code != http.StatusNotModified || second.Body.Len() != 0 {
			t.Fatalf("expected empty 304: %d", second.Code)
		}
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/public/leaderboards?cursor=invalid", nil))
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("error response is cacheable")
	}
}

func TestAvatarRevalidationChecksRemoval(t *testing.T) {
	router, _, st := setupTestRouter(t)
	ctx := context.Background()
	now := time.Now().UTC()
	_, _, _ = st.SeedUserForTest("cache-avatar", "cache-avatar", "cache-avatar@example.test", now)
	svc := media.NewService(st, config.DefaultConfig(), clock.NewMockClock(now), testStorage)
	var original bytes.Buffer
	_ = png.Encode(&original, image.NewNRGBA(image.Rect(0, 0, 1024, 512)))
	hash := sha256.Sum256(original.Bytes())
	intent, err := svc.CreateAvatarIntent(ctx, "cache-avatar", media.CreateAvatarIntentInput{ContentType: "image/png", ByteSize: uint64(original.Len()), Sha256: hex.EncodeToString(hash[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UploadAvatarContent(ctx, intent.ObjectID, "cache-avatar", bytes.NewReader(original.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteAvatarIntent(ctx, intent.ObjectID, "cache-avatar"); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/public/avatars/" + intent.ObjectID
	first := httptest.NewRecorder()
	router.ServeHTTP(first, httptest.NewRequest("GET", path, nil))
	if first.Code != 200 || first.Header().Get("Cache-Control") != "private, max-age=604800, immutable" {
		t.Fatalf("avatar response %d %v", first.Code, first.Header())
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(first.Body.Bytes()))
	if err != nil || cfg.Width != 256 || cfg.Height != 128 {
		t.Fatalf("not a thumbnail: %v %v", cfg, err)
	}
	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("If-None-Match", first.Header().Get("ETag"))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if rec := request(); rec.Code != 304 || rec.Body.Len() != 0 {
		t.Fatal("avatar was downloaded again")
	}
	if err := svc.ClearAvatar(ctx, "cache-avatar"); err != nil {
		t.Fatal(err)
	}
	if rec := request(); rec.Code != 404 || rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("removed avatar returned from cache")
	}
}
