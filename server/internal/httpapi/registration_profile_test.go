package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegisterReturnsSelectedAvatar(t *testing.T) {
	router, svc, _ := setupTestRouter(t)
	email := "profile-api@tokendance.dev"
	if err := svc.RequestRegistrationCode(context.Background(), email, "zh-CN"); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"email": email, "code": svc.EmailSink().LatestCode(email), "password": "Password123!", "displayName": "星际狐狸", "avatarId": "fox", "locale": "zh-CN"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register failed: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		User struct {
			DisplayName string `json:"displayName"`
			AvatarURL   string `json:"avatarUrl"`
		} `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.User.DisplayName != "星际狐狸" || response.User.AvatarURL != "/images/avatars/fox.png" {
		t.Fatalf("incorrect response: %+v", response)
	}
}
