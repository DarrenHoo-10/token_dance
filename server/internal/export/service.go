package export

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/provider"
	"tokendance/internal/store"
)

type Service struct {
	store           store.ExportStore
	clk             clock.Clock
	storage         provider.ObjectStorage
	idempotencyKeys config.VersionedKeyring
}

func NewService(st store.Store, clk clock.Clock, storage provider.ObjectStorage) *Service {
	return NewServiceWithConfig(st, config.DefaultConfig(), clk, storage)
}

func NewServiceWithConfig(st store.Store, cfg *config.Config, clk clock.Clock, storage provider.ObjectStorage) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	if storage == nil {
		storage = provider.NewMemoryObjectStorage("")
	}
	return &Service{
		store:           st.Export(),
		clk:             clk,
		storage:         storage,
		idempotencyKeys: cfg.IdempotencyKeys,
	}
}

type CreateExportInput struct {
	IdempotencyKey string                 `json:"idempotencyKey"`
	Scope          string                 `json:"scope"`
	Format         string                 `json:"format"`
	Filter         map[string]interface{} `json:"filter,omitempty"`
}

func (s *Service) CreateJob(ctx context.Context, userID string, in CreateExportInput) (*domain.DataExportJob, error) {
	if len(in.IdempotencyKey) < 1 || len(in.IdempotencyKey) > 64 {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "export.idempotencyRequired", "Idempotency-Key header must be 1-64 characters", nil, domain.ErrInvalidArgument)
	}
	for _, ch := range in.IdempotencyKey {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-_.:", ch)) {
			return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "export.idempotencyInvalid", "Idempotency-Key contains invalid characters", nil, domain.ErrInvalidArgument)
		}
	}
	idempotencyHash := crypto.HMACSHA256(s.idempotencyKeys.Current(), []byte(in.IdempotencyKey))
	hashedIdempotencyKey := fmt.Sprintf("v%d:%s", s.idempotencyKeys.CurrentVersion, base64.RawURLEncoding.EncodeToString(idempotencyHash[:]))

	if in.Scope != "summary" && in.Scope != "activity" && in.Scope != "all_aggregates" {
		in.Scope = "summary"
	}
	if in.Format != "csv" && in.Format != "json" {
		in.Format = "csv"
	}

	filterBytes, _ := json.Marshal(in.Filter)
	reqHashPayload := fmt.Sprintf("%s:%s:%s", in.Scope, in.Format, string(filterBytes))
	reqHash := sha256.Sum256([]byte(reqHashPayload))

	now := s.clk.Now()
	exportIDToken, _ := crypto.GenerateOpaqueToken(13)
	exportID := "exp_" + exportIDToken

	job := domain.DataExportJob{
		ExportID:       exportID,
		UserID:         userID,
		IdempotencyKey: hashedIdempotencyKey,
		RequestHash:    reqHash,
		ExportScope:    in.Scope,
		ExportFormat:   in.Format,
		FilterJSON:     in.Filter,
		JobStatus:      domain.ExportJobStatusPending,
		AttemptCount:   0,
		NextAttemptAt:  now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	created, err := s.store.CreateJob(ctx, job)
	if err != nil {
		if errors.Is(err, domain.ErrIdempotencyReused) {
			return nil, domain.NewAppError(409, "IDEMPOTENCY_KEY_REUSED", "export.idempotencyReused", "idempotency key reused with different request payload", nil, err)
		}
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to create export job", nil, err)
	}

	return created, nil
}

func (s *Service) ListJobs(ctx context.Context, userID string) ([]domain.DataExportJob, error) {
	return s.store.ListJobs(ctx, userID)
}

func (s *Service) GetJob(ctx context.Context, exportID, userID string) (*domain.DataExportJob, error) {
	job, err := s.store.GetJob(ctx, exportID, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "export.notFound", "export job not found", nil, err)
	}
	return job, nil
}

type DownloadResponse struct {
	DownloadURL string `json:"downloadUrl"`
	ExpiresAt   string `json:"expiresAt"`
}

func (s *Service) GetDownloadURL(ctx context.Context, exportID, userID string) (*DownloadResponse, error) {
	job, err := s.store.GetJob(ctx, exportID, userID)
	if err != nil {
		return nil, domain.NewAppError(404, "RESOURCE_NOT_FOUND", "export.notFound", "export job not found", nil, err)
	}

	if job.JobStatus != domain.ExportJobStatusCompleted {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "export.notReady", "export is not completed yet", nil, nil)
	}

	now := s.clk.Now()
	if job.ExpiresAt != nil && now.After(*job.ExpiresAt) {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "export.expired", "export has expired", nil, nil)
	}

	if job.ObjectKey == nil || *job.ObjectKey == "" {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "export.missingKey", "export object key missing", nil, nil)
	}

	signedURL, err := s.storage.PresignDownloadURL(ctx, *job.ObjectKey, 60*time.Second)
	if err != nil {
		return nil, domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "failed to generate download URL", nil, err)
	}

	expiresAt := now.Add(60 * time.Second).Format("2006-01-02T15:04:05.000Z")

	return &DownloadResponse{
		DownloadURL: signedURL,
		ExpiresAt:   expiresAt,
	}, nil
}
