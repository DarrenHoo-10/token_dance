package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tokendance/internal/analytics"
	"tokendance/internal/auth"
	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/device"
	"tokendance/internal/domain"
	"tokendance/internal/export"
	"tokendance/internal/leaderboard"
	"tokendance/internal/media"
	"tokendance/internal/privacy"
	"tokendance/internal/profile"
	"tokendance/internal/search"
	"tokendance/internal/store/memory"
)

func setupSecurityTestApp(t *testing.T, isProd bool) (*auth.Service, *device.Service, *profile.Service, *memory.MemoryStore, http.Handler) {
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	cfg.Argon2MemoryKiB = 1024
	cfg.Argon2Time = 1
	cfg.Argon2Parallelism = 1

	if isProd {
		cfg.Environment = "production"
		cfg.HMACSecret = "prod-test-hmac-secret-at-least-32-bytes-long"
		cfg.EncryptionKey = "prod-test-enc-key-32-bytes-long-01"
		cfg.MySQLDSN = "mock-dsn"
		cfg.EmailProvider = "worker"
	}

	clk := clock.RealClock{}

	authSvc := auth.NewService(st, cfg, clk)
	profileSvc := profile.NewService(st, clk)
	privacySvc := privacy.NewService(st, clk)
	analyticsSvc := analytics.NewService(st, clk)
	deviceSvc := device.NewService(st, cfg, clk)
	exportSvc := export.NewService(st, clk, testStorage)
	mediaSvc := media.NewService(st, cfg, clk, testStorage)
	searchSvc := search.NewService(st, clk)
	leaderboardSvc := leaderboard.NewService(st)

	router := NewRouter(
		authSvc,
		profileSvc,
		privacySvc,
		analyticsSvc,
		deviceSvc,
		exportSvc,
		mediaSvc,
		searchSvc,
		leaderboardSvc,
	)

	return authSvc, deviceSvc, profileSvc, st, router
}

// TestHTTP_IDOR_SessionRevocation tests that user A cannot revoke user B's session.
func TestHTTP_IDOR_SessionRevocation(t *testing.T) {
	ctx := context.Background()
	authSvc, _, _, st, router := setupSecurityTestApp(t, false)

	// Register User A
	_ = authSvc.RequestRegistrationCode(ctx, "usera@tokendance.dev", "en-US")
	chA, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("usera@tokendance.dev"))
	codeA := getChallengeCode(authSvc, chA.CodeHash)
	resA, _ := authSvc.CompleteRegistration(ctx, "usera@tokendance.dev", codeA, "Password123!", "")

	// Register User B
	_ = authSvc.RequestRegistrationCode(ctx, "userb@tokendance.dev", "en-US")
	chB, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("userb@tokendance.dev"))
	codeB := getChallengeCode(authSvc, chB.CodeHash)
	resB, _ := authSvc.CompleteRegistration(ctx, "userb@tokendance.dev", codeB, "Password123!", "")

	// User A attempts to DELETE /api/v1/auth/sessions/{userB_session_id}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/sessions/"+resB.Session.SessionID, nil)
	req.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: resA.SessionToken})
	req.Header.Set("X-CSRF-Token", resA.CSRFToken)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	// Should return 404 (IDOR blocked)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when user A attempts to revoke user B session, got: %d", rec.Code)
	}

	// Verify User B's session is still active
	sessB, _, err := authSvc.ResolveSession(ctx, resB.SessionToken)
	if err != nil || sessB == nil {
		t.Fatalf("User B's session should remain active after unauthorized deletion attempt")
	}
}

