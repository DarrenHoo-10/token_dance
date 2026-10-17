package pricing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRatesCacheTiersAndUnknown(t *testing.T) {
	var c Catalog
	if err := json.Unmarshal([]byte(`{"data":[{"id":"openai/test","pricing":{"prompt":"0.000002","completion":"0.00001","input_cache_read":"0.0000002","overrides":[{"min_prompt_tokens":200000,"prompt":"0.000004","completion":"0.000015","input_cache_read":"0.0000004"}]}}]}`), &c); err != nil {
		t.Fatal(err)
	}
	m, ok := c.Match("TEST")
	if !ok {
		t.Fatal("exact suffix match failed")
	}
	for _, tc := range []struct {
		usage Usage
		want  string
	}{{Usage{Input: 1000, Output: 100, CacheRead: 800, InputIncludesCache: true}, "0.00156000"}, {Usage{Input: 200000, Output: 100, CacheRead: 100000, InputIncludesCache: true}, "0.44150000"}, {Usage{Input: 200, Output: 100, CacheRead: 800}, "0.00156000"}} {
		got, ok := Estimate(m, tc.usage)
		if !ok || got != tc.want {
			t.Fatalf("%+v: %q, want %s", tc.usage, got, tc.want)
		}
	}
	if _, ok := c.Match("test-high"); ok {
		t.Fatal("unverified alias matched")
	}
	var grok Catalog
	if err := json.Unmarshal([]byte(`{"data":[{"id":"x-ai/grok-4.7","pricing":{"prompt":"0.000002","completion":"0.000006"}}]}`), &grok); err != nil {
		t.Fatal(err)
	}
	matched, ok := grok.Match("grok-4.7-build-fast")
	if !ok || matched.ID != "x-ai/grok-4.7" {
		t.Fatalf("grok-4.7-build-fast alias: %+v ok=%v", matched, ok)
	}
	if _, ok := grok.Match("grok-4.7-build"); ok {
		t.Fatal("unlisted grok-4.7 variant matched")
	}
	if _, ok := Estimate(m, Usage{Input: 1, CacheRead: 2, InputIncludesCache: true}); ok {
		t.Fatal("invalid cache count")
	}
	m.Pricing.Prompt = "-1"
	if _, ok := Estimate(m, Usage{Input: 2}); ok {
		t.Fatal("negative rate accepted")
	}
	m.Pricing = Rates{Prompt: "0", Completion: "0"}
	if cost, ok := Estimate(m, Usage{Input: 10}); !ok || cost != "0.00000000" {
		t.Fatal("explicit free price rejected")
	}
}
func TestCatalogCache(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("public fetch leaked credentials")
		}
		w.Write([]byte(`{"data":[{"id":"vendor/model","pricing":{"prompt":"1","completion":"2"}}]}`))
	}))
	defer s.Close()
	c := NewClient()
	c.URL = s.URL
	for i := 0; i < 2; i++ {
		if _, _, err := c.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("catalog not cached")
	}
}

func TestDeepSeekFlashAliasAndReferenceRates(t *testing.T) {
	var c Catalog
	if err := json.Unmarshal([]byte(`{"data":[{"id":"deepseek/deepseek-v4.1-flash","pricing":{"prompt":"0.00000015","completion":"0.0000006","input_cache_read":"0.000000003","overrides":[{"utc_start":0,"utc_end":100,"prompt":"0"},{"utc_days":["saturday"],"completion":"0"}]}}]}`), &c); err != nil {
		t.Fatal(err)
	}
	m, ok := c.Match("deepseek-flash")
	if !ok || m.ID != "deepseek/deepseek-v4.1-flash" {
		t.Fatalf("alias: %+v, %v", m, ok)
	}
	cost, ok := Estimate(m, Usage{Input: 1000, Output: 100, CacheRead: 800, InputIncludesCache: true})
	if !ok || cost != "0.00009240" {
		t.Fatalf("reference cost: %s, %v", cost, ok)
	}
	for _, id := range []string{"deepseek-flash-unknown", "deepseek-pro", "deepseek-v4-flash"} {
		if _, ok := c.Match(id); ok {
			t.Fatalf("unverified model matched: %s", id)
		}
	}
	c.Data = append(c.Data, Model{ID: "deepseek-flash", Pricing: Rates{Prompt: "0", Completion: "0"}})
	m, ok = c.Match("deepseek-flash")
	if !ok || m.ID != "deepseek-flash" {
		t.Fatal("exact catalog price must win")
	}
	if _, ok := (Catalog{}).Match("deepseek-flash"); ok {
		t.Fatal("missing catalog model matched")
	}
}
