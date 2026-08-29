package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
)

type deviceStore struct {
	db *sql.DB
}

func (s *deviceStore) ListInstallations(ctx context.Context, userID string) ([]domain.Installation, error) {
	query := `
		SELECT installation_id, user_id, device_public_key, device_name,
		       os_type, os_version, architecture, collector_version,
		       installation_status, disabled_at, disabled_reason,
		       status_version, registered_at, last_seen_at, revoked_at, updated_at
		FROM installations
		WHERE user_id = ?
		ORDER BY registered_at DESC`

	rows, err := s.db.QueryContext(ctx, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list installations: %w", err)
	}
	defer rows.Close()

	var list []domain.Installation
	for rows.Next() {
		var inst domain.Installation
		var pubKey []byte
		var devName, osVer, disReason sql.NullString
		var disAt, lastSeen, revokedAt sql.NullTime

		if err := rows.Scan(
			&inst.InstallationID,
			&inst.UserID,
			&pubKey,
			&devName,
			&inst.OSType,
			&osVer,
			&inst.Architecture,
			&inst.CollectorVersion,
			&inst.InstallationStatus,
			&disAt,
			&disReason,
			&inst.StatusVersion,
			&inst.RegisteredAt,
			&lastSeen,
			&revokedAt,
			&inst.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan installation: %w", err)
		}

		inst.DevicePublicKey = scanBytes32(pubKey)
		inst.DeviceName = ptrFromNullString(devName)
		inst.OSVersion = ptrFromNullString(osVer)
		inst.DisabledReason = ptrFromNullString(disReason)
		inst.DisabledAt = ptrFromNullTime(disAt)
		inst.LastSeenAt = ptrFromNullTime(lastSeen)
		inst.RevokedAt = ptrFromNullTime(revokedAt)

		list = append(list, inst)
	}

	return list, nil
}

func (s *deviceStore) GetInstallation(ctx context.Context, installationID string, userID string) (*domain.Installation, error) {
	query := `
		SELECT installation_id, user_id, device_public_key, device_name,
		       os_type, os_version, architecture, collector_version,
		       installation_status, disabled_at, disabled_reason,
		       status_version, registered_at, last_seen_at, revoked_at, updated_at
		FROM installations
		WHERE installation_id = ? AND user_id = ?
		LIMIT 1`

	var inst domain.Installation
	var pubKey []byte
	var devName, osVer, disReason sql.NullString
	var disAt, lastSeen, revokedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, installationID, userID).Scan(
		&inst.InstallationID,
		&inst.UserID,
		&pubKey,
		&devName,
		&inst.OSType,
		&osVer,
		&inst.Architecture,
		&inst.CollectorVersion,
		&inst.InstallationStatus,
		&disAt,
		&disReason,
		&inst.StatusVersion,
		&inst.RegisteredAt,
		&lastSeen,
		&revokedAt,
		&inst.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get installation: %w", err)
	}

	inst.DevicePublicKey = scanBytes32(pubKey)
	inst.DeviceName = ptrFromNullString(devName)
	inst.OSVersion = ptrFromNullString(osVer)
	inst.DisabledReason = ptrFromNullString(disReason)
	inst.DisabledAt = ptrFromNullTime(disAt)
	inst.LastSeenAt = ptrFromNullTime(lastSeen)
	inst.RevokedAt = ptrFromNullTime(revokedAt)

	return &inst, nil
}

