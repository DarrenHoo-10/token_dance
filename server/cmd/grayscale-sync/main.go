// Command grayscale-sync copies the current all-time top N production users
// into the test database so the test service can grayscale against real
// leaderboard and personal-analytics shapes.
//
// It is a dual-DSN ops job, not part of the production worker. Source is
// read-only; the target must be tokendance_dev (or another non-prod schema).
// Auth secrets, sessions, device keys, emails and raw usage_events are not
// copied. Device public keys are replaced with deterministic test keys.
//
//	TOKENDANCE_SOURCE_MYSQL_DSN or TOKENDANCE_SOURCE_MYSQL_DSN_FILE
//	TOKENDANCE_MYSQL_DSN or TOKENDANCE_MYSQL_DSN_FILE
//	TOKENDANCE_SOURCE_SCHEMA=tokendance_prod
//	TOKENDANCE_TARGET_SCHEMA=tokendance_dev
//	TOKENDANCE_GRAYSCALE_LOOP=true          # keep copying on an interval
//	TOKENDANCE_GRAYSCALE_INTERVAL=5m
//	TOKENDANCE_GRAYSCALE_LIMIT=10
//	TOKENDANCE_GRAYSCALE_PASSWORD=          # optional shared test login
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tokendance/internal/grayscale"
	"tokendance/internal/store/mysql"
)

func main() {
	loop := flag.Bool("loop", envBool("TOKENDANCE_GRAYSCALE_LOOP"), "copy on an interval until interrupted")
	flag.Parse()

	cfg := grayscale.Config{
		SourceDSN:    requiredDSN("TOKENDANCE_SOURCE_MYSQL_DSN", "TOKENDANCE_SOURCE_MYSQL_DSN_FILE"),
		TargetDSN:    requiredDSN("TOKENDANCE_MYSQL_DSN", "TOKENDANCE_MYSQL_DSN_FILE"),
		SourceSchema: envOr("TOKENDANCE_SOURCE_SCHEMA", grayscale.DefaultSourceDB),
		TargetSchema: envOr("TOKENDANCE_TARGET_SCHEMA", grayscale.DefaultTargetDB),
		Limit:        envInt("TOKENDANCE_GRAYSCALE_LIMIT", grayscale.DefaultLimit),
		RefreshDays:  envInt("TOKENDANCE_GRAYSCALE_REFRESH_DAYS", grayscale.DefaultRefreshDays),
		Interval:     envDuration("TOKENDANCE_GRAYSCALE_INTERVAL", grayscale.DefaultInterval),
		Loop:         *loop,
		Password:     strings.TrimSpace(os.Getenv("TOKENDANCE_GRAYSCALE_PASSWORD")),
		AllowTarget:  envBool("TOKENDANCE_GRAYSCALE_ALLOW_TARGET"),
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	source, err := mysql.OpenDB(cfg.SourceDSN, mysql.DBConfig{
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: 15 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
		PingTimeout:     10 * time.Second,
	})
	if err != nil {
		log.Fatalf("open source mysql: %v", err)
	}
	defer source.Close()
	target, err := mysql.OpenDB(cfg.TargetDSN, mysql.DBConfig{
		MaxOpenConns:    8,
		MaxIdleConns:    2,
		ConnMaxLifetime: 15 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
		PingTimeout:     10 * time.Second,
	})
	if err != nil {
		log.Fatalf("open target mysql: %v", err)
	}
	defer target.Close()

	mirror, err := grayscale.New(cfg, source, target)
	if err != nil {
		log.Fatalf("grayscale config: %v", err)
	}
	log.Printf("grayscale mirror %s -> %s limit=%d loop=%v interval=%s", cfg.SourceSchema, cfg.TargetSchema, cfg.Limit, cfg.Loop, cfg.Interval)
	if err := mirror.RunLoop(ctx); err != nil && err != context.Canceled {
		log.Fatalf("grayscale mirror: %v", err)
	}
}

func requiredDSN(valueName, fileName string) string {
	if path := strings.TrimSpace(os.Getenv(fileName)); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			log.Fatalf("read %s: %v", fileName, err)
		}
		if value := strings.TrimSpace(string(data)); value != "" {
			return value
		}
	}
	value := strings.TrimSpace(os.Getenv(valueName))
	if value == "" {
		log.Fatalf("%s or %s is required", valueName, fileName)
	}
	return value
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	return value == "1" || value == "true" || value == "yes"
}

func envInt(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		log.Fatalf("%s must be a positive integer", name)
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		log.Fatalf("%s must be a positive duration", name)
	}
	return value
}
