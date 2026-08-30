package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/device"
	"tokendance/internal/export"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/provider"
	"tokendance/internal/search"
	"tokendance/internal/store/memory"
)

var testStorage = provider.NewMemoryObjectStorage("")

func TestNormalizeTelemetryEventBoundsPersistedStringFields(t *testing.T) {
	validEvent := func() TelemetryEventInput {
		return TelemetryEventInput{
			EventID:              strings.Repeat("a", 64),
			SchemaVersion:        1,
			AdapterID:            "dev.tokendance.adapter.test",
			AdapterVersion:       "1.0.0",
			AgentID:              "test-agent",
			EventType:            "tool_invoked",
			Accuracy:             "exact",
			SourceKind:           "runtime_stream",
			OccurredAt:           "2026-08-30T12:00:00Z",
			PrivacyPolicyVersion: 1,
		}
	}

	t.Run("valid identifiers and enum", func(t *testing.T) {
		event := validEvent()
		toolCategory := "filesystem.read"
		invokeType := "explicit"
		event.ToolCategory = &toolCategory
		event.SkillInvokeType = &invokeType
		if _, code := normalizeTelemetryEvent(&event); code != "" {
			t.Fatalf("expected valid telemetry strings, got %q", code)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*TelemetryEventInput)
	}{
		{name: "prompt-like tool category", mutate: func(event *TelemetryEventInput) {
			value := "What is the production database password?"
			event.ToolCategory = &value
		}},
		{name: "code fragment invoke type", mutate: func(event *TelemetryEventInput) {
			value := `fmt.Println("secret")`
			event.SkillInvokeType = &value
		}},
		{name: "free text skill name", mutate: func(event *TelemetryEventInput) {
			value := "Read all production credentials"
			event.SkillPublicName = &value
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := validEvent()
			test.mutate(&event)
			if _, code := normalizeTelemetryEvent(&event); code == "" {
				t.Fatal("expected arbitrary persisted string to be rejected")
			}
		})
	}
}

func TestAvatarUploadIntentWebContractDecodesByteSize(t *testing.T) {
	body := []byte(`{"contentType":"image/png","byteSize":4096,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar-upload-intents", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	var input media.CreateAvatarIntentInput

	if err := decodeJSON(rec, req, 1024, &input); err != nil {
		t.Fatalf("decode web avatar upload contract: %v", err)
	}
	if input.ByteSize != 4096 {
		t.Fatalf("expected byteSize 4096 through Go decoder, got %d", input.ByteSize)
	}
}

func setupTestRouter(t *testing.T) (http.Handler, *auth.Service, *memory.MemoryStore) {
	return setupTestRouterWithConfig(t, config.DefaultConfig())
}

func setupTestRouterWithConfig(t *testing.T, cfg *config.Config) (http.Handler, *auth.Service, *memory.MemoryStore) {
	return setupTestRouterWithArgonMode(t, cfg, true)
}

func setupTestRouterWithProductionArgon(t *testing.T, cfg *config.Config) (http.Handler, *auth.Service, *memory.MemoryStore) {
	return setupTestRouterWithArgonMode(t, cfg, false)
}

func setupTestRouterWithArgonMode(t *testing.T, cfg *config.Config, fast bool) (http.Handler, *auth.Service, *memory.MemoryStore) {
	st := memory.NewMemoryStore()
	if fast {
		cfg.Argon2MemoryKiB = 1024
		cfg.Argon2Time = 1
		cfg.Argon2Parallelism = 1
	}

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	clk := clock.NewMockClock(now)

	authService := auth.NewService(st, cfg, clk)
	profileService := profile.NewService(st, clk)
	privacyService := privacy.NewService(st, clk)
	analyticsService := analytics.NewService(st, clk)
	deviceService := device.NewService(st, cfg, clk)
	exportService := export.NewService(st, clk, testStorage)
	mediaService := media.NewService(st, cfg, clk, testStorage)
	searchService := search.NewService(st, clk)
	leaderboardService := leaderboard.NewService(st)

	router := NewRouter(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
	)

	return router, authService, st
}

func TestHealthEndpoints(t *testing.T) {
	router, _, _ := setupTestRouter(t)

	// GET /healthz
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /healthz, got %d", rec.Code)
	}

	// GET /readyz
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /readyz, got %d", rec.Code)
	}
}

func TestReadinessEndpoint_SchemaCompatibility(t *testing.T) {
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	clk := clock.NewMockClock(time.Now().UTC())

	authService := auth.NewService(st, cfg, clk)
	profileService := profile.NewService(st, clk)
	privacyService := privacy.NewService(st, clk)
	analyticsService := analytics.NewService(st, clk)
	deviceService := device.NewService(st, cfg, clk)
	exportService := export.NewService(st, clk, testStorage)
	mediaService := media.NewService(st, cfg, clk, testStorage)
	searchService := search.NewService(st, clk)
	leaderboardService := leaderboard.NewService(st)

	// 1. Router with failing readiness checker
	failingChecker := func(ctx context.Context) error {
		return fmt.Errorf("schema missing migration 0003")
	}

	failingRouter := NewRouterWithReadiness(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
		failingChecker,
	)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	failingRouter.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 from /readyz when schema is incompatible, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not_ready") || !strings.Contains(rec.Body.String(), "DEPENDENCY_NOT_READY") || strings.Contains(rec.Body.String(), "0003") {
		t.Fatalf("expected safe not_ready response body, got %s", rec.Body.String())
	}

	// 2. Router with passing readiness checker
	passingChecker := func(ctx context.Context) error {
		return nil
	}

	passingRouter := NewRouterWithReadiness(
		authService,
		profileService,
		privacyService,
		analyticsService,
		deviceService,
		exportService,
		mediaService,
		searchService,
		leaderboardService,
		passingChecker,
	)

	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec = httptest.NewRecorder()
	passingRouter.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /readyz when schema is compatible, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ready") {
		t.Fatalf("expected ready response body, got %s", rec.Body.String())
	}
}

func TestAuthAndProtectedRoutes(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	// 1. Request Register Code
	body, _ := json.Marshal(map[string]string{
		"email":  "pilot@tokendance.dev",
		"locale": "en-US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 from register code, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// Find the generated verification code
	emailHash := authSvc.ComputeEmailLookupHash("pilot@tokendance.dev")
	ch, err := st.FindPendingEmailChallenge(nil, "register", emailHash)
	if err != nil {
		t.Fatalf("failed to find challenge: %v", err)
	}

	var validCode string
	for i := 0; i <= 999999; i++ {
		cStr := ""
		val := i
		for k := 0; k < 6; k++ {
			cStr = string(rune('0'+(val%10))) + cStr
			val /= 10
		}
		if authSvc.ComputeTokenHash(cStr) == ch.CodeHash {
			validCode = cStr
			break
		}
	}

	// 2. Complete Registration
	regBody, _ := json.Marshal(map[string]string{
		"email":    "pilot@tokendance.dev",
		"code":     validCode,
		"password": "PilotPassword123!",
		"returnTo": "/me",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from register, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var regResp struct {
		User struct {
			UserID string `json:"userId"`
		} `json:"user"`
		CSRFToken string `json:"csrfToken"`
		ReturnTo  string `json:"returnTo"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)

	// Extract Cookie from response
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == DevSessionCookie || c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected session cookie to be set")
	}

	// 3. GET /api/v1/auth/session
	req = httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/session, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	var sessResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &sessResp)
	csrfToken := sessResp.CSRFToken
	if csrfToken == "" {
		csrfToken = regResp.CSRFToken
	}

	// 4. POST /api/v1/me/onboarding without CSRF -> fails 403
	onboardBody, _ := json.Marshal(map[string]interface{}{
		"displayName": "Token Pilot",
		"handle":      "tokenpilot",
		"timezone":    "UTC",
		"locale":      "en-US",
		"privacy": map[string]interface{}{
			"publicProfileEnabled": true,
			"showTokenTotal":       true,
		},
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", bytes.NewReader(onboardBody))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 CSRF failure without X-CSRF-Token header, got %d", rec.Code)
	}

	// 5. POST /api/v1/me/onboarding with CSRF -> 200 OK
	req = httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", bytes.NewReader(onboardBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from onboarding, got %d (body: %s)", rec.Code, rec.Body.String())
	}

	// 6. GET /api/v1/me/summary
	req = httptest.NewRequest(http.MethodGet, "/api/v1/me/summary?range=30d", nil)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /me/summary, got %d", rec.Code)
	}

	// 7. GET /api/v1/public/users/tokenpilot
	req = httptest.NewRequest(http.MethodGet, "/api/v1/public/users/tokenpilot", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from public profile, got %d", rec.Code)
	}

	// 8. GET /api/v1/public/search?q=pilot
	req = httptest.NewRequest(http.MethodGet, "/api/v1/public/search?q=pilot", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from public search, got %d", rec.Code)
	}

	// 9. GET /api/v1/public/leaderboards
	req = httptest.NewRequest(http.MethodGet, "/api/v1/public/leaderboards", nil)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from leaderboards, got %d", rec.Code)
	}

	// 10. POST /api/v1/me/device-bindings -> creates binding code
	req = httptest.NewRequest(http.MethodPost, "/api/v1/me/device-bindings", nil)
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(sessionCookie)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 from device binding creation, got %d", rec.Code)
	}

	var bindResp struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &bindResp)

	// 11. POST /v1/installations/claim (Collector endpoint)
	pubKey := hex.EncodeToString([]byte("32-bytes-ed25519-public-key-here"))
	claimBody, _ := json.Marshal(map[string]interface{}{
		"code":             bindResp.Code,
		"publicKey":        pubKey,
		"deviceName":       "Dev Machine",
		"osType":           "windows",
		"architecture":     "amd64",
		"collectorVersion": "1.0.0",
	})
	req = httptest.NewRequest(http.MethodPost, "/v1/installations/claim", bytes.NewReader(claimBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from installation claim, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}
