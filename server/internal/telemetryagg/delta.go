package telemetryagg

import (
	"cmp"
	"slices"

	v2 "tokendance/internal/protocol/v2"
)

// EntityKind matches telemetry_bucket_entities.entity_kind.
const (
	EntityKindSession = "session"
	EntityKindTurn    = "turn"
)

// EntitySnapshot is the de-dup / duration state for one entity in one bucket.
type EntitySnapshot struct {
	Kind              string
	Key               [32]byte
	ParentKey         *[32]byte
	HasStarted        bool
	HasCompleted      bool
	HasUserStart      bool
	SessionDurationMs *uint64
	TurnDurationMs    *uint64
}

// DurationFact is one applied (or candidate) fact contributing to duration selection.
type DurationFact struct {
	EventRowID   int64
	OccurredAtMs int64
	EventType    v2.EventType
	SessionKey   [32]byte
	TurnKey      *[32]byte
	ParentKey    *[32]byte
	DurationMs   *uint64
	IsSessionEnd bool // session_ended on a main (non-child) session
	IsTurnEnd    bool
}

// CostFact is one applied (or candidate) cost-bearing fact in a scope.
type CostFact struct {
	EventRowID int64
	OccurredAt int64
	ModelKey   uint64
	Currency   string
	Units      uint64
	Source     v2.CostSource
}

// EffectiveCost is the selected contribution for one cost scope.
type EffectiveCost struct {
	ModelKey   uint64
	Currency   string
	Units      uint64
	Source     v2.CostSource
	IsReported bool
}

// SelectSessionDayDuration picks session_end duration when present, else sums turn ends.
// dayStartMs/dayEndMs bound the Beijing day window [start, end).
func SelectSessionDayDuration(facts []DurationFact, sessionKey [32]byte, dayStartMs, dayEndMs int64) uint64 {
	var bestSessionMs int64 = -1
	var bestSession uint64
	hasSessionEnd := false
	var turnSum uint64
	for _, f := range facts {
		if f.OccurredAtMs < dayStartMs || f.OccurredAtMs >= dayEndMs {
			continue
		}
		if f.SessionKey != sessionKey {
			continue
		}
		if f.IsSessionEnd && f.DurationMs != nil {
			if !hasSessionEnd || f.OccurredAtMs >= bestSessionMs {
				hasSessionEnd = true
				bestSessionMs = f.OccurredAtMs
				bestSession = *f.DurationMs
			}
			continue
		}
		if f.IsTurnEnd && f.DurationMs != nil {
			turnSum += *f.DurationMs
		}
	}
	if hasSessionEnd {
		return bestSession
	}
	return turnSum
}

// SelectEffectiveCost chooses provider_reported over calculated_price within a scope.
// When multiple reported exist, the latest by (occurred_at, event_row_id) wins.
func SelectEffectiveCost(facts []CostFact) *EffectiveCost {
	if len(facts) == 0 {
		return nil
	}
	ordered := append([]CostFact(nil), facts...)
	slices.SortFunc(ordered, func(a, b CostFact) int {
		if c := cmp.Compare(a.OccurredAt, b.OccurredAt); c != 0 {
			return c
		}
		return cmp.Compare(a.EventRowID, b.EventRowID)
	})
	var bestReported *CostFact
	var bestCalculated *CostFact
	for i := range ordered {
		f := &ordered[i]
		switch f.Source {
		case v2.CostSourceProviderReported:
			bestReported = f
		case v2.CostSourceCalculatedPrice:
			if bestCalculated == nil {
				bestCalculated = f
			} else {
				bestCalculated = f // latest wins
			}
		}
	}
	chosen := bestCalculated
	if bestReported != nil {
		chosen = bestReported
	}
	if chosen == nil {
		return nil
	}
	return &EffectiveCost{
		ModelKey:   chosen.ModelKey,
		Currency:   chosen.Currency,
		Units:      chosen.Units,
		Source:     chosen.Source,
		IsReported: chosen.Source == v2.CostSourceProviderReported,
	}
}

