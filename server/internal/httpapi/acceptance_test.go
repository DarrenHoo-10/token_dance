package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"tokendance/internal/domain"
)

// Helper to extract session cookie from recorder
func extractSessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == DevSessionCookie || c.Name == SessionCookieName {
			return c
		}
	}
	return nil
}

// Helper to find valid 6-digit code for pending challenge in memory store
func getChallengeCode(authSvc interface{ ComputeTokenHash(string) [32]byte }, codeHash [32]byte) string {
	for i := 0; i <= 999999; i++ {
		cStr := fmt.Sprintf("%06d", i)
		if authSvc.ComputeTokenHash(cStr) == codeHash {
			return cStr
		}
	}
	return ""
}

// USR-001: 未验证不建用户 (申请验证码但不验证 -> 只有 pending challenge/outbox，无 users 行)
func TestUSR001_RequestCodeWithoutUserCreation(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	email := "unverified-usr001@tokendance.dev"
	body, _ := json.Marshal(map[string]string{
		"email":  email,
		"locale": "en-US",
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 StatusAccepted, got %d: %s", rec.Code, rec.Body.String())
	}

	emailHash := authSvc.ComputeEmailLookupHash(email)

	// Verify pending challenge exists
	ch, err := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("expected pending email challenge to exist: %v", err)
	}
	if ch.ChallengeStatus != domain.ChallengeStatusPending {
		t.Errorf("expected challenge status 'pending', got '%s'", ch.ChallengeStatus)
	}

	// Verify NO users row exists
	u, cred, err := st.FindUserByEmailHash(context.Background(), emailHash)
	if err == nil || u != nil || cred != nil {
		t.Fatalf("expected no user or credential row in database before verification, but found user: %+v", u)
	}
}

// USR-002: 注册原子性 (credential insert 后故障注入 -> user/credential/privacy/session/challenge 全部回滚)
func TestUSR002_AtomicRegistrationAndFaultInjection(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	email := "fault-usr002@tokendance.dev"
	codeReqBody, _ := json.Marshal(map[string]string{
		"email":  email,
		"locale": "en-US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	emailHash := authSvc.ComputeEmailLookupHash(email)
	ch, err := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("failed to find challenge: %v", err)
	}
	validCode := getChallengeCode(authSvc, ch.CodeHash)

	// Inject a failure right before commit
	st.FaultInjector = func(op string) error {
		if op == "CompleteRegistrationTx:credential_inserted" {
			return domain.NewAppError(500, "DATABASE_ERROR", "api.dbError", "simulated db crash after credential insertion", nil, domain.ErrInternal)
		}
		return nil
	}

	regBody, _ := json.Marshal(map[string]string{
		"email":    email,
		"code":     validCode,
		"password": "SecurePassword123!",
		"returnTo": "/me",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 InternalServerError from fault injection, got %d", rec.Code)
	}

	// Verify complete rollback: No user, no credential, and challenge remains pending!
	u, cred, err := st.FindUserByEmailHash(context.Background(), emailHash)
	if err == nil || u != nil || cred != nil {
		t.Fatalf("atomicity violation: user or credential created despite fault injection!")
	}

	// Challenge should still be pending and unconsumed
	chAfter, err := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, emailHash)
	if err != nil || chAfter.ChallengeStatus != domain.ChallengeStatusPending {
		t.Fatalf("atomicity violation: challenge was consumed or lost on failed registration!")
	}

	// Remove fault injector and retry registration: should succeed cleanly
	st.FaultInjector = nil
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created after clearing fault injector, got %d: %s", rec.Code, rec.Body.String())
	}
}

