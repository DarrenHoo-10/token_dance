package teammetrics

import "testing"

func TestValidateTeamAttrJSONRejectInvalid(t *testing.T) {
	okQuality := defaultQuality(false)
	if err := ValidateQualityJSON(okQuality); err != nil {
		t.Fatalf("default quality: %v", err)
	}
	if err := ValidateResourcesJSON(emptyJSONObject()); err != nil {
		t.Fatalf("empty resources: %v", err)
	}
	if err := ValidateActivityJSON(emptyJSONObject()); err != nil {
		t.Fatalf("empty activity: %v", err)
	}
	if err := ValidateHourlyJSON(emptyJSONObject()); err != nil {
		t.Fatalf("empty hourly: %v", err)
	}
	if err := ValidateSkillStatsJSON(emptyJSONObject()); err != nil {
		t.Fatalf("empty skill_stats: %v", err)
	}

	if err := ValidateQualityJSON([]byte(`[]`)); err == nil {
		t.Fatal("quality array must reject")
	}
	if err := ValidateQualityJSON([]byte(`{}`)); err == nil {
		t.Fatal("quality empty object must reject")
	}
	if err := ValidateQualityJSON([]byte(`{"token_supported_event_count":"0"}`)); err == nil {
		t.Fatal("quality missing schemaVersion must reject")
	}
	if err := ValidateQualityJSON([]byte(`{"schemaVersion":2,"token_supported_event_count":"0","reported_cost_event_count":"0","estimated_cost_event_count":"0","reported_covered_usage_count":"0","estimated_covered_usage_count":"0","unattributed_cost_count":"0","legacy_aggregate":false,"calendarBasis":"team_local","legacyCoverage":"known"}`)); err == nil {
		t.Fatal("quality schemaVersion 2 must reject")
	}
	if err := ValidateQualityJSON([]byte(`{"schemaVersion":1,"token_supported_event_count":"-1","reported_cost_event_count":"0","estimated_cost_event_count":"0","reported_covered_usage_count":"0","estimated_covered_usage_count":"0","unattributed_cost_count":"0","legacy_aggregate":false,"calendarBasis":"team_local","legacyCoverage":"known"}`)); err == nil {
		t.Fatal("quality negative must reject")
	}

	badRes := []byte(`{"schemaVersion":1,"inputContextTokens":{"sum":"1","known":"1","observed":"1"},"outputTokens":{"sum":"0","known":"1","observed":"1"},"cachePairs":{"input":"1","read":"2","known":"1","observed":"1"}}`)
	if err := ValidateResourcesJSON(badRes); err == nil {
		t.Fatal("cache read>input must reject")
	}
	knownGT := []byte(`{"schemaVersion":1,"inputContextTokens":{"sum":"1","known":"2","observed":"1"},"outputTokens":{"sum":"0","known":"1","observed":"1"},"cachePairs":{"input":"1","read":"0","known":"1","observed":"1"}}`)
	if err := ValidateResourcesJSON(knownGT); err == nil {
		t.Fatal("known>observed must reject")
	}
	emptyStr := []byte(`{"schemaVersion":1,"inputContextTokens":{"sum":"","known":"1","observed":"1"},"outputTokens":{"sum":"0","known":"1","observed":"1"},"cachePairs":{"input":"1","read":"0","known":"1","observed":"1"}}`)
	if err := ValidateResourcesJSON(emptyStr); err == nil {
		t.Fatal("empty string sum must reject")
	}
	oversize := []byte(`{"schemaVersion":1,"coverage":"complete","unbucketedTokenTotal":"1000000000000000000000000000000","buckets":[]}`)
	if err := ValidateHourlyJSON(oversize); err == nil {
		t.Fatal("hourly value of 10^30 must reject")
	}
	if err := ValidateActivityJSON([]byte(`{"schemaVersion":1}`)); err == nil {
		t.Fatal("activity missing covered sums must reject")
	}
	if err := ValidateSkillStatsJSON([]byte(`{"schemaVersion":1,"exactCount":"1"}`)); err == nil {
		t.Fatal("skill_stats missing counts must reject")
	}
}
