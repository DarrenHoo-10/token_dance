package mysql

import (
	"context"
	"os"
	"testing"
	"time"
	"tokendance/internal/domain"
)

// Read-only integration against the shared dev database: never resets its schema.
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
	var user string
	var bucket int64
	if err = db.QueryRow("SELECT user_id,bucket_start FROM telemetry_model_metrics WHERE grain='hour' AND delete_at IS NULL ORDER BY bucket_start DESC LIMIT 1").Scan(&user, &bucket); err != nil {
		t.Fatal(err)
	}
	from := domain.StartOfDay(time.UnixMilli(bucket))
	to := from.AddDate(0, 0, 1).Add(-time.Nanosecond)
	r := domain.TimeRange{Key: domain.TimeRangeToday, From: from, To: to, Timezone: "Asia/Shanghai"}
	a := analyticsStore{db: db}
	response, err := a.GetTokenTrend(context.Background(), user, r, "total", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.Granularity != "hour" || len(response.Points) == 0 {
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
	summary, err := a.GetPersonalSummary(context.Background(), user, r)
	if err != nil {
		t.Fatal(err)
	}
	if summary == nil {
		t.Fatal("missing summary")
	}
}
