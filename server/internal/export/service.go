package export

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"tokendance/internal/clock"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type Service struct {
	store store.ExportStore
	clk   clock.Clock
}

func NewService(st store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	return &Service{
		store: st.Export(),
		clk:   clk,
	}
}

type CreateExportInput struct {
	IdempotencyKey string                 `json:"idempotencyKey"`
	Scope          string                 `json:"scope"`
	Format         string                 `json:"format"`
	Filter         map[string]interface{} `json:"filter,omitempty"`
}

func (s *Service) CreateJob(ctx context.Context, userID string, in CreateExportInput) (*domain.DataExportJob, error) {
	if in.IdempotencyKey == "" {
		return nil, domain.NewAppError(400, "API_INVALID_ARGUMENT", "export.idempotencyRequired", "Idempotency-Key header is required", nil, domain.ErrInvalidArgument)
	}

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

	objKey := fmt.Sprintf("exports/%s/%s.%s", userID, exportID, in.Format)
	fileSha := sha256.Sum256([]byte("dummy-export-data"))
	fileSize := uint64(1024)
	completedAt := now
	expiresAt := now.Add(24 * time.Hour)

	job := domain.DataExportJob{
		ExportID:       exportID,
		UserID:         userID,
		IdempotencyKey: in.IdempotencyKey,
		RequestHash:    reqHash,
		ExportScope:    in.Scope,
		ExportFormat:   in.Format,
		FilterJSON:     in.Filter,
		JobStatus:      domain.ExportJobStatusCompleted, // In demo / prototype mode marked ready
		ObjectKey:      &objKey,
		FileSha256:     &fileSha,
		FileSize:       &fileSize,
		StartedAt:      &now,
		CompletedAt:    &completedAt,
		ExpiresAt:      &expiresAt,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	created, err := s.store.CreateJob(ctx, job)
	if err != nil {
		if err == domain.ErrIdempotencyReused {
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
	signedURL := fmt.Sprintf("https://storage.tokendance.dev/%s?token=%s", *job.ObjectKey, exportID)
	expiresAt := now.Add(60 * time.Second).Format("2006-01-02T15:04:05.000Z")

	return &DownloadResponse{
		DownloadURL: signedURL,
		ExpiresAt:   expiresAt,
	}, nil
}
