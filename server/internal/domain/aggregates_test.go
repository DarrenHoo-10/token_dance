package domain

import (
	"testing"
	"time"
)

func TestAggregateValidation(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	good := func() AggregateSnapshot {
		return AggregateSnapshot{SchemaVersion: 1, Day: "2026-09-06", Revision: 1, Rows: []AggregateRow{{Kind: "agent", AgentID: "codex", Metrics: map[string]string{"exact_token_total": "100"}}}}
	}
	if err := good().Validate(now); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*AggregateSnapshot){
		"future":              func(s *AggregateSnapshot) { s.Day = "2026-09-07" },
		"negative_revision":   func(s *AggregateSnapshot) { s.Revision = -1 },
		"numeric_overflow":    func(s *AggregateSnapshot) { s.Rows[0].Metrics["exact_token_total"] = "18446744073709551616" },
		"sql_column":          func(s *AggregateSnapshot) { s.Rows[0].Metrics["user_id"] = "1" },
		"duplicate_dimension": func(s *AggregateSnapshot) { s.Rows = append(s.Rows, s.Rows[0]) },
		"fraction":            func(s *AggregateSnapshot) { s.Rows[0].Metrics["exact_token_total"] = "1.5" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := good()
			mutate(&s)
			if s.Validate(now) == nil {
				t.Fatal("accepted invalid aggregate")
			}
		})
	}
}
