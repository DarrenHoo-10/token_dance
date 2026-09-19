package media

import (
	"bytes"
	"context"
	"errors"
	"image"
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
	lock := s.avatars.loadLock(obj.ObjectKey)
	lock.Lock()
	defer lock.Unlock()
	if cached, ok := s.avatars.get(obj.ObjectKey); ok {
		return cached.data, cached.contentType, nil
	}
	// New uploads already have a persistent thumbnail. Older uploads are
	// converted on first read, so existing users do not need to upload again.
	if rc, err := s.storage.OpenObject(ctx, avatarThumbnailKey(obj.ObjectKey)); err == nil {
		data, readErr := io.ReadAll(io.LimitReader(rc, int64(s.cfg.MediaAvatarMaxBytes)+1))
		_ = rc.Close()
		cfg, _, decodeErr := image.DecodeConfig(bytes.NewReader(data))
		ct := detectImageMagicBytes(data)
		if readErr == nil && decodeErr == nil && ct != "" && int64(len(data)) <= s.cfg.MediaAvatarMaxBytes && cfg.Width > 0 && cfg.Height > 0 && cfg.Width <= avatarEdge && cfg.Height <= avatarEdge {
			s.avatars.put(avatarImage{key: obj.ObjectKey, data: data, contentType: ct})
			return data, ct, nil
		}
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
	data, contentType, err := thumbnail(data, s.cfg.MediaAvatarMaxPixels)
	if err != nil {
		return nil, "", domain.ErrInternal
	}
	s.avatars.put(avatarImage{key: obj.ObjectKey, data: data, contentType: contentType})
	// Old uploads are compressed in memory. Do not create persistent objects on
	// a read path that might race account deletion.
	return data, contentType, nil
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
