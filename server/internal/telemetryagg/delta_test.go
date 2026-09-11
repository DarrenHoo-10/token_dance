package telemetryagg

import "testing"

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
	old := &EffectiveCost{ModelKey: 1, Currency: "USD", Units: 5, Source: "calculated_price", IsReported: false}
	neu := &EffectiveCost{ModelKey: 1, Currency: "USD", Units: 4, Source: "provider_reported", IsReported: true}
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