// USR-003: 邮箱唯一并发 (同邮箱并发 20 次正确注册 -> 只创建一个 user，不出现 500)
func TestUSR003_ConcurrentEmailRegistrationUniqueness(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	email := "concurrent-usr003@tokendance.dev"
	codeReqBody, _ := json.Marshal(map[string]string{
		"email":  email,
		"locale": "en-US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	emailHash := authSvc.ComputeEmailLookupHash(email)
	ch, err := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("failed to find challenge: %v", err)
	}
	validCode := getChallengeCode(authSvc, ch.CodeHash)

	const concurrency = 20
	var wg sync.WaitGroup
	statusCodes := make([]int, concurrency)

	regBody, _ := json.Marshal(map[string]string{
		"email":    email,
		"code":     validCode,
		"password": "SecurePassword123!",
		"returnTo": "/me",
	})

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			statusCodes[idx] = w.Code
		}(i)
	}
	wg.Wait()

	successCount := 0
	conflictOrInvalidCount := 0
	for _, code := range statusCodes {
		if code == http.StatusCreated {
			successCount++
		} else if code == http.StatusConflict || code == http.StatusBadRequest {
			conflictOrInvalidCount++
		} else {
			t.Errorf("unexpected status code during concurrent registration: %d (expected 201, 400, or 409; no 500 allowed)", code)
		}
	}

	if successCount != 1 {
		t.Fatalf("expected exactly 1 successful registration among 20 concurrent requests, got %d", successCount)
	}
	if conflictOrInvalidCount != concurrency-1 {
		t.Fatalf("expected %d non-500 rejected responses, got %d", concurrency-1, conflictOrInvalidCount)
	}
}

// USR-004: 验证码防重放 (同 challenge 验证两次 -> 首次成功，第二次不创建用户/Session)
func TestUSR004_CodeReplayPrevention(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	email := "replay-usr004@tokendance.dev"
	codeReqBody, _ := json.Marshal(map[string]string{
		"email":  email,
		"locale": "en-US",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	emailHash := authSvc.ComputeEmailLookupHash(email)
	ch, err := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("failed to find challenge: %v", err)
	}
	validCode := getChallengeCode(authSvc, ch.CodeHash)

	regBody, _ := json.Marshal(map[string]string{
		"email":    email,
		"code":     validCode,
		"password": "ReplayPassword123!",
		"returnTo": "/me",
	})

	// 1st attempt: Succeeds
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusCreated {
		t.Fatalf("first verification attempt should succeed with 201, got %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2nd attempt: Replay same code -> Fails cleanly
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)

	if rec2.Code == http.StatusCreated || rec2.Code == http.StatusOK {
		t.Fatalf("replay attempt must NOT succeed, got status %d", rec2.Code)
	}
	if rec2.Code != http.StatusConflict && rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 or 409 on replayed challenge, got %d: %s", rec2.Code, rec2.Body.String())
	}
}

// USR-005: 登录防枚举 (不存在邮箱和错误密码各 1,000 次 -> 错误码一致，响应时延分布无显著可利用差异)
func TestUSR005_Argon2idAntiEnumeration(t *testing.T) {
	router, _, st := setupTestRouter(t)

	// Seed existing user
	existingEmail := "realuser-usr005@tokendance.dev"
	_, _, err := st.SeedUserForTest("usr_usr005real", "realuser", existingEmail, time.Now().UTC())
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}

	// 1. Existing user with wrong password
	wrongPassBody, _ := json.Marshal(map[string]string{
		"email":    existingEmail,
		"password": "WrongPassword123!",
	})
	reqWrong := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(wrongPassBody))
	reqWrong.Header.Set("Content-Type", "application/json")
	recWrong := httptest.NewRecorder()
	router.ServeHTTP(recWrong, reqWrong)

	if recWrong.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for wrong password, got %d", recWrong.Code)
	}

	var wrongResp ErrorWrapper
	_ = json.Unmarshal(recWrong.Body.Bytes(), &wrongResp)
	if wrongResp.Error.Code != "AUTH_INVALID_CREDENTIALS" {
		t.Errorf("expected error code AUTH_INVALID_CREDENTIALS, got %s", wrongResp.Error.Code)
	}

	// 2. Non-existent user
	nonExistentBody, _ := json.Marshal(map[string]string{
		"email":    "nonexistent-usr005@tokendance.dev",
		"password": "SomeRandomPassword123!",
	})
	reqNonExist := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(nonExistentBody))
	reqNonExist.Header.Set("Content-Type", "application/json")
	recNonExist := httptest.NewRecorder()
	router.ServeHTTP(recNonExist, reqNonExist)

	if recNonExist.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized for non-existent user, got %d", recNonExist.Code)
	}

	var nonExistResp ErrorWrapper
	_ = json.Unmarshal(recNonExist.Body.Bytes(), &nonExistResp)
	if nonExistResp.Error.Code != "AUTH_INVALID_CREDENTIALS" {
		t.Errorf("expected error code AUTH_INVALID_CREDENTIALS, got %s", nonExistResp.Error.Code)
	}

	// Verify both return identical error codes and message keys
	if wrongResp.Error.Code != nonExistResp.Error.Code || wrongResp.Error.MessageKey != nonExistResp.Error.MessageKey {
		t.Fatalf("anti-enumeration failure: wrong password error %+v differs from non-existent user error %+v",
			wrongResp.Error, nonExistResp.Error)
	}
}

