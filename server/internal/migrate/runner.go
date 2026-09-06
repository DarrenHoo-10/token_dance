package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"tokendance/internal/domain"
)

//go:embed all:migrations/*.sql
var EmbeddedMigrations embed.FS

const MigrationAdvisoryLockName = "tokendance_migrations_lock"

type MigrationInfo struct {
	Version     string
	Filename    string
	Checksum    [32]byte
	ChecksumHex string
	Content     string
	AppliedAt   *time.Time
}

type Runner struct {
	db         *sql.DB
	migrations []MigrationInfo
	afterDDL   func(version string, statement int) error
}

type migrationState struct {
	checksum                 [32]byte
	dirty                    bool
	statementCount           int
	lastStatement            int
	pendingStatement         int
	pendingStatementChecksum [32]byte
	appliedAt                *time.Time
}

func NewRunner(db *sql.DB) *Runner {
	return &Runner{
		db: db,
	}
}

// LoadFromEmbed loads migration definitions from embedded files
func (r *Runner) LoadFromEmbed() error {
	entries, err := fs.ReadDir(EmbeddedMigrations, "migrations")
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	var migs []MigrationInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		p := path.Join("migrations", entry.Name())
		content, err := EmbeddedMigrations.ReadFile(p)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", p, err)
		}

		sum := sha256.Sum256(content)
		version := strings.Split(entry.Name(), "_")[0]

		migs = append(migs, MigrationInfo{
			Version:     version,
			Filename:    entry.Name(),
			Checksum:    sum,
			ChecksumHex: fmt.Sprintf("%x", sum),
			Content:     string(content),
		})
	}

	sort.Slice(migs, func(i, j int) bool {
		return migs[i].Filename < migs[j].Filename
	})

	r.migrations = migs
	return nil
}

// LoadFromDir loads migration definitions from a directory on disk
func (r *Runner) LoadFromDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory %s: %w", dir, err)
	}

	var migs []MigrationInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		p := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(p)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", p, err)
		}

		sum := sha256.Sum256(content)
		version := strings.Split(entry.Name(), "_")[0]

		migs = append(migs, MigrationInfo{
			Version:     version,
			Filename:    entry.Name(),
			Checksum:    sum,
			ChecksumHex: fmt.Sprintf("%x", sum),
			Content:     string(content),
		})
	}

	sort.Slice(migs, func(i, j int) bool {
		return migs[i].Filename < migs[j].Filename
	})

	r.migrations = migs
	return nil
}

func (r *Runner) GetMigrations() []MigrationInfo {
	return r.migrations
}

