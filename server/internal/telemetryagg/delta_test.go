package telemetryagg

import (
	"testing"
	"time"

	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
)

func TestSelectSessionDayDurationPrefersSessionEnd(t *testing.T) {
	var session [32]byte
	session[0] = 1
	turnA, turnB := [32]byte{2}, [32]byte{3}
	d100, d200, d500 := uint64(100), uint64(200), uint64(500)
	facts := []DurationFact{
		{OccurredAtMs: 10, SessionKey: session, TurnKey: &turnA, DurationMs: &d100, IsTurnEnd: true},
		{OccurredAtMs: 20, SessionKey: session, TurnKey: &turnB, DurationMs: &d200, IsTurnEnd: true},
		{OccurredAtMs: 30, SessionKey: session, DurationMs: &d500, IsSessionEnd: true},
	}
	got := SelectSessionDayDuration(facts, session, 0, 100)
	if got != 500 {
		t.Fatalf("expected 500, got %d", got)
	}
}

func TestSelectSessionDayDurationTurnFallback(t *testing.T) {
	var session [32]byte
	session[0] = 1
	turnA, turnB := [32]byte{2}, [32]byte{3}
	d100, d200 := uint64(100), uint64(200)
	facts := []DurationFact{
		{OccurredAtMs: 10, SessionKey: session, TurnKey: &turnA, DurationMs: &d100, IsTurnEnd: true},
		{OccurredAtMs: 20, SessionKey: session, TurnKey: &turnB, DurationMs: &d200, IsTurnEnd: true},
	}
	got := SelectSessionDayDuration(facts, session, 0, 100)
	if got != 300 {
		t.Fatalf("expected 300, got %d", got)
	}
}

func TestDiffEffectiveCostReplaceReported(t *testing.T) {
	old := &EffectiveCost{ModelKey: 1, Currency: "USD", Units: 5, Source: "calculated_price", IsReported: false, RequestCount: 1}
	neu := &EffectiveCost{ModelKey: 1, Currency: "USD", Units: 4, Source: "provider_reported", IsReported: true, RequestCount: 1}
	deltas := DiffEffectiveCost(old, neu)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %+v", deltas)
	}
	d := deltas[0]
	if d.EstimatedUnits != -5 || d.ReportedUnits != 4 {
		t.Fatalf("unexpected delta %+v", d)
	}
	if d.EstimatedRequestCount != -1 || d.ReportedRequestCount != 1 {
		t.Fatalf("unexpected request counts %+v", d)
	}
}

// TestReviewSelectEffectiveCostSumsCalculatedThenBillOverrides reproduces
// review blocker #6: same scope 2+3 must sum to 5, then bill 4 replaces (not 3 / not 9).
func TestReviewSelectEffectiveCostSumsCalculatedThenBillOverrides(t *testing.T) {
	facts := []CostFact{
		{EventRowID: 1, OccurredAt: 10, ModelKey: 7, Currency: "USD", Units: 2, Source: v2.CostSourceCalculatedPrice},
		{EventRowID: 2, OccurredAt: 20, ModelKey: 7, Currency: "USD", Units: 3, Source: v2.CostSourceCalculatedPrice},
	}
	got := SelectEffectiveCosts(facts)
	if len(got) != 1 || got[0].Units != 5 || got[0].IsReported || got[0].RequestCount != 2 {
		t.Fatalf("expected calculated sum 5 from 2 requests, got %+v", got)
	}

	withBill := append(append([]CostFact{}, facts...), CostFact{
		EventRowID: 3, OccurredAt: 30, ModelKey: 7, Currency: "USD", Units: 4, Source: v2.CostSourceProviderReported,
	})
	after := SelectEffectiveCosts(withBill)
	if len(after) != 1 || after[0].Units != 4 || !after[0].IsReported {
		t.Fatalf("expected bill override to 4, got %+v", after)
	}
	deltas := DiffEffectiveCosts(got, after)
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %+v", deltas)
	}
	d := deltas[0]
	if d.EstimatedUnits != -5 || d.ReportedUnits != 4 {
		t.Fatalf("expected withdraw 5 estimated / add 4 reported, got %+v", d)
	}
	if d.EstimatedRequestCount != -2 || d.ReportedRequestCount != 1 {
		t.Fatalf("expected request count -2/+1, got %+v", d)
	}
}

