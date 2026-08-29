package analytics

import (
	"context"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store  store.AnalyticsStore
	pStore store.ProfileStore
	clk    clock.Clock
}

func NewService(st store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store:  st.Analytics(),
		pStore: st.Profile(),
		clk:    clk,
	}
}

func (s *Service) ParseTimeRange(key string, userTz string) domain.TimeRange {
	now := s.clk.Now()
	loc, err := time.LoadLocation(userTz)
	if err != nil || loc == nil {
		loc = time.UTC
	}

	rKey := domain.TimeRangeKey(key)
	if rKey != domain.TimeRangeToday && rKey != domain.TimeRange7d && rKey != domain.TimeRange30d && rKey != domain.TimeRangeAll && rKey != domain.TimeRangeCustom {
		rKey = domain.TimeRange30d
	}

	var from, to time.Time
	to = now

	switch rKey {
	case domain.TimeRangeToday:
		year, month, day := now.In(loc).Date()
		from = time.Date(year, month, day, 0, 0, 0, 0, loc).UTC()
	case domain.TimeRange7d:
		from = now.AddDate(0, 0, -7)
	case domain.TimeRange30d:
		from = now.AddDate(0, 0, -30)
	case domain.TimeRangeAll:
		from = now.AddDate(-1, 0, 0)
	default:
		from = now.AddDate(0, 0, -30)
	}

	return domain.TimeRange{
		Key:      rKey,
		From:     from,
		To:       to,
		Timezone: userTz,
	}
}

func (s *Service) GetPersonalSummary(ctx context.Context, userID, rangeKey string) (*domain.PersonalSummary, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}

	r := s.ParseTimeRange(rangeKey, u.TimezoneName)
	return s.store.GetPersonalSummary(ctx, userID, r)
}

func (s *Service) GetTokenTrend(ctx context.Context, userID, rangeKey, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}

	if mode != "total" && mode != "structure" {
		mode = "total"
	}

	r := s.ParseTimeRange(rangeKey, u.TimezoneName)
	return s.store.GetTokenTrend(ctx, userID, r, mode, agentID, providerID, modelID)
}

func (s *Service) GetAgentBreakdown(ctx context.Context, userID, rangeKey string) (*domain.BreakdownResponse, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}

	r := s.ParseTimeRange(rangeKey, u.TimezoneName)
	return s.store.GetAgentBreakdown(ctx, userID, r)
}

func (s *Service) GetModelBreakdown(ctx context.Context, userID, rangeKey string) (*domain.BreakdownResponse, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}

	r := s.ParseTimeRange(rangeKey, u.TimezoneName)
	return s.store.GetModelBreakdown(ctx, userID, r)
}

func (s *Service) GetSkillRanking(ctx context.Context, userID, rangeKey string) (*domain.SkillsResponse, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}

	r := s.ParseTimeRange(rangeKey, u.TimezoneName)
	return s.store.GetSkillRanking(ctx, userID, r)
}

func (s *Service) GetActivityCalendar(ctx context.Context, userID, rangeKey string) (*domain.CalendarResponse, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}

	r := s.ParseTimeRange(rangeKey, u.TimezoneName)
	return s.store.GetActivityCalendar(ctx, userID, r)
}

func (s *Service) GetFilterOptions(ctx context.Context, userID string) (*domain.FilterOptions, error) {
	return s.store.GetFilterOptions(ctx, userID)
}
