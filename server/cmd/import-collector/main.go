package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"tokendance/internal/clock"
	"tokendance/internal/worker"
)

var safeIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

type copySpec struct {
	table       string
	where       string
	conflictKey string
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	sourceDSN := requiredEnv("TOKENDANCE_SOURCE_MYSQL_DSN")
	targetDSN := requiredEnv("TOKENDANCE_MYSQL_DSN")
	sourceUserID := requiredEnv("TOKENDANCE_SOURCE_USER_ID")
	targetUserID := requiredEnv("TOKENDANCE_TARGET_USER_ID")
	sourceSchema := envOr("TOKENDANCE_SOURCE_SCHEMA", "tokenshow")
	targetSchema := envOr("TOKENDANCE_TARGET_SCHEMA", "tokendance")

	for _, identifier := range []string{sourceSchema, targetSchema} {
		if !safeIdentifier.MatchString(identifier) {
			log.Fatalf("unsafe schema identifier %q", identifier)
		}
	}

	source := openDB(ctx, sourceDSN)
	defer source.Close()
	target := openDB(ctx, targetDSN)
	defer target.Close()

	if err := requireUser(ctx, source, sourceSchema, sourceUserID); err != nil {
		log.Fatal(err)
	}
	if err := requireUser(ctx, target, targetSchema, targetUserID); err != nil {
		log.Fatal(err)
	}
	if os.Getenv("TOKENDANCE_BACKFILL_AGGREGATE_ONLY") == "true" {
		rebuildAggregates(ctx, target)
		return
	}

	tx, err := target.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		log.Fatalf("begin target transaction: %v", err)
	}
	defer tx.Rollback()

	specs := []copySpec{
		{
			table:       "installations",
			where:       "user_id = ?",
			conflictKey: "installation_id",
		},
		{
			table:       "ingest_batches",
			where:       "installation_id IN (SELECT installation_id FROM " + quote(sourceSchema) + "." + quote("installations") + " WHERE user_id = ?)",
			conflictKey: "batch_id",
		},
		{
			table:       "usage_events",
			where:       "user_id = ?",
			conflictKey: "event_id",
		},
	}

	for _, spec := range specs {
		copied, inserted, err := copyRows(ctx, source, tx, sourceSchema, targetSchema, spec, sourceUserID, targetUserID)
		if err != nil {
			log.Fatalf("copy %s: %v", spec.table, err)
		}
		log.Printf("%s: read=%d inserted=%d existing=%d", spec.table, copied, inserted, copied-inserted)
	}

	if err := tx.Commit(); err != nil {
		log.Fatalf("commit target transaction: %v", err)
	}
	rebuildAggregates(ctx, target)
	log.Printf("collector backfill completed for target user %s", targetUserID)
}

func rebuildAggregates(ctx context.Context, target *sql.DB) {
	aggregator := worker.NewWorker(target, clock.RealClock{})
	for {
		processed, err := aggregator.ProcessAggregates(ctx)
		if err != nil {
			log.Fatalf("rebuild imported aggregates: %v", err)
		}
		if processed == 0 {
			break
		}
		log.Printf("aggregates: rebuilt=%d", processed)
	}
	log.Printf("aggregate rebuild completed")
}

