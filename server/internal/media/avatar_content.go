package media

import (
	"bytes"
	"context"
	"errors"
	"io"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

// Relay through the authenticated application origin rather than requiring
// browser CORS access to the private object-storage bucket.
func (s *Service) UploadAvatarContent(ctx context.Context, objectID, userID string, body io.Reader) error {
	obj, err := s.store.GetUploadObject(ctx, objectID, userID)
	if err != nil {
		return avatarContentError(err)
	}
	if obj.ObjectType != "avatar" || (obj.UploadStatus != domain.UploadStatusPending && obj.UploadStatus != domain.UploadStatusUploaded) || !s.clk.Now().Before(obj.ExpiresAt) {
		return avatarContentError(domain.ErrInvalidArgument)
	}
	data, err := io.ReadAll(io.LimitReader(body, int64(s.cfg.MediaAvatarMaxBytes)+1))
	if err != nil {
		return avatarContentError(domain.ErrInvalidArgument)
	}
	if len(data) == 0 || int64(len(data)) > s.cfg.MediaAvatarMaxBytes || obj.ByteSize == nil || uint64(len(data)) != *obj.ByteSize || obj.ContentType == nil {
		return avatarContentError(domain.ErrInvalidArgument)
	}
	hash := crypto.SHA256(data)
	if obj.ContentSha256 == nil || !crypto.ConstantTimeCompare(hash[:], obj.ContentSha256[:]) {
		return avatarContentError(domain.ErrInvalidArgument)
	}
	return s.storage.PutObject(ctx, obj.ObjectKey, bytes.NewReader(data), int64(len(data)), *obj.ContentType)
}

// Active accounts participate in the leaderboard regardless of profile privacy.
// Only their current, validated avatar is public; personal statistics remain gated.
func (s *Service) ReadAvatar(ctx context.Context, objectID, viewerID string) ([]byte, string, error) {
	obj, err := s.store.GetVisibleAvatar(ctx, objectID, viewerID)
	if err != nil {
		return nil, "", avatarContentError(err)
	}
	if obj.ContentType == nil {
		return nil, "", avatarContentError(domain.ErrNotFound)
	}
	reader, err := s.storage.OpenObject(ctx, obj.ObjectKey)
	if err != nil {
		return nil, "", avatarContentError(domain.ErrNotFound)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(s.cfg.MediaAvatarMaxBytes)+1))
	if err != nil || int64(len(data)) > s.cfg.MediaAvatarMaxBytes {
		return nil, "", domain.ErrInternal
	}
	return data, *obj.ContentType, nil
}

func avatarContentError(err error) error {
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NewAppError(404, "RESOURCE_NOT_FOUND", "media.objectNotFound", "avatar not found", nil, err)
	}
	if errors.Is(err, domain.ErrInvalidArgument) {
		return domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.invalidContent", "invalid avatar content or upload intent", nil, err)
	}
	return err
}
