package teams

import (
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"strings"
	"time"

	"tokendance/internal/domain"
)

const (
	AnalysisRuleVersion = "1"

	VisibilityNamed          uint32 = 1 << 0
	VisibilityClassification uint32 = 1 << 1
	VisibilityCost           uint32 = 1 << 2

	BucketUnsharedClassification = "unshared_classification"
	BucketUnknown                = "unknown"

	EventModelUsage = "model_usage_recorded"
	EventCostRecord = "cost_recorded"

	AccuracyExact       = "exact"
	AccuracyDerived     = "derived"
	AccuracyEstimated   = "estimated"
	AccuracyCorrelated  = "correlated"
	CostProviderReported = "provider_reported"
	CostEstimatedTable   = "estimated_price_table"

	AdapterExclusiveParts   = "fixture.exclusive_parts"
	AdapterInputOutputOnly  = "fixture.input_output_only"
	AdapterUnknownSemantics = "fixture.unknown"
	AdapterCostFinalTotal   = "fixture.cost_final_total"
	AdapterCostIncremental  = "fixture.cost_incremental"
	AdapterCostAmbiguous    = "fixture.cost_ambiguous"
)

type TokenDerivationMode int

const (
	TokenDeriveNone TokenDerivationMode = iota
	TokenDeriveInputOutput
	TokenDeriveExclusiveParts
)

type AdapterTokenProfile struct {
	Mode                 TokenDerivationMode
	CacheIncludedInInput bool
}

type CostSemantics int

const (
	CostSemanticsUnknown CostSemantics = iota
	CostRecordedIsFinalTotal
	CostRecordedIsIncremental
)

type MembershipFact struct {
	MembershipID string
	UserID       string
	TeamActive   bool
	UserActive   bool
	JoinedAt     time.Time
	EndedAt      *time.Time
}

type GrantWindow struct {
	Dimension domain.SharingDimension
	StartsAt  time.Time
	EndsAt    *time.Time
	RevokedAt *time.Time
}

func (g GrantWindow) Covers(at time.Time) bool {
	if at.Before(g.StartsAt) {
		return false
	}
	if g.EndsAt != nil && !at.Before(*g.EndsAt) {
		return false
	}
	if g.RevokedAt != nil && !at.Before(*g.RevokedAt) {
		return false
	}
	return true
}

type UsageFact struct {
	EventID        [32]byte
	UserID         string
	InstallationID string
	AdapterID      string
	AgentID        string
	ProviderID     string
	ModelID        string
	EventType      string
	Accuracy       string
	OccurredAt     time.Time
	SessionHash    *[32]byte
	TurnHash       *[32]byte
	TokenInput     *uint64
	TokenOutput    *uint64
	TokenCacheRead *uint64
	TokenCacheWrite *uint64
	TokenReasoning *uint64
	TokenTotal     *uint64
	CostAmount     *string
	CostCurrency   *string
	CostSource     *string
	CostIsFinalTotal  bool
	CostIsIncremental bool
}

type AuthorizationResult struct {
	Eligible bool
	Mask     uint32
}

func AdapterTokenProfileFor(adapterID string) AdapterTokenProfile {
	switch adapterID {
	case AdapterExclusiveParts:
		return AdapterTokenProfile{Mode: TokenDeriveExclusiveParts}
	case AdapterInputOutputOnly:
		return AdapterTokenProfile{Mode: TokenDeriveInputOutput, CacheIncludedInInput: true}
	case AdapterUnknownSemantics:
		return AdapterTokenProfile{Mode: TokenDeriveNone}
	default:
		return AdapterTokenProfile{Mode: TokenDeriveInputOutput}
	}
}

func AdapterCostSemanticsFor(adapterID string) CostSemantics {
	switch adapterID {
	case AdapterCostFinalTotal:
		return CostRecordedIsFinalTotal
	case AdapterCostIncremental:
		return CostRecordedIsIncremental
	case AdapterCostAmbiguous:
		return CostSemanticsUnknown
	default:
		return CostSemanticsUnknown
	}
}

