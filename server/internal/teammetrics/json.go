package teammetrics

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

const qualitySchemaVersion = 1

func emptyJSONObject() json.RawMessage { return json.RawMessage(`{}`) }

type coveredSum struct {
	Sum      *string `json:"sum"`
	Known    string  `json:"known"`
	Observed string  `json:"observed"`
}

type cachePairs struct {
	Input    string `json:"input"`
	Read     string `json:"read"`
	Known    string `json:"known"`
	Observed string `json:"observed"`
}

type resourcesV1 struct {
	SchemaVersion      int        `json:"schemaVersion"`
	InputContextTokens coveredSum `json:"inputContextTokens"`
	OutputTokens       coveredSum `json:"outputTokens"`
	CachePairs         cachePairs `json:"cachePairs"`
}

type activityV1 struct {
	SchemaVersion      int        `json:"schemaVersion"`
	GeneratedCodeLines coveredSum `json:"generatedCodeLines"`
	ActiveDurationMs   coveredSum `json:"activeDurationMs"`
	MessageCount       coveredSum `json:"messageCount"`
	UserMessageCount   coveredSum `json:"userMessageCount"`
}

type hourlyBucket struct {
	StartMs   string `json:"startMs"`
	Exact     string `json:"exact"`
	Derived   string `json:"derived"`
	CodeLines string `json:"codeLines,omitempty"`
}

type hourlyV1 struct {
	SchemaVersion        int            `json:"schemaVersion"`
	Coverage             string         `json:"coverage"`
	UnbucketedTokenTotal string         `json:"unbucketedTokenTotal"`
	Buckets              []hourlyBucket `json:"buckets"`
}

type qualityV1 struct {
	SchemaVersion              int    `json:"schemaVersion"`
	TokenSupportedEventCount   string `json:"token_supported_event_count"`
	ReportedCostEventCount     string `json:"reported_cost_event_count"`
	EstimatedCostEventCount    string `json:"estimated_cost_event_count"`
	ReportedCoveredUsageCount  string `json:"reported_covered_usage_count"`
	EstimatedCoveredUsageCount string `json:"estimated_covered_usage_count"`
	UnattributedCostCount      string `json:"unattributed_cost_count"`
	LegacyAggregate            bool   `json:"legacy_aggregate"`
	CalendarBasis              string `json:"calendarBasis"`
	LegacyCoverage             string `json:"legacyCoverage"`
}

type skillStatsV1 struct {
	SchemaVersion        int     `json:"schemaVersion"`
	ExactCount           string  `json:"exactCount"`
	DerivedCount         string  `json:"derivedCount"`
	CorrelatedCount      string  `json:"correlatedCount"`
	EstimatedCount       string  `json:"estimatedCount"`
	UnknownAccuracyCount string  `json:"unknownAccuracyCount"`
	SuccessCount         string  `json:"successCount"`
	FailureCount         string  `json:"failureCount"`
	DurationMs           *string `json:"durationMs"`
	DurationKnownCount   string  `json:"durationKnownCount"`
	PublicName           string  `json:"publicName,omitempty"`
}

func DecodeSkillPublicName(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "{}" {
		return ""
	}
	var s skillStatsV1
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s.PublicName)
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return emptyJSONObject()
	}
	return b
}

func defaultQuality(legacy bool) json.RawMessage {
	basis := "team_local"
	cov := "known"
	if legacy {
		basis = "legacy_utc"
		cov = "unknown"
	}
	return mustJSON(qualityV1{
		SchemaVersion:              qualitySchemaVersion,
		TokenSupportedEventCount:   "0",
		ReportedCostEventCount:     "0",
		EstimatedCostEventCount:    "0",
		ReportedCoveredUsageCount:  "0",
		EstimatedCoveredUsageCount: "0",
		UnattributedCostCount:      "0",
		LegacyAggregate:            legacy,
		CalendarBasis:              basis,
		LegacyCoverage:             cov,
	})
}

