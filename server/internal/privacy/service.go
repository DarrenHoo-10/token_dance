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
	PublicProfileEnabled bool   `json:"publicProfileEnabled"`
	ShowBio              bool   `json:"showBio"`
	ShowTokenTotal       bool   `json:"showTokenTotal"`
	ShowTrends           bool   `json:"showTrends"`
	ShowActivityCalendar bool   `json:"showActivityCalendar"`
	ShowAgentBreakdown   bool   `json:"showAgentBreakdown"`
	ShowSkillRanking     bool   `json:"showSkillRanking"`
	ShowAchievements     bool   `json:"showAchievements"`
	ExpectedVersion      uint64 `json:"expectedVersion"`
}

func (s *Service) UpdatePrivacy(ctx context.Context, userID string, in UpdatePrivacyInput) (*domain.UserPrivacySettings, error) {
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
		UserID:               userID,
		PublicProfileEnabled: in.PublicProfileEnabled,
		ShowBio:              in.ShowBio,
		ShowTokenTotal:       in.ShowTokenTotal,
		ShowTrends:           in.ShowTrends,
		ShowActivityCalendar: in.ShowActivityCalendar,
		ShowAgentBreakdown:   in.ShowAgentBreakdown,
		ShowSkillRanking:     in.ShowSkillRanking,
		ShowAchievements:     in.ShowAchievements,
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

func (s *Service) RequestDeletion(ctx context.Context, userID, scope string, confirmation bool) (*domain.DataDeletionRequest, error) {
	if !confirmation {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "deletion.confirmationRequired", "confirmation required", nil, domain.ErrInvalidArgument)
	}

	if scope != "account" && scope != "installation" && scope != "time_range" && scope != "all_usage" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "deletion.invalidScope", "invalid deletion scope", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	reqIDToken, _ := crypto.GenerateOpaqueToken(13)
	reqID := "del_" + reqIDToken

	var cancelBefore *time.Time
	if scope == "account" {
		cb := now.Add(7 * 24 * time.Hour) // 7 days cancel window
		cancelBefore = &cb
	}

	req := domain.DataDeletionRequest{
		RequestID:      reqID,
		UserID:         &userID,
		DeletionScope:  scope,
		RequestStatus:  domain.DeletionStatusPending,
		Phase:          "queued",
		ProgressCursor: 0,
		CancelBefore:   cancelBefore,
		RequestedAt:    now,
	}

	eventID, _ := crypto.GenerateOpaqueToken(13)
	event := domain.UserSecurityEvent{
		EventID:      "sev_" + eventID,
		UserID:       &userID,
		EventType:    "deletion_requested",
		Outcome:      "success",
		CreatedAt:    now,
		MetadataJSON: map[string]interface{}{"scope": scope},
	}

	createdReq, err := s.store.RequestDeletionTx(ctx, req, event, now)
	if err != nil {
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
