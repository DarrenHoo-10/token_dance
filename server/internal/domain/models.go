package domain

import (
	"time"
)

// AccountStatus represents users.account_status
type AccountStatus string

const (
	AccountStatusActive          AccountStatus = "active"
	AccountStatusSuspended       AccountStatus = "suspended"
	AccountStatusDeletionPending AccountStatus = "deletion_pending"
	AccountStatusDeleted         AccountStatus = "deleted"
)

// LeaderboardVisibility represents users.leaderboard_visibility
type LeaderboardVisibility string

const (
	LeaderboardVisibilityPrivate LeaderboardVisibility = "private"
	LeaderboardVisibilityTeam    LeaderboardVisibility = "team"
	LeaderboardVisibilityPublic  LeaderboardVisibility = "public"
)

// ProductState represents derived user product state
type ProductState string

const (
	ProductStateEmailUnverified ProductState = "email_unverified"
	ProductStateNew             ProductState = "new"
	ProductStateActivePrivate   ProductState = "active_private"
	ProductStateActivePublic    ProductState = "active_public"
	ProductStateSuspended       ProductState = "suspended"
	ProductStateDeletionPending ProductState = "deletion_pending"
	ProductStateDeleted         ProductState = "deleted"
)

// User represents the users table
type User struct {
	UserID                 string                `json:"userId"`
	AuthSubjectHash        [32]byte              `json:"-"`
	EmailLookupHash        *[32]byte             `json:"-"`
	EmailCiphertext        []byte                `json:"-"`
	Handle                 *string               `json:"handle"`
	EmailVerifiedAt        *time.Time            `json:"emailVerifiedAt"`
	DisplayName            string                `json:"displayName"`
	AvatarURL              *string               `json:"avatarUrl"`
	AvatarObjectID         *string               `json:"avatarObjectId,omitempty"`
	Bio                    *string               `json:"bio"`
	AccountStatus          AccountStatus         `json:"accountStatus"`
	LeaderboardVisibility  LeaderboardVisibility `json:"leaderboardVisibility"`
	TimezoneName           string                `json:"timezoneName"`
	Locale                 string                `json:"locale"`
	OnboardingCompletedAt  *time.Time            `json:"onboardingCompletedAt"`
	ProfileVersion         uint64                `json:"profileVersion"`
	PublicProfileUpdatedAt *time.Time            `json:"publicProfileUpdatedAt"`
	CreatedAt              time.Time             `json:"createdAt"`
	UpdatedAt              time.Time             `json:"updatedAt"`
	DeletedAt              *time.Time            `json:"deletedAt,omitempty"`
}

func (u *User) ProductState() ProductState {
	switch u.AccountStatus {
	case AccountStatusSuspended:
		return ProductStateSuspended
	case AccountStatusDeletionPending:
		return ProductStateDeletionPending
	case AccountStatusDeleted:
		return ProductStateDeleted
	case AccountStatusActive:
		if u.OnboardingCompletedAt == nil {
			return ProductStateNew
		}
		if u.LeaderboardVisibility == LeaderboardVisibilityPublic {
			return ProductStateActivePublic
		}
		return ProductStateActivePrivate
	default:
		return ProductStateActivePrivate
	}
}

