package domain

import (
	"crypto/ed25519"
	"time"
)

type User struct {
	UserID                string
	AuthSubjectHash       [32]byte
	DisplayName           string
	AvatarURL             string
	AccountStatus         string // active, suspended, deleted
	LeaderboardVisibility string // private, team, public
	TimezoneName          string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type Installation struct {
	InstallationID     string
	UserID             string
	DevicePublicKey    ed25519.PublicKey
	DeviceName         string
	OSType             string // windows, macos
	OSVersion          string
	Architecture       string
	CollectorVersion   string
	InstallationStatus string // active, revoked, disabled
	RegisteredAt       time.Time
	LastSeenAt         *time.Time
	RevokedAt          *time.Time
}

type IngestNonce struct {
	InstallationID string
	NonceHash      [32]byte
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

type IngestBatch struct {
	BatchID        string
	InstallationID string
	RequestSHA256  [32]byte
	EventCount     uint32
	AcceptedCount  uint32
	DuplicateCount uint32
	RejectedCount  uint32
	BatchStatus    string // received, committed, partial, rejected
	ReceivedAt     time.Time
	CommittedAt    *time.Time
}

type UsageEvent struct {
	EventPK            uint64
	EventID            [32]byte
	SchemaVersion      string
	BatchID            string
	InstallationID     string
	UserID             string
	AdapterID          string
	AdapterVersion     string
	AgentID            string
	AgentVersion       *string
	ProviderID         *string
	ModelID            *string
	EventType          string
	Accuracy           string
	SourceKind         string
	SourceCursorHMAC   [32]byte
	RawFingerprintHMAC [32]byte
	OccurredAt         time.Time
	OccurredDate       time.Time
	ReceivedAt         time.Time
	SessionHash        *[32]byte
	ParentSessionHash  *[32]byte
	TurnHash           *[32]byte
	ToolCallHash       *[32]byte
	TokenInput         *uint64
	TokenOutput        *uint64
	TokenCacheRead     *uint64
	TokenCacheWrite    *uint64
	TokenReasoning     *uint64
	TokenTool          *uint64
	TokenTotal         *uint64
	DurationMs         *uint64
	Success            *bool
	CodeGeneratedLines *uint64
	CodeAcceptedLines  *uint64
	CodeAddedLines     *uint64
	CodeDeletedLines   *uint64
	CodeFileCount      *uint32
}

type DailyUserAgentMetric struct {
	MetricDate           time.Time
	UserID               string
	AgentID              string
	ExactTokenTotal      uint64
	DerivedTokenTotal    uint64
	EstimatedTokenTotal  uint64
	SessionCount         uint64
	ChildSessionCount    uint64
	InteractionTurnCount uint64
	ModelRequestCount    uint64
	ToolCallCount        uint64
	SkillUseCount        uint64
	CodeGeneratedLines   uint64
	CodeAcceptedLines    uint64
	CorrelatedCodeLines  uint64
	SourceMaxEventPK     uint64
	AggregationVersion   uint32
	ComputedAt           time.Time
}

type LeaderboardSnapshot struct {
	SnapshotID         string
	BoardKey           string
	ScopeType          string // global, team, private
	ScopeKey           string
	MetricKey          string
	WindowStart        time.Time
	WindowEnd          time.Time
	TimezoneName       string
	RankingRuleVersion uint32
	ParticipantCount   uint32
	SourceMaxEventPK   uint64
	DataWatermarkAt    time.Time
	SnapshotStatus     string // building, published, superseded, failed
	GeneratedAt        time.Time
	PublishedAt        *time.Time
}

type LeaderboardEntry struct {
	SnapshotID          string
	RankNo              uint32
	UserID              string
	MetricValue         float64
	PreviousRankNo      *uint32
	DisplayNameSnapshot string
	AvatarURLSnapshot   string
	BreakdownJSON       string
}
