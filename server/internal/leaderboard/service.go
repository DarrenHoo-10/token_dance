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
		return &domain.CommunityStatsResponse{MetricDate: today, Timezone: "UTC"}, nil
	}
	response := &domain.CommunityStatsResponse{
		MetricDate:   current.MetricDate,
		Timezone:     "UTC",
		Tokens:       uint64String(current.TokensTotal),
		Developers:   &current.Developers,
		CodeLines:    uint64String(current.CodeLines),
		Interactions: uint64String(current.Interactions),
		CostAmount:   roundedCost(current.CostAmount),
		ComputedAt:   &current.ComputedAt,
	}
	previous, err := s.community.GetCommunityDailyStats(ctx, domain.DayDate(now.AddDate(0, 0, -1)))
	if err != nil {
		return nil, err
	}
	if previous != nil {
		response.Deltas = &domain.CommunityStatsDeltaDTO{
			Tokens:       deltaPct(current.TokensTotal, previous.TokensTotal),
			Developers:   deltaPct(current.Developers, previous.Developers),
			CodeLines:    deltaPct(current.CodeLines, previous.CodeLines),
			Interactions: deltaPct(current.Interactions, previous.Interactions),
			CostAmount:   deltaPct(uint64(math.Round(current.CostAmount*100)), uint64(math.Round(previous.CostAmount*100))),
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
	pct := math.Round((float64(current) - float64(previous)) / float64(previous) * 1000) / 10
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