// UserPasswordCredential represents user_password_credentials table
type UserPasswordCredential struct {
	UserID            string     `json:"userId"`
	PasswordHash      string     `json:"-"`
	PasswordAlgorithm string     `json:"passwordAlgorithm"`
	CredentialVersion uint32     `json:"credentialVersion"`
	FailedLoginCount  uint16     `json:"failedLoginCount"`
	LockedUntil       *time.Time `json:"lockedUntil"`
	LastFailedLoginAt *time.Time `json:"lastFailedLoginAt"`
	PasswordChangedAt time.Time  `json:"passwordChangedAt"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
}

// SessionStatus represents user_sessions.session_status
type SessionStatus string

const (
	SessionStatusActive  SessionStatus = "active"
	SessionStatusRevoked SessionStatus = "revoked"
	SessionStatusExpired SessionStatus = "expired"
)

// UserSession represents user_sessions table
type UserSession struct {
	SessionID         string        `json:"sessionId"`
	UserID            string        `json:"userId"`
	SessionTokenHash  [32]byte      `json:"-"`
	CSRFTokenHash     [32]byte      `json:"-"`
	CSRFToken         string        `json:"-"`
	CredentialVersion uint32        `json:"credentialVersion"`
	SessionStatus     SessionStatus `json:"sessionStatus"`
	DeviceLabel       *string       `json:"deviceLabel"`
	UserAgentHash     *[32]byte     `json:"-"`
	IPPrefixHash      *[32]byte     `json:"-"`
	LastSeenAt        time.Time     `json:"lastSeenAt"`
	IdleExpiresAt     time.Time     `json:"idleExpiresAt"`
	AbsoluteExpiresAt time.Time     `json:"absoluteExpiresAt"`
	RevokedAt         *time.Time    `json:"revokedAt,omitempty"`
	RevokeReason      *string       `json:"revokeReason,omitempty"`
	CreatedAt         time.Time     `json:"createdAt"`
	UpdatedAt         time.Time     `json:"updatedAt"`
}

// ChallengeType represents email_challenges.challenge_type
type ChallengeType string

const (
	ChallengeTypeRegister      ChallengeType = "register"
	ChallengeTypePasswordReset ChallengeType = "password_reset"
	ChallengeTypeEmailChange   ChallengeType = "email_change"
)

// ChallengeStatus represents email_challenges.challenge_status
type ChallengeStatus string

const (
	ChallengeStatusPending   ChallengeStatus = "pending"
	ChallengeStatusConsumed  ChallengeStatus = "consumed"
	ChallengeStatusExpired   ChallengeStatus = "expired"
	ChallengeStatusLocked    ChallengeStatus = "locked"
	ChallengeStatusCancelled ChallengeStatus = "cancelled"
)

// EmailChallenge represents email_challenges table
type EmailChallenge struct {
	ChallengeID           string          `json:"challengeID"`
	UserID                *string         `json:"userId,omitempty"`
	EmailLookupHash       [32]byte        `json:"-"`
	EmailCiphertext       []byte          `json:"-"`
	EmailKeyVersion       uint16          `json:"emailKeyVersion"`
	ChallengeType         ChallengeType   `json:"challengeType"`
	CodeHash              [32]byte        `json:"-"`
	CodeKeyVersion        uint16          `json:"codeKeyVersion"`
	ChallengeStatus       ChallengeStatus `json:"challengeStatus"`
	AttemptCount          uint16          `json:"attemptCount"`
	MaxAttempts           uint16          `json:"maxAttempts"`
	SendCount             uint16          `json:"sendCount"`
	RequestedIPPrefixHash *[32]byte       `json:"-"`
	ExpiresAt             time.Time       `json:"expiresAt"`
	ConsumedAt            *time.Time      `json:"consumedAt,omitempty"`
	CreatedAt             time.Time       `json:"createdAt"`
	UpdatedAt             time.Time       `json:"updatedAt"`
}

// EmailOutbox represents email_outbox table
type EmailOutbox struct {
	EmailID              string     `json:"emailId"`
	UserID               *string    `json:"userId,omitempty"`
	ChallengeID          *string    `json:"challengeId,omitempty"`
	IdempotencyKey       [32]byte   `json:"-"`
	TemplateKey          string     `json:"templateKey"`
	Locale               string     `json:"locale"`
	RecipientCiphertext  []byte     `json:"-"`
	PayloadCiphertext    []byte     `json:"-"`
	EncryptionKeyVersion uint16     `json:"encryptionKeyVersion"`
	DeliveryStatus       string     `json:"deliveryStatus"` // pending, sending, sent, failed, cancelled
	AttemptCount         uint16     `json:"attemptCount"`
	NextAttemptAt        time.Time  `json:"nextAttemptAt"`
	LockedAt             *time.Time `json:"lockedAt,omitempty"`
	LockedBy             *string    `json:"lockedBy,omitempty"`
	ProviderMessageID    *string    `json:"providerMessageId,omitempty"`
	LastErrorCode        *string    `json:"lastErrorCode,omitempty"`
	SentAt               *time.Time `json:"sentAt,omitempty"`
	ExpiresAt            time.Time  `json:"expiresAt"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// UserPrivacySettings represents user_privacy_settings table
type UserPrivacySettings struct {
	UserID                string                `json:"userId"`
	PublicProfileEnabled  bool                  `json:"publicProfileEnabled"`
	LeaderboardVisibility LeaderboardVisibility `json:"leaderboardVisibility"`
	ShowBio               bool                  `json:"showBio"`
	ShowTokenTotal        bool                  `json:"showTokenTotal"`
	ShowTrends            bool                  `json:"showTrends"`
	ShowActivityCalendar  bool                  `json:"showActivityCalendar"`
	ShowAgentBreakdown    bool                  `json:"showAgentBreakdown"`
	ShowSkillRanking      bool                  `json:"showSkillRanking"`
	ShowAchievements      bool                  `json:"showAchievements"`
	PrivacyVersion        uint64                `json:"privacyVersion"`
	CreatedAt             time.Time             `json:"createdAt"`
	UpdatedAt             time.Time             `json:"updatedAt"`
}

// ProfileStatus represents public_user_profiles.profile_status
type ProfileStatus string

const (
	ProfileStatusPublished ProfileStatus = "published"
	ProfileStatusHidden    ProfileStatus = "hidden"
)

// PublicUserProfile represents public_user_profiles projection table
type PublicUserProfile struct {
	UserID               string        `json:"userId"`
	Handle               string        `json:"handle"`
	DisplayName          string        `json:"displayName"`
	AvatarURL            *string       `json:"avatarUrl"`
	Bio                  *string       `json:"bio"`
	ProfileStatus        ProfileStatus `json:"profileStatus"`
	ShowBio              bool          `json:"showBio"`
	ShowTokenTotal       bool          `json:"showTokenTotal"`
	ShowTrends           bool          `json:"showTrends"`
	ShowActivityCalendar bool          `json:"showActivityCalendar"`
	ShowAgentBreakdown   bool          `json:"showAgentBreakdown"`
	ShowSkillRanking     bool          `json:"showSkillRanking"`
	ShowAchievements     bool          `json:"showAchievements"`
	SourceProfileVersion uint64        `json:"sourceProfileVersion"`
	SourcePrivacyVersion uint64        `json:"sourcePrivacyVersion"`
	ProjectionVersion    uint64        `json:"projectionVersion"`
	PublishedAt          *time.Time    `json:"publishedAt,omitempty"`
	CreatedAt            time.Time     `json:"createdAt"`
	UpdatedAt            time.Time     `json:"updatedAt"`
}

// UserHandleHistory represents user_handle_history table
type UserHandleHistory struct {
	Handle        string    `json:"handle"`
	UserID        string    `json:"userId"`
	RedirectUntil time.Time `json:"redirectUntil"`
	ReservedUntil time.Time `json:"reservedUntil"`
	CreatedAt     time.Time `json:"createdAt"`
}

// UploadStatus represents user_upload_objects.upload_status
type UploadStatus string

const (
	UploadStatusPending        UploadStatus = "pending"
	UploadStatusUploaded       UploadStatus = "uploaded"
	UploadStatusReady          UploadStatus = "ready"
	UploadStatusRejected       UploadStatus = "rejected"
	UploadStatusDeletedPending UploadStatus = "deleted_pending"
	UploadStatusDeleted        UploadStatus = "deleted"
)

// UserUploadObject represents user_upload_objects table
type UserUploadObject struct {
	ObjectID      string       `json:"objectId"`
	UserID        string       `json:"userId"`
	ObjectType    string       `json:"objectType"` // avatar
	ObjectKey     string       `json:"objectKey"`
	ContentType   *string      `json:"contentType"`
	ByteSize      *uint64      `json:"byteSize"`
	ContentSha256 *[32]byte    `json:"-"`
	ImageWidth    *uint32      `json:"imageWidth"`
	ImageHeight   *uint32      `json:"imageHeight"`
	UploadStatus  UploadStatus `json:"uploadStatus"`
	ExpiresAt     time.Time    `json:"expiresAt"`
	LastErrorCode *string      `json:"lastErrorCode,omitempty"`
	UploadedAt    *time.Time   `json:"uploadedAt,omitempty"`
	ReadyAt       *time.Time   `json:"readyAt,omitempty"`
	DeletedAt     *time.Time   `json:"deletedAt,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

// DeviceBindingChallenge represents device_binding_challenges table
type DeviceBindingChallenge struct {
	ChallengeID            string          `json:"challengeId"`
	UserID                 string          `json:"userId"`
	SessionID              string          `json:"sessionId"`
	CodeLookupHash         [32]byte        `json:"-"`
	CodeKeyVersion         uint16          `json:"codeKeyVersion"`
	ChallengeStatus        ChallengeStatus `json:"challengeStatus"`
	ExpiresAt              time.Time       `json:"expiresAt"`
	ConsumedInstallationID *string         `json:"consumedInstallationId,omitempty"`
	ConsumedAt             *time.Time      `json:"consumedAt,omitempty"`
	CreatedAt              time.Time       `json:"createdAt"`
	UpdatedAt              time.Time       `json:"updatedAt"`
	ActiveSessionKey       *string         `json:"-"`
}

// InstallationStatus represents installations.installation_status
type InstallationStatus string

const (
	InstallationStatusActive   InstallationStatus = "active"
	InstallationStatusDisabled InstallationStatus = "disabled"
	InstallationStatusRevoked  InstallationStatus = "revoked"
)

// Installation represents installations table
type Installation struct {
	InstallationID     string             `json:"installationId"`
	UserID             string             `json:"userId"`
	DevicePublicKey    [32]byte           `json:"-"`
	DeviceName         *string            `json:"deviceName"`
	OSType             string             `json:"osType"`
	OSVersion          *string            `json:"osVersion"`
	Architecture       string             `json:"architecture"`
	CollectorVersion   string             `json:"collectorVersion"`
	InstallationStatus InstallationStatus `json:"installationStatus"`
	DisabledAt         *time.Time         `json:"disabledAt,omitempty"`
	DisabledReason     *string            `json:"disabledReason,omitempty"`
	StatusVersion      uint64             `json:"statusVersion"`
	RegisteredAt       time.Time          `json:"registeredAt"`
	LastSeenAt         *time.Time         `json:"lastSeenAt,omitempty"`
	RevokedAt          *time.Time         `json:"revokedAt,omitempty"`
	UpdatedAt          time.Time          `json:"updatedAt"`
}

type UsageEvent struct {
	EventID              [32]byte
	SchemaVersion        uint16
	AdapterID            string
	AdapterVersion       string
	AgentID              string
	AgentVersion         *string
	ProviderID           *string
	ModelID              *string
	EventType            string
	Accuracy             string
	SourceKind           string
	OccurredAt           time.Time
	SessionHash          *[32]byte
	ParentSessionHash    *[32]byte
	TurnHash             *[32]byte
	ToolCallHash         *[32]byte
	TokenInput           *uint64
	TokenOutput          *uint64
	TokenCacheRead       *uint64
	TokenCacheWrite      *uint64
	TokenReasoning       *uint64
	TokenTotal           *uint64
	DurationMS           *uint64
	Success              *bool
	ToolCategory         *string
	SkillKey             *[32]byte
	SkillPublicName      *string
	SkillInvokeType      *string
	PluginKey            *[32]byte
	CodeGeneratedLines   *uint64
	CodeAcceptedLines    *uint64
	CodeAddedLines       *uint64
	CodeDeletedLines     *uint64
	CodeFileCount        *uint32
	CostAmount           *string
	CostCurrency         *string
	CostSource           *string
	PrivacyPolicyVersion uint16
	SafeExtensionJSON    []byte
}

type IngestBatch struct {
	BatchID        string
	InstallationID string
	RequestSHA256  [32]byte
	NonceHash      [32]byte
	NonceExpiresAt time.Time
	EventCount     uint32
	RejectedCount  uint32
	Events         []UsageEvent
	ReceivedAt     time.Time
}

type IngestResult struct {
	BatchID        string
	AcceptedCount  uint32
	DuplicateCount uint32
	RejectedCount  uint32
	CommittedAt    time.Time
}

// UserSecurityEvent represents user_security_events table
type UserSecurityEvent struct {
	EventID           string                 `json:"eventId"`
	UserID            *string                `json:"userId,omitempty"`
	SessionID         *string                `json:"sessionId,omitempty"`
	EventType         string                 `json:"eventType"`
	Outcome           string                 `json:"outcome"` // success, denied, failure
	SubjectLookupHash *[32]byte              `json:"-"`
	IPPrefixHash      *[32]byte              `json:"-"`
	UserAgentHash     *[32]byte              `json:"-"`
	MetadataJSON      map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt         time.Time              `json:"createdAt"`
}

// ExportJobStatus represents data_export_jobs.job_status
type ExportJobStatus string

const (
	ExportJobStatusPending   ExportJobStatus = "pending"
	ExportJobStatusRunning   ExportJobStatus = "running"
	ExportJobStatusCompleted ExportJobStatus = "completed"
	ExportJobStatusFailed    ExportJobStatus = "failed"
	ExportJobStatusExpired   ExportJobStatus = "expired"
	ExportJobStatusCancelled ExportJobStatus = "cancelled"
)

// DataExportJob represents data_export_jobs table
type DataExportJob struct {
	ExportID       string                 `json:"exportId"`
	UserID         string                 `json:"userId"`
	IdempotencyKey string                 `json:"idempotencyKey"`
	RequestHash    [32]byte               `json:"-"`
	ExportScope    string                 `json:"exportScope"`  // summary, activity, all_aggregates
	ExportFormat   string                 `json:"exportFormat"` // csv, json
	FilterJSON     map[string]interface{} `json:"filter"`
	JobStatus      ExportJobStatus        `json:"jobStatus"`
	AttemptCount   uint16                 `json:"attemptCount"`
	NextAttemptAt  time.Time              `json:"nextAttemptAt"`
	LockedAt       *time.Time             `json:"lockedAt,omitempty"`
	LockedBy       *string                `json:"lockedBy,omitempty"`
	ObjectKey      *string                `json:"objectKey,omitempty"`
	FileSha256     *[32]byte              `json:"-"`
	FileSize       *uint64                `json:"fileSize,omitempty"`
	LastErrorCode  *string                `json:"lastErrorCode,omitempty"`
	StartedAt      *time.Time             `json:"startedAt,omitempty"`
	CompletedAt    *time.Time             `json:"completedAt,omitempty"`
	ExpiresAt      *time.Time             `json:"expiresAt,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
	UpdatedAt      time.Time              `json:"updatedAt"`
}

// DeletionRequestStatus represents data_deletion_requests.request_status
type DeletionRequestStatus string

const (
	DeletionStatusPending   DeletionRequestStatus = "pending"
	DeletionStatusRunning   DeletionRequestStatus = "running"
	DeletionStatusCompleted DeletionRequestStatus = "completed"
	DeletionStatusFailed    DeletionRequestStatus = "failed"
	DeletionStatusCancelled DeletionRequestStatus = "cancelled"
)

// DataDeletionRequest represents data_deletion_requests table
type DataDeletionRequest struct {
	RequestID        string                 `json:"requestId"`
	UserID           *string                `json:"userId,omitempty"`
	DeletionScope    string                 `json:"deletionScope"` // account, installation, time_range, all_usage
	ScopeFilterJSON  map[string]interface{} `json:"scopeFilter,omitempty"`
	RequestStatus    DeletionRequestStatus  `json:"requestStatus"`
	Phase            string                 `json:"phase"`
	ProgressCursor   uint64                 `json:"progressCursor"`
	ActiveAccountKey *string                `json:"-"`
	CancelBefore     *time.Time             `json:"cancelBefore,omitempty"`
	CancelledAt      *time.Time             `json:"cancelledAt,omitempty"`
	RequestedAt      time.Time              `json:"requestedAt"`
	CompletedAt      *time.Time             `json:"completedAt,omitempty"`
	AuditReference   *string                `json:"auditReference,omitempty"`
}

// Analytics Domain Models
type TimeRangeKey string

const (
	TimeRangeToday  TimeRangeKey = "today"
	TimeRange7d     TimeRangeKey = "7d"
	TimeRange30d    TimeRangeKey = "30d"
	TimeRangeAll    TimeRangeKey = "all"
	TimeRangeCustom TimeRangeKey = "custom"
)

type TimeRange struct {
	Key      TimeRangeKey `json:"key"`
	From     time.Time    `json:"from"`
	To       time.Time    `json:"to"`
	Timezone string       `json:"timezone"`
}

type MetricCost struct {
	Amount    *string `json:"amount"`
	Currency  *string `json:"currency"`
	Supported bool    `json:"supported"`
}

type MetricBigInt struct {
	Value     *string `json:"value"`
	Supported bool    `json:"supported"`
}

type MetricDecimal struct {
	Value     *string `json:"value"`
	Supported bool    `json:"supported"`
}

type PersonalSummaryMetrics struct {
	EstimatedCost      MetricCost    `json:"estimatedCost"`
	TotalTokens        MetricBigInt  `json:"totalTokens"`
	GeneratedCodeLines MetricBigInt  `json:"generatedCodeLines"`
	TokensPerCodeLine  MetricDecimal `json:"tokensPerCodeLine"`
	InputContextTokens MetricBigInt  `json:"inputContextTokens"`
	OutputTokens       MetricBigInt  `json:"outputTokens"`
	CacheHitRate       MetricDecimal `json:"cacheHitRate"`
	ActiveDurationMs   MetricBigInt  `json:"activeDurationMs"`
	MessageCount       MetricBigInt  `json:"messageCount"`
	UserMessageCount   MetricBigInt  `json:"userMessageCount"`
}

type PersonalSummaryRanking struct {
	Visibility LeaderboardVisibility `json:"visibility"`
	Rank       *int                  `json:"rank"`
	Delta      *int                  `json:"delta"`
	Percentile *float64              `json:"percentile"`
}

type PersonalSummarySync struct {
	LastCommittedAt   *time.Time `json:"lastCommittedAt"`
	PendingLocalCount *int       `json:"pendingLocalCount"`
}

type PersonalSummary struct {
	Range              TimeRange              `json:"range"`
	Metrics            PersonalSummaryMetrics `json:"metrics"`
	Ranking            PersonalSummaryRanking `json:"ranking"`
	Sync               PersonalSummarySync    `json:"sync"`
	DataWatermarkAt    *time.Time             `json:"dataWatermarkAt"`
	AggregationVersion uint32                 `json:"aggregationVersion"`
}

type TrendPoint struct {
	Date             string  `json:"date"`
	TokenTotal       *string `json:"tokenTotal,omitempty"`
	InputTokens      *string `json:"inputTokens,omitempty"`
	OutputTokens     *string `json:"outputTokens,omitempty"`
	CacheReadTokens  *string `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens *string `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens  *string `json:"reasoningTokens,omitempty"`
}

type TrendResponse struct {
	Range              TimeRange    `json:"range"`
	Mode               string       `json:"mode"` // total, structure
	AgentID            *string      `json:"agentId,omitempty"`
	ProviderID         *string      `json:"providerId,omitempty"`
	ModelID            *string      `json:"modelId,omitempty"`
	Granularity        string       `json:"granularity"` // day, month
	Points             []TrendPoint `json:"points"`
	DataWatermarkAt    *time.Time   `json:"dataWatermarkAt"`
	AggregationVersion uint32       `json:"aggregationVersion"`
}

type BreakdownItem struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	TokenTotal string  `json:"tokenTotal"`
	Percentage float64 `json:"percentage"`
}

