package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
