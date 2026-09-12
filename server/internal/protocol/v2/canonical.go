package v2

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

var hashRootKeys = map[string]struct{}{
	"eventId": {}, "factKey": {}, "factRevision": {}, "schemaVersion": {},
	"metricSemanticsVersion": {}, "harnessId": {}, "eventType": {}, "occurredAt": {},
	"model": {}, "skill": {}, "sessionKey": {}, "turnKey": {}, "costScopeKey": {},
	"payload": {},
}

func compareCodePoints(a, b string) int {
	for len(a) > 0 || len(b) > 0 {
		ra, sa := utf8.DecodeRuneInString(a)
		rb, sb := utf8.DecodeRuneInString(b)
		if sa == 0 {
			return -1
		}
		if sb == 0 {
			return 1
		}
		if ra != rb {
			if ra < rb {
				return -1
			}
			return 1
		}
		a = a[sa:]
		b = b[sb:]
	}
	return 0
}

// Canonicalize encodes a JSON value with sorted object keys and no whitespace.
func Canonicalize(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "null", nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	case float64:
		if v != float64(int64(v)) {
			return "", fmt.Errorf("non-integer JSON numbers are not allowed")
		}
		return strconv.FormatInt(int64(v), 10), nil
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return "", fmt.Errorf("non-integer JSON numbers are not allowed")
		}
		if i < 0 {
			return "", fmt.Errorf("negative counts are not allowed")
		}
		return strconv.FormatInt(i, 10), nil
	case string:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	case []any:
		var b strings.Builder
		b.WriteByte('[')
		for i, item := range v {
			if i > 0 {
				b.WriteByte(',')
			}
			part, err := Canonicalize(item)
			if err != nil {
				return "", err
			}
			b.WriteString(part)
		}
		b.WriteByte(']')
		return b.String(), nil
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool { return compareCodePoints(keys[i], keys[j]) < 0 })
		var b strings.Builder
		b.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			kb, err := json.Marshal(key)
			if err != nil {
				return "", err
			}
			b.Write(kb)
			b.WriteByte(':')
			part, err := Canonicalize(v[key])
			if err != nil {
				return "", err
			}
			b.WriteString(part)
		}
		b.WriteByte('}')
		return b.String(), nil
	default:
		return "", fmt.Errorf("unsupported JSON type %T", value)
	}
}

func matchesUint64(text string) bool {
	if text == "" || len(text) > 20 {
		return false
	}
	if text == "0" {
		return true
	}
	if text[0] < '1' || text[0] > '9' {
		return false
	}
	for i := 1; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func assertUint64String(value any, path string) error {
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("%s must be an unsigned decimal string", path)
	}
	if strings.HasPrefix(text, "-") {
		return fmt.Errorf("negative counts are not allowed: %s", path)
	}
	if !matchesUint64(text) {
		return fmt.Errorf("%s must be an unsigned decimal string", path)
	}
	return nil
}

func projectForHash(event map[string]any) (map[string]any, error) {
	projected := map[string]any{}
	for key, value := range event {
		if key == "contentHash" {
			continue
		}
		if _, ok := hashRootKeys[key]; !ok {
			return nil, fmt.Errorf("unknown business field: %s", key)
		}
		if value == nil {
			continue
		}
		switch key {
		case "skill":
			skill, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("skill must be an object")
			}
			skillKey, ok := skill["skillKey"]
			if !ok {
				return nil, fmt.Errorf("skill.skillKey is required")
			}
			projected["skill"] = map[string]any{"skillKey": skillKey}
		case "model":
			model, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("model must be an object")
			}
			out := map[string]any{}
			if provider, ok := model["providerId"]; ok {
				out["providerId"] = provider
			}
			if modelID, ok := model["modelId"]; ok {
				out["modelId"] = modelID
			}
			projected["model"] = out
		case "payload":
			payload, err := projectPayload(value)
			if err != nil {
				return nil, err
			}
			projected["payload"] = payload
		case "factRevision", "occurredAt":
			if err := assertUint64String(value, key); err != nil {
				return nil, err
			}
			projected[key] = value
		default:
			projected[key] = value
		}
	}
	if _, ok := projected["payload"]; !ok {
		return nil, fmt.Errorf("payload is required")
	}
	return projected, nil
}

func projectPayload(value any) (map[string]any, error) {
	payload, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("payload must be an object")
	}
	out := map[string]any{}
	for key, child := range payload {
		if child == nil {
			continue
		}
		switch key {
		case "usage":
			usage, err := projectCounts(child, []string{
				"token_total", "input_context_tokens", "output_tokens", "cache_read_tokens",
				"cache_write_tokens", "reasoning_tokens", "request_count",
			}, "usage.")
			if err != nil {
				return nil, err
			}
			out["usage"] = usage
		case "cost":
			cost, err := projectCost(child)
			if err != nil {
				return nil, err
			}
			out["cost"] = cost
		case "code":
			code, err := projectCounts(child, []string{
				"generated", "accepted", "added", "removed", "file_touch_count",
			}, "code.")
			if err != nil {
				return nil, err
			}
			out["code"] = code
		case "activity":
			activity, err := projectActivity(child)
			if err != nil {
				return nil, err
			}
			out["activity"] = activity
		case "context":
			out["context"] = child
		case "meta":
			meta, ok := child.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("meta must be an object")
			}
			metaOut := map[string]any{}
			for mk, mv := range meta {
				switch mk {
				case "accuracy", "time_source", "safe_tags":
					if mv != nil {
						metaOut[mk] = mv
					}
				default:
					return nil, fmt.Errorf("unknown business field: meta.%s", mk)
				}
			}
			out["meta"] = metaOut
		default:
			return nil, fmt.Errorf("unknown business field: payload.%s", key)
		}
	}
	return out, nil
}