// TestHTTP_CSRF_ReloadToWrite tests GET /auth/session returning a refreshed CSRF token
// and subsequent mutating write requests succeeding with the refreshed token.
func TestHTTP_CSRF_ReloadToWrite(t *testing.T) {
	ctx := context.Background()
	authSvc, _, _, st, router := setupSecurityTestApp(t, false)

	// Register user
	_ = authSvc.RequestRegistrationCode(ctx, "csrf_reload@tokendance.dev", "en-US")
	ch, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("csrf_reload@tokendance.dev"))
	code := getChallengeCode(authSvc, ch.CodeHash)
	res, _ := authSvc.CompleteRegistration(ctx, "csrf_reload@tokendance.dev", code, "Password123!", "")

	// 1. Client reloads page -> GET /api/v1/auth/session
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	reqGet.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: res.SessionToken})
	recGet := httptest.NewRecorder()
	router.ServeHTTP(recGet, reqGet)

	if recGet.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for GET /auth/session, got: %d", recGet.Code)
	}

	var sessionResp struct {
		Authenticated bool   `json:"authenticated"`
		CSRFToken     string `json:"csrfToken"`
	}
	if err := json.NewDecoder(recGet.Body).Decode(&sessionResp); err != nil {
		t.Fatalf("failed to decode GET /auth/session response: %v", err)
	}
	if sessionResp.CSRFToken == "" {
		t.Fatalf("expected GET /auth/session to return non-empty csrfToken for reload writes")
	}

	// 2. Perform write request using the refreshed CSRF token -> POST /api/v1/me/onboarding
	onboardBody := `{"handle":"csrf_pilot","displayName":"CSRF Pilot","timezone":"UTC","locale":"en-US","privacy":{"publicProfileEnabled":true}}`
	reqPost := httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", strings.NewReader(onboardBody))
	reqPost.Header.Set("Content-Type", "application/json")
	reqPost.Header.Set("X-CSRF-Token", sessionResp.CSRFToken)
	reqPost.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: res.SessionToken})
	recPost := httptest.NewRecorder()
	router.ServeHTTP(recPost, reqPost)

	if recPost.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for onboarding using refreshed CSRF token, got: %d, body: %s", recPost.Code, recPost.Body.String())
	}

	// 3. Mutating request with invalid CSRF token -> fails 403
	reqInvalid := httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", strings.NewReader(onboardBody))
	reqInvalid.Header.Set("Content-Type", "application/json")
	reqInvalid.Header.Set("X-CSRF-Token", "invalid_csrf_token_value")
	reqInvalid.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: res.SessionToken})
	recInvalid := httptest.NewRecorder()
	router.ServeHTTP(recInvalid, reqInvalid)

	if recInvalid.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for invalid CSRF token, got: %d", recInvalid.Code)
	}
}

