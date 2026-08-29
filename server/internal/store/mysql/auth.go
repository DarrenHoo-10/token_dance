package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type authStore struct {
	db *sql.DB
}

func (s *authStore) CreateOrReplaceEmailChallenge(ctx context.Context, challenge domain.EmailChallenge, outbox domain.EmailOutbox) (*domain.EmailChallenge, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	// Cancel any active pending challenges for this type and email
	cancelSQL := `
		UPDATE email_challenges
		SET challenge_status = 'cancelled', updated_at = ?
		WHERE challenge_type = ?
		  AND email_lookup_hash = ?
		  AND challenge_status = 'pending'`
	if _, err := tx.ExecContext(ctx, cancelSQL, now, challenge.ChallengeType, bytes32Slice(challenge.EmailLookupHash)); err != nil {
		return nil, fmt.Errorf("failed to cancel pending challenges: %w", err)
	}

	// Insert challenge
	insertChallengeSQL := `
		INSERT INTO email_challenges (
			challenge_id, user_id, email_lookup_hash, email_ciphertext, email_key_version,
			challenge_type, code_hash, code_key_version, challenge_status, attempt_count,
			max_attempts, send_count, requested_ip_prefix_hash, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertChallengeSQL,
		challenge.ChallengeID,
		nullStringFromPtr(challenge.UserID),
		bytes32Slice(challenge.EmailLookupHash),
		challenge.EmailCiphertext,
		challenge.EmailKeyVersion,
		challenge.ChallengeType,
		bytes32Slice(challenge.CodeHash),
		challenge.CodeKeyVersion,
		challenge.ChallengeStatus,
		challenge.AttemptCount,
		challenge.MaxAttempts,
		challenge.SendCount,
		bytes32PtrSlice(challenge.RequestedIPPrefixHash),
		challenge.ExpiresAt,
		challenge.CreatedAt,
		challenge.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert challenge: %w", err)
	}

	nextAttemptAt := outbox.NextAttemptAt
	if nextAttemptAt.IsZero() {
		nextAttemptAt = now
	}
	expiresAt := outbox.ExpiresAt
	if expiresAt.IsZero() || !expiresAt.After(now) {
		expiresAt = now.Add(24 * time.Hour)
	}

	// Insert outbox
	insertOutboxSQL := `
		INSERT INTO email_outbox (
			email_id, user_id, challenge_id, idempotency_key, template_key, locale,
			recipient_ciphertext, payload_ciphertext, encryption_key_version, delivery_status,
			attempt_count, next_attempt_at, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertOutboxSQL,
		outbox.EmailID,
		nullStringFromPtr(outbox.UserID),
		nullStringFromPtr(outbox.ChallengeID),
		bytes32Slice(outbox.IdempotencyKey),
		outbox.TemplateKey,
		outbox.Locale,
		outbox.RecipientCiphertext,
		outbox.PayloadCiphertext,
		outbox.EncryptionKeyVersion,
		outbox.DeliveryStatus,
		outbox.AttemptCount,
		nextAttemptAt,
		expiresAt,
		outbox.CreatedAt,
		outbox.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert outbox: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit tx: %w", err)
	}

	cCopy := challenge
	return &cCopy, nil
}

func (s *authStore) FindPendingEmailChallenge(ctx context.Context, challengeType domain.ChallengeType, emailLookupHash [32]byte) (*domain.EmailChallenge, error) {
	query := `
		SELECT challenge_id, user_id, email_lookup_hash, email_ciphertext, email_key_version,
		       challenge_type, code_hash, code_key_version, challenge_status, attempt_count,
		       max_attempts, send_count, requested_ip_prefix_hash, expires_at, consumed_at,
		       created_at, updated_at
		FROM email_challenges
		WHERE challenge_type = ?
		  AND email_lookup_hash = ?
		  AND challenge_status = 'pending'
		LIMIT 1`

	var c domain.EmailChallenge
	var uid sql.NullString
	var emailHash, codeHash []byte
	var ipHash []byte
	var consumedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, challengeType, bytes32Slice(emailLookupHash)).Scan(
		&c.ChallengeID,
		&uid,
		&emailHash,
		&c.EmailCiphertext,
		&c.EmailKeyVersion,
		&c.ChallengeType,
		&codeHash,
		&c.CodeKeyVersion,
		&c.ChallengeStatus,
		&c.AttemptCount,
		&c.MaxAttempts,
		&c.SendCount,
		&ipHash,
		&c.ExpiresAt,
		&consumedAt,
		&c.CreatedAt,
		&c.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query pending email challenge: %w", err)
	}

	c.UserID = ptrFromNullString(uid)
	c.EmailLookupHash = scanBytes32(emailHash)
	c.CodeHash = scanBytes32(codeHash)
	c.RequestedIPPrefixHash = scanBytes32Ptr(ipHash)
	c.ConsumedAt = ptrFromNullTime(consumedAt)

	return &c, nil
}

