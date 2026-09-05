package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopBrowserHandoff(t *testing.T) {
	svc, _, clk := setupAuthService(t)
	ctx := context.Background()
	email := "desktop-handoff@example.com"
	if err := svc.RequestRegistrationCode(ctx, email, "en-US"); err != nil {
		t.Fatal(err)
	}
	registration, err := svc.CompleteRegistration(ctx, email, svc.EmailSink().LatestCode(email), "SecretPass123!", "/", "en-US", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := svc.ResolveSession(ctx, registration.SessionToken)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 64)
	challenge := fmt.Sprintf("%x", sha256.Sum256([]byte(verifier)))
	state := strings.Repeat("b", 64)
	redirect := "http://127.0.0.1:49152/callback"
	issue := func() string {
		t.Helper()
		target, err := svc.AuthorizeDesktop(source, redirect, challenge, state)
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(target)
		if u.Query().Get("state") != state || strings.Contains(target, registration.SessionToken) {
			t.Fatal("unsafe callback")
		}
		return u.Query().Get("code")
	}
	code := issue()
	if _, err := svc.ExchangeDesktop(ctx, code, strings.Repeat("c", 64), redirect); err == nil {
		t.Fatal("wrong verifier accepted")
	}
	if _, err := svc.ExchangeDesktop(ctx, code, verifier, "http://127.0.0.1:49153/callback"); err == nil {
		t.Fatal("wrong callback accepted")
	}
	result, err := svc.ExchangeDesktop(ctx, code, verifier, redirect)
	if err != nil {
		t.Fatal(err)
	}
	if result.User.UserID != source.UserID || result.SessionToken == registration.SessionToken || result.Session.SessionID == source.SessionID {
		t.Fatal("expected independent same-account desktop session")
	}
	if result.Session.DeviceLabel == nil || *result.Session.DeviceLabel != "TokenDance Desktop" {
		t.Fatal("missing device label")
	}
	if _, _, err := svc.ResolveSession(ctx, result.SessionToken); err != nil {
		t.Fatal("desktop session invalid", err)
	}
	if _, err := svc.ExchangeDesktop(ctx, code, verifier, redirect); err == nil {
		t.Fatal("replayed handoff accepted")
	}
	if err := svc.Logout(ctx, result.Session.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.ResolveSession(ctx, registration.SessionToken); err != nil {
		t.Fatal("desktop logout affected browser", err)
	}

	code = issue()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.ExchangeDesktop(ctx, code, verifier, redirect); err == nil {
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatalf("concurrent exchange successes: %d", successes.Load())
	}

	code = issue()
	clk.Advance(2 * time.Minute)
	if _, err := svc.ExchangeDesktop(ctx, code, verifier, redirect); err == nil {
		t.Fatal("expired handoff accepted")
	}
	code = issue()
	if err := svc.Logout(ctx, source.SessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ExchangeDesktop(ctx, code, verifier, redirect); err == nil {
		t.Fatal("revoked browser session accepted")
	}
}

func TestDesktopCallbackValidation(t *testing.T) {
	for _, target := range []string{
		"https://evil.example/callback", "http://localhost:1234/callback", "http://127.0.0.1.evil.example:1234/callback",
		"http://127.0.0.1:1234/other", "http://127.0.0.1:1234/callback?next=evil", "http://u@127.0.0.1:1234/callback",
		"http://127.0.0.1:1234/callback#secret", "http://127.0.0.1:0/callback", "http://127.0.0.1:65536/callback",
	} {
		if validDesktopRedirect(target) {
			t.Errorf("accepted unsafe callback %s", target)
		}
	}
	if !validDesktopRedirect("http://127.0.0.1:49152/callback") {
		t.Fatal("valid loopback callback rejected")
	}
}
