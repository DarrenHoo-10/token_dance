package store

import (
	"context"
	"time"

	"tokendance/internal/domain"
)

type Store interface {
	Auth() AuthStore
	Profile() ProfileStore
	Privacy() PrivacyStore
	Analytics() AnalyticsStore
	Device() DeviceStore
	Ingest() IngestStore
	Export() ExportStore
	Search() SearchStore
	Leaderboard() LeaderboardStore
	CommunityStats() CommunityStatsStore
	Media() MediaStore
	Teams() TeamsStore
}

type RegistrationTxInput struct {
	User          domain.User
	Credential    domain.UserPasswordCredential
	Privacy       domain.UserPrivacySettings
	Session       domain.UserSession
	ChallengeID   string
	SecurityEvent domain.UserSecurityEvent
}

type AuthStore interface {
	CreateOrReplaceEmailChallenge(ctx context.Context, challenge domain.EmailChallenge, outbox domain.EmailOutbox) (*domain.EmailChallenge, error)
	FindPendingEmailChallenge(ctx context.Context, challengeType domain.ChallengeType, emailLookupHash [32]byte) (*domain.EmailChallenge, error)
	UpdateEmailChallengeAttempts(ctx context.Context, challengeID string, attemptCount uint16, status domain.ChallengeStatus) error
	RecordEmailChallengeFailure(ctx context.Context, challengeID string, now time.Time) error
	CompleteRegistrationTx(ctx context.Context, in RegistrationTxInput) (*domain.UserSession, error)
	FindUserByEmailHash(ctx context.Context, emailLookupHash [32]byte) (*domain.User, *domain.UserPasswordCredential, error)
	FindUserByID(ctx context.Context, userID string) (*domain.User, error)
	RecordLoginFailure(ctx context.Context, userID string, failedCount uint16, lockedUntil *time.Time, event domain.UserSecurityEvent) error
	CreateSessionTx(ctx context.Context, session domain.UserSession, event domain.UserSecurityEvent) (*domain.UserSession, error)
	ResolveSession(ctx context.Context, tokenHash [32]byte, now time.Time) (*domain.UserSession, *domain.User, error)
	RevokeSession(ctx context.Context, sessionID string, reason string, now time.Time) error
	RevokeUserSession(ctx context.Context, sessionID string, userID string, reason string, now time.Time) error
	RevokeOtherSessions(ctx context.Context, userID, currentSessionID string, reason string, now time.Time, event domain.UserSecurityEvent) error
	ListUserSessions(ctx context.Context, userID string) ([]domain.UserSession, error)
	ResetPasswordTx(ctx context.Context, emailLookupHash [32]byte, codeHash [32]byte, newHash string, newVersion uint32, event domain.UserSecurityEvent, now time.Time) error
	RotateSessionCSRF(ctx context.Context, sessionID string, newCSRFHash [32]byte, now time.Time) error
	TouchSessionLastSeen(ctx context.Context, sessionID string, now time.Time) error
}

type ProfileStore interface {
	GetUserProfile(ctx context.Context, userID string) (*domain.User, error)
	CompleteOnboardingTx(ctx context.Context, userID string, handle string, displayName string, timezone string, locale string, privacy domain.UserPrivacySettings, event domain.UserSecurityEvent, now time.Time) (*domain.User, *domain.UserPrivacySettings, error)
	UpdateProfileTx(ctx context.Context, userID string, displayName *string, handle *string, bio *string, timezone *string, locale *string, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.User, error)
	IsHandleAvailable(ctx context.Context, handle string, excludeUserID string, now time.Time) (bool, error)
	GetRedirectHandle(ctx context.Context, oldHandle string, now time.Time) (string, error)
}

type PrivacyStore interface {
	GetPrivacy(ctx context.Context, userID string) (*domain.UserPrivacySettings, error)
	UpdatePrivacyTx(ctx context.Context, userID string, in domain.UserPrivacySettings, expectedVersion uint64, event domain.UserSecurityEvent, now time.Time) (*domain.UserPrivacySettings, error)
	GetPublicProfileByHandle(ctx context.Context, handle string, now time.Time) (*domain.PublicUserProfile, error)
	SetAccountStatusTx(ctx context.Context, userID string, status domain.AccountStatus, now time.Time) error
	RequestDeletionTx(ctx context.Context, req domain.DataDeletionRequest, event domain.UserSecurityEvent, now time.Time) (*domain.DataDeletionRequest, error)
	CancelDeletionTx(ctx context.Context, requestID string, userID string, now time.Time) error
	GetDeletionRequest(ctx context.Context, requestID string, userID string) (*domain.DataDeletionRequest, error)
}

type AnalyticsStore interface {
	GetPersonalSummary(ctx context.Context, userID string, r domain.TimeRange) (*domain.PersonalSummary, error)
	GetTokenTrend(ctx context.Context, userID string, r domain.TimeRange, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error)
	GetAgentBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error)
	GetModelBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error)
	GetSkillRanking(ctx context.Context, userID string, r domain.TimeRange) (*domain.SkillsResponse, error)
	GetActivityCalendar(ctx context.Context, userID string, r domain.TimeRange) (*domain.CalendarResponse, error)
	GetActivity(ctx context.Context, userID string, q domain.ActivityQuery) ([]domain.ActivityRow, error)
	GetFilterOptions(ctx context.Context, userID string) (*domain.FilterOptions, error)
}

