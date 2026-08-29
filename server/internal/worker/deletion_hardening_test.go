package worker

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/provider"
)

func resetDeletionTestSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var version string
	if err := db.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("read MySQL version: %v", err)
	}
	if !strings.HasPrefix(version, "8.0.34") {
		t.Fatalf("deletion hardening tests require MySQL 8.0.34, got %s", version)
	}
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(context.Background()); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	if err := runner.RunMigrations(context.Background()); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
}

func seedDeletionUser(t *testing.T, db *sql.DB, userID string, status string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO users (
			user_id, auth_subject_hash, display_name, account_status,
			leaderboard_visibility, timezone_name, locale, created_at, updated_at
		) VALUES (?, UNHEX(SHA2(?, 256)), ?, ?, 'private', 'UTC', 'en-US', CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`,
		userID, "subject:"+userID, "User "+userID, status)
	if err != nil {
		t.Fatalf("seed user %s: %v", userID, err)
	}
}

func seedDeletionUsage(t *testing.T, db *sql.DB, userID, installationID string, occurredAt time.Time) {
	t.Helper()
	batchID := "bat_" + installationID[4:]
	_, err := db.Exec(`
		INSERT INTO installations (
			installation_id, user_id, device_public_key, device_name, os_type,
			architecture, collector_version, installation_status, registered_at, updated_at
		) VALUES (?, ?, UNHEX(SHA2(?, 256)), 'Device', 'windows', 'x64', '1.0.0', 'active', CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`,
		installationID, userID, "device:"+installationID)
	if err != nil {
		t.Fatalf("seed installation: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO ingest_batches (
			batch_id, installation_id, request_sha256, event_count, accepted_count,
			batch_status, received_at, committed_at, updated_at
		) VALUES (?, ?, UNHEX(SHA2(?, 256)), 1, 1, 'committed', CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`,
		batchID, installationID, "batch:"+batchID)
	if err != nil {
		t.Fatalf("seed ingest batch: %v", err)
	}
	_, err = db.Exec(`
		INSERT INTO usage_events (
			event_id, schema_version, batch_id, installation_id, user_id,
			adapter_id, adapter_version, agent_id, event_type, accuracy,
			source_kind, occurred_at, privacy_policy_version
		) VALUES (UNHEX(SHA2(?, 256)), 1, ?, ?, ?, 'adapter', '1.0.0', 'agent',
		          'model_usage_recorded', 'exact', 'runtime_stream', ?, 1)`,
		"event:"+batchID, batchID, installationID, userID, occurredAt)
	if err != nil {
		t.Fatalf("seed usage event: %v", err)
	}
}

func seedDeletionRequest(t *testing.T, db *sql.DB, requestID, userID, installationID, scope, filter string, cancelBefore *time.Time) {
	t.Helper()
	var activeAccount interface{}
	if scope == "account" {
		activeAccount = userID
	}
	_, err := db.Exec(`
		INSERT INTO data_deletion_requests (
			request_id, user_id, installation_id, deletion_scope, scope_filter_json,
			request_status, phase, progress_cursor, active_account_key,
			cancel_before, requested_at, next_attempt_at, updated_at
		) VALUES (?, ?, NULLIF(?, ''), ?, NULLIF(?, ''), 'pending', 'queued', 0, ?, ?,
		          CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`,
		requestID, userID, installationID, scope, filter, activeAccount, cancelBefore)
	if err != nil {
		t.Fatalf("seed deletion request: %v", err)
	}
}

func TestDeletionTwoWorkerSkipLockedCrashTakeoverAndStaleCompletion(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()
	resetDeletionTestSchema(t, db)

	seedDeletionUser(t, db, "usr_claim_worker_one", "active")
	seedDeletionUser(t, db, "usr_claim_worker_two", "active")
	seedDeletionRequest(t, db, "del_claim_worker_one", "usr_claim_worker_one", "", "all_usage", "{}", nil)
	seedDeletionRequest(t, db, "del_claim_worker_two", "usr_claim_worker_two", "", "all_usage", "{}", nil)

	w1 := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	w2 := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))

	claim1, err := w1.claimDeletionRequest(context.Background())
	if err != nil {
		t.Fatalf("worker 1 claim: %v", err)
	}
	claim2, err := w2.claimDeletionRequest(context.Background())
	if err != nil {
		t.Fatalf("worker 2 claim: %v", err)
	}
	if claim1.requestID == claim2.requestID {
		t.Fatalf("FOR UPDATE SKIP LOCKED allowed duplicate claim %s", claim1.requestID)
	}

	if _, err := db.Exec(`UPDATE data_deletion_requests SET lease_expires_at = DATE_SUB(CURRENT_TIMESTAMP(3), INTERVAL 1 SECOND) WHERE request_id = ?`, claim1.requestID); err != nil {
		t.Fatalf("expire worker 1 lease: %v", err)
	}
	takeover, err := w2.claimDeletionRequest(context.Background())
	if err != nil {
		t.Fatalf("worker 2 takeover: %v", err)
	}
	if takeover.requestID != claim1.requestID || takeover.generation <= claim1.generation {
		t.Fatalf("unexpected takeover: request=%s generation=%d", takeover.requestID, takeover.generation)
	}
	if err := w1.withDeletionPhaseTx(context.Background(), claim1, "deleting_events", 1, nil); err == nil {
		t.Fatal("stale worker phase update was not fenced")
	}
	if err := w2.executeDeletionClaim(context.Background(), takeover); err != nil {
		t.Fatalf("takeover execution: %v", err)
	}

	var status string
	var generation uint64
	if err := db.QueryRow("SELECT request_status, claim_generation FROM data_deletion_requests WHERE request_id = ?", claim1.requestID).Scan(&status, &generation); err != nil {
		t.Fatalf("read takeover result: %v", err)
	}
	if status != "completed" || generation != takeover.generation {
		t.Fatalf("takeover did not complete under current fence: status=%s generation=%d", status, generation)
	}
}