func (s *deviceStore) CreateBindingChallenge(ctx context.Context, challenge domain.DeviceBindingChallenge) (*domain.DeviceBindingChallenge, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin create binding challenge tx: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC()

	// Cancel any active binding challenge for this session
	cancelSQL := `
		UPDATE device_binding_challenges
		SET challenge_status = 'cancelled', active_session_key = NULL, updated_at = ?
		WHERE session_id = ? AND challenge_status = 'pending'`
	if _, err := tx.ExecContext(ctx, cancelSQL, now, challenge.SessionID); err != nil {
		return nil, fmt.Errorf("failed to cancel existing binding challenge: %w", err)
	}

	insertSQL := `
		INSERT INTO device_binding_challenges (
			challenge_id, user_id, session_id, code_lookup_hash, code_key_version,
			challenge_status, expires_at, active_session_key, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertSQL,
		challenge.ChallengeID,
		challenge.UserID,
		challenge.SessionID,
		bytes32Slice(challenge.CodeLookupHash),
		challenge.CodeKeyVersion,
		challenge.ChallengeStatus,
		challenge.ExpiresAt,
		challenge.ActiveSessionKey,
		challenge.CreatedAt,
		challenge.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to insert binding challenge: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit binding challenge: %w", err)
	}

	cCopy := challenge
	return &cCopy, nil
}

func (s *deviceStore) CancelBindingChallenge(ctx context.Context, challengeID, userID string) error {
	now := time.Now().UTC()
	query := `
		UPDATE device_binding_challenges
		SET challenge_status = 'cancelled', active_session_key = NULL, updated_at = ?
		WHERE challenge_id = ? AND user_id = ?`

	res, err := s.db.ExecContext(ctx, query, now, challengeID, userID)
	if err != nil {
		return fmt.Errorf("failed to cancel binding challenge: %w", err)
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

func (s *deviceStore) ClaimInstallationTx(ctx context.Context, codeHash [32]byte, inst domain.Installation, now time.Time) (*domain.Installation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin claim tx: %w", err)
	}
	defer tx.Rollback()

	// Query challenge FOR UPDATE
	queryChallenge := `
		SELECT challenge_id, user_id, session_id, challenge_status, expires_at
		FROM device_binding_challenges
		WHERE code_lookup_hash = ? AND challenge_status = 'pending'
		FOR UPDATE`

	var challengeID, userID, sessionID string
	var chStatus domain.ChallengeStatus
	var expiresAt time.Time

	err = tx.QueryRowContext(ctx, queryChallenge, bytes32Slice(codeHash)).Scan(&challengeID, &userID, &sessionID, &chStatus, &expiresAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrChallengeInvalid
		}
		return nil, fmt.Errorf("failed to lock challenge: %w", err)
	}

	if now.After(expiresAt) {
		_, _ = tx.ExecContext(ctx, "UPDATE device_binding_challenges SET challenge_status = 'expired', active_session_key = NULL, updated_at = ? WHERE challenge_id = ?", now, challengeID)
		_ = tx.Commit()
		return nil, domain.ErrChallengeExpired
	}

	// Verify user active
	var accountStatus string
	err = tx.QueryRowContext(ctx, "SELECT account_status FROM users WHERE user_id = ? FOR UPDATE", userID).Scan(&accountStatus)
	if err != nil || accountStatus != string(domain.AccountStatusActive) {
		return nil, domain.ErrForbidden
	}

	// Check device public key conflict
	queryInst := `
		SELECT installation_id, user_id, device_public_key, device_name,
		       os_type, os_version, architecture, collector_version,
		       installation_status, disabled_at, disabled_reason,
		       status_version, registered_at, last_seen_at, revoked_at, updated_at
		FROM installations
		WHERE device_public_key = ?
		FOR UPDATE`

	var existing domain.Installation
	var pubKey []byte
	var devName, osVer, disReason sql.NullString
	var disAt, lastSeen, revokedAt sql.NullTime

	err = tx.QueryRowContext(ctx, queryInst, bytes32Slice(inst.DevicePublicKey)).Scan(
		&existing.InstallationID,
		&existing.UserID,
		&pubKey,
		&devName,
		&existing.OSType,
		&osVer,
		&existing.Architecture,
		&existing.CollectorVersion,
		&existing.InstallationStatus,
		&disAt,
		&disReason,
		&existing.StatusVersion,
		&existing.RegisteredAt,
		&lastSeen,
		&revokedAt,
		&existing.UpdatedAt,
	)

	if err == nil {
		if existing.UserID == userID && existing.InstallationStatus == domain.InstallationStatusActive {
			// Idempotent return
			consumeSQL := `
				UPDATE device_binding_challenges
				SET challenge_status = 'consumed', consumed_installation_id = ?, consumed_at = ?,
				    active_session_key = NULL, updated_at = ?
				WHERE challenge_id = ?`
			_, _ = tx.ExecContext(ctx, consumeSQL, existing.InstallationID, now, now, challengeID)
			if err := tx.Commit(); err != nil {
				return nil, err
			}

			existing.DevicePublicKey = scanBytes32(pubKey)
			existing.DeviceName = ptrFromNullString(devName)
			existing.OSVersion = ptrFromNullString(osVer)
			existing.DisabledReason = ptrFromNullString(disReason)
			existing.DisabledAt = ptrFromNullTime(disAt)
			existing.LastSeenAt = ptrFromNullTime(lastSeen)
			existing.RevokedAt = ptrFromNullTime(revokedAt)

			return &existing, nil
		}
		return nil, domain.ErrPublicKeyConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query existing installation: %w", err)
	}

	// Insert installation
	insertInstSQL := `
		INSERT INTO installations (
			installation_id, user_id, device_public_key, device_name,
			os_type, os_version, architecture, collector_version,
			installation_status, status_version, registered_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertInstSQL,
		inst.InstallationID,
		userID,
		bytes32Slice(inst.DevicePublicKey),
		nullStringFromPtr(inst.DeviceName),
		inst.OSType,
		nullStringFromPtr(inst.OSVersion),
		inst.Architecture,
		inst.CollectorVersion,
		now,
		now,
	); err != nil {
		return nil, fmt.Errorf("failed to insert installation: %w", err)
	}

	// Consume challenge
	consumeSQL := `
		UPDATE device_binding_challenges
		SET challenge_status = 'consumed', consumed_installation_id = ?, consumed_at = ?,
		    active_session_key = NULL, updated_at = ?
		WHERE challenge_id = ?`
	if _, err := tx.ExecContext(ctx, consumeSQL, inst.InstallationID, now, now, challengeID); err != nil {
		return nil, fmt.Errorf("failed to mark binding challenge consumed: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit claim tx: %w", err)
	}

	instCopy := inst
	instCopy.UserID = userID
	instCopy.InstallationStatus = domain.InstallationStatusActive
	instCopy.StatusVersion = 1
	instCopy.RegisteredAt = now
	instCopy.UpdatedAt = now

	return &instCopy, nil
}

