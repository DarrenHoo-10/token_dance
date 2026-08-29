package redisstore_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/tokendance/token-collector/server/internal/api"
	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
	"github.com/tokendance/token-collector/server/internal/store/redisstore"
)

func TestRedisNormalThenOutageFallsBackToMySQL(t *testing.T) {
	if os.Getenv("TEST_REDIS_FAILOVER") != "1" {
		t.Skip("set TEST_REDIS_FAILOVER=1 to run the container outage test")
	}

	ctx := context.Background()
	mysqlStore := resetMySQL(t)
	cache := redisstore.New(redisAddress())
	defer cache.Close()
	if err := cache.Ping(ctx); err != nil {
		t.Fatalf("Redis unavailable at %s: %v", redisAddress(), err)
	}

	userID, installationID := testID(1), testID(2)
	createUserAndInstallation(t, mysqlStore, userID, installationID)
	nonces := &store.FallbackNonceStore{Cache: cache, Fallback: mysqlStore}
	leaderboards := &store.FallbackLeaderboardStore{Cache: cache, Fallback: mysqlStore}

	nonceHash := sha256.Sum256([]byte(fmt.Sprintf("durable-first-%d", time.Now().UnixNano())))
	expiresAt := time.Now().UTC().Add(time.Minute)
	if fresh, err := cache.ConsumeNonce(ctx, installationID, nonceHash, expiresAt); err != nil || !fresh {
		t.Fatalf("seed Redis nonce: fresh=%v err=%v", fresh, err)
	}
	if fresh, err := nonces.ConsumeNonce(ctx, installationID, nonceHash, expiresAt); err != nil || !fresh {
		t.Fatalf("durable-first nonce rejected by stale Redis: fresh=%v err=%v", fresh, err)
	}

	snapshot := createPublishedLeaderboard(t, mysqlStore, userID)
	got, err := leaderboards.LatestPublishedSnapshotScoped(ctx, snapshot.BoardKey, snapshot.ScopeType, snapshot.ScopeKey, snapshot.MetricKey)
	if err != nil || got.SnapshotID != snapshot.SnapshotID {
		t.Fatalf("Redis-backed leaderboard lookup: snapshot=%+v err=%v", got, err)
	}
	entries, err := leaderboards.ListEntries(ctx, snapshot.SnapshotID)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Redis-backed entries lookup: count=%d err=%v", len(entries), err)
	}
	if _, err := cache.GetLatest(ctx, snapshot.BoardKey, snapshot.ScopeType, snapshot.ScopeKey, snapshot.MetricKey); err != nil {
		t.Fatalf("latest snapshot was not cached: %v", err)
	}
	if _, err := cache.GetEntries(ctx, snapshot.SnapshotID); err != nil {
		t.Fatalf("leaderboard entries were not cached: %v", err)
	}

	stopRedisContainer(t)
	defer startRedisContainer(t)

	if fresh, err := nonces.ConsumeNonce(ctx, installationID, nonceHash, expiresAt); err != nil || fresh {
		t.Fatalf("replay accepted during Redis outage: fresh=%v err=%v", fresh, err)
	}
	got, err = leaderboards.LatestPublishedSnapshotScoped(ctx, snapshot.BoardKey, snapshot.ScopeType, snapshot.ScopeKey, snapshot.MetricKey)
	if err != nil || got.SnapshotID != snapshot.SnapshotID {
		t.Fatalf("leaderboard did not fall back to MySQL: snapshot=%+v err=%v", got, err)
	}
	entries, err = leaderboards.ListEntries(ctx, snapshot.SnapshotID)
	if err != nil || len(entries) != 1 || entries[0].DisplayNameSnapshot != "Redis Fallback User" {
		t.Fatalf("entries did not fall back to MySQL: entries=%+v err=%v", entries, err)
	}
}

