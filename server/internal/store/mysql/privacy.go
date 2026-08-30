package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store/sqlcgen"
)

type privacyStore struct {
	db *sql.DB
}

func (s *privacyStore) GetPrivacy(ctx context.Context, userID string) (*domain.UserPrivacySettings, error) {
	row, err := sqlcgen.New(s.db).GetPrivacyByUser(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get privacy settings: %w", err)
	}
	return &domain.UserPrivacySettings{
		UserID:                row.UserID,
		PublicProfileEnabled:  row.PublicProfileEnabled,
		LeaderboardVisibility: domain.LeaderboardVisibility(row.LeaderboardVisibility),
		ShowBio:               row.ShowBio,
		ShowTokenTotal:        row.ShowTokenTotal,
		ShowTrends:            row.ShowTrends,
		ShowActivityCalendar:  row.ShowActivityCalendar,
		ShowAgentBreakdown:    row.ShowAgentBreakdown,
		ShowSkillRanking:      row.ShowSkillRanking,
		ShowAchievements:      row.ShowAchievements,
		PrivacyVersion:        row.PrivacyVersion,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
	}, nil
}

func (s *privacyStore) UpdatePrivacyTx(ctx context.Context, userID string, in domain.UserPrivacySettings, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.UserPrivacySettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin update privacy tx: %w", err)
	}
	defer tx.Rollback()

	currentVersion, err := sqlcgen.New(tx).LockPrivacyVersion(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to lock privacy settings: %w", err)
	}

	if expectedVersion > 0 && currentVersion != expectedVersion {
		return nil, domain.ErrPreconditionFailed
	}

	newPrivacyVer := currentVersion + 1

	// Update user_privacy_settings
	updatePrivacySQL := `
		UPDATE user_privacy_settings
		SET public_profile_enabled = ?, show_bio = ?, show_token_total = ?,
		    show_trends = ?, show_activity_calendar = ?, show_agent_breakdown = ?,
		    show_skill_ranking = ?, show_achievements = ?, privacy_version = ?,
		    updated_at = ?
		WHERE user_id = ?`

	if _, err := tx.ExecContext(ctx, updatePrivacySQL,
		in.PublicProfileEnabled,
		in.ShowBio,
		in.ShowTokenTotal,
		in.ShowTrends,
		in.ShowActivityCalendar,
		in.ShowAgentBreakdown,
		in.ShowSkillRanking,
		in.ShowAchievements,
		newPrivacyVer,
		now,
		userID,
	); err != nil {
		return nil, fmt.Errorf("failed to update privacy settings: %w", err)
	}

	// Update user visibility in the same transaction as privacy and projection state.
	visibility := in.LeaderboardVisibility
	if visibility == "" {
		visibility = domain.LeaderboardVisibilityPrivate
		if in.PublicProfileEnabled {
			visibility = domain.LeaderboardVisibilityPublic
		}
	}

	updateUserSQL := `
		UPDATE users
		SET leaderboard_visibility = ?, public_profile_updated_at = ?, updated_at = ?
		WHERE user_id = ?`

	if _, err := tx.ExecContext(ctx, updateUserSQL, visibility, now, now, userID); err != nil {
		return nil, fmt.Errorf("failed to update user visibility: %w", err)
	}

	// Query user details to sync projection
	var handle, displayName, avatarURL, bio sql.NullString
	var accountStatus string
	var onboardingCompletedAt sql.NullTime
	var profileVer uint64

	err = tx.QueryRowContext(ctx, `
		SELECT handle, display_name, avatar_url, bio, account_status, onboarding_completed_at, profile_version
		FROM users
		WHERE user_id = ?`, userID).Scan(&handle, &displayName, &avatarURL, &bio, &accountStatus, &onboardingCompletedAt, &profileVer)
	if err != nil {
		return nil, fmt.Errorf("failed to query user for projection: %w", err)
	}

	if handle.Valid && handle.String != "" {
		profileStatus := domain.ProfileStatusHidden
		var publishedAt *time.Time
		if in.PublicProfileEnabled && accountStatus == string(domain.AccountStatusActive) && onboardingCompletedAt.Valid {
			profileStatus = domain.ProfileStatusPublished
			publishedAt = &now
		}

		var bioPtr *string
		if in.ShowBio && bio.Valid {
			bioPtr = &bio.String
		}

		upsertPubSQL := `
			INSERT INTO public_user_profiles (
				user_id, handle, display_name, avatar_url, bio,
				profile_status, show_bio, show_token_total, show_trends,
				show_activity_calendar, show_agent_breakdown, show_skill_ranking,
				show_achievements, source_profile_version, source_privacy_version,
				projection_version, published_at, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				handle = VALUES(handle),
				display_name = VALUES(display_name),
				avatar_url = VALUES(avatar_url),
				bio = VALUES(bio),
				profile_status = VALUES(profile_status),
				show_bio = VALUES(show_bio),
				show_token_total = VALUES(show_token_total),
				show_trends = VALUES(show_trends),
				show_activity_calendar = VALUES(show_activity_calendar),
				show_agent_breakdown = VALUES(show_agent_breakdown),
				show_skill_ranking = VALUES(show_skill_ranking),
				show_achievements = VALUES(show_achievements),
				source_profile_version = VALUES(source_profile_version),
				source_privacy_version = VALUES(source_privacy_version),
				projection_version = projection_version + 1,
				published_at = VALUES(published_at),
				updated_at = VALUES(updated_at)`

		if _, err := tx.ExecContext(ctx, upsertPubSQL,
			userID,
			handle.String,
			displayName.String,
			avatarURL,
			nullStringFromPtr(bioPtr),
			profileStatus,
			in.ShowBio,
			in.ShowTokenTotal,
			in.ShowTrends,
			in.ShowActivityCalendar,
			in.ShowAgentBreakdown,
			in.ShowSkillRanking,
			in.ShowAchievements,
			profileVer,
			newPrivacyVer,
			nullTimeFromPtr(publishedAt),
			now,
			now,
		); err != nil {
			return nil, fmt.Errorf("failed to upsert public user profile projection: %w", err)
		}
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, tx, event)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit update privacy tx: %w", err)
	}

	pCopy := in
	pCopy.UserID = userID
	pCopy.LeaderboardVisibility = visibility
	pCopy.PrivacyVersion = newPrivacyVer
	pCopy.UpdatedAt = now

	return &pCopy, nil
}

