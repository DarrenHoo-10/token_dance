package httpapi

import (
	"strings"
	"testing"
)

func TestNormalizeProviderAndModelIdentifiers(t *testing.T) {
	for _, tc := range []struct {
		provider, model string
		valid           bool
	}{
		{"builtin:zai-start-plan", "GLM-5.3-Flash", true},
		{"builtin:bigmodel-coding-plan", "GLM-5.3", true},
		{"openai", "gpt-5.5", true},
		{"provider", "user prompt text", false},
		{"https://example.com", "model", false},
		{"provider", `C:\Users\private`, false},
		{"ghp_secret", "model", false},
		{"provider", "sk-" + strings.Repeat("a", 30), false},
		{strings.Repeat("a", 65), "model", false},
		{"provider", strings.Repeat("a", 129), false},
	} {
		t.Run(tc.provider+"/"+tc.model, func(t *testing.T) {
			event := TelemetryEventInput{EventID: strings.Repeat("a", 64), AdapterID: "zcode", AdapterVersion: "1.0.0", AgentID: "zcode", EventType: "model_usage_recorded", Accuracy: "exact", SourceKind: "jsonl", OccurredAt: "2026-09-06T00:00:00Z", ProviderID: &tc.provider, ModelID: &tc.model}
			got, code := normalizeTelemetryEvent(&event)
			if (code == "") != tc.valid {
				t.Fatalf("valid=%v, code=%q", tc.valid, code)
			}
			if tc.valid && (*got.ProviderID != tc.provider || *got.ModelID != tc.model) {
				t.Fatal("external identifier spelling changed")
			}
		})
	}
}
