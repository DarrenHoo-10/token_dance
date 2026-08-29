package media

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store store.MediaStore
	cfg   *config.Config
	clk   clock.Clock
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store: st.Media(),
		cfg:   cfg,
		clk:   clk,
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

	if in.ByteSize > uint64(s.cfg.MediaAvatarMaxBytes) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "media.fileTooLarge", "file exceeds 5 MiB max size", nil, domain.ErrInvalidArgument)
	}

	now := s.clk.Now()
	objIDToken, _ := crypto.GenerateOpaqueToken(13)
	objectID := "uob_" + objIDToken

	objectKey := fmt.Sprintf("users/%s/avatars/%s", userID, objectID)
	expiresAt := now.Add(10 * time.Minute)

	w := uint32(512)
	h := uint32(512)
	sha := crypto.SHA256([]byte(in.Sha256))

	obj := domain.UserUploadObject{
		ObjectID:      objectID,
		UserID:        userID,
		ObjectType:    "avatar",
		ObjectKey:     objectKey,
		ContentType:   &ct,
		ByteSize:      &in.ByteSize,
		ContentSha256: &sha,
		ImageWidth:    &w,
		ImageHeight:   &h,
		UploadStatus:  domain.UploadStatusPending,
		ExpiresAt:     expiresAt,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	created, err := s.store.CreateAvatarUploadIntent(ctx, obj)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to create avatar upload intent", nil, err)
	}

	uploadURL := fmt.Sprintf("https://upload.tokendance.dev/%s?signedToken=%s", objectKey, objectID)

	return &CreateAvatarIntentResult{
		ObjectID:  created.ObjectID,
		UploadURL: uploadURL,
		ExpiresAt: expiresAt.Format("2006-01-02T15:04:05.000Z"),
	}, nil
}

func (s *Service) CompleteAvatarIntent(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error) {
	now := s.clk.Now()
	obj, err := s.store.CompleteAvatarUploadIntent(ctx, objectID, userID, now)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "media.objectNotFound", "upload object not found", nil, err)
	}
	return obj, nil
}

func (s *Service) ClearAvatar(ctx context.Context, userID string) error {
	now := s.clk.Now()
	return s.store.ClearAvatar(ctx, userID, now)
}
