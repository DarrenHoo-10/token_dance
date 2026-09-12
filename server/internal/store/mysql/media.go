package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/domain"
	"tokendance/internal/store"
	"tokendance/internal/store/sqlcgen"
)

type mediaStore struct {
	db *sql.DB
}

func (s *mediaStore) GetVisibleAvatar(ctx context.Context, objectID, _ string) (*domain.UserUploadObject, error) {
	var obj domain.UserUploadObject
	var contentType string
	err := s.db.QueryRowContext(ctx, `SELECT o.object_id, o.object_key, o.content_type
	FROM user_upload_objects o JOIN users u ON u.avatar_object_id=o.object_id AND u.user_id=o.user_id
	WHERE o.object_id=? AND o.object_type='avatar' AND o.upload_status='ready'
	AND u.account_status='active'`, objectID).Scan(&obj.ObjectID, &obj.ObjectKey, &contentType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	obj.ContentType = &contentType
	return &obj, nil
}

func (s *mediaStore) CreateAvatarUploadIntent(ctx context.Context, obj domain.UserUploadObject) (*domain.UserUploadObject, error) {
	var byteSize sql.NullInt64
	if obj.ByteSize != nil {
		byteSize = sql.NullInt64{Int64: int64(*obj.ByteSize), Valid: true}
	}
	var contentSHA256 sql.NullString
	if obj.ContentSha256 != nil {
		contentSHA256 = sql.NullString{String: string(obj.ContentSha256[:]), Valid: true}
	}
	if err := sqlcgen.New(s.db).CreateUploadObject(ctx, sqlcgen.CreateUploadObjectParams{
		ObjectID:      obj.ObjectID,
		UserID:        obj.UserID,
		ObjectType:    string(obj.ObjectType),
		ObjectKey:     obj.ObjectKey,
		ContentType:   nullStringFromPtr(obj.ContentType),
		ByteSize:      byteSize,
		ContentSha256: contentSHA256,
		UploadStatus:  string(obj.UploadStatus),
		ExpiresAt:     obj.ExpiresAt,
		CreatedAt:     obj.CreatedAt,
		UpdatedAt:     obj.UpdatedAt,
	}); err != nil {
		return nil, fmt.Errorf("failed to insert upload object: %w", err)
	}
	oCopy := obj
	return &oCopy, nil
}

func (s *mediaStore) GetUploadObject(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error) {
	row, err := sqlcgen.New(s.db).GetUploadObjectByOwner(ctx, sqlcgen.GetUploadObjectByOwnerParams{
		ObjectID: objectID,
		UserID:   userID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to query upload object: %w", err)
	}
	obj := &domain.UserUploadObject{
		ObjectID:      row.ObjectID,
		UserID:        row.UserID,
		ObjectType:    row.ObjectType,
		ObjectKey:     row.ObjectKey,
		ContentType:   ptrFromNullString(row.ContentType),
		UploadStatus:  domain.UploadStatus(row.UploadStatus),
		ExpiresAt:     row.ExpiresAt,
		LastErrorCode: ptrFromNullString(row.LastErrorCode),
		UploadedAt:    ptrFromNullTime(row.UploadedAt),
		ReadyAt:       ptrFromNullTime(row.ReadyAt),
		DeletedAt:     ptrFromNullTime(row.DeletedAt),
		CreatedAt:     row.CreatedAt,
		UpdatedAt:     row.UpdatedAt,
	}
	if row.ContentSha256.Valid {
		obj.ContentSha256 = scanBytes32Ptr([]byte(row.ContentSha256.String))
	}
	if row.ByteSize.Valid {
		sz := uint64(row.ByteSize.Int64)
		obj.ByteSize = &sz
	}
	if row.ImageWidth.Valid {
		width := uint32(row.ImageWidth.Int32)
		obj.ImageWidth = &width
	}
	if row.ImageHeight.Valid {
		height := uint32(row.ImageHeight.Int32)
		obj.ImageHeight = &height
	}
	return obj, nil
}

func (s *mediaStore) UpdateUploadObjectStatus(ctx context.Context, objectID string, status domain.UploadStatus, errorCode *string, now time.Time) error {
	rows, err := sqlcgen.New(s.db).UpdateUploadObjectStatus(ctx, sqlcgen.UpdateUploadObjectStatusParams{
		UploadStatus:  string(status),
		LastErrorCode: nullStringFromPtr(errorCode),
		UpdatedAt:     now,
		ObjectID:      objectID,
	})
	if err != nil {
		return fmt.Errorf("failed to update upload object status: %w", err)
	}
	if rows == 0 {
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

	avatarURL := "/api/v1/public/avatars/" + objectID
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
