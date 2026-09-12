package mysql

import (
	"testing"

	"tokendance/internal/domain"
)

func TestReview3KnownZeroCostAndUnpricedCostRemainDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, units, want string
		known             int
	}{
		{"known zero", "0", "0.00000000", 1},
		{"unpriced", "0", "", 0},
		{"fraction", "1", "0.00000001", 1},
		{"large exact", "9007199254740993", "90071992.54740993", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cost, err := costMetricFromUnits("USD", tc.units, tc.known, tc.known, 1)
			if err != nil {
				t.Fatal(err)
			}
			scalar := scalarCostFromCurrencies([]domain.MetricCost{cost}, tc.known, 1, tc.known)
			for _, got := range []domain.MetricCost{cost, scalar} {
				if tc.want == "" {
					if got.Amount != nil || got.Supported {
						t.Fatalf("unpriced must stay unknown: %+v", got)
					}
				} else if got.Amount == nil || *got.Amount != tc.want || !got.Supported {
					t.Fatalf("want amount %s, got %+v", tc.want, got)
				}
			}
		})
	}
}

func TestCacheHitRateMetricDenomZeroIsNull(t *testing.T) {
	m := cacheHitRateMetric(0, 50, 0, 2, true)
	if m.Value != nil {
		t.Fatalf("denom 0 must return null value, got %v", *m.Value)
	}
	if m.Coverage == nil || *m.Coverage != domain.MetricCoverageNone {
		t.Fatalf("expected coverage none, got %+v", m.Coverage)
	}
}

func TestCacheHitRateMetricSumsThenDivides(t *testing.T) {
	// 90/100 + 10/1000 → 100/1100 ≈ 0.091
	m := cacheHitRateMetric(1100, 100, 2, 2, true)
	if m.Value == nil || *m.Value != "0.091" {
		t.Fatalf("expected 0.091, got %+v", m)
	}
	if m.Coverage == nil || *m.Coverage != domain.MetricCoverageComplete {
		t.Fatalf("expected complete coverage, got %+v", m.Coverage)
	}
	if m.KnownCount == nil || *m.KnownCount != 2 || m.ObservedCount == nil || *m.ObservedCount != 2 {
		t.Fatalf("expected known/observed 2/2, got %+v", m)
	}
}

func TestScalarCostFromCurrenciesRefusesMultiCurrencyMerge(t *testing.T) {
	usd, cny := "USD", "CNY"
	a1, a2 := "1.00000000", "7.00000000"
	costs := []domain.MetricCost{
		{Amount: &a1, Currency: &usd, Supported: true, PricedRequests: 1, TotalRequests: 1},
		{Amount: &a2, Currency: &cny, Supported: true, PricedRequests: 1, TotalRequests: 1},
	}
	scalar := scalarCostFromCurrencies(costs, 2, 2, 2)
	if scalar.Amount != nil || scalar.Currency != nil {
		t.Fatalf("multi-currency must not set scalar amount/currency: %+v", scalar)
	}
	if !scalar.Supported || scalar.PricedRequests != 2 || scalar.TotalRequests != 2 {
		t.Fatalf("unexpected scalar totals: %+v", scalar)
	}

	single := scalarCostFromCurrencies(costs[:1], 1, 1, 1)
	if single.Amount == nil || *single.Amount != a1 || single.Currency == nil || *single.Currency != usd {
		t.Fatalf("single currency scalar: %+v", single)
	}
}
