package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/provider"
	"tokendance/internal/store"
)

type Service struct {
	store   store.MediaStore
	cfg     *config.Config
	clk     clock.Clock
	storage provider.ObjectStorage
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock, storage provider.ObjectStorage) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	if storage == nil {
		storage = provider.NewMemoryObjectStorage("")
	}
	return &Service{
		store:   st.Media(),
		cfg:     cfg,
		clk:     clk,
		storage: storage,
	}
}

type CreateAvatarIntentInput struct {
	ContentType string `json:"contentType"`
	ByteSize    uint64 `json:"byteSize"`
	Sha256      string `json:"sha256"`
}

type CreateAvatarIntentResult struct {
	ObjectID  string `json:"objectId"`
	UploadURL string `json:"uploadUrl"`
	ExpiresAt string `json:"expiresAt"`
}

func (s *Service) CreateAvatarIntent(ctx context.Context, userID string, in CreateAvatarIntentInput) (*CreateAvatarIntentResult, error) {
	ct := strings.ToLower(strings.TrimSpace(in.ContentType))
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.invalidContentType", "only image/png, image/jpeg, image/webp allowed", nil, domain.ErrInvalidArgument)
	}

	if in.ByteSize == 0 || in.ByteSize > uint64(s.cfg.MediaAvatarMaxBytes) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.fileTooLarge", "file size must be between 1 byte and 5 MiB", nil, domain.ErrInvalidArgument)
	}

	shaTrimmed := strings.TrimSpace(in.Sha256)
	if len(shaTrimmed) != 64 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.invalidSha256", "sha256 must be a 64-character hex string", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	objIDToken, _ := crypto.GenerateOpaqueToken(13)
	objectID := "uob_" + objIDToken

	objectKey := fmt.Sprintf("users/%s/avatars/%s", userID, objectID)
	expiresAt := now.Add(10 * time.Minute)

	obj := domain.UserUploadObject{
		ObjectID:     objectID,
		UserID:       userID,
		ObjectType:   "avatar",
		ObjectKey:    objectKey,
		ContentType:  &ct,
		ByteSize:     &in.ByteSize,
		UploadStatus: domain.UploadStatusPending,
		ExpiresAt:    expiresAt,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	created, err := s.store.CreateAvatarUploadIntent(ctx, obj)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to create avatar upload intent", nil, err)
	}

	uploadURL, err := s.storage.PresignUploadURL(ctx, objectKey, 10*time.Minute)
	if err != nil {
		uploadURL = fmt.Sprintf("https://upload.tokendance.dev/%s?signedToken=%s", objectKey, objectID)
	}

	return &CreateAvatarIntentResult{
		ObjectID:  created.ObjectID,
		UploadURL: uploadURL,
		ExpiresAt: expiresAt.Format("2006-01-02T15:04:05.000Z"),
	}, nil
}

func (s *Service) CompleteAvatarIntent(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error) {
	now := s.clk.Now()

	obj, err := s.store.GetUploadObject(ctx, objectID, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "media.objectNotFound", "upload object not found", nil, err)
	}

	if obj.UploadStatus == domain.UploadStatusReady {
		return obj, nil
	}
	if obj.UploadStatus != domain.UploadStatusPending && obj.UploadStatus != domain.UploadStatusUploaded {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.invalidStatus", fmt.Sprintf("cannot complete upload in status %s", obj.UploadStatus), nil, domain.ErrInvalidArgument)
	}

	// 1. Object-storage HEAD
	meta, err := s.storage.HeadObject(ctx, obj.ObjectKey)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "media.objectNotUploaded", "object not found in storage", nil, err)
	}
	if meta.Size <= 0 || meta.Size > int64(s.cfg.MediaAvatarMaxBytes) {
		errCode := "INVALID_BYTE_SIZE"
		_ = s.store.UpdateUploadObjectStatus(ctx, objectID, domain.UploadStatusRejected, &errCode, now)
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.fileTooLarge", "file size must be between 1 byte and 5 MiB", nil, domain.ErrInvalidArgument)
	}

	// 2. Object-storage Read
	rc, err := s.storage.GetObject(ctx, obj.ObjectKey)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "media.storageReadFailed", "failed to read object from storage", nil, err)
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, int64(s.cfg.MediaAvatarMaxBytes)+1))
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "media.storageReadFailed", "failed to read object data", nil, err)
	}
	if len(data) == 0 || int64(len(data)) > int64(s.cfg.MediaAvatarMaxBytes) {
		errCode := "INVALID_BYTE_SIZE"
		_ = s.store.UpdateUploadObjectStatus(ctx, objectID, domain.UploadStatusRejected, &errCode, now)
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.fileTooLarge", "file size out of bounds", nil, domain.ErrInvalidArgument)
	}

	// 3. Magic bytes validation
	detectedContentType := detectImageMagicBytes(data)
	if detectedContentType == "" {
		errCode := "INVALID_MAGIC_BYTES"
		_ = s.store.UpdateUploadObjectStatus(ctx, objectID, domain.UploadStatusRejected, &errCode, now)
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.invalidMagicBytes", "uploaded payload magic bytes do not match PNG, JPEG, or WebP", nil, domain.ErrInvalidArgument)
	}

	// 4. Image decode & dimension & pixel validation
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		errCode := "IMAGE_DECODE_FAILED"
		_ = s.store.UpdateUploadObjectStatus(ctx, objectID, domain.UploadStatusRejected, &errCode, now)
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.corruptImage", "failed to decode image header", nil, domain.ErrInvalidArgument)
	}

	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 4096 || cfg.Height > 4096 {
		errCode := "INVALID_DIMENSIONS"
		_ = s.store.UpdateUploadObjectStatus(ctx, objectID, domain.UploadStatusRejected, &errCode, now)
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.invalidDimensions", "image dimensions invalid or exceed 4096x4096px limit", nil, domain.ErrInvalidArgument)
	}

	// Verify full pixel decode
	_, _, err = image.Decode(bytes.NewReader(data))
	if err != nil {
		errCode := "IMAGE_DECODE_FAILED"
		_ = s.store.UpdateUploadObjectStatus(ctx, objectID, domain.UploadStatusRejected, &errCode, now)
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.corruptImage", "corrupt image pixel data", nil, domain.ErrInvalidArgument)
	}

	// 5. Checksum & Metadata
	contentSha := crypto.SHA256(data)
	readyMeta := store.AvatarReadyMeta{
		ByteSize:      uint64(len(data)),
		ContentSha256: contentSha,
		ImageWidth:    uint32(cfg.Width),
		ImageHeight:   uint32(cfg.Height),
		ContentType:   detectedContentType,
	}

	// 6. Transactional owner-ready avatar switch
	completedObj, err := s.store.CompleteAvatarUploadIntent(ctx, objectID, userID, readyMeta, now)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "media.objectNotFound", "upload object not found", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to complete avatar switch", nil, err)
	}

	return completedObj, nil
}

func (s *Service) ClearAvatar(ctx context.Context, userID string) error {
	now := s.clk.Now()
	return s.store.ClearAvatar(ctx, userID, now)
}

func detectImageMagicBytes(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}
