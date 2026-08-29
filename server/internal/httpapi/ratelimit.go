package httpapi

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

type rateLimitBackend interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error)
}

type rateLimitRecord struct {
	timestamps []time.Time
	lastSeen   time.Time
}

type memoryRateLimitBackend struct {
	mu         sync.Mutex
	records    map[string]rateLimitRecord
	maxEntries int
}

func newMemoryRateLimitBackend(maxEntries int) *memoryRateLimitBackend {
	return &memoryRateLimitBackend{records: make(map[string]rateLimitRecord), maxEntries: maxEntries}
}

func (b *memoryRateLimitBackend) evictOldest() {
	var oldestKey string
	var oldestTime time.Time
	for key, record := range b.records {
		if oldestKey == "" || record.lastSeen.Before(oldestTime) {
			oldestKey, oldestTime = key, record.lastSeen
		}
	}
	if oldestKey != "" {
		delete(b.records, oldestKey)
	}
}

func (b *memoryRateLimitBackend) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-window)
	record, exists := b.records[key]
	if !exists && len(b.records) >= b.maxEntries {
		b.evictOldest()
	}
	valid := record.timestamps[:0]
	for _, timestamp := range record.timestamps {
		if timestamp.After(cutoff) {
			valid = append(valid, timestamp)
		}
	}
	record.timestamps = valid
	record.lastSeen = now
	if len(record.timestamps) >= limit {
		b.records[key] = record
		return false, nil
	}
	record.timestamps = append(record.timestamps, now)
	b.records[key] = record
	return true, nil
}

type redisRateLimitBackend struct {
	client *redis.Client
}

var redisRateLimitScript = redis.NewScript(`
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return count
`)

func (b *redisRateLimitBackend) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	count, err := redisRateLimitScript.Run(ctx, b.client, []string{"tokendance:ratelimit:" + key}, window.Milliseconds()).Int64()
	if err != nil {
		return false, err
	}
	return count <= int64(limit), nil
}

type fallbackRateLimitBackend struct {
	primary  rateLimitBackend
	fallback rateLimitBackend
}

func (b fallbackRateLimitBackend) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	allowed, err := b.primary.Allow(ctx, key, limit, window)
	if err == nil {
		return allowed, nil
	}
	allowed, fallbackErr := b.fallback.Allow(ctx, key, limit, window)
	if fallbackErr != nil {
		return false, fmt.Errorf("primary rate limiter: %v; fallback rate limiter: %w", err, fallbackErr)
	}
	return allowed, nil
}
