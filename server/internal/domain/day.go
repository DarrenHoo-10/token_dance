package domain

import "time"

// DayTZName is the product statistics calendar label returned on public and
// personal analytics payloads. It is UTC+8 with no DST (Beijing time).
const DayTZName = "UTC+8"

// DayTZOffset is the MySQL CONVERT_TZ offset that matches DayTZ without
// requiring named timezone tables.
const DayTZOffset = "+08:00"

// DayTZ is the product timezone used to align rolling hour buckets and define
// where calendar-day aggregates break (leaderboard 7d/30d/all windows, personal
// analytics and community totals). Raw events keep universal UTC timestamps in
// occurred_at; this zone projects them onto product hours and days. Changing it
// means rebuilding the derived hourly and daily tables.
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

// Rolling24HourBuckets returns the 24 Beijing-time hour buckets ending at the
// current hour. Aligned buckets keep read models consistent without a midnight
// reset.
func Rolling24HourBuckets(t time.Time) (time.Time, time.Time) {
	local := t.In(DayTZ)
	to := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, DayTZ)
	return to.Add(-23 * time.Hour), to
}

// PreviousDayDate is the statistics-day key before t's statistics day.
func PreviousDayDate(t time.Time) string {
	return StartOfDay(t).AddDate(0, 0, -1).Format("2006-01-02")
}
