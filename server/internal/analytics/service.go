package analytics

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store      store.AnalyticsStore
	pStore     store.ProfileStore
	clk        clock.Clock
	cursorKeys config.VersionedKeyring
}

func NewService(st store.Store, clk clock.Clock) *Service {
	return NewServiceWithConfig(st, config.DefaultConfig(), clk)
}

func NewServiceWithConfig(st store.Store, cfg *config.Config, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{store: st.Analytics(), pStore: st.Profile(), clk: clk, cursorKeys: cfg.IdempotencyKeys}
}

func userLocation(name string) (*time.Location, string) {
	loc, err := time.LoadLocation(name)
	if err != nil || loc == nil {
		return time.UTC, "UTC"
	}
	return loc, name
}

func (s *Service) ResolveTimeRange(key, userTZ, customFrom, customTo string) (domain.TimeRange, error) {
	now := s.clk.Now()
	loc, timezone := userLocation(userTZ)
	localNow := now.In(loc)
	rangeKey := domain.TimeRangeKey(key)
	if rangeKey == "" {
		rangeKey = domain.TimeRange30d
	}
	startOfToday := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, loc)
	from := startOfToday.AddDate(0, 0, -29)
	to := now
	switch rangeKey {
	case domain.TimeRangeToday:
		from = startOfToday
	case domain.TimeRange7d:
		from = startOfToday.AddDate(0, 0, -6)
	case domain.TimeRange30d:
		from = startOfToday.AddDate(0, 0, -29)
	case domain.TimeRange10w:
		from = startOfToday.AddDate(0, 0, -69)
	case domain.TimeRangeAll:
		from = time.Date(1970, 1, 1, 0, 0, 0, 0, loc)
	case domain.TimeRangeCustom:
		start, err := time.ParseInLocation("2006-01-02", customFrom, loc)
		if err != nil {
			return domain.TimeRange{}, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidFrom", "custom range requires from=YYYY-MM-DD", nil, err)
		}
		end, err := time.ParseInLocation("2006-01-02", customTo, loc)
		if err != nil {
			return domain.TimeRange{}, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidTo", "custom range requires to=YYYY-MM-DD", nil, err)
		}
		if end.Before(start) || end.Sub(start) > 366*24*time.Hour {
			return domain.TimeRange{}, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidRange", "custom range must be ordered and no longer than 366 days", nil, domain.ErrInvalidArgument)
		}
		from = start
		to = end.AddDate(0, 0, 1).Add(-time.Nanosecond)
	default:
		return domain.TimeRange{}, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidRange", "unsupported range", nil, domain.ErrInvalidArgument)
	}
	return domain.TimeRange{Key: rangeKey, From: from.UTC(), To: to.UTC(), Timezone: timezone}, nil
}

func (s *Service) ParseTimeRange(key string, userTZ string) domain.TimeRange {
	r, err := s.ResolveTimeRange(key, userTZ, "", "")
	if err != nil {
		r, _ = s.ResolveTimeRange("30d", userTZ, "", "")
	}
	return r
}

func (s *Service) profileRange(ctx context.Context, userID, key, from, to string) (domain.TimeRange, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return domain.TimeRange{}, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}
	return s.ResolveTimeRange(key, u.TimezoneName, from, to)
}

func (s *Service) GetPersonalSummary(ctx context.Context, userID, rangeKey string) (*domain.PersonalSummary, error) {
	return s.GetPersonalSummaryRange(ctx, userID, rangeKey, "", "")
}
func (s *Service) GetPersonalSummaryRange(ctx context.Context, userID, rangeKey, from, to string) (*domain.PersonalSummary, error) {
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	return s.store.GetPersonalSummary(ctx, userID, r)
}

func (s *Service) GetTokenTrend(ctx context.Context, userID, rangeKey, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error) {
	return s.GetTokenTrendRange(ctx, userID, rangeKey, "", "", mode, agentID, providerID, modelID)
}
func (s *Service) GetTokenTrendRange(ctx context.Context, userID, rangeKey, from, to, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error) {
	if mode != "total" && mode != "structure" {
		mode = "total"
	}
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	return s.store.GetTokenTrend(ctx, userID, r, mode, agentID, providerID, modelID)
}

func (s *Service) GetAgentBreakdown(ctx context.Context, userID, rangeKey string) (*domain.BreakdownResponse, error) {
	return s.GetAgentBreakdownRange(ctx, userID, rangeKey, "", "")
}
func (s *Service) GetAgentBreakdownRange(ctx context.Context, userID, rangeKey, from, to string) (*domain.BreakdownResponse, error) {
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	return s.store.GetAgentBreakdown(ctx, userID, r)
}

