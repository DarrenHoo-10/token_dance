package mysql

import (
	"testing"
	"tokendance/internal/domain"
)

func TestAggregateCannotReplaceIncompleteHistoricalCoverage(t *testing.T) {
	s := domain.AggregateSnapshot{Rows: []domain.AggregateRow{{Kind: "agent", AgentID: "codex", Metrics: map[string]string{"exact_token_total": "9007199254740993", "derived_token_total": "1"}}}}
	if coversAggregate(s, "codex", "9007199254740995") {
		t.Fatal("accepted incomplete history")
	}
	if !coversAggregate(s, "codex", "9007199254740994") {
		t.Fatal("lost integer precision")
	}
	if coversAggregate(s, "grok", "1") {
		t.Fatal("missing agent replaced history")
	}
}
