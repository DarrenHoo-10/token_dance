package device

import (
	"context"
	"errors"
	"tokendance/internal/domain"
)

type aggregateWriter interface {
	CommitAggregate(context.Context, domain.AggregateCommit) (*domain.AggregateAck, error)
}

func (s *Service) CommitAggregate(ctx context.Context, in domain.AggregateCommit) (*domain.AggregateAck, error) {
	if err := in.Snapshot.Validate(in.ReceivedAt); err != nil {
		return nil, domain.NewAppError(400, "AGGREGATE_INVALID", "api.invalidBody", "invalid aggregate snapshot", nil, domain.ErrInvalidArgument)
	}
	writer, ok := s.ingestStore.(aggregateWriter)
	if !ok {
		return nil, domain.NewAppError(503, "AGGREGATES_UNAVAILABLE", "api.unavailable", "aggregate ingestion unavailable", nil, domain.ErrInvalidArgument)
	}
	if _, _, err := s.AuthorizeIngest(ctx, in.InstallationID); err != nil {
		return nil, err
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
