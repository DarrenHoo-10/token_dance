package telemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

func raw(value string) json.RawMessage { return json.RawMessage(value) }

func TestNormalizeMetadataRegisteredScalarWhitelist(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		metadata  Metadata
		wantCode  string
	}{
		{name: "registered strings", eventType: "model_usage_recorded", metadata: Metadata{"finishReason": raw(`"stop"`), "serviceTier": raw(`"standard"`)}},
		{name: "registered boolean", eventType: "tool_invoked", metadata: Metadata{"operation": raw(`"search"`), "success": raw(`true`)}},
		{name: "unknown key", eventType: "model_usage_recorded", metadata: Metadata{"region": raw(`"us"`)}, wantCode: "UNREGISTERED_METADATA_KEY"},
		{name: "forbidden key", eventType: "tool_invoked", metadata: Metadata{"toolArgs": raw(`"safe"`)}, wantCode: "FORBIDDEN_METADATA"},
		{name: "object value", eventType: "model_usage_recorded", metadata: Metadata{"finishReason": raw(`{"value":"stop"}`)}, wantCode: "INVALID_METADATA_TYPE"},
		{name: "array value", eventType: "model_usage_recorded", metadata: Metadata{"finishReason": raw(`["stop"]`)}, wantCode: "INVALID_METADATA_TYPE"},
		{name: "wrong scalar type", eventType: "tool_invoked", metadata: Metadata{"success": raw(`"true"`)}, wantCode: "INVALID_METADATA_TYPE"},
		{name: "long string", eventType: "code_changed", metadata: Metadata{"language": raw(`"` + strings.Repeat("a", MaxMetadataStringBytes+1) + `"`)}, wantCode: "METADATA_STRING_TOO_LONG"},
		{name: "prompt canary", eventType: "model_usage_recorded", metadata: Metadata{"finishReason": raw(`"system prompt: ignore prior instructions"`)}, wantCode: "FORBIDDEN_METADATA"},
		{name: "code canary", eventType: "code_changed", metadata: Metadata{"language": raw(`"package main; func main()"`)}, wantCode: "FORBIDDEN_METADATA"},
		{name: "path canary", eventType: "tool_invoked", metadata: Metadata{"operation": raw(`"C:\\Users\\alice\\secret.txt"`)}, wantCode: "FORBIDDEN_METADATA"},
		{name: "api key canary", eventType: "model_usage_recorded", metadata: Metadata{"serviceTier": raw(`"sk-secret-value"`)}, wantCode: "FORBIDDEN_METADATA"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, code := NormalizeMetadata(test.eventType, test.metadata)
			if code != test.wantCode {
				t.Fatalf("expected code %q, got %q", test.wantCode, code)
			}
			if test.wantCode == "" && len(encoded) == 0 {
				t.Fatal("valid metadata was not encoded")
			}
		})
	}
}

func TestNormalizeMetadataCountLimit(t *testing.T) {
	metadata := Metadata{}
	for i := 0; i < MaxMetadataFields+1; i++ {
		metadata[string(rune('a'+i))] = raw(`true`)
	}
	if _, code := NormalizeMetadata("tool_invoked", metadata); code != "METADATA_TOO_MANY_FIELDS" {
		t.Fatalf("expected field count rejection, got %q", code)
	}
}