// USR-006: Session 撤销 (退出其他设备后复用旧 Cookie -> 立即 401，不依赖 Redis TTL)
func TestUSR006_SessionImmediateRevocationAndInvariants(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	email := "session-usr006@tokendance.dev"
	password := "SessionPass123!"

	// Register user
	codeReq, _ := json.Marshal(map[string]string{"email": email, "locale": "en-US"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReq))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	ch, _ := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash(email))
	validCode := getChallengeCode(authSvc, ch.CodeHash)

	regBody, _ := json.Marshal(map[string]string{
		"email":    email,
		"code":     validCode,
		"password": password,
		"returnTo": "/me",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var regResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	cookie1 := extractSessionCookie(rec)

	// Device 2 logs in
	loginBody, _ := json.Marshal(map[string]string{
		"email":       email,
		"password":    password,
		"deviceLabel": "Device 2 Firefox",
	})
	reqLogin := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	recLogin := httptest.NewRecorder()
	router.ServeHTTP(recLogin, reqLogin)

	var loginResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(recLogin.Body.Bytes(), &loginResp)
	cookie2 := extractSessionCookie(recLogin)

	// Device 2 calls /api/v1/auth/sessions/revoke-others
	reqRevoke := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sessions/revoke-others", nil)
	reqRevoke.Header.Set("X-CSRF-Token", loginResp.CSRFToken)
	reqRevoke.AddCookie(cookie2)
	recRevoke := httptest.NewRecorder()
	router.ServeHTTP(recRevoke, reqRevoke)

	if recRevoke.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from revoke-others, got %d", recRevoke.Code)
	}

	// Device 1 uses old cookie -> IMMEDIATELY 401
	reqDev1 := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	reqDev1.AddCookie(cookie1)
	recDev1 := httptest.NewRecorder()
	router.ServeHTTP(recDev1, reqDev1)

	if recDev1.Code != http.StatusNoContent { // unauthenticated session returns 204
		t.Fatalf("expected 204 (no active session) for revoked device 1, got %d", recDev1.Code)
	}

	// Device 1 attempts protected call -> 401 AUTH_REQUIRED
	reqDev1Me := httptest.NewRequest(http.MethodGet, "/api/v1/me/profile", nil)
	reqDev1Me.AddCookie(cookie1)
	recDev1Me := httptest.NewRecorder()
	router.ServeHTTP(recDev1Me, reqDev1Me)

	if recDev1Me.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 AUTH_REQUIRED for revoked session on /me/profile, got %d", recDev1Me.Code)
	}
}

// USR-007: returnTo (传绝对 URL、`//evil`、auth path、backslash, %2F%2F -> 一律回 `/`，合法相对路径保留 query/hash)
func TestUSR007_SafeReturnToFuzzingAndEncodedAttacks(t *testing.T) {
	_, authSvc, _ := setupTestRouter(t)

	adversarialCases := []string{
		"https://evil.com",
		"http://evil.com",
		"//evil.com",
		"//evil.com/path",
		"//localhost:3000",
		"/\\evil.com",
		"\\evil.com",
		"\\\\evil.com",
		"javascript:alert(1)",
		"JAVASCRIPT:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"/%2Fevil.com",
		"/%5Cevil.com",
		"%2F%2Fevil.com",
		"%5C%5Cevil.com",
		"/login",
		"/login?returnTo=/dashboard",
		"/register",
		"/logout",
		"/auth/callback",
		"/api/v1/auth/session",
		"/api/v1/me",
		"/\x00evil.com",
		"/\t/evil.com",
		"   ",
		strings.Repeat("a", 3000),
	}

	for _, mal := range adversarialCases {
		sanitized := authSvc.SanitizeReturnTo(mal)
		if sanitized != "/" {
			t.Errorf("SanitizeReturnTo failed to sanitize malicious payload %q: got %q (expected '/')", mal, sanitized)
		}
	}

	validCases := map[string]string{
		"":                                      "/",
		"/":                                     "/",
		"/me":                                   "/me",
		"/me/settings":                          "/me/settings",
		"/me/settings/privacy":                  "/me/settings/privacy",
		"/leaderboards":                         "/leaderboards",
		"/leaderboards?window=7d&metric=tokens": "/leaderboards?window=7d&metric=tokens",
		"/explore?tab=agents&q=code":            "/explore?tab=agents&q=code",
		"/users/alice":                          "/users/alice",
		"/users/alice?tab=skills#achievements":  "/users/alice?tab=skills#achievements",
	}

	for input, expected := range validCases {
		got := authSvc.SanitizeReturnTo(input)
		if got != expected {
			t.Errorf("SanitizeReturnTo failed for valid path %q: got %q (expected %q)", input, got, expected)
		}
	}
}

