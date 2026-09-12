package leaderboard

import (
	"context"
	"errors"
	"math"
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

// GetCommunityStats serves precomputed daily community totals with the day
// over day percentage change. It only reads rows the stats worker wrote; a
// missing day yields omitted fields so clients can show an empty state
// instead of a fabricated zero.
func (s *Service) GetCommunityStats(ctx context.Context, now time.Time) (*domain.CommunityStatsResponse, error) {
	today := domain.DayDate(now)
	current, err := s.community.GetCommunityDailyStats(ctx, today)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return &domain.CommunityStatsResponse{MetricDate: today, Timezone: domain.DayTZName}, nil
	}
	costAmount, costs := communityCostProjection(*current)
	response := &domain.CommunityStatsResponse{
		MetricDate:   current.MetricDate,
		Timezone:     domain.DayTZName,
		Tokens:       uint64String(current.TokensTotal),
		Developers:   &current.Developers,
		CodeLines:    uint64String(current.CodeLines),
		Interactions: uint64String(current.Interactions),
		CostAmount:   costAmount,
		Costs:        costs,
		ComputedAt:   &current.ComputedAt,
	}
	previous, err := s.community.GetCommunityDailyStats(ctx, domain.PreviousDayDate(now))
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
	harnesses, err := s.community.GetCommunityHarnessShares(ctx, today, 5)
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
