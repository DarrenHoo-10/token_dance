package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
)

type mediaStore struct {
	db *sql.DB
}

func (s *mediaStore) CreateAvatarUploadIntent(ctx context.Context, obj domain.UserUploadObject) (*domain.UserUploadObject, error) {
	insertSQL := `
		INSERT INTO user_upload_objects (
			object_id, user_id, object_type, object_key, content_type,
			byte_size, upload_status, expires_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, insertSQL,
		obj.ObjectID,
		obj.UserID,
		obj.ObjectType,
		obj.ObjectKey,
		nullStringFromPtr(obj.ContentType),
		obj.ByteSize,
		obj.UploadStatus,
		obj.ExpiresAt,
		obj.CreatedAt,
		obj.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert upload object: %w", err)
	}

	oCopy := obj
	return &oCopy, nil
}

func (s *mediaStore) CompleteAvatarUploadIntent(ctx context.Context, objectID, userID string, now time.Time) (*domain.UserUploadObject, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin complete avatar tx: %w", err)
	}
	defer tx.Rollback()

	var obj domain.UserUploadObject
	var objKey string
	err = tx.QueryRowContext(ctx, `
		SELECT object_id, user_id, object_type, object_key, upload_status
		FROM user_upload_objects
		WHERE object_id = ? AND user_id = ?
		FOR UPDATE`, objectID, userID).Scan(&obj.ObjectID, &obj.UserID, &obj.ObjectType, &objKey, &obj.UploadStatus)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to lock upload object: %w", err)
	}

	updateObjSQL := `
		UPDATE user_upload_objects
		SET upload_status = 'ready', ready_at = ?, updated_at = ?
		WHERE object_id = ?`
	if _, err := tx.ExecContext(ctx, updateObjSQL, now, now, objectID); err != nil {
		return nil, fmt.Errorf("failed to mark upload object ready: %w", err)
	}

	avatarURL := fmt.Sprintf("https://cdn.tokendance.dev/%s", objKey)
	updateUserSQL := `
		UPDATE users
		SET avatar_object_id = ?, avatar_url = ?, profile_version = profile_version + 1, updated_at = ?
		WHERE user_id = ?`
	if _, err := tx.ExecContext(ctx, updateUserSQL, objectID, avatarURL, now, userID); err != nil {
		return nil, fmt.Errorf("failed to update user avatar: %w", err)
	}

	updatePubSQL := `
		UPDATE public_user_profiles
		SET avatar_url = ?, source_profile_version = source_profile_version + 1,
		    projection_version = projection_version + 1, updated_at = ?
		WHERE user_id = ?`
	_, _ = tx.ExecContext(ctx, updatePubSQL, avatarURL, now, userID)

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit complete avatar tx: %w", err)
	}

	obj.UploadStatus = domain.UploadStatusReady
	obj.ReadyAt = &now
	obj.ObjectKey = objKey
	obj.UpdatedAt = now
	return &obj, nil
}

func (s *mediaStore) ClearAvatar(ctx context.Context, userID string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin clear avatar tx: %w", err)
	}
	defer tx.Rollback()

	updateUserSQL := `
		UPDATE users
		SET avatar_object_id = NULL, avatar_url = NULL,
		    profile_version = profile_version + 1, updated_at = ?
		WHERE user_id = ?`
	if _, err := tx.ExecContext(ctx, updateUserSQL, now, userID); err != nil {
		return fmt.Errorf("failed to clear user avatar: %w", err)
	}

	updatePubSQL := `
		UPDATE public_user_profiles
		SET avatar_url = NULL, source_profile_version = source_profile_version + 1,
		    projection_version = projection_version + 1, updated_at = ?
		WHERE user_id = ?`
	_, _ = tx.ExecContext(ctx, updatePubSQL, now, userID)

	return tx.Commit()
}
