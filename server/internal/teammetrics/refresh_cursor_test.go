package teammetrics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

var registerSingleCursorDriver sync.Once

type singleCursorDriver struct{}

func (singleCursorDriver) Open(string) (driver.Conn, error) { return &singleCursorConn{}, nil }

type singleCursorConn struct{ rowsOpen bool }

func (*singleCursorConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (*singleCursorConn) Close() error              { return nil }
func (*singleCursorConn) Begin() (driver.Tx, error) { return singleCursorTx{}, nil }

func (c *singleCursorConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.rowsOpen {
		return nil, driver.ErrBadConn
	}
	var values [][]driver.Value
	var columns []string
	switch {
	case strings.Contains(query, "FROM bound_telemetry_model_metrics m") && strings.Contains(query, "GROUP BY m.harness_id"):
		columns = make([]string, 14)
		values = [][]driver.Value{{"codex", "provider", "model", "12", "0", "1", "8", "4", "0", "0", "0", "1", "1", "1"}}
	case strings.Contains(query, "GROUP BY m.bucket_start"):
		columns = make([]string, 3)
		values = [][]driver.Value{{int64(1789380000000), "12", "0"}}
	default:
		columns = []string{"unused"}
	}
	c.rowsOpen = true
	return &singleCursorRows{conn: c, columns: columns, values: values}, nil
}

type singleCursorTx struct{}

func (singleCursorTx) Commit() error   { return nil }
func (singleCursorTx) Rollback() error { return nil }

type singleCursorRows struct {
	conn    *singleCursorConn
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *singleCursorRows) Columns() []string { return r.columns }
func (r *singleCursorRows) Close() error {
	r.conn.rowsOpen = false
	return nil
}
func (r *singleCursorRows) Next(dest []driver.Value) error {
	if r.index >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func TestJoinDayUsageClosesCursorBeforeHourlyQuery(t *testing.T) {
	registerSingleCursorDriver.Do(func() { sql.Register("teammetrics_single_cursor", singleCursorDriver{}) })
	db, err := sql.Open("teammetrics_single_cursor", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	joinedAt := time.Date(2026, 9, 14, 0, 37, 0, 0, time.FixedZone("CST", 8*3600))
	rows, err := projectJoinDayHours(context.Background(), tx, Contributor{TeamID: "team", UserID: "user", ContributorKey: "contributor"}, "2026-09-14", joinedAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].MetricKind != KindUsage || rows[0].TokenExact != "12" {
		t.Fatalf("expected one usage row after join, got %#v", rows)
	}
}
