package modelalias

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseNormalizesAndValidates(t *testing.T) {
	cfg, err := Parse([]byte(`{"revision":3,"aliases":[
		{"from":"  DeepSeek-V4P1-Flash ","to":"DeepSeek-V4.1-Flash"},
		{"from":"kimi-k3-thinking@x","to":"kimi-k3-thinking"}
	]}`))
	if err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	if cfg.Revision != 3 || len(cfg.Aliases) != 2 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Aliases[0].From != "deepseek-v4p1-flash" || cfg.Aliases[0].To != "deepseek-v4.1-flash" {
		t.Errorf("names must normalize to lowercased trims, got %+v", cfg.Aliases[0])
	}
	m := cfg.Map()
	if m["deepseek-v4p1-flash"] != "deepseek-v4.1-flash" || m["kimi-k3-thinking@x"] != "kimi-k3-thinking" {
		t.Errorf("map must index the rules, got %+v", m)
	}
	if _, ok := m["missing"]; ok {
		t.Errorf("unknown spellings must not resolve")
	}
}

func TestParseRejectsInvalidDocuments(t *testing.T) {
	cases := map[string]string{
		"unknown field":    `{"revision":1,"secret":"x"}`,
		"trailing content": `{"revision":1} {}`,
		"empty from":       `{"aliases":[{"from":"  ","to":"x"}]}`,
		"empty to":         `{"aliases":[{"from":"x","to":""}]}`,
		"identity rule":    `{"aliases":[{"from":"x","to":"X"}]}`,
		"duplicate rule":   `{"aliases":[{"from":"x","to":"y"},{"from":"X","to":"z"}]}`,
		"bad charset":      `{"aliases":[{"from":"a b","to":"y"}]}`,
		"oversized name":   `{"aliases":[{"from":"` + strings.Repeat("a", MaxNameLength+1) + `","to":"y"}]}`,
		"not an object":    `[]`,
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	tooMany := `{"aliases":[` +
		strings.TrimSuffix(strings.Repeat(`{"from":"a","to":"b"},`, MaxRules+1), ",") + `]}`
	if _, err := Parse([]byte(tooMany)); !errors.Is(err, ErrTooManyRules) {
		t.Errorf("too many rules must fail with ErrTooManyRules, got %v", err)
	}
}

func TestReadWithoutFileIsEmpty(t *testing.T) {
	t.Setenv("TOKENDANCE_MODEL_ALIAS_FILE", "")
	cfg, err := Read()
	if err != nil || len(cfg.Aliases) != 0 {
		t.Fatalf("unset file must be the empty state, got %+v err=%v", cfg, err)
	}
	if cfg.Map() != nil {
		t.Errorf("an empty config must index to nil")
	}
}

func TestReadsFileWithoutRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.json")
	t.Setenv("TOKENDANCE_MODEL_ALIAS_FILE", path)
	if _, err := Read(); !os.IsNotExist(err) {
		t.Fatalf("a configured but missing file must error, got %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"revision":7,"aliases":[{"from":"a","to":"b"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Read()
	if err != nil || cfg.Revision != 7 || cfg.Map()["a"] != "b" {
		t.Fatalf("rewritten file must take effect on the next read, got %+v err=%v", cfg, err)
	}
	if err := os.WriteFile(path, []byte(`{"revision":8,"aliases":"nope"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(); err == nil {
		t.Errorf("an invalid configured file must error")
	}
}
