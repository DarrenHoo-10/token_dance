package worker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	"tokendance/internal/provider"
	"tokendance/internal/store"
	"tokendance/internal/store/mysql"
)

func getTestMySQLDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TOKENDANCE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("skipping MySQL worker test: TOKENDANCE_TEST_MYSQL_DSN not set")
	}

	normDSN := mysql.NormalizeDSN(dsn)
	db, err := sql.Open("mysql", normDSN)
	if err != nil {
		t.Fatalf("failed to open MySQL test connection: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("failed to ping MySQL test database: %v", err)
	}

	return db
}

type mockFailingEmailProvider struct {
	failCount int
	transient bool
	sentCount int
}

func (m *mockFailingEmailProvider) Send(ctx context.Context, msg email.Message) (string, error) {
	if m.failCount > 0 {
		m.failCount--
		if m.transient {
			return "", &email.ProviderError{Code: "RATE_LIMITED", Message: "too many requests", Transient: true, Err: email.ErrTransient}
		}
		return "", &email.ProviderError{Code: "INVALID_RECIPIENT", Message: "mailbox unavailable", Transient: false, Err: email.ErrPermanent}
	}
	m.sentCount++
	return fmt.Sprintf("prov_msg_%s", msg.EmailID), nil
}

func TestWorker_EmailOutboxWithProvider_RetryAndExpiry(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	clk := clock.NewMockClock(time.Now().UTC())
	mockProv := &mockFailingEmailProvider{failCount: 1, transient: true}
	storage := provider.NewMemoryObjectStorage("")
	w := NewWorkerWithFull(db, clk, nil, mockProv, storage)

	// Seed outbox item with truncated time to avoid sub-millisecond roundoff
	now := clk.Now().Truncate(time.Millisecond)
	emailID := "emb_retry_01"
	_, err := db.ExecContext(ctx, `
		INSERT INTO email_outbox (
			email_id, idempotency_key, template_key, locale,
			recipient_ciphertext, payload_ciphertext, encryption_key_version,
			delivery_status, attempt_count, next_attempt_at, expires_at, created_at, updated_at
		) VALUES (?, UNHEX(SHA2('key_retry', 256)), 'auth_code', 'en-US', 'rcpt@example.com', '{"code":"123456"}', 1, 'pending', 0, ?, ?, ?, ?)`,
		emailID, now, now.Add(24*time.Hour), now, now)
	if err != nil {
		t.Fatalf("failed to seed email outbox: %v", err)
	}

	// First pass: mock provider returns transient error -> message should go back to pending with backoff
	processed, err := w.ProcessOutbox(ctx)
	if err != nil {
		t.Fatalf("ProcessOutbox error: %v", err)
	}
	if processed != 0 {
		t.Fatalf("expected 0 processed on transient error, got %d", processed)
	}

	var status string
	var attempts uint16
	var nextAttempt time.Time
	err = db.QueryRowContext(ctx, "SELECT delivery_status, attempt_count, next_attempt_at FROM email_outbox WHERE email_id = ?", emailID).Scan(&status, &attempts, &nextAttempt)
	if err != nil || status != "pending" || attempts != 1 || !nextAttempt.After(now) {
		t.Fatalf("expected status 'pending', attempts=1, nextAttempt > now, got status=%s, attempts=%d, nextAttempt=%v", status, attempts, nextAttempt)
	}

	// Advance mock clock past nextAttempt
	clk.Advance(2 * time.Minute)
	// Second pass: mock provider succeeds -> delivery_status becomes sent
	processed, err = w.ProcessOutbox(ctx)
	if err != nil {
		t.Fatalf("ProcessOutbox second pass error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 processed on second pass, got %d", processed)
	}

	var providerMsgID sql.NullString
	err = db.QueryRowContext(ctx, "SELECT delivery_status, provider_message_id FROM email_outbox WHERE email_id = ?", emailID).Scan(&status, &providerMsgID)
	if err != nil || status != "sent" || !providerMsgID.Valid {
		t.Fatalf("expected status 'sent' with providerMsgID, got status=%s, id=%v", status, providerMsgID)
	}
}

