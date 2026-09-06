package domain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type AggregateRow struct {
	Kind       string            `json:"kind"`
	AgentID    string            `json:"agentId"`
	ProviderID string            `json:"providerId"`
	ModelID    string            `json:"modelId"`
	SkillKey   string            `json:"skillKey"`
	SkillName  string            `json:"skillName"`
	Metrics    map[string]string `json:"metrics"`
}
type AggregateSnapshot struct {
	SchemaVersion int            `json:"schemaVersion"`
	Day           string         `json:"day"`
	Revision      int64          `json:"revision"`
	Rows          []AggregateRow `json:"rows"`
}
type AggregateCommit struct {
	Snapshot       AggregateSnapshot
	InstallationID string
	Digest         [32]byte
	NonceHash      [32]byte
	ReceivedAt     time.Time
}
type AggregateAck struct {
	Day      string `json:"day"`
	Revision int64  `json:"revision"`
	SHA256   string `json:"sha256"`
}

// Only these numeric fields reach SQL identifiers. Never use unvalidated
// client-supplied metric keys as column names.
var AggregateColumns = map[string][]string{
	"agent": {"exact_token_total", "derived_token_total", "estimated_token_total", "session_count", "child_session_count", "interaction_turn_count", "model_request_count", "tool_call_count", "skill_use_count", "code_generated_lines", "code_accepted_lines", "correlated_code_lines", "token_input_total", "token_output_total", "token_cache_read_total", "token_cache_write_total", "token_reasoning_total", "active_duration_ms", "message_count", "user_message_count", "cost_usd_units", "estimated_cost_usd_units"},
	"model": {"exact_token_total", "derived_token_total", "estimated_token_total", "model_request_count", "token_input_total", "token_output_total", "token_cache_read_total", "token_cache_write_total", "token_reasoning_total", "cost_usd_units", "estimated_cost_usd_units"},
	"skill": {"use_count", "exact_use_count", "derived_use_count", "correlated_use_count", "estimated_use_count", "success_count", "failure_count", "duration_ms"},
}

func (s AggregateSnapshot) Validate(now time.Time) error {
	day, err := time.Parse("2006-01-02", s.Day)
	if err != nil || day.Format("2006-01-02") != s.Day || day.After(now.UTC()) || s.SchemaVersion != 1 || s.Revision < 1 || len(s.Rows) == 0 || len(s.Rows) > 2000 {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, r := range s.Rows {
		columns, ok := AggregateColumns[r.Kind]
		if !ok || r.AgentID == "" || len(r.AgentID) > 64 || len(r.ProviderID) > 64 || len(r.ModelID) > 160 {
			return ErrInvalidArgument
		}
		for _, v := range []string{r.AgentID, r.ProviderID, r.ModelID, r.SkillKey} {
			if len(v) > 256 || strings.ContainsAny(v, "\x00\r\n") || !aggregateASCII(v) {
				return ErrInvalidArgument
			}
		}
		if len([]rune(r.SkillName)) > 120 || (r.Kind == "model" && (r.ModelID == "" || r.ProviderID == "")) || (r.Kind == "skill" && r.SkillKey == "") {
			return ErrInvalidArgument
		}
		if r.Kind == "skill" {
			hash, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(r.SkillKey, "hmac-sha256:"))
			if e != nil || len(hash) != 32 {
				return ErrInvalidArgument
			}
		}
		if r.Kind == "agent" && (r.ProviderID != "" || r.ModelID != "" || r.SkillKey != "" || r.SkillName != "") {
			return ErrInvalidArgument
		}
		if r.Kind == "model" && (r.SkillKey != "" || r.SkillName != "") {
			return ErrInvalidArgument
		}
		if r.Kind == "skill" && (r.ProviderID != "" || r.ModelID != "") {
			return ErrInvalidArgument
		}
		key, _ := json.Marshal([]string{r.Kind, r.AgentID, r.ProviderID, r.ModelID, r.SkillKey})
		if seen[string(key)] {
			return ErrInvalidArgument
		}
		seen[string(key)] = true
		for k, v := range r.Metrics {
			valid := false
			for _, c := range columns {
				if k == c {
					valid = true
					break
				}
			}
			n, e := strconv.ParseUint(v, 10, 64)
			if !valid || e != nil || strconv.FormatUint(n, 10) != v || (strings.Contains(k, "count") && n > 2147483647) {
				return fmt.Errorf("%w: aggregate metric", ErrInvalidArgument)
			}
		}
		if r.Kind == "skill" {
			count, _ := strconv.ParseUint(r.Metrics["use_count"], 10, 64)
			for _, keys := range [][]string{{"success_count", "failure_count"}, {"exact_use_count", "derived_use_count", "correlated_use_count", "estimated_use_count"}} {
				var sum uint64
				for _, key := range keys {
					n, _ := strconv.ParseUint(r.Metrics[key], 10, 64)
					sum += n
				}
				if sum > count {
					return ErrInvalidArgument
				}
			}
		}
	}
	return nil
}

func aggregateASCII(s string) bool {
	for _, b := range []byte(s) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
