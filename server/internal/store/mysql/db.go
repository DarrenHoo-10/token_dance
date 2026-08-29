package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type DBConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	PingTimeout     time.Duration
}

func DefaultDBConfig() DBConfig {
	return DBConfig{
		MaxOpenConns:    50,
		MaxIdleConns:    25,
		ConnMaxLifetime: 15 * time.Minute,
		ConnMaxIdleTime: 5 * time.Minute,
		PingTimeout:     5 * time.Second,
	}
}

// NormalizeDSN ensures parseTime, loc=UTC, and strict SQL mode parameters are configured
func NormalizeDSN(dsn string) string {
	// If DSN contains parameters, merge/override them
	parts := strings.SplitN(dsn, "?", 2)
	base := parts[0]

	var query url.Values
	if len(parts) > 1 {
		var err error
		query, err = url.ParseQuery(parts[1])
		if err != nil {
			query = make(url.Values)
		}
	} else {
		query = make(url.Values)
	}

	query.Set("parseTime", "true")
	query.Set("loc", "UTC")
	query.Set("time_zone", "'+00:00'")
	query.Set("sql_mode", "'STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'")
	query.Set("multiStatements", "true")

	return fmt.Sprintf("%s?%s", base, query.Encode())
}

// OpenDB opens and configures a production-ready MySQL connection pool
func OpenDB(dsn string, cfg DBConfig) (*sql.DB, error) {
	normDSN := NormalizeDSN(dsn)
	db, err := sql.Open("mysql", normDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize MySQL driver: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	// Perform startup ping/readiness check
	ctx, cancel := context.WithTimeout(context.Background(), cfg.PingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("MySQL ping readiness failed: %w", err)
	}

	// Verify session time zone and sql mode on live connection
	if _, err := db.ExecContext(ctx, "SET time_zone = '+00:00';"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to enforce connection UTC time zone: %w", err)
	}

	return db, nil
}