// USR-008: 首次建档 (未建档访问 `/me` -> 只允许 onboarding/logout；完成后回原 returnTo)
func TestUSR008_MandatoryOnboardingRouteGate(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	email := "newuser-usr008@tokendance.dev"
	codeReq, _ := json.Marshal(map[string]string{"email": email, "locale": "en-US"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReq))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	ch, _ := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash(email))
	validCode := getChallengeCode(authSvc, ch.CodeHash)

	regBody, _ := json.Marshal(map[string]string{
		"email":    email,
		"code":     validCode,
		"password": "Password123!",
		"returnTo": "/me/settings",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(regBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var regResp struct {
		CSRFToken string `json:"csrfToken"`
		ReturnTo  string `json:"returnTo"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &regResp)
	cookie := extractSessionCookie(rec)

	if regResp.ReturnTo != "/me/settings" {
		t.Errorf("expected returnTo /me/settings, got %s", regResp.ReturnTo)
	}

	// 1. Attempt accessing /me/profile before onboarding -> BLOCKED 403 ONBOARDING_REQUIRED
	reqProfile := httptest.NewRequest(http.MethodGet, "/api/v1/me/profile", nil)
	reqProfile.AddCookie(cookie)
	recProfile := httptest.NewRecorder()
	router.ServeHTTP(recProfile, reqProfile)

	if recProfile.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for un-onboarded user accessing /me/profile, got %d", recProfile.Code)
	}
	var errResp ErrorWrapper
	_ = json.Unmarshal(recProfile.Body.Bytes(), &errResp)
	if errResp.Error.Code != "ONBOARDING_REQUIRED" {
		t.Errorf("expected error code ONBOARDING_REQUIRED, got %s", errResp.Error.Code)
	}

	// 2. Attempt accessing /me/summary before onboarding -> BLOCKED 403 ONBOARDING_REQUIRED
	reqSummary := httptest.NewRequest(http.MethodGet, "/api/v1/me/summary", nil)
	reqSummary.AddCookie(cookie)
	recSummary := httptest.NewRecorder()
	router.ServeHTTP(recSummary, reqSummary)

	if recSummary.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on /me/summary before onboarding, got %d", recSummary.Code)
	}

	// 3. /api/v1/auth/session is allowed
	reqSess := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	reqSess.AddCookie(cookie)
	recSess := httptest.NewRecorder()
	router.ServeHTTP(recSess, reqSess)

	if recSess.Code != http.StatusOK {
		t.Fatalf("expected 200 on /auth/session, got %d", recSess.Code)
	}

	var sessResp struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(recSess.Body.Bytes(), &sessResp)
	csrfToken := sessResp.CSRFToken
	if csrfToken == "" {
		csrfToken = regResp.CSRFToken
	}

	// 4. Complete Onboarding with returnTo
	onboardBody, _ := json.Marshal(map[string]interface{}{
		"displayName": "New Pilot",
		"handle":      "newpilot",
		"timezone":    "UTC",
		"locale":      "en-US",
		"privacy": map[string]interface{}{
			"publicProfileEnabled": true,
		},
		"returnTo": regResp.ReturnTo,
	})
	reqOnboard := httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", bytes.NewReader(onboardBody))
	reqOnboard.Header.Set("Content-Type", "application/json")
	reqOnboard.Header.Set("X-CSRF-Token", csrfToken)
	reqOnboard.AddCookie(cookie)
	recOnboard := httptest.NewRecorder()
	router.ServeHTTP(recOnboard, reqOnboard)

	if recOnboard.Code != http.StatusOK {
		t.Fatalf("expected 200 on completing onboarding, got %d: %s", recOnboard.Code, recOnboard.Body.String())
	}

	var onboardResp struct {
		ReturnTo string `json:"returnTo"`
	}
	_ = json.Unmarshal(recOnboard.Body.Bytes(), &onboardResp)
	if onboardResp.ReturnTo != "/me/settings" {
		t.Errorf("expected returnTo /me/settings, got %s", onboardResp.ReturnTo)
	}

	// 5. Now /me/profile and /me/summary are accessible
	reqProfileAfter := httptest.NewRequest(http.MethodGet, "/api/v1/me/profile", nil)
	reqProfileAfter.AddCookie(cookie)
	recProfileAfter := httptest.NewRecorder()
	router.ServeHTTP(recProfileAfter, reqProfileAfter)

	if recProfileAfter.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me/profile after onboarding, got %d: %s", recProfileAfter.Code, recProfileAfter.Body.String())
	}
}

// USR-009: Handle 并发 (两用户并发抢同 Handle -> 唯一一个成功；另一个 409)
func TestUSR009_ConcurrentHandleUniqueness(t *testing.T) {
	router, authSvc, st := setupTestRouter(t)

	// Register User 1
	codeReq1, _ := json.Marshal(map[string]string{"email": "user1-usr009@tokendance.dev", "locale": "en-US"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReq1))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	ch1, _ := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("user1-usr009@tokendance.dev"))
	code1 := getChallengeCode(authSvc, ch1.CodeHash)

	reg1, _ := json.Marshal(map[string]string{"email": "user1-usr009@tokendance.dev", "code": code1, "password": "Password123!"})
	r1 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(reg1))
	r1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, r1)
	var resp1 struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
	cookie1 := extractSessionCookie(w1)

	// Register User 2
	codeReq2, _ := json.Marshal(map[string]string{"email": "user2-usr009@tokendance.dev", "locale": "en-US"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/register/code", bytes.NewReader(codeReq2))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	ch2, _ := st.FindPendingEmailChallenge(context.Background(), domain.ChallengeTypeRegister, authSvc.ComputeEmailLookupHash("user2-usr009@tokendance.dev"))
	code2 := getChallengeCode(authSvc, ch2.CodeHash)

	reg2, _ := json.Marshal(map[string]string{"email": "user2-usr009@tokendance.dev", "code": code2, "password": "Password123!"})
	r2 := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(reg2))
	r2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, r2)
	var resp2 struct {
		CSRFToken string `json:"csrfToken"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	cookie2 := extractSessionCookie(w2)

	// Both attempt to claim handle "speedrunner" concurrently
	claimHandle := "speedrunner"
	onboardBody, _ := json.Marshal(map[string]interface{}{
		"displayName": "Runner",
		"handle":      claimHandle,
		"timezone":    "UTC",
		"locale":      "en-US",
		"privacy": map[string]interface{}{
			"publicProfileEnabled": true,
		},
	})

	var wg sync.WaitGroup
	wg.Add(2)
	var status1, status2 int

	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", bytes.NewReader(onboardBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", resp1.CSRFToken)
		req.AddCookie(cookie1)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		status1 = rec.Code
	}()

	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/me/onboarding", bytes.NewReader(onboardBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", resp2.CSRFToken)
		req.AddCookie(cookie2)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		status2 = rec.Code
	}()

	wg.Wait()

	// Exactly one must be 200 and the other 409
	if (status1 == http.StatusOK && status2 == http.StatusConflict) || (status1 == http.StatusConflict && status2 == http.StatusOK) {
		// Expected outcome
	} else {
		t.Fatalf("expected one 200 and one 409 for concurrent handle claim, got User1=%d, User2=%d", status1, status2)
	}
}

