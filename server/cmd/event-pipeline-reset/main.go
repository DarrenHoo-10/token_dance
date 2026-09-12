// Command event-pipeline-reset performs the P8 closed-beta empty-DB stats reset.
// It never truncates auth/device/desktop_releases tables. Idempotent per generation.
//
//	go run ./cmd/event-pipeline-reset --dry-run
//	go run ./cmd/event-pipeline-reset --confirm
//	go run ./cmd/event-pipeline-reset --confirm --force   # closed-beta re-wipe only
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"tokendance/internal/config"
	"tokendance/internal/rollout"
	"tokendance/internal/store/mysql"
	"tokendance/internal/store/redisx"
)

func main() {
	dryRun := flag.Bool("dry-run", false, "print plan without mutating")
	confirm := flag.Bool("confirm", false, "required to mutate (or set --dry-run)")
	force := flag.Bool("force", false, "re-run reset even if generation already ready (closed-beta only)")
	allowMissing := flag.Bool("allow-missing", false, "skip tables that are not present yet")
	skipRedis := flag.Bool("skip-redis", false, "do not clear ranking/community redis prefixes")
	markRollback := flag.Bool("mark-rollback", false, "mark rollout phase rolled_back without wiping")
	flag.Parse()

	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.MySQLDSN == "" {
		log.Fatal("TOKENDANCE_MYSQL_DSN required")
	}
	if cfg.Environment == "production" && *force {
		log.Fatal("refusing --force in production; closed-beta re-wipe only")
	}

	db, err := mysql.OpenDB(cfg.MySQLDSN, mysql.DefaultDBConfig())
	if err != nil {
		log.Fatalf("mysql: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if *markRollback {
		if !*confirm {
			log.Fatal("--mark-rollback requires --confirm")
		}
		if err := rollout.MarkRolledBack(ctx, db, map[string]any{
			"reason": "operator_rollback",
			"at":     time.Now().UTC().Format(time.RFC3339),
		}); err != nil {
			log.Fatalf("mark rollback: %v", err)
		}
		fmt.Println(`{"phase":"rolled_back"}`)
		return
	}

	if !*dryRun && !*confirm {
		log.Fatal("refusing to mutate without --confirm (or pass --dry-run)")
	}

	var rdb *redis.Client
	if !*skipRedis && cfg.RedisConfigured() {
		rdb, err = redisx.OpenClient(cfg, redisx.DefaultClientConfig())
		if err != nil {
			log.Fatalf("redis: %v", err)
		}
		defer rdb.Close()
	}

	result, err := rollout.ResetStats(ctx, db, rollout.ResetOptions{
		TargetGeneration: rollout.ClosedBetaGeneration,
		Force:            *force,
		AllowMissing:     *allowMissing,
		DryRun:           *dryRun,
		Notes: map[string]any{
			"operator": os.Getenv("USER"),
			"at":       time.Now().UTC().Format(time.RFC3339),
			"force":    *force,
		},
		AfterTruncate: func(ctx context.Context) error {
			if rdb == nil {
				return nil
			}
			return clearRedisPrefixes(ctx, rdb, rollout.RedisStatsKeyPrefixes)
		},
	})
	if err != nil {
		log.Fatalf("reset: %v", err)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)

	if !*dryRun && !result.AlreadyReady {
		if err := rollout.AssertNoLegacyStatsResidual(ctx, db); err != nil {
			log.Fatalf("post-reset residual check: %v", err)
		}
		if snap, err := rollout.CollectMonitorSnapshot(ctx, db); err == nil {
			_ = enc.Encode(snap)
		}
	}
}

func clearRedisPrefixes(ctx context.Context, rdb *redis.Client, prefixes []string) error {
	for _, prefix := range prefixes {
		var cursor uint64
		for {
			keys, next, err := rdb.Scan(ctx, cursor, prefix+"*", 200).Result()
			if err != nil {
				return fmt.Errorf("scan %s: %w", prefix, err)
			}
			if len(keys) > 0 {
				if err := rdb.Del(ctx, keys...).Err(); err != nil {
					return fmt.Errorf("del %s: %w", strings.Join(keys, ","), err)
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return nil
}
