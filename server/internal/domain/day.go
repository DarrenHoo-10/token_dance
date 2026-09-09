package domain

import "time"

// DayTZ is the calendar timezone that defines where a statistics day breaks
// (leaderboard windows, daily aggregates, community totals). Raw events keep
// universal UTC timestamps in occurred_at; this zone only projects them onto
// calendar days. Changing it means re-deriving usage_events.occurred_date and
// rebuilding the derived daily tables.
var DayTZ = time.FixedZone("UTC+8", 8*3600)

// DayDate formats an instant as the statistics-day key in DayTZ.
func DayDate(t time.Time) string {
	return t.In(DayTZ).Format("2006-01-02")
}