func (s *Service) GetModelBreakdown(ctx context.Context, userID, rangeKey string) (*domain.BreakdownResponse, error) {
	return s.GetModelBreakdownRange(ctx, userID, rangeKey, "", "")
}
func (s *Service) GetModelBreakdownRange(ctx context.Context, userID, rangeKey, from, to string) (*domain.BreakdownResponse, error) {
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	return s.store.GetModelBreakdown(ctx, userID, r)
}

func (s *Service) GetSkillRanking(ctx context.Context, userID, rangeKey string) (*domain.SkillsResponse, error) {
	return s.GetSkillRankingRange(ctx, userID, rangeKey, "", "")
}
func (s *Service) GetSkillRankingRange(ctx context.Context, userID, rangeKey, from, to string) (*domain.SkillsResponse, error) {
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	return s.store.GetSkillRanking(ctx, userID, r)
}

func (s *Service) GetActivityCalendar(ctx context.Context, userID, rangeKey string) (*domain.CalendarResponse, error) {
	return s.GetActivityCalendarRange(ctx, userID, rangeKey, "", "")
}
func (s *Service) GetActivityCalendarRange(ctx context.Context, userID, rangeKey, from, to string) (*domain.CalendarResponse, error) {
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	return s.store.GetActivityCalendar(ctx, userID, r)
}

func (s *Service) GetFilterOptions(ctx context.Context, userID string) (*domain.FilterOptions, error) {
	return s.store.GetFilterOptions(ctx, userID)
}

type activityCursor struct {
	Offset int   `json:"offset"`
	Exp    int64 `json:"exp"`
}

func (s *Service) encodeCursor(offset int) string {
	payload, _ := json.Marshal(activityCursor{Offset: offset, Exp: s.clk.Now().Add(time.Hour).Unix()})
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	sig := crypto.HMACSHA256(s.cursorKeys.Current(), []byte(encoded))
	return "v" + strconv.Itoa(int(s.cursorKeys.CurrentVersion)) + "." + encoded + "." + base64.RawURLEncoding.EncodeToString(sig[:])
}

func (s *Service) decodeCursor(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "v") {
		return 0, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidCursor", "invalid activity cursor", nil, domain.ErrInvalidArgument)
	}
	version, err := strconv.ParseUint(strings.TrimPrefix(parts[0], "v"), 10, 16)
	if err != nil {
		return 0, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidCursor", "invalid activity cursor", nil, err)
	}
	encoded, signature := parts[1], parts[2]
	key := s.cursorKeys.Keys[uint16(version)]
	sigBytes, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil || len(key) == 0 {
		return 0, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidCursor", "invalid activity cursor", nil, domain.ErrInvalidArgument)
	}
	expected := crypto.HMACSHA256(key, []byte(encoded))
	if !hmac.Equal(sigBytes, expected[:]) {
		return 0, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidCursor", "invalid activity cursor", nil, domain.ErrInvalidArgument)
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return 0, err
	}
	var cursor activityCursor
	if err := json.Unmarshal(payload, &cursor); err != nil || cursor.Offset < 0 || s.clk.Now().Unix() > cursor.Exp {
		return 0, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidCursor", "expired or invalid activity cursor", nil, domain.ErrInvalidArgument)
	}
	return cursor.Offset, nil
}

func contains(values []string, value string) bool {
	if value == "" || value == "all" {
		return true
	}
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func optionalFilter(value string) *string {
	if value == "" || value == "all" {
		return nil
	}
	return &value
}

func (s *Service) GetActivity(ctx context.Context, userID, rangeKey, from, to, agent, provider, model, cursor string, limit int) (*domain.ActivityResponse, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidLimit", "limit must be between 1 and 100", nil, domain.ErrInvalidArgument)
	}
	r, err := s.profileRange(ctx, userID, rangeKey, from, to)
	if err != nil {
		return nil, err
	}
	opts, err := s.store.GetFilterOptions(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !contains(opts.Agents, agent) || !contains(opts.Providers, provider) || !contains(opts.Models, model) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "analytics.invalidFilter", "activity filter is not available for this user", nil, domain.ErrInvalidArgument)
	}
	offset, err := s.decodeCursor(cursor)
	if err != nil {
		return nil, err
	}
	rows, err := s.store.GetActivity(ctx, userID, domain.ActivityQuery{Range: r, AgentID: optionalFilter(agent), ProviderID: optionalFilter(provider), ModelID: optionalFilter(model), Limit: limit + 1, Offset: offset})
	if err != nil {
		return nil, err
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		value := s.encodeCursor(offset + limit)
		next = &value
	}
	return &domain.ActivityResponse{Items: rows, NextCursor: next, Range: r}, nil
}