func AuthorizeEvent(evt UsageFact, member MembershipFact, grants []GrantWindow) AuthorizationResult {
	if !member.TeamActive || !member.UserActive {
		return AuthorizationResult{}
	}
	if member.EndedAt != nil {
		return AuthorizationResult{}
	}
	if evt.UserID != member.UserID {
		return AuthorizationResult{}
	}
	if evt.OccurredAt.Before(member.JoinedAt) {
		return AuthorizationResult{}
	}
	if !dimensionCovered(grants, domain.SharingBase, evt.OccurredAt) {
		return AuthorizationResult{}
	}
	mask := uint32(0)
	if dimensionCovered(grants, domain.SharingNamed, evt.OccurredAt) {
		mask |= VisibilityNamed
	}
	if dimensionCovered(grants, domain.SharingClassification, evt.OccurredAt) {
		mask |= VisibilityClassification
	}
	if dimensionCovered(grants, domain.SharingCost, evt.OccurredAt) {
		mask |= VisibilityCost
	}
	return AuthorizationResult{Eligible: true, Mask: mask}
}

func dimensionCovered(grants []GrantWindow, dim domain.SharingDimension, at time.Time) bool {
	for _, g := range grants {
		if g.Dimension == dim && g.Covers(at) {
			return true
		}
	}
	return false
}

type NormalizedToken struct {
	Total      uint64
	Accuracy   string
	Supported  bool
	Empty      bool
}

func NormalizeToken(evt UsageFact) NormalizedToken {
	if evt.EventType != EventModelUsage {
		return NormalizedToken{}
	}
	if evt.Accuracy != AccuracyExact && evt.Accuracy != AccuracyDerived {
		return NormalizedToken{Accuracy: evt.Accuracy}
	}
	if evt.TokenTotal != nil {
		return NormalizedToken{Total: *evt.TokenTotal, Accuracy: evt.Accuracy, Supported: true, Empty: *evt.TokenTotal == 0}
	}
	profile := AdapterTokenProfileFor(evt.AdapterID)
	derived, ok := deriveTokenTotal(evt, profile)
	if !ok {
		return NormalizedToken{Accuracy: evt.Accuracy}
	}
	return NormalizedToken{Total: derived, Accuracy: AccuracyDerived, Supported: true, Empty: derived == 0}
}

func deriveTokenTotal(evt UsageFact, profile AdapterTokenProfile) (uint64, bool) {
	switch profile.Mode {
	case TokenDeriveNone:
		return 0, false
	case TokenDeriveExclusiveParts:
		if evt.TokenInput == nil && evt.TokenOutput == nil && evt.TokenCacheRead == nil && evt.TokenCacheWrite == nil && evt.TokenReasoning == nil {
			return 0, false
		}
		var total uint64
		if evt.TokenInput != nil {
			total += *evt.TokenInput
		}
		if evt.TokenOutput != nil {
			total += *evt.TokenOutput
		}
		if evt.TokenCacheRead != nil {
			total += *evt.TokenCacheRead
		}
		if evt.TokenCacheWrite != nil {
			total += *evt.TokenCacheWrite
		}
		if evt.TokenReasoning != nil {
			total += *evt.TokenReasoning
		}
		return total, true
	case TokenDeriveInputOutput:
		if evt.TokenInput == nil || evt.TokenOutput == nil {
			return 0, false
		}
		if !profile.CacheIncludedInInput && (evt.TokenCacheRead != nil || evt.TokenCacheWrite != nil || evt.TokenReasoning != nil) {
			// Line items exist but we cannot prove they are exclusive of input/output.
			return 0, false
		}
		return *evt.TokenInput + *evt.TokenOutput, true
	default:
		return 0, false
	}
}

type CostAssociationKey struct {
	UserID         string
	InstallationID string
	AgentID        string
	SessionHex     string
	TurnHex        string
	Currency       string
}

func (k CostAssociationKey) Valid() bool {
	if k.UserID == "" || k.InstallationID == "" || k.AgentID == "" || k.Currency == "" {
		return false
	}
	// Session is required. Turn hash alone must not bind events across devices.
	return k.SessionHex != ""
}

func associationKey(evt UsageFact) (CostAssociationKey, bool) {
	currency := ""
	if evt.CostCurrency != nil {
		currency = strings.ToUpper(strings.TrimSpace(*evt.CostCurrency))
	}
	key := CostAssociationKey{
		UserID:         evt.UserID,
		InstallationID: evt.InstallationID,
		AgentID:        evt.AgentID,
		Currency:       currency,
	}
	if evt.SessionHash != nil {
		key.SessionHex = hex.EncodeToString(evt.SessionHash[:])
	}
	if evt.TurnHash != nil {
		key.TurnHex = hex.EncodeToString(evt.TurnHash[:])
	}
	return key, key.Valid()
}

type CostContribution struct {
	Currency              string
	ReportedAmount        string
	EstimatedAmount       string
	PotentiallyOverlapping bool
	Unattributed          bool
	SourceRecordedOnly    bool
	CoveredUsageIDs       [][32]byte
}

