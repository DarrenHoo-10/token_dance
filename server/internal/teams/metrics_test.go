package teams

import (
	"testing"
	"time"

	"tokendance/internal/domain"
)

func ptrUint(v uint64) *uint64 { return &v }
func ptrStr(v string) *string  { return &v }

func TestCurrentSharingIncludesHistory(t *testing.T) {
	now := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	member := MembershipFact{UserID: "u", TeamActive: true, UserActive: true, JoinedAt: now}
	event := UsageFact{UserID: "u", OccurredAt: now.Add(-7 * 24 * time.Hour)}
	grants := []GrantWindow{{Dimension: domain.SharingBase, StartsAt: now}, {Dimension: domain.SharingNamed, StartsAt: now}}
	got := AuthorizeEvent(event, member, grants)
	if !got.Eligible || got.Mask != VisibilityNamed {
		t.Fatalf("history must follow current grants: %+v", got)
	}
	grants[1].RevokedAt = &now
	if got := AuthorizeEvent(event, member, grants); !got.Eligible || got.Mask != 0 {
		t.Fatalf("revoked name must hide history: %+v", got)
	}
	grants[0].EndsAt = &now
	if AuthorizeEvent(event, member, grants).Eligible {
		t.Fatal("closed base must hide all history")
	}
	grants[0].EndsAt = nil
	member.EndedAt = &now
	if AuthorizeEvent(event, member, grants).Eligible {
		t.Fatal("former member must be excluded")
	}
	member.EndedAt = nil
	event.UserID = "other"
	if AuthorizeEvent(event, member, grants).Eligible {
		t.Fatal("other user must be excluded")
	}
	member.TeamActive = false
	event.UserID = "u"
	if AuthorizeEvent(event, member, grants).Eligible {
		t.Fatal("inactive team must be excluded")
	}
}

func TestNormalizeTokenFixtures(t *testing.T) {
	t.Run("prefer standardized total", func(t *testing.T) {
		evt := UsageFact{
			EventType: EventModelUsage, Accuracy: AccuracyExact, AdapterID: AdapterExclusiveParts,
			TokenInput: ptrUint(10), TokenOutput: ptrUint(5), TokenTotal: ptrUint(12),
		}
		got := NormalizeToken(evt)
		if !got.Supported || got.Total != 12 || got.Accuracy != AccuracyExact {
			t.Fatalf("unexpected %+v", got)
		}
	})
	t.Run("exclusive parts derive", func(t *testing.T) {
		evt := UsageFact{
			EventType: EventModelUsage, Accuracy: AccuracyExact, AdapterID: AdapterExclusiveParts,
			TokenInput: ptrUint(10), TokenOutput: ptrUint(5), TokenCacheRead: ptrUint(3), TokenReasoning: ptrUint(2),
		}
		got := NormalizeToken(evt)
		if !got.Supported || got.Total != 20 || got.Accuracy != AccuracyDerived {
			t.Fatalf("unexpected %+v", got)
		}
	})
	t.Run("cache included in input is not added", func(t *testing.T) {
		evt := UsageFact{
			EventType: EventModelUsage, Accuracy: AccuracyExact, AdapterID: AdapterInputOutputOnly,
			TokenInput: ptrUint(100), TokenOutput: ptrUint(20), TokenCacheRead: ptrUint(40),
		}
		got := NormalizeToken(evt)
		if !got.Supported || got.Total != 120 {
			t.Fatalf("expected input+output only, got %+v", got)
		}
	})
	t.Run("unknown semantics without total is unsupported", func(t *testing.T) {
		evt := UsageFact{
			EventType: EventModelUsage, Accuracy: AccuracyExact, AdapterID: AdapterUnknownSemantics,
			TokenInput: ptrUint(1), TokenOutput: ptrUint(1),
		}
		got := NormalizeToken(evt)
		if got.Supported {
			t.Fatalf("unknown adapter must not invent tokens: %+v", got)
		}
	})
	t.Run("default refuses cache without total", func(t *testing.T) {
		evt := UsageFact{
			EventType: EventModelUsage, Accuracy: AccuracyExact, AdapterID: "other.adapter",
			TokenInput: ptrUint(10), TokenOutput: ptrUint(5), TokenCacheRead: ptrUint(3),
		}
		got := NormalizeToken(evt)
		if got.Supported {
			t.Fatalf("must not add cache globally: %+v", got)
		}
	})
	t.Run("estimated accuracy is quality only", func(t *testing.T) {
		evt := UsageFact{EventType: EventModelUsage, Accuracy: AccuracyEstimated, TokenTotal: ptrUint(9)}
		got := NormalizeToken(evt)
		if got.Supported {
			t.Fatal("estimated events stay out of exact totals")
		}
	})
	t.Run("true zero is available", func(t *testing.T) {
		evt := UsageFact{EventType: EventModelUsage, Accuracy: AccuracyExact, TokenTotal: ptrUint(0)}
		got := NormalizeToken(evt)
		if !got.Supported || !got.Empty {
			t.Fatalf("zero must remain available: %+v", got)
		}
	})
}

