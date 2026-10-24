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
	})
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
	})
	if len(tied) != 1 || tied[0].modelID != "GLM-5.3-Flash" || tied[0].tokens != 10 {
		t.Errorf("ties must fold onto the lexicographically smallest id, got %+v", tied)
	}
}
