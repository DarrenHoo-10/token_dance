package profile

import (
	"context"
	"regexp"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

var (
	handleRegex     = regexp.MustCompile(`^[a-z][a-z0-9_]{2,31}$`)
	reservedHandles = map[string]bool{
		"admin":       true,
		"api":         true,
		"auth":        true,
		"login":       true,
		"logout":      true,
		"register":    true,
		"settings":    true,
		"me":          true,
		"leaderboard": true,
		"explore":     true,
		"compare":     true,
		"support":     true,
		"tokendance":  true,
		"privacy":     true,
		"status":      true,
		"healthz":     true,
		"readyz":      true,
		"devices":     true,
		"profile":     true,
		"v1":          true,
		"null":        true,
		"undefined":   true,
	}
)

type Service struct {
	store store.ProfileStore
	clk   clock.Clock
}

func NewService(st store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store: st.Profile(),
		clk:   clk,
	}
}

func ValidateHandle(handle string) error {
	normalized := strings.ToLower(strings.TrimSpace(handle))
	if !handleRegex.MatchString(normalized) {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.invalidHandleFormat", "handle must be 3-32 chars, start with a-z, containing only a-z, 0-9, and _", nil, domain.ErrInvalidHandle)
	}
	if reservedHandles[normalized] {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.handleReserved", "this handle is reserved", nil, domain.ErrInvalidHandle)
	}
	return nil
}

func ValidateDisplayName(name string) error {
	trimmed := strings.TrimSpace(name)
	if len(trimmed) < 1 || len([]rune(trimmed)) > 80 {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.invalidDisplayName", "display name must be 1-80 characters", nil, domain.ErrInvalidArgument)
	}
	for _, r := range trimmed {
		if r < 32 && r != '\t' {
			return domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.invalidDisplayName", "display name cannot contain control characters", nil, domain.ErrInvalidArgument)
		}
	}
	return nil
}

func ValidateTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.invalidTimezone", "invalid IANA timezone", nil, domain.ErrInvalidArgument)
	}
	return nil
}

type OnboardingInput struct {
	DisplayName string                     `json:"displayName"`
	Handle      string                     `json:"handle"`
	Timezone    string                     `json:"timezone"`
	Locale      string                     `json:"locale"`
	Privacy     domain.UserPrivacySettings `json:"privacy"`
	ReturnTo    string                     `json:"returnTo,omitempty"`
}

func (s *Service) CompleteOnboarding(ctx context.Context, userID string, in OnboardingInput) (*domain.User, *domain.UserPrivacySettings, error) {
	if err := ValidateDisplayName(in.DisplayName); err != nil {
		return nil, nil, err
	}
	if err := ValidateHandle(in.Handle); err != nil {
		return nil, nil, err
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	if err := ValidateTimezone(in.Timezone); err != nil {
		return nil, nil, err
	}
	if in.Locale != "zh-CN" && in.Locale != "en-US" {
		in.Locale = "en-US"
	}

	now := s.clk.Now()
	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:      "sev_" + eventID,
		UserID:       &userID,
		EventType:    "onboarding_completed",
		Outcome:      "success",
		CreatedAt:    now,
		MetadataJSON: map[string]interface{}{"handle": in.Handle},
	}

	u, p, err := s.store.CompleteOnboardingTx(ctx, userID, in.Handle, in.DisplayName, in.Timezone, in.Locale, in.Privacy, event, now)
	if err != nil {
		if err == domain.ErrHandleTaken {
			return nil, nil, domain.NewAppError(409, "PROFILE_HANDLE_TAKEN", "profile.handleTaken", "handle is already taken", nil, err)
		}
		return nil, nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to complete onboarding", nil, err)
	}

	return u, p, nil
}

func (s *Service) GetProfile(ctx context.Context, userID string) (*domain.User, error) {
	u, err := s.store.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}
	return u, nil
}

type UpdateProfileInput struct {
	DisplayName     *string `json:"displayName,omitempty"`
	Handle          *string `json:"handle,omitempty"`
	Bio             *string `json:"bio,omitempty"`
	Timezone        *string `json:"timezone,omitempty"`
	Locale          *string `json:"locale,omitempty"`
	ExpectedVersion uint64  `json:"expectedVersion"`
}

func (s *Service) UpdateProfile(ctx context.Context, userID string, in UpdateProfileInput) (*domain.User, error) {
	if in.DisplayName != nil {
		if err := ValidateDisplayName(*in.DisplayName); err != nil {
			return nil, err
		}
	}
	if in.Handle != nil {
		if err := ValidateHandle(*in.Handle); err != nil {
			return nil, err
		}
	}
	if in.Bio != nil && len([]rune(*in.Bio)) > 280 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.invalidBio", "bio must be 280 characters or fewer", nil, domain.ErrInvalidArgument)
	}
	if in.Timezone != nil {
		if err := ValidateTimezone(*in.Timezone); err != nil {
			return nil, err
		}
	}
	if in.Locale != nil && *in.Locale != "zh-CN" && *in.Locale != "en-US" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "profile.invalidLocale", "locale must be zh-CN or en-US", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:   "sev_" + eventID,
		UserID:    &userID,
		EventType: "profile_changed",
		Outcome:   "success",
		CreatedAt: now,
	}

	u, err := s.store.UpdateProfileTx(ctx, userID, in.DisplayName, in.Handle, in.Bio, in.Timezone, in.Locale, in.ExpectedVersion, event, now)
	if err != nil {
		if err == domain.ErrHandleTaken {
			return nil, domain.NewAppError(409, "PROFILE_HANDLE_TAKEN", "profile.handleTaken", "handle is already taken", nil, err)
		}
		if err == domain.ErrPreconditionFailed {
			return nil, domain.NewAppError(412, "VERSION_CONFLICT", "api.versionConflict", "profile was modified by another request", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to update profile", nil, err)
	}

	return u, nil
}