func defaultHourly() json.RawMessage {
	return mustJSON(hourlyV1{SchemaVersion: 1, Coverage: "complete", UnbucketedTokenTotal: "0", Buckets: []hourlyBucket{}})
}

func defaultSkillStats() json.RawMessage {
	return mustJSON(skillStatsV1{
		SchemaVersion: 1, ExactCount: "0", DerivedCount: "0", CorrelatedCount: "0",
		EstimatedCount: "0", UnknownAccuracyCount: "0", SuccessCount: "0", FailureCount: "0",
		DurationKnownCount: "0",
	})
}

func parseNonNegInt(s string) (*big.Int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "0"
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("invalid non-negative integer %q", s)
	}
	return n, nil
}

// CacheHitRate is Σread / Σinput for complete cache pairs. Denominator 0 → nil.
func CacheHitRate(read, input string) (*string, error) {
	r, err := parseNonNegInt(read)
	if err != nil {
		return nil, err
	}
	i, err := parseNonNegInt(input)
	if err != nil {
		return nil, err
	}
	if i.Sign() == 0 {
		return nil, nil
	}
	if r.Cmp(i) > 0 {
		return nil, fmt.Errorf("cache read %s exceeds input %s", read, input)
	}
	// 4 decimal places, 0–1.
	scaled := new(big.Int).Mul(r, big.NewInt(10000))
	q := new(big.Int).Quo(scaled, i)
	s := fmt.Sprintf("0.%04d", q.Int64())
	if q.Int64() >= 10000 {
		s = "1.0000"
	}
	return &s, nil
}

// DecodeCachePairs extracts cache pair totals from a resources JSON object.
func DecodeCachePairs(raw json.RawMessage) (read, input string, err error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return "0", "0", nil
	}
	var res resourcesV1
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", "", err
	}
	if res.SchemaVersion != 0 && res.SchemaVersion != 1 {
		return "", "", fmt.Errorf("unsupported resources schemaVersion %d", res.SchemaVersion)
	}
	in := res.CachePairs.Input
	if in == "" {
		in = "0"
	}
	rd := res.CachePairs.Read
	if rd == "" {
		rd = "0"
	}
	return rd, in, nil
}

func validateJSONObject(raw json.RawMessage, name string) error {
	if len(raw) == 0 {
		return fmt.Errorf("%s JSON is empty", name)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("%s JSON: %w", name, err)
	}
	if _, ok := v.(map[string]any); !ok {
		return fmt.Errorf("%s JSON must be an object", name)
	}
	return nil
}

var maxDec30 = new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)

func isEmptyObject(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "{}"
}

func parseRequiredNonNeg(s, field string) (*big.Int, error) {
	if s == "" {
		return nil, fmt.Errorf("%s empty string is not allowed", field)
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || n.Sign() < 0 {
		return nil, fmt.Errorf("%s invalid non-negative integer %q", field, s)
	}
	if n.Cmp(maxDec30) >= 0 {
		return nil, fmt.Errorf("%s exceeds DECIMAL(30,0)", field)
	}
	return n, nil
}

func requireSchemaV1(raw json.RawMessage, name string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return fmt.Errorf("%s JSON: %w", name, err)
	}
	sv, ok := obj["schemaVersion"]
	if !ok {
		return fmt.Errorf("%s missing schemaVersion", name)
	}
	var v int
	if err := json.Unmarshal(sv, &v); err != nil {
		return fmt.Errorf("%s schemaVersion: %w", name, err)
	}
	if v != 1 {
		return fmt.Errorf("%s unsupported schemaVersion %d", name, v)
	}
	return nil
}

func validateCoveredSum(raw json.RawMessage, field string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return fmt.Errorf("%s missing", field)
	}
	var cs struct {
		Sum      *string `json:"sum"`
		Known    *string `json:"known"`
		Observed *string `json:"observed"`
	}
	if err := json.Unmarshal(raw, &cs); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if cs.Known == nil || cs.Observed == nil {
		return fmt.Errorf("%s missing known/observed", field)
	}
	known, err := parseRequiredNonNeg(*cs.Known, field+".known")
	if err != nil {
		return err
	}
	observed, err := parseRequiredNonNeg(*cs.Observed, field+".observed")
	if err != nil {
		return err
	}
	if known.Cmp(observed) > 0 {
		return fmt.Errorf("%s known exceeds observed", field)
	}
	if cs.Sum != nil {
		if _, err := parseRequiredNonNeg(*cs.Sum, field+".sum"); err != nil {
			return err
		}
	}
	return nil
}

