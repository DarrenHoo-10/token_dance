package search

import (
	"context"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store store.SearchStore
	clk   clock.Clock
}

func NewService(st store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store: st.Search(),
		clk:   clk,
	}
}

func (s *Service) Search(ctx context.Context, query string, limit int) (*domain.SearchResponse, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	now := s.clk.Now()
	return s.store.Search(ctx, query, limit, now)
}
