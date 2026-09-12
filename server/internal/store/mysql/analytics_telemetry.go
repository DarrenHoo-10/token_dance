package mysql

import (
	"fmt"
	"strings"

	"tokendance/internal/domain"
)

// Convert integer 1e-8 units without floating-point rounding. Zero is a value
// when the aggregate has a known sample; an unpriced sample stays unknown.
func costMetricFromUnits(currency, units string, known, priced, total int) (domain.MetricCost, error) {
	metric := domain.MetricCost{Currency: &currency, PricedRequests: priced, TotalRequests: total}
	if units == "" {
		return metric, fmt.Errorf("empty cost units")
	}
	for _, ch := range units {
		if ch < '0' || ch > '9' {
			return metric, fmt.Errorf("invalid cost units")
		}
	}
	digits := strings.TrimLeft(units, "0")
	metric.Supported = known > 0 || digits != ""
	if !metric.Supported {
		return metric, nil
	}
	if len(digits) <= 8 {
		digits = strings.Repeat("0", 9-len(digits)) + digits
	}
	amount := digits[:len(digits)-8] + "." + digits[len(digits)-8:]
	metric.Amount = &amount
	return metric, nil
}

// telemetryMetricDateSQL formats a day-grain bucket_start (UTC ms of Beijing day
// start) as YYYY-MM-DD in the product statistics calendar.
const telemetryMetricDateSQL = `DATE_FORMAT(CONVERT_TZ(FROM_UNIXTIME(bucket_start / 1000), '+00:00', '+08:00'), '%Y-%m-%d')`

// telemetryMetricDateSQLPrefixed is the same expression with a table alias prefix.
func telemetryMetricDateSQLPrefixed(alias string) string {
	col := "bucket_start"
	if alias != "" {
		col = alias + ".bucket_start"
	}
	return `DATE_FORMAT(CONVERT_TZ(FROM_UNIXTIME(` + col + ` / 1000), '+00:00', '+08:00'), '%Y-%m-%d')`
}

// telemetryWatermarkSQL converts updated_at ms to a DATETIME for API watermarks.
const telemetryWatermarkSQL = `FROM_UNIXTIME(MAX(updated_at) / 1000)`

func dayGrainBucketRange(fromDate, toDate string) (fromMs, toMs int64, err error) {
	fromMs, err = domain.DayBucketStartMs(fromDate)
	if err != nil {
		return 0, 0, fmt.Errorf("from date: %w", err)
	}
	toMs, err = domain.DayBucketStartMs(toDate)
	if err != nil {
		return 0, 0, fmt.Errorf("to date: %w", err)
	}
	return fromMs, toMs, nil
}

func coverageFromCounts(known, observed int64) domain.MetricCoverage {
	if observed <= 0 || known <= 0 {
		return domain.MetricCoverageNone
	}
	if known >= observed {
		return domain.MetricCoverageComplete
	}
	return domain.MetricCoveragePartial
}

// cacheHitRateMetric uses paired cache_eligible_* sample columns.
// Denominator 0 → value null; coverage always reflects pair/observed counts.
func cacheHitRateMetric(eligibleInput, eligibleRead, pairKnown, observed int64, supported bool) domain.MetricDecimal {
	cov := coverageFromCounts(pairKnown, observed)
	known := pairKnown
	obs := observed
	m := domain.MetricDecimal{
		Supported:     supported,
		KnownCount:    &known,
		ObservedCount: &obs,
		Coverage:      &cov,
	}
	if eligibleInput > 0 {
		rateStr := fmt.Sprintf("%.3f", float64(eligibleRead)/float64(eligibleInput))
		m.Value = &rateStr
	}
	return m
}

// scalarCostFromCurrencies fills the legacy single estimatedCost card.
// Multiple currencies cannot be FX-merged: Amount/Currency stay null.
func scalarCostFromCurrencies(costs []domain.MetricCost, pricedTotal, requestTotal, costRecords int) domain.MetricCost {
	supported := costRecords > 0
	for _, cost := range costs {
		supported = supported || cost.Supported
	}
	out := domain.MetricCost{
		Supported:      supported,
		PricedRequests: pricedTotal,
		TotalRequests:  requestTotal,
	}
	if !supported {
		return out
	}
	if len(costs) == 1 {
		c := costs[0]
		out.Amount = c.Amount
		out.Currency = c.Currency
		out.PricingSource = c.PricingSource
		if c.PricedRequests != 0 {
			out.PricedRequests = c.PricedRequests
		}
		if c.TotalRequests != 0 {
			out.TotalRequests = c.TotalRequests
		}
		return out
	}
	// Multi-currency: keep request totals, refuse a fake single-currency amount.
	return out
}
