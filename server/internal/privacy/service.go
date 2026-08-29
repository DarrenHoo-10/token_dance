package privacy

import (
	"context"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store  store.PrivacyStore
	pStore store.ProfileStore
	clk    clock.Clock
}

func NewService(st store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store:  st.Privacy(),
		pStore: st.Profile(),
		clk:    clk,
	}
}

func (s *Service) GetPrivacy(ctx context.Context, userID string) (*domain.UserPrivacySettings, error) {
	p, err := s.store.GetPrivacy(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.privacyNotFound", "privacy settings not found", nil, err)
	}
	return p, nil
}

type UpdatePrivacyInput struct {
	PublicProfileEnabled  bool                         `json:"publicProfileEnabled"`
	LeaderboardVisibility domain.LeaderboardVisibility `json:"leaderboardVisibility"`
	ShowBio               bool                         `json:"showBio"`
	ShowTokenTotal        bool                         `json:"showTokenTotal"`
	ShowTrends            bool                         `json:"showTrends"`
	ShowActivityCalendar  bool                         `json:"showActivityCalendar"`
	ShowAgentBreakdown    bool                         `json:"showAgentBreakdown"`
	ShowSkillRanking      bool                         `json:"showSkillRanking"`
	ShowAchievements      bool                         `json:"showAchievements"`
	ExpectedVersion       uint64                       `json:"expectedVersion"`
}