// CheckBaselineGuard validates preflight conditions before running 0002
func (r *Runner) CheckBaselineGuard(ctx context.Context) error {
	if r.db == nil {
		return nil
	}

	query := `
		SELECT user_id, COUNT(*) as cnt
		FROM data_deletion_requests
		WHERE deletion_scope = 'account'
		  AND request_status IN ('pending', 'running', 'failed')
		GROUP BY user_id
		HAVING cnt > 1;
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		// Table might not exist yet before 0001; that is fine
		return nil
	}
	defer rows.Close()

	var duplicates []string
	for rows.Next() {
		var userID string
		var cnt int
		if err := rows.Scan(&userID, &cnt); err == nil {
			duplicates = append(duplicates, fmt.Sprintf("%s (count=%d)", userID, cnt))
		}
	}

	if len(duplicates) > 0 {
		return fmt.Errorf("%w: duplicate unfinished account-deletion requests detected: %s", domain.ErrDirtyBaseline, strings.Join(duplicates, ", "))
	}

	return nil
}

// ValidateBaselineRecords checks raw slice of deletion records for dirty baseline violations
func ValidateBaselineRecords(records []domain.DataDeletionRequest) error {
	activeAccounts := make(map[string]int)
	for _, rec := range records {
		if rec.UserID != nil && rec.DeletionScope == "account" {
			if rec.RequestStatus == domain.DeletionStatusPending || rec.RequestStatus == domain.DeletionStatusRunning || rec.RequestStatus == domain.DeletionStatusFailed {
				activeAccounts[*rec.UserID]++
				if activeAccounts[*rec.UserID] > 1 {
					return fmt.Errorf("%w: duplicate unfinished account deletion for user %s", domain.ErrDirtyBaseline, *rec.UserID)
				}
			}
		}
	}
	return nil
}

// acquireAdvisoryLock acquires MySQL advisory lock on a dedicated connection
func (r *Runner) acquireAdvisoryLock(ctx context.Context, conn *sql.Conn, timeoutSeconds int) (bool, error) {
	var locked sql.NullInt64
	err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", MigrationAdvisoryLockName, timeoutSeconds).Scan(&locked)
	if err != nil {
		return false, fmt.Errorf("failed to execute GET_LOCK: %w", err)
	}
	return locked.Valid && locked.Int64 == 1, nil
}

// releaseAdvisoryLock releases MySQL advisory lock on a dedicated connection
func (r *Runner) releaseAdvisoryLock(ctx context.Context, conn *sql.Conn) error {
	var released sql.NullInt64
	err := conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", MigrationAdvisoryLockName).Scan(&released)
	if err != nil {
		return fmt.Errorf("failed to execute RELEASE_LOCK: %w", err)
	}
	return nil
}

// RunMigrations applies or resumes migrations with durable statement progress.
func (r *Runner) RunMigrations(ctx context.Context) error {
	if r.db == nil {
		return fmt.Errorf("database connection is nil")
	}
	if len(r.migrations) == 0 {
		if err := r.LoadFromEmbed(); err != nil {
			return err
		}
	}

	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration connection: %w", err)
	}
	defer conn.Close()
	locked, err := r.acquireAdvisoryLock(ctx, conn, 30)
	if err != nil {
		return fmt.Errorf("error acquiring migration advisory lock: %w", err)
	}
	if !locked {
		return fmt.Errorf("failed to acquire migration advisory lock within timeout")
	}
	defer func() { _ = r.releaseAdvisoryLock(context.Background(), conn) }()

	if err := ensureMigrationStateTable(ctx, conn); err != nil {
		return err
	}
	states, err := loadMigrationStates(ctx, conn)
	if err != nil {
		return err
	}

	for _, m := range r.migrations {
		statements := splitSQLStatements(m.Content)
		state, exists := states[m.Version]
		if exists && state.checksum != m.Checksum {
			return fmt.Errorf("checksum mismatch for migration %s: applied %x != current %x", m.Version, state.checksum, m.Checksum)
		}
		if exists && !state.dirty {
			continue
		}
		if !exists {
			if m.Version == "0002" {
				if err := r.CheckBaselineGuard(ctx); err != nil {
					return fmt.Errorf("migration %s dirty baseline preflight failed: %w", m.Version, err)
				}
			}
			if _, err := conn.ExecContext(ctx, `
				INSERT INTO schema_migrations
					(version, checksum_sha256, dirty, statement_count, last_statement, applied_at, updated_at)
				VALUES (?, ?, TRUE, ?, 0, NULL, CURRENT_TIMESTAMP(3))`,
				m.Version, m.Checksum[:], len(statements)); err != nil {
				return fmt.Errorf("failed to mark migration %s dirty: %w", m.Version, err)
			}
			state = migrationState{checksum: m.Checksum, dirty: true, statementCount: len(statements)}
		} else if state.statementCount != len(statements) {
			return fmt.Errorf("dirty migration %s statement count changed: recorded %d != current %d", m.Version, state.statementCount, len(statements))
		}

		for index := state.lastStatement; index < len(statements); index++ {
			stmt := strings.TrimSpace(statements[index])
			statementNumber := index + 1
			statementChecksum := sha256.Sum256([]byte(stmt))
			isDDL := isPersistentDDL(stmt)

			if isDDL {
				if state.pendingStatement != 0 && (state.pendingStatement != statementNumber || state.pendingStatementChecksum != statementChecksum) {
					return fmt.Errorf("dirty migration %s pending statement fingerprint mismatch at statement %d", m.Version, statementNumber)
				}
				if err := markPendingStatement(ctx, conn, m.Version, index, statementNumber, statementChecksum); err != nil {
					return err
				}
				applied, err := ddlAlreadyApplied(ctx, conn, stmt)
				if err != nil {
					return fmt.Errorf("failed reconciling migration %s statement %d schema state: %w", m.Version, statementNumber, err)
				}
				if !applied {
					if _, err := conn.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("failed executing migration %s statement %d/%d [%s]: %w", m.Filename, statementNumber, len(statements), stmt, err)
					}
					if r.afterDDL != nil {
						if err := r.afterDDL(m.Version, statementNumber); err != nil {
							return fmt.Errorf("migration %s interrupted after DDL statement %d before progress update: %w", m.Version, statementNumber, err)
						}
					}
				}
			} else {
				if _, err := conn.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("failed executing migration %s statement %d/%d [%s]: %w", m.Filename, statementNumber, len(statements), stmt, err)
				}
			}
			if err := persistStatementProgress(ctx, conn, m.Version, index, statementNumber); err != nil {
				return err
			}
			state.lastStatement = statementNumber
			state.pendingStatement = 0
			state.pendingStatementChecksum = [32]byte{}
		}
		if _, err := conn.ExecContext(ctx, `
			UPDATE schema_migrations
			SET dirty = FALSE, applied_at = CURRENT_TIMESTAMP(3), updated_at = CURRENT_TIMESTAMP(3)
			WHERE version = ? AND dirty = TRUE AND last_statement = statement_count`, m.Version); err != nil {
			return fmt.Errorf("failed to mark migration %s applied: %w", m.Version, err)
		}
	}
	return nil
}

func markPendingStatement(ctx context.Context, conn *sql.Conn, version string, lastStatement, statementNumber int, checksum [32]byte) error {
	result, err := conn.ExecContext(ctx, `
		UPDATE schema_migrations
		SET pending_statement = ?, pending_statement_sha256 = ?, updated_at = CURRENT_TIMESTAMP(3)
		WHERE version = ? AND dirty = TRUE AND last_statement = ?
		  AND (pending_statement = 0 OR (pending_statement = ? AND pending_statement_sha256 = ?))`,
		statementNumber, checksum[:], version, lastStatement, statementNumber, checksum[:])
	if err != nil {
		return fmt.Errorf("failed to persist migration %s statement %d fingerprint: %w", version, statementNumber, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify migration %s statement %d fingerprint: %w", version, statementNumber, err)
	}
	if rows == 1 {
		return nil
	}
	var recordedStatement, recordedLast int
	var recordedChecksum []byte
	if err := conn.QueryRowContext(ctx, `SELECT last_statement, pending_statement, pending_statement_sha256 FROM schema_migrations WHERE version = ? AND dirty = TRUE`, version).Scan(&recordedLast, &recordedStatement, &recordedChecksum); err != nil {
		return fmt.Errorf("failed to verify migration %s statement %d fingerprint state: %w", version, statementNumber, err)
	}
	var recorded [32]byte
	copy(recorded[:], recordedChecksum)
	if recordedLast != lastStatement || recordedStatement != statementNumber || recorded != checksum {
		return fmt.Errorf("migration %s statement %d fingerprint was not persisted", version, statementNumber)
	}
	return nil
}

func persistStatementProgress(ctx context.Context, conn *sql.Conn, version string, lastStatement, statementNumber int) error {
	result, err := conn.ExecContext(ctx, `
		UPDATE schema_migrations
		SET last_statement = ?, pending_statement = 0, pending_statement_sha256 = NULL,
		    updated_at = CURRENT_TIMESTAMP(3)
		WHERE version = ? AND dirty = TRUE AND last_statement = ?`, statementNumber, version, lastStatement)
	if err != nil {
		return fmt.Errorf("failed to persist migration %s statement %d progress: %w", version, statementNumber, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to verify migration %s statement %d progress: %w", version, statementNumber, err)
	}
	if rows != 1 {
		return fmt.Errorf("migration %s statement %d progress was not persisted", version, statementNumber)
	}
	return nil
}

func isPersistentDDL(stmt string) bool {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	return strings.HasPrefix(upper, "ALTER TABLE") ||
		strings.HasPrefix(upper, "CREATE TABLE") ||
		strings.HasPrefix(upper, "CREATE DATABASE")
}

var (
	createTablePattern   = regexp.MustCompile(`(?is)^CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+((?:` + "`[^`]+`" + `|[a-zA-Z0-9_]+)(?:\.(?:` + "`[^`]+`" + `|[a-zA-Z0-9_]+))?)`)
	alterTablePattern    = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+((?:` + "`[^`]+`" + `|[a-zA-Z0-9_]+)(?:\.(?:` + "`[^`]+`" + `|[a-zA-Z0-9_]+))?)`)
	addColumnPattern     = regexp.MustCompile(`(?is)\bADD\s+COLUMN\s+(` + "`[^`]+`" + `|[a-zA-Z0-9_]+)`)
	addIndexPattern      = regexp.MustCompile(`(?is)\bADD\s+(UNIQUE\s+)?(?:KEY|INDEX)\s+(` + "`[^`]+`" + `|[a-zA-Z0-9_]+)`)
	addConstraintPattern = regexp.MustCompile(`(?is)\bADD\s+CONSTRAINT\s+(` + "`[^`]+`" + `|[a-zA-Z0-9_]+)\s+(CHECK|FOREIGN\s+KEY)`)
	dropCheckPattern     = regexp.MustCompile(`(?is)\bDROP\s+CHECK\s+(` + "`[^`]+`" + `|[a-zA-Z0-9_]+)`)
)

