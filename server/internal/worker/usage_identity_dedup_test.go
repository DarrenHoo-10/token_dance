package worker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
	"tokendance/internal/store/mysql"
)

func commitIdentityEvent(t *testing.T, st *mysql.Store, user, install, seed string, now time.Time, hash [32]byte, cost bool, fact string, revision string) uint64 {
	t.Helper()
	ev := makeAggEvent(t, seed, now.UnixMilli(), func(m map[string]any) {
		m["factKey"] = string(b64url32Seed(fact))
		m["factRevision"] = revision
		if cost {
			m["costScopeKey"] = string(b64url32Seed(fact))
			m["eventType"] = "cost_recorded"
			m["payload"] = map[string]any{"cost": map[string]any{"units": "100", "currency": "USD", "source": "calculated_price"}, "meta": map[string]any{"accuracy": "derived", "time_source": "source_record"}}
		}
	})
	result, err := st.Ingest().CommitTelemetryEventsV2(context.Background(), domain.TelemetryEventsV2Input{
		UserID: user, InstallationID: install, BindingStatusVersion: 1, NonceHash: crypto.SHA256([]byte(seed)), NonceExpiresAt: now.Add(time.Hour), RequestID: seed, ReceivedAt: now, Events: []v2.EventEnvelope{ev}, UsageIdentities: []domain.TelemetryUsageIdentity{{Hash: hash}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Acks) != 1 || result.Acks[0].Result != v2.AckResultAccepted {
		t.Fatalf("unexpected ack: %+v", result)
	}
	var id uint64
	if err = st.DB().QueryRow(`SELECT MAX(id) FROM telemetry_events WHERE installation_id=?`, install).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func drainIdentityTasks(t *testing.T, w *Worker) {
	t.Helper()
	for i := 0; i < 20; i++ {
		n, err := w.ProcessTelemetryAggregation(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
	}
	t.Fatal("tasks did not drain")
}

func TestUsageIdentityDedupAndRepair(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	now := w.clk.Now()
	ctx := context.Background()
	user, a, b := "usr_identity_fix", "ins_identity_a", "ins_identity_b"
	seedAggUserInstall(t, st, user, a, now) // second installation of the same account
	_, err := st.DB().Exec(`INSERT INTO installations(installation_id,user_id,device_public_key,os_type,architecture,collector_version,installation_status,registered_at,updated_at) VALUES(?,?,?,'windows','x86_64','1.0.0','active',?,?)`, b, user, make([]byte, 32), now, now)
	if err != nil {
		t.Fatal(err)
	}
	hash := crypto.SHA256([]byte("request-one"))
	// Cost and usage share an identity, but neither can shadow the other.
	commitIdentityEvent(t, st, user, a, "cost-first", now, hash, true, "cost-one", "1")
	usage := commitIdentityEvent(t, st, user, a, "usage-second", now, hash, false, "usage-one", "1")
	drainIdentityTasks(t, w)
	assertTotals := func(tokens, cost int) {
		t.Helper()
		for _, grain := range []string{"hour", "day", "month"} {
			var gotTokens, gotCost int
			if err := st.DB().QueryRow(`SELECT COALESCE(SUM(exact_token_total),0) FROM telemetry_model_metrics WHERE grain=?`, grain).Scan(&gotTokens); err != nil {
				t.Fatal(err)
			}
			if err := st.DB().QueryRow(`SELECT COALESCE(SUM(estimated_cost_units),0) FROM telemetry_cost_metrics WHERE grain=?`, grain).Scan(&gotCost); err != nil {
				t.Fatal(err)
			}
			if gotTokens != tokens || gotCost != cost {
				t.Fatalf("%s tokens=%d cost=%d, want %d/%d", grain, gotTokens, gotCost, tokens, cost)
			}
		}
	}
	assertTotals(10, 100)
	// Original/mirror copies on another device still deduplicate, including
	// that mirror's own revision chain.
	commitIdentityEvent(t, st, user, b, "mirror-usage", now, hash, false, "mirror-fact", "1")
	commitIdentityEvent(t, st, user, b, "mirror-revision", now, hash, false, "mirror-fact", "2")
	commitIdentityEvent(t, st, user, b, "mirror-cost", now, hash, true, "mirror-cost-fact", "1")
	commitIdentityEvent(t, st, user, a, "owner-revision", now, hash, false, "usage-one", "2")
	drainIdentityTasks(t, w)
	assertTotals(10, 100)
	// Empty slots from mixed batches must be stored as NULL, independently.
	for i := 0; i < 2; i++ {
		id := commitIdentityEvent(t, st, user, a, fmt.Sprint("absent-", i), now, [32]byte{}, false, fmt.Sprint("absent-fact-", i), "1")
		var absent bool
		if err := st.DB().QueryRow(`SELECT usage_identity_hash IS NULL AND usage_identity_source IS NULL FROM telemetry_events WHERE id=?`, id).Scan(&absent); err != nil || !absent {
			t.Fatalf("missing identity stored as value: %v", err)
		}
		// Simulate an old persisted zero identity; worker must ignore it too.
		if _, err := st.DB().Exec(`UPDATE telemetry_events SET usage_identity_hash=UNHEX(REPEAT('00',32)),usage_identity_source=0 WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	drainIdentityTasks(t, w)
	assertTotals(30, 100)
	// Reproduce legacy cross-type suppression without applying any delta.
	legacyHash := crypto.SHA256([]byte("legacy-request"))
	commitIdentityEvent(t, st, user, a, "legacy-cost", now, legacyHash, true, "legacy-cost-fact", "1")
	legacy := commitIdentityEvent(t, st, user, a, "legacy-usage", now, legacyHash, false, "legacy-usage-fact", "1")
	zero := commitIdentityEvent(t, st, user, a, "legacy-zero", now, [32]byte{}, false, "legacy-zero-fact", "1")
	for _, id := range []uint64{legacy, zero} {
		if _, err := st.DB().Exec(`DELETE FROM telemetry_tasks WHERE event_row_id=?`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := st.DB().Exec(`UPDATE telemetry_events SET status_json=JSON_OBJECT('hour',4,'day',4,'month',4) WHERE id=?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().Exec(`UPDATE telemetry_events SET usage_identity_hash=UNHEX(REPEAT('00',32)),usage_identity_source=0 WHERE id=?`, zero); err != nil {
		t.Fatal(err)
	}
	drainIdentityTasks(t, w)
	assertTotals(30, 200)
	// Dry-run makes no task/status/identity changes. The prior revision and
	// true mirrors must remain inapplicable during apply and repeat runs.
	r, err := w.RepairUsageIdentity(ctx, 0, 1000, true, now)
	if err != nil || r.Requeued != 6 {
		t.Fatalf("dry run %+v: %v", r, err)
	}
	var tasks, zeros int
	st.DB().QueryRow(`SELECT COUNT(*) FROM telemetry_tasks`).Scan(&tasks)
	st.DB().QueryRow(`SELECT COUNT(*) FROM telemetry_events WHERE usage_identity_hash=UNHEX(REPEAT('00',32))`).Scan(&zeros)
	if tasks != 0 || zeros != 3 {
		t.Fatalf("dry-run changed data tasks=%d zeros=%d", tasks, zeros)
	}
	r, err = w.RepairUsageIdentity(ctx, 0, 1000, false, now)
	if err != nil || r.Requeued != 6 || r.Normalized != 3 {
		t.Fatalf("repair %+v: %v", r, err)
	}
	drainIdentityTasks(t, w)
	assertTotals(50, 200)
	r, err = w.RepairUsageIdentity(ctx, 0, 1000, false, now)
	if err != nil || r.Requeued != 0 || r.Normalized != 0 {
		t.Fatalf("repeat %+v: %v", r, err)
	}
	drainIdentityTasks(t, w)
	assertTotals(50, 200)
	var status int
	if err := st.DB().QueryRow(`SELECT JSON_EXTRACT(status_json,'$.day') FROM telemetry_events WHERE id=?`, usage).Scan(&status); err != nil || status != 4 {
		t.Fatalf("old revision revived: %d %v", status, err)
	}
}

func TestUsageIdentityMixedBatchMySQL(t *testing.T) {
	st, w, cleanup := setupAggDB(t)
	defer cleanup()
	now := w.clk.Now()
	user, install := "usr_mixed_fix", "ins_mixed_fix"
	seedAggUserInstall(t, st, user, install, now)
	events := []v2.EventEnvelope{
		makeAggEvent(t, "mixed-a", now.UnixMilli(), nil),
		makeAggEvent(t, "mixed-b", now.UnixMilli(), nil),
		makeAggEvent(t, "mixed-c", now.UnixMilli(), nil),
	}
	result, err := st.Ingest().CommitTelemetryEventsV2(context.Background(), domain.TelemetryEventsV2Input{
		UserID: user, InstallationID: install, BindingStatusVersion: 1, NonceHash: crypto.SHA256([]byte("mixed-nonce")), NonceExpiresAt: now.Add(time.Hour), RequestID: "mixed", ReceivedAt: now, Events: events,
		UsageIdentities: []domain.TelemetryUsageIdentity{{}, {Hash: crypto.SHA256([]byte("mixed-identity"))}, {}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ack := range result.Acks {
		if ack.Result != v2.AckResultAccepted {
			t.Fatalf("batch ack %+v", ack)
		}
	}
	var nulls, valid int
	if err := st.DB().QueryRow(`SELECT SUM(usage_identity_hash IS NULL),SUM(usage_identity_hash IS NOT NULL) FROM telemetry_events`).Scan(&nulls, &valid); err != nil {
		t.Fatal(err)
	}
	if nulls != 2 || valid != 1 {
		t.Fatalf("mixed identities: null=%d valid=%d", nulls, valid)
	}
	drainIdentityTasks(t, w)
	var tokens int
	if err := st.DB().QueryRow(`SELECT SUM(exact_token_total) FROM telemetry_model_metrics WHERE grain='day'`).Scan(&tokens); err != nil {
		t.Fatal(err)
	}
	if tokens != 30 {
		t.Fatalf("mixed batch tokens %d, want 30", tokens)
	}
}
