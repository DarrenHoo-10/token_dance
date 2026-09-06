package teams

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"tokendance/internal/auth"
	"tokendance/internal/clock"
	"tokendance/internal/config"
	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/provider"
	"tokendance/internal/store"
)

const (
	teamJSONLimit          = 16 * 1024
	teamAvatarMaxBytes     = 2 * 1024 * 1024
	teamAvatarMaxEdge      = 4096
	defaultPageLimit       = 20
	maxPageLimit           = 100
	invitationTTL          = 7 * 24 * time.Hour
	inviteLinkDefaultDays  = 7
	inviteLinkDefaultUses  = 50
	analysisRetryAfterMS   = 2000
	avatarIntentTTL        = 10 * time.Minute
	timeJSON               = "2006-01-02T15:04:05.000Z"
	dateLayout             = "2006-01-02"
	invitationTemplateKey  = "teams.invitation"
	exportContentTypeCSV   = "text/csv; charset=utf-8"
)

type Service struct {
	teams   store.TeamsStore
	users   store.AuthStore
	cfg     *config.Config
	clk     clock.Clock
	auth    *auth.Service
	storage provider.ObjectStorage
}

func NewService(st store.Store, cfg *config.Config, clk clock.Clock, authSvc *auth.Service, storage provider.ObjectStorage) *Service {
	if clk == nil {
		clk = clock.RealClock{}
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if storage == nil {
		storage = provider.NewMemoryObjectStorage("")
	}
	return &Service{
		teams:   st.Teams(),
		users:   st.Auth(),
		cfg:     cfg,
		clk:     clk,
		auth:    authSvc,
		storage: storage,
	}
}

func (s *Service) Flags() domain.TeamFeatureFlags {
	return domain.TeamFeatureFlags{
		Enabled:         s.cfg.TeamsEnabled,
		CreateEnabled:   s.cfg.TeamsCreateEnabled,
		JoinEnabled:     s.cfg.TeamsJoinEnabled,
		AnalysisEnabled: s.cfg.TeamsAnalysisEnabled,
		ExportEnabled:   s.cfg.TeamsExportEnabled,
	}
}

func (s *Service) requireEnabled() error {
	if !s.cfg.TeamsEnabled {
		return errUnavailable("teams.disabled", "teams feature is disabled")
	}
	return nil
}

func (s *Service) requireCreate() error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	if !s.cfg.TeamsCreateEnabled {
		return errUnavailable("teams.createDisabled", "team creation is disabled")
	}
	return nil
}

func (s *Service) requireJoin() error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	if !s.cfg.TeamsJoinEnabled {
		return errUnavailable("teams.joinDisabled", "joining teams is disabled")
	}
	return nil
}

func (s *Service) requireAnalysis() error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	if !s.cfg.TeamsAnalysisEnabled {
		return errUnavailable("teams.analysisDisabled", "team analysis is disabled")
	}
	return nil
}

func (s *Service) requireExport() error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	if !s.cfg.TeamsExportEnabled {
		return errUnavailable("teams.exportDisabled", "team export is disabled")
	}
	return nil
}

type CreateTeamInput struct {
	Name        string
	Description string
	Timezone    string
	Sharing     *domain.SharingFlags
}

type PatchTeamInput struct {
	Name                   *string
	Description            *string
	ExpectedProfileVersion string
}

type ChangeRoleInput struct {
	Role                 string
	ExpectedAuthRevision string
}

type SharingPatchInput struct {
	ExpectedSharingVersion string
	Sharing                domain.SharingFlags
}

type CreateInvitationInput struct {
	Email string
	Role  string
}

type AcceptInvitationInput struct {
	ExpectedInvitationVersion string
	Sharing                   *domain.SharingFlags
}

type CreateInviteLinkInput struct {
	ExpiresInDays int
	MaxUses       int
}

type AcceptInviteLinkInput struct {
	Token               string
	ExpectedLinkVersion string
	Sharing             *domain.SharingFlags
}

type TransferInput struct {
	TargetMembershipID   string
	ConfirmTeamName      string
	ExpectedAuthRevision string
}

type DissolveInput struct {
	ConfirmTeamName      string
	ExpectedAuthRevision string
}

type CreateExportInput struct {
	SnapshotID string
	Kind       string
	Agent      *string
	Provider   *string
	Model      *string
}

type CreateAvatarIntentInput struct {
	ContentType string
	ByteSize    uint64
	Sha256      string
}

type AnalysisQuery struct {
	RangeKey   string
	From       string
	To         string
	Agent      string
	Provider   string
	Model      string
	SnapshotID string
	Collection string
	Cursor     string
	Limit      int
}

type MemberListQuery struct {
	Q          string
	Cursor     string
	Limit      int
	SnapshotID string
}

type PageQuery struct {
	Cursor string
	Limit  int
}

type TeamDTO struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	Timezone       string `json:"timezone"`
	Visibility     string `json:"visibility"`
	Status         string `json:"status"`
	ProfileVersion string `json:"profileVersion"`
	AuthRevision   string `json:"authRevision"`
}

type MembershipDTO struct {
	ID             string `json:"id"`
	Role           string `json:"role"`
	SharingVersion string `json:"sharingVersion"`
	JoinedAt       string `json:"joinedAt"`
}

type ContextDTO struct {
	Team          TeamDTO                 `json:"team"`
	Membership    MembershipDTO           `json:"membership"`
	Permissions   domain.TeamPermissions  `json:"permissions"`
	AlreadyMember bool                    `json:"alreadyMember,omitempty"`
}

type SharingDTO struct {
	MembershipID   string              `json:"membershipId"`
	SharingVersion string              `json:"sharingVersion"`
	Sharing        domain.SharingFlags `json:"sharing"`
	EffectiveFrom  map[string]string   `json:"effectiveFrom,omitempty"`
}

type InvitationTeamDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type InvitationDTO struct {
	ID              string `json:"id"`
	TeamID          string `json:"teamId,omitempty"`
	InvitedRole     string `json:"invitedRole"`
	RecipientMasked string `json:"recipientMasked,omitempty"`
	Status          string `json:"status"`
	Version         string `json:"version"`
	CreatedAt       string `json:"createdAt"`
	ExpiresAt       string `json:"expiresAt"`
	DeliveryState   string `json:"deliveryState,omitempty"`
}

type InboxInvitationDTO struct {
	ID                 string             `json:"id"`
	Team               InvitationTeamDTO  `json:"team"`
	InviterDisplayName string             `json:"inviterDisplayName"`
	InvitedRole        string             `json:"invitedRole"`
	CreatedAt          string             `json:"createdAt"`
	ExpiresAt          string             `json:"expiresAt"`
	Version            string             `json:"version"`
	Status             string             `json:"status"`
}

type InvitationPreviewDTO struct {
	ID                 string            `json:"id"`
	Team               InvitationTeamDTO `json:"team"`
	InviterDisplayName string            `json:"inviterDisplayName"`
	InvitedRole        string            `json:"invitedRole"`
	CreatedAt          string            `json:"createdAt,omitempty"`
	ExpiresAt          string            `json:"expiresAt"`
	Version            string            `json:"version"`
	Status             string            `json:"status"`
	CanAccept          bool              `json:"canAccept,omitempty"`
}

type InviteLinkDTO struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Role           string `json:"role"`
	ExpiresAt      string `json:"expiresAt"`
	MaxUses        string `json:"maxUses"`
	UsedCount      string `json:"usedCount"`
	EffectiveState string `json:"effectiveState"`
	CreatorUserID  string `json:"creatorUserId"`
	ShareURL       string `json:"shareUrl,omitempty"`
}

type InviteLinkPreviewDTO struct {
	TeamName              string `json:"teamName"`
	InviterDisplayName    string `json:"inviterDisplayName"`
	Role                  string `json:"role"`
	ExpiresAt             string `json:"expiresAt"`
	LinkVersion           string `json:"linkVersion"`
	EffectiveState        string `json:"effectiveState"`
	AlreadyMember         bool   `json:"alreadyMember"`
	ReinvitationRequired  bool   `json:"reinvitationRequired"`
	CanJoin               bool   `json:"canJoin"`
}

type MemberDTO struct {
	MembershipID   string                `json:"membershipId"`
	UserID         string                `json:"userId"`
	DisplayName    string                `json:"displayName"`
	Handle         *string               `json:"handle"`
	Role           string                `json:"role"`
	JoinedAt       string                `json:"joinedAt"`
	Sharing        domain.SharingFlags   `json:"sharing"`
	SyncStatus     string                `json:"syncStatus"`
	LastReceivedAt *string               `json:"lastReceivedAt,omitempty"`
	PeriodTokens   *domain.DecimalMetric `json:"periodTokens,omitempty"`
	CanOpenDetail  bool                  `json:"canOpenDetail"`
}

type ExportDTO struct {
	ID           string  `json:"id"`
	TeamID       string  `json:"teamId"`
	SnapshotID   string  `json:"snapshotId"`
	Kind         string  `json:"kind"`
	Status       string  `json:"status"`
	AuthRevision string  `json:"authRevision"`
	FiltersHash  string  `json:"filtersHash"`
	CreatedAt    string  `json:"createdAt"`
	ExpiresAt    string  `json:"expiresAt"`
	FileSize     *string `json:"fileSize,omitempty"`
	ErrorCode    *string `json:"errorCode,omitempty"`
}

type AvatarIntentDTO struct {
	ObjectID  string `json:"objectId"`
	ExpiresAt string `json:"expiresAt"`
}

type AuditDTO struct {
	ID          string `json:"id"`
	Action      string `json:"action"`
	TargetType  string `json:"targetType"`
	TargetID    string `json:"targetId"`
	SafeDetails string `json:"safeDetails"`
	CreatedAt   string `json:"createdAt"`
}

type FilterOptionsDTO struct {
	SnapshotID string              `json:"snapshotId"`
	Agents     []map[string]string `json:"agents"`
	Providers  []map[string]string `json:"providers"`
	Models     []map[string]string `json:"models"`
}

type AnalysisDTO struct {
	State        string                     `json:"state"`
	Snapshot     *snapshotDTO               `json:"snapshot,omitempty"`
	Range        *domain.TeamAnalysisRange  `json:"range,omitempty"`
	Filters      domain.TeamAnalysisFilters `json:"filters,omitempty"`
	Summary      *domain.TeamAnalysisSummary `json:"summary,omitempty"`
	Costs        *domain.TeamAnalysisCosts  `json:"costs,omitempty"`
	Trend        []domain.TeamTrendPoint    `json:"trend,omitempty"`
	Agents       *domain.TeamPagedItems     `json:"agents,omitempty"`
	Models       *domain.TeamPagedItems     `json:"models,omitempty"`
	Contributions *domain.TeamPagedItems    `json:"contributions,omitempty"`
	Quality      *domain.TeamAnalysisQuality `json:"quality,omitempty"`
	FiltersHash  string                     `json:"filtersHash,omitempty"`
	AuthRevision *string                    `json:"authRevision,omitempty"`
	RetryAfterMs *int                       `json:"retryAfterMs,omitempty"`
	MessageKey   *string                    `json:"messageKey,omitempty"`
}

type snapshotDTO struct {
	ID             string `json:"id"`
	AuthRevision   string `json:"authRevision"`
	SourceRevision string `json:"sourceRevision"`
	RuleVersion    string `json:"ruleVersion"`
	AsOf           string `json:"asOf"`
	Refreshing     bool   `json:"refreshing"`
}

type ExportContent struct {
	Filename    string
	ContentType string
	Body        io.ReadCloser
	Size        int64
}

type AvatarContent struct {
	ContentType string
	Body        []byte
}

func (s *Service) GetMyTeam(ctx context.Context, userID string) (*ContextDTO, bool, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, false, err
	}
	_, team, mem, err := s.teams.GetCurrentTeam(ctx, userID)
	if err != nil {
		if isNotFound(err) {
			return nil, false, nil
		}
		return nil, false, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if team == nil || mem == nil || team.Status != domain.TeamStatusActive {
		return nil, false, nil
	}
	return contextDTO(team, mem, false), true, nil
}

func (s *Service) ListMyInvitations(ctx context.Context, user *domain.User, q PageQuery) ([]InboxInvitationDTO, string, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, "", err
	}
	hash, err := s.verifiedEmailHash(user)
	if err != nil {
		return []InboxInvitationDTO{}, "", nil
	}
	items, next, err := s.teams.ListInvitationsForEmail(ctx, hash, q.Cursor, clampLimit(q.Limit))
	if err != nil {
		return nil, "", mapStoreError(err, "TEAM_INVITATION_NOT_FOUND")
	}
	out := make([]InboxInvitationDTO, 0, len(items))
	for i := range items {
		dto, err := s.inboxInvitationDTO(ctx, items[i])
		if err != nil {
			return nil, "", err
		}
		out = append(out, dto)
	}
	return out, next, nil
}

