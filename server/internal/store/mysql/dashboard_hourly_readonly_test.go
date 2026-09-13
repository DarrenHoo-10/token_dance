package mysql

import (
	"context"
	"os"
	"testing"
	"time"
	"tokendance/internal/domain"
)

// Isolated temporary-table integration: never changes shared dev rows or schema.
func TestDashboardHourlyReadOnlyMySQL(t *testing.T) {
	dsn := os.Getenv("TOKENDANCE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("dev DSN unavailable")
	}
	db, err := OpenDB(dsn, DefaultDBConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var name string
	if err = db.QueryRow("SELECT DATABASE()").Scan(&name); err != nil || name != "tokendance_dev" {
		t.Fatal("read-only probe requires tokendance_dev")
	}
	// Connection-local fixtures disappear on close; shared dev rows stay untouched.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	_, err = db.Exec(`CREATE TEMPORARY TABLE bound_telemetry_model_metrics (
        user_id VARCHAR(64), grain VARCHAR(8), bucket_start BIGINT, delete_at BIGINT NULL,
        harness_id VARCHAR(64), model_key BIGINT DEFAULT 0,
        exact_token_total BIGINT DEFAULT 0, derived_token_total BIGINT DEFAULT 0,
        input_context_tokens BIGINT DEFAULT 0, output_tokens BIGINT DEFAULT 0,
        cache_read_tokens BIGINT DEFAULT 0, cache_write_tokens BIGINT DEFAULT 0,
        reasoning_tokens BIGINT DEFAULT 0, updated_at BIGINT, metric_semantics_version INT DEFAULT 1
    )`)
	if err != nil {
		t.Fatal(err)
	}
	user := "dashboard-hourly-fixture"
	bucket := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC).UnixMilli()
	from := domain.StartOfDay(time.UnixMilli(bucket))
	to := from.AddDate(0, 0, 1).Add(-time.Nanosecond)
	r := domain.TimeRange{Key: domain.TimeRangeToday, From: from, To: to, Timezone: "Asia/Shanghai"}
	for i, value := range []int{100, 200} {
		_, err = db.Exec("INSERT INTO bound_telemetry_model_metrics (user_id,grain,bucket_start,harness_id,exact_token_total,updated_at) VALUES (?,'hour',?,'codex',?,?)", user, bucket+int64(i)*3600000, value, bucket)
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.Exec("INSERT INTO bound_telemetry_model_metrics (user_id,grain,bucket_start,harness_id,exact_token_total,updated_at) VALUES (?,'day',?,'codex',300,?)", user, from.UnixMilli(), bucket)
	if err != nil {
		t.Fatal(err)
	}
	a := analyticsStore{db: db}
	response, err := a.GetTokenTrend(context.Background(), user, r, "total", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Granularity != "hour" || len(response.Points) != 2 {
		t.Fatal("missing hourly series")
	}
	for _, p := range response.Points {
		if len(p.Date) != 16 {
			t.Fatalf("unexpected hour label %q", p.Date)
		}
	}
	r.Key = domain.TimeRange7d
	daily, err := a.GetTokenTrend(context.Background(), user, r, "total", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if daily.Granularity != "day" {
		t.Fatal("multi-day query lost day granularity")
	}
	if len(daily.Points) != 1 || *daily.Points[0].TokenTotal != "300" {
		t.Fatal("daily total differs")
	}
	if *response.Points[0].TokenTotal != "100" || *response.Points[1].TokenTotal != "200" {
		t.Fatal("hourly totals differ")
	}
}