type CostNormalizationResult struct {
	Reported           []domain.TeamCostAmount
	EstimatedUncovered []domain.TeamCostAmount
	ReportedUsageIDs   map[string]struct{}
	EstimatedUsageIDs  map[string]struct{}
	EligibleUsageIDs   map[string]struct{}
	UnattributedCount  uint64
	OverlappingCount   uint64
	Contributions      []CostContribution
}

type costGroup struct {
	key           CostAssociationKey
	usages        []UsageFact
	costRecords   []UsageFact
	usageWithCost []UsageFact
}

func DedupCosts(events []UsageFact, authorizations map[string]AuthorizationResult) CostNormalizationResult {
	out := CostNormalizationResult{
		ReportedUsageIDs:  map[string]struct{}{},
		EstimatedUsageIDs: map[string]struct{}{},
		EligibleUsageIDs:  map[string]struct{}{},
	}
	groups := map[CostAssociationKey]*costGroup{}

	for _, evt := range events {
		auth := authorizations[eventKey(evt.EventID)]
		if evt.EventType == EventModelUsage {
			token := NormalizeToken(evt)
			if auth.Eligible && token.Supported {
				out.EligibleUsageIDs[eventKey(evt.EventID)] = struct{}{}
			}
		}
		costAuthorized := auth.Eligible && auth.Mask&VisibilityCost != 0
		if evt.CostAmount == nil && evt.EventType != EventCostRecord {
			continue
		}
		if !costAuthorized && evt.EventType == EventCostRecord {
			continue
		}
		if evt.EventType == EventModelUsage && !costAuthorized {
			continue
		}
		key, ok := associationKey(evt)
		if !ok {
			if evt.CostAmount != nil && costAuthorized {
				out.UnattributedCount++
				out.Contributions = append(out.Contributions, CostContribution{
					Currency:           currencyOrEmpty(evt.CostCurrency),
					ReportedAmount:     reportedOrEmpty(evt),
					EstimatedAmount:    estimatedOrEmpty(evt),
					Unattributed:       true,
					SourceRecordedOnly: evt.EventType == EventCostRecord,
				})
			}
			continue
		}
		g := groups[key]
		if g == nil {
			g = &costGroup{key: key}
			groups[key] = g
		}
		switch evt.EventType {
		case EventCostRecord:
			g.costRecords = append(g.costRecords, evt)
		case EventModelUsage:
			g.usages = append(g.usages, evt)
			if evt.CostAmount != nil {
				g.usageWithCost = append(g.usageWithCost, evt)
			}
		}
	}

	reportedTotals := map[string]*big.Rat{}
	estimatedTotals := map[string]*big.Rat{}

	for _, g := range groups {
		adapterID := firstAdapterFromSlices(g.costRecords, g.usages)
		semantics := AdapterCostSemanticsFor(adapterID)
		contrib := normalizeGroupedCosts(g.key, g.usages, g.costRecords, g.usageWithCost, semantics)
		out.Contributions = append(out.Contributions, contrib)
		if contrib.Unattributed {
			out.UnattributedCount++
		}
		if contrib.PotentiallyOverlapping {
			out.OverlappingCount++
			continue
		}
		if contrib.ReportedAmount != "" {
			addCostMap(reportedTotals, contrib.Currency, contrib.ReportedAmount)
			for _, id := range contrib.CoveredUsageIDs {
				out.ReportedUsageIDs[eventKey(id)] = struct{}{}
			}
		}
		if contrib.EstimatedAmount != "" && contrib.ReportedAmount == "" {
			addCostMap(estimatedTotals, contrib.Currency, contrib.EstimatedAmount)
			for _, id := range contrib.CoveredUsageIDs {
				if _, ok := out.ReportedUsageIDs[eventKey(id)]; !ok {
					out.EstimatedUsageIDs[eventKey(id)] = struct{}{}
				}
			}
		}
	}

	out.Reported = costMapToList(reportedTotals)
	out.EstimatedUncovered = costMapToList(estimatedTotals)
	return out
}

func firstAdapterFromSlices(costRecords, usages []UsageFact) string {
	if len(costRecords) > 0 {
		return costRecords[0].AdapterID
	}
	if len(usages) > 0 {
		return usages[0].AdapterID
	}
	return ""
}

