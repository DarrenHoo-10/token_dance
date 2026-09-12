package mysql

import (
	"testing"

	"tokendance/internal/store"
)

func TestApplyCommunityCostsRefusesMultiCurrencyMerge(t *testing.T) {
	var mixed store.CommunityDailyTotals
	applyCommunityCosts(&mixed, []store.CommunityCost{
		{Currency: "CNY", Amount: 7},
		{Currency: "USD", Amount: 1},
	})
	if mixed.CostAmount != 0 {
		t.Fatalf("mixed currencies must not store a scalar sum, got %v", mixed.CostAmount)
	}
	if len(mixed.Costs) != 2 {
		t.Fatalf("expected 2 costs, got %+v", mixed.Costs)
	}

	var single store.CommunityDailyTotals
	applyCommunityCosts(&single, []store.CommunityCost{{Currency: "USD", Amount: 1}})
	if single.CostAmount != 1 {
		t.Fatalf("single currency scalar: %+v", single)
	}

	var empty store.CommunityDailyTotals
	applyCommunityCosts(&empty, nil)
	if empty.CostAmount != 0 || empty.Costs != nil {
		t.Fatalf("empty costs: %+v", empty)
	}
}
