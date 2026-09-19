package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/provider"
	"tokendance/internal/store/memory"
)

func TestThumbnailBoundsAndTransparency(t *testing.T) {
	for _, transparent := range []bool{false, true} {
		src := image.NewNRGBA(image.Rect(0, 0, 1024, 512))
		for y := 0; y < 512; y++ {
			for x := 0; x < 1024; x++ {
				a := uint8(255)
				if transparent {
					a = 100
				}
				src.SetNRGBA(x, y, color.NRGBA{uint8(x*17 + y), uint8(y*19 + x), uint8(x * y), a})
			}
		}
		var original bytes.Buffer
		if err := png.Encode(&original, src); err != nil {
			t.Fatal(err)
		}
		data, ct, err := thumbnail(original.Bytes(), 4096*4096)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 128 {
			t.Fatalf("bad dimensions: %v", img.Bounds())
		}
		if len(data) >= original.Len() {
			t.Fatalf("thumbnail not smaller: %d >= %d", len(data), original.Len())
		}
		_, _, _, alpha := img.At(20, 20).RGBA()
		if transparent && (ct != "image/png" || alpha == 65535) {
			t.Fatal("lost transparency")
		}
		if !transparent && ct != "image/jpeg" {
			t.Fatalf("opaque image type: %s", ct)
		}
	}
	if _, _, err := thumbnail(createTestPNG(100, 100), 5000); err == nil {
		t.Fatal("pixel limit ignored")
	}
}

type countingStorage struct {
	provider.ObjectStorage
	reads atomic.Int32
}

func (s *countingStorage) OpenObject(ctx context.Context, key string) (io.ReadCloser, error) {
	s.reads.Add(1)
	return s.ObjectStorage.OpenObject(ctx, key)
}

func TestAvatarCacheCoalescesReadsAndStillChecksVisibility(t *testing.T) {
	ctx := context.Background()
	st := memory.NewMemoryStore()
	now := time.Now().UTC()
	_, _, _ = st.SeedUserForTest("thumb-user", "thumb", "thumb@example.test", now)
	storage := &countingStorage{ObjectStorage: provider.NewMemoryObjectStorage("")}
	svc := NewService(st, config.DefaultConfig(), clock.NewMockClock(now), storage)
	data := createTestPNG(1024, 512)
	hash := sha256.Sum256(data)
	intent, err := svc.CreateAvatarIntent(ctx, "thumb-user", CreateAvatarIntentInput{ContentType: "image/png", ByteSize: uint64(len(data)), Sha256: hex.EncodeToString(hash[:])})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UploadAvatarContent(ctx, intent.ObjectID, "thumb-user", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteAvatarIntent(ctx, intent.ObjectID, "thumb-user"); err != nil {
		t.Fatal(err)
	}
	storage.reads.Store(0)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _, err := svc.ReadAvatar(ctx, intent.ObjectID, "")
			if err != nil || len(b) == 0 {
				t.Errorf("read failed: %v", err)
			}
		}()
	}
	wg.Wait()
	if storage.reads.Load() != 1 {
		t.Fatalf("object reads: %d", storage.reads.Load())
	}
	// Emulate an upload from before thumbnails existed and a fresh API process.
	obj, err := st.Media().GetUploadObject(ctx, intent.ObjectID, "thumb-user")
	if err != nil {
		t.Fatal(err)
	}
	if err := storage.DeleteObject(ctx, avatarThumbnailKey(obj.ObjectKey)); err != nil {
		t.Fatal(err)
	}
	legacyService := NewService(st, config.DefaultConfig(), clock.NewMockClock(now), storage)
	storage.reads.Store(0)
	legacyData, _, err := legacyService.ReadAvatar(ctx, intent.ObjectID, "")
	if err != nil {
		t.Fatal(err)
	}
	legacyConfig, _, err := image.DecodeConfig(bytes.NewReader(legacyData))
	if err != nil || legacyConfig.Width != 256 || legacyConfig.Height != 128 {
		t.Fatalf("legacy avatar not compressed: %v %v", legacyConfig, err)
	}
	if storage.reads.Load() != 2 {
		t.Fatalf("legacy reads: %d", storage.reads.Load())
	}
	if _, _, err := legacyService.ReadAvatar(ctx, intent.ObjectID, ""); err != nil {
		t.Fatal(err)
	}
	if storage.reads.Load() != 2 {
		t.Fatal("legacy image was not cached")
	}
	if err := svc.ClearAvatar(ctx, "thumb-user"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ReadAvatar(ctx, intent.ObjectID, ""); err == nil {
		t.Fatal("cached removed avatar remains visible")
	}
}

func TestAvatarCacheEvictsLeastRecentlyUsed(t *testing.T) {
	var c avatarCache
	for i := 0; i < avatarCacheEntries; i++ {
		c.put(avatarImage{key: string(rune(i)), data: []byte{1}})
	}
	_, _ = c.get(string(rune(0)))
	c.put(avatarImage{key: "new", data: []byte{2}})
	if _, ok := c.get(string(rune(1))); ok {
		t.Fatal("oldest entry not evicted")
	}
	if _, ok := c.get(string(rune(0))); !ok {
		t.Fatal("recently read entry evicted")
	}
	c.put(avatarImage{key: "large", data: make([]byte, avatarCacheBytes)})
	if c.bytes != avatarCacheBytes || c.lru.Len() != 1 {
		t.Fatal("byte budget exceeded")
	}
}
