package mysql

import (
	"fmt"
	"tokendance/internal/domain"
)

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
