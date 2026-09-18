package leaderboard

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store     store.LeaderboardStore
	community store.CommunityStatsStore
}

func NewService(st store.Store) *Service {
	return &Service{
		store:     st.Leaderboard(),
		community: st.CommunityStats(),
	}
}

func (s *Service) GetLeaderboards(ctx context.Context, boardKey, window, metric string, cursor *string, limit int) (*domain.LeaderboardResponse, error) {
	return s.Query(ctx, store.LeaderboardQuery{
		BoardKey: boardKey,
		Window:   window,
		Metric:   metric,
		Cursor:   cursor,
		Limit:    limit,
	})
}

func (s *Service) Query(ctx context.Context, q store.LeaderboardQuery) (*domain.LeaderboardResponse, error) {
	if q.BoardKey == "" {
		q.BoardKey = "global"
	}
	if q.Window == "" {
		q.Window = "30d"
	}
	if q.Metric == "" {
		q.Metric = "tokens"
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 50
	}
	if q.BoardKey == "global" && q.Metric == "tokens" && q.Cursor != nil {
		after, err := strconv.Atoi(*q.Cursor)
		if err != nil || after < 0 || after >= 1000 {
			return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidCursor", "cursor must be within the top 1000", nil, domain.ErrInvalidArgument)
		}
	}
	result, err := s.store.GetLeaderboardView(ctx, q)
	if errors.Is(err, domain.ErrInvalidArgument) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidArgument", "invalid leaderboard query", nil, err)
	}
	return result, err
}

// GetCommunityStats serves precomputed community totals for the same
// today/7d/30d/all window as the leaderboard. Multi-day windows sum the
// worker's daily rows; unique developers are counted across the window.
// A window with no precomputed days yields omitted fields so clients can
// show an empty state instead of a fabricated zero.
func (s *Service) GetCommunityStats(ctx context.Context, now time.Time, window string) (*domain.CommunityStatsResponse, error) {
	if window == "" {
		window = "today"
	}
	from, to, err := domain.WindowInclusiveDates(window, now)
	if err != nil {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidArgument", "invalid community stats window", nil, err)
	}
	today := domain.DayDate(now)
	rows, err := s.community.ListCommunityDailyStats(ctx, from, to)
	if err != nil {
		return nil, err
	}
	current := composeCommunityRange(rows)
	if current == nil {
		return &domain.CommunityStatsResponse{MetricDate: today, Window: window, Timezone: domain.DayTZName}, nil
	}
	if window != "today" {
		developers, err := s.community.CountActiveDevelopers(ctx, from, to)
		if err != nil {
			return nil, err
		}
		current.Developers = developers
	}
	costAmount, costs := communityCostProjection(*current)
	response := &domain.CommunityStatsResponse{
		MetricDate:   today,
		Window:       window,
		Timezone:     domain.DayTZName,
		Tokens:       uint64String(current.TokensTotal),
		Developers:   &current.Developers,
		CodeLines:    uint64String(current.CodeLines),
		Interactions: uint64String(current.Interactions),
		CostAmount:   costAmount,
		Costs:        costs,
		ComputedAt:   &current.ComputedAt,
	}
	if prevFrom, prevTo, ok := domain.PreviousWindowInclusiveDates(window, now); ok {
		previous, err := s.communityRangeTotals(ctx, window, prevFrom, prevTo)
		if err != nil {
			return nil, err
		}
		if previous != nil {
			response.Deltas = &domain.CommunityStatsDeltaDTO{
				Tokens:       deltaPct(current.TokensTotal, previous.TokensTotal),
				Developers:   deltaPct(current.Developers, previous.Developers),
				CodeLines:    deltaPct(current.CodeLines, previous.CodeLines),
				Interactions: deltaPct(current.Interactions, previous.Interactions),
				CostAmount:   communityCostDelta(*current, *previous),
			}
		}
	}
	harnessBase := current.TokensTotal
	if window != "today" {
		todayRow, err := s.community.GetCommunityDailyStats(ctx, today)
		if err != nil {
			return nil, err
		}
		harnessBase = 0
		if todayRow != nil {
			harnessBase = todayRow.TokensTotal
		}
	}
	harnesses, err := s.community.GetCommunityHarnessShares(ctx, today, 5)
	if err != nil {
		return nil, err
	}
	for _, harness := range harnesses {
		share := float64(0)
		if harnessBase > 0 {
			share = math.Round(float64(harness.TokensTotal)/float64(harnessBase)*1000) / 10
		}
		response.Harnesses = append(response.Harnesses, domain.CommunityHarnessDTO{
			AgentID:  harness.AgentID,
			Label:    harness.Label,
			Tokens:   uint64String(harness.TokensTotal),
			SharePct: &share,
		})
	}
	return response, nil
}