func projectCounts(value any, allowed []string, path string) (map[string]any, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an object", strings.TrimSuffix(path, "."))
	}
	allow := map[string]struct{}{}
	for _, key := range allowed {
		allow[key] = struct{}{}
	}
	out := map[string]any{}
	for key, child := range obj {
		if _, ok := allow[key]; !ok {
			return nil, fmt.Errorf("unknown business field: %s%s", path, key)
		}
		if child == nil {
			continue
		}
		if err := assertUint64String(child, path+key); err != nil {
			return nil, err
		}
		out[key] = child
	}
	return out, nil
}

func projectCost(value any) (map[string]any, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("cost must be an object")
	}
	out := map[string]any{}
	for key, child := range obj {
		switch key {
		case "units", "currency", "source", "price_basis_id", "coverage":
			if child == nil {
				continue
			}
			if key == "units" {
				if err := assertUint64String(child, "cost.units"); err != nil {
					return nil, err
				}
			}
			out[key] = child
		default:
			return nil, fmt.Errorf("unknown business field: cost.%s", key)
		}
	}
	return out, nil
}

func projectActivity(value any) (map[string]any, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("activity must be an object")
	}
	out := map[string]any{}
	for key, child := range obj {
		switch key {
		case "duration_ms", "success", "trigger", "reason", "tool_category":
			if child == nil {
				continue
			}
			if key == "duration_ms" {
				if err := assertUint64String(child, "activity.duration_ms"); err != nil {
					return nil, err
				}
			}
			out[key] = child
		default:
			return nil, fmt.Errorf("unknown business field: activity.%s", key)
		}
	}
	return out, nil
}

// ContentHashCanonicalJSON returns the canonical JSON used for content_hash.
func ContentHashCanonicalJSON(event map[string]any) (string, error) {
	projected, err := projectForHash(event)
	if err != nil {
		return "", err
	}
	return Canonicalize(projected)
}

// ComputeContentHash returns base64url(SHA-256(canonical business envelope)).
func ComputeContentHash(event map[string]any) (string, error) {
	canonical, err := ContentHashCanonicalJSON(event)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

// ClassifyAck returns accepted/duplicate/conflict for an existing hash comparison.
func ClassifyAck(existingHash *string, candidateHash string) string {
	if existingHash == nil {
		return "accepted"
	}
	if *existingHash == candidateHash {
		return "duplicate"
	}
	return "conflict"
}

// RejectDuplicateKeys scans raw JSON and rejects objects with repeated keys.
func RejectDuplicateKeys(text string) error {
	var walk func(s string, i *int) error
	skipWS := func(s string, i *int) {
		for *i < len(s) {
			switch s[*i] {
			case ' ', '\t', '\n', '\r':
				*i++
			default:
				return
			}
		}
	}
	parseString := func(s string, i *int) (string, error) {
		if *i >= len(s) || s[*i] != '"' {
			return "", fmt.Errorf("expected string")
		}
		*i++
		start := *i
		for *i < len(s) {
			switch s[*i] {
			case '"':
				out := s[start:*i]
				*i++
				return out, nil
			case '\\':
				*i += 2
			default:
				*i++
			}
		}
		return "", fmt.Errorf("unterminated string")
	}
	walk = func(s string, i *int) error {
		skipWS(s, i)
		if *i >= len(s) {
			return fmt.Errorf("unexpected eof")
		}
		switch s[*i] {
		case '{':
			*i++
			skipWS(s, i)
			seen := map[string]struct{}{}
			if *i < len(s) && s[*i] == '}' {
				*i++
				return nil
			}
			for {
				skipWS(s, i)
				key, err := parseString(s, i)
				if err != nil {
					return err
				}
				if _, ok := seen[key]; ok {
					return fmt.Errorf("duplicate key %q", key)
				}
				seen[key] = struct{}{}
				skipWS(s, i)
				if *i >= len(s) || s[*i] != ':' {
					return fmt.Errorf("expected ':'")
				}
				*i++
				if err := walk(s, i); err != nil {
					return err
				}
				skipWS(s, i)
				if *i < len(s) && s[*i] == ',' {
					*i++
					continue
				}
				if *i < len(s) && s[*i] == '}' {
					*i++
					return nil
				}
				return fmt.Errorf("expected ',' or '}'")
			}
		case '[':
			*i++
			skipWS(s, i)
			if *i < len(s) && s[*i] == ']' {
				*i++
				return nil
			}
			for {
				if err := walk(s, i); err != nil {
					return err
				}
				skipWS(s, i)
				if *i < len(s) && s[*i] == ',' {
					*i++
					continue
				}
				if *i < len(s) && s[*i] == ']' {
					*i++
					return nil
				}
				return fmt.Errorf("expected ',' or ']'")
			}
		case '"':
			_, err := parseString(s, i)
			return err
		case 't':
			*i += 4
			return nil
		case 'f':
			*i += 5
			return nil
		case 'n':
			*i += 4
			return nil
		case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
			for *i < len(s) {
				c := s[*i]
				if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
					*i++
					continue
				}
				break
			}
			return nil
		default:
			return fmt.Errorf("unexpected token %q", s[*i])
		}
	}
	i := 0
	if err := walk(text, &i); err != nil {
		return err
	}
	skipWS(text, &i)
	if i != len(text) {
		return fmt.Errorf("trailing content")
	}
	return nil
}

// DecodeEventMap parses JSON into a generic map using UseNumber semantics for integers.
func DecodeEventMap(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("event must be an object")
	}
	return obj, nil
}
