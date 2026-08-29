package mysqlstore_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/tokendance/token-collector/server/internal/aggregator"
	serverapi "github.com/tokendance/token-collector/server/internal/api"
	"github.com/tokendance/token-collector/server/internal/auth"
	"github.com/tokendance/token-collector/server/internal/domain"
	"github.com/tokendance/token-collector/server/internal/store"
	"github.com/tokendance/token-collector/server/internal/store/mysqlstore"
)

func testDSN() string {
	if dsn := os.Getenv("TEST_MYSQL_DSN"); dsn != "" {
		return dsn
	}
	return "root:tokenshow_test_root@tcp(127.0.0.1:33068)/?charset=utf8mb4"
}

func resetStore(t *testing.T) *mysqlstore.Store {
	t.Helper()
	cfg, err := mysql.ParseDSN(testDSN())
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = ""
	cfg.MultiStatements = true
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatalf("MySQL integration database unavailable at TEST_MYSQL_DSN/default: %v", err)
	}
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS tokenshow`); err != nil {
		t.Fatal(err)
	}
	admin.Close()
	s, err := mysqlstore.Open(context.Background(), testDSN(), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func id(n int) string { return fmt.Sprintf("%030d", n) }

func createUserAndInstallation(t *testing.T, s *mysqlstore.Store, userID, installationID string) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	h := sha256.Sum256([]byte(userID))
	if err := s.CreateUser(ctx, &domain.User{UserID: userID, AuthSubjectHash: h, DisplayName: "User " + userID, AccountStatus: "active", LeaderboardVisibility: "public", TimezoneName: "UTC", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateInstallation(ctx, &domain.Installation{InstallationID: installationID, UserID: userID, DevicePublicKey: pub, OSType: "windows", Architecture: "x86_64", CollectorVersion: "1.0.0", InstallationStatus: "active", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func event(batchID, installationID, userID string, eventID [32]byte, tokens uint64) *domain.UsageEvent {
	now := time.Now().UTC().Truncate(time.Millisecond)
	source := sha256.Sum256([]byte("source" + batchID))
	raw := sha256.Sum256([]byte("raw" + batchID))
	return &domain.UsageEvent{EventID: eventID, SchemaVersion: "1.0.0", BatchID: batchID, InstallationID: installationID, UserID: userID, AdapterID: "test", AdapterVersion: "1.0.0", AgentID: "agent", EventType: "model_usage_recorded", Accuracy: "exact", SourceKind: "runtime_stream", SourceCursorHMAC: source, RawFingerprintHMAC: raw, OccurredAt: now, OccurredDate: now, ReceivedAt: now, TokenTotal: &tokens}
}

func batch(batchID, installationID string, hash [32]byte) *domain.IngestBatch {
	return &domain.IngestBatch{BatchID: batchID, InstallationID: installationID, RequestSHA256: hash, EventCount: 1, BatchStatus: "received", ReceivedAt: time.Now().UTC().Truncate(time.Millisecond)}
}

func TestMySQLStoreIntegrationChecklist(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	_, priv := createUserAndInstallation(t, s, id(1), id(2))

	nonce := []byte("0123456789abcdef")
	bodyHash := sha256.Sum256([]byte(`{"batch":1}`))
	hdr, err := auth.SignRequest(id(2), priv, "POST", "/v1/telemetry/batches", bodyHash, nonce)
	if err != nil {
		t.Fatal(err)
	}
	deviceAuth := &auth.DeviceAuth{Installations: s, Nonces: s}
	if _, err = deviceAuth.VerifyRequest(ctx, "POST", "/v1/telemetry/batches", bodyHash, hdr); err != nil {
		t.Fatalf("valid Ed25519 request rejected: %v", err)
	}
	if _, err = deviceAuth.VerifyRequest(ctx, "POST", "/v1/telemetry/batches", bodyHash, hdr); err == nil {
		t.Fatal("replayed nonce accepted")
	}

	batchID := id(3)
	requestHash := sha256.Sum256([]byte("same batch"))
	eventID := sha256.Sum256([]byte("same event"))
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, e := s.CommitBatch(ctx, batch(batchID, id(2), requestHash), []*store.IngestEvent{{Event: event(batchID, id(2), id(1), eventID, 100)}}, nil)
			if e == nil && (got.Batch.AcceptedCount != 1 || got.Batch.DuplicateCount != 0) {
				e = fmt.Errorf("unexpected stable result: %+v", got.Batch)
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	events, err := s.GetEventsByBatch(ctx, batchID)
	if err != nil || len(events) != 1 {
		t.Fatalf("concurrent batch was not idempotent: count=%d err=%v", len(events), err)
	}
	conflictHash := sha256.Sum256([]byte("different body"))
	if _, err = s.CommitBatch(ctx, batch(batchID, id(2), conflictHash), nil, nil); !errors.Is(err, store.ErrBatchHashConflict) {
		t.Fatalf("expected batch hash conflict, got %v", err)
	}

	batchA, batchB := id(4), id(5)
	sharedEvent := sha256.Sum256([]byte("cross-batch-event"))
	hashes := [][32]byte{sha256.Sum256([]byte("a")), sha256.Sum256([]byte("b"))}
	ids := []string{batchA, batchB}
	results := make(chan *store.IngestCommitResult, 2)
	errs = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, e := s.CommitBatch(ctx, batch(ids[i], id(2), hashes[i]), []*store.IngestEvent{{Event: event(ids[i], id(2), id(1), sharedEvent, 25)}}, nil)
			results <- v
			errs <- e
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var accepted, duplicates uint32
	acceptedBatch := ""
	for r := range results {
		accepted += r.Batch.AcceptedCount
		duplicates += r.Batch.DuplicateCount
		if r.Batch.AcceptedCount == 1 {
			acceptedBatch = r.Batch.BatchID
		}
	}
	if accepted != 1 || duplicates != 1 {
		t.Fatalf("event concurrency totals accepted=%d duplicates=%d", accepted, duplicates)
	}

	worker := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: -time.Nanosecond}
	if n, err := worker.RunOnce(ctx); err != nil || n == 0 {
		t.Fatalf("aggregate: n=%d err=%v", n, err)
	}
	metrics, err := s.GetDailyMetrics(ctx, id(1), time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
	if err != nil || len(metrics) != 1 || metrics[0].ExactTokenTotal != 125 {
		t.Fatalf("unexpected aggregate: %+v err=%v", metrics, err)
	}
	worker2 := &aggregator.Worker{Events: s, Metrics: s, Users: s, Watermarks: s, SafeLag: -time.Nanosecond}
	if n, err := worker2.RunOnce(ctx); err != nil || n != 0 {
		t.Fatalf("persisted watermark not honored: n=%d err=%v", n, err)
	}
	if n, err := s.DeleteEventsByBatch(ctx, acceptedBatch); err != nil || n != 1 {
		t.Fatalf("delete batch: n=%d err=%v", n, err)
	}
	metrics, err = s.GetDailyMetrics(ctx, id(1), time.Now().Add(-24*time.Hour), time.Now().Add(24*time.Hour))
	if err != nil || len(metrics) != 1 || metrics[0].ExactTokenTotal != 100 {
		t.Fatalf("delete did not recompute metrics: %+v err=%v", metrics, err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	snap := &domain.LeaderboardSnapshot{SnapshotID: id(6), BoardKey: "default", ScopeType: "global", ScopeKey: "all", MetricKey: "total_tokens", WindowStart: now.Add(-24 * time.Hour), WindowEnd: now.Add(time.Hour), TimezoneName: "UTC", RankingRuleVersion: 1, SourceMaxEventPK: events[0].EventPK, DataWatermarkAt: now, SnapshotStatus: "building", GeneratedAt: now}
	if err = s.CreateSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateEntry(ctx, &domain.LeaderboardEntry{SnapshotID: id(6), RankNo: 1, UserID: id(1), MetricValue: 100, DisplayNameSnapshot: "Frozen Name"}); err != nil {
		t.Fatal(err)
	}
	if err = s.PublishSnapshot(ctx, id(6), now); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateEntry(ctx, &domain.LeaderboardEntry{SnapshotID: id(6), RankNo: 2, UserID: id(1), MetricValue: 1, DisplayNameSnapshot: "Mutation"}); err == nil {
		t.Fatal("published leaderboard accepted mutation")
	}
	gotSnap, err := s.LatestPublishedSnapshot(ctx, "default", "global", "total_tokens")
	if err != nil || gotSnap.ParticipantCount != 1 {
		t.Fatalf("published snapshot lookup: %+v err=%v", gotSnap, err)
	}
}

func TestInstallationRegistrationIsIdempotent(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	hash := sha256.Sum256([]byte("registration-user"))
	if err := s.CreateUser(ctx, &domain.User{UserID: id(20), AuthSubjectHash: hash, DisplayName: "Registration User", AccountStatus: "active", LeaderboardVisibility: "private", TimezoneName: "UTC", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(serverapi.RegisterInstallationRequest{DevicePublicKey: base64.RawURLEncoding.EncodeToString(pub), OSType: "windows", Architecture: "x86_64", CollectorVersion: "1.0.0"})
	h := &serverapi.Handler{Users: s, Installations: s, UserSessions: &auth.StoredUserSessionResolver{Users: s}, IDGenerator: func() string { return id(21) }}
	for attempt, expectedStatus := range []int{http.StatusCreated, http.StatusOK} {
		req := httptest.NewRequest(http.MethodPost, "/v1/installations/register", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer registration-user")
		w := httptest.NewRecorder()
		h.HandleRegisterInstallation(w, req)
		if w.Code != expectedStatus {
			t.Fatalf("attempt %d status=%d body=%s", attempt+1, w.Code, w.Body.String())
		}
		var response serverapi.RegisterInstallationResponse
		if err := json.NewDecoder(w.Body).Decode(&response); err != nil || response.InstallationID != id(21) {
			t.Fatalf("attempt %d response=%+v err=%v", attempt+1, response, err)
		}
	}
}

func TestTwentyConcurrentHTTPRegistrationsWithSamePublicKeyReturnWinner(t *testing.T) {
	s := resetStore(t)
	ctx := context.Background()
	sessionToken := "concurrent-registration-user"
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := s.CreateUser(ctx, &domain.User{UserID: id(22), AuthSubjectHash: sha256.Sum256([]byte(sessionToken)), DisplayName: "Concurrent Registration User", AccountStatus: "active", LeaderboardVisibility: "private", TimezoneName: "UTC", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(serverapi.RegisterInstallationRequest{DevicePublicKey: base64.RawURLEncoding.EncodeToString(pub), OSType: "windows", Architecture: "x86_64", CollectorVersion: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	var generated atomic.Uint32
	handler := &serverapi.Handler{Users: s, Installations: s, UserSessions: &auth.StoredUserSessionResolver{Users: s}, IDGenerator: func() string { return id(200 + int(generated.Add(1))) }}
	server := httptest.NewServer(serverapi.NewMux(handler))
	defer server.Close()

	const workers = 20
	start := make(chan struct{})
	ids := make(chan string, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			req, reqErr := http.NewRequest(http.MethodPost, server.URL+"/v1/installations/register", bytes.NewReader(body))
			if reqErr != nil {
				errs <- reqErr
				return
			}
			req.Header.Set("Authorization", "Bearer "+sessionToken)
			req.Header.Set("Content-Type", "application/json")
			resp, reqErr := server.Client().Do(req)
			if reqErr != nil {
				errs <- reqErr
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
				payload, _ := io.ReadAll(resp.Body)
				errs <- fmt.Errorf("registration status=%d body=%s", resp.StatusCode, payload)
				return
			}
			var response serverapi.RegisterInstallationResponse
			if reqErr = json.NewDecoder(resp.Body).Decode(&response); reqErr != nil {
				errs <- reqErr
				return
			}
			ids <- response.InstallationID
		}()
	}
	close(start)
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winner := ""
	count := 0
	for installationID := range ids {
		count++
		if winner == "" {
			winner = installationID
		}
		if installationID != winner {
			t.Fatalf("installation id=%q winner=%q", installationID, winner)
		}
	}
	if count != workers {
		t.Fatalf("responses=%d want=%d", count, workers)
	}
	var rows int
	if err := s.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM installations WHERE device_public_key=?`, pub).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("installation rows=%d err=%v", rows, err)
	}
}

type failingCache struct{}

func (failingCache) ConsumeNonce(context.Context, string, [32]byte, time.Time) (bool, error) {
	return false, errors.New("redis down")
}
func (failingCache) PruneExpired(context.Context, time.Time) (int, error) {
	return 0, errors.New("redis down")
}

func TestRedisNonceFailureFallsBackToMySQL(t *testing.T) {
	s := resetStore(t)
	createUserAndInstallation(t, s, id(10), id(11))
	fallback := &store.FallbackNonceStore{Cache: failingCache{}, Fallback: s}
	h := sha256.Sum256([]byte("fallback nonce"))
	fresh, err := fallback.ConsumeNonce(context.Background(), id(11), h, time.Now().Add(time.Minute))
	if err != nil || !fresh {
		t.Fatalf("fallback failed: fresh=%v err=%v", fresh, err)
	}
	fresh, err = fallback.ConsumeNonce(context.Background(), id(11), h, time.Now().Add(time.Minute))
	if err != nil || fresh {
		t.Fatalf("fallback replay accepted: fresh=%v err=%v", fresh, err)
	}
}
