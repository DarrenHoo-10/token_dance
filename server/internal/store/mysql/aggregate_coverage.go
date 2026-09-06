package mysql

import (
	"math/big"
	"tokendance/internal/domain"
)

func coversAggregate(snapshot domain.AggregateSnapshot, agent, total string) bool {
	expected, ok := new(big.Int).SetString(total, 10)
	if !ok {
		return false
	}
	actual := new(big.Int)
	for _, row := range snapshot.Rows {
		if row.Kind == "agent" && row.AgentID == agent {
			for _, key := range []string{"exact_token_total", "derived_token_total"} {
				n, ok := new(big.Int).SetString(row.Metrics[key], 10)
				if ok {
					actual.Add(actual, n)
				}
			}
		}
	}
	return actual.Cmp(expected) >= 0
}