func (s *Service) CreateTeam(ctx context.Context, user *domain.User, in CreateTeamInput, idemKey string) (*ContextDTO, error) {
	if err := s.requireCreate(); err != nil {
		return nil, err
	}
	if err := requireActiveVerified(user); err != nil {
		return nil, err
	}
	name, err := NormalizeTeamName(in.Name)
	if err != nil {
		return nil, err
	}
	desc, err := NormalizeTeamDescription(in.Description)
	if err != nil {
		return nil, err
	}
	tz, err := validateTimezone(in.Timezone)
	if err != nil {
		return nil, err
	}
	sharing, err := normalizeSharing(in.Sharing)
	if err != nil {
		return nil, err
	}
	idem, err := s.parseIdempotency("teams.create", idemKey, canonicalJSON(map[string]any{
		"name": name, "description": desc, "timezone": tz, "sharing": sharing,
	}))
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	teamID, err := NewPrefixedID(domain.TeamIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	memID, err := NewPrefixedID(domain.MembershipIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	team := domain.Team{
		TeamID: teamID, Name: name, Description: desc, TimezoneName: tz,
		OwnerUserID: user.UserID, Status: domain.TeamStatusActive, Visibility: "private",
		ProfileVersion: 1, AuthRevision: 1, CreatedAt: now,
	}
	mem := domain.TeamMembership{
		MembershipID: memID, TeamID: teamID, UserID: user.UserID,
		BaseRole: domain.TeamBaseRoleAdmin, SharingVersion: 1, JoinedAt: now,
	}
	res, err := s.teams.CreateTeamTx(ctx, store.CreateTeamTxInput{
		ActorUserID: user.UserID, Team: team, Membership: mem, Sharing: sharing, Idempotency: idem, Now: now,
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if res != nil && res.Context != nil {
		return contextDTO(&res.Context.Team, &res.Context.Membership, res.Outcome == store.TeamsTxAlreadyMember), nil
	}
	return contextDTO(&team, &mem, false), nil
}

func (s *Service) GetTeam(ctx context.Context, userID, teamID string) (*ContextDTO, error) {
	team, mem, _, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	return contextDTO(team, mem, false), nil
}

func (s *Service) PatchTeam(ctx context.Context, userID, teamID string, in PatchTeamInput) (*ContextDTO, error) {
	team, mem, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).EditProfile {
		return nil, errPermissionDenied()
	}
	version, err := parseVersion(in.ExpectedProfileVersion, "expectedProfileVersion")
	if err != nil {
		return nil, err
	}
	var name, desc *string
	if in.Name != nil {
		n, err := NormalizeTeamName(*in.Name)
		if err != nil {
			return nil, err
		}
		name = &n
	}
	if in.Description != nil {
		d, err := NormalizeTeamDescription(*in.Description)
		if err != nil {
			return nil, err
		}
		desc = &d
	}
	updated, err := s.teams.UpdateTeamProfileTx(ctx, store.UpdateTeamProfileTxInput{
		ActorUserID: userID, TeamID: teamID, Name: name, Description: desc,
		ExpectedProfileVersion: version, Now: s.clk.Now(),
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if updated != nil {
		team = updated
	}
	return contextDTO(team, mem, false), nil
}

func (s *Service) ListMembers(ctx context.Context, userID, teamID string, q MemberListQuery) ([]MemberDTO, string, error) {
	team, _, _, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, "", err
	}
	members, users, next, err := s.teams.ListMembers(ctx, teamID, q.Q, q.Cursor, clampLimit(q.Limit))
	if err != nil {
		return nil, "", mapStoreError(err, "TEAM_NOT_FOUND")
	}
	userByID := map[string]domain.User{}
	for i := range users {
		userByID[users[i].UserID] = users[i]
	}
	var tokenByMem map[string]string
	var receivedByMem map[string]time.Time
	if q.SnapshotID != "" {
		if err := s.requireAnalysis(); err != nil {
			return nil, "", err
		}
		rows, err := s.rowsForSnapshot(ctx, team, q.SnapshotID)
		if err != nil {
			return nil, "", err
		}
		tokenByMem = memberTokenTotals(rows)
		receivedByMem = memberLastReceived(rows)
	}
	out := make([]MemberDTO, 0, len(members))
	now := s.clk.Now()
	for _, m := range members {
		u := userByID[m.UserID]
		sharing := domain.SharingFlags{}
		if st, err := s.teams.GetMySharing(ctx, teamID, m.UserID); err == nil && st != nil {
			sharing = st.Sharing
		}
		item := MemberDTO{
			MembershipID: m.MembershipID, UserID: m.UserID, DisplayName: u.DisplayName, Handle: u.Handle,
			Role: string(team.PublicRoleFor(m.UserID, m.BaseRole)),
			JoinedAt: formatTime(m.JoinedAt), Sharing: sharing, CanOpenDetail: sharing.Named,
		}
		if received, ok := receivedByMem[m.MembershipID]; ok {
			formatted := formatTime(received)
			item.LastReceivedAt = &formatted
		}
		item.SyncStatus = memberSyncStatus(sharing, item.LastReceivedAt, now)
		if tokenByMem != nil {
			item.PeriodTokens = memberPeriodMetric(sharing, tokenByMem[m.MembershipID])
		}
		out = append(out, item)
	}
	return out, next, nil
}

func (s *Service) GetMember(ctx context.Context, userID, teamID, membershipID, snapshotID string) (map[string]any, error) {
	team, _, _, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(snapshotID) == "" {
		return nil, fieldError("snapshotId", "teams.snapshotRequired", "snapshotId is required")
	}
	if err := s.requireAnalysis(); err != nil {
		return nil, err
	}
	target, err := s.teams.GetMembership(ctx, teamID, membershipID)
	if err != nil || target == nil || target.TeamID != teamID {
		return nil, errResourceNotFound()
	}
	rows, err := s.rowsForSnapshot(ctx, team, snapshotID)
	if err != nil {
		return nil, err
	}
	memberRows := rowsForMembership(rows, membershipID)
	named := false
	for _, row := range memberRows {
		if row.VisibilityMask&VisibilityNamed != 0 {
			named = true
			break
		}
	}
	if !named {
		return nil, errPermissionDenied()
	}
	u, _ := s.users.FindUserByID(ctx, target.UserID)
	sharing := domain.SharingFlags{}
	if st, err := s.teams.GetMySharing(ctx, teamID, target.UserID); err == nil && st != nil {
		sharing = st.Sharing
	}
	snap, err := s.teams.GetReadySnapshot(ctx, teamID, snapshotID)
	if err != nil || snap == nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	return assembleMemberDetail(target, u, team, snap, memberRows, sharing), nil
}

func (s *Service) ChangeMemberRole(ctx context.Context, userID, teamID, membershipID string, in ChangeRoleInput) (*ContextDTO, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).AssignAdmins {
		return nil, errPermissionDenied()
	}
	newRole, err := parseBaseRole(in.Role)
	if err != nil {
		return nil, err
	}
	rev, err := parseVersion(in.ExpectedAuthRevision, "expectedAuthRevision")
	if err != nil {
		return nil, err
	}
	if err := s.teams.ChangeMemberRoleTx(ctx, store.ChangeMemberRoleTxInput{
		ActorUserID: userID, TeamID: teamID, MembershipID: membershipID,
		NewRole: newRole, ExpectedAuthRevision: rev, Now: s.clk.Now(),
	}); err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	return s.GetTeam(ctx, userID, teamID)
}

func (s *Service) RemoveMember(ctx context.Context, userID, teamID, membershipID, expectedAuth, idemKey string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if role == domain.TeamRoleMember {
		return errPermissionDenied()
	}
	rev, err := parseVersion(expectedAuth, "expectedAuthRevision")
	if err != nil {
		return err
	}
	idem, err := s.parseIdempotency("teams.remove:"+teamID+":"+membershipID, idemKey, membershipID+":"+expectedAuth)
	if err != nil {
		return err
	}
	if err := s.teams.RemoveMemberTx(ctx, store.RemoveMemberTxInput{
		ActorUserID: userID, TeamID: teamID, MembershipID: membershipID,
		ExpectedAuthRevision: rev, Idempotency: idem, Now: s.clk.Now(),
	}); err != nil {
		return mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	return nil
}

func (s *Service) ListInvitations(ctx context.Context, userID, teamID string, q PageQuery) ([]InvitationDTO, string, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, "", err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return nil, "", errPermissionDenied()
	}
	items, next, err := s.teams.ListTeamInvitations(ctx, teamID, q.Cursor, clampLimit(q.Limit))
	if err != nil {
		return nil, "", mapStoreError(err, "TEAM_NOT_FOUND")
	}
	out := make([]InvitationDTO, 0, len(items))
	for i := range items {
		out = append(out, s.invitationDTOMasked(items[i], "pending"))
	}
	return out, next, nil
}

func (s *Service) CreateInvitation(ctx context.Context, user *domain.User, teamID string, in CreateInvitationInput, idemKey string) (*InvitationDTO, error) {
	team, _, role, err := s.requireMember(ctx, user.UserID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return nil, errPermissionDenied()
	}
	invitedRole := domain.TeamBaseRoleMember
	if strings.TrimSpace(in.Role) != "" {
		invitedRole, err = parseBaseRole(in.Role)
		if err != nil {
			return nil, err
		}
	}
	if invitedRole == domain.TeamBaseRoleAdmin && !domain.PermissionsFor(role).AssignAdmins {
		return nil, errPermissionDenied()
	}
	normalized, err := auth.NormalizeEmail(in.Email)
	if err != nil {
		return nil, fieldError("email", "teams.invalidEmail", "invalid invitation email")
	}
	if s.auth == nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "invitation crypto is unavailable")
	}
	invitationID, err := NewPrefixedID(domain.InvitationIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	now := s.clk.Now()
	lookup := s.auth.ComputeEmailLookupHash(normalized)
	cipher := s.auth.Cipher()
	if cipher == nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "invitation crypto is unavailable")
	}
	recipientCT, err := cipher.Encrypt([]byte(normalized), invitationEmailAAD(invitationID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to encrypt invitation recipient")
	}
	payload := map[string]any{
		"invitationId": invitationID, "role": string(invitedRole), "teamName": team.Name,
		"inviterDisplayName": user.DisplayName, "url": s.invitationURL(invitationID),
		"expiresAt": now.Add(invitationTTL).UTC().Format(timeJSON),
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadCT, err := cipher.Encrypt(payloadJSON, invitationPayloadAAD(invitationID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to encrypt invitation payload")
	}
	emailID := "eml_"
	if tok, e := crypto.GenerateOpaqueToken(13); e == nil {
		emailID += tok
	}
	inv := domain.TeamInvitation{
		InvitationID: invitationID, TeamID: teamID, InviterUserID: user.UserID, InvitedRole: invitedRole,
		RecipientLookupHash: lookup, LookupKeyVersion: s.cfg.EmailLookupKeys.CurrentVersion,
		RecipientCiphertext: recipientCT, EncryptionKeyVersion: cipher.KeyVersion(),
		Status: domain.InvitationPending, ActiveRecipientHash: &lookup,
		CreatedAt: now, ExpiresAt: now.Add(invitationTTL), Version: 1,
	}
	var invVer uint64 = 1
	outbox := domain.EmailOutbox{
		EmailID: emailID, TeamInvitationID: &invitationID, TeamInvitationVersion: &invVer,
		IdempotencyKey: crypto.SHA256([]byte("teams.invitation:" + invitationID)),
		TemplateKey: invitationTemplateKey, Locale: localeOrDefault(user.Locale),
		RecipientCiphertext: recipientCT, PayloadCiphertext: payloadCT,
		EncryptionKeyVersion: cipher.KeyVersion(), DeliveryStatus: "pending",
		NextAttemptAt: now, ExpiresAt: inv.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	idem, err := s.parseIdempotency("teams.invite:"+teamID, idemKey, canonicalJSON(map[string]any{
		"email": normalized, "role": string(invitedRole),
	}))
	if err != nil {
		return nil, err
	}
	res, err := s.teams.CreateInvitationTx(ctx, store.CreateInvitationTxInput{
		ActorUserID: user.UserID, TeamID: teamID, InvitedRole: invitedRole,
		Invitation: inv, Outbox: outbox, Idempotency: idem, Now: now,
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	created := &inv
	if res != nil && res.Invitation != nil {
		created = res.Invitation
	}
	dto := invitationDTO(*created, "pending")
	dto.RecipientMasked = maskEmail(normalized)
	return &dto, nil
}

func (s *Service) RevokeInvitation(ctx context.Context, userID, teamID, invitationID, expectedVersion, idemKey string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return errPermissionDenied()
	}
	ver, err := parseVersion(expectedVersion, "expectedInvitationVersion")
	if err != nil {
		return err
	}
	idem, err := s.parseIdempotency("teams.invite.revoke:"+invitationID, idemKey, invitationID+":"+expectedVersion)
	if err != nil {
		return err
	}
	if err := s.teams.RevokeInvitationTx(ctx, userID, teamID, invitationID, ver, idem, s.clk.Now()); err != nil {
		return mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	return nil
}

func (s *Service) ResendInvitation(ctx context.Context, user *domain.User, teamID, invitationID, expectedVersion, idemKey string) (*InvitationDTO, error) {
	team, _, role, err := s.requireMember(ctx, user.UserID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return nil, errPermissionDenied()
	}
	ver, err := parseVersion(expectedVersion, "expectedInvitationVersion")
	if err != nil {
		return nil, err
	}
	old, err := s.teams.GetInvitation(ctx, invitationID)
	if err != nil || old == nil || old.TeamID != teamID {
		return nil, errResourceNotFound()
	}
	if old.InvitedRole == domain.TeamBaseRoleAdmin && !domain.PermissionsFor(role).AssignAdmins {
		return nil, errPermissionDenied()
	}
	if s.auth == nil || s.auth.Cipher() == nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "invitation crypto is unavailable")
	}
	newID, err := NewPrefixedID(domain.InvitationIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	now := s.clk.Now()
	cipher := s.auth.Cipher()
	plain, err := cipher.Decrypt(old.RecipientCiphertext, invitationEmailAAD(old.InvitationID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to decrypt invitation recipient")
	}
	recipientCT, err := cipher.Encrypt(plain, invitationEmailAAD(newID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to encrypt invitation recipient")
	}
	payload := map[string]any{
		"invitationId": newID, "role": string(old.InvitedRole), "teamName": team.Name,
		"inviterDisplayName": user.DisplayName, "url": s.invitationURL(newID),
		"expiresAt": now.Add(invitationTTL).UTC().Format(timeJSON),
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadCT, err := cipher.Encrypt(payloadJSON, invitationPayloadAAD(newID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to encrypt invitation payload")
	}
	emailID := "eml_"
	if tok, e := crypto.GenerateOpaqueToken(13); e == nil {
		emailID += tok
	}
	lookup := old.RecipientLookupHash
	inv := domain.TeamInvitation{
		InvitationID: newID, TeamID: teamID, InviterUserID: user.UserID, InvitedRole: old.InvitedRole,
		RecipientLookupHash: lookup, LookupKeyVersion: old.LookupKeyVersion,
		RecipientCiphertext: recipientCT, EncryptionKeyVersion: cipher.KeyVersion(),
		Status: domain.InvitationPending, ActiveRecipientHash: &lookup,
		CreatedAt: now, ExpiresAt: now.Add(invitationTTL), Version: 1,
	}
	var invVer uint64 = 1
	outbox := domain.EmailOutbox{
		EmailID: emailID, TeamInvitationID: &newID, TeamInvitationVersion: &invVer,
		IdempotencyKey: crypto.SHA256([]byte("teams.invitation.resend:" + newID)),
		TemplateKey: invitationTemplateKey, Locale: localeOrDefault(user.Locale),
		RecipientCiphertext: recipientCT, PayloadCiphertext: payloadCT,
		EncryptionKeyVersion: cipher.KeyVersion(), DeliveryStatus: "pending",
		NextAttemptAt: now, ExpiresAt: inv.ExpiresAt, CreatedAt: now, UpdatedAt: now,
	}
	idem, err := s.parseIdempotency("teams.invite.resend:"+invitationID, idemKey, invitationID+":"+expectedVersion)
	if err != nil {
		return nil, err
	}
	created, err := s.teams.ResendInvitationTx(ctx, user.UserID, teamID, invitationID, ver, inv, outbox, idem, now)
	if err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	if created == nil {
		created = &inv
	}
	dto := s.invitationDTOMasked(*created, "pending")
	return &dto, nil
}

func (s *Service) PreviewInvitation(ctx context.Context, user *domain.User, invitationID string) (*InvitationPreviewDTO, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	hash, err := s.verifiedEmailHash(user)
	if err != nil {
		return nil, errInvitationNotFound()
	}
	inv, err := s.teams.GetInvitation(ctx, invitationID)
	if err != nil || inv == nil || inv.RecipientLookupHash != hash {
		return nil, errInvitationNotFound()
	}
	now := s.clk.Now()
	status := inv.Status
	if status == domain.InvitationPending && !inv.ExpiresAt.After(now) {
		status = domain.InvitationExpired
	}
	if status == domain.InvitationExpired {
		return nil, domain.NewAppError(410, "TEAM_INVITATION_EXPIRED", "teams.invitationExpired", "invitation expired", nil, domain.ErrNotFound)
	}
	if status == domain.InvitationRevoked {
		return nil, domain.NewAppError(410, "TEAM_INVITATION_REVOKED", "teams.invitationRevoked", "invitation revoked", nil, domain.ErrNotFound)
	}
	team, err := s.teams.GetTeam(ctx, inv.TeamID)
	if err != nil || team == nil || team.Status != domain.TeamStatusActive {
		return nil, errInvitationNotFound()
	}
	inviterName := ""
	if u, e := s.users.FindUserByID(ctx, inv.InviterUserID); e == nil && u != nil {
		inviterName = u.DisplayName
	}
	return &InvitationPreviewDTO{
		ID: inv.InvitationID,
		Team: InvitationTeamDTO{ID: team.TeamID, Name: team.Name, Description: team.Description},
		InviterDisplayName: inviterName,
		InvitedRole: string(inv.InvitedRole), Status: string(status), Version: formatUint(inv.Version),
		CreatedAt: formatTime(inv.CreatedAt), ExpiresAt: formatTime(inv.ExpiresAt),
		CanAccept: status == domain.InvitationPending,
	}, nil
}

func (s *Service) AcceptInvitation(ctx context.Context, user *domain.User, invitationID string, in AcceptInvitationInput, idemKey string) (*ContextDTO, error) {
	if err := s.requireJoin(); err != nil {
		return nil, err
	}
	if err := requireActiveVerified(user); err != nil {
		return nil, err
	}
	hash, err := s.verifiedEmailHash(user)
	if err != nil {
		return nil, errInvitationNotFound()
	}
	ver, err := parseVersion(in.ExpectedInvitationVersion, "expectedInvitationVersion")
	if err != nil {
		return nil, err
	}
	sharing, err := normalizeSharing(in.Sharing)
	if err != nil {
		return nil, err
	}
	idem, err := s.parseIdempotency("teams.invite.accept:"+invitationID, idemKey, canonicalJSON(map[string]any{
		"version": ver, "sharing": sharing,
	}))
	if err != nil {
		return nil, err
	}
	res, err := s.teams.AcceptInvitationTx(ctx, store.AcceptInvitationTxInput{
		ActorUserID: user.UserID, InvitationID: invitationID, ExpectedVersion: ver,
		Sharing: sharing, VerifiedEmailLookupHash: hash, Idempotency: idem, Now: s.clk.Now(),
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_INVITATION_NOT_FOUND")
	}
	if res == nil || res.Context == nil {
		return nil, errUnavailable("teams.unavailable", "accept invitation did not return a team context")
	}
	return contextDTO(&res.Context.Team, &res.Context.Membership, res.Outcome == store.TeamsTxAlreadyMember), nil
}

func (s *Service) ListInviteLinks(ctx context.Context, userID, teamID string, q PageQuery) ([]InviteLinkDTO, string, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, "", err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return nil, "", errPermissionDenied()
	}
	items, next, err := s.teams.ListInviteLinks(ctx, teamID, q.Cursor, clampLimit(q.Limit))
	if err != nil {
		return nil, "", mapStoreError(err, "TEAM_NOT_FOUND")
	}
	now := s.clk.Now()
	out := make([]InviteLinkDTO, 0, len(items))
	for i := range items {
		out = append(out, inviteLinkDTO(items[i], now, ""))
	}
	return out, next, nil
}

func (s *Service) CreateInviteLink(ctx context.Context, userID, teamID string, in CreateInviteLinkInput, idemKey string) (*InviteLinkDTO, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return nil, errPermissionDenied()
	}
	days, uses, err := normalizeInviteLinkParams(in.ExpiresInDays, in.MaxUses)
	if err != nil {
		return nil, err
	}
	token, raw, err := NewInviteToken()
	if err != nil {
		return nil, errInternal(err)
	}
	linkID, err := NewPrefixedID(domain.InviteLinkIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	if s.auth == nil || s.auth.Cipher() == nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "invite link crypto is unavailable")
	}
	now := s.clk.Now()
	ct, err := s.auth.Cipher().Encrypt(raw, inviteLinkAAD(teamID, linkID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to encrypt invite token")
	}
	link := domain.TeamInviteLink{
		LinkID: linkID, TeamID: teamID, CreatorUserID: userID,
		TokenHash: HashInviteToken(raw), TokenCiphertext: ct,
		EncryptionKeyVersion: s.auth.Cipher().KeyVersion(),
		Status: domain.InviteLinkActive, Version: 1,
		MaxUses: uint32(uses), UsedCount: 0, CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour),
	}
	idem, err := s.parseIdempotency("teams.invite-link.create:"+teamID, idemKey, canonicalJSON(map[string]any{
		"expiresInDays": days, "maxUses": uses,
	}))
	if err != nil {
		return nil, err
	}
	res, err := s.teams.CreateInviteLinkTx(ctx, store.CreateInviteLinkTxInput{
		ActorUserID: userID, TeamID: teamID, Link: link, Idempotency: idem, Now: now,
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	created := &link
	if res != nil && res.Link != nil {
		created = res.Link
	}
	shareToken, err := s.inviteLinkResponseToken(res, token)
	if err != nil {
		return nil, err
	}
	if created.EffectiveState(now) != domain.InviteLinkStateActive {
		return nil, inviteLinkGone(created.EffectiveState(now))
	}
	dto := inviteLinkDTO(*created, now, s.shareURL(created.LinkID, shareToken))
	return &dto, nil
}

func (s *Service) InviteLinkShareURL(ctx context.Context, userID, teamID, linkID string) (string, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return "", err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return "", errPermissionDenied()
	}
	link, err := s.teams.GetInviteLink(ctx, linkID)
	if err != nil || link == nil || link.TeamID != teamID {
		return "", errInviteLinkNotFound()
	}
	now := s.clk.Now()
	if link.EffectiveState(now) != domain.InviteLinkStateActive {
		return "", inviteLinkGone(link.EffectiveState(now))
	}
	token, err := s.decryptInviteToken(link)
	if err != nil {
		return "", err
	}
	return s.shareURL(link.LinkID, token), nil
}

func (s *Service) RevokeInviteLink(ctx context.Context, userID, teamID, linkID, expectedVersion, idemKey string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return errPermissionDenied()
	}
	ver, err := parseVersion(expectedVersion, "expectedLinkVersion")
	if err != nil {
		return err
	}
	idem, err := s.parseIdempotency("teams.invite-link.revoke:"+linkID, idemKey, linkID+":"+expectedVersion)
	if err != nil {
		return err
	}
	if err := s.teams.RevokeInviteLinkTx(ctx, store.RevokeInviteLinkTxInput{
		ActorUserID: userID, TeamID: teamID, LinkID: linkID, ExpectedVersion: ver, Idempotency: idem, Now: s.clk.Now(),
	}); err != nil {
		return mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	return nil
}

func (s *Service) RegenerateInviteLink(ctx context.Context, userID, teamID, linkID string, in CreateInviteLinkInput, expectedVersion, idemKey string) (*InviteLinkDTO, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).InviteMembers {
		return nil, errPermissionDenied()
	}
	ver, err := parseVersion(expectedVersion, "expectedLinkVersion")
	if err != nil {
		return nil, err
	}
	days, uses, err := normalizeInviteLinkParams(in.ExpiresInDays, in.MaxUses)
	if err != nil {
		return nil, err
	}
	token, raw, err := NewInviteToken()
	if err != nil {
		return nil, errInternal(err)
	}
	newID, err := NewPrefixedID(domain.InviteLinkIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	if s.auth == nil || s.auth.Cipher() == nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "invite link crypto is unavailable")
	}
	now := s.clk.Now()
	ct, err := s.auth.Cipher().Encrypt(raw, inviteLinkAAD(teamID, newID))
	if err != nil {
		return nil, errUnavailable("teams.cryptoUnavailable", "failed to encrypt invite token")
	}
	newLink := domain.TeamInviteLink{
		LinkID: newID, TeamID: teamID, CreatorUserID: userID,
		TokenHash: HashInviteToken(raw), TokenCiphertext: ct,
		EncryptionKeyVersion: s.auth.Cipher().KeyVersion(),
		Status: domain.InviteLinkActive, Version: 1,
		MaxUses: uint32(uses), CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour),
	}
	idem, err := s.parseIdempotency("teams.invite-link.regenerate:"+linkID, idemKey, canonicalJSON(map[string]any{
		"linkId": linkID, "version": ver, "expiresInDays": days, "maxUses": uses,
	}))
	if err != nil {
		return nil, err
	}
	res, err := s.teams.RegenerateInviteLinkTx(ctx, store.RegenerateInviteLinkTxInput{
		ActorUserID: userID, TeamID: teamID, LinkID: linkID, ExpectedVersion: ver,
		NewLink: newLink, Idempotency: idem, Now: now,
	})
	if err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	created := &newLink
	if res != nil && res.Link != nil {
		created = res.Link
	}
	shareToken, err := s.inviteLinkResponseToken(res, token)
	if err != nil {
		return nil, err
	}
	if created.EffectiveState(now) != domain.InviteLinkStateActive {
		return nil, inviteLinkGone(created.EffectiveState(now))
	}
	dto := inviteLinkDTO(*created, now, s.shareURL(created.LinkID, shareToken))
	return &dto, nil
}

func (s *Service) PreviewInviteLink(ctx context.Context, user *domain.User, linkID, token string) (*InviteLinkPreviewDTO, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	link, raw, err := s.loadInviteLinkToken(ctx, linkID, token)
	if err != nil {
		return nil, err
	}
	_ = raw
	now := s.clk.Now()
	state := link.EffectiveState(now)
	if state != domain.InviteLinkStateActive {
		return nil, inviteLinkGone(state)
	}
	team, err := s.teams.GetTeam(ctx, link.TeamID)
	if err != nil || team == nil || team.Status != domain.TeamStatusActive {
		return nil, errInviteLinkNotFound()
	}
	inviterName := ""
	if u, e := s.users.FindUserByID(ctx, link.CreatorUserID); e == nil && u != nil {
		inviterName = u.DisplayName
	}
	already, reinvite := false, false
	if cur, curTeam, _, e := s.teams.GetCurrentTeam(ctx, user.UserID); e == nil && cur != nil && curTeam != nil {
		already = cur.TeamID == team.TeamID
	}
	return &InviteLinkPreviewDTO{
		TeamName: team.Name, InviterDisplayName: inviterName, Role: string(domain.TeamRoleMember),
		ExpiresAt: formatTime(link.ExpiresAt), LinkVersion: formatUint(link.Version),
		EffectiveState: string(state), AlreadyMember: already, ReinvitationRequired: reinvite,
		CanJoin: !already && !reinvite && user.EmailVerifiedAt != nil && user.AccountStatus == domain.AccountStatusActive,
	}, nil
}

func (s *Service) AcceptInviteLink(ctx context.Context, user *domain.User, linkID string, in AcceptInviteLinkInput, idemKey string) (*ContextDTO, error) {
	if err := s.requireJoin(); err != nil {
		return nil, err
	}
	if err := requireActiveVerified(user); err != nil {
		return nil, err
	}
	_, raw, err := s.loadInviteLinkToken(ctx, linkID, in.Token)
	if err != nil {
		return nil, err
	}
	ver, err := parseVersion(in.ExpectedLinkVersion, "expectedLinkVersion")
	if err != nil {
		return nil, err
	}
	sharing, err := normalizeSharing(in.Sharing)
	if err != nil {
		return nil, err
	}
	idem, err := s.parseIdempotency("teams.invite-link.accept:"+linkID, idemKey, canonicalJSON(map[string]any{
		"version": ver, "sharing": sharing,
	}))
	if err != nil {
		return nil, err
	}
	res, err := s.teams.AcceptInviteLinkTx(ctx, store.AcceptInviteLinkTxInput{
		ActorUserID: user.UserID, LinkID: linkID, TokenHash: HashInviteToken(raw),
		ExpectedVersion: ver, Sharing: sharing, Idempotency: idem, Now: s.clk.Now(),
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_INVITE_LINK_NOT_FOUND")
	}
	if res == nil || res.Context == nil {
		return nil, errUnavailable("teams.unavailable", "accept invite link did not return a team context")
	}
	return contextDTO(&res.Context.Team, &res.Context.Membership, res.Outcome == store.TeamsTxAlreadyMember), nil
}

func (s *Service) GetMySharing(ctx context.Context, userID, teamID string) (*SharingDTO, error) {
	if _, _, _, err := s.requireMember(ctx, userID, teamID); err != nil {
		return nil, err
	}
	st, err := s.teams.GetMySharing(ctx, teamID, userID)
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	return sharingDTO(st), nil
}

func (s *Service) UpdateMySharing(ctx context.Context, userID, teamID string, in SharingPatchInput) (*SharingDTO, error) {
	if _, _, _, err := s.requireMember(ctx, userID, teamID); err != nil {
		return nil, err
	}
	ver, err := parseVersion(in.ExpectedSharingVersion, "expectedSharingVersion")
	if err != nil {
		return nil, err
	}
	sharing, err := normalizeSharing(&in.Sharing)
	if err != nil {
		return nil, err
	}
	st, err := s.teams.UpdateSharingTx(ctx, store.UpdateSharingTxInput{
		ActorUserID: userID, TeamID: teamID, ExpectedVersion: ver, Sharing: sharing, Now: s.clk.Now(),
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	return sharingDTO(st), nil
}

func (s *Service) GetAnalysis(ctx context.Context, userID, teamID string, q AnalysisQuery) (*AnalysisDTO, int, error) {
	if err := s.requireAnalysis(); err != nil {
		return nil, 0, err
	}
	team, _, _, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, 0, err
	}
	now := s.clk.Now()
	from, toEx, err := ResolveTeamRange(team.TimezoneName, q.RangeKey, q.From, q.To, now)
	if err != nil {
		return nil, 0, err
	}
	blocked, err := s.teams.HasOpenDeletionBarrier(ctx, teamID)
	if err != nil {
		return nil, 0, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	var snap *domain.TeamAnalysisSnapshot
	if q.SnapshotID != "" {
		snap, err = s.teams.GetReadySnapshot(ctx, teamID, q.SnapshotID)
		if err != nil {
			return nil, 0, mapStoreError(err, "RESOURCE_NOT_FOUND")
		}
		if snap == nil || snap.TeamID != teamID {
			return nil, 0, errResourceNotFound()
		}
		if snap.AuthRevision != team.AuthRevision {
			return nil, 0, domain.NewAppError(409, "TEAM_SNAPSHOT_OBSOLETE", "teams.snapshotObsolete", "analysis snapshot is obsolete", nil, domain.ErrConflict)
		}
	} else {
		var queued bool
		snap, queued, err = s.teams.GetOrQueueAnalysis(ctx, teamID, from, toEx, team.AuthRevision, AnalysisRuleVersion, now)
		if err != nil {
			return nil, 0, mapStoreError(err, "TEAM_NOT_FOUND")
		}
		if queued || snap == nil || snap.Status != domain.SnapshotReady || blocked {
			return updatingDTO(team.AuthRevision), analysisRetryAfterMS, nil
		}
	}
	if snap.Status != domain.SnapshotReady || blocked {
		return updatingDTO(team.AuthRevision), analysisRetryAfterMS, nil
	}
	rows, err := s.teams.ListAnalysisRows(ctx, snap.SnapshotID, snap.PublishedGeneration)
	if err != nil {
		return nil, 0, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	members, users, _, err := s.teams.ListMembers(ctx, teamID, "", "", 100)
	if err != nil {
		return nil, 0, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	sharingCount := 0
	for _, m := range members {
		if st, e := s.teams.GetMySharing(ctx, teamID, m.UserID); e == nil && st != nil && st.Sharing.Base {
			sharingCount++
		}
	}
	filters := domain.TeamAnalysisFilters{}
	if q.Agent != "" && q.Agent != "all" {
		filters.Agent = &q.Agent
	}
	if q.Provider != "" && q.Provider != "all" {
		filters.Provider = &q.Provider
	}
	if q.Model != "" && q.Model != "all" {
		filters.Model = &q.Model
	}
	dto := assembleAnalysis(team, snap, rows, members, users, sharingCount, from, toEx, filters, q)
	return dto, 0, nil
}

func (s *Service) GetFilterOptions(ctx context.Context, userID, teamID, snapshotID string) (*FilterOptionsDTO, error) {
	if err := s.requireAnalysis(); err != nil {
		return nil, err
	}
	team, _, _, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(snapshotID) == "" {
		return nil, fieldError("snapshotId", "teams.snapshotRequired", "snapshotId is required")
	}
	rows, err := s.rowsForSnapshot(ctx, team, snapshotID)
	if err != nil {
		return nil, err
	}
	agents, providers, models := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	for _, row := range rows {
		if row.VisibilityMask&VisibilityClassification == 0 {
			agents[BucketUnsharedClassification] = struct{}{}
			continue
		}
		if row.AgentID != nil && *row.AgentID != "" {
			agents[*row.AgentID] = struct{}{}
		}
		if row.ProviderID != nil && *row.ProviderID != "" {
			providers[*row.ProviderID] = struct{}{}
		}
		if row.ModelID != nil && *row.ModelID != "" {
			models[*row.ModelID] = struct{}{}
		}
	}
	return &FilterOptionsDTO{
		SnapshotID: snapshotID,
		Agents:     setToOptions(agents),
		Providers:  setToOptions(providers),
		Models:     setToOptions(models),
	}, nil
}

func (s *Service) CreateExport(ctx context.Context, userID, teamID string, in CreateExportInput, idemKey string) (*ExportDTO, error) {
	if err := s.requireExport(); err != nil {
		return nil, err
	}
	team, mem, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).ExportAnalytics {
		return nil, errPermissionDenied()
	}
	kind, err := parseExportKind(in.Kind)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SnapshotID) == "" {
		return nil, fieldError("snapshotId", "teams.snapshotRequired", "snapshotId is required")
	}
	if _, err := s.rowsForSnapshot(ctx, team, in.SnapshotID); err != nil {
		return nil, err
	}
	filter := map[string]any{
		"agent":    normalizeExportFilter(in.Agent),
		"provider": normalizeExportFilter(in.Provider),
		"model":    normalizeExportFilter(in.Model),
		"kind":     kind,
	}
	filterJSON, _ := json.Marshal(filter)
	filterSum := crypto.SHA256(filterJSON)
	filterHash := hex.EncodeToString(filterSum[:])
	exportID, err := NewPrefixedID(domain.ExportIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	now := s.clk.Now()
	job := domain.TeamExportJob{
		ExportID: exportID, TeamID: teamID, RequesterUserID: userID,
		RequesterMembershipID: mem.MembershipID, SnapshotID: in.SnapshotID,
		AuthRevision: team.AuthRevision, Kind: kind, FilterJSON: string(filterJSON),
		FiltersHash: filterHash, Status: domain.TeamExportQueued,
		CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}
	idem, err := s.parseIdempotency("teams.export:"+teamID, idemKey, string(filterJSON)+":"+in.SnapshotID)
	if err != nil {
		return nil, err
	}
	created, err := s.teams.QueueTeamExportTx(ctx, store.QueueTeamExportTxInput{
		ActorUserID: userID, Job: job, Idempotency: idem, Now: now,
	})
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if created == nil {
		created = &job
	}
	dto := exportDTO(*created)
	return &dto, nil
}

func (s *Service) ListExports(ctx context.Context, userID, teamID string) ([]ExportDTO, error) {
	if err := s.requireExport(); err != nil {
		return nil, err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).ExportAnalytics {
		return nil, errPermissionDenied()
	}
	jobs, err := s.teams.ListExports(ctx, teamID, userID)
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	out := make([]ExportDTO, 0, len(jobs))
	for i := range jobs {
		out = append(out, exportDTO(jobs[i]))
	}
	return out, nil
}

func (s *Service) GetExport(ctx context.Context, userID, teamID, exportID string) (*ExportDTO, error) {
	job, err := s.exportForRequester(ctx, userID, teamID, exportID)
	if err != nil {
		return nil, err
	}
	dto := exportDTO(*job)
	return &dto, nil
}

func (s *Service) OpenExportContent(ctx context.Context, userID, teamID, exportID string) (*ExportContent, error) {
	job, err := s.exportForRequester(ctx, userID, teamID, exportID)
	if err != nil {
		return nil, err
	}
	team, err := s.teams.GetTeam(ctx, teamID)
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if team.AuthRevision != job.AuthRevision {
		return nil, domain.NewAppError(410, "TEAM_EXPORT_REVOKED", "teams.exportRevoked", "export is no longer authorized", nil, domain.ErrForbidden)
	}
	switch job.Status {
	case domain.TeamExportRevoked:
		return nil, domain.NewAppError(410, "TEAM_EXPORT_REVOKED", "teams.exportRevoked", "export revoked", nil, domain.ErrNotFound)
	case domain.TeamExportExpired:
		return nil, domain.NewAppError(410, "TEAM_EXPORT_EXPIRED", "teams.exportExpired", "export expired", nil, domain.ErrNotFound)
	case domain.TeamExportCompleted:
	default:
		return nil, errUnavailable("teams.exportNotReady", "export is not ready")
	}
	if job.ObjectKey == nil || *job.ObjectKey == "" {
		return nil, errUnavailable("teams.exportNotReady", "export object is not available")
	}
	rc, err := s.storage.OpenObject(ctx, *job.ObjectKey)
	if err != nil {
		return nil, errUnavailable("teams.exportUnavailable", "failed to open export object")
	}
	size := int64(0)
	if job.FileSize != nil {
		size = int64(*job.FileSize)
	}
	return &ExportContent{
		Filename:    fmt.Sprintf("tokendance-team-%s-%s.csv", teamID, job.CreatedAt.UTC().Format("20060102")),
		ContentType: exportContentTypeCSV,
		Body:        rc,
		Size:        size,
	}, nil
}

func (s *Service) LeaveTeam(ctx context.Context, userID, teamID, expectedAuth, idemKey string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !domain.PermissionsFor(role).Leave {
		return domain.NewAppError(409, "TEAM_OWNER_TRANSFER_REQUIRED", "teams.ownerTransferRequired", "owner must transfer or dissolve before leaving", nil, domain.ErrConflict)
	}
	rev, err := parseVersion(expectedAuth, "expectedAuthRevision")
	if err != nil {
		return err
	}
	idem, err := s.parseIdempotency("teams.leave:"+teamID, idemKey, teamID+":"+expectedAuth)
	if err != nil {
		return err
	}
	if err := s.teams.LeaveTeamTx(ctx, store.LeaveTeamTxInput{
		ActorUserID: userID, TeamID: teamID, ExpectedAuthRevision: rev, Idempotency: idem, Now: s.clk.Now(),
	}); err != nil {
		return mapStoreError(err, "TEAM_NOT_FOUND")
	}
	return nil
}

func (s *Service) TransferOwnership(ctx context.Context, userID, teamID string, in TransferInput, idemKey string) (*ContextDTO, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).TransferOwnership {
		return nil, errPermissionDenied()
	}
	if strings.TrimSpace(in.TargetMembershipID) == "" || strings.TrimSpace(in.ConfirmTeamName) == "" {
		return nil, fieldError("confirmTeamName", "teams.confirmRequired", "target membership and team name confirmation are required")
	}
	rev, err := parseVersion(in.ExpectedAuthRevision, "expectedAuthRevision")
	if err != nil {
		return nil, err
	}
	idem, err := s.parseIdempotency("teams.transfer:"+teamID, idemKey, canonicalJSON(map[string]any{
		"target": in.TargetMembershipID, "confirm": in.ConfirmTeamName, "rev": rev,
	}))
	if err != nil {
		return nil, err
	}
	ctxRes, err := s.teams.TransferOwnershipTx(ctx, store.TransferOwnershipTxInput{
		ActorUserID: userID, TeamID: teamID, TargetMembershipID: in.TargetMembershipID,
		ConfirmTeamName: in.ConfirmTeamName, ExpectedAuthRevision: rev, Idempotency: idem, Now: s.clk.Now(),
	})
	if err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	if ctxRes == nil {
		return nil, errUnavailable("teams.unavailable", "transfer did not return a team context")
	}
	return contextDTO(&ctxRes.Team, &ctxRes.Membership, false), nil
}

func (s *Service) DissolveTeam(ctx context.Context, userID, teamID string, in DissolveInput, idemKey string) error {
	if err := s.requireEnabled(); err != nil {
		return err
	}
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !domain.PermissionsFor(role).Dissolve {
		return errPermissionDenied()
	}
	if strings.TrimSpace(in.ConfirmTeamName) == "" {
		return fieldError("confirmTeamName", "teams.confirmRequired", "team name confirmation is required")
	}
	rev, err := parseVersion(in.ExpectedAuthRevision, "expectedAuthRevision")
	if err != nil {
		return err
	}
	idem, err := s.parseIdempotency("teams.dissolve:"+teamID, idemKey, teamID+":"+in.ConfirmTeamName+":"+in.ExpectedAuthRevision)
	if err != nil {
		return err
	}
	if err := s.teams.DissolveTeamTx(ctx, store.DissolveTeamTxInput{
		ActorUserID: userID, TeamID: teamID, ConfirmTeamName: in.ConfirmTeamName,
		ExpectedAuthRevision: rev, Idempotency: idem, Now: s.clk.Now(),
	}); err != nil {
		return mapStoreError(err, "TEAM_NOT_FOUND")
	}
	return nil
}

func (s *Service) ListAuditEvents(ctx context.Context, userID, teamID string, q PageQuery) ([]AuditDTO, string, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, "", err
	}
	if !domain.PermissionsFor(role).ExportAnalytics {
		return nil, "", errPermissionDenied()
	}
	items, next, err := s.teams.ListAuditEvents(ctx, teamID, q.Cursor, clampLimit(q.Limit))
	if err != nil {
		return nil, "", mapStoreError(err, "TEAM_NOT_FOUND")
	}
	out := make([]AuditDTO, 0, len(items))
	for _, e := range items {
		out = append(out, AuditDTO{
			ID: e.AuditID, Action: e.Action, TargetType: e.TargetType, TargetID: e.TargetID,
			SafeDetails: e.SafeDetailsJSON, CreatedAt: formatTime(e.CreatedAt),
		})
	}
	return out, next, nil
}

func (s *Service) CreateAvatarIntent(ctx context.Context, userID, teamID string, in CreateAvatarIntentInput) (*AvatarIntentDTO, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).EditProfile {
		return nil, errPermissionDenied()
	}
	ct := strings.ToLower(strings.TrimSpace(in.ContentType))
	if ct != "image/png" && ct != "image/jpeg" && ct != "image/webp" {
		return nil, fieldError("contentType", "teams.invalidAvatarType", "only image/png, image/jpeg, image/webp allowed")
	}
	if in.ByteSize == 0 || in.ByteSize > teamAvatarMaxBytes {
		return nil, fieldError("byteSize", "teams.avatarTooLarge", "avatar must be between 1 byte and 2 MiB")
	}
	shaTrimmed := strings.ToLower(strings.TrimSpace(in.Sha256))
	sum, err := hex.DecodeString(shaTrimmed)
	if err != nil || len(sum) != 32 {
		return nil, fieldError("sha256", "teams.invalidSha256", "sha256 must be a 64-character hex string")
	}
	var digest [32]byte
	copy(digest[:], sum)
	objectID, err := NewPrefixedID(domain.TeamObjectIDPrefix)
	if err != nil {
		return nil, errInternal(err)
	}
	now := s.clk.Now()
	expires := now.Add(avatarIntentTTL)
	obj := domain.TeamUploadObject{
		ObjectID: objectID, TeamID: teamID, UploaderID: userID,
		ObjectKey: fmt.Sprintf("teams/%s/avatars/%s", teamID, objectID),
		ContentType: ct, ByteSize: in.ByteSize, SHA256: digest, Status: "pending", ExpiresAt: &expires,
	}
	created, err := s.teams.CreateTeamAvatarUploadIntent(ctx, obj)
	if err != nil {
		return nil, mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if created == nil {
		created = &obj
	}
	return &AvatarIntentDTO{ObjectID: created.ObjectID, ExpiresAt: formatTime(expires)}, nil
}

func (s *Service) UploadAvatarContent(ctx context.Context, userID, teamID, objectID string, body io.Reader) error {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !domain.PermissionsFor(role).EditProfile {
		return errPermissionDenied()
	}
	obj, err := s.teams.GetAvatarObject(ctx, teamID, objectID)
	if err != nil || obj == nil || obj.TeamID != teamID || obj.UploaderID != userID {
		return errResourceNotFound()
	}
	data, err := io.ReadAll(io.LimitReader(body, teamAvatarMaxBytes+1))
	if err != nil {
		return fieldError("content", "teams.invalidAvatar", "failed to read avatar content")
	}
	if len(data) == 0 || uint64(len(data)) != obj.ByteSize || int64(len(data)) > teamAvatarMaxBytes {
		return fieldError("content", "teams.avatarTooLarge", "avatar payload size mismatch")
	}
	hash := crypto.SHA256(data)
	if !crypto.ConstantTimeCompare(hash[:], obj.SHA256[:]) {
		return fieldError("content", "teams.sha256Mismatch", "uploaded payload does not match declared sha256")
	}
	if detectImageMagicBytes(data) == "" {
		return fieldError("content", "teams.invalidAvatarType", "uploaded payload is not a static PNG, JPEG, or WebP")
	}
	if err := s.storage.PutObject(ctx, obj.ObjectKey, bytes.NewReader(data), int64(len(data)), obj.ContentType); err != nil {
		return errUnavailable("teams.storageUnavailable", "object storage is temporarily unavailable")
	}
	return nil
}

func (s *Service) CompleteAvatar(ctx context.Context, userID, teamID, objectID, expectedProfileVersion string) (*TeamDTO, error) {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).EditProfile {
		return nil, errPermissionDenied()
	}
	ver, err := parseVersion(expectedProfileVersion, "expectedProfileVersion")
	if err != nil {
		return nil, err
	}
	obj, err := s.teams.GetAvatarObject(ctx, teamID, objectID)
	if err != nil || obj == nil || obj.TeamID != teamID {
		return nil, errResourceNotFound()
	}
	rc, err := s.storage.OpenObject(ctx, obj.ObjectKey)
	if err != nil {
		return nil, errResourceNotFound()
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, teamAvatarMaxBytes+1))
	if err != nil || len(data) == 0 || int64(len(data)) > teamAvatarMaxBytes {
		return nil, fieldError("content", "teams.invalidAvatar", "avatar object is unreadable")
	}
	detected := detectImageMagicBytes(data)
	if detected == "" {
		return nil, fieldError("content", "teams.invalidAvatarType", "uploaded payload is not a static PNG, JPEG, or WebP")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > teamAvatarMaxEdge || cfg.Height > teamAvatarMaxEdge {
		return nil, fieldError("content", "teams.invalidAvatarDimensions", "image dimensions exceed 4096x4096")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, fieldError("content", "teams.corruptAvatar", "corrupt image pixel data")
	}
	hash := crypto.SHA256(data)
	team, err := s.teams.CompleteTeamAvatarUpload(ctx, teamID, objectID, userID, store.AvatarReadyMeta{
		ByteSize: uint64(len(data)), ContentSha256: hash,
		ImageWidth: uint32(cfg.Width), ImageHeight: uint32(cfg.Height), ContentType: detected,
	}, ver, s.clk.Now())
	if err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	dto := teamDTO(team)
	return &dto, nil
}

func (s *Service) ClearAvatar(ctx context.Context, userID, teamID, expectedProfileVersion string) error {
	_, _, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return err
	}
	if !domain.PermissionsFor(role).EditProfile {
		return errPermissionDenied()
	}
	ver, err := parseVersion(expectedProfileVersion, "expectedProfileVersion")
	if err != nil {
		return err
	}
	if err := s.teams.ClearTeamAvatar(ctx, teamID, userID, ver, s.clk.Now()); err != nil {
		return mapStoreError(err, "TEAM_NOT_FOUND")
	}
	return nil
}

func (s *Service) ReadAvatar(ctx context.Context, userID, teamID string) (*AvatarContent, error) {
	team, _, _, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if team.AvatarObjectID == nil || *team.AvatarObjectID == "" {
		return nil, errResourceNotFound()
	}
	obj, err := s.teams.GetAvatarObject(ctx, teamID, *team.AvatarObjectID)
	if err != nil || obj == nil {
		return nil, errResourceNotFound()
	}
	rc, err := s.storage.OpenObject(ctx, obj.ObjectKey)
	if err != nil {
		return nil, errResourceNotFound()
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, teamAvatarMaxBytes+1))
	if err != nil || int64(len(data)) > teamAvatarMaxBytes {
		return nil, errUnavailable("teams.storageUnavailable", "failed to read team avatar")
	}
	return &AvatarContent{ContentType: obj.ContentType, Body: data}, nil
}

func (s *Service) requireMember(ctx context.Context, userID, teamID string) (*domain.Team, *domain.TeamMembership, domain.TeamPublicRole, error) {
	if err := s.requireEnabled(); err != nil {
		return nil, nil, "", err
	}
	cur, team, mem, err := s.teams.GetCurrentTeam(ctx, userID)
	if err != nil {
		if isNotFound(err) {
			return nil, nil, "", errTeamNotFound()
		}
		return nil, nil, "", mapStoreError(err, "TEAM_NOT_FOUND")
	}
	if cur == nil || team == nil || mem == nil || cur.TeamID != teamID || team.TeamID != teamID || team.Status != domain.TeamStatusActive {
		return nil, nil, "", errTeamNotFound()
	}
	return team, mem, team.PublicRoleFor(userID, mem.BaseRole), nil
}

func (s *Service) rowsForSnapshot(ctx context.Context, team *domain.Team, snapshotID string) ([]domain.TeamAnalysisRow, error) {
	snap, err := s.teams.GetReadySnapshot(ctx, team.TeamID, snapshotID)
	if err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	if snap == nil || snap.TeamID != team.TeamID {
		return nil, errResourceNotFound()
	}
	if snap.AuthRevision != team.AuthRevision {
		return nil, domain.NewAppError(409, "TEAM_SNAPSHOT_OBSOLETE", "teams.snapshotObsolete", "analysis snapshot is obsolete", nil, domain.ErrConflict)
	}
	rows, err := s.teams.ListAnalysisRows(ctx, snap.SnapshotID, snap.PublishedGeneration)
	if err != nil {
		return nil, mapStoreError(err, "RESOURCE_NOT_FOUND")
	}
	return rows, nil
}

func (s *Service) exportForRequester(ctx context.Context, userID, teamID, exportID string) (*domain.TeamExportJob, error) {
	if err := s.requireExport(); err != nil {
		return nil, err
	}
	team, mem, role, err := s.requireMember(ctx, userID, teamID)
	if err != nil {
		return nil, err
	}
	if !domain.PermissionsFor(role).ExportAnalytics {
		return nil, errPermissionDenied()
	}
	job, err := s.teams.GetExport(ctx, teamID, exportID)
	if err != nil || job == nil || job.TeamID != teamID {
		return nil, errResourceNotFound()
	}
	if job.RequesterUserID != userID || job.RequesterMembershipID != mem.MembershipID {
		return nil, errResourceNotFound()
	}
	if team.AuthRevision != job.AuthRevision && job.Status == domain.TeamExportCompleted {
		return nil, domain.NewAppError(410, "TEAM_EXPORT_REVOKED", "teams.exportRevoked", "export is no longer authorized", nil, domain.ErrForbidden)
	}
	return job, nil
}

func (s *Service) loadInviteLinkToken(ctx context.Context, linkID, token string) (*domain.TeamInviteLink, []byte, error) {
	raw, err := DecodeInviteToken(token)
	if err != nil {
		return nil, nil, errInviteLinkNotFound()
	}
	link, err := s.teams.GetInviteLink(ctx, linkID)
	want := HashInviteToken(raw)
	if err != nil || link == nil {
		_ = crypto.ConstantTimeCompare(want[:], want[:])
		return nil, nil, errInviteLinkNotFound()
	}
	if !crypto.ConstantTimeCompare(want[:], link.TokenHash[:]) {
		return nil, nil, errInviteLinkNotFound()
	}
	return link, raw, nil
}

func (s *Service) decryptInviteToken(link *domain.TeamInviteLink) (string, error) {
	if s.auth == nil || s.auth.Cipher() == nil {
		return "", errUnavailable("teams.cryptoUnavailable", "invite link crypto is unavailable")
	}
	raw, err := s.auth.Cipher().Decrypt(link.TokenCiphertext, inviteLinkAAD(link.TeamID, link.LinkID))
	if err != nil || len(raw) != domain.InviteLinkTokenBytes {
		return "", errUnavailable("teams.cryptoUnavailable", "failed to decrypt invite token")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *Service) verifiedEmailHash(user *domain.User) ([32]byte, error) {
	var zero [32]byte
	if user == nil || user.EmailVerifiedAt == nil || s.auth == nil {
		return zero, errInvitationNotFound()
	}
	email, err := s.auth.DecryptUserEmail(user)
	if err != nil || email == "" {
		return zero, errInvitationNotFound()
	}
	normalized, err := auth.NormalizeEmail(email)
	if err != nil {
		return zero, errInvitationNotFound()
	}
	return s.auth.ComputeEmailLookupHash(normalized), nil
}

func (s *Service) parseIdempotency(scope, key, canonical string) (store.TeamsIdempotency, error) {
	if len(key) < 1 || len(key) > 64 {
		return store.TeamsIdempotency{}, fieldError("Idempotency-Key", "teams.idempotencyRequired", "Idempotency-Key header must be 1-64 characters")
	}
	for _, ch := range key {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || strings.ContainsRune("-_.:", ch)) {
			return store.TeamsIdempotency{}, fieldError("Idempotency-Key", "teams.idempotencyInvalid", "Idempotency-Key contains invalid characters")
		}
	}
	keyHash := crypto.HMACSHA256(s.cfg.IdempotencyKeys.Current(), []byte(scope+"\x00"+key))
	return store.TeamsIdempotency{
		KeyHash:     keyHash,
		RequestHash: sha256.Sum256([]byte(canonical)),
		Scope:       scope,
	}, nil
}

func (s *Service) shareURL(linkID, token string) string {
	base := strings.TrimRight(s.cfg.TeamsPublicBaseURL, "/")
	return base + "/teams/join/" + linkID + "#key=" + token
}

func (s *Service) invitationURL(invitationID string) string {
	base := strings.TrimRight(s.cfg.TeamsPublicBaseURL, "/")
	return base + "/teams/invitations/" + invitationID
}

func ResolveTeamRange(timezone, rangeKey, from, to string, now time.Time) (time.Time, time.Time, error) {
	if strings.TrimSpace(timezone) == "" {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, time.Time{}, fieldError("timezone", "teams.invalidTimezone", "invalid IANA timezone")
	}
	today := localMidnight(now.In(loc))
	var start, endExclusive time.Time
	switch strings.TrimSpace(rangeKey) {
	case "", "7d":
		start = today.AddDate(0, 0, -6)
		endExclusive = today.AddDate(0, 0, 1)
	case "30d":
		start = today.AddDate(0, 0, -29)
		endExclusive = today.AddDate(0, 0, 1)
	case "custom":
		if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
			return time.Time{}, time.Time{}, fieldError("from", "teams.invalidDateRange", "custom range requires from and to")
		}
		startDay, err := parseLocalDate(from, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fieldError("from", "teams.invalidDateRange", "from must be YYYY-MM-DD")
		}
		endDay, err := parseLocalDate(to, loc)
		if err != nil {
			return time.Time{}, time.Time{}, fieldError("to", "teams.invalidDateRange", "to must be YYYY-MM-DD")
		}
		if endDay.Before(startDay) {
			return time.Time{}, time.Time{}, fieldError("to", "teams.invalidDateRange", "to must be on or after from")
		}
		start = startDay
		endExclusive = endDay.AddDate(0, 0, 1)
	default:
		return time.Time{}, time.Time{}, fieldError("range", "teams.invalidDateRange", "range must be 7d, 30d, or custom")
	}
	if !start.Before(today.AddDate(0, 0, 1)) || !endExclusive.After(start) {
		return time.Time{}, time.Time{}, fieldError("from", "teams.invalidDateRange", "future dates are not allowed")
	}
	if start.After(today) {
		return time.Time{}, time.Time{}, fieldError("from", "teams.invalidDateRange", "future dates are not allowed")
	}
	days := 0
	for d := start; d.Before(endExclusive); d = d.AddDate(0, 0, 1) {
		days++
		if days > domain.TeamAnalysisMaxDays {
			return time.Time{}, time.Time{}, fieldError("to", "teams.dateRangeTooLong", "date range cannot exceed 90 local days")
		}
	}
	return start.UTC(), endExclusive.UTC(), nil
}

func localMidnight(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

func parseLocalDate(value string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation(dateLayout, strings.TrimSpace(value), loc)
}

func assembleAnalysis(team *domain.Team, snap *domain.TeamAnalysisSnapshot, rows []domain.TeamAnalysisRow, members []domain.TeamMembership, users []domain.User, sharingCount int, from, toEx time.Time, filters domain.TeamAnalysisFilters, q AnalysisQuery) *AnalysisDTO {
	filtered := filterRows(rows, filters)
	tokenTotal := "0"
	unsupported, estimated := uint64(0), uint64(0)
	active := map[string]struct{}{}
	byDate := map[string]string{}
	agentTok := map[string]string{}
	modelTok := map[string]string{}
	contribTok := map[string]string{}
	reported := map[string]*bigRatAcc{}
	estimatedCost := map[string]*bigRatAcc{}
	unattributed := "0"
	reportedUsage, eligibleUsage := uint64(0), uint64(0)
	for _, row := range filtered {
		tokenTotal = AddIntDecimal(tokenTotal, emptyZero(row.TokenExactTotal))
		tokenTotal = AddIntDecimal(tokenTotal, emptyZero(row.TokenDerivedTotal))
		if row.MembershipID != nil && (row.TokenExactTotal != "0" || row.TokenDerivedTotal != "0" || row.UsageEventCount != "0") {
			active[*row.MembershipID] = struct{}{}
		}
		if row.MetricDate != nil && *row.MetricDate != "" {
			byDate[*row.MetricDate] = AddIntDecimal(byDate[*row.MetricDate], AddIntDecimal(emptyZero(row.TokenExactTotal), emptyZero(row.TokenDerivedTotal)))
		}
		if row.AgentID != nil {
			agentTok[*row.AgentID] = AddIntDecimal(agentTok[*row.AgentID], AddIntDecimal(emptyZero(row.TokenExactTotal), emptyZero(row.TokenDerivedTotal)))
		}
		if row.ModelID != nil {
			key := ""
			if row.ProviderID != nil {
				key = *row.ProviderID + "/"
			}
			key += *row.ModelID
			modelTok[key] = AddIntDecimal(modelTok[key], AddIntDecimal(emptyZero(row.TokenExactTotal), emptyZero(row.TokenDerivedTotal)))
		}
		if row.MembershipID != nil && row.VisibilityMask&VisibilityNamed != 0 {
			contribTok[*row.MembershipID] = AddIntDecimal(contribTok[*row.MembershipID], AddIntDecimal(emptyZero(row.TokenExactTotal), emptyZero(row.TokenDerivedTotal)))
		}
		if row.Currency != nil && row.ReportedCostAmount != "" && row.ReportedCostAmount != "0" {
			if reported[*row.Currency] == nil {
				reported[*row.Currency] = &bigRatAcc{}
			}
			reported[*row.Currency].add(row.ReportedCostAmount)
		}
		if row.Currency != nil && row.EstimatedCostAmount != "" && row.EstimatedCostAmount != "0" {
			if estimatedCost[*row.Currency] == nil {
				estimatedCost[*row.Currency] = &bigRatAcc{}
			}
			estimatedCost[*row.Currency].add(row.EstimatedCostAmount)
		}
		unattributed = AddIntDecimal(unattributed, emptyZero(row.UnattributedCostCount))
		unsupported += parseCount(row.UsageEventCount) - parseCount(row.TokenSupportedEventCount)
		estimated += parseCount(row.EstimatedCostEventCount)
		reportedUsage += parseCount(row.ReportedCoveredUsageCount)
		eligibleUsage += parseCount(row.UsageEventCount)
	}
	state := domain.MetricAvailable
	if tokenTotal == "0" {
		state = domain.MetricEmpty
	}
	reason := "insufficient_history"
	trend := make([]domain.TeamTrendPoint, 0, len(byDate))
	for date, val := range byDate {
		st := domain.MetricAvailable
		if val == "0" {
			st = domain.MetricEmpty
		}
		trend = append(trend, domain.TeamTrendPoint{Date: date, Tokens: domain.DecimalMetric{Value: val, State: st}})
	}
	limit := clampLimit(q.Limit)
	agents := pageBuckets(sortKV(agentTok), "agent", q.Collection == "agents", q.Cursor, limit, tokenTotal)
	models := pageBuckets(sortKV(modelTok), "model", q.Collection == "models", q.Cursor, limit, tokenTotal)
	contribs := pageContributions(sortKV(contribTok), members, users, q.Collection == "contributions", q.Cursor, limit)
	costList := make([]domain.TeamCostAmount, 0, len(reported))
	for cur, acc := range reported {
		costList = append(costList, domain.TeamCostAmount{Currency: cur, Amount: acc.string()})
	}
	estimatedList := make([]domain.TeamCostAmount, 0, len(estimatedCost))
	for cur, acc := range estimatedCost {
		estimatedList = append(estimatedList, domain.TeamCostAmount{Currency: cur, Amount: acc.string()})
	}
	authRev := formatUint(snap.AuthRevision)
	_ = authRev
	return &AnalysisDTO{
		State: "ready",
		Snapshot: &snapshotDTO{
			ID: snap.SnapshotID, AuthRevision: formatUint(snap.AuthRevision),
			SourceRevision: formatUint(snap.SourceRevision), RuleVersion: snap.RuleVersion,
			AsOf: formatTime(snap.AsOf), Refreshing: snap.Refreshing,
		},
		Range: &domain.TeamAnalysisRange{
			Timezone: team.TimezoneName, From: from, ToExclusive: toEx, DataToExclusive: snap.AsOf,
		},
		Filters: filters,
		Summary: &domain.TeamAnalysisSummary{
			Tokens: domain.DecimalMetric{Value: tokenTotal, State: state},
			ActiveMembers: formatUint(uint64(len(active))),
			CurrentMembers: formatUint(uint64(len(members))),
			CurrentSharingMembers: formatUint(uint64(sharingCount)),
			ComparisonReason: &reason,
		},
		Costs: &domain.TeamAnalysisCosts{
			Reported: costList, EstimatedUncovered: estimatedList,
			Coverage: domain.TeamCostCoverage{ReportedUsageEvents: formatUint(reportedUsage), EligibleUsageEvents: formatUint(eligibleUsage)},
			UnattributedCostCount: unattributed,
		},
		Trend: trend,
		Agents: agents, Models: models, Contributions: contribs,
		Quality: &domain.TeamAnalysisQuality{UnsupportedEvents: formatUint(unsupported), EstimatedEvents: formatUint(estimated)},
		FiltersHash: analysisFiltersHash(filters),
	}
}

type kv struct {
	Key   string
	Value string
}

func sortKV(m map[string]string) []kv {
	out := make([]kv, 0, len(m))
	for k, v := range m {
		out = append(out, kv{Key: k, Value: v})
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if cmpIntDecimal(out[j].Value, out[i].Value) > 0 || (out[j].Value == out[i].Value && out[j].Key < out[i].Key) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func pageWindow(items []kv, paging bool, cursor string, limit int) (page []kv, start, end int, next *string) {
	start = 0
	if paging && cursor != "" {
		if n, err := strconv.Atoi(cursor); err == nil && n > 0 {
			start = n
		}
	}
	end = start + 20
	if paging {
		end = start + limit
	}
	if end > len(items) {
		end = len(items)
	}
	if start > len(items) {
		start = len(items)
	}
	page = items[start:end]
	if end < len(items) {
		n := strconv.Itoa(end)
		next = &n
	}
	return page, start, end, next
}

func pageBuckets(items []kv, kind string, paging bool, cursor string, limit int, tokenTotal string) *domain.TeamPagedItems {
	page, _, _, next := pageWindow(items, paging, cursor, limit)
	out := make([]map[string]any, 0, len(page))
	for _, it := range page {
		id, label, bucketType := bucketIdentity(kind, it.Key)
		item := map[string]any{
			"id": id, "label": label, "bucketType": bucketType,
			"tokens": domain.DecimalMetric{Value: it.Value, State: domain.MetricAvailable},
		}
		if share := tokenShare(it.Value, tokenTotal); share != nil {
			item["share"] = *share
		}
		out = append(out, item)
	}
	return &domain.TeamPagedItems{Items: out, NextCursor: next}
}

func pageContributions(items []kv, members []domain.TeamMembership, users []domain.User, paging bool, cursor string, limit int) *domain.TeamPagedItems {
	page, start, _, next := pageWindow(items, paging, cursor, limit)
	userByID := map[string]domain.User{}
	for i := range users {
		userByID[users[i].UserID] = users[i]
	}
	memByID := map[string]domain.TeamMembership{}
	for i := range members {
		memByID[members[i].MembershipID] = members[i]
	}
	out := make([]map[string]any, 0, len(page))
	for i, it := range page {
		display := ""
		var handle *string
		if mem, ok := memByID[it.Key]; ok {
			if u, found := userByID[mem.UserID]; found {
				display = u.DisplayName
				handle = u.Handle
			}
		}
		out = append(out, map[string]any{
			"membershipId": it.Key,
			"displayName":  display,
			"handle":       handle,
			"rank":         strconv.Itoa(start + i + 1),
			"tokens":       domain.DecimalMetric{Value: it.Value, State: domain.MetricAvailable},
			"namedShare":   true,
		})
	}
	return &domain.TeamPagedItems{Items: out, NextCursor: next}
}

func bucketIdentity(kind, key string) (id, label, bucketType string) {
	if key == BucketUnsharedClassification {
		return key, key, "unshared_classification"
	}
	if kind == "model" {
		id, label = key, key
		if idx := strings.LastIndex(key, "/"); idx >= 0 && idx+1 < len(key) {
			id = key[idx+1:]
		}
		return id, label, "model"
	}
	return key, key, "agent"
}

func tokenShare(part, total string) *string {
	num, okN := new(big.Int).SetString(emptyZero(part), 10)
	den, okD := new(big.Int).SetString(emptyZero(total), 10)
	if !okN || !okD || den.Sign() == 0 {
		return nil
	}
	rat := new(big.Rat).SetFrac(num, den)
	rat.Mul(rat, new(big.Rat).SetInt64(100))
	text := strings.TrimRight(strings.TrimRight(rat.FloatString(1), "0"), ".")
	if text == "" {
		text = "0"
	}
	return &text
}

func analysisFiltersHash(filters domain.TeamAnalysisFilters) string {
	raw, _ := json.Marshal(map[string]any{"agent": filters.Agent, "provider": filters.Provider, "model": filters.Model})
	sum := crypto.SHA256(raw)
	return hex.EncodeToString(sum[:])
}

func rowsForMembership(rows []domain.TeamAnalysisRow, membershipID string) []domain.TeamAnalysisRow {
	out := make([]domain.TeamAnalysisRow, 0, len(rows))
	for _, row := range rows {
		if row.MembershipID != nil && *row.MembershipID == membershipID {
			out = append(out, row)
		}
	}
	return out
}

func assembleMemberDetail(target *domain.TeamMembership, user *domain.User, team *domain.Team, snap *domain.TeamAnalysisSnapshot, rows []domain.TeamAnalysisRow, sharing domain.SharingFlags) map[string]any {
	display := ""
	var handle *string
	if user != nil {
		display = user.DisplayName
		handle = user.Handle
	}
	tokens := "0"
	for _, row := range rows {
		if row.VisibilityMask&VisibilityNamed != 0 {
			tokens = AddIntDecimal(tokens, emptyZero(row.TokenExactTotal))
			tokens = AddIntDecimal(tokens, emptyZero(row.TokenDerivedTotal))
		}
	}
	state := domain.MetricAvailable
	if tokens == "0" {
		state = domain.MetricEmpty
	}
	costRows := rows
	classRows := rows
	if !sharing.Cost {
		costRows = nil
	}
	if !sharing.Classification {
		classRows = nil
	}
	rollup := rollupCosts(costRows)
	agentTok, modelTok := classificationTotals(classRows)
	agents := pageBuckets(sortKV(agentTok), "agent", false, "", 20, tokens)
	models := pageBuckets(sortKV(modelTok), "model", false, "", 20, tokens)
	return map[string]any{
		"membershipId": target.MembershipID, "displayName": display, "handle": handle,
		"role": string(team.PublicRoleFor(target.UserID, target.BaseRole)),
		"joinedAt": formatTime(target.JoinedAt),
		"range": domain.TeamAnalysisRange{Timezone: team.TimezoneName, From: snap.FromDate, ToExclusive: snap.ToDateExclusive, DataToExclusive: snap.AsOf},
		"sharing": sharing,
		"tokens": domain.DecimalMetric{Value: tokens, State: state},
		"costs": domain.TeamAnalysisCosts{
			Reported: rollup.reported, EstimatedUncovered: rollup.estimated,
			Coverage: domain.TeamCostCoverage{ReportedUsageEvents: formatUint(rollup.reportedUsage), EligibleUsageEvents: formatUint(rollup.eligibleUsage)},
			UnattributedCostCount: rollup.unattributed,
		},
		"agents": agents.Items, "models": models.Items,
		"dimensions": map[string]string{
			"named":          dimensionState(sharing.Named),
			"classification": dimensionState(sharing.Classification),
			"cost":           dimensionState(sharing.Cost),
		},
	}
}

type costRollup struct {
	reported      []domain.TeamCostAmount
	estimated     []domain.TeamCostAmount
	unattributed  string
	reportedUsage uint64
	eligibleUsage uint64
}

func rollupCosts(rows []domain.TeamAnalysisRow) costRollup {
	reported := map[string]*bigRatAcc{}
	estimated := map[string]*bigRatAcc{}
	out := costRollup{unattributed: "0"}
	for _, row := range rows {
		if row.Currency != nil && row.ReportedCostAmount != "" && row.ReportedCostAmount != "0" {
			if reported[*row.Currency] == nil {
				reported[*row.Currency] = &bigRatAcc{}
			}
			reported[*row.Currency].add(row.ReportedCostAmount)
		}
		if row.Currency != nil && row.EstimatedCostAmount != "" && row.EstimatedCostAmount != "0" {
			if estimated[*row.Currency] == nil {
				estimated[*row.Currency] = &bigRatAcc{}
			}
			estimated[*row.Currency].add(row.EstimatedCostAmount)
		}
		out.unattributed = AddIntDecimal(out.unattributed, emptyZero(row.UnattributedCostCount))
		out.reportedUsage += parseCount(row.ReportedCoveredUsageCount)
		out.eligibleUsage += parseCount(row.UsageEventCount)
	}
	for cur, acc := range reported {
		out.reported = append(out.reported, domain.TeamCostAmount{Currency: cur, Amount: acc.string()})
	}
	for cur, acc := range estimated {
		out.estimated = append(out.estimated, domain.TeamCostAmount{Currency: cur, Amount: acc.string()})
	}
	if out.reported == nil {
		out.reported = []domain.TeamCostAmount{}
	}
	if out.estimated == nil {
		out.estimated = []domain.TeamCostAmount{}
	}
	return out
}

func classificationTotals(rows []domain.TeamAnalysisRow) (map[string]string, map[string]string) {
	agentTok := map[string]string{}
	modelTok := map[string]string{}
	for _, row := range rows {
		tokens := AddIntDecimal(emptyZero(row.TokenExactTotal), emptyZero(row.TokenDerivedTotal))
		if row.AgentID != nil {
			agentTok[*row.AgentID] = AddIntDecimal(agentTok[*row.AgentID], tokens)
		}
		if row.ModelID != nil {
			key := ""
			if row.ProviderID != nil {
				key = *row.ProviderID + "/"
			}
			key += *row.ModelID
			modelTok[key] = AddIntDecimal(modelTok[key], tokens)
		}
	}
	return agentTok, modelTok
}

func dimensionState(enabled bool) string {
	if enabled {
		return "available"
	}
	return "unavailable"
}

func normalizeExportFilter(value *string) any {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" || trimmed == "all" {
		return nil
	}
	return trimmed
}

func filterRows(rows []domain.TeamAnalysisRow, filters domain.TeamAnalysisFilters) []domain.TeamAnalysisRow {
	if filters.Agent == nil && filters.Provider == nil && filters.Model == nil {
		return rows
	}
	out := make([]domain.TeamAnalysisRow, 0, len(rows))
	for _, row := range rows {
		if filters.Agent != nil && (row.AgentID == nil || *row.AgentID != *filters.Agent) {
			if row.AgentID == nil || *row.AgentID != BucketUnsharedClassification {
				continue
			}
		}
		if filters.Provider != nil && (row.ProviderID == nil || *row.ProviderID != *filters.Provider) {
			continue
		}
		if filters.Model != nil && (row.ModelID == nil || *row.ModelID != *filters.Model) {
			continue
		}
		out = append(out, row)
	}
	return out
}

func memberLastReceived(rows []domain.TeamAnalysisRow) map[string]time.Time {
	out := map[string]time.Time{}
	for _, row := range rows {
		if row.MembershipID == nil || row.MaxReceivedAt == nil {
			continue
		}
		if prev, ok := out[*row.MembershipID]; !ok || row.MaxReceivedAt.After(prev) {
			out[*row.MembershipID] = *row.MaxReceivedAt
		}
	}
	return out
}

func memberPeriodMetric(sharing domain.SharingFlags, tokens string) *domain.DecimalMetric {
	if !sharing.Named {
		return &domain.DecimalMetric{Value: "0", State: domain.MetricNotShared}
	}
	if strings.TrimSpace(tokens) == "" || tokens == "0" {
		return &domain.DecimalMetric{Value: "0", State: domain.MetricEmpty}
	}
	return &domain.DecimalMetric{Value: tokens, State: domain.MetricAvailable}
}

func memberSyncStatus(sharing domain.SharingFlags, lastReceived *string, now time.Time) string {
	if !sharing.Base {
		return "not_shared"
	}
	if lastReceived == nil || *lastReceived == "" {
		return "waiting"
	}
	parsed, err := time.Parse(time.RFC3339Nano, *lastReceived)
	if err != nil {
		parsed, err = time.Parse(timeJSON, *lastReceived)
	}
	if err != nil {
		return "waiting"
	}
	if now.Sub(parsed) > 15*time.Minute {
		return "delayed"
	}
	return "healthy"
}

func memberTokenTotals(rows []domain.TeamAnalysisRow) map[string]string {
	out := map[string]string{}
	for _, row := range rows {
		if row.MembershipID == nil || row.VisibilityMask&VisibilityNamed == 0 {
			continue
		}
		out[*row.MembershipID] = AddIntDecimal(out[*row.MembershipID], AddIntDecimal(emptyZero(row.TokenExactTotal), emptyZero(row.TokenDerivedTotal)))
	}
	return out
}

type bigRatAcc struct {
	set bool
	raw string
}

func (a *bigRatAcc) add(v string) {
	if !a.set {
		a.raw = NormalizeCostAmount(v)
		a.set = true
		return
	}
	sum, ok := sumCostAmounts([]string{a.raw, v})
	if ok {
		a.raw = sum
	}
}

func (a *bigRatAcc) string() string {
	if !a.set {
		return "0.00000000"
	}
	return a.raw
}

func parseCount(s string) uint64 {
	n, _ := strconv.ParseUint(emptyZero(s), 10, 64)
	return n
}

func setToOptions(set map[string]struct{}) []map[string]string {
	out := make([]map[string]string, 0, len(set))
	for id := range set {
		out = append(out, map[string]string{"id": id, "label": id})
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["id"] < out[j]["id"] })
	return out
}

func updatingDTO(authRevision uint64) *AnalysisDTO {
	rev := formatUint(authRevision)
	retry := analysisRetryAfterMS
	msg := "teams.analytics.updating"
	return &AnalysisDTO{State: "updating", AuthRevision: &rev, RetryAfterMs: &retry, MessageKey: &msg}
}

func contextDTO(team *domain.Team, mem *domain.TeamMembership, already bool) *ContextDTO {
	role := team.PublicRoleFor(mem.UserID, mem.BaseRole)
	return &ContextDTO{
		Team: teamDTO(team),
		Membership: MembershipDTO{
			ID: mem.MembershipID, Role: string(role),
			SharingVersion: formatUint(mem.SharingVersion), JoinedAt: formatTime(mem.JoinedAt),
		},
		Permissions:   domain.PermissionsFor(role),
		AlreadyMember: already,
	}
}

func teamDTO(team *domain.Team) TeamDTO {
	vis := team.Visibility
	if vis == "" {
		vis = "private"
	}
	return TeamDTO{
		ID: team.TeamID, Name: team.Name, Description: team.Description, Timezone: team.TimezoneName,
		Visibility: vis, Status: string(team.Status),
		ProfileVersion: formatUint(team.ProfileVersion), AuthRevision: formatUint(team.AuthRevision),
	}
}

func invitationDTO(inv domain.TeamInvitation, delivery string) InvitationDTO {
	return InvitationDTO{
		ID: inv.InvitationID, TeamID: inv.TeamID, InvitedRole: string(inv.InvitedRole),
		Status: string(inv.Status), Version: formatUint(inv.Version),
		CreatedAt: formatTime(inv.CreatedAt), ExpiresAt: formatTime(inv.ExpiresAt),
		DeliveryState: delivery,
	}
}

func (s *Service) invitationDTOMasked(inv domain.TeamInvitation, delivery string) InvitationDTO {
	dto := invitationDTO(inv, delivery)
	dto.RecipientMasked = s.maskInvitationEmail(inv)
	return dto
}

func (s *Service) inboxInvitationDTO(ctx context.Context, inv domain.TeamInvitation) (InboxInvitationDTO, error) {
	team, err := s.teams.GetTeam(ctx, inv.TeamID)
	if err != nil || team == nil {
		return InboxInvitationDTO{}, errInvitationNotFound()
	}
	inviterName := ""
	if u, e := s.users.FindUserByID(ctx, inv.InviterUserID); e == nil && u != nil {
		inviterName = u.DisplayName
	}
	status := inv.Status
	if status == domain.InvitationPending && !inv.ExpiresAt.After(s.clk.Now()) {
		status = domain.InvitationExpired
	}
	return InboxInvitationDTO{
		ID: inv.InvitationID,
		Team: InvitationTeamDTO{ID: team.TeamID, Name: team.Name, Description: team.Description},
		InviterDisplayName: inviterName,
		InvitedRole: string(inv.InvitedRole),
		CreatedAt: formatTime(inv.CreatedAt),
		ExpiresAt: formatTime(inv.ExpiresAt),
		Version: formatUint(inv.Version),
		Status: string(status),
	}, nil
}

func (s *Service) maskInvitationEmail(inv domain.TeamInvitation) string {
	if s.auth == nil || s.auth.Cipher() == nil || len(inv.RecipientCiphertext) == 0 {
		return "***"
	}
	plain, err := s.auth.Cipher().Decrypt(inv.RecipientCiphertext, invitationEmailAAD(inv.InvitationID))
	if err != nil {
		return "***"
	}
	return maskEmail(string(plain))
}

func maskEmail(email string) string {
	email = strings.TrimSpace(email)
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return "***"
	}
	local, domain := email[:at], email[at+1:]
	if local == "" {
		return "***@" + domain
	}
	return string([]rune(local)[0]) + "***@" + domain
}

func (s *Service) inviteLinkResponseToken(res *store.CreateInviteLinkTxResult, generated string) (string, error) {
	if res != nil && res.Outcome == store.TeamsTxReplay && res.Link != nil {
		return s.decryptInviteToken(res.Link)
	}
	return generated, nil
}

func cmpIntDecimal(a, b string) int {
	x, okA := new(big.Int).SetString(emptyZero(a), 10)
	y, okB := new(big.Int).SetString(emptyZero(b), 10)
	if !okA || !okB {
		switch {
		case a > b:
			return 1
		case a < b:
			return -1
		default:
			return 0
		}
	}
	return x.Cmp(y)
}

func inviteLinkDTO(link domain.TeamInviteLink, now time.Time, shareURL string) InviteLinkDTO {
	return InviteLinkDTO{
		ID: link.LinkID, Version: formatUint(link.Version), Role: string(domain.TeamRoleMember),
		ExpiresAt: formatTime(link.ExpiresAt), MaxUses: formatUint(uint64(link.MaxUses)),
		UsedCount: formatUint(uint64(link.UsedCount)), EffectiveState: string(link.EffectiveState(now)),
		CreatorUserID: link.CreatorUserID, ShareURL: shareURL,
	}
}

func exportDTO(job domain.TeamExportJob) ExportDTO {
	dto := ExportDTO{
		ID: job.ExportID, TeamID: job.TeamID, SnapshotID: job.SnapshotID, Kind: string(job.Kind),
		Status: string(job.Status), AuthRevision: formatUint(job.AuthRevision), FiltersHash: job.FiltersHash,
		CreatedAt: formatTime(job.CreatedAt), ExpiresAt: formatTime(job.ExpiresAt), ErrorCode: job.ErrorCode,
	}
	if job.FileSize != nil {
		s := formatUint(*job.FileSize)
		dto.FileSize = &s
	}
	return dto
}

func sharingDTO(st *domain.TeamSharingState) *SharingDTO {
	if st == nil {
		return &SharingDTO{Sharing: domain.SharingFlags{}, EffectiveFrom: map[string]string{}}
	}
	from := map[string]string{}
	for k, v := range st.EffectiveFrom {
		from[k] = formatTime(v)
	}
	return &SharingDTO{
		MembershipID: st.MembershipID, SharingVersion: formatUint(st.SharingVersion),
		Sharing: st.Sharing, EffectiveFrom: from,
	}
}

func normalizeSharing(flags *domain.SharingFlags) (domain.SharingFlags, error) {
	if flags == nil {
		return domain.SharingFlags{}, nil
	}
	if !flags.Valid() {
		return domain.SharingFlags{}, fieldError("sharing", "teams.invalidSharing", "base=false cannot enable other sharing dimensions")
	}
	return *flags, nil
}

func normalizeInviteLinkParams(days, uses int) (int, int, error) {
	if days == 0 {
		days = inviteLinkDefaultDays
	}
	if days != 1 && days != 7 && days != 30 {
		return 0, 0, fieldError("expiresInDays", "teams.invalidInviteLink", "expiresInDays must be 1, 7, or 30")
	}
	if uses == 0 {
		uses = inviteLinkDefaultUses
	}
	if uses < domain.InviteLinkMaxUsesMin || uses > domain.InviteLinkMaxUsesMax {
		return 0, 0, fieldError("maxUses", "teams.invalidInviteLink", "maxUses must be between 1 and 100")
	}
	return days, uses, nil
}

func parseBaseRole(role string) (domain.TeamBaseRole, error) {
	switch strings.TrimSpace(role) {
	case string(domain.TeamBaseRoleAdmin):
		return domain.TeamBaseRoleAdmin, nil
	case string(domain.TeamBaseRoleMember), "":
		return domain.TeamBaseRoleMember, nil
	default:
		return "", fieldError("role", "teams.invalidRole", "role must be admin or member")
	}
}

func parseExportKind(kind string) (domain.TeamExportKind, error) {
	switch domain.TeamExportKind(strings.TrimSpace(kind)) {
	case domain.TeamExportDaily, domain.TeamExportAgents, domain.TeamExportModels, domain.TeamExportMembers:
		return domain.TeamExportKind(kind), nil
	default:
		return "", fieldError("kind", "teams.invalidExportKind", "kind must be daily, agents, models, or members")
	}
}

func validateTimezone(tz string) (string, error) {
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return "", fieldError("timezone", "teams.invalidTimezone", "timezone is required")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return "", fieldError("timezone", "teams.invalidTimezone", "invalid IANA timezone")
	}
	return tz, nil
}

func parseVersion(raw, field string) (uint64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, fieldError(field, "teams.versionRequired", "expected version is required")
	}
	v, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fieldError(field, "teams.invalidVersion", "version must be a decimal string")
	}
	return v, nil
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultPageLimit
	}
	if limit > maxPageLimit {
		return maxPageLimit
	}
	return limit
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeJSON)
}

func canonicalJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func localeOrDefault(locale string) string {
	if locale == "" {
		return "en-US"
	}
	return locale
}

func requireActiveVerified(user *domain.User) error {
	if user == nil || user.AccountStatus != domain.AccountStatusActive {
		return domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "auth.accountActionNotAllowed", "account cannot perform this action", nil, domain.ErrForbidden)
	}
	if user.EmailVerifiedAt == nil {
		return domain.NewAppError(403, "ACCOUNT_ACTION_NOT_ALLOWED", "teams.emailUnverified", "verified email is required", nil, domain.ErrForbidden)
	}
	return nil
}

func invitationEmailAAD(invitationID string) []byte {
	return []byte("tokendance.team-invitation.email.v1\x00" + invitationID)
}

func invitationPayloadAAD(invitationID string) []byte {
	return []byte("tokendance.team-invitation.payload.v1\x00" + invitationID)
}

func inviteLinkAAD(teamID, linkID string) []byte {
	return []byte("tokendance.team-invite-link.v1\x00" + teamID + "\x00" + linkID)
}

func detectImageMagicBytes(data []byte) string {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}) {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

func mapStoreError(err error, notFoundCode string) error {
	if err == nil {
		return nil
	}
	var app *domain.AppError
	if errors.As(err, &app) {
		switch app.Code {
		case "VERSION_CONFLICT":
			return errVersionConflict()
		case "TEAM_TEMPORARILY_UNAVAILABLE", "TEAM_MEMBERSHIP_EXISTS", "TEAM_VERSION_CONFLICT",
			"TEAM_OWNER_TRANSFER_REQUIRED", "TEAM_INVITATION_PENDING", "TEAM_SNAPSHOT_OBSOLETE",
			"IDEMPOTENCY_KEY_REUSED", "TEAM_INVITATION_NOT_FOUND", "TEAM_INVITE_LINK_NOT_FOUND",
			"TEAM_REINVITATION_REQUIRED", "TEAM_INVITATION_EXPIRED", "TEAM_INVITATION_REVOKED",
			"TEAM_INVITE_LINK_EXPIRED", "TEAM_INVITE_LINK_REVOKED", "TEAM_INVITE_LINK_EXHAUSTED",
			"TEAM_INVITE_LINK_ALREADY_USED", "TEAM_EXPORT_REVOKED", "TEAM_EXPORT_EXPIRED",
			"COMMAND_RESULT_UNAVAILABLE", "TEAM_NOT_FOUND", "RESOURCE_NOT_FOUND", "TEAM_PERMISSION_DENIED":
			return app
		}
		if app.HTTPStatus == 409 && errors.Is(err, domain.ErrPreconditionFailed) {
			return errVersionConflict()
		}
		return app
	}
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if notFoundCode == "RESOURCE_NOT_FOUND" {
			return errResourceNotFound()
		}
		if notFoundCode == "TEAM_INVITATION_NOT_FOUND" {
			return errInvitationNotFound()
		}
		if notFoundCode == "TEAM_INVITE_LINK_NOT_FOUND" {
			return errInviteLinkNotFound()
		}
		return errTeamNotFound()
	case errors.Is(err, domain.ErrPreconditionFailed):
		return errVersionConflict()
	case errors.Is(err, domain.ErrConflict):
		return errMembershipExists()
	case errors.Is(err, domain.ErrIdempotencyReused):
		return errIdempotencyReused()
	case errors.Is(err, domain.ErrForbidden):
		return errPermissionDenied()
	default:
		return errUnavailable("teams.unavailable", "teams store is temporarily unavailable")
	}
}

