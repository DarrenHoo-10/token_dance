package auth

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
	emailpkg "tokendance/internal/email"
	"tokendance/internal/profile"
	"tokendance/internal/store/memory"
)

func setupAuthService(t *testing.T) (*Service, *memory.MemoryStore, *clock.MockClock) {
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	cfg.Argon2MemoryKiB = 1024
	cfg.Argon2Time = 1
	cfg.Argon2Parallelism = 1

	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	clk := clock.NewMockClock(now)

	svc := NewService(st, cfg, clk)
	return svc, st, clk
}

func TestAuthFlows(t *testing.T) {
	ctx := context.Background()
	svc, st, clk := setupAuthService(t)

	email := "testuser@example.com"
	password := "SecretPass123!"

	// 1. Request Register Code
	err := svc.RequestRegistrationCode(ctx, email, "en-US")
	if err != nil {
		t.Fatalf("failed to request registration code: %v", err)
	}

	emailHash := svc.ComputeEmailLookupHash(email)
	challenge, err := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("expected pending challenge: %v", err)
	}

	// 2. Complete Registration with wrong code
	_, err = svc.CompleteRegistration(ctx, email, "999999", password, "/dashboard", "en-US", "UTC")
	if err == nil {
		t.Fatalf("expected error on wrong verification code")
	}

	code := svc.EmailSink().LatestCode(email)
	if code == "" {
		for i := 0; i <= 999999; i++ {
			cStr := formatCode(i)
			cHash := svc.ComputeTokenHash(cStr)
			if cHash == challenge.CodeHash {
				code = cStr
				break
			}
		}
	}
	if code == "" {
		t.Fatalf("failed to find code from challenge")
	}

	// 3. Complete Registration with valid code
	regResult, err := svc.CompleteRegistration(ctx, email, code, password, "/dashboard", "en-US", "UTC")
	if err != nil {
		t.Fatalf("failed to complete registration: %v", err)
	}

	if regResult.User == nil || regResult.User.EmailVerifiedAt == nil {
		t.Errorf("expected verified user")
	}
	if regResult.ReturnTo != "/dashboard" {
		t.Errorf("expected /dashboard returnTo, got %s", regResult.ReturnTo)
	}
	if regResult.SessionToken == "" || regResult.CSRFToken == "" {
		t.Errorf("expected session and csrf tokens")
	}

	// 4. Resolve Session
	sess, u, err := svc.ResolveSession(ctx, regResult.SessionToken)
	if err != nil {
		t.Fatalf("failed to resolve session: %v", err)
	}
	if sess == nil || u.UserID != regResult.User.UserID {
		t.Errorf("resolved user id mismatch")
	}

	// 5. Test Expiry
	clk.Add(31 * 24 * time.Hour) // > 30 days
	_, _, err = svc.ResolveSession(ctx, regResult.SessionToken)
	if err == nil {
		t.Errorf("session should be expired after 31 days")
	}
	clk.Set(time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)) // reset clock

	// 6. Login with correct password
	loginResult, err := svc.Login(ctx, email, password, "/settings", "Chrome / macOS")
	if err != nil {
		t.Fatalf("failed to login: %v", err)
	}
	if loginResult.User.UserID != regResult.User.UserID {
		t.Errorf("login returned wrong user")
	}

	// 7. Login with wrong password
	_, err = svc.Login(ctx, email, "WrongPassword!", "/home", "Safari")
	if err == nil {
		t.Errorf("expected login error with wrong password")
	}

	// 8. Revoke Other Sessions
	err = svc.RevokeOtherSessions(ctx, regResult.User.UserID, loginResult.Session.SessionID)
	if err != nil {
		t.Fatalf("failed to revoke other sessions: %v", err)
	}

	// Check old session is now unauthorized
	_, _, err = svc.ResolveSession(ctx, regResult.SessionToken)
	if err == nil {
		t.Errorf("old session should be revoked")
	}

	// Check current session is still valid
	_, _, err = svc.ResolveSession(ctx, loginResult.SessionToken)
	if err != nil {
		t.Errorf("current session should still be valid: %v", err)
	}

	// 9. Logout
	err = svc.Logout(ctx, loginResult.Session.SessionID)
	if err != nil {
		t.Fatalf("failed to logout: %v", err)
	}
	_, _, err = svc.ResolveSession(ctx, loginResult.SessionToken)
	if err == nil {
		t.Errorf("logged out session should be revoked")
	}
}

