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
	Media() MediaStore
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
	UpdateInstallationName(ctx context.Context, installationID, userID string, name string, now time.Time) (*domain.Installation, error)
	PauseInstallation(ctx context.Context, installationID, userID string, reason string, now time.Time) (*domain.Installation, error)
	ResumeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error)
	RevokeInstallation(ctx context.Context, installationID, userID string, now time.Time) (*domain.Installation, error)
	AuthorizeIngest(ctx context.Context, installationID string) (*domain.Installation, *domain.User, error)
}

type IngestStore interface {
	GetIngestInstallation(ctx context.Context, installationID string) (*domain.Installation, error)
	CommitIngest(ctx context.Context, batch domain.IngestBatch) (*domain.IngestResult, error)
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

type LeaderboardStore interface {
	PublishSnapshot(ctx context.Context, snapshotID string, boardKey, window, metric string, entries []domain.LeaderboardEntry, now time.Time) error
	GetLeaderboard(ctx context.Context, boardKey, window, metric string, cursor *string, limit int) (*domain.LeaderboardResponse, error)
}

type AvatarReadyMeta struct {
	ByteSize      uint64
	ContentSha256 [32]byte
	ImageWidth    uint32
	ImageHeight   uint32
	ContentType   string
}

type MediaStore interface {
	CreateAvatarUploadIntent(ctx context.Context, obj domain.UserUploadObject) (*domain.UserUploadObject, error)
	GetUploadObject(ctx context.Context, objectID, userID string) (*domain.UserUploadObject, error)
	UpdateUploadObjectStatus(ctx context.Context, objectID string, status domain.UploadStatus, errorCode *string, now time.Time) error
	CompleteAvatarUploadIntent(ctx context.Context, objectID, userID string, meta AvatarReadyMeta, now time.Time) (*domain.UserUploadObject, error)
	ClearAvatar(ctx context.Context, userID string, now time.Time) error
}