func ddlAlreadyApplied(ctx context.Context, conn *sql.Conn, stmt string) (bool, error) {
	upper := strings.ToUpper(strings.TrimSpace(stmt))
	if strings.HasPrefix(upper, "CREATE DATABASE") {
		return false, nil
	}
	if match := createTablePattern.FindStringSubmatch(stmt); match != nil {
		schema, table := schemaAndObject(match[1])
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = COALESCE(NULLIF(?, ''), DATABASE()) AND table_name = ?`, schema, table).Scan(&count); err != nil {
			return false, err
		}
		return count == 1, nil
	}
	match := alterTablePattern.FindStringSubmatch(stmt)
	if match == nil {
		return false, nil
	}
	schema, table := schemaAndObject(match[1])
	checks := 0
	for _, columnMatch := range addColumnPattern.FindAllStringSubmatch(stmt, -1) {
		checks++
		exists, err := informationSchemaObjectExists(ctx, conn, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = COALESCE(NULLIF(?, ''), DATABASE()) AND table_name = ? AND column_name = ?`, schema, table, unquoteIdentifier(columnMatch[1]))
		if err != nil || !exists {
			return false, err
		}
	}
	for _, indexMatch := range addIndexPattern.FindAllStringSubmatch(stmt, -1) {
		checks++
		indexName := unquoteIdentifier(indexMatch[2])
		var nonUnique sql.NullInt64
		if err := conn.QueryRowContext(ctx, `SELECT MIN(non_unique) FROM information_schema.statistics WHERE table_schema = COALESCE(NULLIF(?, ''), DATABASE()) AND table_name = ? AND index_name = ?`, schema, table, indexName).Scan(&nonUnique); err != nil {
			return false, err
		}
		if !nonUnique.Valid {
			return false, nil
		}
		expectsUnique := strings.TrimSpace(indexMatch[1]) != ""
		if expectsUnique == (nonUnique.Int64 != 0) {
			return false, nil
		}
	}
	addedConstraints := make(map[string]struct{})
	for _, constraintMatch := range addConstraintPattern.FindAllStringSubmatch(stmt, -1) {
		checks++
		name := unquoteIdentifier(constraintMatch[1])
		addedConstraints[name] = struct{}{}
		constraintType := "CHECK"
		if strings.HasPrefix(strings.ToUpper(constraintMatch[2]), "FOREIGN") {
			constraintType = "FOREIGN KEY"
		}
		exists, err := informationSchemaObjectExists(ctx, conn, `SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema = COALESCE(NULLIF(?, ''), DATABASE()) AND table_name = ? AND constraint_name = ? AND constraint_type = ?`, schema, table, name, constraintType)
		if err != nil || !exists {
			return false, err
		}
	}
	for _, dropMatch := range dropCheckPattern.FindAllStringSubmatch(stmt, -1) {
		name := unquoteIdentifier(dropMatch[1])
		if _, replaced := addedConstraints[name]; replaced {
			continue
		}
		checks++
		exists, err := informationSchemaObjectExists(ctx, conn, `SELECT COUNT(*) FROM information_schema.table_constraints WHERE constraint_schema = COALESCE(NULLIF(?, ''), DATABASE()) AND table_name = ? AND constraint_name = ? AND constraint_type = 'CHECK'`, schema, table, name)
		if err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	return checks > 0, nil
}

