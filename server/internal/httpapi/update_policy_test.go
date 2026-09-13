package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdatePolicyReloadsWithoutRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("TOKENDANCE_UPDATE_POLICY_FILE", path)
	for _, version := range []string{"0.1.27", "0.2.0", ""} {
		if err := os.WriteFile(path, []byte(`{"minimumVersion":"`+version+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		(&Handlers{}).GetUpdatePolicy(rec, httptest.NewRequest("GET", "/v1/update-policy", nil))
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"minimumVersion":"`+version+`"`) || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Body.String())
		}
	}
	if err := os.WriteFile(path, []byte(`{"minimumVersion":"oops"}`), 0600); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	(&Handlers{}).GetUpdatePolicy(rec, httptest.NewRequest("GET", "/v1/update-policy", nil))
	if rec.Code != 503 {
		t.Fatal(rec.Code)
	}
	for _, content := range []string{`{"minimumVersion":"0.1.27"} {}`, `{"minimumVersion":"0.1.27","secret":"no"}`, `{"minimumVersion":"0.1.27"}` + strings.Repeat(" ", 4096)} {
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		(&Handlers{}).GetUpdatePolicy(rec, httptest.NewRequest("GET", "/v1/update-policy", nil))
		if rec.Code != 503 {
			t.Fatal("invalid configuration accepted")
		}
	}
}
