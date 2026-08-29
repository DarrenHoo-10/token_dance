package redisstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
)

const (
	defaultLeaderboardTTL = 5 * time.Minute
	leaderboardVersionKey = "tokenshow:leaderboard:version"
	leaderboardKeyPrefix  = "tokenshow:leaderboard:v"
)

const getVersionedLeaderboardValue = `
local version = redis.call('GET', KEYS[1]) or '0'
return redis.call('GET', ARGV[1] .. version .. ':' .. ARGV[2])
`

const setVersionedLeaderboardValue = `
local version = redis.call('GET', KEYS[1]) or '0'
return redis.call('SET', ARGV[1] .. version .. ':' .. ARGV[2], ARGV[3], 'PX', ARGV[4])
`

var (
	_ store.NonceCache       = (*Cache)(nil)
	_ store.LeaderboardCache = (*Cache)(nil)
)

// Cache implements the server Redis caches with go-redis.
type Cache struct {
	client         *redis.Client
	leaderboardTTL time.Duration
}

func New(addr string) *Cache {
	return &Cache{
		client: redis.NewClient(&redis.Options{
			Addr:               addr,
			DialTimeout:        500 * time.Millisecond,
			DialerRetries:      1,
			DialerRetryTimeout: 10 * time.Millisecond,
			ReadTimeout:        500 * time.Millisecond,
			WriteTimeout:       500 * time.Millisecond,
			MaxRetries:         -1,
		}),
		leaderboardTTL: defaultLeaderboardTTL,
	}
}

func (c *Cache) Close() error {
	return c.client.Close()
}

func (c *Cache) Ping(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

func (c *Cache) ConsumeNonce(ctx context.Context, installationID string, nonceHash [32]byte, expiresAt time.Time) (bool, error) {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return false, nil
	}
	return c.client.SetNX(ctx, nonceKey(installationID, nonceHash), "1", ttl).Result()
}

func (c *Cache) PruneExpired(context.Context, time.Time) (int, error) {
	return 0, nil
}

func (c *Cache) GetSnapshot(ctx context.Context, snapshotID string) (*domain.LeaderboardSnapshot, error) {
	var snapshot domain.LeaderboardSnapshot
	if err := c.getJSON(ctx, snapshotKey(snapshotID), &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (c *Cache) SetSnapshot(ctx context.Context, snapshot *domain.LeaderboardSnapshot) error {
	return c.setJSON(ctx, snapshotKey(snapshot.SnapshotID), snapshot)
}

func (c *Cache) GetLatest(ctx context.Context, boardKey, scopeType, scopeKey, metricKey string) (*domain.LeaderboardSnapshot, error) {
	var snapshot domain.LeaderboardSnapshot
	if err := c.getJSON(ctx, latestKey(boardKey, scopeType, scopeKey, metricKey), &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (c *Cache) SetLatest(ctx context.Context, boardKey, scopeType, scopeKey, metricKey string, snapshot *domain.LeaderboardSnapshot) error {
	return c.setJSON(ctx, latestKey(boardKey, scopeType, scopeKey, metricKey), snapshot)
}

func (c *Cache) GetEntries(ctx context.Context, snapshotID string) ([]*domain.LeaderboardEntry, error) {
	var entries []*domain.LeaderboardEntry
	if err := c.getJSON(ctx, entriesKey(snapshotID), &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (c *Cache) SetEntries(ctx context.Context, snapshotID string, entries []*domain.LeaderboardEntry) error {
	return c.setJSON(ctx, entriesKey(snapshotID), entries)
}

func (c *Cache) getJSON(ctx context.Context, key string, destination any) error {
	result, err := c.client.Eval(ctx, getVersionedLeaderboardValue, []string{leaderboardVersionKey}, leaderboardKeyPrefix, key).Result()
	if err == redis.Nil || result == nil {
		return store.ErrCacheMiss
	}
	if err != nil {
		return err
	}
	value, ok := result.(string)
	if !ok {
		return fmt.Errorf("unexpected Redis value type %T for %s", result, key)
	}
	if err := json.Unmarshal([]byte(value), destination); err != nil {
		return fmt.Errorf("decode Redis value %s: %w", key, err)
	}
	return nil
}

func (c *Cache) setJSON(ctx context.Context, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	ttlMillis := c.leaderboardTTL.Milliseconds()
	if ttlMillis <= 0 {
		ttlMillis = 1
	}
	return c.client.Eval(ctx, setVersionedLeaderboardValue, []string{leaderboardVersionKey}, leaderboardKeyPrefix, key, encoded, ttlMillis).Err()
}

func (c *Cache) InvalidateLeaderboard(ctx context.Context) error {
	return c.client.Incr(ctx, leaderboardVersionKey).Err()
}

func nonceKey(installationID string, nonceHash [32]byte) string {
	return "tokenshow:nonce:" + installationID + ":" + hex.EncodeToString(nonceHash[:])
}

func snapshotKey(snapshotID string) string {
	return "tokenshow:leaderboard:snapshot:" + snapshotID
}

func entriesKey(snapshotID string) string {
	return "tokenshow:leaderboard:entries:" + snapshotID
}

func latestKey(boardKey, scopeType, scopeKey, metricKey string) string {
	digest := sha256.Sum256([]byte(boardKey + "\x00" + scopeType + "\x00" + scopeKey + "\x00" + metricKey))
	return "tokenshow:leaderboard:latest:" + hex.EncodeToString(digest[:])
}