func informationSchemaObjectExists(ctx context.Context, conn *sql.Conn, query string, args ...any) (bool, error) {
	var count int
	if err := conn.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func schemaAndObject(identifier string) (string, string) {
	parts := strings.Split(identifier, ".")
	if len(parts) == 2 {
		return unquoteIdentifier(parts[0]), unquoteIdentifier(parts[1])
	}
	return "", unquoteIdentifier(identifier)
}

func unquoteIdentifier(identifier string) string {
	return strings.Trim(strings.TrimSpace(identifier), "`")
}

func ensureMigrationStateTable(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		  version VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		  checksum_sha256 BINARY(32) NOT NULL,
		  dirty BOOLEAN NOT NULL DEFAULT TRUE,
		  statement_count INT UNSIGNED NOT NULL DEFAULT 0,
		  last_statement INT UNSIGNED NOT NULL DEFAULT 0,
		  pending_statement INT UNSIGNED NOT NULL DEFAULT 0,
		  pending_statement_sha256 BINARY(32) NULL,
		  applied_at DATETIME(3) NULL,
		  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
		  PRIMARY KEY (version)
		) ENGINE = InnoDB`); err != nil {
		return fmt.Errorf("failed to ensure schema_migrations table: %w", err)
	}
	columns := []struct {
		name string
		ddl  string
	}{
		{"dirty", "ALTER TABLE schema_migrations ADD COLUMN dirty BOOLEAN NOT NULL DEFAULT FALSE AFTER checksum_sha256"},
		{"statement_count", "ALTER TABLE schema_migrations ADD COLUMN statement_count INT UNSIGNED NOT NULL DEFAULT 0 AFTER dirty"},
		{"last_statement", "ALTER TABLE schema_migrations ADD COLUMN last_statement INT UNSIGNED NOT NULL DEFAULT 0 AFTER statement_count"},
		{"pending_statement", "ALTER TABLE schema_migrations ADD COLUMN pending_statement INT UNSIGNED NOT NULL DEFAULT 0 AFTER last_statement"},
		{"pending_statement_sha256", "ALTER TABLE schema_migrations ADD COLUMN pending_statement_sha256 BINARY(32) NULL AFTER pending_statement"},
		{"updated_at", "ALTER TABLE schema_migrations ADD COLUMN updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3) AFTER applied_at"},
	}
	for _, column := range columns {
		var count int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'schema_migrations' AND column_name = ?`, column.name).Scan(&count); err != nil {
			return fmt.Errorf("failed to inspect schema_migrations.%s: %w", column.name, err)
		}
		if count == 0 {
			if _, err := conn.ExecContext(ctx, column.ddl); err != nil {
				return fmt.Errorf("failed to add schema_migrations.%s: %w", column.name, err)
			}
		}
	}
	return nil
}

