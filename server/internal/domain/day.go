package domain

import "time"

// DayTZName is the product statistics calendar label returned on public and
// personal analytics payloads. It is UTC+8 with no DST (Beijing time).
const DayTZName = "UTC+8"

// DayTZOffset is the MySQL CONVERT_TZ offset that matches DayTZ without
// requiring named timezone tables.
const DayTZOffset = "+08:00"

// DayTZ is the calendar timezone that defines where a statistics day breaks
// (leaderboard windows, personal today/7d/30d/all, daily aggregates, community
// totals). Raw events keep universal UTC timestamps in occurred_at; this zone
// only projects them onto calendar days. Changing it means re-deriving
// usage_events.occurred_date and rebuilding the derived daily tables.
var DayTZ = time.FixedZone(DayTZName, 8*3600)

// DayDate formats an instant as the statistics-day key in DayTZ.
func DayDate(t time.Time) string {
	return t.In(DayTZ).Format("2006-01-02")
}

// StartOfDay is the midnight instant of t's statistics day in DayTZ.
func StartOfDay(t time.Time) time.Time {
	local := t.In(DayTZ)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, DayTZ)
}

// PreviousDayDate is the statistics-day key before t's statistics day.
func PreviousDayDate(t time.Time) string {
	return StartOfDay(t).AddDate(0, 0, -1).Format("2006-01-02")
}
