package migrate

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tokendance/internal/domain"
)

func TestMigrationEmbedLoading(t *testing.T) {
	runner := NewRunner(nil)
	if err := runner.LoadFromEmbed(); err != nil {
		t.Fatalf("failed to load embedded migrations: %v", err)
	}

	migs := runner.GetMigrations()
	if len(migs) != 3 {
		t.Fatalf("expected 3 migrations, got %d", len(migs))
	}

	expected := []string{"0001", "0002", "0003"}
	for i, m := range migs {
		if m.Version != expected[i] {
			t.Errorf("migration %d: expected version %s, got %s", i, expected[i], m.Version)
		}
		if len(m.ChecksumHex) != 64 {
			t.Errorf("migration %d: invalid checksum hex length %d", i, len(m.ChecksumHex))
		}
		if len(m.Content) == 0 {
			t.Errorf("migration %d: content is empty", i)
		}
	}
}

func TestValidateBaselineRecords(t *testing.T) {
	now := time.Now().UTC()
	u1 := "usr_user1"
	u2 := "usr_user2"

	// Valid records: unique active account deletions
	validRecords := []domain.DataDeletionRequest{
		{RequestID: "del_1", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusPending, RequestedAt: now},
		{RequestID: "del_2", UserID: &u2, DeletionScope: "account", RequestStatus: domain.DeletionStatusRunning, RequestedAt: now},
		{RequestID: "del_3", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusCompleted, RequestedAt: now},
		{RequestID: "del_4", UserID: &u1, DeletionScope: "installation", RequestStatus: domain.DeletionStatusPending, RequestedAt: now},
	}

	if err := ValidateBaselineRecords(validRecords); err != nil {
		t.Errorf("expected valid records to pass: %v", err)
	}

	// Invalid records: duplicate pending account deletions for same user
	invalidRecords := []domain.DataDeletionRequest{
		{RequestID: "del_1", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusPending, RequestedAt: now},
		{RequestID: "del_2", UserID: &u1, DeletionScope: "account", RequestStatus: domain.DeletionStatusRunning, RequestedAt: now},
	}

	if err := ValidateBaselineRecords(invalidRecords); err == nil {
		t.Errorf("expected duplicate active account deletion to fail baseline guard")
	}
}

func getTestMySQLDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TOKENDANCE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("skipping MySQL test: TOKENDANCE_TEST_MYSQL_DSN not set")
	}

	db, err := sql.Open("mysql", dsn)
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

func TestMigrationRunnerIntegration_CleanAndUpgrade(t *testing.T) {
	db := getTestMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	runner := NewRunner(db)

	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}

	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Verify idempotency
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("expected second run to be idempotent: %v", err)
	}
}

func TestMigrationRunnerIntegration_DirtyBaselineGuard(t *testing.T) {
	db := getTestMySQLDB(t)
	defer db.Close()

	ctx := context.Background()
	runner := NewRunner(db)

	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}

	if err := runner.LoadFromEmbed(); err != nil {
		t.Fatalf("failed to load migrations: %v", err)
	}

	// Apply only 0001
	runner0001 := &Runner{
		db:         db,
		migrations: []MigrationInfo{runner.migrations[0]},
	}
	if err := runner0001.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run 0001: %v", err)
	}

	// Insert duplicate pending account deletion requests
	uID := "usr_test_dirty_01"
	_, err := db.ExecContext(ctx, "INSERT INTO users (user_id, auth_subject_hash, display_name) VALUES (?, UNHEX(SHA2('user', 256)), 'User 1')", uID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO data_deletion_requests (request_id, user_id, deletion_scope, request_status) VALUES ('req_1', ?, 'account', 'pending'), ('req_2', ?, 'account', 'running')", uID, uID)
	if err != nil {
		t.Fatalf("failed to insert duplicate deletion requests: %v", err)
	}

	// Now try to run 0002
	runner0002 := &Runner{
		db:         db,
		migrations: []MigrationInfo{runner.migrations[1]},
	}
	err = runner0002.RunMigrations(ctx)
	if err == nil {
		t.Fatalf("expected dirty baseline guard to fail when duplicate account deletions exist")
	}
}
