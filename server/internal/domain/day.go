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

// WindowInclusiveDates returns the inclusive YYYY-MM-DD bounds for a
// leaderboard / community-stats window on the product calendar.
func WindowInclusiveDates(window string, now time.Time) (from, to string, err error) {
	end := StartOfDay(now)
	to = end.Format("2006-01-02")
	switch window {
	case "today":
		return to, to, nil
	case "7d":
		return end.AddDate(0, 0, -6).Format("2006-01-02"), to, nil
	case "30d":
		return end.AddDate(0, 0, -29).Format("2006-01-02"), to, nil
	case "all":
		return "1000-01-01", to, nil
	default:
		return "", "", ErrInvalidArgument
	}
}

// PreviousWindowInclusiveDates is the same-length window immediately before
// WindowInclusiveDates. "all" and unknown keys have no comparison baseline.
func PreviousWindowInclusiveDates(window string, now time.Time) (from, to string, ok bool) {
	end := StartOfDay(now)
	switch window {
	case "today":
		prev := end.AddDate(0, 0, -1).Format("2006-01-02")
		return prev, prev, true
	case "7d":
		return end.AddDate(0, 0, -13).Format("2006-01-02"), end.AddDate(0, 0, -7).Format("2006-01-02"), true
	case "30d":
		return end.AddDate(0, 0, -59).Format("2006-01-02"), end.AddDate(0, 0, -30).Format("2006-01-02"), true
	default:
		return "", "", false
	}
}
