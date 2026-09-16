package domain

import (
	"errors"
	"testing"
	"time"
)

func TestWindowInclusiveDates(t *testing.T) {
	now := time.Date(2026, 9, 16, 15, 0, 0, 0, DayTZ)
	cases := []struct {
		window, from, to string
	}{
		{"today", "2026-09-16", "2026-09-16"},
		{"7d", "2026-09-10", "2026-09-16"},
		{"30d", "2026-08-18", "2026-09-16"},
		{"all", "1000-01-01", "2026-09-16"},
	}
	for _, tc := range cases {
		from, to, err := WindowInclusiveDates(tc.window, now)
		if err != nil || from != tc.from || to != tc.to {
			t.Fatalf("%s: got %s..%s %v, want %s..%s", tc.window, from, to, err, tc.from, tc.to)
		}
	}
	if _, _, err := WindowInclusiveDates("week", now); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected invalid window, got %v", err)
	}
}

func TestPreviousWindowInclusiveDates(t *testing.T) {
	now := time.Date(2026, 9, 16, 15, 0, 0, 0, DayTZ)
	from, to, ok := PreviousWindowInclusiveDates("today", now)
	if !ok || from != "2026-09-15" || to != "2026-09-15" {
		t.Fatalf("today previous: %s..%s ok=%v", from, to, ok)
	}
	from, to, ok = PreviousWindowInclusiveDates("7d", now)
	if !ok || from != "2026-09-03" || to != "2026-09-09" {
		t.Fatalf("7d previous: %s..%s ok=%v", from, to, ok)
	}
	from, to, ok = PreviousWindowInclusiveDates("30d", now)
	if !ok || from != "2026-07-19" || to != "2026-08-17" {
		t.Fatalf("30d previous: %s..%s ok=%v", from, to, ok)
	}
	if _, _, ok = PreviousWindowInclusiveDates("all", now); ok {
		t.Fatal("all-time has no previous window")
	}
}
