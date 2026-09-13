package grayscale

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type columnQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type session interface {
	columnQuerier
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

type tableMeta struct {
	columns []string
	pk      map[string]struct{}
}

type copySpec struct {
	table      string
	dateColumn string
	transform  func(columns []string, values []any) error
}

func loadTableMeta(ctx context.Context, db columnQuerier, schema, table string) (tableMeta, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT column_name, extra, column_key
		FROM information_schema.columns
		WHERE table_schema = ? AND table_name = ?
		ORDER BY ordinal_position`, schema, table)
	if err != nil {
		return tableMeta{}, fmt.Errorf("describe %s.%s: %w", schema, table, err)
	}
	defer rows.Close()
	meta := tableMeta{pk: map[string]struct{}{}}
	for rows.Next() {
		var name, extra, key string
		if err := rows.Scan(&name, &extra, &key); err != nil {
			return tableMeta{}, err
		}
		if !safeIdentifier.MatchString(name) {
			return tableMeta{}, fmt.Errorf("unsafe column %q", name)
		}
		upper := strings.ToUpper(extra)
		// DEFAULT_GENERATED (e.g. CURRENT_TIMESTAMP) is writable. Omitting it
		// changes source timestamps on every replacement and invalidates caches.
		if strings.Contains(upper, "VIRTUAL GENERATED") || strings.Contains(upper, "STORED GENERATED") || strings.Contains(upper, "AUTO_INCREMENT") {
			continue
		}
		meta.columns = append(meta.columns, name)
		if key == "PRI" {
			meta.pk[name] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return tableMeta{}, err
	}
	if len(meta.columns) == 0 {
		return tableMeta{}, fmt.Errorf("no writable columns for %s.%s", schema, table)
	}
	return meta, nil
}

func intersectColumns(source, target tableMeta) []string {
	allowed := make(map[string]struct{}, len(target.columns))
	for _, column := range target.columns {
		allowed[column] = struct{}{}
	}
	common := make([]string, 0, len(source.columns))
	for _, column := range source.columns {
		if _, ok := allowed[column]; ok {
			common = append(common, column)
		}
	}
	return common
}

func quote(identifier string) string {
	return "`" + identifier + "`"
}

func quotedList(columns []string) string {
	parts := make([]string, len(columns))
	for i, column := range columns {
		parts[i] = quote(column)
	}
	return strings.Join(parts, ",")
}

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func inClause(count int) string {
	return "(" + placeholders(count) + ")"
}

func upsertSQL(schema, table string, columns []string, pk map[string]struct{}) string {
	assignments := make([]string, 0, len(columns))
	for _, column := range columns {
		if _, isPK := pk[column]; isPK {
			continue
		}
		// Login identity belongs to the test database. New mirrors receive
		// sanitized placeholders, but later refreshes must retain local bindings.
		if table == "users" {
			switch column {
			case "auth_subject_hash", "email_lookup_hash", "email_ciphertext", "email_verified_at":
				continue
			}
		}
		quoted := quote(column)
		assignments = append(assignments, quoted+" = VALUES("+quoted+")")
	}
	if len(assignments) == 0 {
		pkCol := quote(columns[0])
		assignments = append(assignments, pkCol+" = VALUES("+pkCol+")")
	}
	return "INSERT INTO " + quote(schema) + "." + quote(table) + " (" + quotedList(columns) + ") VALUES (" + placeholders(len(columns)) + ") ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ",")
}

func anyStrings(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}

func copyTable(
	ctx context.Context,
	source *sql.DB,
	target session,
	cfg Config,
	spec copySpec,
	userIDs []string,
	since string,
) (int64, error) {
	sourceMeta, err := loadTableMeta(ctx, source, cfg.SourceSchema, spec.table)
	if err != nil {
		return 0, err
	}
	targetMeta, err := loadTableMeta(ctx, target, cfg.TargetSchema, spec.table)
	if err != nil {
		return 0, err
	}
	columns := intersectColumns(sourceMeta, targetMeta)
	if len(columns) == 0 {
		return 0, fmt.Errorf("no shared columns for %s", spec.table)
	}
	selectSQL := "SELECT " + quotedList(columns) + " FROM " + quote(cfg.SourceSchema) + "." + quote(spec.table) +
		" WHERE user_id IN " + inClause(len(userIDs))
	args := anyStrings(userIDs)
	if spec.dateColumn != "" && since != "" {
		if !safeIdentifier.MatchString(spec.dateColumn) {
			return 0, fmt.Errorf("unsafe date column %q", spec.dateColumn)
		}
		selectSQL += " AND " + quote(spec.dateColumn) + " >= ?"
		args = append(args, since)
	}
	selectSQL += " ORDER BY " + quotedList(columns)
	rows, err := source.QueryContext(ctx, selectSQL, args...)
	if err != nil {
		return 0, fmt.Errorf("query %s: %w", spec.table, err)
	}
	defer rows.Close()

	stmt, err := target.PrepareContext(ctx, upsertSQL(cfg.TargetSchema, spec.table, columns, targetMeta.pk))
	if err != nil {
		return 0, fmt.Errorf("prepare %s: %w", spec.table, err)
	}
	defer stmt.Close()

	var written int64
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return written, fmt.Errorf("scan %s: %w", spec.table, err)
		}
		if spec.transform != nil {
			if err := spec.transform(columns, values); err != nil {
				return written, err
			}
		}
		if _, err := stmt.ExecContext(ctx, values...); err != nil {
			return written, fmt.Errorf("upsert %s: %w", spec.table, err)
		}
		written++
	}
	return written, rows.Err()
}
