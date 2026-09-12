package mysql

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

func b64url32(seed string) v2.Base64Url32 {
	sum := crypto.SHA256([]byte(seed))
	return v2.Base64Url32(base64.RawURLEncoding.EncodeToString(sum[:]))
}

func makeV2Event(t *testing.T, seed string, occurredAtMs int64, mutate func(map[string]any)) v2.EventEnvelope {
	t.Helper()
	eventMap := map[string]any{
		"eventId":                string(b64url32("event:" + seed)),
		"factKey":                string(b64url32("fact:" + seed)),
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
		t.Fatalf("compute hash: %v", err)
	}
	eventMap["contentHash"] = hash
	raw, err := json.Marshal(eventMap)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	var envelope v2.EventEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	return envelope
}

func commitV2(t *testing.T, st *Store, userID, installationID string, statusVersion uint64, nonce string, now time.Time, events ...v2.EventEnvelope) *domain.TelemetryEventsV2Result {
	t.Helper()
	result, err := st.Ingest().CommitTelemetryEventsV2(context.Background(), domain.TelemetryEventsV2Input{
		InstallationID:       installationID,
		UserID:               userID,
		BindingStatusVersion: statusVersion,
		NonceHash:            crypto.SHA256([]byte(nonce)),
		NonceExpiresAt:       now.Add(time.Minute),
		RequestID:            "req_" + nonce,
		Events:               events,
		ReceivedAt:           now,
	})
	if err != nil {
		t.Fatalf("commit v2: %v", err)
	}
	return result
}