// ValidateResourcesJSON accepts {} for unused families; otherwise requires resources v1.
func ValidateResourcesJSON(raw json.RawMessage) error {
	if err := validateJSONObject(raw, "resources"); err != nil {
		return err
	}
	if isEmptyObject(raw) {
		return nil
	}
	if err := requireSchemaV1(raw, "resources"); err != nil {
		return err
	}
	var res resourcesV1
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("resources JSON: %w", err)
	}
	inTok, _ := json.Marshal(res.InputContextTokens)
	outTok, _ := json.Marshal(res.OutputTokens)
	if err := validateCoveredSum(inTok, "resources.inputContextTokens"); err != nil {
		return err
	}
	if err := validateCoveredSum(outTok, "resources.outputTokens"); err != nil {
		return err
	}
	in, err := parseRequiredNonNeg(res.CachePairs.Input, "resources.cachePairs.input")
	if err != nil {
		return err
	}
	rd, err := parseRequiredNonNeg(res.CachePairs.Read, "resources.cachePairs.read")
	if err != nil {
		return err
	}
	if rd.Cmp(in) > 0 {
		return fmt.Errorf("resources cache read exceeds input")
	}
	if _, err := parseRequiredNonNeg(res.CachePairs.Known, "resources.cachePairs.known"); err != nil {
		return err
	}
	if _, err := parseRequiredNonNeg(res.CachePairs.Observed, "resources.cachePairs.observed"); err != nil {
		return err
	}
	return nil
}

// ValidateActivityJSON accepts {} for unused families; otherwise requires activity v1.
func ValidateActivityJSON(raw json.RawMessage) error {
	if err := validateJSONObject(raw, "activity"); err != nil {
		return err
	}
	if isEmptyObject(raw) {
		return nil
	}
	if err := requireSchemaV1(raw, "activity"); err != nil {
		return err
	}
	var act activityV1
	if err := json.Unmarshal(raw, &act); err != nil {
		return fmt.Errorf("activity JSON: %w", err)
	}
	fields := []struct {
		v    coveredSum
		name string
	}{
		{act.GeneratedCodeLines, "activity.generatedCodeLines"},
		{act.ActiveDurationMs, "activity.activeDurationMs"},
		{act.MessageCount, "activity.messageCount"},
		{act.UserMessageCount, "activity.userMessageCount"},
	}
	for _, f := range fields {
		b, _ := json.Marshal(f.v)
		if err := validateCoveredSum(b, f.name); err != nil {
			return err
		}
	}
	return nil
}