func (s *Service) communityRangeTotals(ctx context.Context, window, from, to string) (*store.CommunityDailyTotals, error) {
	rows, err := s.community.ListCommunityDailyStats(ctx, from, to)
	if err != nil {
		return nil, err
	}
	totals := composeCommunityRange(rows)
	if totals == nil {
		return nil, nil
	}
	if window != "today" {
		developers, err := s.community.CountActiveDevelopers(ctx, from, to)
		if err != nil {
			return nil, err
		}
		totals.Developers = developers
	}
	return totals, nil
}

// composeCommunityRange sums additive daily totals. Developers stay as the
// busiest day until the caller replaces them with a unique window count.
func composeCommunityRange(rows []store.CommunityDailyTotals) *store.CommunityDailyTotals {
	if len(rows) == 0 {
		return nil
	}
	out := store.CommunityDailyTotals{
		MetricDate: rows[0].MetricDate,
		Developers: rows[0].Developers,
		ComputedAt: rows[0].ComputedAt,
	}
	costByCurrency := map[string]float64{}
	var currencies []string
	legacyCost := 0.0
	hasLegacy := false
	hasCosts := false
	for _, row := range rows {
		out.TokensTotal += row.TokensTotal
		out.CodeLines += row.CodeLines
		out.Interactions += row.Interactions
		if row.Developers > out.Developers {
			out.Developers = row.Developers
		}
		if row.MetricDate > out.MetricDate {
			out.MetricDate = row.MetricDate
		}
		if row.ComputedAt.After(out.ComputedAt) {
			out.ComputedAt = row.ComputedAt
		}
		if len(row.Costs) > 0 {
			hasCosts = true
			for _, cost := range row.Costs {
				if _, seen := costByCurrency[cost.Currency]; !seen {
					currencies = append(currencies, cost.Currency)
				}
				costByCurrency[cost.Currency] += cost.Amount
			}
			continue
		}
		if row.CostAmount != 0 {
			hasLegacy = true
			legacyCost += row.CostAmount
		}
	}
	if hasCosts {
		sort.Strings(currencies)
		out.Costs = make([]store.CommunityCost, 0, len(currencies))
		for _, currency := range currencies {
			out.Costs = append(out.Costs, store.CommunityCost{Currency: currency, Amount: costByCurrency[currency]})
		}
	} else if hasLegacy {
		out.CostAmount = legacyCost
	}
	return &out
}

// deltaPct returns (current-previous)/previous in percent with one decimal.
// nil when the previous day is missing or zero — never an invented number.
func deltaPct(current, previous uint64) *float64 {
	if previous == 0 {
		return nil
	}
	pct := math.Round((float64(current)-float64(previous))/float64(previous)*1000) / 10
	return &pct
}

func uint64String(value uint64) *string {
	text := strconv.FormatUint(value, 10)
	return &text
}

func roundedCost(amount float64) *float64 {
	rounded := math.Round(amount*100) / 100
	return &rounded
}

func communityCostDTOs(costs []store.CommunityCost) []domain.CommunityCostDTO {
	if len(costs) == 0 {
		return nil
	}
	out := make([]domain.CommunityCostDTO, len(costs))
	for i, c := range costs {
		out[i] = domain.CommunityCostDTO{
			Amount:   math.Round(c.Amount*100) / 100,
			Currency: c.Currency,
		}
	}
	return out
}

// communityCostProjection returns a scalar only when a single currency exists.
// Multiple currencies keep Costs and omit CostAmount (no FX merge).
func communityCostProjection(totals store.CommunityDailyTotals) (*float64, []domain.CommunityCostDTO) {
	costs := communityCostDTOs(totals.Costs)
	switch len(totals.Costs) {
	case 0:
		return roundedCost(totals.CostAmount), nil
	case 1:
		return roundedCost(totals.Costs[0].Amount), costs
	default:
		return nil, costs
	}
}

func communityCostDelta(current, previous store.CommunityDailyTotals) *float64 {
	if len(current.Costs) > 1 || len(previous.Costs) > 1 {
		return nil
	}
	if len(current.Costs) == 1 && len(previous.Costs) == 1 {
		if current.Costs[0].Currency != previous.Costs[0].Currency {
			return nil
		}
		return deltaPct(uint64(math.Round(current.Costs[0].Amount*100)), uint64(math.Round(previous.Costs[0].Amount*100)))
	}
	if len(current.Costs) == 0 && len(previous.Costs) == 0 {
		return deltaPct(uint64(math.Round(current.CostAmount*100)), uint64(math.Round(previous.CostAmount*100)))
	}
	return nil
}