func TestDedupCostsFixtures(t *testing.T) {
	session := StableEventID("session-a")
	turn := StableEventID("turn-a")
	usageID := StableEventID("usage-1")
	costID := StableEventID("cost-1")
	currency := "USD"
	authAll := AuthorizationResult{Eligible: true, Mask: VisibilityNamed | VisibilityClassification | VisibilityCost}
	auths := map[string]AuthorizationResult{
		eventKey(usageID): authAll,
		eventKey(costID):  authAll,
	}

	baseUsage := UsageFact{
		EventID: usageID, UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostFinalTotal,
		AgentID: "codex", EventType: EventModelUsage, Accuracy: AccuracyExact, TokenTotal: ptrUint(100),
		SessionHash: &session, TurnHash: &turn, CostCurrency: &currency,
	}

	t.Run("final reported total wins over usage fee", func(t *testing.T) {
		usage := baseUsage
		usage.CostAmount = ptrStr("3.00")
		usage.CostSource = ptrStr(CostProviderReported)
		cost := UsageFact{
			EventID: costID, UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostFinalTotal,
			AgentID: "codex", EventType: EventCostRecord, SessionHash: &session, TurnHash: &turn,
			CostAmount: ptrStr("12.34000000"), CostCurrency: &currency, CostSource: ptrStr(CostProviderReported),
			CostIsFinalTotal: true,
		}
		got := DedupCosts([]UsageFact{usage, cost}, auths)
		if len(got.Reported) != 1 || got.Reported[0].Amount != "12.34000000" {
			t.Fatalf("reported=%v", got.Reported)
		}
		if len(got.EstimatedUncovered) != 0 {
			t.Fatalf("estimated should be empty, got %v", got.EstimatedUncovered)
		}
		if _, ok := got.ReportedUsageIDs[eventKey(usageID)]; !ok {
			t.Fatal("usage should be covered by reported cost")
		}
	})

	t.Run("incremental sums proven non-overlapping", func(t *testing.T) {
		costA := UsageFact{
			EventID: StableEventID("inc-a"), UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostIncremental,
			AgentID: "codex", EventType: EventCostRecord, SessionHash: &session, TurnHash: &turn,
			CostAmount: ptrStr("1.25"), CostCurrency: &currency, CostSource: ptrStr(CostProviderReported),
			CostIsIncremental: true,
		}
		costB := costA
		costB.EventID = StableEventID("inc-b")
		costB.CostAmount = ptrStr("2.75")
		usage := baseUsage
		usage.AdapterID = AdapterCostIncremental
		got := DedupCosts([]UsageFact{usage, costA, costB}, map[string]AuthorizationResult{
			eventKey(usageID): authAll, eventKey(costA.EventID): authAll, eventKey(costB.EventID): authAll,
		})
		if len(got.Reported) != 1 || got.Reported[0].Amount != "4.00000000" {
			t.Fatalf("reported=%v", got.Reported)
		}
	})

	t.Run("ambiguous overlap excluded from additive subtotal", func(t *testing.T) {
		usage := baseUsage
		usage.AdapterID = AdapterCostAmbiguous
		usage.CostAmount = ptrStr("5.00")
		usage.CostSource = ptrStr(CostProviderReported)
		cost := UsageFact{
			EventID: costID, UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostAmbiguous,
			AgentID: "codex", EventType: EventCostRecord, SessionHash: &session, TurnHash: &turn,
			CostAmount: ptrStr("5.00"), CostCurrency: &currency, CostSource: ptrStr(CostProviderReported),
		}
		got := DedupCosts([]UsageFact{usage, cost}, auths)
		if len(got.Reported) != 0 {
			t.Fatalf("overlapping group must not add, got %v", got.Reported)
		}
		if got.OverlappingCount == 0 {
			t.Fatal("expected overlapping quality count")
		}
	})

	t.Run("reported group excludes estimated", func(t *testing.T) {
		usage := baseUsage
		est := UsageFact{
			EventID: StableEventID("est-1"), UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostFinalTotal,
			AgentID: "codex", EventType: EventCostRecord, SessionHash: &session, TurnHash: &turn,
			CostAmount: ptrStr("9.99"), CostCurrency: &currency, CostSource: ptrStr(CostEstimatedTable),
		}
		cost := UsageFact{
			EventID: costID, UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostFinalTotal,
			AgentID: "codex", EventType: EventCostRecord, SessionHash: &session, TurnHash: &turn,
			CostAmount: ptrStr("1.00"), CostCurrency: &currency, CostSource: ptrStr(CostProviderReported),
			CostIsFinalTotal: true,
		}
		got := DedupCosts([]UsageFact{usage, est, cost}, map[string]AuthorizationResult{
			eventKey(usageID): authAll, eventKey(est.EventID): authAll, eventKey(costID): authAll,
		})
		if len(got.EstimatedUncovered) != 0 {
			t.Fatalf("estimated must not mix into a reported group: %v", got.EstimatedUncovered)
		}
		if len(got.Reported) != 1 || got.Reported[0].Amount != "1.00000000" {
			t.Fatalf("reported=%v", got.Reported)
		}
	})

	t.Run("cross-device turn-only does not associate", func(t *testing.T) {
		usage := baseUsage
		usage.SessionHash = nil
		cost := UsageFact{
			EventID: costID, UserID: "u1", InstallationID: "ins_other", AdapterID: AdapterCostFinalTotal,
			AgentID: "codex", EventType: EventCostRecord, TurnHash: &turn,
			CostAmount: ptrStr("8.00"), CostCurrency: &currency, CostSource: ptrStr(CostProviderReported),
			CostIsFinalTotal: true,
		}
		got := DedupCosts([]UsageFact{usage, cost}, auths)
		if got.UnattributedCount == 0 && len(got.Reported) != 0 {
			// cost without session+same installation is unattributed
			t.Fatalf("cross-device turn hash must not merge: reported=%v unattributed=%d", got.Reported, got.UnattributedCount)
		}
	})

	t.Run("cost-only is source recorded and does not inflate usage denominator", func(t *testing.T) {
		cost := UsageFact{
			EventID: costID, UserID: "u1", InstallationID: "ins_1", AdapterID: AdapterCostFinalTotal,
			AgentID: "codex", EventType: EventCostRecord, SessionHash: &session, TurnHash: &turn,
			CostAmount: ptrStr("2.00"), CostCurrency: &currency, CostSource: ptrStr(CostProviderReported),
			CostIsFinalTotal: true,
		}
		got := DedupCosts([]UsageFact{cost}, map[string]AuthorizationResult{eventKey(costID): authAll})
		if len(got.EligibleUsageIDs) != 0 {
			t.Fatal("cost-only must not add usage denominator")
		}
		if got.UnattributedCount == 0 && len(got.Reported) == 0 {
			t.Fatal("cost-only amount should still be retained as source recorded")
		}
		cov := CostCoverage(got)
		if cov.EligibleUsageEvents != "0" {
			t.Fatalf("coverage denominator=%s", cov.EligibleUsageEvents)
		}
	})

	t.Run("unshared cost stays in denominator only", func(t *testing.T) {
		usage := baseUsage
		authsNoCost := map[string]AuthorizationResult{eventKey(usageID): {Eligible: true, Mask: VisibilityNamed}}
		got := DedupCosts([]UsageFact{usage}, authsNoCost)
		if len(got.EligibleUsageIDs) != 1 {
			t.Fatal("usage without cost grant remains eligible")
		}
		if len(got.Reported) != 0 {
			t.Fatal("cost must not leak without grant")
		}
		cov := CostCoverage(got)
		if cov.EligibleUsageEvents != "1" || cov.ReportedUsageEvents != "0" {
			t.Fatalf("coverage %+v", cov)
		}
	})
}

func TestClassificationBucket(t *testing.T) {
	p, m, b := ClassificationBucket(0, "openai", "gpt")
	if b != BucketUnsharedClassification || p != "" || m != "" {
		t.Fatalf("unshared got %s %s %s", p, m, b)
	}
	p, m, b = ClassificationBucket(VisibilityClassification, "", "")
	if b != BucketUnknown {
		t.Fatalf("unknown bucket %s/%s/%s", p, m, b)
	}
}