// ValidateHourlyJSON accepts {} when a fact family has no hour series.
func ValidateHourlyJSON(raw json.RawMessage) error {
	if err := validateJSONObject(raw, "hourly"); err != nil {
		return err
	}
	if isEmptyObject(raw) {
		return nil
	}
	if err := requireSchemaV1(raw, "hourly"); err != nil {
		return err
	}
	var h hourlyV1
	if err := json.Unmarshal(raw, &h); err != nil {
		return fmt.Errorf("hourly JSON: %w", err)
	}
	switch h.Coverage {
	case "complete", "partial", "none":
	default:
		return fmt.Errorf("hourly unknown coverage %q", h.Coverage)
	}
	if _, err := parseRequiredNonNeg(h.UnbucketedTokenTotal, "hourly.unbucketedTokenTotal"); err != nil {
		return err
	}
	if h.Buckets == nil {
		return fmt.Errorf("hourly missing buckets")
	}
	for i, b := range h.Buckets {
		if _, err := parseRequiredNonNeg(b.StartMs, fmt.Sprintf("hourly.buckets[%d].startMs", i)); err != nil {
			return err
		}
		if _, err := parseRequiredNonNeg(b.Exact, fmt.Sprintf("hourly.buckets[%d].exact", i)); err != nil {
			return err
		}
		if _, err := parseRequiredNonNeg(b.Derived, fmt.Sprintf("hourly.buckets[%d].derived", i)); err != nil {
			return err
		}
		if b.CodeLines != "" {
			if _, err := parseRequiredNonNeg(b.CodeLines, fmt.Sprintf("hourly.buckets[%d].codeLines", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateQualityJSON rejects missing keys, unknown versions, and invalid counts.
func ValidateQualityJSON(raw json.RawMessage) error {
	if err := validateJSONObject(raw, "quality"); err != nil {
		return err
	}
	if isEmptyObject(raw) {
		return fmt.Errorf("quality JSON missing required keys")
	}
	if err := requireSchemaV1(raw, "quality"); err != nil {
		return err
	}
	var q qualityV1
	if err := json.Unmarshal(raw, &q); err != nil {
		return fmt.Errorf("quality JSON: %w", err)
	}
	counts := []struct{ v, name string }{
		{q.TokenSupportedEventCount, "quality.token_supported_event_count"},
		{q.ReportedCostEventCount, "quality.reported_cost_event_count"},
		{q.EstimatedCostEventCount, "quality.estimated_cost_event_count"},
		{q.ReportedCoveredUsageCount, "quality.reported_covered_usage_count"},
		{q.EstimatedCoveredUsageCount, "quality.estimated_covered_usage_count"},
		{q.UnattributedCostCount, "quality.unattributed_cost_count"},
	}
	for _, c := range counts {
		if _, err := parseRequiredNonNeg(c.v, c.name); err != nil {
			return err
		}
	}
	switch q.CalendarBasis {
	case "team_local", "legacy_utc", "mixed":
	default:
		return fmt.Errorf("quality unknown calendarBasis %q", q.CalendarBasis)
	}
	switch q.LegacyCoverage {
	case "known", "unknown":
	default:
		return fmt.Errorf("quality unknown legacyCoverage %q", q.LegacyCoverage)
	}
	return nil
}

// ValidateSkillStatsJSON accepts {} for non-skill rows.
func ValidateSkillStatsJSON(raw json.RawMessage) error {
	if err := validateJSONObject(raw, "skill_stats"); err != nil {
		return err
	}
	if isEmptyObject(raw) {
		return nil
	}
	if err := requireSchemaV1(raw, "skill_stats"); err != nil {
		return err
	}
	var s skillStatsV1
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("skill_stats JSON: %w", err)
	}
	fields := []struct{ v, name string }{
		{s.ExactCount, "skill_stats.exactCount"},
		{s.DerivedCount, "skill_stats.derivedCount"},
		{s.CorrelatedCount, "skill_stats.correlatedCount"},
		{s.EstimatedCount, "skill_stats.estimatedCount"},
		{s.UnknownAccuracyCount, "skill_stats.unknownAccuracyCount"},
		{s.SuccessCount, "skill_stats.successCount"},
		{s.FailureCount, "skill_stats.failureCount"},
		{s.DurationKnownCount, "skill_stats.durationKnownCount"},
	}
	for _, f := range fields {
		if _, err := parseRequiredNonNeg(f.v, f.name); err != nil {
			return err
		}
	}
	if s.DurationMs != nil {
		if _, err := parseRequiredNonNeg(*s.DurationMs, "skill_stats.durationMs"); err != nil {
			return err
		}
	}
	return nil
}

func validateDayRowJSON(row DayRow) error {
	if err := ValidateSkillStatsJSON(row.SkillStats); err != nil {
		return err
	}
	if err := ValidateResourcesJSON(row.Resources); err != nil {
		return err
	}
	if err := ValidateActivityJSON(row.Activity); err != nil {
		return err
	}
	if err := ValidateHourlyJSON(row.Hourly); err != nil {
		return err
	}
	return ValidateQualityJSON(row.Quality)
}