// TestHTTP_ProductionCookieSecurity verifies that in production mode, DevSessionCookie
// is NOT emitted and only the secure __Host-tokendance_session is emitted and accepted.
func TestHTTP_ProductionCookieSecurity(t *testing.T) {
	ctx := context.Background()
	authSvc, _, _, st, router := setupSecurityTestApp(t, true) // Production mode!

	// 1. Login/Register in production
	_ = authSvc.RequestRegistrationCode(ctx, "prod_cookie@tokendance.dev", "en-US")
	ch, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("prod_cookie@tokendance.dev"))
	code := getChallengeCode(authSvc, ch.CodeHash)

	regBody := `{"email":"prod_cookie@tokendance.dev","code":"` + code + `","password":"Password123!"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", strings.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created in production register, got: %d", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var hasHostCookie bool
	var hasInsecureDevCookie bool

	for _, c := range cookies {
		if c.Name == SessionCookieName {
			hasHostCookie = true
			if !c.Secure {
				t.Fatalf("production __Host- cookie must have Secure=true")
			}
		}
		if c.Name == DevSessionCookie {
			hasInsecureDevCookie = true
		}
	}

	if !hasHostCookie {
		t.Fatalf("expected production response to set %s cookie", SessionCookieName)
	}
	if hasInsecureDevCookie {
		t.Fatalf("production response must NOT emit insecure %s cookie", DevSessionCookie)
	}

	// 2. Middleware in production must reject DevSessionCookie
	reqDevCookie := httptest.NewRequest(http.MethodGet, "/api/v1/me/profile", nil)
	reqDevCookie.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: "some-token"})
	recDevCookie := httptest.NewRecorder()
	router.ServeHTTP(recDevCookie, reqDevCookie)

	if recDevCookie.Code != http.StatusUnauthorized {
		t.Fatalf("production middleware must reject insecure DevSessionCookie, got: %d", recDevCookie.Code)
	}
}

// TestHTTP_CollectorRegister_GrantScope verifies that Collector registration requires
// a scoped grant (dgt_...) and rejects broad Web session bearer tokens.
func TestHTTP_CollectorRegister_GrantScope(t *testing.T) {
	ctx := context.Background()
	authSvc, _, profileSvc, st, router := setupSecurityTestApp(t, false)

	// Register & onboard user
	_ = authSvc.RequestRegistrationCode(ctx, "grant_test@tokendance.dev", "en-US")
	ch, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("grant_test@tokendance.dev"))
	code := getChallengeCode(authSvc, ch.CodeHash)
	res, _ := authSvc.CompleteRegistration(ctx, "grant_test@tokendance.dev", code, "Password123!", "")
	_, _, _ = profileSvc.CompleteOnboarding(ctx, res.User.UserID, profile.OnboardingInput{
		Handle:      "grantpilot",
		DisplayName: "Grant Pilot",
		Timezone:    "UTC",
		Locale:      "en-US",
	})

	claimBody := `{"publicKey":"0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20","osType":"windows","architecture":"x86_64","collectorVersion":"1.0.0"}`

	// 1. Attempt registering with Web Session Token -> MUST FAIL (401 scoped grant required)
	reqWebBearer := httptest.NewRequest(http.MethodPost, "/v1/installations/register", strings.NewReader(claimBody))
	reqWebBearer.Header.Set("Content-Type", "application/json")
	reqWebBearer.Header.Set("Authorization", "Bearer "+res.SessionToken)
	recWebBearer := httptest.NewRecorder()
	router.ServeHTTP(recWebBearer, reqWebBearer)

	if recWebBearer.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized when registering with Web session token, got: %d", recWebBearer.Code)
	}

	// 2. Mint a scoped device grant via POST /api/v1/me/device-grants
	reqGrant := httptest.NewRequest(http.MethodPost, "/api/v1/me/device-grants", nil)
	reqGrant.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: res.SessionToken})
	reqGrant.Header.Set("X-CSRF-Token", res.CSRFToken)
	recGrant := httptest.NewRecorder()
	router.ServeHTTP(recGrant, reqGrant)

	if recGrant.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for device grant, got: %d", recGrant.Code)
	}

	var grantResp struct {
		GrantToken string `json:"grantToken"`
		ExpiresAt  string `json:"expiresAt"`
	}
	if err := json.NewDecoder(recGrant.Body).Decode(&grantResp); err != nil {
		t.Fatalf("failed to decode grant response: %v", err)
	}
	if !strings.HasPrefix(grantResp.GrantToken, "dgt_") {
		t.Fatalf("expected grant token with prefix 'dgt_', got: %s", grantResp.GrantToken)
	}

	// 3. Register installation with valid scoped grant -> 200 OK
	reqRegister := httptest.NewRequest(http.MethodPost, "/v1/installations/register", strings.NewReader(claimBody))
	reqRegister.Header.Set("Content-Type", "application/json")
	reqRegister.Header.Set("Authorization", "Bearer "+grantResp.GrantToken)
	recRegister := httptest.NewRecorder()
	router.ServeHTTP(recRegister, reqRegister)

	if recRegister.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for installation register with scoped grant, got: %d, body: %s", recRegister.Code, recRegister.Body.String())
	}
}

// TestHTTP_DisallowUnknownFields verifies that API endpoints reject unknown fields
// in request JSON bodies with 400 API_INVALID_ARGUMENT.
func TestHTTP_DisallowUnknownFields(t *testing.T) {
	_, _, _, _, router := setupSecurityTestApp(t, false)

	// Sending unexpected field "maliciousField": 123 to /api/v1/auth/login
	body := `{"email":"test@tokendance.dev","password":"Password123!","unknownField":"hacked"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for unknown JSON fields, got: %d", rec.Code)
	}
}