func loadMigrationStates(ctx context.Context, conn *sql.Conn) (map[string]migrationState, error) {
	rows, err := conn.QueryContext(ctx, `SELECT version, checksum_sha256, dirty, statement_count, last_statement, pending_statement, pending_statement_sha256, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("failed to query schema_migrations: %w", err)
	}
	defer rows.Close()
	states := make(map[string]migrationState)
	for rows.Next() {
		var version string
		var checksum []byte
		var pendingChecksum []byte
		var state migrationState
		var appliedAt sql.NullTime
		if err := rows.Scan(&version, &checksum, &state.dirty, &state.statementCount, &state.lastStatement, &state.pendingStatement, &pendingChecksum, &appliedAt); err != nil {
			return nil, fmt.Errorf("failed to scan migration state: %w", err)
		}
		copy(state.checksum[:], checksum)
		copy(state.pendingStatementChecksum[:], pendingChecksum)
		if appliedAt.Valid {
			state.appliedAt = &appliedAt.Time
		}
		states[version] = state
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed reading migration states: %w", err)
	}
	return states, nil
}

// CheckMigrationState reports partial migrations without changing database state.
func (r *Runner) CheckMigrationState(ctx context.Context) error {
	if r.db == nil {
		return fmt.Errorf("database connection is nil")
	}
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire migration connection: %w", err)
	}
	defer conn.Close()
	states, err := loadMigrationStates(ctx, conn)
	if err != nil {
		return err
	}
	for version, state := range states {
		if state.dirty {
			if state.pendingStatement != 0 {
				return fmt.Errorf("migration %s is dirty at statement %d/%d with pending DDL statement %d; run migrate -repair to reconcile information_schema", version, state.lastStatement, state.statementCount, state.pendingStatement)
			}
			return fmt.Errorf("migration %s is dirty at statement %d/%d; run migrate -repair", version, state.lastStatement, state.statementCount)
		}
	}
	return nil
}

// RepairMigrations reconciles pending DDL, resumes progress, and verifies checksums.
func (r *Runner) RepairMigrations(ctx context.Context) error {
	if err := r.RunMigrations(ctx); err != nil {
		return err
	}
	return r.ValidateSchemaCompatibility(ctx)
}

// ValidateSchemaCompatibility verifies all embedded migrations have been applied with matching checksums
// without executing any DDL statements or modifying database state.
func (r *Runner) ValidateSchemaCompatibility(ctx context.Context) error {
	if r.db == nil {
		return nil
	}

	if len(r.migrations) == 0 {
		if err := r.LoadFromEmbed(); err != nil {
			return fmt.Errorf("failed to load migration definitions: %w", err)
		}
	}

	if err := r.db.PingContext(ctx); err != nil {
		return fmt.Errorf("database ping failed: %w", err)
	}

	rows, err := r.db.QueryContext(ctx, "SELECT version, checksum_sha256, dirty, statement_count, last_statement FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		return fmt.Errorf("schema_migrations table does not exist or database is not migrated: %w", err)
	}
	defer rows.Close()

	applied := make(map[string][32]byte)
	for rows.Next() {
		var ver string
		var chk []byte
		var dirty bool
		var statementCount, lastStatement int
		if err := rows.Scan(&ver, &chk, &dirty, &statementCount, &lastStatement); err != nil {
			return fmt.Errorf("failed scanning schema_migrations row: %w", err)
		}
		if dirty {
			return fmt.Errorf("database has dirty migration %s at statement %d/%d; run migrate -repair", ver, lastStatement, statementCount)
		}
		var sum [32]byte
		copy(sum[:], chk)
		applied[ver] = sum
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error reading schema_migrations rows: %w", err)
	}

	for _, m := range r.migrations {
		prevSum, ok := applied[m.Version]
		if !ok {
			return fmt.Errorf("database schema missing required migration %s (%s)", m.Version, m.Filename)
		}
		if prevSum != m.Checksum {
			return fmt.Errorf("schema checksum mismatch for migration %s: applied %x != expected %x", m.Version, prevSum, m.Checksum)
		}
	}

	return nil
}

// ResetCleanSchema drops all application tables for clean test setup
func (r *Runner) ResetCleanSchema(ctx context.Context) error {
	if r.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	conn, err := r.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Close()

	// Disable foreign key checks on this connection for clean tear down
	if _, err := conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 0;"); err != nil {
		return fmt.Errorf("failed to disable foreign key checks: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS = 1;")
	}()

	tables := []string{
		"ranking_outbox",
		"aggregate_dirty_days",
		"user_window_scores",
		"deletion_object_keys",
		"data_export_jobs",
		"user_security_events",
		"device_binding_challenges",
		"user_upload_objects",
		"user_handle_history",
		"public_user_profiles",
		"user_privacy_settings",
		"email_outbox",
		"email_challenges",
		"user_sessions",
		"user_password_credentials",
		"adapter_releases",
		"leaderboard_entries",
		"leaderboard_snapshots",
		"daily_skill_metrics",
		"daily_user_agent_model_metrics",
		"daily_user_agent_metrics",
		"usage_events",
		"ingest_batches",
		"ingest_nonces",
		"installation_adapter_status",
		"installations",
		"data_deletion_requests",
		"users",
		"schema_migrations",
	}

	for _, table := range tables {
		query := fmt.Sprintf("DROP TABLE IF EXISTS %s;", table)
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return fmt.Errorf("failed to drop table %s: %w", table, err)
		}
	}

	return nil
}

func splitSQLStatements(content string) []string {
	var stmts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false
	inBacktick := false
	inBlockComment := false
	inLineComment := false

	chars := []rune(content)
	n := len(chars)

	for i := 0; i < n; i++ {
		c := chars[i]

		// Handle block comments
		if inBlockComment {
			if c == '*' && i+1 < n && chars[i+1] == '/' {
				inBlockComment = false
				i++ // skip '/'
			}
			continue
		}

		// Handle line comments
		if inLineComment {
			if c == '\n' || c == '\r' {
				inLineComment = false
				current.WriteRune(c)
			}
			continue
		}

		// Check for comment starts when not in quotes
		if !inSingleQuote && !inDoubleQuote && !inBacktick {
			if c == '/' && i+1 < n && chars[i+1] == '*' {
				inBlockComment = true
				i++ // skip '*'
				continue
			}
			if c == '#' {
				inLineComment = true
				continue
			}
			if c == '-' && i+1 < n && chars[i+1] == '-' {
				if i+2 >= n || chars[i+2] == ' ' || chars[i+2] == '\t' || chars[i+2] == '\n' || chars[i+2] == '\r' {
					inLineComment = true
					i++ // skip second '-'
					continue
				}
			}
		}

		// Handle string quotes
		if c == '\'' && !inDoubleQuote && !inBacktick {
			if inSingleQuote {
				if i > 0 && chars[i-1] == '\\' {
					// escaped backslash
				} else if i+1 < n && chars[i+1] == '\'' {
					current.WriteRune(c)
					i++
					current.WriteRune(chars[i])
					continue
				} else {
					inSingleQuote = false
				}
			} else {
				inSingleQuote = true
			}
		} else if c == '"' && !inSingleQuote && !inBacktick {
			if inDoubleQuote {
				if i > 0 && chars[i-1] == '\\' {
					// escaped
				} else {
					inDoubleQuote = false
				}
			} else {
				inDoubleQuote = true
			}
		} else if c == '`' && !inSingleQuote && !inDoubleQuote {
			if inBacktick {
				inBacktick = false
			} else {
				inBacktick = true
			}
		}

		if c == ';' && !inSingleQuote && !inDoubleQuote && !inBacktick {
			stmt := strings.TrimSpace(current.String())
			if stmt != "" {
				upper := strings.ToUpper(stmt)
				// Skip standalone CREATE DATABASE and USE statements as connection is already bound
				// Also skip CREATE TABLE [IF NOT EXISTS] schema_migrations as it is authoritatively managed by the runner
				if !strings.HasPrefix(upper, "CREATE DATABASE ") &&
					!strings.HasPrefix(upper, "USE ") &&
					!strings.HasPrefix(upper, "CREATE TABLE SCHEMA_MIGRATIONS ") &&
					!strings.HasPrefix(upper, "CREATE TABLE IF NOT EXISTS SCHEMA_MIGRATIONS ") &&
					!strings.HasPrefix(upper, "CREATE TABLE `SCHEMA_MIGRATIONS`") &&
					!strings.HasPrefix(upper, "CREATE TABLE IF NOT EXISTS `SCHEMA_MIGRATIONS`") {
					stmts = append(stmts, stmt)
				}
			}
			current.Reset()
			continue
		}

		current.WriteRune(c)
	}

	remaining := strings.TrimSpace(current.String())
	if remaining != "" {
		upper := strings.ToUpper(remaining)
		if !strings.HasPrefix(upper, "CREATE DATABASE ") &&
			!strings.HasPrefix(upper, "USE ") &&
			!strings.HasPrefix(upper, "CREATE TABLE SCHEMA_MIGRATIONS ") &&
			!strings.HasPrefix(upper, "CREATE TABLE IF NOT EXISTS SCHEMA_MIGRATIONS ") &&
			!strings.HasPrefix(upper, "CREATE TABLE `SCHEMA_MIGRATIONS`") &&
			!strings.HasPrefix(upper, "CREATE TABLE IF NOT EXISTS `SCHEMA_MIGRATIONS`") {
			stmts = append(stmts, remaining)
		}
	}

	return stmts
}