// CostDelta is old→new difference for one cost metrics row key.
type CostDelta struct {
	ModelKey              uint64
	Currency              string
	ReportedUnits         int64
	EstimatedUnits        int64
	ReportedRequestCount  int64
	EstimatedRequestCount int64
	UnpricedRequestCount  int64
	CostKnownCount        int64
}

// DiffEffectiveCost computes signed deltas between previous and next effective costs.
func DiffEffectiveCost(oldCost, newCost *EffectiveCost) []CostDelta {
	type key struct {
		model    uint64
		currency string
	}
	acc := map[key]*CostDelta{}
	apply := func(c *EffectiveCost, sign int64) {
		if c == nil {
			return
		}
		k := key{c.ModelKey, c.Currency}
		d := acc[k]
		if d == nil {
			d = &CostDelta{ModelKey: c.ModelKey, Currency: c.Currency}
			acc[k] = d
		}
		d.CostKnownCount += sign
		if c.IsReported {
			d.ReportedUnits += sign * int64(c.Units)
			d.ReportedRequestCount += sign
		} else {
			d.EstimatedUnits += sign * int64(c.Units)
			d.EstimatedRequestCount += sign
		}
	}
	apply(oldCost, -1)
	apply(newCost, 1)
	out := make([]CostDelta, 0, len(acc))
	for _, d := range acc {
		if d.ReportedUnits == 0 && d.EstimatedUnits == 0 &&
			d.ReportedRequestCount == 0 && d.EstimatedRequestCount == 0 &&
			d.UnpricedRequestCount == 0 && d.CostKnownCount == 0 {
			continue
		}
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b CostDelta) int {
		if c := cmp.Compare(a.ModelKey, b.ModelKey); c != 0 {
			return c
		}
		return cmp.Compare(a.Currency, b.Currency)
	})
	return out
}

// ApplyEntityFlags updates entity flags from an event; returns harness counter deltas (0/1).
type EntityFlagDelta struct {
	SessionCount         uint64
	ChildSessionCount    uint64
	InteractionTurnCount uint64
	TurnStartedCount     uint64
	TurnCompletedCount   uint64
	UserTurnStartedCount uint64
	ToolCallCount        uint64
	SkillUseCount        uint64
}

func ApplySessionStarted(ent *EntitySnapshot, isChild bool) EntityFlagDelta {
	var d EntityFlagDelta
	first := !ent.HasStarted && !ent.HasCompleted
	if !ent.HasStarted {
		ent.HasStarted = true
	}
	if first {
		if isChild {
			d.ChildSessionCount = 1
		} else {
			d.SessionCount = 1
		}
	}
	return d
}

func ApplySessionEnded(ent *EntitySnapshot, isChild bool, durationMs *uint64) EntityFlagDelta {
	var d EntityFlagDelta
	first := !ent.HasStarted && !ent.HasCompleted
	if !ent.HasCompleted {
		ent.HasCompleted = true
	}
	if durationMs != nil {
		ent.SessionDurationMs = durationMs
	}
	if first {
		if isChild {
			d.ChildSessionCount = 1
		} else {
			d.SessionCount = 1
		}
	}
	return d
}

func ApplyTurnStarted(ent *EntitySnapshot, userTrigger bool) EntityFlagDelta {
	var d EntityFlagDelta
	first := !ent.HasStarted && !ent.HasCompleted
	if !ent.HasStarted {
		ent.HasStarted = true
		d.TurnStartedCount = 1
	}
	if userTrigger && !ent.HasUserStart {
		ent.HasUserStart = true
		d.UserTurnStartedCount = 1
	}
	if first {
		d.InteractionTurnCount = 1
	}
	return d
}

func ApplyTurnCompleted(ent *EntitySnapshot, durationMs *uint64) EntityFlagDelta {
	var d EntityFlagDelta
	first := !ent.HasStarted && !ent.HasCompleted
	if !ent.HasCompleted {
		ent.HasCompleted = true
		d.TurnCompletedCount = 1
	}
	if durationMs != nil {
		ent.TurnDurationMs = durationMs
	}
	if first {
		d.InteractionTurnCount = 1
	}
	return d
}
