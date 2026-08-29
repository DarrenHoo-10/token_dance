package store

import (
	"context"
	"errors"
	"time"

	"github.com/tokendance/token-collector/server/internal/domain"
)

// UserStore manages user records.
type UserStore interface {
	CreateUser(ctx context.Context, u *domain.User) error
	GetUser(ctx context.Context, userID string) (*domain.User, error)
	GetUserByAuthSubjectHash(ctx context.Context, hash [32]byte) (*domain.User, error)
	UpdateVisibility(ctx context.Context, userID, visibility string) error
	ListPublicUsers(ctx context.Context) ([]*domain.User, error)
}

// InstallationStore manages device installations.
type InstallationStore interface {
	CreateInstallation(ctx context.Context, inst *domain.Installation) error
	GetInstallation(ctx context.Context, installationID string) (*domain.Installation, error)
	GetInstallationByPublicKey(ctx context.Context, pubKey []byte) (*domain.Installation, error)
	UpdateLastSeen(ctx context.Context, installationID string, t time.Time) error
	RevokeInstallation(ctx context.Context, installationID string, t time.Time) error
	ListByUser(ctx context.Context, userID string) ([]*domain.Installation, error)
}

// NonceStore provides anti-replay nonce tracking.
type NonceStore interface {
	// ConsumeNonce atomically checks and records a nonce. Returns true if the
	// nonce was fresh (not seen before expiry).
	ConsumeNonce(ctx context.Context, installationID string, nonceHash [32]byte, expiresAt time.Time) (bool, error)
	PruneExpired(ctx context.Context, now time.Time) (int, error)
}

// BatchStore manages ingest batch idempotency.
type BatchStore interface {
	CreateBatch(ctx context.Context, b *domain.IngestBatch) error
	GetBatch(ctx context.Context, batchID string) (*domain.IngestBatch, error)
	UpdateBatch(ctx context.Context, b *domain.IngestBatch) error
}

var ErrBatchHashConflict = errors.New("batch id reused with different request hash")

// IngestEvent carries protocol fields that are not part of the aggregation model.
type IngestEvent struct {
	Event              *domain.UsageEvent
	ToolCategory       *string
	SkillKey           *[32]byte
	SkillInvokeType    *string
	PluginKey          *[32]byte
	CostAmount         *string
	CostCurrency       *string
	CostSource         *string
	CostDiscountAmount *string
	ChildSessionHash   *[32]byte
	SpawnedAgentType   *string
	CodeLanguage       *string
}

// BatchRejection is the durable per-event ACK detail for a rejected event.
type BatchRejection struct {
	Ordinal   uint32
	EventID   string
	ErrorCode string
	Retryable bool
}

// IngestCommitResult is returned only after the ingest transaction commits.
type IngestCommitResult struct {
	Batch    *domain.IngestBatch
	Rejected []BatchRejection
}

// AtomicIngestStore commits a batch, valid events, and rejected ACK details in one transaction.
type AtomicIngestStore interface {
	CommitBatch(ctx context.Context, b *domain.IngestBatch, events []*IngestEvent, rejected []BatchRejection) (*IngestCommitResult, error)
}

// BatchRejectionStore loads durable per-event ACK details for idempotent replays.
type BatchRejectionStore interface {
	GetBatchRejections(ctx context.Context, batchID string) ([]BatchRejection, error)
}

// WatermarkStore persists aggregation progress across worker restarts.
type WatermarkStore interface {
	GetWatermark(ctx context.Context, name string) (uint64, error)
	SetWatermark(ctx context.Context, name string, eventPK uint64, updatedAt time.Time) error
}

// RecomputeMetricStore atomically rebuilds metrics and advances the watermark.
type RecomputeMetricStore interface {
	RecomputeMetrics(ctx context.Context, watermarkName string, throughEventPK uint64) error
}

// EventStore manages usage events with idempotent insert.
type EventStore interface {
	// InsertEvent inserts an event. Returns (false, nil) if the event already
	// exists (duplicate by installation_id + event_id).
	InsertEvent(ctx context.Context, e *domain.UsageEvent) (inserted bool, err error)
	GetEventsByBatch(ctx context.Context, batchID string) ([]*domain.UsageEvent, error)
	// ListEventsAfterPK returns events with event_pk > afterPK, up to limit.
	ListEventsAfterPK(ctx context.Context, afterPK uint64, limit int) ([]*domain.UsageEvent, error)
	MaxEventPK(ctx context.Context) (uint64, error)
	// DeleteEventsByBatch removes all events belonging to a batch (for recompute).
	DeleteEventsByBatch(ctx context.Context, batchID string) (int, error)
}

// MetricStore manages pre-aggregated daily metrics.
type MetricStore interface {
	UpsertDailyUserAgentMetric(ctx context.Context, m *domain.DailyUserAgentMetric) error
	// GetDailyMetrics returns metrics for a user within [start, end].
	GetDailyMetrics(ctx context.Context, userID string, start, end time.Time) ([]*domain.DailyUserAgentMetric, error)
	// GetDailyMetricsAllUsers returns all metrics within [start, end].
	GetDailyMetricsAllUsers(ctx context.Context, start, end time.Time) ([]*domain.DailyUserAgentMetric, error)
	// DeleteAllMetrics clears all metrics (for full recompute).
	DeleteAllMetrics(ctx context.Context) error
}

// LeaderboardStore manages immutable leaderboard snapshots.
type LeaderboardStore interface {
	CreateSnapshot(ctx context.Context, s *domain.LeaderboardSnapshot) error
	GetSnapshot(ctx context.Context, snapshotID string) (*domain.LeaderboardSnapshot, error)
	PublishSnapshot(ctx context.Context, snapshotID string, publishedAt time.Time) error
	SupersedeSnapshot(ctx context.Context, snapshotID string) error
	// LatestPublishedSnapshot returns the most recently published snapshot
	// matching the given board/scope/metric filter.
	LatestPublishedSnapshot(ctx context.Context, boardKey, scopeType, metricKey string) (*domain.LeaderboardSnapshot, error)
	CreateEntry(ctx context.Context, e *domain.LeaderboardEntry) error
	ListEntries(ctx context.Context, snapshotID string) ([]*domain.LeaderboardEntry, error)
}
