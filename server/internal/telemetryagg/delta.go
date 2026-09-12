package telemetryagg

import (
	"cmp"
	"slices"
	"time"

	"tokendance/internal/domain"
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
// EventRowID is the request identity used when summing calculated prices.
type CostFact struct {
	EventRowID int64
	OccurredAt int64
	ModelKey   uint64
	Currency   string
	Units      uint64
	Source     v2.CostSource
}

// EffectiveCost is the selected contribution for one cost scope slice
// (model_key + currency). RequestCount is the number of request identities
// that contributed when Source is calculated_price.
type EffectiveCost struct {
	ModelKey     uint64
	Currency     string
	Units        uint64
	Source       v2.CostSource
	IsReported   bool
	RequestCount int64
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

// SessionHourContributions maps Beijing hour bucket_start → effective duration
// for one session on one business day. Session_end authority replaces turn
// fallbacks and lands on the session_end hour; otherwise each turn contributes
// to its own hour bucket.
func SessionHourContributions(facts []DurationFact, sessionKey [32]byte, dayStartMs, dayEndMs int64) map[int64]uint64 {
	out := map[int64]uint64{}
	var bestSessionMs int64 = -1
	var bestSession uint64
	var bestSessionAt int64
	hasSessionEnd := false
	for _, f := range facts {
		if f.OccurredAtMs < dayStartMs || f.OccurredAtMs >= dayEndMs {
			continue
		}
		if f.SessionKey != sessionKey {
			continue
		}
		if f.IsSessionEnd && f.DurationMs != nil {
			if !hasSessionEnd || f.OccurredAtMs > bestSessionMs ||
				(f.OccurredAtMs == bestSessionMs && f.EventRowID > 0) {
				hasSessionEnd = true
				bestSessionMs = f.OccurredAtMs
				bestSession = *f.DurationMs
				bestSessionAt = f.OccurredAtMs
			}
		}
	}
	if hasSessionEnd {
		bucket, err := domain.BucketStartMs(domain.TelemetryGrainHour, bestSessionAt)
		if err != nil {
			return out
		}
		out[bucket] = bestSession
		return out
	}
	for _, f := range facts {
		if f.OccurredAtMs < dayStartMs || f.OccurredAtMs >= dayEndMs {
			continue
		}
		if f.SessionKey != sessionKey || !f.IsTurnEnd || f.DurationMs == nil {
			continue
		}
		bucket, err := domain.BucketStartMs(domain.TelemetryGrainHour, f.OccurredAtMs)
		if err != nil {
			continue
		}
		out[bucket] += *f.DurationMs
	}
	return out
}

// DiffHourDurationMaps returns signed hour-bucket duration deltas (new - old).
func DiffHourDurationMaps(oldMap, newMap map[int64]uint64) map[int64]int64 {
	keys := make(map[int64]struct{}, len(oldMap)+len(newMap))
	for k := range oldMap {
		keys[k] = struct{}{}
	}
	for k := range newMap {
		keys[k] = struct{}{}
	}
	out := make(map[int64]int64, len(keys))
	for k := range keys {
		delta := int64(newMap[k]) - int64(oldMap[k])
		if delta != 0 {
			out[k] = delta
		}
	}
	return out
}

// SumDurationMap totals contributions across hour buckets.
func SumDurationMap(m map[int64]uint64) uint64 {
	var sum uint64
	for _, v := range m {
		sum += v
	}
	return sum
}

// SelectEffectiveCosts selects effective cost rows for a cost_scope_key.
//
// Calculated prices: group by request identity (EventRowID) then sum by
// model_key+currency (2+3=5). Provider-reported bills replace the whole scope
// (latest reported wins) so 2+3 then bill 4 → 4, not 9 or 3.
func SelectEffectiveCosts(facts []CostFact) []EffectiveCost {
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
	for i := range ordered {
		f := &ordered[i]
		if f.Source == v2.CostSourceProviderReported {
			bestReported = f
		}
	}
	if bestReported != nil {
		return []EffectiveCost{{
			ModelKey:     bestReported.ModelKey,
			Currency:     bestReported.Currency,
			Units:        bestReported.Units,
			Source:       v2.CostSourceProviderReported,
			IsReported:   true,
			RequestCount: 1,
		}}
	}

	// Latest fact per request identity (EventRowID), then sum by model+currency.
	type reqKey struct {
		id int64
	}
	latestByRequest := map[reqKey]CostFact{}
	for _, f := range ordered {
		if f.Source != v2.CostSourceCalculatedPrice {
			continue
		}
		k := reqKey{id: f.EventRowID}
		prev, ok := latestByRequest[k]
		if !ok || f.OccurredAt > prev.OccurredAt || (f.OccurredAt == prev.OccurredAt && f.EventRowID >= prev.EventRowID) {
			latestByRequest[k] = f
		}
	}
	type groupKey struct {
		model    uint64
		currency string
	}
	type groupAgg struct {
		units uint64
		count int64
	}
	groups := map[groupKey]*groupAgg{}
	for _, f := range latestByRequest {
		gk := groupKey{model: f.ModelKey, currency: f.Currency}
		g := groups[gk]
		if g == nil {
			g = &groupAgg{}
			groups[gk] = g
		}
		g.units += f.Units
		g.count++
	}
	out := make([]EffectiveCost, 0, len(groups))
	for gk, g := range groups {
		out = append(out, EffectiveCost{
			ModelKey:     gk.model,
			Currency:     gk.currency,
			Units:        g.units,
			Source:       v2.CostSourceCalculatedPrice,
			IsReported:   false,
			RequestCount: g.count,
		})
	}
	slices.SortFunc(out, func(a, b EffectiveCost) int {
		if c := cmp.Compare(a.ModelKey, b.ModelKey); c != 0 {
			return c
		}
		return cmp.Compare(a.Currency, b.Currency)
	})
	return out
}

// SelectEffectiveCost is a convenience wrapper used when callers expect a single
// effective row (same currency/model, or a reported bill). Multiple calculated
// model slices are merged only when they share model+currency; otherwise the
// first sorted row is returned — prefer SelectEffectiveCosts for deltas.
func SelectEffectiveCost(facts []CostFact) *EffectiveCost {
	costs := SelectEffectiveCosts(facts)
	if len(costs) == 0 {
		return nil
	}
	if len(costs) == 1 {
		c := costs[0]
		return &c
	}
	// Same-source calculated multi-model: return nil so callers use SelectEffectiveCosts.
	// Keep legacy single-pointer callers working when all rows share model+currency
	// (should already be one after grouping).
	c := costs[0]
	return &c
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
	var oldList, newList []EffectiveCost
	if oldCost != nil {
		oldList = []EffectiveCost{*oldCost}
	}
	if newCost != nil {
		newList = []EffectiveCost{*newCost}
	}
	return DiffEffectiveCosts(oldList, newList)
}

// DiffEffectiveCosts computes signed deltas across effective cost slices.
func DiffEffectiveCosts(oldCosts, newCosts []EffectiveCost) []CostDelta {
	type key struct {
		model    uint64
		currency string
	}
	acc := map[key]*CostDelta{}
	apply := func(c EffectiveCost, sign int64) {
		k := key{c.ModelKey, c.Currency}
		d := acc[k]
		if d == nil {
			d = &CostDelta{ModelKey: c.ModelKey, Currency: c.Currency}
			acc[k] = d
		}
		req := c.RequestCount
		if req == 0 {
			req = 1
		}
		d.CostKnownCount += sign
		if c.IsReported {
			d.ReportedUnits += sign * int64(c.Units)
			d.ReportedRequestCount += sign * req
		} else {
			d.EstimatedUnits += sign * int64(c.Units)
			d.EstimatedRequestCount += sign * req
		}
	}
	for _, c := range oldCosts {
		apply(c, -1)
	}
	for _, c := range newCosts {
		apply(c, 1)
	}
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

// BeijingDayBounds returns [start, end) ms for the Beijing calendar day of occurredAtMs.
func BeijingDayBounds(occurredAtMs int64) (dayStartMs, dayEndMs int64) {
	dayStartMs = domain.StartOfDay(time.UnixMilli(occurredAtMs)).UnixMilli()
	dayEndMs = domain.StartOfDay(time.UnixMilli(occurredAtMs)).AddDate(0, 0, 1).UnixMilli()
	return dayStartMs, dayEndMs
}
