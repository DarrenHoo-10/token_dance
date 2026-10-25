package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetModelAliasesServesDeploymentFile(t *testing.T) {
	rec := httptest.NewRecorder()
	(&Handlers{}).GetModelAliases(rec, httptest.NewRequest("GET", "/v1/model-aliases", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revision":0`) || !strings.Contains(rec.Body.String(), `"aliases":[]`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unset rules must serve the empty document, got %d %s", rec.Code, rec.Body.String())
	}

	path := filepath.Join(t.TempDir(), "aliases.json")
	t.Setenv("TOKENDANCE_MODEL_ALIAS_FILE", path)
	if err := os.WriteFile(path, []byte(`{"revision":2,"aliases":[{"from":"deepseek-v4p1-flash","to":"deepseek-v4.1-flash"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	(&Handlers{}).GetModelAliases(rec, httptest.NewRequest("GET", "/v1/model-aliases", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"revision":2`) || !strings.Contains(rec.Body.String(), `"from":"deepseek-v4p1-flash"`) || !strings.Contains(rec.Body.String(), `"to":"deepseek-v4.1-flash"`) {
		t.Fatalf("rewritten rules must take effect without restart, got %d %s", rec.Code, rec.Body.String())
	}

	if err := os.WriteFile(path, []byte(`{"revision":3,"aliases":[{"from":"a b","to":"c"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	(&Handlers{}).GetModelAliases(rec, httptest.NewRequest("GET", "/v1/model-aliases", nil))
	if rec.Code != 503 {
		t.Fatalf("an invalid configured file must fail closed, got %d", rec.Code)
	}
}
