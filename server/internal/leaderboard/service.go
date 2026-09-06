package leaderboard

import (
	"context"
	"errors"
	"strconv"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store store.LeaderboardStore
}

func NewService(st store.Store) *Service {
	return &Service{
		store: st.Leaderboard(),
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
