package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicLeaderboardAndStatsDoNotRequireAuth(t *testing.T) {
	router, _, _ := setupTestRouter(t)
	for _, path := range []string{
		"/api/v1/public/leaderboards?window=today",
		"/api/v1/public/leaderboards/stats?window=today",
		"/api/v1/public/leaderboards/stats?window=7d",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"ownEntry"`) {
			t.Fatalf("%s leaked ownEntry: %s", path, rec.Body.String())
		}
	}

	for _, path := range []string{"/api/v1/me/leaderboards", "/api/v1/me/summary", "/api/v1/me/exports"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s should stay private, got %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestPublicLeaderboardIgnoresClientUserID(t *testing.T) {
	router, _, _ := setupTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/public/leaderboards?window=today&userId=usr_other&user=usr_other", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"ownEntry"`) {
		t.Fatalf("public leaderboard leaked ownEntry from client user id: %s", rec.Body.String())
	}
}

func TestLeaderboardAndAvatarPublicErrors(t *testing.T) {
	router, _, _ := setupTestRouter(t)
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/api/v1/public/leaderboards?cursor=1000", http.StatusBadRequest},
		{"/api/v1/public/leaderboards?cursor=-1", http.StatusBadRequest},
		{"/api/v1/public/leaderboards?cursor=invalid", http.StatusBadRequest},
		{"/api/v1/public/avatars/missing-avatar", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}
