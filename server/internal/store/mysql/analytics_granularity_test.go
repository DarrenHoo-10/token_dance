package mysql

import (
	"testing"

	"tokendance/internal/domain"
)

func TestTrendGranularityForRange(t *testing.T) {
	tests := []struct {
		name string
		key  domain.TimeRangeKey
		want string
	}{
		{name: "today is hourly", key: domain.TimeRangeToday, want: "hour"},
		{name: "seven days is daily", key: domain.TimeRange7d, want: "day"},
		{name: "thirty days is daily", key: domain.TimeRange30d, want: "day"},
		{name: "all time is monthly", key: domain.TimeRangeAll, want: "month"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := trendGranularityForRange(test.key); got != test.want {
				t.Fatalf("trendGranularityForRange(%q) = %q, want %q", test.key, got, test.want)
			}
		})
	}
}
