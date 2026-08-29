package leaderboard

import (
	"context"

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
	return s.store.GetLeaderboard(ctx, boardKey, window, metric, cursor, limit)
}