func (s *deviceStore) RegisterInstallationTx(ctx context.Context, inst domain.Installation, now time.Time) (*domain.Installation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin register installation tx: %w", err)
	}
	defer tx.Rollback()

	var accountStatus string
	err = tx.QueryRowContext(ctx, "SELECT account_status FROM users WHERE user_id = ? FOR UPDATE", inst.UserID).Scan(&accountStatus)
	if err != nil || accountStatus != string(domain.AccountStatusActive) {
		return nil, domain.ErrForbidden
	}

	var existingUserID string
	var existingStatus string
	err = tx.QueryRowContext(ctx, "SELECT user_id, installation_status FROM installations WHERE device_public_key = ? FOR UPDATE", bytes32Slice(inst.DevicePublicKey)).Scan(&existingUserID, &existingStatus)
	if err == nil {
		if existingUserID == inst.UserID && existingStatus == string(domain.InstallationStatusActive) {
			instCopy := inst
			return &instCopy, nil
		}
		return nil, domain.ErrPublicKeyConflict
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	insertInstSQL := `
		INSERT INTO installations (
			installation_id, user_id, device_public_key, device_name,
			os_type, os_version, architecture, collector_version,
			installation_status, status_version, registered_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'active', 1, ?, ?)`

	if _, err := tx.ExecContext(ctx, insertInstSQL,
		inst.InstallationID,
		inst.UserID,
		bytes32Slice(inst.DevicePublicKey),
		nullStringFromPtr(inst.DeviceName),
		inst.OSType,
		nullStringFromPtr(inst.OSVersion),
		inst.Architecture,
		inst.CollectorVersion,
		now,
		now,
	); err != nil {
		return nil, fmt.Errorf("failed to insert installation: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	instCopy := inst
	instCopy.InstallationStatus = domain.InstallationStatusActive
	instCopy.StatusVersion = 1
	instCopy.RegisteredAt = now
	instCopy.UpdatedAt = now

	return &instCopy, nil
}

func (s *deviceStore) UpdateInstallationName(ctx context.Context, installationID, userID string, name string, now time.Time) (*domain.Installation, error) {
	query := `
		UPDATE installations
		SET device_name = ?, updated_at = ?
		WHERE installation_id = ? AND user_id = ?`

	res, err := s.db.ExecContext(ctx, query, name, now, installationID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to update installation name: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, domain.ErrNotFound
	}

	return s.GetInstallation(ctx, installationID, userID)
}

func (s *deviceStore) PauseInstallation(ctx context.Context, installationID, userID string, reason string, now time.Time) (*domain.Installation, error) {
	inst, err := s.GetInstallation(ctx, installationID, userID)
	if err != nil {
		return nil, err
	}

	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusDisabled {
		return inst, nil
	}

	query := `
		UPDATE installations
		SET installation_status = 'disabled', disabled_at = ?, disabled_reason = ?,
		    status_version = status_version + 1, updated_at = ?
		WHERE installation_id = ? AND user_id = ?`

	if _, err := s.db.ExecContext(ctx, query, now, reason, now, installationID, userID); err != nil {
		return nil, fmt.Errorf("failed to pause installation: %w", err)
	}

	return s.GetInstallation(ctx, installationID, userID)
}

func (s *deviceStore) ResumeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error) {
	inst, err := s.GetInstallation(ctx, installationID, userID)
	if err != nil {
		return nil, err
	}

	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusActive {
		return inst, nil
	}
	if inst.DisabledReason != nil && *inst.DisabledReason != "user_paused" {
		return nil, domain.ErrForbidden
	}

	query := `
		UPDATE installations
		SET installation_status = 'active', disabled_at = NULL, disabled_reason = NULL,
		    status_version = status_version + 1, updated_at = ?
		WHERE installation_id = ? AND user_id = ?`

	if _, err := s.db.ExecContext(ctx, query, now, installationID, userID); err != nil {
		return nil, fmt.Errorf("failed to resume installation: %w", err)
	}

	return s.GetInstallation(ctx, installationID, userID)
}

func (s *deviceStore) RevokeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error) {
	inst, err := s.GetInstallation(ctx, installationID, userID)
	if err != nil {
		return nil, err
	}

	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return inst, nil
	}

	query := `
		UPDATE installations
		SET installation_status = 'revoked', revoked_at = ?,
		    status_version = status_version + 1, updated_at = ?
		WHERE installation_id = ? AND user_id = ?`

	if _, err := s.db.ExecContext(ctx, query, now, now, installationID, userID); err != nil {
		return nil, fmt.Errorf("failed to revoke installation: %w", err)
	}

	return s.GetInstallation(ctx, installationID, userID)
}

