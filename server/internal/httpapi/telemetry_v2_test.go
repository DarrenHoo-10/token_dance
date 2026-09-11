package httpapi

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"tokendance/internal/device"
	"tokendance/internal/domain"
	"tokendance/internal/profile"
	v2 "tokendance/internal/protocol/v2"
)

func TestTelemetryCapabilitiesShape(t *testing.T) {
	_, _, _, _, router := setupSecurityTestApp(t, false)
	req := httptest.NewRequest(http.MethodGet, "/v2/telemetry/capabilities", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var caps v2.TelemetryCapabilities
	if err := json.Unmarshal(rec.Body.Bytes(), &caps); err != nil {
		t.Fatal(err)
	}
	if caps.ProtocolVersion != 2 || caps.MaxBatchEvents != 500 || caps.MaxBatchBytes != 1048576 {
		t.Fatalf("unexpected capabilities: %+v", caps)
	}
	if len(caps.SupportedSchemaVersions) != 1 || caps.SupportedSchemaVersions[0] != 2 {
		t.Fatalf("schema versions: %+v", caps.SupportedSchemaVersions)
	}
	if caps.ServerTimeMs == "" || caps.EventReceiveLowerBoundMs == "" {
		t.Fatalf("missing time bounds: %+v", caps)
	}
}

func TestTelemetryV2SignedIngestAccepted(t *testing.T) {
	ctx := context.Background()
	authSvc, deviceSvc, profileSvc, st, router := setupSecurityTestApp(t, false)

	_ = authSvc.RequestRegistrationCode(ctx, "v2_ingest@tokendance.dev", "en-US")
	ch, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("v2_ingest@tokendance.dev"))
	code := getChallengeCode(authSvc, ch.CodeHash)
	res, err := authSvc.CompleteRegistration(ctx, "v2_ingest@tokendance.dev", code, "Password123!", "", "en-US", "UTC")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, _, err := profileSvc.CompleteOnboarding(ctx, res.User.UserID, profile.OnboardingInput{
		Handle:      "v2ingest",
		DisplayName: "V2 Ingest",
		Timezone:    "UTC",
		Locale:      "en-US",
	}); err != nil {
		t.Fatalf("onboard: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	inst, err := deviceSvc.RegisterInstallation(ctx, res.User.UserID, device.ClaimInput{
		PublicKey:        hex.EncodeToString(pub),
		OSType:           "windows",
		Architecture:     "x86_64",
		CollectorVersion: "2.0.0",
	})
	if err != nil {
		t.Fatalf("register installation: %v", err)
	}

	now := time.Now().UTC()
	eventID := sha256.Sum256([]byte("http-v2-event"))
	factKey := sha256.Sum256([]byte("http-v2-fact"))
	eventMap := map[string]any{
		"eventId":                base64.RawURLEncoding.EncodeToString(eventID[:]),
		"factKey":                base64.RawURLEncoding.EncodeToString(factKey[:]),
		"factRevision":           "1",
		"schemaVersion":          float64(2),
		"metricSemanticsVersion": float64(1),
		"harnessId":              "codex",
		"eventType":              "model_usage_recorded",
		"occurredAt":             strconv.FormatInt(now.UnixMilli(), 10),
		"payload": map[string]any{
			"usage": map[string]any{"token_total": "7"},
			"meta":  map[string]any{"accuracy": "exact", "time_source": "source_record"},
		},
	}
	hash, err := v2.ComputeContentHash(eventMap)
	if err != nil {
		t.Fatal(err)
	}
	eventMap["contentHash"] = hash
	bodyObj := map[string]any{
		"protocolVersion": 2,
		"requestId":       "req_http_v2",
		"events":          []any{eventMap},
	}
	body, _ := json.Marshal(bodyObj)
	timestamp := now.Format(time.RFC3339Nano)
	nonce := "nonce-http-v2-signed-01"
	bodyHash := sha256.Sum256(body)
	bodyHashHex := hex.EncodeToString(bodyHash[:])
	binding := strconv.FormatUint(inst.StatusVersion, 10)
	canonical := telemetryCanonicalRequestV2(http.MethodPost, "/v2/telemetry/events", timestamp, nonce, bodyHashHex, inst.InstallationID, binding)
	sig := base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(canonical)))

	req := httptest.NewRequest(http.MethodPost, "/v2/telemetry/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+res.SessionToken)
	req.Header.Set("X-Device-Authorization", "Device "+inst.InstallationID+":"+sig)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Body-SHA256", bodyHashHex)
	req.Header.Set("X-Binding-Status-Version", binding)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp v2.TelemetryEventsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Acks) != 1 || resp.Acks[0].Result != v2.AckResultAccepted {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestTelemetryV1PathsRequireUpgrade(t *testing.T) {
	_, _, _, _, router := setupSecurityTestApp(t, false)
	paths := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/telemetry/batches"},
		{http.MethodPost, "/v1/telemetry/ingest"},
		{http.MethodPost, "/v1/telemetry/aggregates"},
		{http.MethodGet, "/v1/telemetry/cursor"},
	}
	for _, p := range paths {
		req := httptest.NewRequest(p.method, p.path, bytes.NewReader([]byte(`{}`)))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusUpgradeRequired {
			t.Fatalf("%s %s => %d %s", p.method, p.path, rec.Code, rec.Body.String())
		}
		var envelope ErrorWrapper
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Error.Code != "CLIENT_UPGRADE_REQUIRED" {
			t.Fatalf("expected CLIENT_UPGRADE_REQUIRED, got %+v", envelope.Error)
		}
	}
}