func (s *Service) UpdatePrivacy(ctx context.Context, userID string, in UpdatePrivacyInput) (*domain.UserPrivacySettings, error) {
	if in.LeaderboardVisibility == "" {
		if in.PublicProfileEnabled {
			in.LeaderboardVisibility = domain.LeaderboardVisibilityPublic
		} else {
			in.LeaderboardVisibility = domain.LeaderboardVisibilityPrivate
		}
	}
	if (in.LeaderboardVisibility != domain.LeaderboardVisibilityPublic && in.LeaderboardVisibility != domain.LeaderboardVisibilityPrivate) ||
		(in.PublicProfileEnabled != (in.LeaderboardVisibility == domain.LeaderboardVisibilityPublic)) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "privacy.visibilityMismatch", "leaderboard visibility must match public profile state", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:      "sev_" + eventID,
		UserID:       &userID,
		EventType:    "privacy_changed",
		Outcome:      "success",
		CreatedAt:    now,
		MetadataJSON: map[string]interface{}{"publicProfileEnabled": in.PublicProfileEnabled},
	}

	settings := domain.UserPrivacySettings{
		UserID:                userID,
		PublicProfileEnabled:  in.PublicProfileEnabled,
		LeaderboardVisibility: in.LeaderboardVisibility,
		ShowBio:               in.ShowBio,
		ShowTokenTotal:        in.ShowTokenTotal,
		ShowTrends:            in.ShowTrends,
		ShowActivityCalendar:  in.ShowActivityCalendar,
		ShowAgentBreakdown:    in.ShowAgentBreakdown,
		ShowSkillRanking:      in.ShowSkillRanking,
		ShowAchievements:      in.ShowAchievements,
	}

	p, err := s.store.UpdatePrivacyTx(ctx, userID, settings, in.ExpectedVersion, event, now)
	if err != nil {
		if err == domain.ErrPreconditionFailed {
			return nil, domain.NewAppError(412, "VERSION_CONFLICT", "api.versionConflict", "privacy settings were modified by another request", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to update privacy settings", nil, err)
	}

	return p, nil
}

func (s *Service) GetPublicPreview(ctx context.Context, userID string) (*domain.PublicUserProfile, error) {
	u, err := s.pStore.GetUserProfile(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.userNotFound", "user not found", nil, err)
	}
	priv, err := s.store.GetPrivacy(ctx, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.privacyNotFound", "privacy settings not found", nil, err)
	}

	handle := ""
	if u.Handle != nil {
		handle = *u.Handle
	}

	var bio *string
	if priv.ShowBio {
		bio = u.Bio
	}

	return &domain.PublicUserProfile{
		UserID:               userID,
		Handle:               handle,
		DisplayName:          u.DisplayName,
		AvatarURL:            u.AvatarURL,
		Bio:                  bio,
		ProfileStatus:        domain.ProfileStatusPublished,
		ShowBio:              priv.ShowBio,
		ShowTokenTotal:       priv.ShowTokenTotal,
		ShowTrends:           priv.ShowTrends,
		ShowActivityCalendar: priv.ShowActivityCalendar,
		ShowAgentBreakdown:   priv.ShowAgentBreakdown,
		ShowSkillRanking:     priv.ShowSkillRanking,
		ShowAchievements:     priv.ShowAchievements,
		ProjectionVersion:    1,
	}, nil
}

func (s *Service) GetPublicProfileByHandle(ctx context.Context, handle string) (*domain.PublicUserProfile, string, error) {
	now := s.clk.Now()
	pub, err := s.store.GetPublicProfileByHandle(ctx, handle, now)
	if err == nil {
		return pub, "", nil
	}

	// Check if old handle has redirect
	redirectHandle, errRedirect := s.pStore.GetRedirectHandle(ctx, handle, now)
	if errRedirect == nil && redirectHandle != "" {
		pubRedirect, errPub := s.store.GetPublicProfileByHandle(ctx, redirectHandle, now)
		if errPub == nil {
			return pubRedirect, redirectHandle, nil
		}
	}

	return nil, "", domain.NewAppError(404, "PUBLIC_PROFILE_NOT_FOUND", "profile.notFound", "public profile not found", nil, domain.ErrNotFound)
}

type DeletionRequestInput struct {
	Scope          string
	Confirmation   bool
	InstallationID string
	From           *time.Time
	To             *time.Time
}

func (s *Service) RequestDeletion(ctx context.Context, userID, scope string, confirmation bool) (*domain.DataDeletionRequest, error) {
	return s.RequestDeletionWithFilter(ctx, userID, DeletionRequestInput{Scope: scope, Confirmation: confirmation})
}

func (s *Service) RequestDeletionWithFilter(ctx context.Context, userID string, in DeletionRequestInput) (*domain.DataDeletionRequest, error) {
	if !in.Confirmation {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "deletion.confirmationRequired", "confirmation required", nil, domain.ErrInvalidArgument)
	}

	if in.Scope != "account" && in.Scope != "installation" && in.Scope != "time_range" && in.Scope != "all_usage" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "deletion.invalidScope", "invalid deletion scope", nil, domain.ErrInvalidArgument)
	}
	if in.Scope == "installation" && in.InstallationID == "" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "deletion.installationRequired", "installationId is required", nil, domain.ErrInvalidArgument)
	}
	if in.Scope == "time_range" && (in.From == nil || in.To == nil || !in.To.After(*in.From)) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "deletion.invalidTimeRange", "from and to must define a valid half-open time range", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	reqIDToken, _ := crypto.GenerateOpaqueToken(13)
	reqID := "del_" + reqIDToken

	var cancelBefore *time.Time
	if in.Scope == "account" {
		cb := now.Add(7 * 24 * time.Hour)
		cancelBefore = &cb
	}

	scopeFilter := make(map[string]interface{})
	if in.InstallationID != "" {
		scopeFilter["installationId"] = in.InstallationID
	}
	if in.From != nil {
		scopeFilter["from"] = in.From.UTC().Format(time.RFC3339)
	}
	if in.To != nil {
		scopeFilter["to"] = in.To.UTC().Format(time.RFC3339)
	}

	req := domain.DataDeletionRequest{
		RequestID:       reqID,
		UserID:          &userID,
		DeletionScope:   in.Scope,
		ScopeFilterJSON: scopeFilter,
		RequestStatus:   domain.DeletionStatusPending,
		Phase:           "queued",
		ProgressCursor:  0,
		CancelBefore:    cancelBefore,
		RequestedAt:     now,
	}

	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:      "sev_" + eventID,
		UserID:       &userID,
		EventType:    "deletion_requested",
		Outcome:      "success",
		CreatedAt:    now,
		MetadataJSON: map[string]interface{}{"scope": in.Scope},
	}

	createdReq, err := s.store.RequestDeletionTx(ctx, req, event, now)
	if err != nil {
		if err == domain.ErrNotFound {
			return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.deviceNotFound", "installation not found", nil, err)
		}
		if err == domain.ErrConflict {
			return nil, domain.NewAppError(409, "ACCOUNT_ACTION_NOT_ALLOWED", "deletion.alreadyPending", "an account deletion is already active", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to request deletion", nil, err)
	}

	return createdReq, nil
}

func (s *Service) CancelDeletion(ctx context.Context, requestID, userID string) error {
	now := s.clk.Now()
	if err := s.store.CancelDeletionTx(ctx, requestID, userID, now); err != nil {
		if err == domain.ErrNotFound {
			return domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.deletionNotFound", "deletion request not found", nil, err)
		}
		if err == domain.ErrConflict {
			return domain.NewAppError(409, "ACCOUNT_ACTION_NOT_ALLOWED", "deletion.cannotCancel", "deletion request cannot be cancelled", nil, err)
		}
		return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to cancel deletion", nil, err)
	}
	return nil
}

func (s *Service) GetDeletionRequest(ctx context.Context, requestID, userID string) (*domain.DataDeletionRequest, error) {
	req, err := s.store.GetDeletionRequest(ctx, requestID, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "api.deletionNotFound", "deletion request not found", nil, err)
	}
	return req, nil
}
