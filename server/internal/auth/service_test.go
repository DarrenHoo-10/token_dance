package auth

import (
	"context"
	"testing"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/domain"
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
	_, err = svc.CompleteRegistration(ctx, email, "999999", password, "/dashboard")
	if err == nil {
		t.Fatalf("expected error on wrong verification code")
	}

	var code string
	for i := 0; i <= 999999; i++ {
		cStr := formatCode(i)
		cHash := svc.ComputeTokenHash(cStr)
		if cHash == challenge.CodeHash {
			code = cStr
			break
		}
	}
	if code == "" {
		t.Fatalf("failed to find code from challenge")
	}

	// 3. Complete Registration with valid code
	regResult, err := svc.CompleteRegistration(ctx, email, code, password, "/dashboard")
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

func formatCode(n int) string {
	s := ""
	for i := 0; i < 6; i++ {
		s = string(rune('0'+(n%10))) + s
		n /= 10
	}
	return s
}