type DeviceStore interface {
	ListInstallations(ctx context.Context, userID string) ([]domain.Installation, error)
	GetInstallation(ctx context.Context, installationID string, userID string) (*domain.Installation, error)
	CreateBindingChallenge(ctx context.Context, challenge domain.DeviceBindingChallenge) (*domain.DeviceBindingChallenge, error)
	CancelBindingChallenge(ctx context.Context, challengeID, userID string) error
	ClaimInstallationTx(ctx context.Context, codeHash [32]byte, inst domain.Installation, now time.Time) (*domain.Installation, error)
	RegisterInstallationTx(ctx context.Context, inst domain.Installation, now time.Time) (*domain.Installation, error)
	RebindInstallationTx(ctx context.Context, installationID, newUserID string, now time.Time) (*domain.Installation, error)
	UpdateInstallationName(ctx context.Context, installationID, userID string, name string, now time.Time) (*domain.Installation, error)
	PauseInstallation(ctx context.Context, installationID, userID string, reason string, now time.Time) (*domain.Installation, error)
	ResumeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error)
	RevokeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error)
	AuthorizeIngest(ctx context.Context, installationID string) (*domain.Installation, *domain.User, error)
}

type IngestStore interface {
	GetIngestInstallation(ctx context.Context, installationID string) (*domain.Installation, error)
	CommitIngest(ctx context.Context, batch domain.IngestBatch) (*domain.IngestResult, error)
	CommitTelemetryEventsV2(ctx context.Context, in domain.TelemetryEventsV2Input) (*domain.TelemetryEventsV2Result, error)
	GetIngestCursor(ctx context.Context, installationID string) (domain.TelemetryCursor, error)
}

type ExportStore interface {
	CreateJob(ctx context.Context, job domain.DataExportJob, idempotencyKeys []string) (*domain.DataExportJob, error)
	ListJobs(ctx context.Context, userID string) ([]domain.DataExportJob, error)
	GetJob(ctx context.Context, exportID, userID string) (*domain.DataExportJob, error)
	ClaimPendingJob(ctx context.Context, workerID string, leaseDuration time.Duration, now time.Time) (*domain.DataExportJob, error)
	CompleteJob(ctx context.Context, exportID string, workerID string, objectKey string, fileSha256 [32]byte, fileSize uint64, now time.Time) error
	FailJob(ctx context.Context, exportID string, workerID string, lastError string, now time.Time) error
}

type SearchStore interface {
	Search(ctx context.Context, query string, limit int, now time.Time) (*domain.SearchResponse, error)
}

type LeaderboardQuery struct {
	BoardKey     string
	Window       string
	Metric       string
	SnapshotID   string
	ViewerUserID string
	Cursor       *string
	Limit        int
}

type LeaderboardStore interface {
	PublishSnapshot(ctx context.Context, snapshotID string, boardKey, window, metric string, entries []domain.LeaderboardEntry, now time.Time) error
	GetLeaderboard(ctx context.Context, boardKey, window, metric string, cursor *string, limit int) (*domain.LeaderboardResponse, error)
	GetLeaderboardView(ctx context.Context, q LeaderboardQuery) (*domain.LeaderboardResponse, error)
}

// CommunityCost is one currency's community total in major units.
// Distinct currencies are never FX-merged into CostAmount.
type CommunityCost struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
}

// CommunityDailyTotals is one precomputed day of whole-community aggregates.
// The stats worker recomputes a day from telemetry_* tables and overwrites
// the row; request paths only read these rows, never aggregate.
type CommunityDailyTotals struct {
	MetricDate   string          `json:"metricDate"`
	TokensTotal  uint64          `json:"tokensTotal"`
	Developers   uint64          `json:"developers"`
	CodeLines    uint64          `json:"codeLines"`
	Interactions uint64          `json:"interactions"`
	CostAmount   float64         `json:"costAmount"`
	Costs        []CommunityCost `json:"costs,omitempty"`
	IsFinal      bool            `json:"isFinal"`
	ComputedAt   time.Time       `json:"computedAt"`
}

type CommunityStatsStore interface {
	SumCommunityDay(ctx context.Context, date string) (CommunityDailyTotals, error)
	UpsertCommunityDailyStats(ctx context.Context, totals CommunityDailyTotals) error
	GetCommunityDailyStats(ctx context.Context, date string) (*CommunityDailyTotals, error)
	ReplaceCommunityAgentDay(ctx context.Context, date string, rows []CommunityAgentTokens) error
	GetCommunityHarnessShares(ctx context.Context, date string, limit int) ([]CommunityHarness, error)
}

// CommunityAgentTokens is one harness's token total inside a metric day.
type CommunityAgentTokens struct {
	AgentID     string
	TokensTotal uint64
}

// CommunityHarness is a display-ready harness row: tokens plus the agent
// display name resolved by the store implementation.
type CommunityHarness struct {
	AgentID     string
	Label       string
	TokensTotal uint64
}

type AvatarReadyMeta struct {
	ByteSize      uint64
	ContentSha256 [32]byte
	ImageWidth    uint32
	ImageHeight   uint32
	ContentType   string
}

type MediaStore interface {
	GetVisibleAvatar(ctx context.Context, objectID, viewerID string) (*domain.UserUploadObject, error)
	CreateAvatarUploadIntent(ctx context.Context, obj domain.UserUploadObject) (*domain.UserUploadObject, error)
	GetUploadObject(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error)
	UpdateUploadObjectStatus(ctx context.Context, objectID string, status domain.UploadStatus, errorCode *string, now time.Time) error
	CompleteAvatarUploadIntent(ctx context.Context, objectID, userID string, meta AvatarReadyMeta, now time.Time) (*domain.UserUploadObject, error)
	ClearAvatar(ctx context.Context, userID string, now time.Time) error
}