func isNotFound(err error) bool {
	if errors.Is(err, domain.ErrNotFound) {
		return true
	}
	var app *domain.AppError
	return errors.As(err, &app) && (app.Code == "RESOURCE_NOT_FOUND" || app.Code == "TEAM_NOT_FOUND")
}

func fieldError(field, key, message string) error {
	return domain.NewAppError(400, "API_INVALID_ARGUMENT", key, message, map[string]interface{}{
		"fieldErrors": map[string]string{field: key},
	}, domain.ErrInvalidArgument)
}

func errUnavailable(key, message string) error {
	return domain.NewAppError(503, "TEAM_TEMPORARILY_UNAVAILABLE", key, message, nil, domain.ErrInternal)
}

func errTeamNotFound() error {
	return domain.NewAppError(404, "TEAM_NOT_FOUND", "teams.notFound", "team not found", nil, domain.ErrNotFound)
}

func errResourceNotFound() error {
	return domain.NewAppError(404, "RESOURCE_NOT_FOUND", "teams.resourceNotFound", "resource not found", nil, domain.ErrNotFound)
}

func errInvitationNotFound() error {
	return domain.NewAppError(404, "TEAM_INVITATION_NOT_FOUND", "teams.invitationNotFound", "invitation not found", nil, domain.ErrNotFound)
}