func (s *privacyStore) GetPublicProfileByHandle(ctx context.Context, handle string, now time.Time) (*domain.PublicUserProfile, error) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	row, err := sqlcgen.New(s.db).GetPublishedProfileByHandle(ctx, handle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query public profile by handle: %w", err)
	}
	return &domain.PublicUserProfile{
		UserID:               row.UserID,
		Handle:               row.Handle,
		DisplayName:          row.DisplayName,
		AvatarURL:            ptrFromNullString(row.AvatarUrl),
		Bio:                  ptrFromNullString(row.Bio),
		ProfileStatus:        domain.ProfileStatus(row.ProfileStatus),
		ShowBio:              row.ShowBio,
		ShowTokenTotal:       row.ShowTokenTotal,
		ShowTrends:           row.ShowTrends,
		ShowActivityCalendar: row.ShowActivityCalendar,
		ShowAgentBreakdown:   row.ShowAgentBreakdown,
		ShowSkillRanking:     row.ShowSkillRanking,
		ShowAchievements:     row.ShowAchievements,
		SourceProfileVersion: row.SourceProfileVersion,
		SourcePrivacyVersion: row.SourcePrivacyVersion,
		ProjectionVersion:    row.ProjectionVersion,
		PublishedAt:          ptrFromNullTime(row.PublishedAt),
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}, nil
}

