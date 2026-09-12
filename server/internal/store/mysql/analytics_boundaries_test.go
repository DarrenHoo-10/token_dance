package mysql

import (
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestPlanUTCAggregatesTodayUsesBeijingMetricDate(t *testing.T) {
	from := time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 10, 1, 51, 0, 0, time.UTC)
	plan := planUTCAggregates(domain.TimeRange{Key: domain.TimeRangeToday, From: from, To: to, Timezone: "UTC"})
	if !plan.hasFull || plan.fromDate != "2026-09-10" || plan.toDate != "2026-09-10" {
		t.Fatalf("expected Beijing today metric_date 2026-09-10, got %+v", plan)
	}
	if len(plan.raw) != 0 {
		t.Fatalf("in-progress days must not SUM usage_events, got %+v", plan.raw)
	}
}

func TestPlanUTCAggregatesThirtyDaysIncludesBeijingToday(t *testing.T) {
	from := time.Date(2026, 8, 12, 0, 0, 0, 0, domain.DayTZ)
	to := time.Date(2026, 9, 10, 9, 51, 0, 0, domain.DayTZ)
	plan := planUTCAggregates(domain.TimeRange{Key: domain.TimeRange30d, From: from.UTC(), To: to.UTC(), Timezone: "UTC"})
	if !plan.hasFull || plan.fromDate != "2026-08-12" || plan.toDate != "2026-09-10" {
		t.Fatalf("expected Beijing full days Aug 12–Sep 10, got %+v", plan)
	}
	if len(plan.raw) != 0 {
		t.Fatalf("expected no live event remainder, got %+v", plan.raw)
	}
}