func TestAEADCiphertext_NoPlaintextInDB(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := setupAuthService(t)

	sink := emailpkg.NewDeliverySink()
	svc.SetEmailSink(sink)

	email := "confidential-pilot@tokendance.dev"
	password := "StrongPass123!"

	// 1. Request Code
	if err := svc.RequestRegistrationCode(ctx, email, "en-US"); err != nil {
		t.Fatalf("failed to request registration code: %v", err)
	}

	// Verify challenge in store does NOT contain plaintext email
	emailHash := svc.ComputeEmailLookupHash(email)
	challenge, err := st.FindPendingEmailChallenge(ctx, domain.ChallengeTypeRegister, emailHash)
	if err != nil {
		t.Fatalf("failed to find pending challenge: %v", err)
	}

	if bytes.Contains(challenge.EmailCiphertext, []byte(email)) {
		t.Fatalf("email_challenges.email_ciphertext contains plaintext email: %s", string(challenge.EmailCiphertext))
	}
	if len(challenge.EmailCiphertext) < 28 {
		t.Fatalf("email_challenges.email_ciphertext length is too short for AEAD: %d", len(challenge.EmailCiphertext))
	}

	// Retrieve code from deterministic delivery sink
	code := sink.LatestCode(email)
	if len(code) != 6 {
		t.Fatalf("expected 6-digit code from test delivery sink, got %s", code)
	}

	// 2. Complete Registration
	regResult, err := svc.CompleteRegistration(ctx, email, code, password, "/dashboard", "en-US", "UTC")
	if err != nil {
		t.Fatalf("failed to complete registration: %v", err)
	}

	// Verify users.email_ciphertext in store does NOT contain plaintext email
	if bytes.Contains(regResult.User.EmailCiphertext, []byte(email)) {
		t.Fatalf("users.email_ciphertext contains plaintext email: %s", string(regResult.User.EmailCiphertext))
	}
	if len(regResult.User.EmailCiphertext) < 28 {
		t.Fatalf("users.email_ciphertext length is too short for AEAD: %d", len(regResult.User.EmailCiphertext))
	}

	// Verify decryption recovers exact normalized email
	decryptedEmail, err := svc.DecryptUserEmail(regResult.User)
	if err != nil {
		t.Fatalf("failed to decrypt user email: %v", err)
	}
	if decryptedEmail != email {
		t.Fatalf("expected decrypted email %s, got %s", email, decryptedEmail)
	}
}

func TestTestAuthCode_EnvBehavior(t *testing.T) {
	ctx := context.Background()

	// 1. In dev/test environment, TOKENDANCE_TEST_AUTH_CODE is honored
	st := memory.NewMemoryStore()
	cfg := config.DefaultConfig()
	cfg.Environment = "test"
	cfg.TestAuthCode = "778899"
	clk := clock.NewMockClock(time.Now().UTC())

	svc := NewService(st, cfg, clk)
	sink := emailpkg.NewDeliverySink()
	svc.SetEmailSink(sink)

	email := "testcode@tokendance.dev"
	if err := svc.RequestRegistrationCode(ctx, email, "en-US"); err != nil {
		t.Fatalf("failed to request code: %v", err)
	}

	if code := sink.LatestCode(email); code != "778899" {
		t.Fatalf("expected test auth code 778899 in test env, got %s", code)
	}

	// Verification with 778899 must succeed
	regResult, err := svc.CompleteRegistration(ctx, email, "778899", "Password123!", "/", "en-US", "UTC")
	if err != nil {
		t.Fatalf("expected registration to succeed with test auth code: %v", err)
	}
	if regResult.User == nil {
		t.Fatalf("expected created user")
	}

	// 2. In production environment, TOKENDANCE_TEST_AUTH_CODE is strictly prohibited
	prodCfg := config.DefaultConfig()
	prodCfg.Environment = "production"
	prodCfg.MySQLDSN = "root:pass@tcp(127.0.0.1:3306)/tokendance"
	prodCfg.EncryptionKey = "prod-32-byte-encryption-key-0001"
	prodCfg.HMACSecret = "prod-32-byte-hmac-secret-tokendance-01"
	prodCfg.EmailProvider = "worker"
	prodCfg.TestAuthCode = "778899"

	if err := prodCfg.Validate(); err == nil {
		t.Fatalf("expected config.Validate to fail when TOKENDANCE_TEST_AUTH_CODE is set in production")
	}
}