// TestHTTP_MaxBodySize verifies that oversized request bodies are rejected.
func TestHTTP_MaxBodySize(t *testing.T) {
	_, _, _, _, router := setupSecurityTestApp(t, false)

	// Generate oversized body > 1MB
	oversized := make([]byte, 1024*1024+100)
	for i := range oversized {
		oversized[i] = 'a'
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(oversized))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 400/413 for oversized body, got: %d", rec.Code)
	}
}

// TestHTTP_TelemetryIngestRoute verifies telemetry batches endpoint routing and validation.
func TestHTTP_TelemetryIngestRoute(t *testing.T) {
	ctx := context.Background()
	authSvc, deviceSvc, profileSvc, st, router := setupSecurityTestApp(t, false)

	// Setup active user & registered device
	_ = authSvc.RequestRegistrationCode(ctx, "telemetry_test@tokendance.dev", "en-US")
	ch, _ := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("telemetry_test@tokendance.dev"))
	code := getChallengeCode(authSvc, ch.CodeHash)
	res, _ := authSvc.CompleteRegistration(ctx, "telemetry_test@tokendance.dev", code, "Password123!", "")
	_, _, _ = profileSvc.CompleteOnboarding(ctx, res.User.UserID, profile.OnboardingInput{
		Handle:      "telemetrypilot",
		DisplayName: "Telemetry Pilot",
		Timezone:    "UTC",
		Locale:      "en-US",
	})

	grant, _ := deviceSvc.CreateDeviceGrant(ctx, res.User.UserID, res.Session.SessionID)
	inst, err := deviceSvc.RegisterInstallation(ctx, res.User.UserID, device.ClaimInput{
		PublicKey:        "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
		OSType:           "windows",
		Architecture:     "x86_64",
		CollectorVersion: "1.0.0",
	})
	if err != nil {
		t.Fatalf("failed to register installation: %v", err)
	}
	_ = grant

	// 1. Post telemetry batch to /v1/telemetry/batches
	batchJSON := `{"batchId":"bat_test_01","events":[{"eventId":"evt_01","eventType":"model_turn","occurredAt":"2026-08-30T00:00:00Z","tokenTotal":100}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/telemetry/batches", strings.NewReader(batchJSON))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Device "+inst.InstallationID+":signaturesample")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for telemetry batch ingest, got: %d, body: %s", rec.Code, rec.Body.String())
	}

	var batchResp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&batchResp); err != nil {
		t.Fatalf("failed to decode telemetry batch response: %v", err)
	}
	if batchResp["batchId"] != "bat_test_01" || batchResp["accepted"] != float64(1) {
		t.Fatalf("unexpected batch response: %+v", batchResp)
	}
}

// TestHTTP_RateLimiting verifies that exceeding rate limits returns 429 API_RATE_LIMIT_EXCEEDED.
func TestHTTP_RateLimiting(t *testing.T) {
	_, _, _, _, router := setupSecurityTestApp(t, false)

	// Loop 70 times on rate-limited route with 60/min limit
	hit429 := false
	for i := 0; i < 70; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", strings.NewReader(`{"email":"ratelimit@tokendance.dev","locale":"en-US"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "198.51.100.25:12345"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code == http.StatusTooManyRequests {
			hit429 = true
			if rec.Header().Get("Retry-After") == "" {
				t.Fatalf("expected Retry-After header on 429 response")
			}
			break
		}
	}

	if !hit429 {
		t.Fatalf("expected 429 Too Many Requests after exceeding rate limit")
	}
}
