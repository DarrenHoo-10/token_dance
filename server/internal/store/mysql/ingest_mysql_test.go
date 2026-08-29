package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

func seedIngestInstallation(t *testing.T, st *Store, userID, installationID string, publicKey [32]byte, now time.Time) {
	t.Helper()
	ctx := context.Background()
	authSubjectHash := crypto.SHA256([]byte("subject:" + userID))
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO users (
			user_id, auth_subject_hash, display_name, account_status,
			leaderboard_visibility, timezone_name, created_at, updated_at
		) VALUES (?, ?, ?, 'active', 'private', 'UTC', ?, ?)`,
		userID, authSubjectHash[:], "Ingest Race User", now, now,
	); err != nil {
		t.Fatalf("seed ingest user: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
		INSERT INTO installations (
			installation_id, user_id, device_public_key, os_type, architecture,
			collector_version, installation_status, registered_at, updated_at
		) VALUES (?, ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		installationID, userID, publicKey[:], now, now,
	); err != nil {
		t.Fatalf("seed ingest installation: %v", err)
	}
}

func validIngestEvent(seed string, occurredAt time.Time) domain.UsageEvent {
	return domain.UsageEvent{
		EventID:              crypto.SHA256([]byte("event:" + seed)),
		SchemaVersion:        1,
		AdapterID:            "dev.tokendance.adapter.test",
		AdapterVersion:       "1.0.0",
		AgentID:              "test-agent",
		EventType:            "model_usage_recorded",
		Accuracy:             "exact",
		SourceKind:           "runtime_stream",
		OccurredAt:           occurredAt,
		PrivacyPolicyVersion: 1,
	}
}

func TestMySQL_ConcurrentIngestSameBatchIsIdempotent(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	installationID := "ins_ingest_idempotent"
	seedIngestInstallation(t, st, "usr_ingest_idempotent", installationID, crypto.SHA256([]byte("public:idempotent")), now)

	const concurrency = 8
	requestHash := crypto.SHA256([]byte("same canonical request body"))
	event := validIngestEvent("idempotent", now)
	errs := make(chan error, concurrency)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			result, err := st.Ingest().CommitIngest(context.Background(), domain.IngestBatch{
				BatchID:        "bat_ingest_idempotent",
				InstallationID: installationID,
				RequestSHA256:  requestHash,
				NonceHash:      crypto.SHA256([]byte(fmt.Sprintf("nonce:%d", index))),
				NonceExpiresAt: now.Add(10 * time.Minute),
				EventCount:     1,
				Events:         []domain.UsageEvent{event},
				ReceivedAt:     now.Add(time.Duration(index) * time.Millisecond),
			})
			if err == nil && (result.AcceptedCount != 1 || result.DuplicateCount != 0) {
				err = fmt.Errorf("unexpected ACK: %+v", result)
			}
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent idempotent ingest failed: %v", err)
		}
	}

	assertTableCount(t, db, "SELECT COUNT(*) FROM ingest_batches WHERE batch_id = ?", 1, "bat_ingest_idempotent")
	assertTableCount(t, db, "SELECT COUNT(*) FROM usage_events WHERE installation_id = ?", 1, installationID)
	assertTableCount(t, db, "SELECT COUNT(*) FROM ingest_nonces WHERE installation_id = ?", concurrency, installationID)
}

func TestMySQL_ConcurrentIngestRejectsNonceReplay(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	installationID := "ins_ingest_nonce_race"
	seedIngestInstallation(t, st, "usr_ingest_nonce_race", installationID, crypto.SHA256([]byte("public:nonce")), now)
	nonceHash := crypto.SHA256([]byte("shared replay nonce"))

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := st.Ingest().CommitIngest(context.Background(), domain.IngestBatch{
				BatchID:        fmt.Sprintf("bat_nonce_race_%d", index),
				InstallationID: installationID,
				RequestSHA256:  crypto.SHA256([]byte(fmt.Sprintf("request:%d", index))),
				NonceHash:      nonceHash,
				NonceExpiresAt: now.Add(10 * time.Minute),
				EventCount:     1,
				Events:         []domain.UsageEvent{validIngestEvent(fmt.Sprintf("nonce:%d", index), now)},
				ReceivedAt:     now,
			})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, replays int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrNonceReplay):
			replays++
		default:
			t.Fatalf("unexpected concurrent nonce result: %v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("expected one success and one nonce replay, got successes=%d replays=%d", successes, replays)
	}
	assertTableCount(t, db, "SELECT COUNT(*) FROM ingest_batches WHERE installation_id = ?", 1, installationID)
	assertTableCount(t, db, "SELECT COUNT(*) FROM usage_events WHERE installation_id = ?", 1, installationID)
	assertTableCount(t, db, "SELECT COUNT(*) FROM ingest_nonces WHERE installation_id = ?", 1, installationID)
}

func TestMySQL_ConcurrentIngestDetectsBatchHashConflict(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	installationID := "ins_ingest_hash_race"
	seedIngestInstallation(t, st, "usr_ingest_hash_race", installationID, crypto.SHA256([]byte("public:hash")), now)

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			_, err := st.Ingest().CommitIngest(context.Background(), domain.IngestBatch{
				BatchID:        "bat_hash_conflict",
				InstallationID: installationID,
				RequestSHA256:  crypto.SHA256([]byte(fmt.Sprintf("different-request:%d", index))),
				NonceHash:      crypto.SHA256([]byte(fmt.Sprintf("different-nonce:%d", index))),
				NonceExpiresAt: now.Add(10 * time.Minute),
				EventCount:     1,
				Events:         []domain.UsageEvent{validIngestEvent(fmt.Sprintf("hash:%d", index), now)},
				ReceivedAt:     now,
			})
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrBatchHashConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent hash result: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("expected one success and one batch hash conflict, got successes=%d conflicts=%d", successes, conflicts)
	}
	assertTableCount(t, db, "SELECT COUNT(*) FROM ingest_batches WHERE batch_id = ?", 1, "bat_hash_conflict")
	assertTableCount(t, db, "SELECT COUNT(*) FROM usage_events WHERE installation_id = ?", 1, installationID)
	assertTableCount(t, db, "SELECT COUNT(*) FROM ingest_nonces WHERE installation_id = ?", 1, installationID)
}

func assertTableCount(t *testing.T, db *sql.DB, query string, expected int, args ...interface{}) {
	t.Helper()
	var count int
	if err := db.QueryRowContext(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count query failed: %v", err)
	}
	if count != expected {
		t.Fatalf("expected count %d, got %d for %s", expected, count, query)
	}
}
