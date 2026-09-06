package mysql

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
	"tokendance/internal/config"
	"tokendance/internal/device"
	"tokendance/internal/domain"
	"tokendance/internal/httpapi"
)

func TestMySQL_SignedAggregateHTTP(t *testing.T) {
	st, _, cleanup := getTestStore(t)
	defer cleanup()
	now := time.Now().UTC()
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	var public [32]byte
	copy(public[:], key.Public().(ed25519.PublicKey))
	seedIngestInstallation(t, st, "usr_aggregate_http", "ins_aggregate_http", public, now)
	h := httpapi.NewHandlers(nil, nil, nil, nil, device.NewService(st, config.DefaultConfig(), nil), nil, nil, nil, nil)
	snapshot := domain.AggregateSnapshot{SchemaVersion: 1, Day: now.Format("2006-01-02"), Revision: 1, Rows: []domain.AggregateRow{{Kind: "agent", AgentID: "codex", Metrics: map[string]string{"exact_token_total": "100"}}}}
	body, _ := json.Marshal(snapshot)
	send := func(nonce string, body []byte, tamper bool) *httptest.ResponseRecorder {
		hash := sha256.Sum256(body)
		digest := hex.EncodeToString(hash[:])
		stamp := now.Format(time.RFC3339Nano)
		canonical := fmt.Sprintf("POST\n/v1/telemetry/aggregates\n%s\n%s\n%s", stamp, nonce, digest)
		sig := ed25519.Sign(key, []byte(canonical))
		if tamper {
			sig[0] ^= 1
		}
		r := httptest.NewRequest("POST", "/v1/telemetry/aggregates", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Device ins_aggregate_http:"+base64.RawURLEncoding.EncodeToString(sig))
		r.Header.Set("X-Timestamp", stamp)
		r.Header.Set("X-Nonce", nonce)
		r.Header.Set("X-Body-SHA256", digest)
		rec := httptest.NewRecorder()
		h.IngestAggregate(rec, r)
		return rec
	}
	if rec := send("nonce-valid-aggregate-1", body, false); rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body.String())
	}
	if rec := send("nonce-valid-aggregate-1", body, false); rec.Code != 409 {
		t.Fatal("replay", rec.Code, rec.Body.String())
	}
	if rec := send("nonce-valid-aggregate-2", body, true); rec.Code != 401 {
		t.Fatal("signature", rec.Code, rec.Body.String())
	}
	if rec := send("nonce-valid-aggregate-3", []byte(`{"schemaVersion":1,"day":"invalid","revision":1,"rows":[]}`), false); rec.Code != 400 {
		t.Fatal("invalid body", rec.Code, rec.Body.String())
	}
	if rec := send("nonce-valid-aggregate-4", body, false); rec.Code != 200 {
		t.Fatal("idempotent retry", rec.Code, rec.Body.String())
	}
}
