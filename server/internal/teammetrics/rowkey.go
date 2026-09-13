package teammetrics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const rowKeyPrefix = "tmdm-v1"

func normalizeID(v *string) any {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	return s
}

// SkillIDFromLegacyKey maps daily_skill_metrics.skill_key (BINARY(32)) onto the
// UNSIGNED BIGINT skill_id column. The value is identity-only; ranking groups by public name.
func SkillIDFromLegacyKey(key []byte) int64 {
	var n uint64
	for i := 0; i < 8 && i < len(key); i++ {
		n = (n << 8) | uint64(key[i])
	}
	n &^= 1 << 63
	if n == 0 {
		n = 1
	}
	return int64(n)
}

// RowKey is SHA-256 hex of the canonical JSON array described in the static-metrics spec §17.1.
func RowKey(contributorKey, metricDate, kind string, agent, provider, model, currency *string, skillID *int64) (string, error) {
	var skill any
	if skillID != nil {
		skill = *skillID
	}
	payload := []any{
		rowKeyPrefix,
		contributorKey,
		metricDate,
		kind,
		normalizeID(agent),
		normalizeID(provider),
		normalizeID(model),
		normalizeID(currency),
		skill,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
