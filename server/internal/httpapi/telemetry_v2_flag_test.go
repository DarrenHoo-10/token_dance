package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tokendance/internal/config"
)

func TestTelemetryV2PausedByFeatureFlag(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.EventPipelineV2Ingest = false
	router, _, _ := setupTestRouterWithConfig(t, cfg)

	req := httptest.NewRequest(http.MethodGet, "/v2/telemetry/capabilities", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when ingest paused, got %d %s", rec.Code, rec.Body.String())
	}
	var envelope ErrorWrapper
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != "EVENT_PIPELINE_V2_PAUSED" {
		t.Fatalf("expected EVENT_PIPELINE_V2_PAUSED, got %+v", envelope.Error)
	}

	// Legacy paths must stay closed (never reopen on v2 pause).
	req = httptest.NewRequest(http.MethodPost, "/v1/telemetry/aggregates", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("expected 426 on legacy path while paused, got %d", rec.Code)
	}
}
