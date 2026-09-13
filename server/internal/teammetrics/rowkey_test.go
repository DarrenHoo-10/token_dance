package teammetrics

import "testing"

func TestSkillIDFromLegacyKeyFitsUnsigned(t *testing.T) {
	if SkillIDFromLegacyKey(nil) != 1 {
		t.Fatal("empty key must not be 0")
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = 0xff
	}
	got := SkillIDFromLegacyKey(key)
	if got <= 0 {
		t.Fatalf("must stay in BIGINT UNSIGNED positive range, got %d", got)
	}
	again := SkillIDFromLegacyKey(key)
	if again != got {
		t.Fatal("legacy skill id must be stable")
	}
}

func TestRowKeyStableGolden(t *testing.T) {
	agent, provider, model := "codex", "openai", "gpt-5"
	got, err := RowKey("tco_aaaaaaaaaaaaaaaaaaaaaaaaaa", "2026-09-11", KindUsage, &agent, &provider, &model, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RowKey("tco_aaaaaaaaaaaaaaaaaaaaaaaaaa", "2026-09-11", KindUsage, &agent, &provider, &model, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != again || len(got) != 64 {
		t.Fatalf("unstable or wrong length key %q", got)
	}
	empty := ""
	a, _ := RowKey("tco_aaaaaaaaaaaaaaaaaaaaaaaaaa", "2026-09-11", KindUsage, &empty, nil, nil, nil, nil)
	b, _ := RowKey("tco_aaaaaaaaaaaaaaaaaaaaaaaaaa", "2026-09-11", KindUsage, nil, nil, nil, nil, nil)
	if a != b {
		t.Fatalf("empty string must canonicalize to null: %s vs %s", a, b)
	}
}

func TestCacheHitRateM01(t *testing.T) {
	// A 90/100 + B 0/900 = 90/1000 = 9%, not the mean of 90% and 0%.
	rate, err := CacheHitRate("90", "1000")
	if err != nil {
		t.Fatal(err)
	}
	if rate == nil || *rate != "0.0900" {
		t.Fatalf("got %v want 0.0900", rate)
	}
	zero, err := CacheHitRate("0", "0")
	if err != nil {
		t.Fatal(err)
	}
	if zero != nil {
		t.Fatalf("denominator 0 must be null, got %v", *zero)
	}
	if _, err := CacheHitRate("2", "1"); err == nil {
		t.Fatal("read>input must reject")
	}
}