func TestLeaderboardVersionInvalidationAndDeletedAccountSnapshot(t *testing.T) {
	if os.Getenv("TEST_REDIS_FAILOVER") != "1" {
		t.Skip("set TEST_REDIS_FAILOVER=1 to run the Redis integration test")
	}

	ctx := context.Background()
	mysqlStore := resetMySQL(t)
	cache := redisstore.New(redisAddress())
	defer cache.Close()
	if err := cache.Ping(ctx); err != nil {
		t.Fatalf("Redis unavailable at %s: %v", redisAddress(), err)
	}
	userID, installationID := testID(10), testID(11)
	createUserAndInstallation(t, mysqlStore, userID, installationID)
	leaderboards := &store.FallbackLeaderboardStore{Cache: cache, Fallback: mysqlStore}
	now := time.Now().UTC().Truncate(time.Millisecond)

	publishID := testID(12)
	publishSnapshot := &domain.LeaderboardSnapshot{SnapshotID: publishID, BoardKey: "publish", ScopeType: "global", ScopeKey: "all", MetricKey: "total_tokens", WindowStart: now.Add(-time.Hour), WindowEnd: now, TimezoneName: "UTC", RankingRuleVersion: 2, DataWatermarkAt: now, SnapshotStatus: "building", GeneratedAt: now}
	if err := leaderboards.CreateSnapshot(ctx, publishSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetSnapshot(ctx, publishSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := leaderboards.PublishSnapshot(ctx, publishID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GetSnapshot(ctx, publishID); !errors.Is(err, store.ErrCacheMiss) {
		t.Fatalf("publish did not invalidate prior cache generation: %v", err)
	}

	published, err := leaderboards.GetSnapshot(ctx, publishID)
	if err != nil || published.SnapshotStatus != "published" {
		t.Fatalf("published snapshot=%+v err=%v", published, err)
	}
	if err := leaderboards.SupersedeSnapshot(ctx, publishID); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GetSnapshot(ctx, publishID); !errors.Is(err, store.ErrCacheMiss) {
		t.Fatalf("supersede did not invalidate prior cache generation: %v", err)
	}

	accountSnapshot := createPublishedLeaderboard(t, mysqlStore, userID)
	if _, err := leaderboards.GetSnapshot(ctx, accountSnapshot.SnapshotID); err != nil {
		t.Fatal(err)
	}
	request := mysqlstore.DeletionRequest{RequestID: testID(13), UserID: userID, Scope: "account", RequestedAt: now}
	if err := mysqlStore.CreateDeletionRequest(ctx, request); err != nil {
		t.Fatal(err)
	}
	if processed, err := mysqlStore.ProcessNextDeletion(ctx, "daily_metrics_committed_v2"); err != nil || processed != request.RequestID {
		t.Fatalf("process account deletion=%q err=%v", processed, err)
	}
	if err := cache.InvalidateLeaderboard(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.GetSnapshot(ctx, accountSnapshot.SnapshotID); !errors.Is(err, store.ErrCacheMiss) {
		t.Fatalf("account deletion did not invalidate old snapshot cache: %v", err)
	}

	handler := &api.Handler{Leaderboards: leaderboards}
	response := httptest.NewRecorder()
	handler.HandleGetSnapshot(response, httptest.NewRequest(http.MethodGet, "/v1/leaderboard/snapshots?id="+accountSnapshot.SnapshotID, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("deleted account old snapshot status=%d body=%s", response.Code, response.Body.String())
	}
}

func redisAddress() string {
	if addr := os.Getenv("TEST_REDIS_ADDR"); addr != "" {
		return addr
	}
	return "127.0.0.1:36379"
}

func resetMySQL(t *testing.T) *mysqlstore.Store {
	t.Helper()
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = "root:tokenshow_test_root@tcp(127.0.0.1:33068)/?charset=utf8mb4"
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS tokenshow`); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	admin.Close()
	mysqlStore, err := mysqlstore.Open(context.Background(), dsn, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mysqlStore.Close() })
	return mysqlStore
}

func createUserAndInstallation(t *testing.T, mysqlStore *mysqlstore.Store, userID, installationID string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	authHash := sha256.Sum256([]byte(userID))
	if err := mysqlStore.CreateUser(context.Background(), &domain.User{UserID: userID, AuthSubjectHash: authHash, DisplayName: "Redis Fallback User", AccountStatus: "active", LeaderboardVisibility: "public", TimezoneName: "UTC", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := mysqlStore.CreateInstallation(context.Background(), &domain.Installation{InstallationID: installationID, UserID: userID, DevicePublicKey: publicKey, OSType: "windows", Architecture: "x86_64", CollectorVersion: "1.0.0", InstallationStatus: "active", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
}

func createPublishedLeaderboard(t *testing.T, mysqlStore *mysqlstore.Store, userID string) *domain.LeaderboardSnapshot {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	snapshot := &domain.LeaderboardSnapshot{SnapshotID: testID(3), BoardKey: "today", ScopeType: "global", ScopeKey: "all", MetricKey: "total_tokens", WindowStart: now.Add(-24 * time.Hour), WindowEnd: now, TimezoneName: "UTC", RankingRuleVersion: 2, DataWatermarkAt: now, SnapshotStatus: "building", GeneratedAt: now}
	if err := mysqlStore.CreateSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := mysqlStore.CreateEntry(ctx, &domain.LeaderboardEntry{SnapshotID: snapshot.SnapshotID, RankNo: 1, UserID: userID, MetricValue: 42, DisplayNameSnapshot: "Redis Fallback User"}); err != nil {
		t.Fatal(err)
	}
	if err := mysqlStore.PublishSnapshot(ctx, snapshot.SnapshotID, now); err != nil {
		t.Fatal(err)
	}
	published, err := mysqlStore.GetSnapshot(ctx, snapshot.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	return published
}

func stopRedisContainer(t *testing.T) {
	t.Helper()
	if output, err := exec.Command("docker", "stop", "tokenshow-redis-test").CombinedOutput(); err != nil {
		t.Fatalf("stop tokenshow-redis-test: %v: %s", err, output)
	}
}

func startRedisContainer(t *testing.T) {
	t.Helper()
	if output, err := exec.Command("docker", "start", "tokenshow-redis-test").CombinedOutput(); err != nil {
		t.Fatalf("start tokenshow-redis-test: %v: %s", err, output)
	}
	probe := redisstore.New(redisAddress())
	defer probe.Close()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if probe.Ping(context.Background()) == nil {
			return
		}
	}
	t.Fatalf("tokenshow-redis-test did not become ready at %s", redisAddress())
}

func testID(value int) string {
	return fmt.Sprintf("%030d", value)
}