type BreakdownResponse struct {
	Range              TimeRange       `json:"range"`
	Items              []BreakdownItem `json:"items"`
	DataWatermarkAt    *time.Time      `json:"dataWatermarkAt"`
	AggregationVersion uint32          `json:"aggregationVersion"`
}

type SkillItem struct {
	SkillID          string   `json:"skillId"`
	SkillPublicName  string   `json:"skillPublicName"`
	UseCount         string   `json:"useCount"`
	ActiveDays       int      `json:"activeDays"`
	SuccessRate      *float64 `json:"successRate,omitempty"`
	PreviousDeltaPct *float64 `json:"previousDeltaPct,omitempty"`
}

type SkillsResponse struct {
	Range              TimeRange   `json:"range"`
	Skills             []SkillItem `json:"skills"`
	DataWatermarkAt    *time.Time  `json:"dataWatermarkAt"`
	AggregationVersion uint32      `json:"aggregationVersion"`
}

type CalendarDay struct {
	Date       string `json:"date"`
	Active     bool   `json:"active"`
	Level      int    `json:"level"` // 0..4
	TokenTotal string `json:"tokenTotal"`
}

type CalendarResponse struct {
	Days               []CalendarDay `json:"days"`
	CurrentStreak      int           `json:"currentStreak"`
	LongestStreak      int           `json:"longestStreak"`
	TotalActiveDays    int           `json:"totalActiveDays"`
	DataWatermarkAt    *time.Time    `json:"dataWatermarkAt"`
	AggregationVersion uint32        `json:"aggregationVersion"`
}

