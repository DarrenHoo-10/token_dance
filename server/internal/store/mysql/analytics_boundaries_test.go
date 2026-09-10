package mysql

import (
	"testing"
	"time"

	"tokendance/internal/domain"
)

func TestPlanUTCAggregatesTodayIsRawBeijingMorning(t *testing.T) {
	from := time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC)
	to := time.Date(2026, 9, 10, 1, 51, 0, 0, time.UTC)
	plan := planUTCAggregates(domain.TimeRange{Key: domain.TimeRangeToday, From: from, To: to, Timezone: "UTC"})
	if plan.hasFull {
		t.Fatalf("partial Beijing today must not use complete UTC days: %+v", plan)
	}
	if len(plan.raw) != 1 {
		t.Fatalf("expected one raw interval, got %+v", plan.raw)
	}
	if !plan.raw[0].from.Equal(from) || !plan.raw[0].to.Equal(to.Add(time.Nanosecond)) {
		t.Fatalf("raw interval must cover Beijing midnight through now, got %+v", plan.raw[0])
	}
}

func TestPlanUTCAggregatesThirtyDaysUsesBeijingMetricDates(t *testing.T) {
	from := time.Date(2026, 8, 12, 0, 0, 0, 0, domain.DayTZ)
	to := time.Date(2026, 9, 10, 9, 51, 0, 0, domain.DayTZ)
	plan := planUTCAggregates(domain.TimeRange{Key: domain.TimeRange30d, From: from.UTC(), To: to.UTC(), Timezone: "UTC"})
	if !plan.hasFull || plan.fromDate != "2026-08-12" || plan.toDate != "2026-09-09" {
		t.Fatalf("expected Beijing full days Aug 12–Sep 9, got %+v", plan)
	}
	if len(plan.raw) != 1 || domain.DayDate(plan.raw[0].from) != "2026-09-10" {
		t.Fatalf("expected raw remainder on Beijing today, got %+v", plan.raw)
	}
}