func (s *authStore) UpdateEmailChallengeAttempts(ctx context.Context, challengeID string, attemptCount uint16, status domain.ChallengeStatus) error {
	query := `
		UPDATE email_challenges
		SET attempt_count = ?, challenge_status = ?, updated_at = ?
		WHERE challenge_id = ?`

	res, err := s.db.ExecContext(ctx, query, attemptCount, status, time.Now().UTC(), challengeID)
	if err != nil {
		return fmt.Errorf("failed to update email challenge attempts: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *authStore) CompleteRegistrationTx(ctx context.Context, in store.RegistrationTxInput) (*domain.UserSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	// Check email uniqueness
	if in.User.EmailLookupHash != nil {
		var existingID string
		err := tx.QueryRowContext(ctx, "SELECT user_id FROM users WHERE email_lookup_hash = ? LIMIT 1", bytes32PtrSlice(in.User.EmailLookupHash)).Scan(&existingID)
		if err == nil {
			return nil, domain.ErrAccountExists
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to check email uniqueness: %w", err)
		}
	}

	// Lock & verify challenge
	var challengeStatus domain.ChallengeStatus
	err = tx.QueryRowContext(ctx, "SELECT challenge_status FROM email_challenges WHERE challenge_id = ? FOR UPDATE", in.ChallengeID).Scan(&challengeStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrChallengeInvalid
		}
		return nil, fmt.Errorf("failed to lock challenge: %w", err)
	}
	if challengeStatus != domain.ChallengeStatusPending {
		return nil, domain.ErrChallengeInvalid
	}

	// 1. Insert user first to satisfy foreign keys
	insertUserSQL := `
		INSERT INTO users (
			user_id, auth_subject_hash, email_lookup_hash, email_ciphertext, handle,
			email_verified_at, display_name, avatar_url, avatar_object_id, bio,
			account_status, leaderboard_visibility, timezone_name, locale,
			onboarding_completed_at, profile_version, public_profile_updated_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertUserSQL,
		in.User.UserID,
		bytes32Slice(in.User.AuthSubjectHash),
		bytes32PtrSlice(in.User.EmailLookupHash),
		in.User.EmailCiphertext,
		nullStringFromPtr(in.User.Handle),
		nullTimeFromPtr(in.User.EmailVerifiedAt),
		in.User.DisplayName,
		nullStringFromPtr(in.User.AvatarURL),
		nullStringFromPtr(in.User.AvatarObjectID),
		nullStringFromPtr(in.User.Bio),
		in.User.AccountStatus,
		in.User.LeaderboardVisibility,
		in.User.TimezoneName,
		in.User.Locale,
		nullTimeFromPtr(in.User.OnboardingCompletedAt),
		in.User.ProfileVersion,
		nullTimeFromPtr(in.User.PublicProfileUpdatedAt),
		in.User.CreatedAt,
		in.User.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert user: %w", err)
	}

	// 2. Update challenge with user_id pointer
	updateChallengeSQL := `
		UPDATE email_challenges
		SET challenge_status = 'consumed', consumed_at = ?, user_id = ?, updated_at = ?
		WHERE challenge_id = ?`
	if _, err := tx.ExecContext(ctx, updateChallengeSQL, now, in.User.UserID, now, in.ChallengeID); err != nil {
		return nil, fmt.Errorf("failed to mark challenge consumed: %w", err)
	}

	// Insert credential
	insertCredSQL := `
		INSERT INTO user_password_credentials (
			user_id, password_hash, password_algorithm, credential_version,
			failed_login_count, locked_until, last_failed_login_at,
			password_changed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertCredSQL,
		in.Credential.UserID,
		in.Credential.PasswordHash,
		in.Credential.PasswordAlgorithm,
		in.Credential.CredentialVersion,
		in.Credential.FailedLoginCount,
		nullTimeFromPtr(in.Credential.LockedUntil),
		nullTimeFromPtr(in.Credential.LastFailedLoginAt),
		in.Credential.PasswordChangedAt,
		in.Credential.CreatedAt,
		in.Credential.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert user credentials: %w", err)
	}

	// Insert privacy settings
	insertPrivacySQL := `
		INSERT INTO user_privacy_settings (
			user_id, public_profile_enabled, show_bio, show_token_total,
			show_trends, show_activity_calendar, show_agent_breakdown,
			show_skill_ranking, show_achievements, privacy_version,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertPrivacySQL,
		in.Privacy.UserID,
		in.Privacy.PublicProfileEnabled,
		in.Privacy.ShowBio,
		in.Privacy.ShowTokenTotal,
		in.Privacy.ShowTrends,
		in.Privacy.ShowActivityCalendar,
		in.Privacy.ShowAgentBreakdown,
		in.Privacy.ShowSkillRanking,
		in.Privacy.ShowAchievements,
		in.Privacy.PrivacyVersion,
		in.Privacy.CreatedAt,
		in.Privacy.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert privacy settings: %w", err)
	}

	// Insert session
	insertSessionSQL := `
		INSERT INTO user_sessions (
			session_id, user_id, session_token_hash, csrf_token_hash,
			credential_version, session_status, device_label,
			user_agent_hash, ip_prefix_hash, last_seen_at,
			idle_expires_at, absolute_expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertSessionSQL,
		in.Session.SessionID,
		in.Session.UserID,
		bytes32Slice(in.Session.SessionTokenHash),
		bytes32Slice(in.Session.CSRFTokenHash),
		in.Session.CredentialVersion,
		in.Session.SessionStatus,
		nullStringFromPtr(in.Session.DeviceLabel),
		bytes32PtrSlice(in.Session.UserAgentHash),
		bytes32PtrSlice(in.Session.IPPrefixHash),
		in.Session.LastSeenAt,
		in.Session.IdleExpiresAt,
		in.Session.AbsoluteExpiresAt,
		in.Session.CreatedAt,
		in.Session.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert user session: %w", err)
	}

	// Insert security event
	if in.SecurityEvent.EventID != "" {
		insertSecurityEvent(ctx, tx, in.SecurityEvent)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit registration: %w", err)
	}

	sessCopy := in.Session
	return &sessCopy, nil
}

func (s *authStore) FindUserByEmailHash(ctx context.Context, emailLookupHash [32]byte) (*domain.User, *domain.UserPasswordCredential, error) {
	query := `
		SELECT u.user_id, u.auth_subject_hash, u.email_lookup_hash, u.email_ciphertext,
		       u.handle, u.email_verified_at, u.display_name, u.avatar_url, u.avatar_object_id,
		       u.bio, u.account_status, u.leaderboard_visibility, u.timezone_name, u.locale,
		       u.onboarding_completed_at, u.profile_version, u.public_profile_updated_at,
		       u.created_at, u.updated_at, u.deleted_at,
		       c.password_hash, c.password_algorithm, c.credential_version,
		       c.failed_login_count, c.locked_until, c.last_failed_login_at,
		       c.password_changed_at, c.created_at, c.updated_at
		FROM users u
		JOIN user_password_credentials c ON u.user_id = c.user_id
		WHERE u.email_lookup_hash = ?
		  AND (u.account_status != 'deleted' OR u.account_status IS NULL)
		LIMIT 1`

	var u domain.User
	var cred domain.UserPasswordCredential
	var authSubHash, emailHash []byte
	var handle, avatarURL, avatarObjID, bio sql.NullString
	var emailVerifiedAt, onboardingCompletedAt, publicProfileUpdatedAt, deletedAt sql.NullTime
	var lockedUntil, lastFailedLoginAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, bytes32Slice(emailLookupHash)).Scan(
		&u.UserID,
		&authSubHash,
		&emailHash,
		&u.EmailCiphertext,
		&handle,
		&emailVerifiedAt,
		&u.DisplayName,
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
		&cred.PasswordHash,
		&cred.PasswordAlgorithm,
		&cred.CredentialVersion,
		&cred.FailedLoginCount,
		&lockedUntil,
		&lastFailedLoginAt,
		&cred.PasswordChangedAt,
		&cred.CreatedAt,
		&cred.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, domain.ErrNotFound
		}
		return nil, nil, fmt.Errorf("failed to query user by email hash: %w", err)
	}

	u.AuthSubjectHash = scanBytes32(authSubHash)
	u.EmailLookupHash = scanBytes32Ptr(emailHash)
	u.Handle = ptrFromNullString(handle)
	u.EmailVerifiedAt = ptrFromNullTime(emailVerifiedAt)
	u.AvatarURL = ptrFromNullString(avatarURL)
	u.AvatarObjectID = ptrFromNullString(avatarObjID)
	u.Bio = ptrFromNullString(bio)
	u.OnboardingCompletedAt = ptrFromNullTime(onboardingCompletedAt)
	u.PublicProfileUpdatedAt = ptrFromNullTime(publicProfileUpdatedAt)
	u.DeletedAt = ptrFromNullTime(deletedAt)

	cred.UserID = u.UserID
	cred.LockedUntil = ptrFromNullTime(lockedUntil)
	cred.LastFailedLoginAt = ptrFromNullTime(lastFailedLoginAt)

	return &u, &cred, nil
}