// TestReviewHourDurationWithdrawsTurnBuckets reproduces review blocker #7:
// after session_end, hour-09 and hour-10 turn fallbacks must be withdrawn and
// hour-11 credited — checking each hour, not only the day sum.
func TestReviewHourDurationWithdrawsTurnBuckets(t *testing.T) {
	var session [32]byte
	session[0] = 1
	turnA, turnB := [32]byte{2}, [32]byte{3}
	h9 := beijingMs(2024, 6, 15, 9)
	h10 := beijingMs(2024, 6, 15, 10)
	h11 := beijingMs(2024, 6, 15, 11)
	d100, d200, d500 := uint64(100), uint64(200), uint64(500)
	turns := []DurationFact{
		{EventRowID: 1, OccurredAtMs: h9, SessionKey: session, TurnKey: &turnA, DurationMs: &d100, IsTurnEnd: true},
		{EventRowID: 2, OccurredAtMs: h10, SessionKey: session, TurnKey: &turnB, DurationMs: &d200, IsTurnEnd: true},
	}
	dayStart, dayEnd := BeijingDayBounds(h9)
	oldMap := SessionHourContributions(turns, session, dayStart, dayEnd)
	b9 := mustHour(t, h9)
	b10 := mustHour(t, h10)
	b11 := mustHour(t, h11)
	if oldMap[b9] != 100 || oldMap[b10] != 200 {
		t.Fatalf("turn fallback map wrong: %+v", oldMap)
	}
	withEnd := append(append([]DurationFact{}, turns...), DurationFact{
		EventRowID: 3, OccurredAtMs: h11, SessionKey: session, DurationMs: &d500, IsSessionEnd: true,
	})
	newMap := SessionHourContributions(withEnd, session, dayStart, dayEnd)
	if newMap[b11] != 500 || len(newMap) != 1 {
		t.Fatalf("session_end map wrong: %+v", newMap)
	}
	deltas := DiffHourDurationMaps(oldMap, newMap)
	if deltas[b9] != -100 || deltas[b10] != -200 || deltas[b11] != 500 {
		t.Fatalf("hour deltas wrong: %+v", deltas)
	}
	if SumDurationMap(oldMap) != 300 || SumDurationMap(newMap) != 500 {
		t.Fatalf("day totals wrong old=%d new=%d", SumDurationMap(oldMap), SumDurationMap(newMap))
	}
}

func beijingMs(year, month, day, hour int) int64 {
	return time.Date(year, time.Month(month), day, hour, 0, 0, 0, domain.DayTZ).UnixMilli()
}

func mustHour(t *testing.T, occurredAtMs int64) int64 {
	t.Helper()
	b, err := domain.BucketStartMs(domain.TelemetryGrainHour, occurredAtMs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCalculatedCostRevisionReplacesOlderQuote(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		old := CostFact{FactKey: "request", Revision: 1, EventRowID: 1, ModelKey: 1, Currency: "USD", Units: 20, Source: v2.CostSourceCalculatedPrice}
		latest := old
		latest.Revision = 2
		latest.EventRowID = 2
		latest.Units = 5
		facts := []CostFact{old, latest}
		if reverse {
			facts = []CostFact{latest, old}
		}
		got := SelectEffectiveCosts(facts)
		if len(got) != 1 || got[0].Units != 5 || got[0].RequestCount != 1 {
			t.Fatalf("revision must replace: %+v", got)
		}
	}
}