func errInviteLinkNotFound() error {
	return domain.NewAppError(404, "TEAM_INVITE_LINK_NOT_FOUND", "teams.inviteLinkNotFound", "invite link not found", nil, domain.ErrNotFound)
}

func errPermissionDenied() error {
	return domain.NewAppError(403, "TEAM_PERMISSION_DENIED", "teams.permissionDenied", "team permission denied", nil, domain.ErrForbidden)
}

func errVersionConflict() error {
	return domain.NewAppError(409, "TEAM_VERSION_CONFLICT", "teams.versionConflict", "team version conflict", nil, domain.ErrPreconditionFailed)
}

func errMembershipExists() error {
	return domain.NewAppError(409, "TEAM_MEMBERSHIP_EXISTS", "teams.membershipExists", "account already belongs to a team", nil, domain.ErrConflict)
}

func errIdempotencyReused() error {
	return domain.NewAppError(409, "IDEMPOTENCY_KEY_REUSED", "teams.idempotencyReused", "idempotency key reused with a different request", nil, domain.ErrIdempotencyReused)
}

func errInternal(err error) error {
	return domain.NewAppError(500, "INTERNAL_ERROR", "api.internal", "internal server error", nil, err)
}

func inviteLinkGone(state domain.InviteLinkEffectiveState) error {
	switch state {
	case domain.InviteLinkStateExpired:
		return domain.NewAppError(410, "TEAM_INVITE_LINK_EXPIRED", "teams.inviteLinkExpired", "invite link expired", nil, domain.ErrNotFound)
	case domain.InviteLinkStateRevoked:
		return domain.NewAppError(410, "TEAM_INVITE_LINK_REVOKED", "teams.inviteLinkRevoked", "invite link revoked", nil, domain.ErrNotFound)
	case domain.InviteLinkStateExhausted:
		return domain.NewAppError(410, "TEAM_INVITE_LINK_EXHAUSTED", "teams.inviteLinkExhausted", "invite link exhausted", nil, domain.ErrNotFound)
	default:
		return errInviteLinkNotFound()
	}
}

func TeamJSONLimit() int64 { return teamJSONLimit }
