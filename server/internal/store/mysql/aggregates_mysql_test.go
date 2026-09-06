package mysql

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"testing"
	"time"
	"tokendance/internal/domain"
)

func TestMySQL_AggregateVersionReplayAndCoverage(t *testing.T) {
	st, db, cleanup := getTestStore(t)
	defer cleanup()
	now := time.Now().UTC()
	ctx := context.Background()
	seedIngestInstallation(t, st, "usr_agg_test", "ins_agg_test", sha256.Sum256([]byte("key")), now)
	store := st.Ingest().(*ingestStore)
	total := uint64(100)
	legacy := validIngestEvent("legacy", now)
	legacy.TokenTotal = &total
	_, err := store.CommitIngest(ctx, domain.IngestBatch{BatchID: "bat_agg_legacy", InstallationID: "ins_agg_test", RequestSHA256: sha256.Sum256([]byte("legacy")), NonceHash: sha256.Sum256([]byte("legacy_nonce")), NonceExpiresAt: now.Add(time.Minute), EventCount: 1, Events: []domain.UsageEvent{legacy}, ReceivedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	sequence := 0
	put := func(revision int64, tokens string) (*domain.AggregateAck, error) {
		sequence++
		s := domain.AggregateSnapshot{SchemaVersion: 1, Day: now.Format("2006-01-02"), Revision: revision, Rows: []domain.AggregateRow{{Kind: "agent", AgentID: "test-agent", Metrics: map[string]string{"exact_token_total": tokens}}}}
		raw, _ := json.Marshal(s)
		return store.CommitAggregate(ctx, domain.AggregateCommit{Snapshot: s, InstallationID: "ins_agg_test", Digest: sha256.Sum256(raw), NonceHash: sha256.Sum256([]byte(fmt.Sprint(sequence))), ReceivedAt: now})
	}
	if _, err = put(1, "99"); err == nil {
		t.Fatal("partial rebuild overwrote legacy history")
	}
	if _, err = put(1, "100"); err != nil {
		t.Fatal(err)
	}
	if _, err = put(1, "100"); err != nil {
		t.Fatal("identical retry:", err)
	}
	if _, err = put(1, "101"); err == nil {
		t.Fatal("same revision accepted different content")
	}
	if _, err = put(2, "80"); err != nil {
		t.Fatal("explicit correction:", err)
	}
	if _, err = put(1, "100"); err == nil {
		t.Fatal("stale revision restored old total")
	}
	var rev int
	var payload string
	if err = db.QueryRow("SELECT revision,payload_json FROM device_daily_aggregates WHERE installation_id='ins_agg_test'").Scan(&rev, &payload); err != nil {
		t.Fatal(err)
	}
	if rev != 2 {
		t.Fatal(rev, payload)
	}
	if _, err = db.Exec("UPDATE installations SET installation_status='disabled',disabled_at=NOW(),disabled_reason='user_paused' WHERE installation_id='ins_agg_test'"); err != nil {
		t.Fatal(err)
	}
	if _, err = put(3, "90"); err == nil {
		t.Fatal("disabled device uploaded")
	}
}
