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

// GetCommunityStats aggregates the worker's precomputed daily rows for one
// homepage window. Request paths never scan raw telemetry. Active developers
// come from the same precomputed window scores as the leaderboard.
func (s *Service) GetCommunityStats(ctx context.Context, now time.Time, window string) (*domain.CommunityStatsResponse, error) {
	fromDate, toDate, previousFrom, previousTo, err := communityWindowDates(window, now)
	if err != nil {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidArgument", "invalid community stats window", nil, err)
	}
	response := &domain.CommunityStatsResponse{
		MetricDate: toDate,
		Timezone:   domain.DayTZName,
		Window:     window,
		FromDate:   fromDate,
		ToDate:     toDate,
	}
	rows, err := s.community.ListCommunityDailyStats(ctx, fromDate, toDate)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return response, nil
	}
	current := aggregateCommunityRows(toDate, rows)
	developers, err := s.community.GetCommunityActiveDevelopers(ctx, window, domain.DayDate(now), fromDate, toDate)
	if err != nil {
		return nil, err
	}
	current.Developers = developers
	costAmount, costs := communityCostProjection(current)
	response.Tokens = uint64String(current.TokensTotal)
	response.Developers = &current.Developers
	response.CodeLines = uint64String(current.CodeLines)
	response.Interactions = uint64String(current.Interactions)
	response.CostAmount = costAmount
	response.Costs = costs
	response.ComputedAt = &current.ComputedAt

	if previousFrom != "" {
		previousRows, err := s.community.ListCommunityDailyStats(ctx, previousFrom, previousTo)
		if err != nil {
			return nil, err
		}
		if len(previousRows) > 0 {
			previous := aggregateCommunityRows(previousTo, previousRows)
			response.Deltas = &domain.CommunityStatsDeltaDTO{
				Tokens:       deltaPct(current.TokensTotal, previous.TokensTotal),
				CodeLines:    deltaPct(current.CodeLines, previous.CodeLines),
				Interactions: deltaPct(current.Interactions, previous.Interactions),
				CostAmount:   communityCostDelta(current, previous),
			}
			if window == "today" {
				response.Deltas.Developers = deltaPct(current.Developers, previous.Developers)
			}
		}
	}
	harnesses, err := s.community.GetCommunityHarnessSharesRange(ctx, fromDate, toDate, 5)
	if err != nil {
		return nil, err
	}
	for _, harness := range harnesses {
		share := float64(0)
		if current.TokensTotal > 0 {
			share = math.Round(float64(harness.TokensTotal)/float64(current.TokensTotal)*1000) / 10
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

func communityWindowDates(window string, now time.Time) (string, string, string, string, error) {
	end := domain.StartOfDay(now)
	days := 1
	switch window {
	case "today":
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "all":
		return "1000-01-01", domain.DayDate(now), "", "", nil
	default:
		return "", "", "", "", domain.ErrInvalidArgument
	}
	from := end.AddDate(0, 0, -(days - 1))
	previousTo := from.AddDate(0, 0, -1)
	previousFrom := previousTo.AddDate(0, 0, -(days - 1))
	return domain.DayDate(from), domain.DayDate(end), domain.DayDate(previousFrom), domain.DayDate(previousTo), nil
}

func aggregateCommunityRows(metricDate string, rows []store.CommunityDailyTotals) store.CommunityDailyTotals {
	totals := store.CommunityDailyTotals{MetricDate: metricDate}
	costs := make(map[string]float64)
	for _, row := range rows {
		totals.TokensTotal += row.TokensTotal
		if row.Developers > totals.Developers {
			totals.Developers = row.Developers
		}
		totals.CodeLines += row.CodeLines
		totals.Interactions += row.Interactions
		if row.ComputedAt.After(totals.ComputedAt) {
			totals.ComputedAt = row.ComputedAt
		}
		if len(row.Costs) == 0 {
			costs["USD"] += row.CostAmount
			continue
		}
		for _, cost := range row.Costs {
			costs[cost.Currency] += cost.Amount
		}
	}
	for currency, amount := range costs {
		totals.Costs = append(totals.Costs, store.CommunityCost{Currency: currency, Amount: amount})
	}
	sort.Slice(totals.Costs, func(i, j int) bool { return totals.Costs[i].Currency < totals.Costs[j].Currency })
	if len(totals.Costs) == 1 {
		totals.CostAmount = totals.Costs[0].Amount
	}
	return totals
}

// deltaPct returns (current-previous)/previous in percent with one decimal.
// It stays nil when the previous period is zero.
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