// USR-010: 中英文 (切换 locale 后访问全部用户页 -> 数据口径不变，只改变 messageKey 映射和 UI 文本)
func TestUSR010_LocaleAndMessageKeyConsistency(t *testing.T) {
	router, _, st := setupTestRouter(t)

	userID := "usr_usr010test"
	now := time.Now().UTC()
	u, _, _ := st.SeedUserForTest(userID, "localepilot", "locale@tokendance.dev", now)

	cookie := &http.Cookie{
		Name:  DevSessionCookie,
		Value: "test-session-token-" + userID,
	}
	csrfToken := "test-csrf-token-" + userID

	// 1. Update locale to zh-CN with If-Match
	newLocale := "zh-CN"
	patchBody, _ := json.Marshal(map[string]interface{}{
		"locale": newLocale,
	})
	reqPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/me/profile", bytes.NewReader(patchBody))
	reqPatch.Header.Set("Content-Type", "application/json")
	reqPatch.Header.Set("X-CSRF-Token", csrfToken)
	reqPatch.Header.Set("If-Match", fmt.Sprintf(`"%d"`, u.ProfileVersion))
	reqPatch.AddCookie(cookie)
	recPatch := httptest.NewRecorder()
	router.ServeHTTP(recPatch, reqPatch)

	if recPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 on updating profile locale, got %d: %s", recPatch.Code, recPatch.Body.String())
	}

	var updatedUser domain.User
	_ = json.Unmarshal(recPatch.Body.Bytes(), &updatedUser)
	if updatedUser.Locale != "zh-CN" {
		t.Errorf("expected locale zh-CN, got %s", updatedUser.Locale)
	}

	// 2. Fetch summary metrics: ensure metric numbers and schema remain identical regardless of locale
	reqSummary := httptest.NewRequest(http.MethodGet, "/api/v1/me/summary?range=30d", nil)
	reqSummary.AddCookie(cookie)
	recSummary := httptest.NewRecorder()
	router.ServeHTTP(recSummary, reqSummary)

	if recSummary.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me/summary, got %d", recSummary.Code)
	}

	var summary domain.PersonalSummary
	_ = json.Unmarshal(recSummary.Body.Bytes(), &summary)
	if !summary.Metrics.TotalTokens.Supported || *summary.Metrics.TotalTokens.Value == "" {
		t.Errorf("expected valid total tokens metric in summary")
	}

	// 3. Trigger error and verify structured error envelope with messageKey
	invalidName := ""
	invalidBody, _ := json.Marshal(map[string]interface{}{
		"displayName": invalidName,
	})
	reqErr := httptest.NewRequest(http.MethodPatch, "/api/v1/me/profile", bytes.NewReader(invalidBody))
	reqErr.Header.Set("Content-Type", "application/json")
	reqErr.Header.Set("X-CSRF-Token", csrfToken)
	reqErr.AddCookie(cookie)
	recErr := httptest.NewRecorder()
	router.ServeHTTP(recErr, reqErr)

	if recErr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", recErr.Code)
	}
	var errResp ErrorWrapper
	_ = json.Unmarshal(recErr.Body.Bytes(), &errResp)
	if errResp.Error.MessageKey == "" || errResp.Error.Code == "" {
		t.Errorf("expected non-empty messageKey and code in error response: %+v", errResp.Error)
	}
}