func TestMySQL_TelemetryV2ReplayDuplicateAndConflict(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	userID := "usr_v2_dup"
	installationID := "ins_v2_dup"
	seedIngestInstallation(t, st, userID, installationID, crypto.SHA256([]byte("pk:v2-dup")), now)

	event := makeV2Event(t, "dup-base", now.UnixMilli(), nil)
	first := commitV2(t, st, userID, installationID, 1, "nonce-v2-dup-1", now, event)
	if first.Acks[0].Result != v2.AckResultAccepted {
		t.Fatalf("expected accepted, got %+v", first.Acks[0])
	}

	second := commitV2(t, st, userID, installationID, 1, "nonce-v2-dup-2", now, event)
	if second.Acks[0].Result != v2.AckResultDuplicate {
		t.Fatalf("expected duplicate, got %+v", second.Acks[0])
	}

	conflict := makeV2Event(t, "dup-base", now.UnixMilli(), func(m map[string]any) {
		usage := m["payload"].(map[string]any)["usage"].(map[string]any)
		usage["token_total"] = "99"
	})
	third := commitV2(t, st, userID, installationID, 1, "nonce-v2-dup-3", now, conflict)
	if third.Acks[0].Result != v2.AckResultConflict || third.Acks[0].Code == nil || *third.Acks[0].Code != v2.AckErrorCodeHashMismatch {
		t.Fatalf("expected conflict hash_mismatch, got %+v", third.Acks[0])
	}

	var taskCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telemetry_tasks`).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if taskCount != 3 {
		t.Fatalf("expected 3 tasks after first accept, got %d", taskCount)
	}
}

func TestMySQL_TelemetryV2OutsideWindowAndFuture(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	userID := "usr_v2_window"
	installationID := "ins_v2_window"
	seedIngestInstallation(t, st, userID, installationID, crypto.SHA256([]byte("pk:v2-window")), now)

	tooOld := domain.StartOfDay(now).AddDate(0, 0, -15).UnixMilli()
	oldEvent := makeV2Event(t, "too-old", tooOld, nil)
	oldResult := commitV2(t, st, userID, installationID, 1, "nonce-v2-old", now, oldEvent)
	if oldResult.Acks[0].Result != v2.AckResultDiscarded || oldResult.Acks[0].Code == nil || *oldResult.Acks[0].Code != v2.AckErrorCodeOutsideWindow {
		t.Fatalf("expected discarded outside_window, got %+v", oldResult.Acks[0])
	}

	future := now.Add(10 * time.Minute).UnixMilli()
	futureEvent := makeV2Event(t, "future", future, nil)
	futureResult := commitV2(t, st, userID, installationID, 1, "nonce-v2-future", now, futureEvent)
	if futureResult.Acks[0].Result != v2.AckResultRetry || futureResult.Acks[0].Code == nil || *futureResult.Acks[0].Code != v2.AckErrorCodeFutureEventTime {
		t.Fatalf("expected retry future_event_time, got %+v", futureResult.Acks[0])
	}
}

func TestMySQL_TelemetryV2ConcurrentSameEventID(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	userID := "usr_v2_race"
	installationID := "ins_v2_race"
	seedIngestInstallation(t, st, userID, installationID, crypto.SHA256([]byte("pk:v2-race")), now)
	event := makeV2Event(t, "race-event", now.UnixMilli(), nil)

	var wg sync.WaitGroup
	results := make([]*domain.TelemetryEventsV2Result, 8)
	errs := make([]error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = st.Ingest().CommitTelemetryEventsV2(context.Background(), domain.TelemetryEventsV2Input{
				InstallationID:       installationID,
				UserID:               userID,
				BindingStatusVersion: 1,
				NonceHash:            crypto.SHA256([]byte(fmt.Sprintf("nonce-race-%d", i))),
				NonceExpiresAt:       now.Add(time.Minute),
				RequestID:            fmt.Sprintf("req-race-%d", i),
				Events:               []v2.EventEnvelope{event},
				ReceivedAt:           now,
			})
		}(i)
	}
	wg.Wait()

	accepted, duplicated := 0, 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", i, err)
		}
		switch results[i].Acks[0].Result {
		case v2.AckResultAccepted:
			accepted++
		case v2.AckResultDuplicate:
			duplicated++
		default:
			t.Fatalf("unexpected ack %+v", results[i].Acks[0])
		}
	}
	if accepted != 1 || duplicated != 7 {
		t.Fatalf("expected 1 accepted / 7 duplicate, got %d / %d", accepted, duplicated)
	}
	var eventCount, taskCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM telemetry_events`).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM telemetry_tasks`).Scan(&taskCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || taskCount != 3 {
		t.Fatalf("expected 1 event and 3 tasks, got %d / %d", eventCount, taskCount)
	}
}

func TestMySQL_TelemetryV2RebindFencing(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()

	now := time.Now().UTC().Truncate(time.Millisecond)
	userA := "usr_v2_rebind_a"
	userB := "usr_v2_rebind_b"
	installationID := "ins_v2_rebind"
	pub := crypto.SHA256([]byte("pk:v2-rebind"))
	seedIngestInstallation(t, st, userA, installationID, pub, now)

	authSubjectHash := crypto.SHA256([]byte("subject:" + userB))
	if _, err := db.Exec(`
		INSERT INTO users (
			user_id, auth_subject_hash, display_name, account_status,
			leaderboard_visibility, timezone_name, created_at, updated_at
		) VALUES (?, ?, ?, 'active', 'private', 'UTC', ?, ?)`,
		userB, authSubjectHash[:], "User B", now, now,
	); err != nil {
		t.Fatalf("seed user B: %v", err)
	}

	eventA := makeV2Event(t, "owned-by-a", now.UnixMilli(), nil)
	first := commitV2(t, st, userA, installationID, 1, "nonce-rebind-a", now, eventA)
	if first.Acks[0].Result != v2.AckResultAccepted {
		t.Fatalf("expected accepted for A, got %+v", first.Acks[0])
	}

	if _, err := st.Device().RebindInstallationTx(context.Background(), installationID, userB, now); err != domain.ErrPublicKeyConflict {
		t.Fatalf("bound device must reject another user: %v", err)
	}
	if _, err := st.Device().RevokeInstallation(context.Background(), installationID, userA, now); err != nil {
		t.Fatal(err)
	}
	rebound, err := st.Device().RebindInstallationTx(context.Background(), installationID, userB, now.Add(time.Second))
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if rebound.UserID != userB || rebound.StatusVersion != 3 {
		t.Fatalf("unexpected rebound installation: %+v", rebound)
	}

	var storedUser string
	if err := db.QueryRow(`SELECT installation_id FROM telemetry_events WHERE installation_id = ?`, installationID).Scan(&storedUser); err != nil {
		t.Fatal(err)
	}
	if storedUser != installationID {
		t.Fatalf("historical event device changed: got %s want %s", storedUser, installationID)
	}

	eventB := makeV2Event(t, "owned-by-b", now.UnixMilli(), nil)
	_, err = st.Ingest().CommitTelemetryEventsV2(context.Background(), domain.TelemetryEventsV2Input{
		InstallationID:       installationID,
		UserID:               userB,
		BindingStatusVersion: 1,
		NonceHash:            crypto.SHA256([]byte("nonce-rebind-old")),
		NonceExpiresAt:       now.Add(time.Minute),
		RequestID:            "req-old-version",
		Events:               []v2.EventEnvelope{eventB},
		ReceivedAt:           now,
	})
	if err != domain.ErrBindingVersionMismatch {
		t.Fatalf("expected binding version mismatch, got %v", err)
	}

	ok := commitV2(t, st, userB, installationID, 3, "nonce-rebind-new", now, eventB)
	if ok.Acks[0].Result != v2.AckResultAccepted {
		t.Fatalf("expected accepted for B with new version, got %+v", ok.Acks[0])
	}
	var ownerB string
	if err := db.QueryRow(`
		SELECT installation_id FROM telemetry_events
		WHERE installation_id = ? AND event_id = ?`,
		installationID, mustDecodeB64(t, string(eventB.EventID)),
	).Scan(&ownerB); err != nil {
		t.Fatal(err)
	}
	if ownerB != installationID {
		t.Fatalf("new event owner = %s, want %s", ownerB, installationID)
	}
}

func mustDecodeB64(t *testing.T, value string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReconstructionRejectsInvalidTimestampWithoutOverflow(t *testing.T) {
	now := time.Now().UnixMilli()
	for _, timestamp := range []string{"0", "9223372036854775808", "18446744073709551615"} {
		event := v2.EventEnvelope{SchemaVersion: v2.SchemaVersion, MetricSemanticsVersion: v2.MetricSemanticsVersion, OccurredAt: v2.UInt64String(timestamp)}
		ack, err := processTelemetryEventV2(context.Background(), nil, domain.TelemetryEventsV2Input{Reconstruction: true}, event, [32]byte{}, now, now-14*86400000, now+300000)
		if err != nil || ack.Result != v2.AckResultInvalid {
			t.Fatalf("invalid timestamp %s: %+v %v", timestamp, ack, err)
		}
	}
}
