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
	if boardKey == "" {
		boardKey = "global"
	}
	if window == "" {
		window = "30d"
	}
	if metric == "" {
		metric = "tokens"
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if boardKey == "global" && metric == "tokens" && cursor != nil {
		after, err := strconv.Atoi(*cursor)
		if err != nil || after < 0 || after >= 1000 {
			return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidCursor", "cursor must be within the top 1000", nil, domain.ErrInvalidArgument)
		}
	}
	result, err := s.store.GetLeaderboard(ctx, boardKey, window, metric, cursor, limit)
	if errors.Is(err, domain.ErrInvalidArgument) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "api.invalidArgument", "invalid leaderboard query", nil, err)
	}
	return result, err
}