type ActivityQuery struct {
	Range      TimeRange
	AgentID    *string
	ProviderID *string
	ModelID    *string
	Limit      int
	Offset     int
}

type ActivityRow struct {
	Date               string  `json:"date"`
	AgentID            string  `json:"agentId"`
	ProviderID         *string `json:"providerId,omitempty"`
	ModelID            *string `json:"modelId,omitempty"`
	TokenTotal         string  `json:"tokenTotal"`
	MessageCount       *string `json:"messageCount,omitempty"`
	ActiveDurationMs   *string `json:"activeDurationMs,omitempty"`
	GeneratedCodeLines string  `json:"generatedCodeLines"`
}

type ActivityResponse struct {
	Items      []ActivityRow `json:"items"`
	NextCursor *string       `json:"nextCursor,omitempty"`
	Range      TimeRange     `json:"range"`
}

type FilterOptions struct {
	Agents    []string `json:"agents"`
	Providers []string `json:"providers"`
	Models    []string `json:"models"`
}

// Leaderboard Snapshot & Entries
type LeaderboardEntry struct {
	RankNo      int     `json:"rankNo"`
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
	MetricValue string  `json:"metricValue"`
	RankDelta   *int    `json:"rankDelta,omitempty"`
}

type LeaderboardResponse struct {
	SnapshotID      string             `json:"snapshotId"`
	BoardKey        string             `json:"boardKey"`
	Window          string             `json:"window"`
	Metric          string             `json:"metric"`
	Entries         []LeaderboardEntry `json:"entries"`
	NextCursor      *string            `json:"nextCursor,omitempty"`
	DataWatermarkAt *time.Time         `json:"dataWatermarkAt"`
}

