package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	MaxMetadataFields      = 8
	MaxMetadataKeyBytes    = 32
	MaxMetadataStringBytes = 128
)

type Metadata map[string]json.RawMessage

type scalarKind uint8

const (
	scalarString scalarKind = iota + 1
	scalarBool
	scalarNumber
)

var metadataRegistry = map[string]map[string]scalarKind{
	"session_started":      {"sessionMode": scalarString, "trigger": scalarString},
	"session_ended":        {"endReason": scalarString},
	"turn_started":         {"trigger": scalarString},
	"turn_completed":       {"finishReason": scalarString},
	"model_usage_recorded": {"finishReason": scalarString, "serviceTier": scalarString},
	"tool_invoked":         {"operation": scalarString, "success": scalarBool},
	"skill_invoked":        {"invocationSource": scalarString},
	"code_changed":         {"language": scalarString},
	"cost_recorded":        {"billingCategory": scalarString, "estimated": scalarBool},
	"agent_spawned":        {"spawnReason": scalarString},
}

var forbiddenKeyCanaries = []string{
	"prompt", "response", "reasoning", "code", "snippet", "toolargs", "argument",
	"environment", "envvar", "file", "path", "token", "apikey", "api_key", "secret", "credential",
}

var forbiddenValueCanaries = []string{
	"system prompt", "user prompt", "assistant response", "chain of thought", "reasoning:",
	"api_key=", "api-key:", "apikey:", "authorization: bearer ", "-----begin private key-----",
	"/users/", "/home/", "c:\\users\\", "../", "..\\", "package main", "func main(",
	"def main(", "public static void main(", "console.log(", "#!/bin/",
}

func NormalizeMetadata(eventType string, metadata Metadata) ([]byte, string) {
	if len(metadata) == 0 {
		return nil, ""
	}
	if len(metadata) > MaxMetadataFields {
		return nil, "METADATA_TOO_MANY_FIELDS"
	}
	registered := metadataRegistry[eventType]
	for key, raw := range metadata {
		if key == "" || len(key) > MaxMetadataKeyBytes || !utf8.ValidString(key) {
			return nil, "INVALID_METADATA_KEY"
		}
		keyCanary := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
		for _, forbidden := range forbiddenKeyCanaries {
			if strings.Contains(keyCanary, forbidden) {
				return nil, "FORBIDDEN_METADATA"
			}
		}
		kind, ok := registered[key]
		if !ok {
			return nil, "UNREGISTERED_METADATA_KEY"
		}
		if code := validateScalar(raw, kind); code != "" {
			return nil, code
		}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, "INVALID_METADATA"
	}
	return encoded, ""
}

func ValidateSafeExtensionJSON(eventType string, encoded []byte) error {
	if len(encoded) == 0 {
		return nil
	}
	var metadata Metadata
	dec := json.NewDecoder(bytes.NewReader(encoded))
	if err := dec.Decode(&metadata); err != nil {
		return fmt.Errorf("invalid safe extension JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("safe extension JSON must contain exactly one value")
	}
	normalized, code := NormalizeMetadata(eventType, metadata)
	if code != "" {
		return fmt.Errorf("unsafe telemetry metadata: %s", code)
	}
	if len(normalized) == 0 {
		return fmt.Errorf("safe extension JSON must not encode an empty object")
	}
	return nil
}

func validateScalar(raw json.RawMessage, expected scalarKind) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value interface{}
	if err := dec.Decode(&value); err != nil {
		return "INVALID_METADATA"
	}
	if dec.More() {
		return "INVALID_METADATA"
	}
	switch typed := value.(type) {
	case string:
		if expected != scalarString {
			return "INVALID_METADATA_TYPE"
		}
		if len(typed) > MaxMetadataStringBytes || !utf8.ValidString(typed) {
			return "METADATA_STRING_TOO_LONG"
		}
		lower := strings.ToLower(typed)
		for _, canary := range forbiddenValueCanaries {
			if strings.Contains(lower, canary) {
				return "FORBIDDEN_METADATA"
			}
		}
		if strings.HasPrefix(lower, "sk-") || strings.HasPrefix(lower, "ghp_") || strings.HasPrefix(lower, "xoxb-") {
			return "FORBIDDEN_METADATA"
		}
	case bool:
		if expected != scalarBool {
			return "INVALID_METADATA_TYPE"
		}
	case json.Number:
		if expected != scalarNumber {
			return "INVALID_METADATA_TYPE"
		}
	default:
		return "INVALID_METADATA_TYPE"
	}
	return ""
}
