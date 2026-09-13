package teams

import (
	"testing"
	"time"
)

func TestTeamTodayDefaultAndCustomBoundaries(t *testing.T) {
	now := time.Date(2026, 9, 6, 18, 0, 0, 0, time.UTC)
	for _, key := range []string{"", "today"} {
		from, to, err := ResolveTeamRange("Asia/Shanghai", key, "", "", now)
		if err != nil || !from.Equal(time.Date(2026, 9, 6, 16, 0, 0, 0, time.UTC)) || to.Sub(from) != 24*time.Hour {
			t.Fatalf("today must follow team timezone: %s %s %v", from, to, err)
		}
	}
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{
		{"2026-01-01", "2026-03-31", true}, // 90 inclusive days, not a rolling lookback limit.
		{"2026-01-01", "2026-04-01", false},
		{"2026-09-01", "2026-09-07", true},
		{"2026-09-01", "2026-09-08", false}, // Future end with a historical start.
		{"2026-09-02", "2026-09-01", false},
		{"", "2026-09-01", false},
	} {
		_, _, err := ResolveTeamRange("Asia/Shanghai", "custom", tc.from, tc.to, now)
		if (err == nil) != tc.valid {
			t.Fatalf("range %s..%s valid=%v: %v", tc.from, tc.to, tc.valid, err)
		}
	}
	// A local day is not always 24 hours.
	from, to, err := ResolveTeamRange("America/New_York", "today", "", "", time.Date(2026, 3, 8, 18, 0, 0, 0, time.UTC))
	if err != nil || to.Sub(from) != 23*time.Hour {
		t.Fatalf("DST day: %s %s %v", from, to, err)
	}
}
