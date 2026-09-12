package device

import (
	"context"
	"errors"
	"tokendance/internal/domain"
)

type aggregateWriter interface {
	CommitAggregate(context.Context, domain.AggregateCommit) (*domain.AggregateAck, error)
	GetIngestCursor(context.Context, string) (domain.TelemetryCursor, error)
}

func (s *Service) CommitAggregate(ctx context.Context, in domain.AggregateCommit) (*domain.AggregateAck, error) {
	if err := in.Snapshot.Validate(in.ReceivedAt); err != nil {
		return nil, domain.NewAppError(400, "AGGREGATE_INVALID", "api.invalidBody", "invalid aggregate snapshot", nil, domain.ErrInvalidArgument)
	}
	inst, _, err := s.AuthorizeIngest(ctx, in.InstallationID)
	if err != nil {
		return nil, err
	}
	// Builds older than the floor double-counted daily usage (fixed in
	// 0.1.22). Their snapshots must not reach the leaderboard; the desktop
	// treats this error as an update prompt.
	if !aggregateCollectorVersionSupported(inst.CollectorVersion) {
		return nil, domain.NewAppError(403, "CLIENT_VERSION_UNSUPPORTED", "sync.clientVersionUnsupported", "collector version no longer accepted for aggregate sync; update TokenDance", nil, nil)
	}
	writer, ok := s.ingestStore.(aggregateWriter)
	if !ok {
		return nil, domain.NewAppError(503, "AGGREGATES_UNAVAILABLE", "api.unavailable", "aggregate ingestion unavailable", nil, domain.ErrInvalidArgument)
	}
	ack, err := writer.CommitAggregate(ctx, in)
	if errors.Is(err, domain.ErrNonceReplay) {
		return nil, domain.NewAppError(409, "INGEST_NONCE_REPLAY", "ingest.nonceReplay", "nonce already used", nil, err)
	}
	if errors.Is(err, domain.ErrDeviceDisabled) || errors.Is(err, domain.ErrDeviceRevoked) || errors.Is(err, domain.ErrAccountSuspended) {
		return nil, domain.NewAppError(403, "DEVICE_UNAVAILABLE", "device.unavailable", "device unavailable", nil, err)
	}
	return ack, err
}

// GetIngestCursor exposes the per-device event watermark.
func (s *Service) GetIngestCursor(ctx context.Context, installationID string) (domain.TelemetryCursor, error) {
	if _, _, err := s.AuthorizeIngest(ctx, installationID); err != nil {
		return domain.TelemetryCursor{}, err
	}
	return s.ingestStore.GetIngestCursor(ctx, installationID)
}
