package worker

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/email"
	"tokendance/internal/migrate"
	v2 "tokendance/internal/protocol/v2"
	"tokendance/internal/provider"
	"tokendance/internal/store/mysql"
)

func b64url32Seed(seed string) v2.Base64Url32 {
	sum := crypto.SHA256([]byte(seed))
	return v2.Base64Url32(base64.RawURLEncoding.EncodeToString(sum[:]))
}

func makeAggEvent(t *testing.T, seed string, occurredAtMs int64, mutate func(map[string]any)) v2.EventEnvelope {
	t.Helper()
	eventMap := map[string]any{
		"eventId":                string(b64url32Seed("event:" + seed)),
		"factKey":                string(b64url32Seed("fact:" + seed)),
		"factRevision":           "1",
		"schemaVersion":          float64(2),
		"metricSemanticsVersion": float64(1),
		"harnessId":              "codex",
		"eventType":              "model_usage_recorded",
		"occurredAt":             fmt.Sprintf("%d", occurredAtMs),
		"model": map[string]any{
			"providerId": "openai",
			"modelId":    "gpt-test",
		},
		"payload": map[string]any{
			"usage": map[string]any{
				"token_total": "10",
			},
			"meta": map[string]any{
				"accuracy":    "exact",
				"time_source": "source_record",
			},
		},
	}
	if mutate != nil {
		mutate(eventMap)
	}
	hash, err := v2.ComputeContentHash(eventMap)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	eventMap["contentHash"] = hash
	raw, err := json.Marshal(eventMap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var env v2.EventEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return env
}

func seedAggUserInstall(t *testing.T, st *mysql.Store, userID, installationID string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	db := st.DB()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (user_id, auth_subject_hash, display_name, account_status, leaderboard_visibility, timezone_name, created_at, updated_at)
		VALUES (?, UNHEX(SHA2(?, 256)), 'Agg User', 'active', 'private', 'UTC', ?, ?)`,
		userID, "subject:"+userID, now, now); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	pk := crypto.SHA256([]byte("pk:" + installationID))
	if _, err := db.ExecContext(ctx, `
		INSERT INTO installations (installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, registered_at, updated_at)
		VALUES (?, ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		installationID, userID, pk[:], now, now); err != nil {
		t.Fatalf("seed installation: %v", err)
	}
}

func commitAggEvents(t *testing.T, st *mysql.Store, userID, installationID, nonce string, now time.Time, events ...v2.EventEnvelope) {
	t.Helper()
	_, err := st.Ingest().CommitTelemetryEventsV2(context.Background(), domain.TelemetryEventsV2Input{
		InstallationID:       installationID,
		UserID:               userID,
		BindingStatusVersion: 1,
		NonceHash:            crypto.SHA256([]byte(nonce)),
		NonceExpiresAt:       now.Add(time.Minute),
		RequestID:            "req_" + nonce,
		Events:               events,
		ReceivedAt:           now,
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func setupAggDB(t *testing.T) (*mysql.Store, *Worker, func()) {
	t.Helper()
	db := getTestMySQLDB(t)
	_, _ = db.Exec("SELECT GET_LOCK('tokendance_global_test_lock', 60)")
	ctx := context.Background()
	runner := migrate.NewRunner(db)
	if err := runner.ResetCleanSchema(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := runner.RunMigrations(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	st := mysql.NewStore(db)
	now := time.Now().UTC().Truncate(time.Millisecond)
	w := NewWorkerWithFull(db, clock.NewMockClock(now), nil, email.DefaultSink, provider.NewMemoryObjectStorage(""))
	cleanup := func() {
		_, _ = db.Exec("SELECT RELEASE_LOCK('tokendance_global_test_lock')")
		db.Close()
	}
	return st, w, cleanup
}

func TestTelemetryAggregationReplayNoDoubleAdd(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	now := w.clk.Now()
	userID, installationID := "usr_agg_replay", "ins_agg_replay"
	seedAggUserInstall(t, st, userID, installationID, now)

	ev := makeAggEvent(t, "replay-1", now.UnixMilli(), nil)
	commitAggEvents(t, st, userID, installationID, "nonce-replay-1", now, ev)

	n, err := w.ProcessTelemetryAggregation(context.Background())
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if n == 0 {
		t.Fatal("expected tasks processed")
	}
	var exact string
	if err := st.DB().QueryRow(`
		SELECT CAST(exact_token_total AS CHAR) FROM telemetry_model_metrics
		WHERE user_id=? AND grain='day'`, userID).Scan(&exact); err != nil {
		t.Fatalf("read metrics: %v", err)
	}
	if exact != "10" {
		t.Fatalf("expected exact 10, got %s", exact)
	}
	var tasks int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM telemetry_tasks WHERE event_row_id IN (SELECT id FROM telemetry_events WHERE user_id=?)`, userID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Fatalf("expected tasks deleted, got %d", tasks)
	}

	// Re-insert a duplicate task against already-applied day status must not double-add.
	var eventID uint64
	if err := st.DB().QueryRow(`SELECT id FROM telemetry_events WHERE user_id=?`, userID).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	nowMs := now.UnixMilli()
	if _, err := st.DB().Exec(`
		INSERT INTO telemetry_tasks (created_at, updated_at, event_row_id, consumer, runnable_at, attempt_count)
		VALUES (?, ?, ?, 'day', ?, 0)`, nowMs, nowMs, eventID, nowMs); err != nil {
		t.Fatalf("reinsert task: %v", err)
	}
	if _, err := w.ProcessTelemetryAggregation(context.Background()); err != nil {
		t.Fatalf("second aggregate: %v", err)
	}
	if err := st.DB().QueryRow(`
		SELECT CAST(exact_token_total AS CHAR) FROM telemetry_model_metrics
		WHERE user_id=? AND grain='day'`, userID).Scan(&exact); err != nil {
		t.Fatal(err)
	}
	if exact != "10" {
		t.Fatalf("replay double-added: got %s", exact)
	}
}

func TestTelemetryAggregationConcurrentDevices(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	now := w.clk.Now()
	userID := "usr_agg_parallel"
	seedAggUserInstall(t, st, userID, "ins_agg_a", now)
	pk := crypto.SHA256([]byte("pk:ins_agg_b"))
	if _, err := st.DB().Exec(`
		INSERT INTO installations (installation_id, user_id, device_public_key, os_type, architecture, collector_version, installation_status, registered_at, updated_at)
		VALUES ('ins_agg_b', ?, ?, 'windows', 'x86_64', '1.0.0', 'active', ?, ?)`,
		userID, pk[:], now, now); err != nil {
		t.Fatalf("seed b: %v", err)
	}
	commitAggEvents(t, st, userID, "ins_agg_a", "n-a", now, makeAggEvent(t, "dev-a", now.UnixMilli(), nil))
	commitAggEvents(t, st, userID, "ins_agg_b", "n-b", now, makeAggEvent(t, "dev-b", now.UnixMilli(), func(m map[string]any) {
		m["payload"].(map[string]any)["usage"].(map[string]any)["token_total"] = "7"
	}))

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = w.ProcessTelemetryAggregation(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
	}
	// Drain any remaining tasks serially.
	for {
		n, err := w.ProcessTelemetryAggregation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	var sum string
	if err := st.DB().QueryRow(`
		SELECT CAST(COALESCE(SUM(exact_token_total),0) AS CHAR)
		FROM telemetry_model_metrics WHERE user_id=? AND grain='day'`, userID).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != "17" {
		t.Fatalf("expected 17 across devices, got %s", sum)
	}
}

func TestDirtyDayVersionConfirmDoesNotSwallowMidFlight(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	now := w.clk.Now()
	userID, installationID := "usr_dirty_ver", "ins_dirty_ver"
	seedAggUserInstall(t, st, userID, installationID, now)
	commitAggEvents(t, st, userID, installationID, "n-dirty-1", now, makeAggEvent(t, "dirty-1", now.UnixMilli(), nil))
	if _, err := w.ProcessTelemetryAggregation(context.Background()); err != nil {
		t.Fatal(err)
	}

	claims, err := mysql.ClaimDirtyDays(context.Background(), st.DB(), now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected 1 dirty claim, got %d", len(claims))
	}
	claimed := claims[0]

	// Mid-flight dirty bump.
	metricDate := claimed.MetricDate
	tx, err := st.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := mysql.MarkAggregateDirtyDayTx(context.Background(), tx, userID, metricDate, now); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	ok, err := w.refreshDirtyDay(context.Background(), claimed, now)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("stale claim must not confirm when dirty_version advanced")
	}
	var dirty, applied uint64
	var next sql.NullInt64
	if err := st.DB().QueryRow(`
		SELECT dirty_version, applied_version, next_attempt_at
		FROM aggregate_dirty_days WHERE user_id=? AND metric_date=?`,
		userID, metricDate).Scan(&dirty, &applied, &next); err != nil {
		t.Fatal(err)
	}
	if dirty <= claimed.DirtyVersion {
		t.Fatalf("expected dirty bumped, got %d", dirty)
	}
	if applied >= dirty {
		t.Fatalf("applied must lag dirty: applied=%d dirty=%d", applied, dirty)
	}
	if !next.Valid {
		t.Fatal("expected requeue next_attempt_at")
	}

	claims2, err := mysql.ClaimDirtyDays(context.Background(), st.DB(), now.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(claims2) != 1 {
		t.Fatalf("expected requeued claim, got %d", len(claims2))
	}
	ok, err = w.refreshDirtyDay(context.Background(), claims2[0], now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected confirm")
	}
	var tokens uint64
	if err := st.DB().QueryRow(`
		SELECT token_total FROM user_window_scores
		WHERE user_id=? AND window_key='today' AND generation=?`,
		userID, mysql.WindowGeneration(now)).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 10 {
		t.Fatalf("expected window tokens 10, got %d", tokens)
	}
}
