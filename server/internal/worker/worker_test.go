package worker

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tokendance/internal/clock"
	"tokendance/internal/migrate"
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

func TestWorker_LeaseClaimAndFencingIntegration(t *testing.T) {
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
	w1 := NewWorker(db, clk)
	w2 := NewWorker(db, clk)

	// Seed outbox item
	now := time.Now().UTC()
	emailID := "emb_test_worker_01"
	_, err := db.ExecContext(ctx, `
		INSERT INTO email_outbox (
			email_id, idempotency_key, template_key, locale,
			recipient_ciphertext, payload_ciphertext, encryption_key_version,
			delivery_status, attempt_count, next_attempt_at, expires_at, created_at, updated_at
		) VALUES (?, UNHEX(SHA2('key1', 256)), 'auth_code', 'en-US', 'rcpt', 'payload', 1, 'pending', 0, ?, ?, ?, ?)`,
		emailID, now, now.Add(24*time.Hour), now, now)
	if err != nil {
		t.Fatalf("failed to seed email outbox: %v", err)
	}

	// Worker 1 processes outbox
	processed, err := w1.ProcessOutbox(ctx)
	if err != nil {
		t.Fatalf("w1 ProcessOutbox error: %v", err)
	}
	if processed != 1 {
		t.Fatalf("expected w1 to process 1 email, got %d", processed)
	}

	// Worker 2 tries to process immediately - should find nothing pending
	processed2, err := w2.ProcessOutbox(ctx)
	if err != nil {
		t.Fatalf("w2 ProcessOutbox error: %v", err)
	}
	if processed2 != 0 {
		t.Fatalf("expected w2 to process 0 emails, got %d", processed2)
	}

	// Verify email is marked sent in DB
	var status string
	err = db.QueryRowContext(ctx, "SELECT delivery_status FROM email_outbox WHERE email_id = ?", emailID).Scan(&status)
	if err != nil || status != "sent" {
		t.Fatalf("expected delivery_status 'sent', got %s, err: %v", status, err)
	}
}

func TestWorker_ProcessExpirations(t *testing.T) {
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
	w := NewWorker(db, clk)

	// Seed expired challenge (expires_at in past, but created_at before expires_at to satisfy check constraint)
	pastExpiry := time.Now().UTC().Add(-1 * time.Hour)
	pastCreated := pastExpiry.Add(-10 * time.Minute)
	chID := "emc_expired_01"
	_, err := db.ExecContext(ctx, `
		INSERT INTO email_challenges (
			challenge_id, email_lookup_hash, email_ciphertext, email_key_version,
			challenge_type, code_hash, code_key_version, challenge_status,
			attempt_count, max_attempts, send_count, expires_at, created_at, updated_at
		) VALUES (?, UNHEX(SHA2('email', 256)), 'ct', 1, 'register', UNHEX(SHA2('code', 256)), 1, 'pending', 0, 6, 1, ?, ?, ?)`,
		chID, pastExpiry, pastCreated, pastCreated)
	if err != nil {
		t.Fatalf("failed to seed email challenge: %v", err)
	}

	if err := w.ProcessExpirations(ctx); err != nil {
		t.Fatalf("failed to process expirations: %v", err)
	}

	var status string
	err = db.QueryRowContext(ctx, "SELECT challenge_status FROM email_challenges WHERE challenge_id = ?", chID).Scan(&status)
	if err != nil || status != "expired" {
		t.Fatalf("expected challenge_status 'expired', got %s, err: %v", status, err)
	}
}
