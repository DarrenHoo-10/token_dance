package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEventPayloadStrictDecodeAndRoundTrip(t *testing.T) {
	input := `{"type":"model_usage_recorded","providerId":"mock-provider","modelId":"mock-model","tokens":{"inputTokens":"9007199254740993","totalTokens":"9007199254740993"}}`
	var payload EventPayload
	if err := json.Unmarshal([]byte(input), &payload); err != nil {
		t.Fatalf("decode valid payload: %v", err)
	}
	model, ok := payload.Value.(ModelUsageRecordedPayload)
	if !ok {
		t.Fatalf("decoded payload type = %T", payload.Value)
	}
	if model.Tokens.TotalTokens == nil || string(*model.Tokens.TotalTokens) != "9007199254740993" {
		t.Fatalf("uint64 string lost precision: %#v", model.Tokens.TotalTokens)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	if !strings.Contains(string(encoded), `"totalTokens":"9007199254740993"`) {
		t.Fatalf("round trip changed wire value: %s", encoded)
	}
}

func TestEventPayloadRejectsUnknownFields(t *testing.T) {
	input := `{"type":"model_usage_recorded","providerId":"mock-provider","modelId":"mock-model","tokens":{"totalTokens":"1"},"prompt":"TOKSHOW_TEST_PROMPT_SECRET"}`
	var payload EventPayload
	if err := json.Unmarshal([]byte(input), &payload); err == nil {
		t.Fatal("unknown sensitive field was accepted")
	}
}

func TestEventPayloadRejectsTrailingJSON(t *testing.T) {
	input := `{"type":"session_started"}{"type":"session_started"}`
	var payload EventPayload
	if err := json.Unmarshal([]byte(input), &payload); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}