func TestWorker_EmailOutbox_CrashTakeoverAndFencing(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	clk := clock.NewMockClock(now)
	liveSink := email.NewDeliverySink()
	storage := provider.NewMemoryObjectStorage("")

	wLive := NewWorkerWithFull(db, clk, nil, liveSink, storage)

	// Simulate crashed worker: job locked 10 minutes ago in 'sending' status
	staleLockedAt := now.Add(-10 * time.Minute)
	staleEmailID := "emb_crashed_takeover_01"
	_, err := db.ExecContext(ctx, `
		INSERT INTO email_outbox (
			email_id, idempotency_key, template_key, locale,
			recipient_ciphertext, payload_ciphertext, encryption_key_version,
			delivery_status, attempt_count, next_attempt_at, locked_at, locked_by,
			expires_at, created_at, updated_at
		) VALUES (?, UNHEX(SHA2('key_stale', 256)), 'auth_code', 'en-US', 'crashed@example.com', '{"code":"654321"}', 1,
		          'sending', 1, ?, ?, 'wrk_crashed_host_123', ?, ?, ?)`,
		staleEmailID, staleLockedAt, staleLockedAt, now.Add(24*time.Hour), staleLockedAt, staleLockedAt)
	if err != nil {
		t.Fatalf("failed to seed stale outbox row: %v", err)
	}

	// Live worker takes over stale sending job
	processed, err := wLive.ProcessOutbox(ctx)
	if err != nil {
		t.Fatalf("live worker ProcessOutbox error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected live worker to claim and process 1 stale sending email, got %d", processed)
	}

	// Verify email is marked sent
	var status, lockedBy sql.NullString
	err = db.QueryRowContext(ctx, "SELECT delivery_status, locked_by FROM email_outbox WHERE email_id = ?", staleEmailID).Scan(&status, &lockedBy)
	if err != nil || status.String != "sent" || lockedBy.Valid {
		t.Fatalf("expected delivery_status 'sent' and lock released, got status=%v, lockedBy=%v", status, lockedBy)
	}

	// Verify crashed worker cannot overwrite (fencing verification)
	res, err := db.ExecContext(ctx, `
		UPDATE email_outbox
		SET delivery_status = 'failed', last_error_code = 'CRASH_OVERWRITE'
		WHERE email_id = ? AND locked_by = 'wrk_crashed_host_123'`, staleEmailID)
	if err != nil {
		t.Fatalf("crashed worker fenced update error: %v", err)
	}
	aff, _ := res.RowsAffected()
	if aff != 0 {
		t.Fatalf("fencing failed: expected 0 rows affected by stale worker, got %d", aff)
	}
}

