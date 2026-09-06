package ranking

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func openTestRedis(t *testing.T) *redis.Client {
	t.Helper()
	if u := strings.TrimSpace(os.Getenv("TOKENDANCE_TEST_REDIS_URL")); u != "" {
		opt, err := redis.ParseURL(u)
		if err != nil {
			t.Fatalf("TOKENDANCE_TEST_REDIS_URL: %v", err)
		}
		client := redis.NewClient(opt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			_ = client.Close()
			t.Fatalf("test redis ping: %v", err)
		}
		t.Cleanup(func() {
			flushRankingKeys(t, client)
			_ = client.Close()
		})
		return client
	}
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func flushRankingKeys(t *testing.T, client *redis.Client) {
	t.Helper()
	ctx := context.Background()
	for _, window := range Windows {
		keys, err := client.Keys(ctx, WindowPrefix(window)+"*").Result()
		if err != nil {
			t.Fatalf("list ranking keys: %v", err)
		}
		if len(keys) > 0 {
			if err := client.Del(ctx, keys...).Err(); err != nil {
				t.Fatalf("delete ranking keys: %v", err)
			}
		}
	}
}

func testIndex(t *testing.T) *Index {
	t.Helper()
	return NewIndex(openTestRedis(t))
}