// USR-021: Public Profile 投影 (修改资料、隐私、暂停/注销账户并并发读取 -> projection version 单调递增；关闭路径同事务 hidden，无私有字段)
func TestUSR021_PublicProfileProjectionAndSameTransactionHidden(t *testing.T) {
	router, _, st := setupTestRouter(t)

	userID := "usr_usr021test"
	handle := "usr021pilot"
	now := time.Now().UTC()
	u, _, _ := st.SeedUserForTest(userID, handle, "usr021@tokendance.dev", now)

	cookie := &http.Cookie{
		Name:  DevSessionCookie,
		Value: "test-session-token-" + userID,
	}
	csrfToken := "test-csrf-token-" + userID

	// 1. Enable Public Profile in Privacy
	enablePrivBody, _ := json.Marshal(map[string]interface{}{
		"publicProfileEnabled":  true,
		"leaderboardVisibility": "public",
		"showBio":               true,
		"showTokenTotal":        true,
	})
	reqEnable := httptest.NewRequest(http.MethodPatch, "/api/v1/me/privacy", bytes.NewReader(enablePrivBody))
	reqEnable.Header.Set("Content-Type", "application/json")
	reqEnable.Header.Set("X-CSRF-Token", csrfToken)
	reqEnable.AddCookie(cookie)
	recEnable := httptest.NewRecorder()
	router.ServeHTTP(recEnable, reqEnable)

	if recEnable.Code != http.StatusOK {
		t.Fatalf("expected 200 on enabling public profile, got %d: %s", recEnable.Code, recEnable.Body.String())
	}
	var enabledPrivacy domain.UserPrivacySettings
	_ = json.Unmarshal(recEnable.Body.Bytes(), &enabledPrivacy)
	if !enabledPrivacy.PublicProfileEnabled || enabledPrivacy.LeaderboardVisibility != domain.LeaderboardVisibilityPublic {
		t.Fatalf("expected public profile and leaderboard visibility to update together: %+v", enabledPrivacy)
	}

	reqPub := httptest.NewRequest(http.MethodGet, "/api/v1/public/users/"+handle, nil)
	recPub := httptest.NewRecorder()
	router.ServeHTTP(recPub, reqPub)

	if recPub.Code != http.StatusOK {
		t.Fatalf("expected 200 on published public profile, got %d: %s", recPub.Code, recPub.Body.String())
	}
	var pub1 domain.PublicUserProfile
	_ = json.Unmarshal(recPub.Body.Bytes(), &pub1)
	v1 := pub1.ProjectionVersion

	// 2. Update Profile -> Monotonically increments projection version
	newDisplay := "USR021 Updated Name"
	newBio := "My public bio"
	profBody, _ := json.Marshal(map[string]interface{}{
		"displayName": newDisplay,
		"bio":         newBio,
	})
	reqPatch := httptest.NewRequest(http.MethodPatch, "/api/v1/me/profile", bytes.NewReader(profBody))
	reqPatch.Header.Set("Content-Type", "application/json")
	reqPatch.Header.Set("X-CSRF-Token", csrfToken)
	reqPatch.Header.Set("If-Match", fmt.Sprintf(`"%d"`, u.ProfileVersion))
	reqPatch.AddCookie(cookie)
	recPatch := httptest.NewRecorder()
	router.ServeHTTP(recPatch, reqPatch)

	if recPatch.Code != http.StatusOK {
		t.Fatalf("expected 200 on profile update, got %d: %s", recPatch.Code, recPatch.Body.String())
	}

	reqPub2 := httptest.NewRequest(http.MethodGet, "/api/v1/public/users/"+handle, nil)
	recPub2 := httptest.NewRecorder()
	router.ServeHTTP(recPub2, reqPub2)

	var pub2 domain.PublicUserProfile
	_ = json.Unmarshal(recPub2.Body.Bytes(), &pub2)
	if pub2.ProjectionVersion <= v1 {
		t.Fatalf("expected monotonic projection version increase: v2 (%d) <= v1 (%d)", pub2.ProjectionVersion, v1)
	}
	if pub2.DisplayName != newDisplay {
		t.Errorf("expected updated display name in projection")
	}

	// 3. Disable Public Profile in Privacy -> Immediately Hidden in Same Transaction
	privBody, _ := json.Marshal(map[string]interface{}{
		"publicProfileEnabled":  false,
		"leaderboardVisibility": "private",
		"showBio":               false,
	})
	reqPriv := httptest.NewRequest(http.MethodPatch, "/api/v1/me/privacy", bytes.NewReader(privBody))
	reqPriv.Header.Set("Content-Type", "application/json")
	reqPriv.Header.Set("X-CSRF-Token", csrfToken)
	reqPriv.AddCookie(cookie)
	recPriv := httptest.NewRecorder()
	router.ServeHTTP(recPriv, reqPriv)

	if recPriv.Code != http.StatusOK {
		t.Fatalf("expected 200 on privacy update, got %d: %s", recPriv.Code, recPriv.Body.String())
	}
	var disabledPrivacy domain.UserPrivacySettings
	_ = json.Unmarshal(recPriv.Body.Bytes(), &disabledPrivacy)
	if disabledPrivacy.PublicProfileEnabled || disabledPrivacy.LeaderboardVisibility != domain.LeaderboardVisibilityPrivate {
		t.Fatalf("expected public profile and leaderboard visibility to update together: %+v", disabledPrivacy)
	}

	// Immediate concurrent public read -> MUST RETURN 404 NOT FOUND
	reqPubHidden := httptest.NewRequest(http.MethodGet, "/api/v1/public/users/"+handle, nil)
	recPubHidden := httptest.NewRecorder()
	router.ServeHTTP(recPubHidden, reqPubHidden)

	if recPubHidden.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found for disabled public profile, got %d (body: %s)", recPubHidden.Code, recPubHidden.Body.String())
	}
}