func TestDeletionCancellationRaceCannotReclaimCancelledPendingAccount(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()
	resetDeletionTestSchema(t, db)

	userID := "usr_cancel_race_user"
	requestID := "del_cancel_race_req"
	seedDeletionUser(t, db, userID, "deletion_pending")
	cancelBefore := time.Now().UTC().Add(time.Hour)
	seedDeletionRequest(t, db, requestID, userID, "", "account", "{}", &cancelBefore)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin cancellation lock: %v", err)
	}
	var locked string
	if err := tx.QueryRow("SELECT request_id FROM data_deletion_requests WHERE request_id = ? FOR UPDATE", requestID).Scan(&locked); err != nil {
		t.Fatalf("lock cancellation row: %v", err)
	}

	worker := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	claimResult := make(chan error, 1)
	go func() {
		_, err := worker.claimDeletionRequest(context.Background())
		claimResult <- err
	}()

	if _, err := tx.Exec(`
		UPDATE data_deletion_requests
		SET request_status = 'cancelled', phase = 'cancelled', cancelled_at = CURRENT_TIMESTAMP(3),
		    active_account_key = NULL, updated_at = CURRENT_TIMESTAMP(3)
		WHERE request_id = ? AND request_status = 'pending'`, requestID); err != nil {
		t.Fatalf("cancel request: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit cancellation: %v", err)
	}

	if err := <-claimResult; !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cancelled request was claimable: %v", err)
	}
	if _, err := db.Exec(`UPDATE data_deletion_requests SET cancel_before = DATE_SUB(CURRENT_TIMESTAMP(3), INTERVAL 1 SECOND), next_attempt_at = CURRENT_TIMESTAMP(3) WHERE request_id = ?`, requestID); err != nil {
		t.Fatalf("age cancelled request: %v", err)
	}
	if _, err := worker.claimDeletionRequest(context.Background()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cancelled pending account was reclaimed after aging: %v", err)
	}
}

type failingDeleteStorage struct {
	*provider.MemoryObjectStorage
	failures int
}

