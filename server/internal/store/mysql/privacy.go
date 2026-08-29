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
)

type privacyStore struct {
	db *sql.DB
}

func (s *privacyStore) GetPrivacy(ctx context.Context, userID string) (*domain.UserPrivacySettings, error) {
	query := `
		SELECT p.user_id, p.public_profile_enabled, u.leaderboard_visibility,
		       p.show_bio, p.show_token_total, p.show_trends,
		       p.show_activity_calendar, p.show_agent_breakdown,
		       p.show_skill_ranking, p.show_achievements, p.privacy_version,
		       p.created_at, p.updated_at
		FROM user_privacy_settings p
		JOIN users u ON u.user_id = p.user_id
		WHERE p.user_id = ?
		LIMIT 1`

	var p domain.UserPrivacySettings
	err := s.db.QueryRowContext(ctx, query, userID).Scan(
		&p.UserID,
		&p.PublicProfileEnabled,
		&p.LeaderboardVisibility,
		&p.ShowBio,
		&p.ShowTokenTotal,
		&p.ShowTrends,
		&p.ShowActivityCalendar,
		&p.ShowAgentBreakdown,
		&p.ShowSkillRanking,
		&p.ShowAchievements,
		&p.PrivacyVersion,
		&p.CreatedAt,
		&p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get privacy settings: %w", err)
	}

	return &p, nil
}

func (s *privacyStore) UpdatePrivacyTx(ctx context.Context, userID string, in domain.UserPrivacySettings, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.UserPrivacySettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin update privacy tx: %w", err)
	}
	defer tx.Rollback()

	var currentVersion uint64
	err = tx.QueryRowContext(ctx, "SELECT privacy_version FROM user_privacy_settings WHERE user_id = ? FOR UPDATE", userID).Scan(&currentVersion)
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
	query := `
		SELECT p.user_id, p.handle, p.display_name, p.avatar_url, p.bio,
		       p.profile_status, p.show_bio, p.show_token_total, p.show_trends,
		       p.show_activity_calendar, p.show_agent_breakdown, p.show_skill_ranking,
		       p.show_achievements, p.source_profile_version, p.source_privacy_version,
		       p.projection_version, p.published_at, p.created_at, p.updated_at
		FROM public_user_profiles p
		JOIN users u ON p.user_id = u.user_id
		WHERE p.handle = ?
		  AND p.profile_status = 'published'
		  AND u.account_status = 'active'
		LIMIT 1`

	var pub domain.PublicUserProfile
	var avatarURL, bio sql.NullString
	var publishedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, handle).Scan(
		&pub.UserID,
		&pub.Handle,
		&pub.DisplayName,
		&avatarURL,
		&bio,
		&pub.ProfileStatus,
		&pub.ShowBio,
		&pub.ShowTokenTotal,
		&pub.ShowTrends,
		&pub.ShowActivityCalendar,
		&pub.ShowAgentBreakdown,
		&pub.ShowSkillRanking,
		&pub.ShowAchievements,
		&pub.SourceProfileVersion,
		&pub.SourcePrivacyVersion,
		&pub.ProjectionVersion,
		&publishedAt,
		&pub.CreatedAt,
		&pub.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query public profile by handle: %w", err)
	}

	pub.AvatarURL = ptrFromNullString(avatarURL)
	pub.Bio = ptrFromNullString(bio)
	pub.PublishedAt = ptrFromNullTime(publishedAt)

	return &pub, nil
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

		// Update public profile
		updatePubSQL := `
			UPDATE public_user_profiles
			SET profile_status = 'hidden', projection_version = projection_version + 1, updated_at = ?
			WHERE user_id = ?`
		if _, err := tx.ExecContext(ctx, updatePubSQL, now, *req.UserID); err != nil {
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

	query := `
		SELECT request_status, deletion_scope,
		       cancel_before IS NOT NULL AND cancel_before > CURRENT_TIMESTAMP(3)
		FROM data_deletion_requests
		WHERE request_id = ? AND user_id = ?
		FOR UPDATE`

	var status, scope string
	var cancelWindowOpen bool
	err = tx.QueryRowContext(ctx, query, requestID, userID).Scan(&status, &scope, &cancelWindowOpen)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("failed to lock deletion request: %w", err)
	}

	if status != string(domain.DeletionStatusPending) || scope != "account" || !cancelWindowOpen {
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

	if scope == "account" {
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
	query := `
		SELECT request_id, user_id, deletion_scope, scope_filter_json,
		       request_status, phase, progress_cursor, cancel_before,
		       cancelled_at, requested_at, completed_at, audit_reference
		FROM data_deletion_requests
		WHERE request_id = ? AND user_id = ?
		LIMIT 1`

	var req domain.DataDeletionRequest
	var uid, auditRef sql.NullString
	var filterJSON []byte
	var cancelBefore, cancelledAt, completedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, requestID, userID).Scan(
		&req.RequestID,
		&uid,
		&req.DeletionScope,
		&filterJSON,
		&req.RequestStatus,
		&req.Phase,
		&req.ProgressCursor,
		&cancelBefore,
		&cancelledAt,
		&req.RequestedAt,
		&completedAt,
		&auditRef,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get deletion request: %w", err)
	}

	req.UserID = ptrFromNullString(uid)
	req.AuditReference = ptrFromNullString(auditRef)
	req.CancelBefore = ptrFromNullTime(cancelBefore)
	req.CancelledAt = ptrFromNullTime(cancelledAt)
	req.CompletedAt = ptrFromNullTime(completedAt)
	if len(filterJSON) > 0 {
		_ = json.Unmarshal(filterJSON, &req.ScopeFilterJSON)
	}

	return &req, nil
}
