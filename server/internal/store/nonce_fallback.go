package store

import (
	"context"
	"time"
)

// NonceCache is the Redis-facing anti-replay contract.
type NonceCache interface {
	ConsumeNonce(ctx context.Context, installationID string, nonceHash [32]byte, expiresAt time.Time) (bool, error)
	PruneExpired(ctx context.Context, now time.Time) (int, error)
}

// FallbackNonceStore uses the durable MySQL store when Redis is unavailable.
type FallbackNonceStore struct {
	Cache    NonceCache
	Fallback NonceStore
}

func (s *FallbackNonceStore) ConsumeNonce(ctx context.Context, installationID string, nonceHash [32]byte, expiresAt time.Time) (bool, error) {
	fresh, err := s.Fallback.ConsumeNonce(ctx, installationID, nonceHash, expiresAt)
	if err != nil || !fresh {
		return fresh, err
	}
	if s.Cache == nil {
		return true, nil
	}
	_, _ = s.Cache.ConsumeNonce(ctx, installationID, nonceHash, expiresAt)
	return true, nil
}

func (s *FallbackNonceStore) PruneExpired(ctx context.Context, now time.Time) (int, error) {
	n, err := s.Fallback.PruneExpired(ctx, now)
	if err != nil {
		return 0, err
	}
	if s.Cache != nil {
		_, _ = s.Cache.PruneExpired(ctx, now)
	}
	return n, nil
}
