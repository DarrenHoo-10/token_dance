package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
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
)

type fieldSpec struct {
	kind    scalarKind
	allowed map[string]struct{}
	pattern *regexp.Regexp
}

var (
	normalizedIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	versionPattern              = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+){0,3}(?:-[a-z0-9]+(?:[.-][a-z0-9]+)*)?$`)
	decimalAmountPattern        = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]{1,8})?$`)
	currencyPattern             = regexp.MustCompile(`^[A-Z]{3}$`)
)

func ValidIdentifier(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) && normalizedIdentifierPattern.MatchString(value)
}

func ValidVersion(value string) bool {
	return len(value) <= 32 && versionPattern.MatchString(value)
}

func ValidSkillInvokeType(value string) bool {
	switch value {
	case "explicit", "implicit", "automatic", "unknown":
		return true
	default:
		return false
	}
}

func ValidCostAmount(value string) bool {
	return len(value) <= 29 && decimalAmountPattern.MatchString(value)
}

func ValidCurrency(value string) bool {
	return currencyPattern.MatchString(value)
}

func ValidCostSource(value string) bool {
	return value == "provider_reported" || value == "estimated_price_table"
}

func enumSpec(values ...string) fieldSpec {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return fieldSpec{kind: scalarString, allowed: allowed}
}

func identifierSpec() fieldSpec {
	return fieldSpec{kind: scalarString, pattern: normalizedIdentifierPattern}
}

var metadataRegistry = map[string]map[string]fieldSpec{
	"session_started": {
		"sessionMode": enumSpec("interactive", "batch", "background", "daemon", "unknown"),
		"trigger":     enumSpec("user", "system", "automation", "resume", "unknown"),
	},
	"session_ended": {
		"endReason": enumSpec("completed", "cancelled", "error", "timeout", "shutdown", "unknown"),
	},
	"turn_started": {
		"trigger": enumSpec("user", "system", "automation", "resume", "unknown"),
	},
	"turn_completed": {
		"finishReason": enumSpec("stop", "length", "tool_call", "content_filter", "error", "cancelled", "unknown"),
	},
	"model_usage_recorded": {
		"finishReason": enumSpec("stop", "length", "tool_call", "content_filter", "error", "cancelled", "unknown"),
		"serviceTier":  enumSpec("default", "standard", "priority", "flex", "scale", "unknown"),
	},
	"tool_invoked": {
		"operation": identifierSpec(),
		"success":   {kind: scalarBool},
	},
	"skill_invoked": {
		"invocationSource": enumSpec("user", "model", "system", "automation", "unknown"),
	},
	"code_changed": {
		"language": identifierSpec(),
	},
	"cost_recorded": {
		"billingCategory": enumSpec("token_usage", "tool_usage", "subscription", "credit", "other"),
		"estimated":       {kind: scalarBool},
	},
	"agent_spawned": {
		"spawnReason": enumSpec("user_request", "delegation", "parallelism", "retry", "specialization", "unknown"),
	},
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
		spec, ok := registered[key]
		if !ok {
			return nil, "UNREGISTERED_METADATA_KEY"
		}
		if code := validateScalar(raw, spec); code != "" {
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

func validateScalar(raw json.RawMessage, spec fieldSpec) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var value interface{}
	if err := dec.Decode(&value); err != nil {
		return "INVALID_METADATA"
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return "INVALID_METADATA"
	}
	switch typed := value.(type) {
	case string:
		if spec.kind != scalarString {
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
		if spec.allowed != nil {
			if _, ok := spec.allowed[typed]; !ok {
				return "INVALID_METADATA_VALUE"
			}
		}
		if spec.pattern != nil && !spec.pattern.MatchString(typed) {
			return "INVALID_METADATA_VALUE"
		}
	case bool:
		if spec.kind != scalarBool {
			return "INVALID_METADATA_TYPE"
		}
	default:
		return "INVALID_METADATA_TYPE"
	}
	return ""
}