// Public Search Models
type SearchUserResult struct {
	Handle      string  `json:"handle"`
	DisplayName string  `json:"displayName"`
	AvatarURL   *string `json:"avatarUrl"`
	Bio         *string `json:"bio,omitempty"`
	Rank        *int    `json:"rank,omitempty"`
}

type SearchAgentResult struct {
	AgentID     string `json:"agentId"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type SearchSkillResult struct {
	SkillID         string `json:"skillId"`
	SkillPublicName string `json:"skillPublicName"`
	UseCount        string `json:"useCount"`
	PublicUserCount int    `json:"publicUserCount"`
	ActiveDays      int    `json:"activeDays"`
}

type SearchResponse struct {
	Users  []SearchUserResult  `json:"users"`
	Agents []SearchAgentResult `json:"agents"`
	Skills []SearchSkillResult `json:"skills,omitempty"`
}

// Compare Models (P1)
type CompareUserItem struct {
	Handle          string          `json:"handle"`
	DisplayName     *string         `json:"displayName,omitempty"`
	AvatarURL       *string         `json:"avatarUrl,omitempty"`
	Visible         bool            `json:"visible"`
	TokenTotal      *string         `json:"tokenTotal,omitempty"`
	CodeLinesTotal  *string         `json:"codeLinesTotal,omitempty"`
	Rank            *int            `json:"rank,omitempty"`
	Percentile      *float64        `json:"percentile,omitempty"`
	ActiveDays      *int            `json:"activeDays,omitempty"`
	CurrentStreak   *int            `json:"currentStreak,omitempty"`
	AgentBreakdown  []BreakdownItem `json:"agentBreakdown,omitempty"`
	SkillRanking    []SkillItem     `json:"skillRanking,omitempty"`
	DataWatermarkAt *time.Time      `json:"dataWatermarkAt,omitempty"`
}

type CompareResponse struct {
	Users       []CompareUserItem `json:"users"`
	GeneratedAt time.Time         `json:"generatedAt"`
}

// Public DTO Whitelist (USR-016)
type PublicProfileDTO struct {
	Handle               string     `json:"handle"`
	DisplayName          string     `json:"displayName"`
	AvatarURL            *string    `json:"avatarUrl"`
	Bio                  *string    `json:"bio,omitempty"`
	Rank                 *int       `json:"rank,omitempty"`
	RankDelta            *int       `json:"rankDelta,omitempty"`
	Percentile           *float64   `json:"percentile,omitempty"`
	TokenTotal           *string    `json:"tokenTotal,omitempty"`
	ActiveDays           *int       `json:"activeDays,omitempty"`
	CurrentStreak        *int       `json:"currentStreak,omitempty"`
	DataWatermarkAt      *time.Time `json:"dataWatermarkAt,omitempty"`
	GeneratedAt          time.Time  `json:"generatedAt"`
	ProjectionVersion    uint64     `json:"projectionVersion"`
	ShowBio              bool       `json:"showBio"`
	ShowTokenTotal       bool       `json:"showTokenTotal"`
	ShowTrends           bool       `json:"showTrends"`
	ShowActivityCalendar bool       `json:"showActivityCalendar"`
	ShowAgentBreakdown   bool       `json:"showAgentBreakdown"`
	ShowSkillRanking     bool       `json:"showSkillRanking"`
	ShowAchievements     bool       `json:"showAchievements"`
}