func (s *deviceStore) AuthorizeIngest(ctx context.Context, installationID string) (*domain.Installation, *domain.User, error) {
	query := `
		SELECT i.installation_id, i.user_id, i.device_public_key, i.device_name,
		       i.os_type, i.os_version, i.architecture, i.collector_version,
		       i.installation_status, i.disabled_at, i.disabled_reason,
		       i.status_version, i.registered_at, i.last_seen_at, i.revoked_at, i.updated_at,
		       u.auth_subject_hash, u.email_lookup_hash, u.email_ciphertext,
		       u.handle, u.email_verified_at, u.display_name, u.avatar_url, u.avatar_object_id,
		       u.bio, u.account_status, u.leaderboard_visibility, u.timezone_name, u.locale,
		       u.onboarding_completed_at, u.profile_version, u.public_profile_updated_at,
		       u.created_at, u.updated_at, u.deleted_at
		FROM installations i
		JOIN users u ON i.user_id = u.user_id
		WHERE i.installation_id = ?
		LIMIT 1`

	var inst domain.Installation
	var u domain.User
	var pubKey, authSubHash, emailHash []byte
	var devName, osVer, disReason sql.NullString
	var disAt, lastSeen, revokedAt sql.NullTime
	var handle, avatarURL, avatarObjID, bio sql.NullString
	var emailVerifiedAt, onboardingCompletedAt, publicProfileUpdatedAt, deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, installationID).Scan(
		&inst.InstallationID,
		&inst.UserID,
		&pubKey,
		&devName,
		&inst.OSType,
		&osVer,
		&inst.Architecture,
		&inst.CollectorVersion,
		&inst.InstallationStatus,
		&disAt,
		&disReason,
		&inst.StatusVersion,
		&inst.RegisteredAt,
		&lastSeen,
		&revokedAt,
		&inst.UpdatedAt,
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
			return nil, nil, domain.ErrNotFound
		}
		return nil, nil, fmt.Errorf("failed to authorize ingest: %w", err)
	}

	if inst.InstallationStatus == domain.InstallationStatusRevoked {
		return nil, nil, domain.ErrDeviceRevoked
	}
	if inst.InstallationStatus == domain.InstallationStatusDisabled {
		return nil, nil, domain.ErrDeviceDisabled
	}
	if u.AccountStatus != domain.AccountStatusActive {
		return nil, nil, domain.ErrAccountSuspended
	}

	inst.DevicePublicKey = scanBytes32(pubKey)
	inst.DeviceName = ptrFromNullString(devName)
	inst.OSVersion = ptrFromNullString(osVer)
	inst.DisabledReason = ptrFromNullString(disReason)
	inst.DisabledAt = ptrFromNullTime(disAt)
	inst.LastSeenAt = ptrFromNullTime(lastSeen)
	inst.RevokedAt = ptrFromNullTime(revokedAt)

	u.UserID = inst.UserID
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

	return &inst, &u, nil
}
