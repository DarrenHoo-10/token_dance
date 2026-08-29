package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/domain"
)

type profileStore struct {
	db *sql.DB
}

func (s *profileStore) GetUserProfile(ctx context.Context, userID string) (*domain.User, error) {
	auth := &authStore{db: s.db}
	return auth.FindUserByID(ctx, userID)
}

func (s *profileStore) CompleteOnboardingTx(ctx context.Context, userID string, handle string, displayName string, timezone string, locale string, privacy domain.UserPrivacySettings, event domain.UserSecurityEvent, now time.Time) (*domain.User, *domain.UserPrivacySettings, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to begin onboarding tx: %w", err)
	}
	defer tx.Rollback()

	handle = strings.ToLower(strings.TrimSpace(handle))

	// Lock user FOR UPDATE
	var u domain.User
	var authSubHash, emailHash []byte
	var currentHandle, currentDisplayName, avatarURL, avatarObjID, bio sql.NullString
	var emailVerifiedAt, onboardingCompletedAt, publicProfileUpdatedAt, deletedAt sql.NullTime

	queryUser := `
		SELECT user_id, auth_subject_hash, email_lookup_hash, email_ciphertext,
		       handle, email_verified_at, display_name, avatar_url, avatar_object_id,
		       bio, account_status, leaderboard_visibility, timezone_name, locale,
		       onboarding_completed_at, profile_version, public_profile_updated_at,
		       created_at, updated_at, deleted_at
		FROM users
		WHERE user_id = ?
		FOR UPDATE`

	err = tx.QueryRowContext(ctx, queryUser, userID).Scan(
		&u.UserID,
		&authSubHash,
		&emailHash,
		&u.EmailCiphertext,
		&currentHandle,
		&emailVerifiedAt,
		&currentDisplayName,
		&avatarURL,
		&avatarObjID,
		&bio,
		&u.AccountStatus,
		&u.LeaderboardVisibility,
		&u.TimezoneName,
		&u.Locale,
		&onboardingCompletedAt,
		&u.ProfileVersion,
		&publicProfileUpdatedAt,
		&u.CreatedAt,
		&u.UpdatedAt,
		&deletedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, domain.ErrNotFound
		}
		return nil, nil, fmt.Errorf("failed to lock user for onboarding: %w", err)
	}

	u.AuthSubjectHash = scanBytes32(authSubHash)
	u.EmailLookupHash = scanBytes32Ptr(emailHash)
	u.Handle = ptrFromNullString(currentHandle)
	u.EmailVerifiedAt = ptrFromNullTime(emailVerifiedAt)
	u.DisplayName = currentDisplayName.String
	u.AvatarURL = ptrFromNullString(avatarURL)
	u.AvatarObjectID = ptrFromNullString(avatarObjID)
	u.Bio = ptrFromNullString(bio)
	u.OnboardingCompletedAt = ptrFromNullTime(onboardingCompletedAt)
	u.PublicProfileUpdatedAt = ptrFromNullTime(publicProfileUpdatedAt)
	u.DeletedAt = ptrFromNullTime(deletedAt)

	if u.AccountStatus != domain.AccountStatusActive {
		return nil, nil, domain.ErrAccountSuspended
	}

	// Check handle availability
	avail, err := s.isHandleAvailableTx(ctx, tx, handle, userID, now)
	if err != nil {
		return nil, nil, err
	}
	if !avail {
		return nil, nil, domain.ErrHandleTaken
	}

	// Determine leaderboard visibility
	visibility := domain.LeaderboardVisibilityPrivate
	if privacy.PublicProfileEnabled {
		visibility = domain.LeaderboardVisibilityPublic
	}

	// Update user with FOR UPDATE lock protection and visibility
	newProfileVer := u.ProfileVersion + 1
	updateUserSQL := `
		UPDATE users
		SET handle = ?, display_name = ?, timezone_name = ?, locale = ?,
		    leaderboard_visibility = ?, onboarding_completed_at = ?, profile_version = ?, updated_at = ?
		WHERE user_id = ?`

	if _, err := tx.ExecContext(ctx, updateUserSQL, handle, displayName, timezone, locale, visibility, now, newProfileVer, now, userID); err != nil {
		return nil, nil, fmt.Errorf("failed to update user onboarding: %w", err)
	}

	// Upsert privacy settings
	newPrivacyVer := uint64(1)
	upsertPrivacySQL := `
		INSERT INTO user_privacy_settings (
			user_id, public_profile_enabled, show_bio, show_token_total,
			show_trends, show_activity_calendar, show_agent_breakdown,
			show_skill_ranking, show_achievements, privacy_version,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON DUPLICATE KEY UPDATE
			public_profile_enabled = VALUES(public_profile_enabled),
			show_bio = VALUES(show_bio),
			show_token_total = VALUES(show_token_total),
			show_trends = VALUES(show_trends),
			show_activity_calendar = VALUES(show_activity_calendar),
			show_agent_breakdown = VALUES(show_agent_breakdown),
			show_skill_ranking = VALUES(show_skill_ranking),
			show_achievements = VALUES(show_achievements),
			privacy_version = 1,
			updated_at = VALUES(updated_at)`

	if _, err := tx.ExecContext(ctx, upsertPrivacySQL,
		userID,
		privacy.PublicProfileEnabled,
		privacy.ShowBio,
		privacy.ShowTokenTotal,
		privacy.ShowTrends,
		privacy.ShowActivityCalendar,
		privacy.ShowAgentBreakdown,
		privacy.ShowSkillRanking,
		privacy.ShowAchievements,
		now,
		now,
	); err != nil {
		return nil, nil, fmt.Errorf("failed to upsert privacy settings: %w", err)
	}

	// Query updated privacy version
	_ = tx.QueryRowContext(ctx, "SELECT privacy_version FROM user_privacy_settings WHERE user_id = ?", userID).Scan(&newPrivacyVer)

	// Upsert public profile projection
	profileStatus := domain.ProfileStatusHidden
	var publishedAt *time.Time
	if privacy.PublicProfileEnabled {
		profileStatus = domain.ProfileStatusPublished
		publishedAt = &now
	}

	var pubBio *string
	if privacy.ShowBio {
		pubBio = u.Bio
	}

	upsertPublicProfileSQL := `
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

	if _, err := tx.ExecContext(ctx, upsertPublicProfileSQL,
		userID,
		handle,
		displayName,
		nullStringFromPtr(u.AvatarURL),
		nullStringFromPtr(pubBio),
		profileStatus,
		privacy.ShowBio,
		privacy.ShowTokenTotal,
		privacy.ShowTrends,
		privacy.ShowActivityCalendar,
		privacy.ShowAgentBreakdown,
		privacy.ShowSkillRanking,
		privacy.ShowAchievements,
		newProfileVer,
		newPrivacyVer,
		nullTimeFromPtr(publishedAt),
		now,
		now,
	); err != nil {
		return nil, nil, fmt.Errorf("failed to upsert public profile projection: %w", err)
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, tx, event)
	}

	if err := tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("failed to commit onboarding tx: %w", err)
	}

	u.Handle = &handle
	u.DisplayName = displayName
	u.TimezoneName = timezone
	u.Locale = locale
	u.LeaderboardVisibility = visibility
	u.OnboardingCompletedAt = &now
	u.ProfileVersion = newProfileVer
	u.UpdatedAt = now

	pCopy := privacy
	pCopy.UserID = userID
	pCopy.PrivacyVersion = newPrivacyVer
	pCopy.UpdatedAt = now

	return &u, &pCopy, nil
}

func (s *profileStore) UpdateProfileTx(ctx context.Context, userID string, displayName *string, handle *string, bio *string, timezone *string, locale *string, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin update profile tx: %w", err)
	}
	defer tx.Rollback()

	// Query user with FOR UPDATE
	query := `
		SELECT user_id, auth_subject_hash, email_lookup_hash, email_ciphertext,
		       handle, email_verified_at, display_name, avatar_url, avatar_object_id,
		       bio, account_status, leaderboard_visibility, timezone_name, locale,
		       onboarding_completed_at, profile_version, public_profile_updated_at,
		       created_at, updated_at, deleted_at
		FROM users
		WHERE user_id = ?
		FOR UPDATE`

	var u domain.User
	var authSubHash, emailHash []byte
	var currentHandle, avatarURL, avatarObjID, currentBio sql.NullString
	var emailVerifiedAt, onboardingCompletedAt, publicProfileUpdatedAt, deletedAt sql.NullTime

	err = tx.QueryRowContext(ctx, query, userID).Scan(
		&u.UserID,
		&authSubHash,
		&emailHash,
		&u.EmailCiphertext,
		&currentHandle,
		&emailVerifiedAt,
		&u.DisplayName,
		&avatarURL,
		&avatarObjID,
		&currentBio,
		&u.AccountStatus,
		&u.LeaderboardVisibility,
		&u.TimezoneName,
		&u.Locale,
		&onboardingCompletedAt,
		&u.ProfileVersion,
		&publicProfileUpdatedAt,
		&u.CreatedAt,
		&u.UpdatedAt,
		&deletedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to select user for update: %w", err)
	}

	u.AuthSubjectHash = scanBytes32(authSubHash)
	u.EmailLookupHash = scanBytes32Ptr(emailHash)
	u.Handle = ptrFromNullString(currentHandle)
	u.EmailVerifiedAt = ptrFromNullTime(emailVerifiedAt)
	u.AvatarURL = ptrFromNullString(avatarURL)
	u.AvatarObjectID = ptrFromNullString(avatarObjID)
	u.Bio = ptrFromNullString(currentBio)
	u.OnboardingCompletedAt = ptrFromNullTime(onboardingCompletedAt)
	u.PublicProfileUpdatedAt = ptrFromNullTime(publicProfileUpdatedAt)
	u.DeletedAt = ptrFromNullTime(deletedAt)

	if expectedVersion > 0 && u.ProfileVersion != expectedVersion {
		return nil, domain.ErrPreconditionFailed
	}

	if handle != nil {
		newHandle := strings.ToLower(strings.TrimSpace(*handle))
		if u.Handle == nil || *u.Handle != newHandle {
			avail, err := s.isHandleAvailableTx(ctx, tx, newHandle, userID, now)
			if err != nil {
				return nil, err
			}
			if !avail {
				return nil, domain.ErrHandleTaken
			}

			// Store old handle in history if present
			if u.Handle != nil && *u.Handle != "" {
				redirectUntil := now.Add(7 * 24 * time.Hour)
				reservedUntil := now.Add(30 * 24 * time.Hour)
				insertHistSQL := `
					INSERT INTO user_handle_history (handle, user_id, redirect_until, reserved_until, created_at)
					VALUES (?, ?, ?, ?, ?)
					ON DUPLICATE KEY UPDATE redirect_until = VALUES(redirect_until), reserved_until = VALUES(reserved_until)`
				if _, err := tx.ExecContext(ctx, insertHistSQL, *u.Handle, userID, redirectUntil, reservedUntil, now); err != nil {
					return nil, fmt.Errorf("failed to record handle history: %w", err)
				}
			}
			u.Handle = &newHandle
		}
	}

	if displayName != nil {
		u.DisplayName = *displayName
	}
	if bio != nil {
		u.Bio = bio
	}
	if timezone != nil {
		u.TimezoneName = *timezone
	}
	if locale != nil {
		u.Locale = *locale
	}

	u.ProfileVersion++
	u.UpdatedAt = now

	// Update user in DB
	updateSQL := `
		UPDATE users
		SET handle = ?, display_name = ?, bio = ?, timezone_name = ?, locale = ?,
		    profile_version = ?, updated_at = ?
		WHERE user_id = ?`

	if _, err := tx.ExecContext(ctx, updateSQL,
		nullStringFromPtr(u.Handle),
		u.DisplayName,
		nullStringFromPtr(u.Bio),
		u.TimezoneName,
		u.Locale,
		u.ProfileVersion,
		now,
		userID,
	); err != nil {
		return nil, fmt.Errorf("failed to update user: %w", err)
	}

	// Update public profile projection
	updatePubSQL := `
		UPDATE public_user_profiles
		SET handle = COALESCE(?, handle),
		    display_name = ?,
		    avatar_url = ?,
		    bio = CASE WHEN show_bio THEN ? ELSE NULL END,
		    source_profile_version = ?,
		    projection_version = projection_version + 1,
		    updated_at = ?
		WHERE user_id = ?`

	if _, err := tx.ExecContext(ctx, updatePubSQL,
		nullStringFromPtr(u.Handle),
		u.DisplayName,
		nullStringFromPtr(u.AvatarURL),
		nullStringFromPtr(u.Bio),
		u.ProfileVersion,
		now,
		userID,
	); err != nil {
		return nil, fmt.Errorf("failed to update public profile projection: %w", err)
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, tx, event)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit update profile tx: %w", err)
	}

	uCopy := u
	return &uCopy, nil
}

func (s *profileStore) IsHandleAvailable(ctx context.Context, handle string, excludeUserID string, now time.Time) (bool, error) {
	return s.isHandleAvailableTx(ctx, s.db, handle, excludeUserID, now)
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func (s *profileStore) isHandleAvailableTx(ctx context.Context, q queryer, handle string, excludeUserID string, now time.Time) (bool, error) {
	handle = strings.ToLower(strings.TrimSpace(handle))

	// Check users table
	var uid string
	err := q.QueryRowContext(ctx, "SELECT user_id FROM users WHERE handle = ? AND user_id != ? LIMIT 1", handle, excludeUserID).Scan(&uid)
	if err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("failed to query handle in users: %w", err)
	}

	// Check handle history table
	err = q.QueryRowContext(ctx, "SELECT user_id FROM user_handle_history WHERE handle = ? AND user_id != ? AND reserved_until > ? LIMIT 1", handle, excludeUserID, now).Scan(&uid)
	if err == nil {
		return false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("failed to query handle in history: %w", err)
	}

	return true, nil
}

func (s *profileStore) GetRedirectHandle(ctx context.Context, oldHandle string, now time.Time) (string, error) {
	oldHandle = strings.ToLower(strings.TrimSpace(oldHandle))
	query := `
		SELECT u.handle
		FROM user_handle_history h
		JOIN users u ON h.user_id = u.user_id
		WHERE h.handle = ?
		  AND h.redirect_until > ?
		LIMIT 1`

	var currentHandle sql.NullString
	err := s.db.QueryRowContext(ctx, query, oldHandle, now).Scan(&currentHandle)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("failed to query redirect handle: %w", err)
	}
	if !currentHandle.Valid || currentHandle.String == "" {
		return "", domain.ErrNotFound
	}
	return currentHandle.String, nil
}
