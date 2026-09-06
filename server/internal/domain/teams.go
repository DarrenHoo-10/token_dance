package domain

import (
	"strings"
	"time"
)

const (
	TeamIDPrefix           = "tem_"
	MembershipIDPrefix     = "tmb_"
	InvitationIDPrefix     = "tiv_"
	GrantIDPrefix          = "tgr_"
	SnapshotIDPrefix       = "tas_"
	ExportIDPrefix         = "tex_"
	InviteLinkIDPrefix     = "tln_"
	AuditIDPrefix          = "tau_"
	TeamObjectIDPrefix     = "tob_"
	CommandReceiptIDPrefix = "tcr_"
)

const (
	TeamNameMinGraphemes = 2
	TeamNameMaxGraphemes = 40
	TeamDescMaxGraphemes = 120
	TeamNameMaxUTF8Bytes = 1024
	TeamDescMaxUTF8Bytes = 4096
	InviteLinkTokenBytes = 32
	InviteLinkMaxUsesMin = 1
	InviteLinkMaxUsesMax = 100
	TeamAnalysisMaxDays  = 90
	JSONIDVersionString  = true
)

type TeamStatus string

const (
	TeamStatusActive    TeamStatus = "active"
	TeamStatusDissolved TeamStatus = "dissolved"
)

type TeamBaseRole string

const (
	TeamBaseRoleAdmin  TeamBaseRole = "admin"
	TeamBaseRoleMember TeamBaseRole = "member"
)

type TeamPublicRole string

const (
	TeamRoleOwner  TeamPublicRole = "owner"
	TeamRoleAdmin  TeamPublicRole = "admin"
	TeamRoleMember TeamPublicRole = "member"
)

type TeamMembershipEndReason string

const (
	TeamEndReasonLeft      TeamMembershipEndReason = "left"
	TeamEndReasonRemoved   TeamMembershipEndReason = "removed"
	TeamEndReasonDissolved TeamMembershipEndReason = "dissolved"
	TeamEndReasonDeleted   TeamMembershipEndReason = "account_deleted"
)

type SharingDimension string

const (
	SharingBase           SharingDimension = "base"
	SharingNamed          SharingDimension = "named"
	SharingClassification SharingDimension = "classification"
	SharingCost           SharingDimension = "cost"
)

type InvitationStatus string

const (
	InvitationPending  InvitationStatus = "pending"
	InvitationAccepted InvitationStatus = "accepted"
	InvitationRevoked  InvitationStatus = "revoked"
	InvitationExpired  InvitationStatus = "expired"
)

type InviteLinkStatus string

const (
	InviteLinkActive  InviteLinkStatus = "active"
	InviteLinkRevoked InviteLinkStatus = "revoked"
)

type InviteLinkEffectiveState string

const (
	InviteLinkStateActive     InviteLinkEffectiveState = "active"
	InviteLinkStateRevoked    InviteLinkEffectiveState = "revoked"
	InviteLinkStateExpired    InviteLinkEffectiveState = "expired"
	InviteLinkStateExhausted  InviteLinkEffectiveState = "exhausted"
)

type SnapshotStatus string

const (
	SnapshotQueued   SnapshotStatus = "queued"
	SnapshotBuilding SnapshotStatus = "building"
	SnapshotReady    SnapshotStatus = "ready"
	SnapshotObsolete SnapshotStatus = "obsolete"
	SnapshotFailed   SnapshotStatus = "failed"
	SnapshotExpired  SnapshotStatus = "expired"
)

type TeamExportStatus string

const (
	TeamExportQueued    TeamExportStatus = "queued"
	TeamExportRunning   TeamExportStatus = "running"
	TeamExportCompleted TeamExportStatus = "completed"
	TeamExportRevoked   TeamExportStatus = "revoked"
	TeamExportFailed    TeamExportStatus = "failed"
	TeamExportExpired   TeamExportStatus = "expired"
)

type TeamExportKind string