func (s *authStore) FindUserByID(ctx context.Context, userID string) (*domain.User, error) {
	query := `
		SELECT user_id, auth_subject_hash, email_lookup_hash, email_ciphertext,
		       handle, email_verified_at, display_name, avatar_url, avatar_object_id,
		       bio, account_status, leaderboard_visibility, timezone_name, locale,
		       onboarding_completed_at, profile_version, public_profile_updated_at,
		       created_at, updated_at, deleted_at
		FROM users
		WHERE user_id = ?
		LIMIT 1`

	var u domain.User
	var authSubHash, emailHash []byte
	var handle, avatarURL, avatarObjID, bio sql.NullString
	var emailVerifiedAt, onboardingCompletedAt, publicProfileUpdatedAt, deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, userID).Scan(
		&u.UserID,
		&authSubHash,
		&emailHash,
		&u.EmailCiphertext,
		&handle,
		&emailVerifiedAt,
		&u.DisplayName,
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
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query user by ID: %w", err)
	}

	u.AuthSubjectHash = scanBytes32(authSubHash)
	u.EmailLookupHash = scanBytes32Ptr(emailHash)
	u.Handle = ptrFromNullString(handle)
	u.EmailVerifiedAt = ptrFromNullTime(emailVerifiedAt)
	u.AvatarURL = ptrFromNullString(avatarURL)
	u.AvatarObjectID = ptrFromNullString(avatarObjID)
	u.Bio = ptrFromNullString(bio)
	u.OnboardingCompletedAt = ptrFromNullTime(onboardingCompletedAt)
	u.PublicProfileUpdatedAt = ptrFromNullTime(publicProfileUpdatedAt)
	u.DeletedAt = ptrFromNullTime(deletedAt)

	return &u, nil
}

