package store

import (
	"context"
	"errors"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
)

var ErrCacheMiss = errors.New("cache miss")

// LeaderboardCache stores published snapshots and their immutable entries.
type LeaderboardCache interface {
	GetSnapshot(ctx context.Context, snapshotID string) (*domain.LeaderboardSnapshot, error)
	SetSnapshot(ctx context.Context, snapshot *domain.LeaderboardSnapshot) error
	GetLatest(ctx context.Context, boardKey, scopeType, scopeKey, metricKey string) (*domain.LeaderboardSnapshot, error)
	SetLatest(ctx context.Context, boardKey, scopeType, scopeKey, metricKey string, snapshot *domain.LeaderboardSnapshot) error
	GetEntries(ctx context.Context, snapshotID string) ([]*domain.LeaderboardEntry, error)
	SetEntries(ctx context.Context, snapshotID string, entries []*domain.LeaderboardEntry) error
	InvalidateLeaderboard(ctx context.Context) error
}

// FallbackLeaderboardStore reads through Redis and treats MySQL as authoritative.
type FallbackLeaderboardStore struct {
	Cache    LeaderboardCache
	Fallback LeaderboardStore
}

func (s *FallbackLeaderboardStore) CreateSnapshot(ctx context.Context, snapshot *domain.LeaderboardSnapshot) error {
	return s.Fallback.CreateSnapshot(ctx, snapshot)
}

func (s *FallbackLeaderboardStore) GetSnapshot(ctx context.Context, snapshotID string) (*domain.LeaderboardSnapshot, error) {
	if s.Cache != nil {
		if snapshot, err := s.Cache.GetSnapshot(ctx, snapshotID); err == nil {
			return snapshot, nil
		}
	}
	snapshot, err := s.Fallback.GetSnapshot(ctx, snapshotID)
	if err == nil && s.Cache != nil && snapshot.SnapshotStatus == "published" {
		_ = s.Cache.SetSnapshot(ctx, snapshot)
	}
	return snapshot, err
}

func (s *FallbackLeaderboardStore) PublishSnapshot(ctx context.Context, snapshotID string, publishedAt time.Time) error {
	if err := s.Fallback.PublishSnapshot(ctx, snapshotID, publishedAt); err != nil {
		return err
	}
	return s.invalidate(ctx)
}

func (s *FallbackLeaderboardStore) SupersedeSnapshot(ctx context.Context, snapshotID string) error {
	if err := s.Fallback.SupersedeSnapshot(ctx, snapshotID); err != nil {
		return err
	}
	return s.invalidate(ctx)
}

func (s *FallbackLeaderboardStore) invalidate(ctx context.Context) error {
	if s.Cache == nil {
		return nil
	}
	return s.Cache.InvalidateLeaderboard(ctx)
}

func (s *FallbackLeaderboardStore) LatestPublishedSnapshot(ctx context.Context, boardKey, scopeType, metricKey string) (*domain.LeaderboardSnapshot, error) {
	return s.latest(ctx, boardKey, scopeType, "", metricKey, func() (*domain.LeaderboardSnapshot, error) {
		return s.Fallback.LatestPublishedSnapshot(ctx, boardKey, scopeType, metricKey)
	})
}

func (s *FallbackLeaderboardStore) LatestPublishedSnapshotScoped(ctx context.Context, boardKey, scopeType, scopeKey, metricKey string) (*domain.LeaderboardSnapshot, error) {
	loader, ok := s.Fallback.(interface {
		LatestPublishedSnapshotScoped(context.Context, string, string, string, string) (*domain.LeaderboardSnapshot, error)
	})
	if !ok {
		return s.LatestPublishedSnapshot(ctx, boardKey, scopeType, metricKey)
	}
	return s.latest(ctx, boardKey, scopeType, scopeKey, metricKey, func() (*domain.LeaderboardSnapshot, error) {
		return loader.LatestPublishedSnapshotScoped(ctx, boardKey, scopeType, scopeKey, metricKey)
	})
}

func (s *FallbackLeaderboardStore) latest(ctx context.Context, boardKey, scopeType, scopeKey, metricKey string, load func() (*domain.LeaderboardSnapshot, error)) (*domain.LeaderboardSnapshot, error) {
	if s.Cache != nil {
		if snapshot, err := s.Cache.GetLatest(ctx, boardKey, scopeType, scopeKey, metricKey); err == nil {
			return snapshot, nil
		}
	}
	snapshot, err := load()
	if err == nil && s.Cache != nil {
		_ = s.Cache.SetLatest(ctx, boardKey, scopeType, scopeKey, metricKey, snapshot)
		_ = s.Cache.SetSnapshot(ctx, snapshot)
	}
	return snapshot, err
}

func (s *FallbackLeaderboardStore) CreateEntry(ctx context.Context, entry *domain.LeaderboardEntry) error {
	return s.Fallback.CreateEntry(ctx, entry)
}

func (s *FallbackLeaderboardStore) ListEntries(ctx context.Context, snapshotID string) ([]*domain.LeaderboardEntry, error) {
	if s.Cache != nil {
		if entries, err := s.Cache.GetEntries(ctx, snapshotID); err == nil {
			return entries, nil
		}
	}
	entries, err := s.Fallback.ListEntries(ctx, snapshotID)
	if err == nil && s.Cache != nil {
		_ = s.Cache.SetEntries(ctx, snapshotID, entries)
	}
	return entries, err
}

func (s *FallbackLeaderboardStore) AuthorizeLeaderboardScope(ctx context.Context, userID, scopeType, scopeKey string) (bool, error) {
	authorizer, ok := s.Fallback.(interface {
		AuthorizeLeaderboardScope(context.Context, string, string, string) (bool, error)
	})
	if !ok {
		return scopeType == "global" && scopeKey == "all", nil
	}
	return authorizer.AuthorizeLeaderboardScope(ctx, userID, scopeType, scopeKey)
}
