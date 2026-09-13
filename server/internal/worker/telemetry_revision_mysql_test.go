package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	driver "github.com/go-sql-driver/mysql"
	"os"
	"strings"
	"testing"
	"time"
	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
	mysqlstore "tokendance/internal/store/mysql"
)

// Uses connection-local temporary tables only. Safe against the shared dev DB;
// never call the schema-reset integration helper from this test.
func TestUsageRevisionTemporaryMySQL(t *testing.T) {
	dsn := os.Getenv("TOKENDANCE_REVISION_TEST_DSN")
	if dsn == "" {
		t.Skip("explicit dev DSN required")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if cfg.DBName != "tokendance_dev" {
		t.Fatal("test requires tokendance_dev")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	metricDDL := `CREATE TEMPORARY TABLE telemetry_model_metrics (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,created_at BIGINT UNSIGNED NOT NULL,updated_at BIGINT UNSIGNED NOT NULL,delete_at BIGINT UNSIGNED NULL,extra JSON NULL,installation_id VARCHAR(100),grain VARCHAR(10),bucket_start BIGINT,harness_id VARCHAR(100),model_key BIGINT UNSIGNED,metric_semantics_version INT,`
	cols := strings.Fields("exact_token_total derived_token_total input_context_tokens input_uncached_tokens output_tokens cache_read_tokens cache_write_tokens reasoning_tokens tool_extra_tokens model_request_count usage_observed_count token_total_known_count input_context_known_count input_uncached_known_count output_known_count cache_read_known_count cache_write_known_count reasoning_known_count tool_extra_known_count cache_eligible_input_tokens cache_eligible_read_tokens cache_pair_known_count")
	for _, col := range cols {
		metricDDL += col + " BIGINT UNSIGNED NOT NULL DEFAULT 0,"
	}
	metricDDL += `UNIQUE(installation_id,grain,bucket_start,harness_id,model_key),CHECK(token_total_known_count<=usage_observed_count),CHECK(cache_eligible_read_tokens<=cache_eligible_input_tokens))`
	for _, q := range []string{
		metricDDL,
		`CREATE TEMPORARY TABLE aggregate_dirty_days (user_id VARCHAR(100),metric_date DATE,dirty_version BIGINT,applied_version BIGINT,next_attempt_at BIGINT,created_at BIGINT,updated_at BIGINT,extra JSON,claim_token VARCHAR(100),last_error_code VARCHAR(100),PRIMARY KEY(user_id,metric_date))`,
		`CREATE TEMPORARY TABLE telemetry_events (id BIGINT PRIMARY KEY,installation_id VARCHAR(100),fact_key BINARY(32),fact_revision BIGINT,occurred_at BIGINT,model_key BIGINT,payload_json JSON,metric_semantics_version INT,status_json JSON,delete_at BIGINT NULL,updated_at BIGINT)`,
	} {
		if _, err = db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	for _, grain := range []string{"hour", "day", "month"} {
		for _, reverse := range []bool{false, true} {
			for _, q := range []string{"DELETE FROM telemetry_model_metrics", "DELETE FROM telemetry_events", "DELETE FROM aggregate_dirty_days"} {
				if _, err = db.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			at := int64(1789257600000)
			bucket, err := domain.BucketStartMs(grain, at)
			if err != nil {
				t.Fatal(err)
			}
			payloads := []string{`{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":"10","input_context_tokens":"8","output_tokens":"2"}}`, `{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":"20","input_context_tokens":"18","output_tokens":"2"}}`}
			for i, p := range payloads {
				_, err = db.ExecContext(ctx, `INSERT INTO telemetry_events VALUES (?, 'fixture-device', UNHEX(REPEAT('ab',32)), ?, ?, 0, ?, 1, JSON_OBJECT('hour',2,'day',2,'month',2), NULL, ?)`, i+1, i+1, at, p, at)
				if err != nil {
					t.Fatal(err)
				}
			}
			order := []int{0, 1}
			if reverse {
				order = []int{1, 0}
			}
			w := &Worker{}
			for _, i := range order {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				ev := telemetryEventRow{ID: uint64(i + 1), InstallationID: "fixture-device", UserID: "fixture-user", HarnessID: "codex", EventType: "model_usage_recorded", OccurredAtMs: at, MetricSemanticsVersion: 1}
				apply, err := w.prepareFactRevision(ctx, tx, grain, &ev, at)
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				status := 4
				if apply {
					var p v2.EventPayload
					if err = json.Unmarshal([]byte(payloads[i]), &p); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					d := map[string]int64{}
					if err = accumulateUsage(&p, d); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					if err = mysqlstore.ApplyModelMetricDeltaTx(ctx, tx, "fixture-user", "fixture-device", grain, bucket, "codex", 0, 1, at, d); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					status = 3
				}
				if _, err = tx.ExecContext(ctx, "UPDATE telemetry_events SET status_json=JSON_SET(status_json,?,?) WHERE id=?", "$."+grain, status, i+1); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			var tokens, known, requests int64
			if err = db.QueryRowContext(ctx, "SELECT exact_token_total,token_total_known_count,model_request_count FROM telemetry_model_metrics WHERE delete_at IS NULL").Scan(&tokens, &known, &requests); err != nil {
				t.Fatal(err)
			}
			if tokens != 20 || known != 1 || requests != 1 {
				t.Fatalf("%s reverse=%v: got tokens=%d known=%d requests=%d", grain, reverse, tokens, known, requests)
			}
		}
	}
	if _, err = db.ExecContext(ctx, `CREATE TEMPORARY TABLE telemetry_harness_metrics (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,created_at BIGINT UNSIGNED,updated_at BIGINT UNSIGNED,delete_at BIGINT UNSIGNED NULL,installation_id VARCHAR(100),grain VARCHAR(10),bucket_start BIGINT,harness_id VARCHAR(100),metric_semantics_version INT,active_duration_ms BIGINT UNSIGNED DEFAULT 0,duration_known_count BIGINT UNSIGNED DEFAULT 0,code_generated_lines BIGINT UNSIGNED DEFAULT 0,code_known_count BIGINT UNSIGNED DEFAULT 0,code_file_touch_count BIGINT UNSIGNED DEFAULT 0,UNIQUE(installation_id,grain,bucket_start,harness_id))`); err != nil {
		t.Fatal(err)
	}
	for _, grain := range []string{"hour", "day", "month"} {
		for _, reverse := range []bool{false, true} {
			for _, q := range []string{"DELETE FROM telemetry_harness_metrics", "DELETE FROM telemetry_events", "DELETE FROM aggregate_dirty_days"} {
				if _, err = db.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			at := int64(1789257600000)
			bucket, err := domain.BucketStartMs(grain, at)
			if err != nil {
				t.Fatal(err)
			}
			payloads := []string{`{"meta":{"accuracy":"exact","time_source":"source_record"},"code":{"generated":"10","file_touch_count":"1"}}`, `{"meta":{"accuracy":"exact","time_source":"source_record"},"code":{"generated":"20","file_touch_count":"1"}}`}
			for i, p := range payloads {
				_, err = db.ExecContext(ctx, `INSERT INTO telemetry_events VALUES (?, 'fixture-device', UNHEX(REPEAT('ab',32)), ?, ?, 0, ?, 1, JSON_OBJECT('hour',2,'day',2,'month',2), NULL, ?)`, i+1, i+1, at, p, at)
				if err != nil {
					t.Fatal(err)
				}
			}
			order := []int{0, 1}
			if reverse {
				order = []int{1, 0}
			}
			w := &Worker{}
			for _, i := range order {
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				ev := telemetryEventRow{ID: uint64(i + 1), InstallationID: "fixture-device", UserID: "fixture-user", HarnessID: "codex", EventType: "code_changed", OccurredAtMs: at, MetricSemanticsVersion: 1}
				apply, err := w.prepareFactRevision(ctx, tx, grain, &ev, at)
				if err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				status := 4
				if apply {
					var p v2.EventPayload
					if err = json.Unmarshal([]byte(payloads[i]), &p); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					d := map[string]int64{}
					accumulateCode(&p, d)
					if err = mysqlstore.ApplyHarnessMetricDeltaTx(ctx, tx, "fixture-user", "fixture-device", grain, bucket, "codex", 1, at, d); err != nil {
						tx.Rollback()
						t.Fatal(err)
					}
					status = 3
				}
				if _, err = tx.ExecContext(ctx, "UPDATE telemetry_events SET status_json=JSON_SET(status_json,?,?) WHERE id=?", "$."+grain, status, i+1); err != nil {
					tx.Rollback()
					t.Fatal(err)
				}
				if err = tx.Commit(); err != nil {
					t.Fatal(err)
				}
			}
			var tokens, known, requests int64
			if err = db.QueryRowContext(ctx, "SELECT code_generated_lines,code_known_count,code_file_touch_count FROM telemetry_harness_metrics WHERE delete_at IS NULL").Scan(&tokens, &known, &requests); err != nil {
				t.Fatal(err)
			}
			if tokens != 20 || known != 1 || requests != 1 {
				t.Fatalf("%s reverse=%v: got tokens=%d known=%d requests=%d", grain, reverse, tokens, known, requests)
			}
		}
	}

	if _, err = db.ExecContext(ctx, `CREATE TEMPORARY TABLE telemetry_session_extents(id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,created_at BIGINT UNSIGNED,updated_at BIGINT UNSIGNED,delete_at BIGINT UNSIGNED NULL,installation_id VARCHAR(100),harness_id VARCHAR(100),session_key BINARY(32),grain VARCHAR(10),first_event_at BIGINT,last_event_at BIGINT,UNIQUE(installation_id,harness_id,session_key,grain))`); err != nil {
		t.Fatal(err)
	}
	for _, grain := range []string{"hour", "day", "month"} {
		if _, err = db.ExecContext(ctx, "DELETE FROM telemetry_harness_metrics"); err != nil {
			t.Fatal(err)
		}
		start := time.Date(2024, 5, 31, 23, 30, 0, 0, domain.DayTZ).UnixMilli()
		end := start + 3600000
		for _, at := range []int64{end, start, end, start} {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			ev := telemetryEventRow{InstallationID: "fixture-device", UserID: "fixture-user", HarnessID: "codex", EventType: "turn_completed", SessionKey: make([]byte, 32), OccurredAtMs: at, MetricSemanticsVersion: 1}
			if _, err = applySessionExtent(ctx, tx, grain, &ev, end); err != nil {
				tx.Rollback()
				t.Fatal(err)
			}
			if err = tx.Commit(); err != nil {
				t.Fatal(err)
			}
		}
		var duration, known, count int64
		if err = db.QueryRowContext(ctx, "SELECT SUM(active_duration_ms),SUM(duration_known_count),COUNT(*) FROM telemetry_harness_metrics").Scan(&duration, &known, &count); err != nil {
			t.Fatal(err)
		}
		if duration != 3600000 || known != 2 || count != 2 {
			t.Fatalf("%s duration=%d known=%d count=%d", grain, duration, known, count)
		}
	}
}
