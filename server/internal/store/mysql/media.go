package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
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

func (s *mediaStore) GetUploadObject(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error) {
	query := `
		SELECT object_id, user_id, object_type, object_key, content_type,
		       byte_size, content_sha256, image_width, image_height,
		       upload_status, expires_at, last_error_code, uploaded_at,
		       ready_at, deleted_at, created_at, updated_at
		FROM user_upload_objects
		WHERE object_id = ? AND user_id = ?
		LIMIT 1`

	var obj domain.UserUploadObject
	var cType, lastErr sql.NullString
	var byteSz sql.NullInt64
	var width, height sql.NullInt64
	var sha []byte
	var uploadedAt, readyAt, deletedAt sql.NullTime

	err := s.db.QueryRowContext(ctx, query, objectID, userID).Scan(
		&obj.ObjectID,
		&obj.UserID,
		&obj.ObjectType,
		&obj.ObjectKey,
		&cType,
		&byteSz,
		&sha,
		&width,
		&height,
		&obj.UploadStatus,
		&obj.ExpiresAt,
		&lastErr,
		&uploadedAt,
		&readyAt,
		&deletedAt,
		&obj.CreatedAt,
		&obj.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query upload object: %w", err)
	}

	obj.ContentType = ptrFromNullString(cType)
	obj.LastErrorCode = ptrFromNullString(lastErr)
	obj.ContentSha256 = scanBytes32Ptr(sha)
	obj.UploadedAt = ptrFromNullTime(uploadedAt)
	obj.ReadyAt = ptrFromNullTime(readyAt)
	obj.DeletedAt = ptrFromNullTime(deletedAt)
	if byteSz.Valid {
		sz := uint64(byteSz.Int64)
		obj.ByteSize = &sz
	}
	if width.Valid {
		w := uint32(width.Int64)
		obj.ImageWidth = &w
	}
	if height.Valid {
		h := uint32(height.Int64)
		obj.ImageHeight = &h
	}

	return &obj, nil
}

func (s *mediaStore) UpdateUploadObjectStatus(ctx context.Context, objectID string, status domain.UploadStatus, errorCode *string, now time.Time) error {
	query := `
		UPDATE user_upload_objects
		SET upload_status = ?, last_error_code = ?, updated_at = ?
		WHERE object_id = ?`

	res, err := s.db.ExecContext(ctx, query, string(status), nullStringFromPtr(errorCode), now, objectID)
	if err != nil {
		return fmt.Errorf("failed to update upload object status: %w", err)
	}
	aff, _ := res.RowsAffected()
	if aff == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *mediaStore) CompleteAvatarUploadIntent(ctx context.Context, objectID, userID string, meta store.AvatarReadyMeta, now time.Time) (*domain.UserUploadObject, error) {
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

	// Retire previously active avatar uploads for this user
	_, _ = tx.ExecContext(ctx, `
		UPDATE user_upload_objects
		SET upload_status = 'deleted_pending', deleted_at = ?, updated_at = ?
		WHERE user_id = ? AND object_type = 'avatar' AND object_id != ? AND upload_status = 'ready'`,
		now, now, userID, objectID)

	updateObjSQL := `
		UPDATE user_upload_objects
		SET upload_status = 'ready',
		    byte_size = ?,
		    content_sha256 = ?,
		    image_width = ?,
		    image_height = ?,
		    content_type = ?,
		    ready_at = ?,
		    updated_at = ?
		WHERE object_id = ?`
	if _, err := tx.ExecContext(ctx, updateObjSQL,
		meta.ByteSize,
		meta.ContentSha256[:],
		meta.ImageWidth,
		meta.ImageHeight,
		meta.ContentType,
		now,
		now,
		objectID,
	); err != nil {
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
	obj.ByteSize = &meta.ByteSize
	obj.ContentSha256 = &meta.ContentSha256
	obj.ImageWidth = &meta.ImageWidth
	obj.ImageHeight = &meta.ImageHeight
	obj.ContentType = &meta.ContentType
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
