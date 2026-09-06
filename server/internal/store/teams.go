package store

import (
	"context"
	"time"

	"tokendance/internal/domain"
)

type TeamsTxOutcome string

const (
	TeamsTxChanged       TeamsTxOutcome = "changed"
	TeamsTxReplay        TeamsTxOutcome = "replay"
	TeamsTxAlreadyMember TeamsTxOutcome = "already_member"
)

type TeamsIdempotency struct {
	KeyHash     [32]byte
	RequestHash [32]byte
	Scope       string
}

type CreateTeamTxInput struct {
	ActorUserID string
	Team        domain.Team
	Membership  domain.TeamMembership
	Sharing     domain.SharingFlags
	Idempotency TeamsIdempotency
	Now         time.Time
}

type CreateTeamTxResult struct {
	Outcome    TeamsTxOutcome
	Context    *domain.TeamContext
	Receipt    *domain.TeamCommandReceipt
}

type CreateInvitationTxInput struct {
	ActorUserID   string
	TeamID        string
	InvitedRole   domain.TeamBaseRole
	Invitation    domain.TeamInvitation
	Outbox        domain.EmailOutbox
	Idempotency   TeamsIdempotency
	Now           time.Time
}

type CreateInvitationTxResult struct {
	Outcome    TeamsTxOutcome
	Invitation *domain.TeamInvitation
}

type AcceptInvitationTxInput struct {
	ActorUserID             string
	InvitationID            string
	ExpectedVersion         uint64
	Sharing                 domain.SharingFlags
	VerifiedEmailLookupHash [32]byte
	Idempotency             TeamsIdempotency
	Now                     time.Time
}

type AcceptInvitationTxResult struct {
	Outcome TeamsTxOutcome
	Context *domain.TeamContext
}

type CreateInviteLinkTxInput struct {
	ActorUserID string
	TeamID      string
	Link        domain.TeamInviteLink
	Idempotency TeamsIdempotency
	Now         time.Time
}

type CreateInviteLinkTxResult struct {
	Outcome TeamsTxOutcome
	Link    *domain.TeamInviteLink
}

type RevokeInviteLinkTxInput struct {
	ActorUserID     string
	TeamID          string
	LinkID          string
	ExpectedVersion uint64
	Idempotency     TeamsIdempotency
	Now             time.Time
}

type RegenerateInviteLinkTxInput struct {
	ActorUserID     string
	TeamID          string
	LinkID          string
	ExpectedVersion uint64
	NewLink         domain.TeamInviteLink
	Idempotency     TeamsIdempotency
	Now             time.Time
}

type AcceptInviteLinkTxInput struct {
	ActorUserID     string
	LinkID          string
	TokenHash       [32]byte
	ExpectedVersion uint64
	Sharing         domain.SharingFlags
	Idempotency     TeamsIdempotency
	Now             time.Time
}

type AcceptInviteLinkTxResult struct {
	Outcome TeamsTxOutcome
	Context *domain.TeamContext
}

type UpdateSharingTxInput struct {
	ActorUserID     string
	TeamID          string
	ExpectedVersion uint64
	Sharing         domain.SharingFlags
	Now             time.Time
}

type ChangeMemberRoleTxInput struct {
	ActorUserID         string
	TeamID              string
	MembershipID        string
	NewRole             domain.TeamBaseRole
	ExpectedAuthRevision uint64
	Now                 time.Time
}

type RemoveMemberTxInput struct {
	ActorUserID          string
	TeamID               string
	MembershipID         string
	ExpectedAuthRevision uint64
	Idempotency          TeamsIdempotency
	Now                  time.Time
}

type LeaveTeamTxInput struct {
	ActorUserID          string
	TeamID               string
	ExpectedAuthRevision uint64
	Idempotency          TeamsIdempotency
	Now                  time.Time
}

type TransferOwnershipTxInput struct {
	ActorUserID          string
	TeamID               string
	TargetMembershipID   string
	ConfirmTeamName      string
	ExpectedAuthRevision uint64
	Idempotency          TeamsIdempotency
	Now                  time.Time
}

type DissolveTeamTxInput struct {
	ActorUserID          string
	TeamID               string
	ConfirmTeamName      string
	ExpectedAuthRevision uint64
	Idempotency          TeamsIdempotency
	Now                  time.Time
}

type QueueTeamExportTxInput struct {
	ActorUserID string
	Job         domain.TeamExportJob
	Idempotency TeamsIdempotency
	Now         time.Time
}

type UpdateTeamProfileTxInput struct {
	ActorUserID             string
	TeamID                  string
	Name                    *string
	Description             *string
	ExpectedProfileVersion  uint64
	Now                     time.Time
}

