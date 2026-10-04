package teammetrics

import (
	"testing"
	"time"
)

func TestRollingHandleKeepsExactHourAndRangeIdentity(t *testing.T) {
	from := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	if got := coveringEndDate(to); got != "2026-09-30" {
		t.Fatalf("partial final day must be included, got upper date %s", got)
	}
	if got := coveringEndDate(time.Date(2026, 9, 28, 16, 0, 0, 0, time.UTC)); got != "2026-09-29" {
		t.Fatalf("midnight upper bound must not include an extra day, got %s", got)
	}
	id := HandleID("tem_123", "today", from, to, 1)
	if id == HandleID("tem_123", "today", from.Add(time.Hour), to.Add(time.Hour), 1) ||
		id == HandleID("tem_123", "custom", from, to, 1) {
		t.Fatal("snapshot handle reused across rolling hours or range types")
	}
}
