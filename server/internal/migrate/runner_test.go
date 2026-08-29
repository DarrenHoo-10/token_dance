package migrate

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tokendance/internal/domain"
	"tokendance/internal/store/mysql"
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
		stmts := splitSQLStatements(m.Content)
		t.Logf("Migration %s has %d statements:", m.Filename, len(stmts))
		for j, st := range stmts {
			firstLine := strings.Split(st, "\n")[0]
			t.Logf("  [%d] %s", j, firstLine)
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

// Path 1: Clean Install (0001 -> 0002 -> 0003)
func TestMigrationRunnerIntegration_CleanInstall(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := NewRunner(db)

	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}

	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// Verify all 3 migrations recorded in schema_migrations
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil || count != 3 {
		t.Fatalf("expected 3 applied migrations, got %d (err: %v)", count, err)
	}

	// Verify idempotency
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("expected second run to be idempotent: %v", err)
	}
}

// Path 2: Upgrade from 0001 baseline with user privacy backfill
func TestMigrationRunnerIntegration_UpgradeFrom0001(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := NewRunner(db)

	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}

	if err := runner.LoadFromEmbed(); err != nil {
		t.Fatalf("failed to load migrations: %v", err)
	}

	// Apply only 0001 baseline
	runner0001 := &Runner{
		db:         db,
		migrations: []MigrationInfo{runner.migrations[0]},
	}
	if err := runner0001.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to apply 0001 baseline: %v", err)
	}

	// Seed pre-existing users under 0001 baseline
	uID1 := "usr_baseline_01"
	uID2 := "usr_baseline_02"
	_, err := db.ExecContext(ctx, "INSERT INTO users (user_id, auth_subject_hash, display_name) VALUES (?, UNHEX(SHA2('user1', 256)), 'User One'), (?, UNHEX(SHA2('user2', 256)), 'User Two')", uID1, uID2)
	if err != nil {
		t.Fatalf("failed to insert baseline users: %v", err)
	}

	// Seed clean single deletion request
	_, err = db.ExecContext(ctx, "INSERT INTO data_deletion_requests (request_id, user_id, deletion_scope, request_status) VALUES ('req_clean_1', ?, 'account', 'pending')", uID1)
	if err != nil {
		t.Fatalf("failed to insert baseline deletion request: %v", err)
	}

	// Now run full migration runner (should apply 0002 and 0003, backfilling user_privacy_settings)
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to upgrade from 0001: %v", err)
	}

	// Verify backfilled user_privacy_settings for pre-existing users
	var privCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_privacy_settings WHERE user_id IN (?, ?) AND public_profile_enabled = FALSE", uID1, uID2).Scan(&privCount)
	if err != nil || privCount != 2 {
		t.Fatalf("expected 2 backfilled default-private user_privacy_settings rows, got %d, err: %v", privCount, err)
	}
}

// Path 3: Reject dirty baseline before persistent 0002 DDL
func TestMigrationRunnerIntegration_RejectDirtyBaseline(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := NewRunner(db)

	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}

	if err := runner.LoadFromEmbed(); err != nil {
		t.Fatalf("failed to load migrations: %v", err)
	}

	// Apply only 0001 baseline
	runner0001 := &Runner{
		db:         db,
		migrations: []MigrationInfo{runner.migrations[0]},
	}
	if err := runner0001.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to apply 0001 baseline: %v", err)
	}

	// Insert duplicate pending account deletion requests for same user (dirty baseline)
	uID := "usr_test_dirty_01"
	_, err := db.ExecContext(ctx, "INSERT INTO users (user_id, auth_subject_hash, display_name) VALUES (?, UNHEX(SHA2('dirty', 256)), 'Dirty User')", uID)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}
	_, err = db.ExecContext(ctx, "INSERT INTO data_deletion_requests (request_id, user_id, deletion_scope, request_status) VALUES ('req_d1', ?, 'account', 'pending'), ('req_d2', ?, 'account', 'running')", uID, uID)
	if err != nil {
		t.Fatalf("failed to insert duplicate deletion requests: %v", err)
	}

	// Now try to run migrations (0002 should be rejected by baseline guard)
	err = runner.RunMigrations(ctx)
	if err == nil {
		t.Fatalf("expected dirty baseline guard to fail when duplicate account deletions exist")
	}

	// Verify 0002 was NOT recorded in schema_migrations
	var count0002 int
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = '0002'").Scan(&count0002)
	if count0002 != 0 {
		t.Fatalf("expected 0002 to not be recorded when dirty baseline fails")
	}
}

func TestValidateSchemaCompatibility(t *testing.T) {
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	defer func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}()

	ctx := context.Background()
	runner := NewRunner(db)

	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("failed to reset clean schema: %v", err)
	}

	// 1. Unmigrated DB should fail compatibility check
	if err := runner.ValidateSchemaCompatibility(ctx); err == nil {
		t.Fatalf("expected unmigrated database to fail schema compatibility check")
	}

	// 2. Run migrations
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	// 3. Fully migrated DB should pass compatibility check
	if err := runner.ValidateSchemaCompatibility(ctx); err != nil {
		t.Fatalf("expected migrated database to pass compatibility check: %v", err)
	}

	// 4. In-memory runner (db=nil) should pass compatibility check
	nilRunner := NewRunner(nil)
	if err := nilRunner.ValidateSchemaCompatibility(ctx); err != nil {
		t.Fatalf("expected in-memory runner to pass compatibility check: %v", err)
	}

	// 5. Tampered checksum in schema_migrations should fail compatibility check
	_, err := db.ExecContext(ctx, "UPDATE schema_migrations SET checksum_sha256 = UNHEX(SHA2('tampered', 256)) WHERE version = '0001'")
	if err != nil {
		t.Fatalf("failed to tamper checksum: %v", err)
	}

	if err := runner.ValidateSchemaCompatibility(ctx); err == nil {
		t.Fatalf("expected tampered checksum to fail schema compatibility check")
	}
}