func (s *privacyStore) SetAccountStatusTx(ctx context.Context, userID string, status domain.AccountStatus, now time.Time) error {
	if status != domain.AccountStatusActive && status != domain.AccountStatusSuspended {
		return domain.ErrInvalidArgument
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account status transaction: %w", err)
	}
	defer tx.Rollback()

	var currentStatus string
	if err := tx.QueryRowContext(ctx, "SELECT account_status FROM users WHERE user_id = ? FOR UPDATE", userID).Scan(&currentStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("lock account status: %w", err)
	}
	current := domain.AccountStatus(currentStatus)
	if current == status {
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit idempotent account status transaction: %w", err)
		}
		return nil
	}
	if !((current == domain.AccountStatusActive && status == domain.AccountStatusSuspended) ||
		(current == domain.AccountStatusSuspended && status == domain.AccountStatusActive)) {
		return domain.ErrConflict
	}
	if _, err := tx.ExecContext(ctx, "UPDATE users SET account_status = ?, updated_at = ? WHERE user_id = ?", status, now, userID); err != nil {
		return fmt.Errorf("update account status: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE public_user_profiles p
		JOIN users u ON u.user_id = p.user_id
		LEFT JOIN user_privacy_settings privacy ON privacy.user_id = p.user_id
		SET p.profile_status = CASE
				WHEN ? = 'active' AND u.onboarding_completed_at IS NOT NULL
					AND COALESCE(privacy.public_profile_enabled, FALSE) = TRUE THEN 'published'
				ELSE 'hidden'
			END,
			p.published_at = CASE
				WHEN ? = 'active' AND u.onboarding_completed_at IS NOT NULL
					AND COALESCE(privacy.public_profile_enabled, FALSE) = TRUE THEN ?
				ELSE NULL
			END,
			p.projection_version = p.projection_version + 1,
			p.updated_at = ?
		WHERE p.user_id = ?`, status, status, now, now, userID); err != nil {
		return fmt.Errorf("project account status: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account status transaction: %w", err)
	}
	return nil
}

func (s *privacyStore) RequestDeletionTx(ctx context.Context, req domain.DataDeletionRequest, event domain.UserSecurityEvent, now time.Time) (*domain.DataDeletionRequest, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin deletion request tx: %w", err)
	}
	defer tx.Rollback()

	var filterJSON []byte
	if len(req.ScopeFilterJSON) > 0 {
		filterJSON, err = json.Marshal(req.ScopeFilterJSON)
		if err != nil {
			return nil, fmt.Errorf("failed to encode deletion scope filter: %w", err)
		}
	}

	var installationID *string
	if value, ok := req.ScopeFilterJSON["installationId"].(string); ok && value != "" {
		installationID = &value
	}
	if req.DeletionScope == "installation" {
		if req.UserID == nil || installationID == nil {
			return nil, domain.ErrInvalidArgument
		}
		var owner string
		if err := tx.QueryRowContext(ctx, "SELECT user_id FROM installations WHERE installation_id = ? FOR UPDATE", *installationID).Scan(&owner); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, domain.ErrNotFound
			}
			return nil, fmt.Errorf("failed to lock deletion installation: %w", err)
		}
		if owner != *req.UserID {
			return nil, domain.ErrNotFound
		}
	}

	var activeAccountKey *string
	if req.DeletionScope == "account" && req.UserID != nil {
		activeAccountKey = req.UserID
	}

	phase := req.Phase
	if phase == "" {
		phase = "queued"
	}

	if req.DeletionScope == "account" && req.UserID != nil {
		var accountStatus string
		if err := tx.QueryRowContext(ctx, "SELECT account_status FROM users WHERE user_id = ? FOR UPDATE", *req.UserID).Scan(&accountStatus); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, domain.ErrNotFound
			}
			return nil, fmt.Errorf("failed to lock deletion user: %w", err)
		}
		if accountStatus == string(domain.AccountStatusDeleted) || accountStatus == string(domain.AccountStatusDeletionPending) {
			return nil, domain.ErrConflict
		}
	}

	insertSQL := `
		INSERT INTO data_deletion_requests (
			request_id, user_id, installation_id, deletion_scope, request_status, phase,
			progress_cursor, scope_filter_json, active_account_key,
			cancel_before, requested_at, next_attempt_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertSQL,
		req.RequestID,
		nullStringFromPtr(req.UserID),
		nullStringFromPtr(installationID),
		req.DeletionScope,
		req.RequestStatus,
		phase,
		req.ProgressCursor,
		filterJSON,
		nullStringFromPtr(activeAccountKey),
		nullTimeFromPtr(req.CancelBefore),
		req.RequestedAt,
		req.RequestedAt,
		req.RequestedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert data deletion request: %w", err)
	}

	if req.DeletionScope == "account" && req.UserID != nil {
		// Update user
		updateUserSQL := `
			UPDATE users
			SET account_status = 'deletion_pending', leaderboard_visibility = 'private', updated_at = ?
			WHERE user_id = ?`
		if _, err := tx.ExecContext(ctx, updateUserSQL, now, *req.UserID); err != nil {
			return nil, fmt.Errorf("failed to update user status to deletion_pending: %w", err)
		}

		if err := sqlcgen.New(tx).HidePublicProfileForDeletion(ctx, sqlcgen.HidePublicProfileForDeletionParams{
			UpdatedAt: now,
			UserID:    *req.UserID,
		}); err != nil {
			return nil, fmt.Errorf("failed to hide public user profile: %w", err)
		}
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, tx, event)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit deletion request tx: %w", err)
	}

	rCopy := req
	return &rCopy, nil
}