func normalizeGroupedCosts(key CostAssociationKey, usages, costRecords, usageWithCost []UsageFact, semantics CostSemantics) CostContribution {
	contrib := CostContribution{Currency: key.Currency}
	if !key.Valid() {
		contrib.Unattributed = true
		return contrib
	}

	var covered [][32]byte
	for _, u := range usages {
		if NormalizeToken(u).Supported {
			covered = append(covered, u.EventID)
		}
	}

	reportedParts := make([]string, 0)
	estimatedParts := make([]string, 0)
	finalTotals := make([]string, 0)
	incrementals := make([]string, 0)
	ambiguous := false

	for _, rec := range costRecords {
		if rec.CostAmount == nil {
			continue
		}
		src := ""
		if rec.CostSource != nil {
			src = *rec.CostSource
		}
		final := rec.CostIsFinalTotal || semantics == CostRecordedIsFinalTotal
		incr := rec.CostIsIncremental || semantics == CostRecordedIsIncremental
		switch {
		case src == CostProviderReported && final:
			finalTotals = append(finalTotals, *rec.CostAmount)
		case src == CostProviderReported && incr:
			incrementals = append(incrementals, *rec.CostAmount)
		case src == CostProviderReported && semantics == CostSemanticsUnknown:
			ambiguous = true
			reportedParts = append(reportedParts, *rec.CostAmount)
		case src == CostEstimatedTable:
			estimatedParts = append(estimatedParts, *rec.CostAmount)
		default:
			if src == CostProviderReported {
				ambiguous = true
				reportedParts = append(reportedParts, *rec.CostAmount)
			}
		}
	}
	for _, rec := range usageWithCost {
		if rec.CostAmount == nil {
			continue
		}
		src := ""
		if rec.CostSource != nil {
			src = *rec.CostSource
		}
		if src == CostProviderReported {
			if len(finalTotals) > 0 || len(incrementals) > 0 || semantics == CostRecordedIsFinalTotal {
				continue
			}
			if semantics == CostSemanticsUnknown && len(costRecords) > 0 {
				ambiguous = true
				continue
			}
			reportedParts = append(reportedParts, *rec.CostAmount)
		} else if src == CostEstimatedTable {
			estimatedParts = append(estimatedParts, *rec.CostAmount)
		}
	}

	if ambiguous && len(finalTotals) == 0 && len(incrementals) == 0 {
		contrib.PotentiallyOverlapping = true
		contrib.CoveredUsageIDs = covered
		return contrib
	}
	if len(finalTotals) > 1 {
		contrib.PotentiallyOverlapping = true
		contrib.CoveredUsageIDs = covered
		return contrib
	}
	if len(finalTotals) == 1 {
		contrib.ReportedAmount = NormalizeCostAmount(finalTotals[0])
		contrib.CoveredUsageIDs = covered
		return contrib
	}
	if len(incrementals) > 0 {
		sum, ok := sumCostAmounts(incrementals)
		if !ok {
			contrib.PotentiallyOverlapping = true
			contrib.CoveredUsageIDs = covered
			return contrib
		}
		contrib.ReportedAmount = sum
		contrib.CoveredUsageIDs = covered
		return contrib
	}
	if len(reportedParts) > 0 {
		sum, ok := sumCostAmounts(reportedParts)
		if !ok {
			contrib.PotentiallyOverlapping = true
			contrib.CoveredUsageIDs = covered
			return contrib
		}
		contrib.ReportedAmount = sum
		contrib.CoveredUsageIDs = covered
		return contrib
	}
	if len(estimatedParts) > 0 {
		sum, ok := sumCostAmounts(estimatedParts)
		if !ok {
			contrib.PotentiallyOverlapping = true
			contrib.CoveredUsageIDs = covered
			return contrib
		}
		contrib.EstimatedAmount = sum
		contrib.CoveredUsageIDs = covered
	}
	if len(costRecords) > 0 && len(usages) == 0 {
		contrib.SourceRecordedOnly = true
		if contrib.ReportedAmount == "" && contrib.EstimatedAmount == "" && costRecords[0].CostAmount != nil && *costRecords[0].CostAmount != "" {
			if costRecords[0].CostSource != nil && *costRecords[0].CostSource == CostEstimatedTable {
				contrib.EstimatedAmount = NormalizeCostAmount(*costRecords[0].CostAmount)
			} else {
				contrib.ReportedAmount = NormalizeCostAmount(*costRecords[0].CostAmount)
			}
		}
	}
	return contrib
}

func DedupCostsFromFacts(events []UsageFact, authorizations map[string]AuthorizationResult) CostNormalizationResult {
	return DedupCosts(events, authorizations)
}