const (
	TeamExportDaily   TeamExportKind = "daily"
	TeamExportAgents  TeamExportKind = "agents"
	TeamExportModels  TeamExportKind = "models"
	TeamExportMembers TeamExportKind = "members"
)

type MetricValueState string

const (
	MetricAvailable   MetricValueState = "available"
	MetricEmpty       MetricValueState = "empty"
	MetricUnsupported MetricValueState = "unsupported"
	MetricNotShared   MetricValueState = "not_shared"
)

type SharingFlags struct {
	Base           bool `json:"base"`
	Named          bool `json:"named"`
	Classification bool `json:"classification"`
	Cost           bool `json:"cost"`
}

func (s SharingFlags) Valid() bool {
	if !s.Base && (s.Named || s.Classification || s.Cost) {
		return false
	}
	return true
}

func (s SharingFlags) DimensionEnabled(dim SharingDimension) bool {
	switch dim {
	case SharingBase:
		return s.Base
	case SharingNamed:
		return s.Named
	case SharingClassification:
		return s.Classification
	case SharingCost:
		return s.Cost
	default:
		return false
	}
}

type Team struct {
	TeamID          string     `json:"id"`
	Name            string     `json:"name"`
	Description     string     `json:"description"`
	TimezoneName    string     `json:"timezone"`
	OwnerUserID     string     `json:"-"`
	Status          TeamStatus `json:"status"`
	Visibility      string     `json:"visibility"`
	ProfileVersion  uint64     `json:"profileVersion"`
	AuthRevision    uint64     `json:"authRevision"`
	AvatarObjectID  *string    `json:"avatarObjectId,omitempty"`
	CreatedAt       time.Time  `json:"createdAt"`
	DissolvedAt     *time.Time `json:"dissolvedAt,omitempty"`
}