func (s *privacyStore) CancelDeletionTx(ctx context.Context, requestID string, userID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin cancel deletion tx: %w", err)
	}
	defer tx.Rollback()

	row, err := sqlcgen.New(tx).LockDeletionRequestForCancel(ctx, sqlcgen.LockDeletionRequestForCancelParams{
		RequestID: requestID,
		UserID:    sql.NullString{String: userID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("failed to lock deletion request: %w", err)
	}

	if row.RequestStatus != string(domain.DeletionStatusPending) || row.DeletionScope != "account" || !row.CancelWindowOpen.Valid || !row.CancelWindowOpen.Bool {
		return domain.ErrConflict
	}

	updateReqSQL := `
		UPDATE data_deletion_requests
		SET request_status = 'cancelled', phase = 'cancelled',
		    cancelled_at = CURRENT_TIMESTAMP(3), active_account_key = NULL,
		    claim_token = NULL, locked_by = NULL, lease_expires_at = NULL,
		    updated_at = CURRENT_TIMESTAMP(3)
		WHERE request_id = ? AND request_status = 'pending'`
	res, err := tx.ExecContext(ctx, updateReqSQL, requestID)
	if err != nil {
		return fmt.Errorf("failed to cancel deletion request: %w", err)
	}
	if affected, err := res.RowsAffected(); err != nil || affected != 1 {
		return domain.ErrConflict
	}

	if row.DeletionScope == "account" {
		updateUserSQL := `
			UPDATE users
			SET account_status = 'active', leaderboard_visibility = 'private', updated_at = ?
			WHERE user_id = ?`
		if _, err := tx.ExecContext(ctx, updateUserSQL, now, userID); err != nil {
			return fmt.Errorf("failed to restore user status: %w", err)
		}
	}

	return tx.Commit()
}

func (s *privacyStore) GetDeletionRequest(ctx context.Context, requestID string, userID string) (*domain.DataDeletionRequest, error) {
	row, err := sqlcgen.New(s.db).GetDeletionRequestByOwner(ctx, sqlcgen.GetDeletionRequestByOwnerParams{
		RequestID: requestID,
		UserID:    sql.NullString{String: userID, Valid: true},
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get deletion request: %w", err)
	}
	req := &domain.DataDeletionRequest{
		RequestID:      row.RequestID,
		UserID:         ptrFromNullString(row.UserID),
		DeletionScope:  row.DeletionScope,
		RequestStatus:  domain.DeletionRequestStatus(row.RequestStatus),
		Phase:          row.Phase,
		ProgressCursor: row.ProgressCursor,
		CancelBefore:   ptrFromNullTime(row.CancelBefore),
		CancelledAt:    ptrFromNullTime(row.CancelledAt),
		RequestedAt:    row.RequestedAt,
		CompletedAt:    ptrFromNullTime(row.CompletedAt),
		AuditReference: ptrFromNullString(row.AuditReference),
	}
	if len(row.ScopeFilterJson) > 0 {
		_ = json.Unmarshal(row.ScopeFilterJson, &req.ScopeFilterJSON)
	}
	return req, nil
}