func formatCode(n int) string {
	s := ""
	for i := 0; i < 6; i++ {
		s = string(rune('0'+(n%10))) + s
		n /= 10
	}
	return s
}

// 注册即默认建档：新账号无需手动 onboarding 即可用默认资料直接进入应用。
func TestCompleteRegistration_DefaultProfile(t *testing.T) {
	ctx := context.Background()
	svc, st, _ := setupAuthService(t)

	registerUser := func(email, locale, timezone string) *domain.User {
		if err := svc.RequestRegistrationCode(ctx, email, locale); err != nil {
			t.Fatalf("failed to request code for %s: %v", email, err)
		}
		code := svc.EmailSink().LatestCode(email)
		if code == "" {
			t.Fatalf("expected verification code for %s", email)
		}
		res, err := svc.CompleteRegistration(ctx, email, code, "Password123!", "/", locale, timezone)
		if err != nil {
			t.Fatalf("failed to complete registration for %s: %v", email, err)
		}
		return res.User
	}

	// 1. Registration completes onboarding with an email-derived handle and
	//    the client-provided locale/timezone; visibility defaults to private.
	u := registerUser("darthcoder@tokendance.dev", "zh-CN", "Asia/Shanghai")
	if u.OnboardingCompletedAt == nil {
		t.Errorf("expected onboarding completed at registration")
	}
	if u.Handle == nil || *u.Handle != "darthcoder" {
		t.Errorf("expected email-derived handle darthcoder, got %v", u.Handle)
	}
	if u.Locale != "zh-CN" || u.TimezoneName != "Asia/Shanghai" {
		t.Errorf("expected client locale/timezone defaults, got %s/%s", u.Locale, u.TimezoneName)
	}
	if u.LeaderboardVisibility != domain.LeaderboardVisibilityPrivate {
		t.Errorf("expected private default visibility, got %s", u.LeaderboardVisibility)
	}
	priv, err := st.Privacy().GetPrivacy(ctx, u.UserID)
	if err != nil {
		t.Fatalf("failed to load privacy settings: %v", err)
	}
	if priv.PublicProfileEnabled || priv.LeaderboardVisibility != domain.LeaderboardVisibilityPrivate {
		t.Errorf("expected all-private privacy defaults, got %+v", priv)
	}

	// 2. Unsupported locale/timezone fall back to safe defaults.
	u2 := registerUser("fallbacks@tokendance.dev", "fr-FR", "Mars/Olympus")
	if u2.Locale != "en-US" {
		t.Errorf("expected locale fallback en-US, got %s", u2.Locale)
	}
	if u2.TimezoneName != "UTC" {
		t.Errorf("expected timezone fallback UTC, got %s", u2.TimezoneName)
	}

	// 3. Same local part on another domain gets a unique suffixed handle.
	u3 := registerUser("darthcoder@other.dev", "en-US", "UTC")
	if u3.Handle == nil || *u3.Handle == "darthcoder" {
		t.Errorf("expected suffixed handle for duplicated local part, got %v", u3.Handle)
	}
	if u3.Handle != nil {
		if !strings.HasPrefix(*u3.Handle, "darthcoder_") {
			t.Errorf("expected stem-prefixed handle, got %s", *u3.Handle)
		}
		if err := profile.ValidateHandle(*u3.Handle); err != nil {
			t.Errorf("expected valid generated handle, got %s (%v)", *u3.Handle, err)
		}
	}

	// 4. A local part without usable handle characters falls back to dancer_.
	u4 := registerUser("12345@tokendance.dev", "en-US", "UTC")
	if u4.Handle == nil || !strings.HasPrefix(*u4.Handle, "dancer_") {
		t.Errorf("expected dancer_ fallback handle, got %v", u4.Handle)
	}
}