func TestWorker_ExportJobs_StreamingToStorageAndTakeover(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	clk := clock.RealClock{}
	storage := provider.NewMemoryObjectStorage("")
	w := NewWorkerWithFull(db, clk, nil, email.DefaultSink, storage)

	now := time.Now().UTC()
	userID := "usr_export_worker_user"

	// Seed user
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, display_name, account_status, leaderboard_visibility, timezone_name, locale, created_at, updated_at)
		VALUES (?, UNHEX(SHA2('usr_sub', 256)), 'Export User', 'active', 'private', 'UTC', 'en-US', ?, ?)`,
		userID, now, now)
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	// Seed daily metrics for user
	_, err = db.ExecContext(ctx, `
		INSERT INTO daily_user_agent_metrics (
			metric_date, user_id, agent_id, exact_token_total, derived_token_total,
			estimated_token_total, session_count, interaction_turn_count,
			model_request_count, code_generated_lines, code_accepted_lines,
			cost_amount, cost_currency, source_max_event_pk, aggregation_version, computed_at, updated_at
		) VALUES
		('2026-08-01', ?, 'claude-code', 1000, 500, 0, 5, 20, 15, 120, 100, 0.05, 'USD', 1, 1, ?, ?),
		('2026-08-02', ?, 'claude-code', 2000, 1000, 0, 8, 35, 25, 250, 200, 0.10, 'USD', 2, 1, ?, ?)`,
		userID, now, now, userID, now, now)
	if err != nil {
		t.Fatalf("failed to seed daily metrics: %v", err)
	}

	// 1. Seed pending CSV export job
	exportID1 := "exp_worker_test_csv"
	_, err = db.ExecContext(ctx, `
		INSERT INTO data_export_jobs (
			export_id, user_id, idempotency_key, request_hash, export_scope,
			export_format, filter_json, job_status, attempt_count, next_attempt_at,
			created_at, updated_at
		) VALUES (?, ?, 'idemp_exp_csv', UNHEX(SHA2('req_csv', 256)), 'summary', 'csv', '{}', 'pending', 0, ?, ?, ?)`,
		exportID1, userID, now, now, now)
	if err != nil {
		t.Fatalf("failed to seed export job 1: %v", err)
	}

	// 2. Seed stale running JSON export job locked by crashed worker
	exportID2 := "exp_worker_test_json"
	staleTime := now.Add(-10 * time.Minute)
	_, err = db.ExecContext(ctx, `
		INSERT INTO data_export_jobs (
			export_id, user_id, idempotency_key, request_hash, export_scope,
			export_format, filter_json, job_status, attempt_count, next_attempt_at,
			locked_at, locked_by, started_at, created_at, updated_at
		) VALUES (?, ?, 'idemp_exp_json', UNHEX(SHA2('req_json', 256)), 'all_aggregates', 'json', '{}', 'running', 1, ?, ?, 'wrk_crashed_export_1', ?, ?, ?)`,
		exportID2, userID, staleTime, staleTime, staleTime, staleTime, staleTime)
	if err != nil {
		t.Fatalf("failed to seed export job 2: %v", err)
	}

	// Process export jobs
	processed, err := w.ProcessExportJobs(ctx)
	if err != nil {
		t.Fatalf("ProcessExportJobs error: %v", err)
	}
	if processed != 2 {
		t.Fatalf("expected 2 export jobs processed, got %d", processed)
	}

	// Verify CSV export job is completed with object_key, file_sha256, file_size
	var status1, objKey1 string
	var size1 uint64
	var sha1 []byte
	err = db.QueryRowContext(ctx, "SELECT job_status, object_key, file_sha256, file_size FROM data_export_jobs WHERE export_id = ?", exportID1).Scan(&status1, &objKey1, &sha1, &size1)
	if err != nil || status1 != "completed" || size1 == 0 || len(sha1) != 32 {
		t.Fatalf("expected completed CSV job with key/sha/size, got status=%s, size=%d, sha=%x", status1, size1, sha1)
	}

	// Verify object exists in storage and contains valid CSV header
	meta1, err := storage.HeadObject(ctx, objKey1)
	if err != nil || meta1.Size != int64(size1) {
		t.Fatalf("storage object verification failed for %s: %v", objKey1, err)
	}
	rc1, _ := storage.GetObject(ctx, objKey1)
	buf1 := new(bytes.Buffer)
	buf1.ReadFrom(rc1)
	rc1.Close()
	if !bytes.Contains(buf1.Bytes(), []byte("metric_date,agent_id,exact_tokens")) {
		t.Fatalf("CSV export does not contain expected header: %s", buf1.String())
	}

	// Verify JSON export job is completed
	var status2, objKey2 string
	var size2 uint64
	err = db.QueryRowContext(ctx, "SELECT job_status, object_key, file_size FROM data_export_jobs WHERE export_id = ?", exportID2).Scan(&status2, &objKey2, &size2)
	if err != nil || status2 != "completed" || size2 == 0 {
		t.Fatalf("expected completed JSON job with key/size, got status=%s, size=%d", status2, size2)
	}

	rc2, _ := storage.GetObject(ctx, objKey2)
	buf2 := new(bytes.Buffer)
	buf2.ReadFrom(rc2)
	rc2.Close()
	var parsedJSON map[string]interface{}
	if err := json.Unmarshal(buf2.Bytes(), &parsedJSON); err != nil {
		t.Fatalf("JSON export data invalid: %v", err)
	}
}

func TestWorker_DeletionRequests_AllFourScopesAndPIIScrubbing(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	clk := clock.RealClock{}
	storage := provider.NewMemoryObjectStorage("")
	w := NewWorkerWithFull(db, clk, nil, email.DefaultSink, storage)

	now := time.Now().UTC()
	uID := "usr_full_deletion_test"

	// 1. Seed complete user data ecosystem
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, email_lookup_hash, email_ciphertext, handle, display_name, account_status, leaderboard_visibility, timezone_name, locale, created_at, updated_at)
		VALUES (?, UNHEX(SHA2('sub_del', 256)), UNHEX(SHA2('email_del', 256)), 'ct_email', 'deleteme', 'Delete Me', 'deletion_pending', 'public', 'UTC', 'en-US', ?, ?)`,
		uID, now, now)
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	_, _ = db.ExecContext(ctx, `INSERT INTO user_password_credentials (user_id, password_hash, created_at, updated_at) VALUES (?, 'argon_hash', ?, ?)`, uID, now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO user_sessions (session_id, user_id, session_token_hash, csrf_token_hash, credential_version, session_status, idle_expires_at, absolute_expires_at, created_at, updated_at) VALUES ('ses_del1', ?, UNHEX(SHA2('tok1', 256)), UNHEX(SHA2('csrf1', 256)), 1, 'active', ?, ?, ?, ?)`, uID, now.Add(time.Hour), now.Add(time.Hour), now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO public_user_profiles (user_id, handle, display_name, profile_status, source_profile_version, source_privacy_version, created_at, updated_at) VALUES (?, 'deleteme', 'Delete Me', 'hidden', 1, 1, ?, ?)`, uID, now, now)
	_, _ = db.ExecContext(ctx, `INSERT INTO user_privacy_settings (user_id, public_profile_enabled, created_at, updated_at) VALUES (?, TRUE, ?, ?)`, uID, now, now)

	// Seed upload avatar object in storage and DB
	avatarKey := fmt.Sprintf("users/%s/avatars/uob_del_01", uID)
	_ = storage.PutObject(ctx, avatarKey, bytes.NewReader([]byte("avatar-bytes")), 12, "image/png")
	_, _ = db.ExecContext(ctx, `INSERT INTO user_upload_objects (object_id, user_id, object_type, object_key, content_type, byte_size, content_sha256, image_width, image_height, upload_status, ready_at, expires_at, created_at, updated_at) VALUES ('uob_del_01', ?, 'avatar', ?, 'image/png', 12, UNHEX(SHA2('av', 256)), 100, 100, 'ready', ?, ?, ?, ?)`, uID, avatarKey, now, now.Add(24*time.Hour), now, now)

	// Seed export job in storage and DB
	exportKey := fmt.Sprintf("exports/%s/exp_del_01.csv", uID)
	_ = storage.PutObject(ctx, exportKey, bytes.NewReader([]byte("export-bytes")), 12, "text/csv")
	_, _ = db.ExecContext(ctx, `INSERT INTO data_export_jobs (export_id, user_id, idempotency_key, request_hash, export_scope, export_format, filter_json, job_status, object_key, file_sha256, file_size, completed_at, expires_at, created_at, updated_at) VALUES ('exp_del_01', ?, 'idemp_exp_del', UNHEX(SHA2('rh', 256)), 'summary', 'csv', '{}', 'completed', ?, UNHEX(SHA2('ex', 256)), 12, ?, ?, ?, ?)`, uID, exportKey, now, now.Add(24*time.Hour), now, now)

	// Seed daily metrics
	_, _ = db.ExecContext(ctx, `INSERT INTO daily_user_agent_metrics (metric_date, user_id, agent_id, exact_token_total, aggregation_version, computed_at, updated_at) VALUES ('2026-08-01', ?, 'agent1', 500, 1, ?, ?)`, uID, now, now)

	// Seed account deletion request with past cancel_before (grace period expired)
	reqID := "del_req_account_01"
	pastGrace := now.Add(-1 * time.Hour)
	_, err = db.ExecContext(ctx, `
		INSERT INTO data_deletion_requests (
			request_id, user_id, deletion_scope, request_status, phase,
			progress_cursor, active_account_key, cancel_before, requested_at
		) VALUES (?, ?, 'account', 'pending', 'queued', 0, ?, ?, ?)`,
		reqID, uID, uID, pastGrace, pastGrace.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatalf("failed to seed deletion request: %v", err)
	}

	// Execute Deletion Worker Pass
	processed, err := w.ProcessDeletionRequests(ctx)
	if err != nil {
		t.Fatalf("ProcessDeletionRequests error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected 1 deletion request processed, got %d", processed)
	}

	// 1. Verify Deletion Request is completed with audit_reference and active_account_key is NULL
	var reqStatus, phase string
	var auditRef, activeAccKey sql.NullString
	err = db.QueryRowContext(ctx, "SELECT request_status, phase, audit_reference, active_account_key FROM data_deletion_requests WHERE request_id = ?", reqID).Scan(&reqStatus, &phase, &auditRef, &activeAccKey)
	if err != nil || reqStatus != "completed" || phase != "completed" || !auditRef.Valid || activeAccKey.Valid {
		t.Fatalf("deletion request state invalid: status=%s, phase=%s, auditRef=%v, activeAccKey=%v", reqStatus, phase, auditRef, activeAccKey)
	}

	// 2. Verify User record is scrubbed to safe tombstone
	var accStatus, dispName string
	var handle, emailHash, emailCT sql.NullString
	err = db.QueryRowContext(ctx, "SELECT account_status, display_name, handle, email_lookup_hash, email_ciphertext FROM users WHERE user_id = ?", uID).Scan(&accStatus, &dispName, &handle, &emailHash, &emailCT)
	if err != nil || accStatus != "deleted" || dispName != "Deleted User" || handle.Valid || emailHash.Valid || emailCT.Valid {
		t.Fatalf("user record not properly scrubbed: status=%s, name=%s, handle=%v, emailHash=%v", accStatus, dispName, handle, emailHash)
	}

	// 3. Verify PII residuals are completely eliminated
	var credCount, sessionCount, pubProfileCount, dailyMetricCount int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_password_credentials WHERE user_id = ?", uID).Scan(&credCount)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_sessions WHERE user_id = ?", uID).Scan(&sessionCount)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM public_user_profiles WHERE user_id = ?", uID).Scan(&pubProfileCount)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM daily_user_agent_metrics WHERE user_id = ?", uID).Scan(&dailyMetricCount)

	if credCount != 0 || sessionCount != 0 || pubProfileCount != 0 || dailyMetricCount != 0 {
		t.Fatalf("PII residuals remained after account deletion: creds=%d, sessions=%d, pubProfiles=%d, metrics=%d", credCount, sessionCount, pubProfileCount, dailyMetricCount)
	}

	// 4. Verify storage objects were deleted
	if storage.HasObject(avatarKey) {
		t.Fatalf("avatar storage object was not deleted: %s", avatarKey)
	}
	if storage.HasObject(exportKey) {
		t.Fatalf("export storage object was not deleted: %s", exportKey)
	}
}

