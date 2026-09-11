package domain

import (
	"fmt"
	"time"
)

// Telemetry consumer / grain identifiers.
const (
	TelemetryGrainHour  = "hour"
	TelemetryGrainDay   = "day"
	TelemetryGrainMonth = "month"
)

// status_json path values for hour/day/month consumers.
const (
	TelemetryStatusPending      = 0
	TelemetryStatusRetry        = 1
	TelemetryStatusInFlight     = 2
	TelemetryStatusApplied      = 3
	TelemetryStatusNotApplicable = 4
	TelemetryStatusBlocked      = 5
	TelemetryStatusQuarantined  = 6
)

// ParseDayDate parses a YYYY-MM-DD statistics calendar key in DayTZ.
func ParseDayDate(s string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02", s, DayTZ)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse day date %q: %w", s, err)
	}
	return t, nil
}

// BucketStartMs returns the UTC+8 grain bucket start as UTC epoch milliseconds.
func BucketStartMs(grain string, occurredAtMs int64) (int64, error) {
	t := time.UnixMilli(occurredAtMs).In(DayTZ)
	switch grain {
	case TelemetryGrainHour:
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, DayTZ).UnixMilli(), nil
	case TelemetryGrainDay:
		return StartOfDay(t).UnixMilli(), nil
	case TelemetryGrainMonth:
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, DayTZ).UnixMilli(), nil
	default:
		return 0, fmt.Errorf("unknown telemetry grain %q", grain)
	}
}

// DayBucketStartMs is the day-grain bucket_start for a YYYY-MM-DD key.
func DayBucketStartMs(metricDate string) (int64, error) {
	day, err := ParseDayDate(metricDate)
	if err != nil {
		return 0, err
	}
	return day.UnixMilli(), nil
}