func (s *authStore) RecordLoginFailure(ctx context.Context, userID string, failedCount uint16, lockedUntil *time.Time, event domain.UserSecurityEvent) error {
	now := time.Now().UTC()
	query := `
		UPDATE user_password_credentials
		SET failed_login_count = ?, locked_until = ?, last_failed_login_at = ?, updated_at = ?
		WHERE user_id = ?`

	if _, err := s.db.ExecContext(ctx, query, failedCount, nullTimeFromPtr(lockedUntil), now, now, userID); err != nil {
		return fmt.Errorf("failed to record login failure: %w", err)
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, s.db, event)
	}
	return nil
}

func (s *authStore) CreateSessionTx(ctx context.Context, session domain.UserSession, event domain.UserSecurityEvent) (*domain.UserSession, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	// Clear failed login count
	clearSQL := `
		UPDATE user_password_credentials
		SET failed_login_count = 0, locked_until = NULL, updated_at = ?
		WHERE user_id = ?`
	if _, err := tx.ExecContext(ctx, clearSQL, now, session.UserID); err != nil {
		return nil, fmt.Errorf("failed to clear failed login count: %w", err)
	}

	// Check max 20 active sessions limit - revoke oldest if >= 20
	rows, err := tx.QueryContext(ctx, "SELECT session_id FROM user_sessions WHERE user_id = ? AND session_status = 'active' ORDER BY last_seen_at ASC FOR UPDATE", session.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to query active sessions: %w", err)
	}

	var activeSessionIDs []string
	for rows.Next() {
		var sID string
		if err := rows.Scan(&sID); err == nil {
			activeSessionIDs = append(activeSessionIDs, sID)
		}
	}
	rows.Close()

	if len(activeSessionIDs) >= 20 {
		toRevoke := len(activeSessionIDs) - 19
		for i := 0; i < toRevoke; i++ {
			revokeSQL := `
				UPDATE user_sessions
				SET session_status = 'revoked', revoked_at = ?, revoke_reason = 'session_limit', updated_at = ?
				WHERE session_id = ?`
			if _, err := tx.ExecContext(ctx, revokeSQL, now, now, activeSessionIDs[i]); err != nil {
				return nil, fmt.Errorf("failed to revoke old session: %w", err)
			}
		}
	}

	// Insert session
	insertSessionSQL := `
		INSERT INTO user_sessions (
			session_id, user_id, session_token_hash, csrf_token_hash,
			credential_version, session_status, device_label,
			user_agent_hash, ip_prefix_hash, last_seen_at,
			idle_expires_at, absolute_expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertSessionSQL,
		session.SessionID,
		session.UserID,
		bytes32Slice(session.SessionTokenHash),
		bytes32Slice(session.CSRFTokenHash),
		session.CredentialVersion,
		session.SessionStatus,
		nullStringFromPtr(session.DeviceLabel),
		bytes32PtrSlice(session.UserAgentHash),
		bytes32PtrSlice(session.IPPrefixHash),
		session.LastSeenAt,
		session.IdleExpiresAt,
		session.AbsoluteExpiresAt,
		session.CreatedAt,
		session.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert session: %w", err)
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, tx, event)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit create session tx: %w", err)
	}

	sCopy := session
	return &sCopy, nil
}

func (s *authStore) ResolveSession(ctx context.Context, tokenHash [32]byte, now time.Time) (*domain.UserSession, *domain.User, error) {
	query := `
		SELECT s.session_id, s.user_id, s.session_token_hash, s.csrf_token_hash,
		       s.credential_version, s.session_status, s.device_label,
		       s.user_agent_hash, s.ip_prefix_hash, s.last_seen_at,
		       s.idle_expires_at, s.absolute_expires_at, s.revoked_at, s.revoke_reason,
		       s.created_at, s.updated_at,
		       u.auth_subject_hash, u.email_lookup_hash, u.email_ciphertext,
		       u.handle, u.email_verified_at, u.display_name, u.avatar_url, u.avatar_object_id,
		       u.bio, u.account_status, u.leaderboard_visibility, u.timezone_name, u.locale,
		       u.onboarding_completed_at, u.profile_version, u.public_profile_updated_at,
		       u.created_at, u.updated_at, u.deleted_at,
		       c.credential_version
		FROM user_sessions s
		JOIN users u ON s.user_id = u.user_id
		JOIN user_password_credentials c ON u.user_id = c.user_id
		WHERE s.session_token_hash = ?
		LIMIT 1`

	var sess domain.UserSession
	var u domain.User
	var credVer uint32
	var sessTokenHash, csrfTokenHash, userAgentHash, ipPrefixHash []byte
	var devLabel, revokeReason sql.NullString
	var revokedAt sql.NullTime
	var authSubHash, emailHash []byte
	var handle, avatarURL, avatarObjID, bio sql.NullString
	var emailVerifiedAt, onboardingCompletedAt, publicProfileUpdatedAt, deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, bytes32Slice(tokenHash)).Scan(
		&sess.SessionID,
		&sess.UserID,
		&sessTokenHash,
		&csrfTokenHash,
		&sess.CredentialVersion,
		&sess.SessionStatus,
		&devLabel,
		&userAgentHash,
		&ipPrefixHash,
		&sess.LastSeenAt,
		&sess.IdleExpiresAt,
		&sess.AbsoluteExpiresAt,
		&revokedAt,
		&revokeReason,
		&sess.CreatedAt,
		&sess.UpdatedAt,
		&authSubHash,
		&emailHash,
		&u.EmailCiphertext,
		&handle,
		&emailVerifiedAt,
		&u.DisplayName,
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
		&credVer,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, domain.ErrUnauthorized
		}
		return nil, nil, fmt.Errorf("failed to resolve session: %w", err)
	}

	if sess.SessionStatus != domain.SessionStatusActive {
		return nil, nil, domain.ErrUnauthorized
	}
	if now.After(sess.AbsoluteExpiresAt) || now.After(sess.IdleExpiresAt) {
		return nil, nil, domain.ErrUnauthorized
	}
	if credVer != sess.CredentialVersion {
		return nil, nil, domain.ErrUnauthorized
	}

	sess.SessionTokenHash = scanBytes32(sessTokenHash)
	sess.CSRFTokenHash = scanBytes32(csrfTokenHash)
	sess.DeviceLabel = ptrFromNullString(devLabel)
	sess.UserAgentHash = scanBytes32Ptr(userAgentHash)
	sess.IPPrefixHash = scanBytes32Ptr(ipPrefixHash)
	sess.RevokedAt = ptrFromNullTime(revokedAt)
	sess.RevokeReason = ptrFromNullString(revokeReason)

	u.UserID = sess.UserID
	u.AuthSubjectHash = scanBytes32(authSubHash)
	u.EmailLookupHash = scanBytes32Ptr(emailHash)
	u.Handle = ptrFromNullString(handle)
	u.EmailVerifiedAt = ptrFromNullTime(emailVerifiedAt)
	u.AvatarURL = ptrFromNullString(avatarURL)
	u.AvatarObjectID = ptrFromNullString(avatarObjID)
	u.Bio = ptrFromNullString(bio)
	u.OnboardingCompletedAt = ptrFromNullTime(onboardingCompletedAt)
	u.PublicProfileUpdatedAt = ptrFromNullTime(publicProfileUpdatedAt)
	u.DeletedAt = ptrFromNullTime(deletedAt)

	return &sess, &u, nil
}

func (s *authStore) RevokeSession(ctx context.Context, sessionID string, reason string, now time.Time) error {
	query := `
		UPDATE user_sessions
		SET session_status = 'revoked', revoked_at = ?, revoke_reason = ?, updated_at = ?
		WHERE session_id = ?`

	res, err := s.db.ExecContext(ctx, query, now, reason, now, sessionID)
	if err != nil {
		return fmt.Errorf("failed to revoke session: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *authStore) RevokeOtherSessions(ctx context.Context, userID, currentSessionID string, reason string, now time.Time, event domain.UserSecurityEvent) error {
	query := `
		UPDATE user_sessions
		SET session_status = 'revoked', revoked_at = ?, revoke_reason = ?, updated_at = ?
		WHERE user_id = ?
		  AND session_id != ?
		  AND session_status = 'active'`

	if _, err := s.db.ExecContext(ctx, query, now, reason, now, userID, currentSessionID); err != nil {
		return fmt.Errorf("failed to revoke other sessions: %w", err)
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, s.db, event)
	}
	return nil
}

func (s *authStore) ListUserSessions(ctx context.Context, userID string) ([]domain.UserSession, error) {
	query := `
		SELECT session_id, user_id, session_token_hash, csrf_token_hash,
		       credential_version, session_status, device_label,
		       user_agent_hash, ip_prefix_hash, last_seen_at,
		       idle_expires_at, absolute_expires_at, revoked_at, revoke_reason,
		       created_at, updated_at
		FROM user_sessions
		WHERE user_id = ?
		ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list user sessions: %w", err)
	}
	defer rows.Close()

	var list []domain.UserSession
	for rows.Next() {
		var sess domain.UserSession
		var sessTokenHash, csrfTokenHash, uaHash, ipHash []byte
		var devLabel, revokeReason sql.NullString
		var revokedAt sql.NullTime

		if err := rows.Scan(
			&sess.SessionID,
			&sess.UserID,
			&sessTokenHash,
			&csrfTokenHash,
			&sess.CredentialVersion,
			&sess.SessionStatus,
			&devLabel,
			&uaHash,
			&ipHash,
			&sess.LastSeenAt,
			&sess.IdleExpiresAt,
			&sess.AbsoluteExpiresAt,
			&revokedAt,
			&revokeReason,
			&sess.CreatedAt,
			&sess.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan session row: %w", err)
		}

		sess.SessionTokenHash = scanBytes32(sessTokenHash)
		sess.CSRFTokenHash = scanBytes32(csrfTokenHash)
		sess.DeviceLabel = ptrFromNullString(devLabel)
		sess.UserAgentHash = scanBytes32Ptr(uaHash)
		sess.IPPrefixHash = scanBytes32Ptr(ipHash)
		sess.RevokedAt = ptrFromNullTime(revokedAt)
		sess.RevokeReason = ptrFromNullString(revokeReason)

		list = append(list, sess)
	}

	return list, nil
}