// FormatTeamCalendarDate returns the team-local calendar date for an instant.
func FormatTeamCalendarDate(t time.Time, timezone string) string {
	if strings.TrimSpace(timezone) == "" {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("2006-01-02")
}

func (t Team) PublicRoleFor(userID string, base TeamBaseRole) TeamPublicRole {
	if t.OwnerUserID == userID {
		return TeamRoleOwner
	}
	if base == TeamBaseRoleAdmin {
		return TeamRoleAdmin
	}
	return TeamRoleMember
}

type TeamMembership struct {
	MembershipID   string                   `json:"id"`
	TeamID         string                   `json:"teamId"`
	UserID         string                   `json:"userId"`
	BaseRole       TeamBaseRole             `json:"-"`
	SharingVersion uint64                   `json:"sharingVersion"`
	JoinedAt       time.Time                `json:"joinedAt"`
	EndedAt        *time.Time               `json:"endedAt,omitempty"`
	EndReason      *TeamMembershipEndReason `json:"endReason,omitempty"`
}

type UserCurrentTeam struct {
	UserID       string    `json:"userId"`
	TeamID       string    `json:"teamId"`
	MembershipID string    `json:"membershipId"`
	JoinedAt     time.Time `json:"joinedAt"`
}

type TeamSharingGrant struct {
	GrantID         string           `json:"grantId"`
	MembershipID    string           `json:"membershipId"`
	Dimension       SharingDimension `json:"dimension"`
	StartsAt        time.Time        `json:"startsAt"`
	EndsAt          *time.Time       `json:"endsAt,omitempty"`
	RevokedAt       *time.Time       `json:"revokedAt,omitempty"`
	ActiveDimension *string          `json:"-"`
}

type TeamInvitation struct {
	InvitationID          string           `json:"id"`
	TeamID                string           `json:"teamId"`
	InviterUserID         string           `json:"inviterUserId"`
	InvitedRole           TeamBaseRole     `json:"role"`
	RecipientLookupHash   [32]byte         `json:"-"`
	LookupKeyVersion      uint16           `json:"-"`
	RecipientCiphertext   []byte           `json:"-"`
	EncryptionKeyVersion  uint16           `json:"-"`
	Status                InvitationStatus `json:"status"`
	ActiveRecipientHash   *[32]byte        `json:"-"`
	CreatedAt             time.Time        `json:"createdAt"`
	ExpiresAt             time.Time        `json:"expiresAt"`
	AcceptedByUserID      *string          `json:"acceptedByUserId,omitempty"`
	AcceptedMembershipID  *string          `json:"acceptedMembershipId,omitempty"`
	Version               uint64           `json:"version"`
}

type TeamInviteLink struct {
	LinkID               string           `json:"id"`
	TeamID               string           `json:"teamId"`
	CreatorUserID        string           `json:"creatorUserId"`
	TokenHash            [32]byte         `json:"-"`
	TokenCiphertext      []byte           `json:"-"`
	EncryptionKeyVersion uint16           `json:"-"`
	Status               InviteLinkStatus `json:"status"`
	Version              uint64           `json:"version"`
	MaxUses              uint32           `json:"maxUses"`
	UsedCount            uint32           `json:"usedCount"`
	CreatedAt            time.Time        `json:"createdAt"`
	ExpiresAt            time.Time        `json:"expiresAt"`
	RevokedAt            *time.Time       `json:"revokedAt,omitempty"`
}

func (l TeamInviteLink) EffectiveState(now time.Time) InviteLinkEffectiveState {
	if l.Status == InviteLinkRevoked {
		return InviteLinkStateRevoked
	}
	if !l.ExpiresAt.After(now) {
		return InviteLinkStateExpired
	}
	if l.UsedCount >= l.MaxUses {
		return InviteLinkStateExhausted
	}
	return InviteLinkStateActive
}

type TeamInviteLinkJoin struct {
	LinkID       string    `json:"linkId"`
	UserID       string    `json:"userId"`
	MembershipID string    `json:"membershipId"`
	JoinedAt     time.Time `json:"joinedAt"`
}

type TeamCommandReceipt struct {
	ActorUserID        string    `json:"actorUserId"`
	OperationScope     string    `json:"operationScope"`
	IdempotencyKeyHash [32]byte  `json:"-"`
	RequestHash        [32]byte  `json:"-"`
	ResultType         string    `json:"resultType"`
	ResultID           string    `json:"resultId"`
	CreatedAt          time.Time `json:"createdAt"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

type TeamSourceRevision struct {
	TeamID         string    `json:"teamId"`
	SourceRevision uint64    `json:"sourceRevision"`
	ChangedAt      time.Time `json:"changedAt"`
}

type TeamAnalysisSnapshot struct {
	SnapshotID          string         `json:"id"`
	TeamID              string         `json:"teamId"`
	FromDate            time.Time      `json:"fromDate"`
	ToDateExclusive     time.Time      `json:"toDateExclusive"`
	AuthRevision        uint64         `json:"authRevision"`
	SourceRevision      uint64         `json:"sourceRevision"`
	RuleVersion         string         `json:"ruleVersion"`
	Status              SnapshotStatus `json:"status"`
	ActiveRequestKey    *string        `json:"-"`
	AsOf                time.Time      `json:"asOf"`
	LeaseToken          *string        `json:"-"`
	LeaseGeneration     uint64         `json:"-"`
	PublishedGeneration uint64         `json:"-"`
	LeaseExpiresAt      *time.Time     `json:"-"`
	AttemptCount        uint16         `json:"-"`
	NextAttemptAt       time.Time      `json:"-"`
	ErrorCode           *string        `json:"errorCode,omitempty"`
	ExpiresAt           time.Time      `json:"expiresAt"`
	Refreshing          bool           `json:"refreshing,omitempty"`
}

type TeamAnalysisRow struct {
	SnapshotID                 string    `json:"-"`
	BuildGeneration            uint64    `json:"-"`
	RowKey                     string    `json:"-"`
	MembershipID               *string   `json:"membershipId,omitempty"`
	MetricDate                 *string   `json:"metricDate,omitempty"`
	VisibilityMask             uint32    `json:"-"`
	AgentID                    *string   `json:"agentId,omitempty"`
	ProviderID                 *string   `json:"providerId,omitempty"`
	ModelID                    *string   `json:"modelId,omitempty"`
	Currency                   *string   `json:"currency,omitempty"`
	TokenExactTotal            string    `json:"tokenExactTotal"`
	TokenDerivedTotal          string    `json:"tokenDerivedTotal"`
	UsageEventCount            string    `json:"usageEventCount"`
	TokenSupportedEventCount   string    `json:"tokenSupportedEventCount"`
	ReportedCostAmount         string    `json:"reportedCostAmount"`
	EstimatedCostAmount        string    `json:"estimatedCostAmount"`
	ReportedCostEventCount     string    `json:"reportedCostEventCount"`
	EstimatedCostEventCount    string    `json:"estimatedCostEventCount"`
	ReportedCoveredUsageCount  string    `json:"reportedCoveredUsageCount"`
	EstimatedCoveredUsageCount string    `json:"estimatedCoveredUsageCount"`
	UnattributedCostCount      string    `json:"unattributedCostCount"`
	MaxReceivedAt              *time.Time `json:"maxReceivedAt,omitempty"`
}

type TeamExportJob struct {
	ExportID               string           `json:"id"`
	TeamID                 string           `json:"teamId"`
	RequesterUserID        string           `json:"requesterUserId"`
	RequesterMembershipID  string           `json:"requesterMembershipId"`
	SnapshotID             string           `json:"snapshotId"`
	AuthRevision           uint64           `json:"authRevision"`
	Kind                   TeamExportKind   `json:"kind"`
	FilterJSON             string           `json:"-"`
	FiltersHash            string           `json:"filtersHash"`
	Status                 TeamExportStatus `json:"status"`
	LeaseToken             *string          `json:"-"`
	LeaseGeneration        uint64           `json:"-"`
	LeaseExpiresAt         *time.Time       `json:"-"`
	AttemptCount           uint16           `json:"-"`
	NextAttemptAt          time.Time        `json:"-"`
	ObjectKey              *string          `json:"-"`
	FileSHA256             *[32]byte        `json:"-"`
	FileSize               *uint64          `json:"fileSize,omitempty"`
	CreatedAt              time.Time        `json:"createdAt"`
	ExpiresAt              time.Time        `json:"expiresAt"`
	ErrorCode              *string          `json:"errorCode,omitempty"`
}

type TeamAuditEvent struct {
	AuditID         string    `json:"id"`
	TeamID          string    `json:"teamId"`
	ActorUserID     *string   `json:"actorUserId,omitempty"`
	Action          string    `json:"action"`
	TargetType      string    `json:"targetType"`
	TargetID        string    `json:"targetId"`
	SafeDetailsJSON string    `json:"safeDetails"`
	CreatedAt       time.Time `json:"createdAt"`
}

type TeamUploadObject struct {
	ObjectID     string     `json:"id"`
	TeamID       string     `json:"teamId"`
	UploaderID   string     `json:"uploaderUserId"`
	ObjectKey    string     `json:"-"`
	ContentType  string     `json:"contentType"`
	ByteSize     uint64     `json:"byteSize"`
	ImageWidth   uint32     `json:"imageWidth"`
	ImageHeight  uint32     `json:"imageHeight"`
	SHA256       [32]byte   `json:"-"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
}

type TeamDeletionBarrier struct {
	DeletionRequestID string     `json:"deletionRequestId"`
	TeamID            string     `json:"teamId"`
	BlockedAt         time.Time  `json:"blockedAt"`
	ReleasedAt        *time.Time `json:"releasedAt,omitempty"`
}

type TeamPermissions struct {
	InviteMembers     bool `json:"inviteMembers"`
	AssignAdmins      bool `json:"assignAdmins"`
	EditProfile       bool `json:"editProfile"`
	ExportAnalytics   bool `json:"exportAnalytics"`
	TransferOwnership bool `json:"transferOwnership"`
	Dissolve          bool `json:"dissolve"`
	Leave             bool `json:"leave"`
}

func PermissionsFor(role TeamPublicRole) TeamPermissions {
	switch role {
	case TeamRoleOwner:
		return TeamPermissions{
			InviteMembers: true, AssignAdmins: true, EditProfile: true,
			ExportAnalytics: true, TransferOwnership: true, Dissolve: true,
		}
	case TeamRoleAdmin:
		return TeamPermissions{
			InviteMembers: true, EditProfile: true, ExportAnalytics: true, Leave: true,
		}
	default:
		return TeamPermissions{Leave: true}
	}
}

type TeamContext struct {
	Team        Team             `json:"team"`
	Membership  TeamMembership   `json:"membership"`
	Role        TeamPublicRole   `json:"role"`
	Permissions TeamPermissions  `json:"permissions"`
	AlreadyMember bool           `json:"alreadyMember,omitempty"`
}

type TeamSharingState struct {
	MembershipID    string        `json:"membershipId"`
	SharingVersion  uint64        `json:"sharingVersion"`
	Sharing         SharingFlags  `json:"sharing"`
	EffectiveFrom   map[string]time.Time `json:"effectiveFrom,omitempty"`
}

type DecimalMetric struct {
	Value string           `json:"value"`
	State MetricValueState `json:"state"`
}

type TeamCostAmount struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type TeamAnalysisResult struct {
	State      string               `json:"state"`
	Snapshot   *TeamAnalysisSnapshot `json:"snapshot,omitempty"`
	Range      *TeamAnalysisRange   `json:"range,omitempty"`
	Filters    TeamAnalysisFilters  `json:"filters,omitempty"`
	Summary    *TeamAnalysisSummary `json:"summary,omitempty"`
	Costs      *TeamAnalysisCosts   `json:"costs,omitempty"`
	Trend      []TeamTrendPoint     `json:"trend,omitempty"`
	Agents     *TeamPagedItems      `json:"agents,omitempty"`
	Models     *TeamPagedItems      `json:"models,omitempty"`
	Contributions *TeamPagedItems   `json:"contributions,omitempty"`
	Quality    *TeamAnalysisQuality `json:"quality,omitempty"`
	AuthRevision *string            `json:"authRevision,omitempty"`
	RetryAfterMs *int               `json:"retryAfterMs,omitempty"`
	MessageKey *string              `json:"messageKey,omitempty"`
}

type TeamAnalysisRange struct {
	Timezone         string    `json:"timezone"`
	From             time.Time `json:"from"`
	ToExclusive      time.Time `json:"toExclusive"`
	DataToExclusive  time.Time `json:"dataToExclusive"`
}

type TeamAnalysisFilters struct {
	Agent    *string `json:"agent"`
	Provider *string `json:"provider"`
	Model    *string `json:"model"`
}

type TeamAnalysisSummary struct {
	Tokens                 DecimalMetric `json:"tokens"`
	ActiveMembers          string        `json:"activeMembers"`
	CurrentMembers         string        `json:"currentMembers"`
	CurrentSharingMembers  string        `json:"currentSharingMembers"`
	Comparison             *string       `json:"comparison"`
	ComparisonReason       *string       `json:"comparisonReason,omitempty"`
}

type TeamAnalysisCosts struct {
	Reported           []TeamCostAmount `json:"reported"`
	EstimatedUncovered []TeamCostAmount `json:"estimatedUncovered"`
	Coverage           TeamCostCoverage `json:"coverage"`
	UnattributedCostCount string        `json:"unattributedCostCount"`
}

type TeamCostCoverage struct {
	ReportedUsageEvents string `json:"reportedUsageEvents"`
	EligibleUsageEvents string `json:"eligibleUsageEvents"`
}

type TeamTrendPoint struct {
	Date   string        `json:"date"`
	Tokens DecimalMetric `json:"tokens"`
}

type TeamPagedItems struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}

type TeamAnalysisQuality struct {
	UnsupportedEvents string `json:"unsupportedEvents"`
	EstimatedEvents   string `json:"estimatedEvents"`
}

type TeamFeatureFlags struct {
	Enabled         bool
	CreateEnabled   bool
	JoinEnabled     bool
	AnalysisEnabled bool
	ExportEnabled   bool
}
