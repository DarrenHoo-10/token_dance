package main

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/tokendance/token-collector/server/internal/aggregator"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
	"github.com/tokendance/token-collector/server/internal/store/redisstore"
)

const (
	aggregationWatermark = "daily_metrics_committed_v2"
	aggregationInterval  = 5 * time.Second
	todayInterval        = 60 * time.Second
	rollingInterval      = 5 * time.Minute
	allInterval          = 15 * time.Minute
	aggregationSafeLag   = 5 * time.Second
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		dsn = "root:tokenshow_test_root@tcp(127.0.0.1:33068)/?charset=utf8mb4"
	}
	s, err := mysqlstore.Open(context.Background(), dsn, true)
	if err != nil {
		log.Fatalf("open MySQL store: %v", err)
	}
	defer s.Close()

	var cache *redisstore.Cache
	if redisAddr := os.Getenv("REDIS_ADDR"); redisAddr != "" {
		cache = redisstore.New(redisAddr)
		defer cache.Close()
		if err := cache.Ping(context.Background()); err != nil {
			log.Printf("Redis cache unavailable at %s: %v", redisAddr, err)
		}
	}

	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: aggregationSafeLag}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	aggregationTicker := time.NewTicker(aggregationInterval)
	deletionTicker := time.NewTicker(time.Second)
	todayTicker := time.NewTicker(todayInterval)
	rollingTicker := time.NewTicker(rollingInterval)
	allTicker := time.NewTicker(allInterval)
	defer aggregationTicker.Stop()
	defer deletionTicker.Stop()
	defer todayTicker.Stop()
	defer rollingTicker.Stop()
	defer allTicker.Stop()

	log.Printf("TokenShow worker started (today=60s, 7d/30d=5m, all=15m)")
	runAggregation(ctx, worker)

	for {
		select {
		case <-ctx.Done():
			log.Println("worker shutting down")
			return
		case <-aggregationTicker.C:
			runAggregation(ctx, worker)
		case <-deletionTicker.C:
			requestID, err := s.ProcessNextDeletion(ctx, aggregationWatermark)
			if err != nil {
				log.Printf("deletion error: %v", err)
			} else if requestID != "" {
				if cache != nil {
					if err := cache.InvalidateLeaderboard(ctx); err != nil {
						log.Printf("leaderboard cache invalidation after deletion: %v", err)
					}
				}
				log.Printf("deletion completed: %s", requestID)
				buildBoards(ctx, worker, s, cache, "today", "7d", "30d", "all")
			}
		case <-todayTicker.C:
			buildBoards(ctx, worker, s, cache, "today")
		case <-rollingTicker.C:
			buildBoards(ctx, worker, s, cache, "7d", "30d")
		case <-allTicker.C:
			buildBoards(ctx, worker, s, cache, "all")
		}
	}
}

func runAggregation(ctx context.Context, worker *aggregator.Worker) {
	n, err := worker.RunOnce(ctx)
	if err != nil {
		log.Printf("aggregation error: %v", err)
	} else if n > 0 {
		log.Printf("aggregated %d committed events (watermark pk=%d)", n, worker.LastProcessedPK)
	}
}

func buildBoards(ctx context.Context, worker *aggregator.Worker, s *mysqlstore.Store, cache *redisstore.Cache, boards ...string) {
	scopes, err := s.ListLeaderboardScopes(ctx)
	if err != nil {
		log.Printf("list leaderboard scopes: %v", err)
		return
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, board := range boards {
		start := boardStart(board, now)
		for _, scope := range scopes {
			id := generateID()
			if err := worker.BuildLeaderboardSnapshotScoped(ctx, s, id, board, scope.Type, scope.Key, "total_tokens", start, now); err != nil {
				log.Printf("snapshot %s/%s/%s: %v", board, scope.Type, scope.Key, err)
				continue
			}
			if cache != nil {
				if err := cache.InvalidateLeaderboard(ctx); err != nil {
					log.Printf("leaderboard cache invalidation after publish: %v", err)
				}
			}
		}
	}
}

func boardStart(board string, now time.Time) time.Time {
	switch board {
	case "today":
		return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	case "7d":
		return now.AddDate(0, 0, -7)
	case "30d":
		return now.AddDate(0, 0, -30)
	default:
		return time.Unix(0, 0).UTC()
	}
}

func generateID() string {
	value := make([]byte, 18)
	if _, err := rand.Read(value); err != nil {
		panic(err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(value)[:30]
}