func CostCoverage(result CostNormalizationResult) domain.TeamCostCoverage {
	eligible := uint64(len(result.EligibleUsageIDs))
	reported := uint64(0)
	for id := range result.ReportedUsageIDs {
		if _, ok := result.EligibleUsageIDs[id]; ok {
			reported++
		}
	}
	coverage := domain.TeamCostCoverage{
		ReportedUsageEvents: formatUint(reported),
		EligibleUsageEvents: formatUint(eligible),
	}
	if eligible == 0 && result.UnattributedCount > 0 {
		coverage.ReportedUsageEvents = "0"
		coverage.EligibleUsageEvents = "0"
	}
	return coverage
}

func NormalizeCostAmount(raw string) string {
	r, ok := parseDecimal(raw)
	if !ok {
		return ""
	}
	return formatDecimal(r, 8)
}

func eventKey(id [32]byte) string {
	return hex.EncodeToString(id[:])
}

func currencyOrEmpty(c *string) string {
	if c == nil {
		return ""
	}
	return strings.ToUpper(strings.TrimSpace(*c))
}

func reportedOrEmpty(evt UsageFact) string {
	if evt.CostAmount == nil || evt.CostSource == nil || *evt.CostSource != CostProviderReported {
		return ""
	}
	return NormalizeCostAmount(*evt.CostAmount)
}

func estimatedOrEmpty(evt UsageFact) string {
	if evt.CostAmount == nil || evt.CostSource == nil || *evt.CostSource != CostEstimatedTable {
		return ""
	}
	return NormalizeCostAmount(*evt.CostAmount)
}

func addCostMap(dst map[string]*big.Rat, currency, amount string) {
	r, ok := parseDecimal(amount)
	if !ok {
		return
	}
	if dst[currency] == nil {
		dst[currency] = new(big.Rat)
	}
	dst[currency].Add(dst[currency], r)
}

func costMapToList(m map[string]*big.Rat) []domain.TeamCostAmount {
	if len(m) == 0 {
		return []domain.TeamCostAmount{}
	}
	out := make([]domain.TeamCostAmount, 0, len(m))
	for cur, r := range m {
		out = append(out, domain.TeamCostAmount{Currency: cur, Amount: formatDecimal(r, 8)})
	}
	return out
}

func sumCostAmounts(parts []string) (string, bool) {
	total := new(big.Rat)
	for _, p := range parts {
		r, ok := parseDecimal(p)
		if !ok {
			return "", false
		}
		total.Add(total, r)
	}
	return formatDecimal(total, 8), true
}

func parseDecimal(s string) (*big.Rat, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}
	r := new(big.Rat)
	if _, ok := r.SetString(s); !ok {
		return nil, false
	}
	return r, true
}

func formatDecimal(r *big.Rat, scale int) string {
	if r == nil {
		return "0." + strings.Repeat("0", scale)
	}
	n := new(big.Int).Set(r.Num())
	d := new(big.Int).Set(r.Denom())
	neg := n.Sign() < 0
	n.Abs(n)
	quo := new(big.Int)
	rem := new(big.Int)
	quo.QuoRem(n, d, rem)
	if scale == 0 {
		if neg && quo.Sign() != 0 {
			return "-" + quo.String()
		}
		return quo.String()
	}
	scalePow := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	frac := new(big.Int).Mul(rem, scalePow)
	frac.Quo(frac, d)
	fracStr := frac.String()
	if len(fracStr) < scale {
		fracStr = strings.Repeat("0", scale-len(fracStr)) + fracStr
	}
	out := quo.String() + "." + fracStr
	if neg && (quo.Sign() != 0 || frac.Sign() != 0) {
		return "-" + out
	}
	return out
}

func formatUint(v uint64) string {
	return new(big.Int).SetUint64(v).String()
}

func AddIntDecimal(a, b string) string {
	x, okA := new(big.Int).SetString(emptyZero(a), 10)
	y, okB := new(big.Int).SetString(emptyZero(b), 10)
	if !okA || !okB {
		return a
	}
	return x.Add(x, y).String()
}

func emptyZero(s string) string {
	if strings.TrimSpace(s) == "" {
		return "0"
	}
	return s
}

func StableEventID(seed string) [32]byte {
	return sha256.Sum256([]byte(seed))
}

func ClassificationBucket(mask uint32, providerID, modelID string) (provider, model, bucket string) {
	if mask&VisibilityClassification == 0 {
		return "", "", BucketUnsharedClassification
	}
	if strings.TrimSpace(providerID) == "" && strings.TrimSpace(modelID) == "" {
		return BucketUnknown, BucketUnknown, BucketUnknown
	}
	if strings.TrimSpace(modelID) == "" {
		return providerID, BucketUnknown, BucketUnknown
	}
	return providerID, modelID, ""
}
