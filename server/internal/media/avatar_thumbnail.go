package media

import (
	"bytes"
	"container/list"
	"fmt"
	"hash/fnv"
	"image"
	"image/jpeg"
	"image/png"
	"sync"

	"golang.org/x/image/draw"
)

const avatarEdge = 256
const avatarCacheBytes = 16 << 20
const avatarCacheEntries = 256

// AvatarThumbnailSuffix is also used by account deletion to remove derived files.
const AvatarThumbnailSuffix = "/thumbnail-v1"

func avatarThumbnailKey(originalKey string) string { return originalKey + AvatarThumbnailSuffix }

type avatarImage struct {
	key         string
	data        []byte
	contentType string
}

// Only image bytes are cached. Visibility is checked in the store on every read.
// Bounded striped locks coalesce concurrent reads and limit parallel decoding.
type avatarCache struct {
	mu      sync.Mutex
	loads   [16]sync.Mutex
	entries map[string]*list.Element
	lru     list.List
	bytes   int
}

func (c *avatarCache) loadLock(key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return &c.loads[h.Sum32()%uint32(len(c.loads))]
}

func (c *avatarCache) get(key string) (avatarImage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		c.lru.MoveToFront(e)
		return e.Value.(avatarImage), true
	}
	return avatarImage{}, false
}

func (c *avatarCache) put(v avatarImage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(v.data) > avatarCacheBytes {
		return
	}
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	if e := c.entries[v.key]; e != nil {
		c.bytes -= len(e.Value.(avatarImage).data)
		c.lru.Remove(e)
	}
	c.entries[v.key] = c.lru.PushFront(v)
	c.bytes += len(v.data)
	for c.bytes > avatarCacheBytes || c.lru.Len() > avatarCacheEntries {
		e := c.lru.Back()
		old := e.Value.(avatarImage)
		delete(c.entries, old.key)
		c.bytes -= len(old.data)
		c.lru.Remove(e)
	}
}

// Originals remain intact for upload checksum validation and future reprocessing.
// Every delivered avatar, including old uploads, uses this bounded representation.
func thumbnail(data []byte, maxPixels int64) ([]byte, string, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 4096 || cfg.Height > 4096 || int64(cfg.Width) > maxPixels/int64(cfg.Height) {
		return nil, "", fmt.Errorf("invalid avatar dimensions")
	}
	src, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", err
	}
	w, h := cfg.Width, cfg.Height
	if w > avatarEdge || h > avatarEdge {
		if w >= h {
			h = max(1, h*avatarEdge/w)
			w = avatarEdge
		} else {
			w = max(1, w*avatarEdge/h)
			h = avatarEdge
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var out bytes.Buffer
	contentType := "image/png"
	if dst.Opaque() {
		contentType = "image/jpeg"
		err = jpeg.Encode(&out, dst, &jpeg.Options{Quality: 80})
	} else {
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		err = encoder.Encode(&out, dst)
	}
	if err != nil {
		return nil, "", err
	}
	if w == cfg.Width && h == cfg.Height && len(data) <= out.Len() {
		return data, "image/" + format, nil
	}
	return out.Bytes(), contentType, nil
}
