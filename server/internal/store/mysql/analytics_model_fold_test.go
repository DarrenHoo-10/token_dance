package mysql

import "testing"

func TestBaseModelKeyFoldsSpellingVariants(t *testing.T) {
	cases := []struct {
		id   string
		want string
	}{
		{"GLM-5.3-Flash", "glm-5.3-flash"},
		{"glm-5.3-flash", "glm-5.3-flash"},
		{"accounts/fireworks/models/glm-5p3-flash", "glm-5.3-flash"},
		{"accounts/fireworks/models/llama-v3p1-40b-instruct", "llama-v3.1-40b-instruct"},
		{"preview", "preview"},
		{"kmodel_latest", "kmodel_latest"},
		{"  GPT-4o  ", "gpt-4o"},
		{"openrouter/gpt-4o", "openrouter/gpt-4o"},
		{"", ""},
	}
	for _, c := range cases {
		if got := baseModelKey(c.id); got != c.want {
			t.Errorf("baseModelKey(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestFoldModelVariantsSumsAndKeepsDominantName(t *testing.T) {
	folded := foldModelVariants([]rawModelItem{
		{modelID: "GLM-5.3-Flash", tokens: 50},
		{modelID: "accounts/fireworks/models/glm-5p3-flash", tokens: 1},
		{modelID: "glm-5.3-flash", tokens: 7},
		{modelID: "glm-5.3-flash-lite", tokens: 2},
	}, nil)
	if len(folded) != 2 {
		t.Fatalf("expected 2 folded rows, got %d: %+v", len(folded), folded)
	}
	byID := make(map[string]uint64, len(folded))
	for _, item := range folded {
		byID[item.modelID] = item.tokens
	}
	if byID["GLM-5.3-Flash"] != 58 {
		t.Errorf("dominant variant should carry the folded total 58, got %+v", byID)
	}
	if byID["glm-5.3-flash-lite"] != 2 {
		t.Errorf("a distinct model must stay separate, got %+v", byID)
	}

	tied := foldModelVariants([]rawModelItem{
		{modelID: "glm-5.3-flash", tokens: 5},
		{modelID: "GLM-5.3-Flash", tokens: 5},
	}, nil)
	if len(tied) != 1 || tied[0].modelID != "GLM-5.3-Flash" || tied[0].tokens != 10 {
		t.Errorf("ties must fold onto the lexicographically smallest id, got %+v", tied)
	}

	solo := foldModelVariants([]rawModelItem{
		{modelID: "accounts/fireworks/models/deepseek-v4p1-flash", tokens: 9},
	}, nil)
	if len(solo) != 1 || solo[0].modelID != "deepseek-v4.1-flash" || solo[0].tokens != 9 {
		t.Errorf("a solo provider path must display its folded base model, got %+v", solo)
	}

	dominantPath := foldModelVariants([]rawModelItem{
		{modelID: "accounts/fireworks/models/glm-5p3-flash", tokens: 100},
		{modelID: "GLM-5.3-Flash", tokens: 1},
	}, nil)
	if len(dominantPath) != 1 || dominantPath[0].modelID != "glm-5.3-flash" || dominantPath[0].tokens != 101 {
		t.Errorf("a dominant provider path must display its folded base model, got %+v", dominantPath)
	}
}

func TestFoldModelVariantsAppliesDeploymentAliases(t *testing.T) {
	aliases := map[string]string{
		"deepseek-v4p1-flash": "deepseek-v4.1-flash",
		"kimi-k3-thinking@x":  "kimi-k3-thinking",
		"gpt-6-mini":          "gpt-6-mini-internal",
		"preview":             "preview-model",
	}
	aliased := foldModelVariants([]rawModelItem{
		{modelID: "accounts/fireworks/models/deepseek-v4p1-flash", tokens: 12},
		{modelID: "Kimi-K3-Thinking@X", tokens: 3},
		{modelID: "gpt-6-mini", tokens: 7},
		{modelID: "preview", tokens: 1},
	}, aliases)
	if len(aliased) != 4 {
		t.Fatalf("alias rules must not merge unrelated models, got %+v", aliased)
	}
	byID := make(map[string]uint64, len(aliased))
	for _, item := range aliased {
		byID[item.modelID] = item.tokens
	}
	// The p-encoded path form matches through its final segment and the dot
	// decode; the whole-id and plain-segment rules match exactly.
	if byID["deepseek-v4.1-flash"] != 12 || byID["kimi-k3-thinking"] != 3 {
		t.Errorf("alias rules must rename their matches, got %+v", byID)
	}
	if byID["gpt-6-mini-internal"] != 7 || byID["preview-model"] != 1 {
		t.Errorf("plain-spelling rules must apply verbatim, got %+v", byID)
	}

	// An alias takes precedence over the built-in fold: without the rule the
	// spelling folds onto glm-5.3-flash, with it the canonical name wins.
	overridden := foldModelVariants([]rawModelItem{
		{modelID: "accounts/fireworks/models/glm-5p3-flash", tokens: 5},
	}, map[string]string{"glm-5p3-flash": "glm-5.3-flash-cn"})
	if len(overridden) != 1 || overridden[0].modelID != "glm-5.3-flash-cn" || overridden[0].tokens != 5 {
		t.Errorf("a matching alias must win over the built-in fold, got %+v", overridden)
	}
}
