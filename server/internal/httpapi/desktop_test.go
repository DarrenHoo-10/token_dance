package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDesktopLoginHTTP(t *testing.T) {
	router, svc, _ := setupTestRouter(t)
	ctx := context.Background()
	email := "http-desktop@example.com"
	if err := svc.RequestRegistrationCode(ctx, email, "en-US"); err != nil {
		t.Fatal(err)
	}
	reg, err := svc.CompleteRegistration(ctx, email, svc.EmailSink().LatestCode(email), "SecretPass123!", "/", "en-US", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("a", 64)
	redirect := "http://127.0.0.1:49152/callback"
	body, _ := json.Marshal(map[string]string{"redirectUri": redirect, "codeChallenge": fmt.Sprintf("%x", sha256.Sum256([]byte(verifier))), "state": strings.Repeat("b", 64)})
	for _, mode := range []string{"anonymous", "no-csrf", "authorized"} {
		req := httptest.NewRequest("POST", "/api/v1/auth/desktop/authorize", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if mode != "anonymous" {
			req.AddCookie(&http.Cookie{Name: DevSessionCookie, Value: reg.SessionToken})
		}
		if mode == "authorized" {
			req.Header.Set("X-CSRF-Token", reg.CSRFToken)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if mode != "authorized" {
			if rec.Code < 400 {
				t.Fatal("handoff bypassed auth/CSRF", mode)
			}
			continue
		}
		if rec.Code != 200 {
			t.Fatalf("authorize %d %s", rec.Code, rec.Body.String())
		}
		if extractSessionCookie(rec) != nil {
			t.Fatal("authorize replaced browser session")
		}
		var payload struct {
			RedirectURL string `json:"redirectUrl"`
		}
		json.Unmarshal(rec.Body.Bytes(), &payload)
		target, _ := url.Parse(payload.RedirectURL)
		exchange, _ := json.Marshal(map[string]string{"code": target.Query().Get("code"), "codeVerifier": verifier, "redirectUri": redirect})
		req = httptest.NewRequest("POST", "/api/v1/auth/desktop/exchange", strings.NewReader(string(exchange)))
		req.Header.Set("Content-Type", "application/json")
		rec = httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("exchange %d %s", rec.Code, rec.Body.String())
		}
		cookie := extractSessionCookie(rec)
		if cookie == nil || !cookie.HttpOnly || cookie.Value == reg.SessionToken {
			t.Fatal("invalid native session cookie")
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("handoff must not be cached")
		}
		sess, user, err := svc.ResolveSession(ctx, cookie.Value)
		if err != nil || user.UserID != reg.User.UserID || sess.CSRFToken == "" {
			t.Fatal("native session invalid")
		}
	}
}