type TeamsStore interface {
	GetCurrentTeam(ctx context.Context, userID string) (*domain.UserCurrentTeam, *domain.Team, *domain.TeamMembership, error)
	GetTeam(ctx context.Context, teamID string) (*domain.Team, error)
	GetMembership(ctx context.Context, teamID, membershipID string) (*domain.TeamMembership, error)
	ListMembers(ctx context.Context, teamID string, query string, cursor string, limit int) ([]domain.TeamMembership, []domain.User, string, error)
	ListInvitationsForEmail(ctx context.Context, emailLookupHash [32]byte, cursor string, limit int) ([]domain.TeamInvitation, string, error)
	ListTeamInvitations(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamInvitation, string, error)
	GetInvitation(ctx context.Context, invitationID string) (*domain.TeamInvitation, error)
	ListInviteLinks(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamInviteLink, string, error)
	GetInviteLink(ctx context.Context, linkID string) (*domain.TeamInviteLink, error)
	GetInviteLinkByTokenHash(ctx context.Context, tokenHash [32]byte) (*domain.TeamInviteLink, error)
	GetMySharing(ctx context.Context, teamID, userID string) (*domain.TeamSharingState, error)
	ListAuditEvents(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamAuditEvent, string, error)
	ListExports(ctx context.Context, teamID, requesterUserID string) ([]domain.TeamExportJob, error)
	GetExport(ctx context.Context, teamID, exportID string) (*domain.TeamExportJob, error)
	GetAvatarObject(ctx context.Context, teamID, objectID string) (*domain.TeamUploadObject, error)
	HasOpenDeletionBarrier(ctx context.Context, teamID string) (bool, error)

	CreateTeamTx(ctx context.Context, in CreateTeamTxInput) (*CreateTeamTxResult, error)
	UpdateTeamProfileTx(ctx context.Context, in UpdateTeamProfileTxInput) (*domain.Team, error)
	CreateInvitationTx(ctx context.Context, in CreateInvitationTxInput) (*CreateInvitationTxResult, error)
	RevokeInvitationTx(ctx context.Context, actorUserID, teamID, invitationID string, expectedVersion uint64, idem TeamsIdempotency, now time.Time) error
	ResendInvitationTx(ctx context.Context, actorUserID, teamID, invitationID string, expectedVersion uint64, newInvitation domain.TeamInvitation, outbox domain.EmailOutbox, idem TeamsIdempotency, now time.Time) (*domain.TeamInvitation, error)
	AcceptInvitationTx(ctx context.Context, in AcceptInvitationTxInput) (*AcceptInvitationTxResult, error)
	CreateInviteLinkTx(ctx context.Context, in CreateInviteLinkTxInput) (*CreateInviteLinkTxResult, error)
	RevokeInviteLinkTx(ctx context.Context, in RevokeInviteLinkTxInput) error
	RegenerateInviteLinkTx(ctx context.Context, in RegenerateInviteLinkTxInput) (*CreateInviteLinkTxResult, error)
	AcceptInviteLinkTx(ctx context.Context, in AcceptInviteLinkTxInput) (*AcceptInviteLinkTxResult, error)
	UpdateSharingTx(ctx context.Context, in UpdateSharingTxInput) (*domain.TeamSharingState, error)
	ChangeMemberRoleTx(ctx context.Context, in ChangeMemberRoleTxInput) error
	RemoveMemberTx(ctx context.Context, in RemoveMemberTxInput) error
	LeaveTeamTx(ctx context.Context, in LeaveTeamTxInput) error
	TransferOwnershipTx(ctx context.Context, in TransferOwnershipTxInput) (*domain.TeamContext, error)
	DissolveTeamTx(ctx context.Context, in DissolveTeamTxInput) error
	QueueTeamExportTx(ctx context.Context, in QueueTeamExportTxInput) (*domain.TeamExportJob, error)

	CreateTeamAvatarUploadIntent(ctx context.Context, obj domain.TeamUploadObject) (*domain.TeamUploadObject, error)
	CompleteTeamAvatarUpload(ctx context.Context, teamID, objectID, actorUserID string, meta AvatarReadyMeta, expectedProfileVersion uint64, now time.Time) (*domain.Team, error)
	ClearTeamAvatar(ctx context.Context, teamID, actorUserID string, expectedProfileVersion uint64, now time.Time) error

	GetOrQueueAnalysis(ctx context.Context, teamID string, from, toExclusive time.Time, authRevision uint64, ruleVersion string, now time.Time) (*domain.TeamAnalysisSnapshot, bool, error)
	GetReadySnapshot(ctx context.Context, teamID, snapshotID string) (*domain.TeamAnalysisSnapshot, error)
	ListAnalysisRows(ctx context.Context, snapshotID string, generation uint64) ([]domain.TeamAnalysisRow, error)
	ClaimAnalysis(ctx context.Context, workerID string, lease time.Duration, now time.Time) (*domain.TeamAnalysisSnapshot, error)
	PublishAnalysis(ctx context.Context, snapshotID, leaseToken string, leaseGeneration, capturedAuth, capturedSource, publishedGeneration uint64, now time.Time) error
	MarkAnalysisFailed(ctx context.Context, snapshotID, leaseToken string, leaseGeneration uint64, errorCode string, now time.Time) error
	BumpSourceRevision(ctx context.Context, teamID string, now time.Time) (uint64, error)
	RegisterDeletionBarrier(ctx context.Context, deletionRequestID, teamID string, now time.Time) error
	ReleaseDeletionBarrier(ctx context.Context, deletionRequestID, teamID string, now time.Time) error
	ClaimTeamExport(ctx context.Context, workerID string, lease time.Duration, now time.Time) (*domain.TeamExportJob, error)
	CompleteTeamExport(ctx context.Context, exportID, leaseToken string, leaseGeneration uint64, objectKey string, sha256 [32]byte, size uint64, now time.Time) error
	FailTeamExport(ctx context.Context, exportID, leaseToken string, leaseGeneration uint64, errorCode string, now time.Time) error
}