func (s *authStore) ResetPasswordTx(ctx context.Context, userID string, challengeID string, newHash string, newVersion uint32, event domain.UserSecurityEvent, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer tx.Rollback()

	// Mark challenge consumed
	updateChallengeSQL := `
		UPDATE email_challenges
		SET challenge_status = 'consumed', consumed_at = ?, updated_at = ?
		WHERE challenge_id = ?`
	if _, err := tx.ExecContext(ctx, updateChallengeSQL, now, now, challengeID); err != nil {
		return fmt.Errorf("failed to consume challenge on reset: %w", err)
	}

	// Update credentials
	updateCredSQL := `
		UPDATE user_password_credentials
		SET password_hash = ?, credential_version = ?, password_changed_at = ?,
		    failed_login_count = 0, locked_until = NULL, updated_at = ?
		WHERE user_id = ?`
	res, err := tx.ExecContext(ctx, updateCredSQL, newHash, newVersion, now, now, userID)
	if err != nil {
		return fmt.Errorf("failed to update credentials: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrNotFound
	}

	// Revoke all active sessions
	revokeAllSQL := `
		UPDATE user_sessions
		SET session_status = 'revoked', revoked_at = ?, revoke_reason = 'password_reset', updated_at = ?
		WHERE user_id = ? AND session_status = 'active'`
	if _, err := tx.ExecContext(ctx, revokeAllSQL, now, now, userID); err != nil {
		return fmt.Errorf("failed to revoke sessions on password reset: %w", err)
	}

	if event.EventID != "" {
		insertSecurityEvent(ctx, tx, event)
	}

	return tx.Commit()
}

func (s *authStore) TouchSessionLastSeen(ctx context.Context, sessionID string, now time.Time) error {
	query := "UPDATE user_sessions SET last_seen_at = ? WHERE session_id = ?"
	_, err := s.db.ExecContext(ctx, query, now, sessionID)
	return err
}

type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
}

func insertSecurityEvent(ctx context.Context, exec sqlExecutor, ev domain.UserSecurityEvent) {
	var metaJSON []byte
	if len(ev.MetadataJSON) > 0 {
		metaJSON, _ = json.Marshal(ev.MetadataJSON)
	}

	query := `
		INSERT INTO user_security_events (
			event_id, user_id, session_id, event_type, outcome,
			subject_lookup_hash, ip_prefix_hash, user_agent_hash,
			metadata_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, _ = exec.ExecContext(ctx, query,
		ev.EventID,
		nullStringFromPtr(ev.UserID),
		nullStringFromPtr(ev.SessionID),
		ev.EventType,
		ev.Outcome,
		bytes32PtrSlice(ev.SubjectLookupHash),
		bytes32PtrSlice(ev.IPPrefixHash),
		bytes32PtrSlice(ev.UserAgentHash),
		metaJSON,
		ev.CreatedAt,
	)
}