func (s *failingDeleteStorage) DeleteObject(ctx context.Context, key string) error {
	if s.failures > 0 {
		s.failures--
		return fmt.Errorf("injected object deletion failure")
	}
	return s.MemoryObjectStorage.DeleteObject(ctx, key)
}

func TestDeletionDatabaseAndObjectFailureInjectionRetainsRetryableState(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	t.Run("database", func(t *testing.T) {
		resetDeletionTestSchema(t, db)
		userID := "usr_db_failure_user"
		seedDeletionUser(t, db, userID, "active")
		seedDeletionUsage(t, db, userID, "ins_db_failure_device", time.Now().UTC())
		seedDeletionRequest(t, db, "del_db_failure_req", userID, "", "all_usage", "{}", nil)
		if _, err := db.Exec(`CREATE TRIGGER inject_usage_delete_failure BEFORE DELETE ON usage_events FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'injected delete failure'`); err != nil {
			t.Fatalf("create failure trigger: %v", err)
		}

		worker := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
		if _, err := worker.ProcessDeletionRequests(context.Background()); err == nil {
			t.Fatal("expected injected database failure")
		}
		var status, phase string
		if err := db.QueryRow("SELECT request_status, phase FROM data_deletion_requests WHERE request_id = 'del_db_failure_req'").Scan(&status, &phase); err != nil {
			t.Fatalf("read failed state: %v", err)
		}
		if status != "failed" || phase != "failed" {
			t.Fatalf("database failure marked terminal success: status=%s phase=%s", status, phase)
		}
		if _, err := db.Exec("DROP TRIGGER inject_usage_delete_failure"); err != nil {
			t.Fatalf("drop failure trigger: %v", err)
		}
		if _, err := db.Exec("UPDATE data_deletion_requests SET next_attempt_at = CURRENT_TIMESTAMP(3) WHERE request_id = 'del_db_failure_req'"); err != nil {
			t.Fatalf("make retry due: %v", err)
		}
		if processed, err := worker.ProcessDeletionRequests(context.Background()); err != nil || processed != 1 {
			t.Fatalf("database failure retry: processed=%d err=%v", processed, err)
		}
	})

	t.Run("object", func(t *testing.T) {
		resetDeletionTestSchema(t, db)
		userID := "usr_object_failure_user"
		seedDeletionUser(t, db, userID, "deletion_pending")
		storage := &failingDeleteStorage{MemoryObjectStorage: provider.NewMemoryObjectStorage(""), failures: 1}
		key := "exports/" + userID + "/failure.csv"
		if err := storage.PutObject(context.Background(), key, bytes.NewReader([]byte("x")), 1, "text/csv"); err != nil {
			t.Fatalf("seed object: %v", err)
		}
		_, err := db.Exec(`
			INSERT INTO data_export_jobs (
				export_id, user_id, idempotency_key, request_hash, export_scope, export_format,
				filter_json, job_status, object_key, file_sha256, file_size, completed_at,
				expires_at, created_at, updated_at
			) VALUES ('exp_object_failure', ?, 'object-failure', UNHEX(SHA2('object-failure', 256)),
			          'summary', 'csv', '{}', 'completed', ?, UNHEX(SHA2('x', 256)), 1,
			          CURRENT_TIMESTAMP(3), DATE_ADD(CURRENT_TIMESTAMP(3), INTERVAL 1 DAY),
			          CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`, userID, key)
		if err != nil {
			t.Fatalf("seed export: %v", err)
		}
		past := time.Now().UTC().Add(-time.Minute)
		seedDeletionRequest(t, db, "del_object_failure", userID, "", "account", "{}", &past)

		worker := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, storage)
		if _, err := worker.ProcessDeletionRequests(context.Background()); err == nil {
			t.Fatal("expected injected object failure")
		}
		var status string
		if err := db.QueryRow("SELECT request_status FROM data_deletion_requests WHERE request_id = 'del_object_failure'").Scan(&status); err != nil {
			t.Fatalf("read object failure state: %v", err)
		}
		if status != "failed" || !storage.HasObject(key) {
			t.Fatalf("object failure did not retain retryable state: status=%s object=%v", status, storage.HasObject(key))
		}
		if _, err := db.Exec("UPDATE data_deletion_requests SET next_attempt_at = CURRENT_TIMESTAMP(3) WHERE request_id = 'del_object_failure'"); err != nil {
			t.Fatalf("make object retry due: %v", err)
		}
		if processed, err := worker.ProcessDeletionRequests(context.Background()); err != nil || processed != 1 {
			t.Fatalf("object failure retry: processed=%d err=%v", processed, err)
		}
		if storage.HasObject(key) {
			t.Fatal("object remained after successful retry")
		}
	})
}

