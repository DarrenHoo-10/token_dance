package teams

import (
	"encoding/json"
	"strconv"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/teammetrics"
)

type rollingHourBucket struct {
	StartMs   string `json:"startMs"`
	Exact     string `json:"exact"`
	Derived   string `json:"derived"`
	CodeLines string `json:"codeLines,omitempty"`
}

type rollingHourly struct {
	SchemaVersion        int                 `json:"schemaVersion"`
	Coverage             string              `json:"coverage"`
	UnbucketedTokenTotal string              `json:"unbucketedTokenTotal"`
	Buckets              []rollingHourBucket `json:"buckets"`
}

// Team day rows are authoritative for membership and sharing. The rolling
// window may use only their attributed hour buckets; a day total cannot be
// assigned to a narrower period without inventing when it happened.
func ClipTeamRowsToRollingHours(rows []domain.TeamAnalysisRow, from, toExclusive time.Time) ([]domain.TeamAnalysisRow, bool) {
	out := make([]domain.TeamAnalysisRow, 0, len(rows))
	partial := false
	for _, row := range rows {
		if row.MetricKind != teammetrics.KindUsage && row.MetricKind != teammetrics.KindActivity {
			continue
		}
		var hourly rollingHourly
		if len(row.HourlyJSON) == 0 || json.Unmarshal(row.HourlyJSON, &hourly) != nil || hourly.SchemaVersion != 1 {
			if row.TokenExactTotal != "0" || row.TokenDerivedTotal != "0" || generatedCodeLineSum(row.ActivityJSON) != "0" {
				partial = true
			}
			continue
		}
		selected := make([]rollingHourBucket, 0, len(hourly.Buckets))
		allExact, allDerived, allCode := "0", "0", "0"
		exact, derived, code := "0", "0", "0"
		for _, bucket := range hourly.Buckets {
			ms, err := strconv.ParseInt(bucket.StartMs, 10, 64)
			if err != nil {
				partial = true
				continue
			}
			allExact = AddIntDecimal(allExact, emptyZero(bucket.Exact))
			allDerived = AddIntDecimal(allDerived, emptyZero(bucket.Derived))
			allCode = AddIntDecimal(allCode, emptyZero(bucket.CodeLines))
			instant := time.UnixMilli(ms).UTC()
			if instant.Before(from) || !instant.Before(toExclusive) {
				continue
			}
			selected = append(selected, bucket)
			exact = AddIntDecimal(exact, emptyZero(bucket.Exact))
			derived = AddIntDecimal(derived, emptyZero(bucket.Derived))
			code = AddIntDecimal(code, emptyZero(bucket.CodeLines))
		}
		if hourly.Coverage != "complete" || emptyZero(hourly.UnbucketedTokenTotal) != "0" ||
			(row.MetricKind == teammetrics.KindUsage && (allExact != emptyZero(row.TokenExactTotal) || allDerived != emptyZero(row.TokenDerivedTotal))) ||
			(row.MetricKind == teammetrics.KindActivity && allCode != generatedCodeLineSum(row.ActivityJSON)) {
			partial = true
		}
		if len(selected) == 0 {
			continue
		}
		row.HourlyJSON, _ = json.Marshal(rollingHourly{SchemaVersion: 1, Coverage: hourly.Coverage, UnbucketedTokenTotal: "0", Buckets: selected})
		if row.MetricKind == teammetrics.KindUsage {
			row.TokenExactTotal, row.TokenDerivedTotal = exact, derived
			// Event counts and resource splits currently exist only at day grain.
			// Leave them unavailable instead of showing a full day's values.
			row.UsageEventCount, row.TokenSupportedEventCount = "0", "0"
			row.ResourcesJSON = nil
		} else {
			row.ActivityJSON, _ = json.Marshal(map[string]any{"generatedCodeLines": map[string]string{"sum": code}})
		}
		out = append(out, row)
	}
	return out, partial
}