func copyRows(
	ctx context.Context,
	source *sql.DB,
	target *sql.Tx,
	sourceSchema string,
	targetSchema string,
	spec copySpec,
	sourceUserID string,
	targetUserID string,
) (int64, int64, error) {
	sourceColumns, err := columns(ctx, source, sourceSchema, spec.table)
	if err != nil {
		return 0, 0, err
	}
	targetColumns, err := columns(ctx, target, targetSchema, spec.table)
	if err != nil {
		return 0, 0, err
	}

	targetSet := make(map[string]struct{}, len(targetColumns))
	for _, column := range targetColumns {
		targetSet[column] = struct{}{}
	}
	excluded := map[string]struct{}{"event_pk": {}, "occurred_date": {}}
	common := make([]string, 0, len(sourceColumns))
	for _, column := range sourceColumns {
		if _, skip := excluded[column]; skip {
			continue
		}
		if _, exists := targetSet[column]; exists {
			common = append(common, column)
		}
	}
	if len(common) == 0 {
		return 0, 0, fmt.Errorf("no compatible columns")
	}

	columnSQL := quotedList(common)
	selectSQL := "SELECT " + columnSQL + " FROM " + quote(sourceSchema) + "." + quote(spec.table) + " WHERE " + spec.where
	rows, err := source.QueryContext(ctx, selectSQL, sourceUserID)
	if err != nil {
		return 0, 0, fmt.Errorf("query source: %w", err)
	}
	defer rows.Close()

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(common)), ",")
	insertSQL := "INSERT INTO " + quote(targetSchema) + "." + quote(spec.table) + " (" + columnSQL + ") VALUES (" + placeholders + ") ON DUPLICATE KEY UPDATE " + quote(spec.conflictKey) + " = VALUES(" + quote(spec.conflictKey) + ")"
	stmt, err := target.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare target insert: %w", err)
	}
	defer stmt.Close()

	userIndex := -1
	for index, column := range common {
		if column == "user_id" {
			userIndex = index
			break
		}
	}

	var readCount int64
	var insertCount int64
	for rows.Next() {
		values := make([]any, len(common))
		scanTargets := make([]any, len(common))
		for index := range values {
			scanTargets[index] = &values[index]
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return readCount, insertCount, fmt.Errorf("scan source row: %w", err)
		}
		if userIndex >= 0 {
			values[userIndex] = targetUserID
		}
		for index, column := range common {
			values[index] = normalizeValue(spec.table, column, values[index])
		}
		result, err := stmt.ExecContext(ctx, values...)
		if err != nil {
			return readCount, insertCount, fmt.Errorf("insert target row %d: %w", readCount+1, err)
		}
		readCount++
		affected, err := result.RowsAffected()
		if err != nil {
			return readCount, insertCount, fmt.Errorf("read affected rows: %w", err)
		}
		if affected > 0 {
			insertCount++
		}
	}
	if err := rows.Err(); err != nil {
		return readCount, insertCount, fmt.Errorf("iterate source rows: %w", err)
	}
	return readCount, insertCount, nil
}

func normalizeValue(table string, column string, value any) any {
	if table != "usage_events" || column != "source_kind" {
		return value
	}
	var text string
	switch typed := value.(type) {
	case []byte:
		text = string(typed)
	case string:
		text = typed
	default:
		return value
	}
	if text == "jsonl_tail" {
		return "jsonl"
	}
	return value
}

type columnQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func columns(ctx context.Context, db columnQuerier, schema string, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return nil, fmt.Errorf("query columns for %s.%s: %w", schema, table, err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !safeIdentifier.MatchString(name) {
			return nil, fmt.Errorf("unsafe column identifier %q", name)
		}
		result = append(result, name)
	}
	return result, rows.Err()
}

func requireUser(ctx context.Context, db *sql.DB, schema string, userID string) error {
	var count int
	query := "SELECT COUNT(*) FROM " + quote(schema) + "." + quote("users") + " WHERE user_id = ?"
	if err := db.QueryRowContext(ctx, query, userID).Scan(&count); err != nil {
		return fmt.Errorf("verify user %s: %w", userID, err)
	}
	if count != 1 {
		return fmt.Errorf("expected user %s in %s, found %d", userID, schema, count)
	}
	return nil
}

func openDB(ctx context.Context, dsn string) *sql.DB {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("ping database: %v", err)
	}
	return db
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("%s is required", name)
	}
	return value
}

func envOr(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func quote(identifier string) string {
	return "`" + identifier + "`"
}

func quotedList(columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = quote(column)
	}
	return strings.Join(quoted, ",")
}