func TestDeletionInstallationPreservesOtherInstallationFactsAndRebuildsAggregates(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()
	resetDeletionTestSchema(t, db)

	userID := "usr_two_installations"
	targetID := "ins_delete_target"
	survivorID := "ins_keep_survivor"
	occurredAt := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	seedDeletionUser(t, db, userID, "active")
	seedDeletionUsage(t, db, userID, targetID, occurredAt)
	seedDeletionUsage(t, db, userID, survivorID, occurredAt)
	if _, err := db.Exec(`UPDATE usage_events SET token_total = CASE installation_id WHEN ? THEN 300 ELSE 700 END, provider_id = 'provider', model_id = 'model' WHERE user_id = ?`, targetID, userID); err != nil {
		t.Fatalf("seed per-installation event metrics: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO daily_user_agent_metrics (metric_date, user_id, agent_id, exact_token_total, aggregation_version, computed_at, updated_at) VALUES ('2026-08-10', ?, 'agent', 1000, 1, CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3)), ('2026-08-09', ?, 'archived-agent', 900, 1, CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`, userID, userID); err != nil {
		t.Fatalf("seed combined aggregate: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO daily_user_agent_model_metrics (metric_date, user_id, agent_id, provider_id, model_id, exact_token_total, aggregation_version, computed_at, updated_at) VALUES ('2026-08-10', ?, 'agent', 'provider', 'model', 1000, 1, CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`, userID); err != nil {
		t.Fatalf("seed combined model aggregate: %v", err)
	}
	seedDeletionRequest(t, db, "del_one_installation", userID, targetID, "installation", `{"installationId":"ins_delete_target"}`, nil)

	worker := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	if processed, err := worker.ProcessDeletionRequests(context.Background()); err != nil || processed != 1 {
		t.Fatalf("process installation deletion: processed=%d err=%v", processed, err)
	}

	var survivorEvents, survivorInstallations, survivorBatches int
	var agentTokens, modelTokens, unaffectedTokens uint64
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_events WHERE user_id = ? AND installation_id = ?`, userID, survivorID).Scan(&survivorEvents); err != nil {
		t.Fatalf("count surviving events: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM installations WHERE user_id = ? AND installation_id = ?`, userID, survivorID).Scan(&survivorInstallations); err != nil {
		t.Fatalf("count surviving installation: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM ingest_batches WHERE installation_id = ?`, survivorID).Scan(&survivorBatches); err != nil {
		t.Fatalf("count surviving batches: %v", err)
	}
	if err := db.QueryRow(`SELECT exact_token_total FROM daily_user_agent_metrics WHERE metric_date = '2026-08-10' AND user_id = ? AND agent_id = 'agent'`, userID).Scan(&agentTokens); err != nil {
		t.Fatalf("read rebuilt agent aggregate: %v", err)
	}
	if err := db.QueryRow(`SELECT exact_token_total FROM daily_user_agent_model_metrics WHERE metric_date = '2026-08-10' AND user_id = ? AND agent_id = 'agent' AND provider_id = 'provider' AND model_id = 'model'`, userID).Scan(&modelTokens); err != nil {
		t.Fatalf("read rebuilt model aggregate: %v", err)
	}
	if err := db.QueryRow(`SELECT exact_token_total FROM daily_user_agent_metrics WHERE metric_date = '2026-08-09' AND user_id = ? AND agent_id = 'archived-agent'`, userID).Scan(&unaffectedTokens); err != nil {
		t.Fatalf("read unaffected aggregate: %v", err)
	}
	if survivorEvents != 1 || survivorInstallations != 1 || survivorBatches != 1 || agentTokens != 700 || modelTokens != 700 || unaffectedTokens != 900 {
		t.Fatalf("surviving installation facts changed: events=%d installations=%d batches=%d agentTokens=%d modelTokens=%d unaffectedTokens=%d", survivorEvents, survivorInstallations, survivorBatches, agentTokens, modelTokens, unaffectedTokens)
	}
	assertDeletionCount(t, db, `SELECT COUNT(*) FROM usage_events WHERE installation_id = ?`, 0, targetID)
	assertDeletionCount(t, db, `SELECT COUNT(*) FROM installations WHERE installation_id = ?`, 0, targetID)
	assertDeletionCount(t, db, `SELECT COUNT(*) FROM ingest_batches WHERE installation_id = ?`, 0, targetID)
}

func assertDeletionCount(t *testing.T, db *sql.DB, query string, expected int, args ...interface{}) {
	t.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatalf("count deletion residue: %v", err)
	}
	if count != expected {
		t.Fatalf("expected %d rows for %s, got %d", expected, query, count)
	}
}

func TestDeletionAllNonAccountScopesMySQL8034(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	tests := []struct {
		name           string
		scope          string
		installationID string
		filter         string
		occurredAt     time.Time
	}{
		{name: "installation", scope: "installation", installationID: "ins_scope_installation", filter: `{"installationId":"ins_scope_installation"}`, occurredAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)},
		{name: "time_range", scope: "time_range", installationID: "ins_scope_time_range", filter: `{"from":"2026-08-10T00:00:00Z","to":"2026-08-11T00:00:00Z"}`, occurredAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)},
		{name: "all_usage", scope: "all_usage", installationID: "ins_scope_all_usage", filter: `{}`, occurredAt: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resetDeletionTestSchema(t, db)
			userID := "usr_scope_" + tc.name
			requestID := "del_scope_" + tc.name
			seedDeletionUser(t, db, userID, "active")
			seedDeletionUsage(t, db, userID, tc.installationID, tc.occurredAt)
			_, err := db.Exec(`INSERT INTO daily_user_agent_metrics (metric_date, user_id, agent_id, aggregation_version, computed_at, updated_at) VALUES ('2026-08-10', ?, 'agent', 1, CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3))`, userID)
			if err != nil {
				t.Fatalf("seed aggregate: %v", err)
			}
			seedDeletionRequest(t, db, requestID, userID, tc.installationID, tc.scope, tc.filter, nil)

			worker := NewWorkerWithFull(db, clock.RealClock{}, nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
			processed, err := worker.ProcessDeletionRequests(context.Background())
			if err != nil || processed != 1 {
				t.Fatalf("process %s deletion: processed=%d err=%v", tc.scope, processed, err)
			}
			var status string
			if err := db.QueryRow("SELECT request_status FROM data_deletion_requests WHERE request_id = ?", requestID).Scan(&status); err != nil {
				t.Fatalf("read request status: %v", err)
			}
			if status != "completed" {
				t.Fatalf("scope %s did not complete: %s", tc.scope, status)
			}
			var eventCount, aggregateCount int
			if err := db.QueryRow("SELECT COUNT(*) FROM usage_events WHERE user_id = ?", userID).Scan(&eventCount); err != nil {
				t.Fatalf("count events: %v", err)
			}
			if err := db.QueryRow("SELECT COUNT(*) FROM daily_user_agent_metrics WHERE user_id = ?", userID).Scan(&aggregateCount); err != nil {
				t.Fatalf("count aggregates: %v", err)
			}
			if eventCount != 0 || aggregateCount != 0 {
				t.Fatalf("scope %s residuals: events=%d aggregates=%d", tc.scope, eventCount, aggregateCount)
			}
		})
	}
}