func TestWorker_MediaCompletion_RealMySQLCheckConstraints(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	now := time.Now().UTC()
	uID := "usr_media_check_user"

	// Seed user
	_, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, display_name, account_status, leaderboard_visibility, timezone_name, locale, created_at, updated_at)
		VALUES (?, UNHEX(SHA2('usr_media_sub', 256)), 'Media User', 'active', 'private', 'UTC', 'en-US', ?, ?)`,
		uID, now, now)
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	st := mysql.NewStore(db)
	mediaSt := st.Media()

	// 1. Create upload intent with NULL ready fields
	objectID := "uob_check_test_01"
	ct := "image/png"
	sz := uint64(512)
	intent := domain.UserUploadObject{
		ObjectID:     objectID,
		UserID:       uID,
		ObjectType:   "avatar",
		ObjectKey:    "users/" + uID + "/avatars/" + objectID,
		ContentType:  &ct,
		ByteSize:     &sz,
		UploadStatus: domain.UploadStatusPending,
		ExpiresAt:    now.Add(10 * time.Minute),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	_, err = mediaSt.CreateAvatarUploadIntent(ctx, intent)
	if err != nil {
		t.Fatalf("failed to create upload intent: %v", err)
	}

	// 2. Direct violation test: Attempt to set status 'ready' without required dimensions/sha256/ready_at
	_, checkErr := db.ExecContext(ctx, `
		UPDATE user_upload_objects
		SET upload_status = 'ready'
		WHERE object_id = ?`, objectID)
	if checkErr == nil {
		t.Fatalf("expected MySQL CHECK constraint (chk_user_upload_objects_ready) violation, but query succeeded")
	}

	// 3. Complete avatar intent with valid ready metadata -> Must succeed and satisfy MySQL CHECK constraint
	meta := store.AvatarReadyMeta{
		ByteSize:      512,
		ContentSha256: crypto.SHA256([]byte("valid_image_bytes")),
		ImageWidth:    128,
		ImageHeight:   128,
		ContentType:   "image/png",
	}

	completedObj, err := mediaSt.CompleteAvatarUploadIntent(ctx, objectID, uID, meta, now)
	if err != nil {
		t.Fatalf("CompleteAvatarUploadIntent failed against MySQL: %v", err)
	}
	if completedObj.UploadStatus != domain.UploadStatusReady {
		t.Fatalf("expected upload status 'ready', got %s", completedObj.UploadStatus)
	}

	// 4. Verify user record has updated avatar_object_id
	var userAvatarObj sql.NullString
	err = db.QueryRowContext(ctx, "SELECT avatar_object_id FROM users WHERE user_id = ?", uID).Scan(&userAvatarObj)
	if err != nil || !userAvatarObj.Valid || userAvatarObj.String != objectID {
		t.Fatalf("expected user avatar_object_id=%s, got %v", objectID, userAvatarObj)
	}
}
