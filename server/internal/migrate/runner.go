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
	"sort"
	"strings"
	"time"

	"tokendance/internal/domain"
)

//go:embed all:migrations/*.sql
var EmbeddedMigrations embed.FS

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
		path := filepath.Join(dir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", path, err)
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

// RunMigrations applies migrations in order to the database
func (r *Runner) RunMigrations(ctx context.Context) error {
	if r.db == nil {
		return fmt.Errorf("database connection is nil")
	}

	// 1. Ensure schema_migrations table exists
	createTableSQL := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
	  version             VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
	  checksum_sha256     BINARY(32) NOT NULL,
	  applied_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
	  PRIMARY KEY (version)
	) ENGINE = InnoDB;`

	if _, err := r.db.ExecContext(ctx, createTableSQL); err != nil {
		return fmt.Errorf("failed to ensure schema_migrations table: %w", err)
	}

	// 2. Query applied migrations
	rows, err := r.db.QueryContext(ctx, "SELECT version, checksum_sha256, applied_at FROM schema_migrations ORDER BY version ASC")
	if err != nil {
		return fmt.Errorf("failed to query schema_migrations: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]MigrationInfo)
	for rows.Next() {
		var ver string
		var chk []byte
		var appliedAt time.Time
		if err := rows.Scan(&ver, &chk, &appliedAt); err != nil {
			return fmt.Errorf("failed to scan migration row: %w", err)
		}
		var sum [32]byte
		copy(sum[:], chk)
		applied[ver] = MigrationInfo{
			Version:   ver,
			Checksum:  sum,
			AppliedAt: &appliedAt,
		}
	}

	// 3. Process each migration
	for _, m := range r.migrations {
		if prev, ok := applied[m.Version]; ok {
			// Verify checksum
			if prev.Checksum != m.Checksum {
				return fmt.Errorf("checksum mismatch for migration %s: applied %x != current %x", m.Version, prev.Checksum, m.Checksum)
			}
			continue
		}

		// Preflight check if 0002
		if m.Version == "0002" {
			if err := r.CheckBaselineGuard(ctx); err != nil {
				return fmt.Errorf("migration %s dirty baseline preflight failed: %w", m.Version, err)
			}
		}

		// Execute migration
		statements := splitSQLStatements(m.Content)
		for _, stmt := range statements {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := r.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("failed executing migration %s statement [%s]: %w", m.Filename, stmt, err)
			}
		}

		// Record in schema_migrations
		insertSQL := "INSERT INTO schema_migrations (version, checksum_sha256, applied_at) VALUES (?, ?, ?)"
		if _, err := r.db.ExecContext(ctx, insertSQL, m.Version, m.Checksum[:], time.Now().UTC()); err != nil {
			return fmt.Errorf("failed to record applied migration %s: %w", m.Version, err)
		}
	}

	return nil
}

func splitSQLStatements(sql string) []string {
	var stmts []string
	var current strings.Builder
	lines := strings.Split(sql, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		current.WriteString(line)
		current.WriteString("\n")
		if strings.HasSuffix(trimmed, ";") {
			stmts = append(stmts, current.String())
			current.Reset()
		}
	}
	if strings.TrimSpace(current.String()) != "" {
		stmts = append(stmts, current.String())
	}
	return stmts
}