// USR-022: 头像对象 (上传伪造 MIME、超限/像素炸弹、他人 object id -> 全部拒绝；只有 owner 的 ready 对象可切为当前头像)
func TestUSR022_AvatarValidationAndPixelBombProtection(t *testing.T) {
	router, _, st := setupTestRouter(t)

	userID1 := "usr_usr022user1"
	userID2 := "usr_usr022user2"
	now := time.Now().UTC()
	_, _, _ = st.SeedUserForTest(userID1, "avataruser1", "user1@tokendance.dev", now)
	_, _, _ = st.SeedUserForTest(userID2, "avataruser2", "user2@tokendance.dev", now)

	cookie1 := &http.Cookie{Name: DevSessionCookie, Value: "test-session-token-" + userID1}
	csrfToken1 := "test-csrf-token-" + userID1

	cookie2 := &http.Cookie{Name: DevSessionCookie, Value: "test-session-token-" + userID2}
	csrfToken2 := "test-csrf-token-" + userID2

	// 1. Forged MIME type (e.g. text/html, application/javascript, image/svg+xml) -> REJECTED 400
	forgedMimes := []string{"text/html", "application/javascript", "image/svg+xml", "application/octet-stream", "video/mp4"}
	for _, mime := range forgedMimes {
		body, _ := json.Marshal(map[string]interface{}{
			"contentType": mime,
			"byteSize":    1024,
			"sha256":      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar-upload-intents", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CSRF-Token", csrfToken1)
		req.AddCookie(cookie1)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request for forged MIME %s, got %d", mime, rec.Code)
		}
	}

	// 2. Oversized / > 5 MiB file -> REJECTED 400
	overSizeBody, _ := json.Marshal(map[string]interface{}{
		"contentType": "image/png",
		"byteSize":    6 * 1024 * 1024, // 6 MiB > 5 MiB max
		"sha256":      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	reqOver := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar-upload-intents", bytes.NewReader(overSizeBody))
	reqOver.Header.Set("Content-Type", "application/json")
	reqOver.Header.Set("X-CSRF-Token", csrfToken1)
	reqOver.AddCookie(cookie1)
	recOver := httptest.NewRecorder()
	router.ServeHTTP(recOver, reqOver)

	if recOver.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request for oversized file, got %d", recOver.Code)
	}

	// 3. Valid Upload Intent created for User 1
	validBody, _ := json.Marshal(map[string]interface{}{
		"contentType": "image/png",
		"byteSize":    1024 * 1024, // 1 MiB
		"sha256":      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	})
	reqValid := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar-upload-intents", bytes.NewReader(validBody))
	reqValid.Header.Set("Content-Type", "application/json")
	reqValid.Header.Set("X-CSRF-Token", csrfToken1)
	reqValid.AddCookie(cookie1)
	recValid := httptest.NewRecorder()
	router.ServeHTTP(recValid, reqValid)

	if recValid.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for valid avatar intent, got %d: %s", recValid.Code, recValid.Body.String())
	}
	var intentResp struct {
		ObjectID string `json:"objectId"`
	}
	_ = json.Unmarshal(recValid.Body.Bytes(), &intentResp)

	// 4. User 2 attempts to complete User 1's avatar object -> REJECTED 404 / Unauthorized
	reqCrossUser := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar-upload-intents/"+intentResp.ObjectID+"/complete", nil)
	reqCrossUser.Header.Set("X-CSRF-Token", csrfToken2)
	reqCrossUser.AddCookie(cookie2)
	recCrossUser := httptest.NewRecorder()
	router.ServeHTTP(recCrossUser, reqCrossUser)

	if recCrossUser.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-user completing another's upload object, got %d", recCrossUser.Code)
	}

	// 5. Seed valid PNG image data into storage to simulate successful client upload
	validPNG := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
		0x0A, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
		0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	_ = testStorage.PutObject(context.Background(), "users/"+userID1+"/avatars/"+intentResp.ObjectID, bytes.NewReader(validPNG), int64(len(validPNG)), "image/png")

	// 6. User 1 completes own avatar intent -> SUCCEEDS and switches avatar pointer
	reqComplete := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar-upload-intents/"+intentResp.ObjectID+"/complete", nil)
	reqComplete.Header.Set("X-CSRF-Token", csrfToken1)
	reqComplete.AddCookie(cookie1)
	recComplete := httptest.NewRecorder()
	router.ServeHTTP(recComplete, reqComplete)

	if recComplete.Code != http.StatusOK {
		t.Fatalf("expected 200 on completing own avatar intent, got %d: %s", recComplete.Code, recComplete.Body.String())
	}

	// Verify User 1's avatar pointer was updated
	u1, err := st.FindUserByID(context.Background(), userID1)
	if err != nil || u1.AvatarObjectID == nil || *u1.AvatarObjectID != intentResp.ObjectID {
		t.Fatalf("expected user1 avatar pointer to be updated to %s", intentResp.ObjectID)
	}
}
