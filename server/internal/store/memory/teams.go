package memory

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

const (
	memReceiptTTL       = 7 * 24 * time.Hour
	memDefaultListLimit = 20
	memMaxListLimit     = 100
)

func memTeamErr(status int, code, key, msg string, underlying error) error {
	return domain.NewAppError(status, code, key, msg, nil, underlying)
}

func memErrTeamNotFound() error {
	return memTeamErr(404, "TEAM_NOT_FOUND", "teams.notFound", "team not found", domain.ErrNotFound)
}
func memErrInvitationNotFound() error {
	return memTeamErr(404, "TEAM_INVITATION_NOT_FOUND", "teams.invitationNotFound", "invitation not found", domain.ErrNotFound)
}
func memErrInviteLinkNotFound() error {
	return memTeamErr(404, "TEAM_INVITE_LINK_NOT_FOUND", "teams.inviteLinkNotFound", "invite link not found", domain.ErrNotFound)
}
func memErrResourceNotFound() error {
	return memTeamErr(404, "RESOURCE_NOT_FOUND", "teams.resourceNotFound", "resource not found", domain.ErrNotFound)
}
func memErrMembershipExists() error {
	return memTeamErr(409, "TEAM_MEMBERSHIP_EXISTS", "teams.membershipExists", "user already belongs to a team", domain.ErrConflict)
}
func memErrIdempotencyReused() error {
	return memTeamErr(409, "IDEMPOTENCY_KEY_REUSED", "teams.idempotencyKeyReused", "idempotency key reused with a different request", domain.ErrIdempotencyReused)
}
func memErrVersionConflict() error {
	return memTeamErr(409, "TEAM_VERSION_CONFLICT", "teams.versionConflict", "team version conflict", domain.ErrConflict)
}
func memErrInvitationPending() error {
	return memTeamErr(409, "TEAM_INVITATION_PENDING", "teams.invitationPending", "a pending invitation already exists", domain.ErrConflict)
}
func memErrPermissionDenied() error {
	return memTeamErr(403, "TEAM_PERMISSION_DENIED", "teams.permissionDenied", "permission denied", domain.ErrForbidden)
}
func memErrAccountNotAllowed() error {
	return memTeamErr(403, "ACCOUNT_ACTION_NOT_ALLOWED", "account.actionNotAllowed", "account cannot perform this action", domain.ErrForbidden)
}
func memErrReinvitationRequired() error {
	return memTeamErr(403, "TEAM_REINVITATION_REQUIRED", "teams.reinvitationRequired", "removed members must be reinvited by email", domain.ErrForbidden)
}
func memErrInvitationExpired() error {
	return memTeamErr(410, "TEAM_INVITATION_EXPIRED", "teams.invitationExpired", "invitation expired", domain.ErrNotFound)
}
func memErrInvitationRevoked() error {
	return memTeamErr(410, "TEAM_INVITATION_REVOKED", "teams.invitationRevoked", "invitation revoked", domain.ErrNotFound)
}
func memErrInviteLinkExpired() error {
	return memTeamErr(410, "TEAM_INVITE_LINK_EXPIRED", "teams.inviteLinkExpired", "invite link expired", domain.ErrNotFound)
}
func memErrInviteLinkRevoked() error {
	return memTeamErr(410, "TEAM_INVITE_LINK_REVOKED", "teams.inviteLinkRevoked", "invite link revoked", domain.ErrNotFound)
}
func memErrInviteLinkExhausted() error {
	return memTeamErr(410, "TEAM_INVITE_LINK_EXHAUSTED", "teams.inviteLinkExhausted", "invite link exhausted", domain.ErrNotFound)
}
func memErrInviteLinkAlreadyUsed() error {
	return memTeamErr(410, "TEAM_INVITE_LINK_ALREADY_USED", "teams.inviteLinkAlreadyUsed", "invite link already used", domain.ErrNotFound)
}
func memErrCommandUnavailable() error {
	return memTeamErr(410, "COMMAND_RESULT_UNAVAILABLE", "teams.commandResultUnavailable", "command result is no longer available", domain.ErrNotFound)
}
func memErrInvalid(msg string) error {
	return memTeamErr(400, "API_INVALID_ARGUMENT", "api.invalidArgument", msg, domain.ErrInvalidArgument)
}

func memNewID(prefix string) (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", err
	}
	id := prefix + token
	if len(id) != 30 {
		return "", fmt.Errorf("team id length %d != 30", len(id))
	}
	return id, nil
}

func memReceiptKey(actor, scope string, keyHash [32]byte) string {
	return actor + "\x00" + scope + "\x00" + hex.EncodeToString(keyHash[:])
}

func memJoinKey(linkID, userID string) string {
	return linkID + "\x00" + userID
}

func memBarrierKey(requestID, teamID string) string {
	return requestID + "\x00" + teamID
}

func memHashesEqual(a, b [32]byte) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func memClampLimit(limit int) int {
	if limit <= 0 {
		return memDefaultListLimit
	}
	if limit > memMaxListLimit {
		return memMaxListLimit
	}
	return limit
}

func memEncodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x1f")))
}

func memDecodeCursor(cursor string) []string {
	if strings.TrimSpace(cursor) == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil
	}
	return strings.Split(string(raw), "\x1f")
}

func memLikeContains(q, value string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(q))
}

func memCloneTeam(t *domain.Team) *domain.Team {
	if t == nil {
		return nil
	}
	c := *t
	c.Visibility = "private"
	if t.AvatarObjectID != nil {
		v := *t.AvatarObjectID
		c.AvatarObjectID = &v
	}
	if t.DissolvedAt != nil {
		v := *t.DissolvedAt
		c.DissolvedAt = &v
	}
	return &c
}

func memCloneMem(m *domain.TeamMembership) *domain.TeamMembership {
	if m == nil {
		return nil
	}
	c := *m
	if m.EndedAt != nil {
		v := *m.EndedAt
		c.EndedAt = &v
	}
	if m.EndReason != nil {
		v := *m.EndReason
		c.EndReason = &v
	}
	return &c
}

func memCloneInv(inv *domain.TeamInvitation) *domain.TeamInvitation {
	if inv == nil {
		return nil
	}
	c := *inv
	if inv.ActiveRecipientHash != nil {
		h := *inv.ActiveRecipientHash
		c.ActiveRecipientHash = &h
	}
	if inv.AcceptedByUserID != nil {
		v := *inv.AcceptedByUserID
		c.AcceptedByUserID = &v
	}
	if inv.AcceptedMembershipID != nil {
		v := *inv.AcceptedMembershipID
		c.AcceptedMembershipID = &v
	}
	c.RecipientCiphertext = append([]byte(nil), inv.RecipientCiphertext...)
	return &c
}

func memCloneLink(link *domain.TeamInviteLink) *domain.TeamInviteLink {
	if link == nil {
		return nil
	}
	c := *link
	c.TokenCiphertext = append([]byte(nil), link.TokenCiphertext...)
	if link.RevokedAt != nil {
		v := *link.RevokedAt
		c.RevokedAt = &v
	}
	return &c
}

func memCloneReceipt(r *domain.TeamCommandReceipt) *domain.TeamCommandReceipt {
	if r == nil {
		return nil
	}
	c := *r
	return &c
}

func memCloneSnap(s *domain.TeamAnalysisSnapshot) *domain.TeamAnalysisSnapshot {
	if s == nil {
		return nil
	}
	c := *s
	if s.ActiveRequestKey != nil {
		v := *s.ActiveRequestKey
		c.ActiveRequestKey = &v
	}
	if s.LeaseToken != nil {
		v := *s.LeaseToken
		c.LeaseToken = &v
	}
	if s.LeaseExpiresAt != nil {
		v := *s.LeaseExpiresAt
		c.LeaseExpiresAt = &v
	}
	if s.ErrorCode != nil {
		v := *s.ErrorCode
		c.ErrorCode = &v
	}
	return &c
}

func memCloneExport(j *domain.TeamExportJob) *domain.TeamExportJob {
	if j == nil {
		return nil
	}
	c := *j
	if j.LeaseToken != nil {
		v := *j.LeaseToken
		c.LeaseToken = &v
	}
	if j.LeaseExpiresAt != nil {
		v := *j.LeaseExpiresAt
		c.LeaseExpiresAt = &v
	}
	if j.ObjectKey != nil {
		v := *j.ObjectKey
		c.ObjectKey = &v
	}
	if j.FileSHA256 != nil {
		v := *j.FileSHA256
		c.FileSHA256 = &v
	}
	if j.FileSize != nil {
		v := *j.FileSize
		c.FileSize = &v
	}
	if j.ErrorCode != nil {
		v := *j.ErrorCode
		c.ErrorCode = &v
	}
	return &c
}

func memCloneObject(o *domain.TeamUploadObject) *domain.TeamUploadObject {
	if o == nil {
		return nil
	}
	c := *o
	if o.ExpiresAt != nil {
		v := *o.ExpiresAt
		c.ExpiresAt = &v
	}
	return &c
}

func memContext(team domain.Team, mem domain.TeamMembership, already bool) *domain.TeamContext {
	team.Visibility = "private"
	role := team.PublicRoleFor(mem.UserID, mem.BaseRole)
	return &domain.TeamContext{
		Team:          team,
		Membership:    mem,
		Role:          role,
		Permissions:   domain.PermissionsFor(role),
		AlreadyMember: already,
	}
}

func memPublicRole(team *domain.Team, mem *domain.TeamMembership) domain.TeamPublicRole {
	if team == nil || mem == nil || mem.EndedAt != nil {
		return ""
	}
	return team.PublicRoleFor(mem.UserID, mem.BaseRole)
}

func memIsManager(team *domain.Team, mem *domain.TeamMembership) bool {
	role := memPublicRole(team, mem)
	return role == domain.TeamRoleOwner || role == domain.TeamRoleAdmin
}

func memCanGrant(team *domain.Team, inviter *domain.TeamMembership, role domain.TeamBaseRole) bool {
	switch memPublicRole(team, inviter) {
	case domain.TeamRoleOwner:
		return role == domain.TeamBaseRoleAdmin || role == domain.TeamBaseRoleMember
	case domain.TeamRoleAdmin:
		return role == domain.TeamBaseRoleMember
	default:
		return false
	}
}

func memSharingDims() []domain.SharingDimension {
	return []domain.SharingDimension{domain.SharingBase, domain.SharingNamed, domain.SharingClassification, domain.SharingCost}
}

func (m *MemoryStore) requireActiveOnboarded(userID string) (*domain.User, error) {
	u := m.users[userID]
	if u == nil || u.AccountStatus != domain.AccountStatusActive || u.OnboardingCompletedAt == nil {
		return nil, memErrAccountNotAllowed()
	}
	return u, nil
}

func (m *MemoryStore) requireEmailVerified(userID string) (*domain.User, error) {
	u, err := m.requireActiveOnboarded(userID)
	if err != nil {
		return nil, err
	}
	if u.EmailVerifiedAt == nil {
		return nil, memErrAccountNotAllowed()
	}
	return u, nil
}

func (m *MemoryStore) checkReceipt(actor string, idem store.TeamsIdempotency, now time.Time) (*domain.TeamCommandReceipt, error) {
	rec := m.teamReceipts[memReceiptKey(actor, idem.Scope, idem.KeyHash)]
	if rec == nil {
		return nil, nil
	}
	if now.After(rec.ExpiresAt) {
		delete(m.teamReceipts, memReceiptKey(actor, idem.Scope, idem.KeyHash))
		return nil, nil
	}
	if !memHashesEqual(rec.RequestHash, idem.RequestHash) {
		return nil, memErrIdempotencyReused()
	}
	return memCloneReceipt(rec), nil
}

func (m *MemoryStore) insertReceipt(actor string, idem store.TeamsIdempotency, resultType, resultID string, now time.Time) (*domain.TeamCommandReceipt, error) {
	key := memReceiptKey(actor, idem.Scope, idem.KeyHash)
	if _, ok := m.teamReceipts[key]; ok {
		return nil, memErrIdempotencyReused()
	}
	rec := &domain.TeamCommandReceipt{
		ActorUserID:        actor,
		OperationScope:     idem.Scope,
		IdempotencyKeyHash: idem.KeyHash,
		RequestHash:        idem.RequestHash,
		ResultType:         resultType,
		ResultID:           resultID,
		CreatedAt:          now,
		ExpiresAt:          now.Add(memReceiptTTL),
	}
	m.teamReceipts[key] = rec
	return memCloneReceipt(rec), nil
}

func (m *MemoryStore) insertAudit(teamID, actor, action, targetType, targetID string, details any, now time.Time) error {
	id, err := memNewID(domain.AuditIDPrefix)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(details)
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	actorCopy := actor
	m.teamAudits[id] = &domain.TeamAuditEvent{
		AuditID:         id,
		TeamID:          teamID,
		ActorUserID:     &actorCopy,
		Action:          action,
		TargetType:      targetType,
		TargetID:        targetID,
		SafeDetailsJSON: string(raw),
		CreatedAt:       now,
	}
	return nil
}

func (m *MemoryStore) currentMembershipOf(teamID, userID string) *domain.TeamMembership {
	cur := m.userCurrentTeams[userID]
	if cur == nil || cur.TeamID != teamID {
		return nil
	}
	return m.teamMemberships[cur.MembershipID]
}

func (m *MemoryStore) latestHistory(teamID, userID string) *domain.TeamMembership {
	var best *domain.TeamMembership
	for _, mem := range m.teamMemberships {
		if mem.TeamID != teamID || mem.UserID != userID {
			continue
		}
		if best == nil || mem.JoinedAt.After(best.JoinedAt) || (mem.JoinedAt.Equal(best.JoinedAt) && mem.MembershipID > best.MembershipID) {
			best = mem
		}
	}
	return best
}

func (m *MemoryStore) occupyMembership(membershipID, teamID, userID string, role domain.TeamBaseRole, sharing domain.SharingFlags, at time.Time) error {
	if role != domain.TeamBaseRoleAdmin && role != domain.TeamBaseRoleMember {
		return memErrInvalid("membership base role must be admin or member")
	}
	if !sharing.Valid() {
		return memErrInvalid("invalid sharing flags")
	}
	if _, ok := m.userCurrentTeams[userID]; ok {
		return memErrMembershipExists()
	}
	m.teamMemberships[membershipID] = &domain.TeamMembership{
		MembershipID:   membershipID,
		TeamID:         teamID,
		UserID:         userID,
		BaseRole:       role,
		SharingVersion: 1,
		JoinedAt:       at,
	}
	m.userCurrentTeams[userID] = &domain.UserCurrentTeam{
		UserID:       userID,
		TeamID:       teamID,
		MembershipID: membershipID,
		JoinedAt:     at,
	}
	if err := m.insertEnabledGrants(membershipID, sharing, at); err != nil {
		return err
	}
	return m.registerOpenBarriers(userID, teamID, at)
}

func (m *MemoryStore) insertEnabledGrants(membershipID string, flags domain.SharingFlags, at time.Time) error {
	for _, dim := range memSharingDims() {
		if flags.DimensionEnabled(dim) {
			if err := m.insertActiveGrant(membershipID, dim, at); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *MemoryStore) insertActiveGrant(membershipID string, dim domain.SharingDimension, at time.Time) error {
	id, err := memNewID(domain.GrantIDPrefix)
	if err != nil {
		return err
	}
	active := string(dim)
	m.teamGrants[id] = &domain.TeamSharingGrant{
		GrantID:         id,
		MembershipID:    membershipID,
		Dimension:       dim,
		StartsAt:        at,
		ActiveDimension: &active,
	}
	return nil
}

func (m *MemoryStore) revokeActiveGrant(membershipID string, dim domain.SharingDimension, at time.Time) {
	for _, g := range m.teamGrants {
		if g.MembershipID == membershipID && g.ActiveDimension != nil && *g.ActiveDimension == string(dim) {
			end := at
			g.EndsAt = &end
			g.RevokedAt = &end
			g.ActiveDimension = nil
		}
	}
}

func (m *MemoryStore) revokeAllGrants(membershipID string, at time.Time) {
	for _, dim := range memSharingDims() {
		m.revokeActiveGrant(membershipID, dim, at)
	}
}

func (m *MemoryStore) sharingFlags(membershipID string) (domain.SharingFlags, map[string]time.Time) {
	var flags domain.SharingFlags
	effective := make(map[string]time.Time)
	for _, g := range m.teamGrants {
		if g.MembershipID != membershipID || g.ActiveDimension == nil {
			continue
		}
		switch g.Dimension {
		case domain.SharingBase:
			flags.Base = true
		case domain.SharingNamed:
			flags.Named = true
		case domain.SharingClassification:
			flags.Classification = true
		case domain.SharingCost:
			flags.Cost = true
		}
		effective[string(g.Dimension)] = g.StartsAt
	}
	return flags, effective
}

func (m *MemoryStore) applySharing(membershipID string, target domain.SharingFlags, at time.Time) (bool, error) {
	if !target.Valid() {
		return false, memErrInvalid("invalid sharing flags")
	}
	if !target.Base {
		target.Named = false
		target.Classification = false
		target.Cost = false
	}
	current, _ := m.sharingFlags(membershipID)
	changed := false
	for _, dim := range memSharingDims() {
		want := target.DimensionEnabled(dim)
		have := current.DimensionEnabled(dim)
		switch {
		case want && !have:
			if err := m.insertActiveGrant(membershipID, dim, at); err != nil {
				return false, err
			}
			changed = true
		case !want && have:
			m.revokeActiveGrant(membershipID, dim, at)
			changed = true
		}
	}
	return changed, nil
}

func (m *MemoryStore) registerOpenBarriers(userID, teamID string, now time.Time) error {
	for _, req := range m.deletionRequests {
		if req.UserID == nil || *req.UserID != userID {
			continue
		}
		switch req.RequestStatus {
		case domain.DeletionStatusPending, domain.DeletionStatusRunning, domain.DeletionStatusFailed:
			key := memBarrierKey(req.RequestID, teamID)
			if existing := m.teamBarriers[key]; existing != nil && existing.ReleasedAt == nil {
				continue
			}
			blocked := now
			m.teamBarriers[key] = &domain.TeamDeletionBarrier{
				DeletionRequestID: req.RequestID,
				TeamID:            teamID,
				BlockedAt:         blocked,
			}
		}
	}
	return nil
}

func (m *MemoryStore) bumpAuth(teamID string) uint64 {
	if team := m.teams[teamID]; team != nil {
		team.AuthRevision++
		return team.AuthRevision
	}
	return 0
}

func (m *MemoryStore) replayTeamContext(actorUserID, teamID string) (*domain.TeamContext, error) {
	team := m.teams[teamID]
	mem := m.currentMembershipOf(teamID, actorUserID)
	if team == nil || mem == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrCommandUnavailable()
	}
	return memContext(*memCloneTeam(team), *memCloneMem(mem), false), nil
}

func (m *MemoryStore) closeMembership(mem *domain.TeamMembership, reason domain.TeamMembershipEndReason, now time.Time) {
	end := now
	mem.EndedAt = &end
	mem.EndReason = &reason
	delete(m.userCurrentTeams, mem.UserID)
	m.revokeAllGrants(mem.MembershipID, now)
	m.revokeInvitesFromUser(mem.TeamID, mem.UserID, now)
	m.revokeLinksFromUser(mem.TeamID, mem.UserID, now)
	m.revokePendingForEmail(mem.TeamID, mem.UserID)
}

func (m *MemoryStore) revokeInvitesFromUser(teamID, userID string, now time.Time) {
	for _, inv := range m.teamInvitations {
		if inv.TeamID == teamID && inv.InviterUserID == userID && inv.Status == domain.InvitationPending {
			inv.Status = domain.InvitationRevoked
			inv.ActiveRecipientHash = nil
			inv.Version++
			m.cancelOutbox(inv.InvitationID, now)
		}
	}
}

func (m *MemoryStore) revokeLinksFromUser(teamID, userID string, now time.Time) {
	for _, link := range m.teamInviteLinks {
		if link.TeamID == teamID && link.CreatorUserID == userID && link.Status == domain.InviteLinkActive {
			rev := now
			link.Status = domain.InviteLinkRevoked
			link.RevokedAt = &rev
			link.Version++
			link.TokenCiphertext = nil
		}
	}
}

func (m *MemoryStore) revokePendingForEmail(teamID, userID string) {
	u := m.users[userID]
	if u == nil || u.EmailLookupHash == nil {
		return
	}
	for _, inv := range m.teamInvitations {
		if inv.TeamID == teamID && inv.Status == domain.InvitationPending && memHashesEqual(inv.RecipientLookupHash, *u.EmailLookupHash) {
			inv.Status = domain.InvitationRevoked
			inv.ActiveRecipientHash = nil
			inv.Version++
		}
	}
}

func (m *MemoryStore) cancelOutbox(invitationID string, now time.Time) {
	for _, mail := range m.emailOutbox {
		if mail.TeamInvitationID != nil && *mail.TeamInvitationID == invitationID {
			if mail.DeliveryStatus == "pending" || mail.DeliveryStatus == "sending" {
				mail.DeliveryStatus = "cancelled"
				mail.UpdatedAt = now
			}
		}
	}
}

func (m *MemoryStore) expireOverdueInvites(teamID string, now time.Time) {
	for _, inv := range m.teamInvitations {
		if inv.TeamID == teamID && inv.Status == domain.InvitationPending && !inv.ExpiresAt.After(now) {
			inv.Status = domain.InvitationExpired
			inv.ActiveRecipientHash = nil
			inv.Version++
		}
	}
}

func (m *MemoryStore) GetCurrentTeam(ctx context.Context, userID string) (*domain.UserCurrentTeam, *domain.Team, *domain.TeamMembership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cur := m.userCurrentTeams[userID]
	if cur == nil {
		return nil, nil, nil, nil
	}
	c := *cur
	return &c, memCloneTeam(m.teams[cur.TeamID]), memCloneMem(m.teamMemberships[cur.MembershipID]), nil
}

func (m *MemoryStore) GetTeam(ctx context.Context, teamID string) (*domain.Team, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	team := m.teams[teamID]
	if team == nil {
		return nil, memErrTeamNotFound()
	}
	return memCloneTeam(team), nil
}

func (m *MemoryStore) GetMembership(ctx context.Context, teamID, membershipID string) (*domain.TeamMembership, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mem := m.teamMemberships[membershipID]
	if mem == nil || mem.TeamID != teamID {
		return nil, memErrResourceNotFound()
	}
	return memCloneMem(mem), nil
}

func (m *MemoryStore) ListMembers(ctx context.Context, teamID string, query string, cursor string, limit int) ([]domain.TeamMembership, []domain.User, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit = memClampLimit(limit)
	type row struct {
		mem  domain.TeamMembership
		user domain.User
	}
	var rows []row
	q := strings.TrimSpace(query)
	for _, mem := range m.teamMemberships {
		if mem.TeamID != teamID || mem.EndedAt != nil {
			continue
		}
		u := m.users[mem.UserID]
		if u == nil {
			continue
		}
		if q != "" {
			handle := ""
			if u.Handle != nil {
				handle = *u.Handle
			}
			if !memLikeContains(q, u.DisplayName) && !memLikeContains(q, handle) {
				continue
			}
		}
		rows = append(rows, row{mem: *memCloneMem(mem), user: *u})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].mem.JoinedAt.Equal(rows[j].mem.JoinedAt) {
			return rows[i].mem.MembershipID < rows[j].mem.MembershipID
		}
		return rows[i].mem.JoinedAt.Before(rows[j].mem.JoinedAt)
	})
	if parts := memDecodeCursor(cursor); len(parts) == 2 {
		if ts, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			filtered := rows[:0]
			for _, r := range rows {
				if r.mem.JoinedAt.After(ts) || (r.mem.JoinedAt.Equal(ts) && r.mem.MembershipID > parts[1]) {
					filtered = append(filtered, r)
				}
			}
			rows = filtered
		}
	}
	next := ""
	if len(rows) > limit {
		last := rows[limit-1].mem
		rows = rows[:limit]
		next = memEncodeCursor(last.JoinedAt.UTC().Format(time.RFC3339Nano), last.MembershipID)
	}
	mems := make([]domain.TeamMembership, 0, len(rows))
	users := make([]domain.User, 0, len(rows))
	for _, r := range rows {
		mems = append(mems, r.mem)
		users = append(users, r.user)
	}
	return mems, users, next, nil
}

func (m *MemoryStore) ListInvitationsForEmail(ctx context.Context, emailLookupHash [32]byte, cursor string, limit int) ([]domain.TeamInvitation, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []domain.TeamInvitation
	for _, inv := range m.teamInvitations {
		if memHashesEqual(inv.RecipientLookupHash, emailLookupHash) {
			list = append(list, *memCloneInv(inv))
		}
	}
	return memPageInvitations(list, cursor, limit)
}

func (m *MemoryStore) ListTeamInvitations(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamInvitation, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var list []domain.TeamInvitation
	for _, inv := range m.teamInvitations {
		if inv.TeamID == teamID {
			list = append(list, *memCloneInv(inv))
		}
	}
	return memPageInvitations(list, cursor, limit)
}

func memPageInvitations(list []domain.TeamInvitation, cursor string, limit int) ([]domain.TeamInvitation, string, error) {
	limit = memClampLimit(limit)
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].InvitationID < list[j].InvitationID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	if parts := memDecodeCursor(cursor); len(parts) == 2 {
		if ts, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			filtered := list[:0]
			for _, inv := range list {
				if inv.CreatedAt.After(ts) || (inv.CreatedAt.Equal(ts) && inv.InvitationID > parts[1]) {
					filtered = append(filtered, inv)
				}
			}
			list = filtered
		}
	}
	next := ""
	if len(list) > limit {
		last := list[limit-1]
		list = list[:limit]
		next = memEncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.InvitationID)
	}
	return list, next, nil
}

func (m *MemoryStore) GetInvitation(ctx context.Context, invitationID string) (*domain.TeamInvitation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	inv := m.teamInvitations[invitationID]
	if inv == nil {
		return nil, memErrInvitationNotFound()
	}
	return memCloneInv(inv), nil
}

func (m *MemoryStore) ListInviteLinks(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamInviteLink, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit = memClampLimit(limit)
	var list []domain.TeamInviteLink
	for _, link := range m.teamInviteLinks {
		if link.TeamID == teamID {
			list = append(list, *memCloneLink(link))
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].LinkID < list[j].LinkID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	if parts := memDecodeCursor(cursor); len(parts) == 2 {
		if ts, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			filtered := list[:0]
			for _, link := range list {
				if link.CreatedAt.After(ts) || (link.CreatedAt.Equal(ts) && link.LinkID > parts[1]) {
					filtered = append(filtered, link)
				}
			}
			list = filtered
		}
	}
	next := ""
	if len(list) > limit {
		last := list[limit-1]
		list = list[:limit]
		next = memEncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.LinkID)
	}
	return list, next, nil
}

func (m *MemoryStore) GetInviteLink(ctx context.Context, linkID string) (*domain.TeamInviteLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	link := m.teamInviteLinks[linkID]
	if link == nil {
		return nil, memErrInviteLinkNotFound()
	}
	return memCloneLink(link), nil
}

func (m *MemoryStore) GetInviteLinkByTokenHash(ctx context.Context, tokenHash [32]byte) (*domain.TeamInviteLink, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, link := range m.teamInviteLinks {
		if memHashesEqual(link.TokenHash, tokenHash) {
			return memCloneLink(link), nil
		}
	}
	return nil, memErrInviteLinkNotFound()
}

func (m *MemoryStore) GetMySharing(ctx context.Context, teamID, userID string) (*domain.TeamSharingState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mem := m.currentMembershipOf(teamID, userID)
	if mem == nil {
		return nil, memErrTeamNotFound()
	}
	flags, effective := m.sharingFlags(mem.MembershipID)
	return &domain.TeamSharingState{
		MembershipID:   mem.MembershipID,
		SharingVersion: mem.SharingVersion,
		Sharing:        flags,
		EffectiveFrom:  effective,
	}, nil
}

func (m *MemoryStore) ListAuditEvents(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamAuditEvent, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit = memClampLimit(limit)
	var list []domain.TeamAuditEvent
	for _, ev := range m.teamAudits {
		if ev.TeamID == teamID {
			c := *ev
			if ev.ActorUserID != nil {
				v := *ev.ActorUserID
				c.ActorUserID = &v
			}
			list = append(list, c)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].AuditID < list[j].AuditID
		}
		return list[i].CreatedAt.Before(list[j].CreatedAt)
	})
	if parts := memDecodeCursor(cursor); len(parts) == 2 {
		if ts, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			filtered := list[:0]
			for _, ev := range list {
				if ev.CreatedAt.After(ts) || (ev.CreatedAt.Equal(ts) && ev.AuditID > parts[1]) {
					filtered = append(filtered, ev)
				}
			}
			list = filtered
		}
	}
	next := ""
	if len(list) > limit {
		last := list[limit-1]
		list = list[:limit]
		next = memEncodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.AuditID)
	}
	return list, next, nil
}

func (m *MemoryStore) ListExports(ctx context.Context, teamID, requesterUserID string) ([]domain.TeamExportJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.TeamExportJob
	for _, job := range m.teamExports {
		if job.TeamID == teamID && job.RequesterUserID == requesterUserID {
			out = append(out, *memCloneExport(job))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ExportID > out[j].ExportID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemoryStore) GetExport(ctx context.Context, teamID, exportID string) (*domain.TeamExportJob, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	job := m.teamExports[exportID]
	if job == nil || job.TeamID != teamID {
		return nil, memErrResourceNotFound()
	}
	return memCloneExport(job), nil
}

func (m *MemoryStore) GetAvatarObject(ctx context.Context, teamID, objectID string) (*domain.TeamUploadObject, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	obj := m.teamUploadObjects[objectID]
	if obj == nil || obj.TeamID != teamID {
		return nil, memErrResourceNotFound()
	}
	return memCloneObject(obj), nil
}

func (m *MemoryStore) HasOpenDeletionBarrier(ctx context.Context, teamID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, b := range m.teamBarriers {
		if b.TeamID == teamID && b.ReleasedAt == nil {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemoryStore) CreateTeamTx(ctx context.Context, in store.CreateTeamTxInput) (*store.CreateTeamTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !in.Sharing.Valid() {
		return nil, memErrInvalid("invalid sharing flags")
	}
	if _, err := m.requireActiveOnboarded(in.ActorUserID); err != nil {
		return nil, err
	}
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		ctxn, err := m.replayTeamContext(in.ActorUserID, rec.ResultID)
		if err != nil {
			return nil, err
		}
		return &store.CreateTeamTxResult{Outcome: store.TeamsTxReplay, Context: ctxn, Receipt: rec}, nil
	}
	if m.userCurrentTeams[in.ActorUserID] != nil {
		return nil, memErrMembershipExists()
	}
	team := in.Team
	if team.TeamID == "" {
		id, err := memNewID(domain.TeamIDPrefix)
		if err != nil {
			return nil, err
		}
		team.TeamID = id
	}
	mem := in.Membership
	if mem.MembershipID == "" {
		id, err := memNewID(domain.MembershipIDPrefix)
		if err != nil {
			return nil, err
		}
		mem.MembershipID = id
	}
	team.OwnerUserID = in.ActorUserID
	team.Status = domain.TeamStatusActive
	if team.ProfileVersion == 0 {
		team.ProfileVersion = 1
	}
	if team.AuthRevision == 0 {
		team.AuthRevision = 1
	}
	team.Visibility = "private"
	team.CreatedAt = now
	copied := team
	m.teams[team.TeamID] = &copied
	if err := m.occupyMembership(mem.MembershipID, team.TeamID, in.ActorUserID, domain.TeamBaseRoleAdmin, in.Sharing, now); err != nil {
		delete(m.teams, team.TeamID)
		return nil, err
	}
	m.teamRevisions[team.TeamID] = &domain.TeamSourceRevision{TeamID: team.TeamID, SourceRevision: 0, ChangedAt: now}
	if err := m.insertAudit(team.TeamID, in.ActorUserID, "create", "team", team.TeamID, map[string]any{"result": "created"}, now); err != nil {
		return nil, err
	}
	rec, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now)
	if err != nil {
		return nil, err
	}
	return &store.CreateTeamTxResult{
		Outcome: store.TeamsTxChanged,
		Context: memContext(*memCloneTeam(m.teams[team.TeamID]), *memCloneMem(m.teamMemberships[mem.MembershipID]), false),
		Receipt: rec,
	}, nil
}

func (m *MemoryStore) UpdateTeamProfileTx(ctx context.Context, in store.UpdateTeamProfileTxInput) (*domain.Team, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrTeamNotFound()
	}
	mem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	if !memIsManager(team, mem) {
		if mem == nil {
			return nil, memErrTeamNotFound()
		}
		return nil, memErrPermissionDenied()
	}
	if team.ProfileVersion != in.ExpectedProfileVersion {
		return nil, memErrVersionConflict()
	}
	if in.Name != nil {
		team.Name = *in.Name
	}
	if in.Description != nil {
		team.Description = *in.Description
	}
	team.ProfileVersion++
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "profile_change", "team", in.TeamID, map[string]any{
		"profileVersion": strconv.FormatUint(team.ProfileVersion, 10),
	}, in.Now.UTC())
	return memCloneTeam(team), nil
}

func (m *MemoryStore) CreateInvitationTx(ctx context.Context, in store.CreateInvitationTxInput) (*store.CreateInvitationTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.requireActiveOnboarded(in.ActorUserID); err != nil {
		return nil, err
	}
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrTeamNotFound()
	}
	actorMem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	if !memCanGrant(team, actorMem, in.InvitedRole) {
		if actorMem == nil {
			return nil, memErrTeamNotFound()
		}
		return nil, memErrPermissionDenied()
	}
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		inv := m.teamInvitations[rec.ResultID]
		if inv == nil {
			return nil, memErrCommandUnavailable()
		}
		return &store.CreateInvitationTxResult{Outcome: store.TeamsTxReplay, Invitation: memCloneInv(inv)}, nil
	}
	m.expireOverdueInvites(in.TeamID, now)
	inv := in.Invitation
	if inv.InvitationID == "" {
		id, err := memNewID(domain.InvitationIDPrefix)
		if err != nil {
			return nil, err
		}
		inv.InvitationID = id
	}
	for _, existing := range m.teamInvitations {
		if existing.TeamID == in.TeamID && existing.Status == domain.InvitationPending && memHashesEqual(existing.RecipientLookupHash, inv.RecipientLookupHash) {
			return nil, memErrInvitationPending()
		}
	}
	inv.TeamID = in.TeamID
	inv.InviterUserID = in.ActorUserID
	inv.InvitedRole = in.InvitedRole
	inv.Status = domain.InvitationPending
	inv.Version = 1
	inv.CreatedAt = now
	if inv.ExpiresAt.IsZero() {
		inv.ExpiresAt = now.Add(7 * 24 * time.Hour)
	}
	h := inv.RecipientLookupHash
	inv.ActiveRecipientHash = &h
	copied := inv
	m.teamInvitations[inv.InvitationID] = &copied
	if in.Outbox.EmailID != "" {
		mail := in.Outbox
		mail.TeamInvitationID = &inv.InvitationID
		ver := inv.Version
		mail.TeamInvitationVersion = &ver
		if mail.Locale == "" {
			mail.Locale = "en-US"
		}
		m.emailOutbox[mail.EmailID] = &mail
	}
	if err := m.insertAudit(in.TeamID, in.ActorUserID, "invite", "invitation", inv.InvitationID, map[string]any{"role": string(inv.InvitedRole)}, now); err != nil {
		return nil, err
	}
	if _, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "invitation", inv.InvitationID, now); err != nil {
		return nil, err
	}
	return &store.CreateInvitationTxResult{Outcome: store.TeamsTxChanged, Invitation: memCloneInv(&copied)}, nil
}

func (m *MemoryStore) RevokeInvitationTx(ctx context.Context, actorUserID, teamID, invitationID string, expectedVersion uint64, idem store.TeamsIdempotency, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tNow := now.UTC()
	if rec, err := m.checkReceipt(actorUserID, idem, tNow); err != nil {
		return err
	} else if rec != nil {
		return nil
	}
	team := m.teams[teamID]
	inv := m.teamInvitations[invitationID]
	if team == nil || inv == nil || inv.TeamID != teamID {
		return memErrInvitationNotFound()
	}
	mem := m.currentMembershipOf(teamID, actorUserID)
	if !memCanGrant(team, mem, inv.InvitedRole) && !memIsManager(team, mem) {
		if mem == nil {
			return memErrTeamNotFound()
		}
		return memErrPermissionDenied()
	}
	if inv.Version != expectedVersion {
		return memErrVersionConflict()
	}
	if inv.Status == domain.InvitationPending {
		inv.Status = domain.InvitationRevoked
		inv.ActiveRecipientHash = nil
		inv.Version++
		m.cancelOutbox(invitationID, tNow)
		_ = m.insertAudit(teamID, actorUserID, "revoke", "invitation", invitationID, map[string]any{"result": "revoked"}, tNow)
	}
	_, err := m.insertReceipt(actorUserID, idem, "invitation", invitationID, tNow)
	return err
}

func (m *MemoryStore) ResendInvitationTx(ctx context.Context, actorUserID, teamID, invitationID string, expectedVersion uint64, newInvitation domain.TeamInvitation, outbox domain.EmailOutbox, idem store.TeamsIdempotency, now time.Time) (*domain.TeamInvitation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tNow := now.UTC()
	if rec, err := m.checkReceipt(actorUserID, idem, tNow); err != nil {
		return nil, err
	} else if rec != nil {
		inv := m.teamInvitations[rec.ResultID]
		if inv == nil {
			return nil, memErrCommandUnavailable()
		}
		return memCloneInv(inv), nil
	}
	team := m.teams[teamID]
	old := m.teamInvitations[invitationID]
	if team == nil || old == nil || old.TeamID != teamID {
		return nil, memErrInvitationNotFound()
	}
	mem := m.currentMembershipOf(teamID, actorUserID)
	if !memCanGrant(team, mem, old.InvitedRole) {
		if mem == nil {
			return nil, memErrTeamNotFound()
		}
		return nil, memErrPermissionDenied()
	}
	if old.Version != expectedVersion || old.Status != domain.InvitationPending {
		if old.Version != expectedVersion {
			return nil, memErrVersionConflict()
		}
		return nil, memErrInvitationNotFound()
	}
	old.Status = domain.InvitationRevoked
	old.ActiveRecipientHash = nil
	old.Version++
	m.cancelOutbox(invitationID, tNow)
	if newInvitation.InvitationID == "" {
		id, err := memNewID(domain.InvitationIDPrefix)
		if err != nil {
			return nil, err
		}
		newInvitation.InvitationID = id
	}
	newInvitation.TeamID = teamID
	newInvitation.InviterUserID = actorUserID
	if newInvitation.InvitedRole == "" {
		newInvitation.InvitedRole = old.InvitedRole
	}
	newInvitation.RecipientLookupHash = old.RecipientLookupHash
	if len(newInvitation.RecipientCiphertext) == 0 {
		newInvitation.RecipientCiphertext = append([]byte(nil), old.RecipientCiphertext...)
	}
	newInvitation.Status = domain.InvitationPending
	newInvitation.Version = 1
	newInvitation.CreatedAt = tNow
	if !newInvitation.ExpiresAt.After(tNow) {
		newInvitation.ExpiresAt = tNow.Add(7 * 24 * time.Hour)
	}
	h := newInvitation.RecipientLookupHash
	newInvitation.ActiveRecipientHash = &h
	copied := newInvitation
	m.teamInvitations[newInvitation.InvitationID] = &copied
	if outbox.EmailID != "" {
		mail := outbox
		mail.TeamInvitationID = &newInvitation.InvitationID
		m.emailOutbox[mail.EmailID] = &mail
	}
	_ = m.insertAudit(teamID, actorUserID, "resend", "invitation", newInvitation.InvitationID, map[string]any{"replaced": invitationID}, tNow)
	if _, err := m.insertReceipt(actorUserID, idem, "invitation", newInvitation.InvitationID, tNow); err != nil {
		return nil, err
	}
	return memCloneInv(&copied), nil
}

func (m *MemoryStore) AcceptInvitationTx(ctx context.Context, in store.AcceptInvitationTxInput) (*store.AcceptInvitationTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !in.Sharing.Valid() {
		return nil, memErrInvalid("invalid sharing flags")
	}
	if _, err := m.requireEmailVerified(in.ActorUserID); err != nil {
		return nil, err
	}
	now := in.Now.UTC()
	inv := m.teamInvitations[in.InvitationID]
	if inv == nil {
		return nil, memErrInvitationNotFound()
	}
	if !memHashesEqual(inv.RecipientLookupHash, in.VerifiedEmailLookupHash) {
		return nil, memErrInvitationNotFound()
	}
	team := m.teams[inv.TeamID]
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		ctxn, err := m.replayTeamContext(in.ActorUserID, inv.TeamID)
		if err != nil {
			return nil, err
		}
		return &store.AcceptInvitationTxResult{Outcome: store.TeamsTxReplay, Context: ctxn}, nil
	}
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrTeamNotFound()
	}
	current := m.userCurrentTeams[in.ActorUserID]
	history := m.latestHistory(team.TeamID, in.ActorUserID)
	if inv.Status == domain.InvitationAccepted {
		if inv.AcceptedByUserID != nil && *inv.AcceptedByUserID == in.ActorUserID && inv.AcceptedMembershipID != nil {
			if current != nil && current.MembershipID == *inv.AcceptedMembershipID {
				_, _ = m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now)
				return &store.AcceptInvitationTxResult{
					Outcome: store.TeamsTxAlreadyMember,
					Context: memContext(*memCloneTeam(team), *memCloneMem(m.teamMemberships[current.MembershipID]), true),
				}, nil
			}
		}
		return nil, memErrInvitationNotFound()
	}
	if inv.Status == domain.InvitationRevoked {
		return nil, memErrInvitationRevoked()
	}
	if inv.Status == domain.InvitationExpired || !inv.ExpiresAt.After(now) {
		if inv.Status == domain.InvitationPending {
			inv.Status = domain.InvitationExpired
			inv.ActiveRecipientHash = nil
			inv.Version++
		}
		return nil, memErrInvitationExpired()
	}
	if inv.Status != domain.InvitationPending {
		return nil, memErrInvitationNotFound()
	}
	if inv.Version != in.ExpectedVersion {
		return nil, memErrVersionConflict()
	}
	if !memCanGrant(team, m.currentMembershipOf(team.TeamID, inv.InviterUserID), inv.InvitedRole) {
		return nil, memErrInvitationNotFound()
	}
	if history != nil && history.EndReason != nil && *history.EndReason == domain.TeamEndReasonRemoved {
		if history.EndedAt == nil || !inv.CreatedAt.After(*history.EndedAt) {
			return nil, memErrInvitationNotFound()
		}
	}
	if current != nil && current.TeamID != team.TeamID {
		return nil, memErrMembershipExists()
	}
	if current != nil && current.TeamID == team.TeamID {
		m.markAccepted(inv, in.ActorUserID, current.MembershipID)
		_ = m.insertAudit(team.TeamID, in.ActorUserID, "accept", "invitation", inv.InvitationID, map[string]any{"alreadyMember": true}, now)
		_, _ = m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now)
		return &store.AcceptInvitationTxResult{
			Outcome: store.TeamsTxAlreadyMember,
			Context: memContext(*memCloneTeam(team), *memCloneMem(m.teamMemberships[current.MembershipID]), true),
		}, nil
	}
	membershipID, err := memNewID(domain.MembershipIDPrefix)
	if err != nil {
		return nil, err
	}
	if err := m.occupyMembership(membershipID, team.TeamID, in.ActorUserID, inv.InvitedRole, in.Sharing, now); err != nil {
		return nil, err
	}
	m.markAccepted(inv, in.ActorUserID, membershipID)
	m.bumpAuth(team.TeamID)
	_ = m.insertAudit(team.TeamID, in.ActorUserID, "accept", "invitation", inv.InvitationID, map[string]any{"membershipId": membershipID}, now)
	if _, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
		return nil, err
	}
	return &store.AcceptInvitationTxResult{
		Outcome: store.TeamsTxChanged,
		Context: memContext(*memCloneTeam(team), *memCloneMem(m.teamMemberships[membershipID]), false),
	}, nil
}

func (m *MemoryStore) markAccepted(inv *domain.TeamInvitation, userID, membershipID string) {
	inv.Status = domain.InvitationAccepted
	inv.ActiveRecipientHash = nil
	inv.AcceptedByUserID = &userID
	inv.AcceptedMembershipID = &membershipID
	inv.Version++
}

func (m *MemoryStore) CreateInviteLinkTx(ctx context.Context, in store.CreateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createInviteLinkLocked(in)
}

func (m *MemoryStore) createInviteLinkLocked(in store.CreateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	if _, err := m.requireActiveOnboarded(in.ActorUserID); err != nil {
		return nil, err
	}
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrTeamNotFound()
	}
	mem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	if !memIsManager(team, mem) {
		if mem == nil {
			return nil, memErrTeamNotFound()
		}
		return nil, memErrPermissionDenied()
	}
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		link := m.teamInviteLinks[rec.ResultID]
		if link == nil {
			return nil, memErrCommandUnavailable()
		}
		return &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxReplay, Link: memCloneLink(link)}, nil
	}
	link := in.Link
	if link.LinkID == "" {
		id, err := memNewID(domain.InviteLinkIDPrefix)
		if err != nil {
			return nil, err
		}
		link.LinkID = id
	}
	if link.MaxUses < domain.InviteLinkMaxUsesMin || link.MaxUses > domain.InviteLinkMaxUsesMax {
		return nil, memErrInvalid("maxUses must be between 1 and 100")
	}
	if !link.ExpiresAt.After(now) {
		return nil, memErrInvalid("invite link expiry must be in the future")
	}
	link.TeamID = in.TeamID
	link.CreatorUserID = in.ActorUserID
	link.Status = domain.InviteLinkActive
	if link.Version == 0 {
		link.Version = 1
	}
	link.UsedCount = 0
	link.CreatedAt = now
	copied := link
	m.teamInviteLinks[link.LinkID] = &copied
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "invite_link_create", "invite_link", link.LinkID, map[string]any{"maxUses": link.MaxUses}, now)
	if _, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "invite_link", link.LinkID, now); err != nil {
		return nil, err
	}
	return &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxChanged, Link: memCloneLink(&copied)}, nil
}

func (m *MemoryStore) RevokeInviteLinkTx(ctx context.Context, in store.RevokeInviteLinkTxInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return err
	} else if rec != nil {
		return nil
	}
	team := m.teams[in.TeamID]
	link := m.teamInviteLinks[in.LinkID]
	if team == nil || link == nil || link.TeamID != in.TeamID {
		return memErrInviteLinkNotFound()
	}
	mem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	if !memIsManager(team, mem) {
		if mem == nil {
			return memErrTeamNotFound()
		}
		return memErrPermissionDenied()
	}
	if link.Version != in.ExpectedVersion {
		return memErrVersionConflict()
	}
	if link.Status == domain.InviteLinkActive {
		rev := now
		link.Status = domain.InviteLinkRevoked
		link.RevokedAt = &rev
		link.Version++
		link.TokenCiphertext = nil
		_ = m.insertAudit(in.TeamID, in.ActorUserID, "invite_link_revoke", "invite_link", in.LinkID, map[string]any{"result": "revoked"}, now)
	}
	_, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "invite_link", in.LinkID, now)
	return err
}

func (m *MemoryStore) RegenerateInviteLinkTx(ctx context.Context, in store.RegenerateInviteLinkTxInput) (*store.CreateInviteLinkTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		link := m.teamInviteLinks[rec.ResultID]
		if link == nil {
			return nil, memErrCommandUnavailable()
		}
		return &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxReplay, Link: memCloneLink(link)}, nil
	}
	old := m.teamInviteLinks[in.LinkID]
	if old == nil || old.TeamID != in.TeamID {
		return nil, memErrInviteLinkNotFound()
	}
	if old.Version != in.ExpectedVersion {
		return nil, memErrVersionConflict()
	}
	if old.Status != domain.InviteLinkActive || !old.ExpiresAt.After(now) || old.UsedCount >= old.MaxUses {
		return &store.CreateInviteLinkTxResult{Outcome: store.TeamsTxReplay, Link: memCloneLink(old)}, nil
	}
	rev := now
	old.Status = domain.InviteLinkRevoked
	old.RevokedAt = &rev
	old.Version++
	old.TokenCiphertext = nil
	created, err := m.createInviteLinkLocked(store.CreateInviteLinkTxInput{
		ActorUserID: in.ActorUserID,
		TeamID:      in.TeamID,
		Link:        in.NewLink,
		Idempotency: in.Idempotency,
		Now:         now,
	})
	if err != nil {
		return nil, err
	}
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "invite_link_regenerate", "invite_link", created.Link.LinkID, map[string]any{"replaced": in.LinkID}, now)
	return created, nil
}

func (m *MemoryStore) AcceptInviteLinkTx(ctx context.Context, in store.AcceptInviteLinkTxInput) (*store.AcceptInviteLinkTxResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !in.Sharing.Valid() {
		return nil, memErrInvalid("invalid sharing flags")
	}
	if _, err := m.requireEmailVerified(in.ActorUserID); err != nil {
		return nil, err
	}
	now := in.Now.UTC()
	link := m.teamInviteLinks[in.LinkID]
	if link == nil || !memHashesEqual(link.TokenHash, in.TokenHash) {
		return nil, memErrInviteLinkNotFound()
	}
	team := m.teams[link.TeamID]
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		ctxn, err := m.replayTeamContext(in.ActorUserID, link.TeamID)
		if err != nil {
			return nil, memErrInviteLinkAlreadyUsed()
		}
		return &store.AcceptInviteLinkTxResult{Outcome: store.TeamsTxReplay, Context: ctxn}, nil
	}
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrInviteLinkNotFound()
	}
	current := m.userCurrentTeams[in.ActorUserID]
	history := m.latestHistory(team.TeamID, in.ActorUserID)
	join := m.teamInviteLinkJoins[memJoinKey(in.LinkID, in.ActorUserID)]
	if join != nil {
		if current != nil && current.MembershipID == join.MembershipID {
			_, _ = m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now)
			return &store.AcceptInviteLinkTxResult{
				Outcome: store.TeamsTxAlreadyMember,
				Context: memContext(*memCloneTeam(team), *memCloneMem(m.teamMemberships[current.MembershipID]), true),
			}, nil
		}
		return nil, memErrInviteLinkAlreadyUsed()
	}
	if current != nil && current.TeamID != team.TeamID {
		return nil, memErrMembershipExists()
	}
	if current != nil && current.TeamID == team.TeamID {
		_, _ = m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now)
		return &store.AcceptInviteLinkTxResult{
			Outcome: store.TeamsTxAlreadyMember,
			Context: memContext(*memCloneTeam(team), *memCloneMem(m.teamMemberships[current.MembershipID]), true),
		}, nil
	}
	if history != nil && history.EndReason != nil && *history.EndReason == domain.TeamEndReasonRemoved {
		return nil, memErrReinvitationRequired()
	}
	if link.Status != domain.InviteLinkActive {
		return nil, memErrInviteLinkRevoked()
	}
	if !link.ExpiresAt.After(now) {
		return nil, memErrInviteLinkExpired()
	}
	if link.Version != in.ExpectedVersion {
		return nil, memErrVersionConflict()
	}
	if !memIsManager(team, m.currentMembershipOf(team.TeamID, link.CreatorUserID)) {
		return nil, memErrInviteLinkNotFound()
	}
	if link.UsedCount >= link.MaxUses {
		return nil, memErrInviteLinkExhausted()
	}
	link.UsedCount++
	membershipID, err := memNewID(domain.MembershipIDPrefix)
	if err != nil {
		link.UsedCount--
		return nil, err
	}
	if err := m.occupyMembership(membershipID, team.TeamID, in.ActorUserID, domain.TeamBaseRoleMember, in.Sharing, now); err != nil {
		link.UsedCount--
		return nil, err
	}
	m.teamInviteLinkJoins[memJoinKey(link.LinkID, in.ActorUserID)] = &domain.TeamInviteLinkJoin{
		LinkID: link.LinkID, UserID: in.ActorUserID, MembershipID: membershipID, JoinedAt: now,
	}
	m.bumpAuth(team.TeamID)
	_ = m.insertAudit(team.TeamID, in.ActorUserID, "invite_link_accept", "invite_link", link.LinkID, map[string]any{"membershipId": membershipID}, now)
	if _, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
		return nil, err
	}
	return &store.AcceptInviteLinkTxResult{
		Outcome: store.TeamsTxChanged,
		Context: memContext(*memCloneTeam(team), *memCloneMem(m.teamMemberships[membershipID]), false),
	}, nil
}

func (m *MemoryStore) UpdateSharingTx(ctx context.Context, in store.UpdateSharingTxInput) (*domain.TeamSharingState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[in.TeamID]
	mem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	if team == nil || team.Status != domain.TeamStatusActive || mem == nil {
		return nil, memErrTeamNotFound()
	}
	if mem.SharingVersion != in.ExpectedVersion {
		return nil, memErrVersionConflict()
	}
	now := in.Now.UTC()
	changed, err := m.applySharing(mem.MembershipID, in.Sharing, now)
	if err != nil {
		return nil, err
	}
	if changed {
		mem.SharingVersion++
		m.bumpAuth(in.TeamID)
		_ = m.insertAudit(in.TeamID, in.ActorUserID, "sharing_change", "membership", mem.MembershipID, map[string]any{"sharing": in.Sharing}, now)
	}
	flags, effective := m.sharingFlags(mem.MembershipID)
	return &domain.TeamSharingState{MembershipID: mem.MembershipID, SharingVersion: mem.SharingVersion, Sharing: flags, EffectiveFrom: effective}, nil
}

func (m *MemoryStore) ChangeMemberRoleTx(ctx context.Context, in store.ChangeMemberRoleTxInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return memErrTeamNotFound()
	}
	if memPublicRole(team, m.currentMembershipOf(in.TeamID, in.ActorUserID)) != domain.TeamRoleOwner {
		return memErrPermissionDenied()
	}
	if team.AuthRevision != in.ExpectedAuthRevision {
		return memErrVersionConflict()
	}
	target := m.teamMemberships[in.MembershipID]
	if target == nil || target.TeamID != in.TeamID || target.EndedAt != nil || target.UserID == team.OwnerUserID {
		if target != nil && target.UserID == team.OwnerUserID {
			return memErrPermissionDenied()
		}
		return memErrResourceNotFound()
	}
	if in.NewRole != domain.TeamBaseRoleAdmin && in.NewRole != domain.TeamBaseRoleMember {
		return memErrInvalid("role must be admin or member")
	}
	if target.BaseRole == in.NewRole {
		return nil
	}
	from := target.BaseRole
	target.BaseRole = in.NewRole
	if in.NewRole == domain.TeamBaseRoleMember {
		m.revokeLinksFromUser(in.TeamID, target.UserID, in.Now.UTC())
		m.revokeInvitesFromUser(in.TeamID, target.UserID, in.Now.UTC())
	}
	m.bumpAuth(in.TeamID)
	return m.insertAudit(in.TeamID, in.ActorUserID, "role_change", "membership", in.MembershipID, map[string]any{"from": string(from), "to": string(in.NewRole)}, in.Now.UTC())
}

func (m *MemoryStore) RemoveMemberTx(ctx context.Context, in store.RemoveMemberTxInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return err
	} else if rec != nil {
		return nil
	}
	team := m.teams[in.TeamID]
	target := m.teamMemberships[in.MembershipID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return memErrTeamNotFound()
	}
	if team.AuthRevision != in.ExpectedAuthRevision {
		return memErrVersionConflict()
	}
	if target == nil || target.TeamID != in.TeamID || target.EndedAt != nil {
		return memErrResourceNotFound()
	}
	if target.UserID == team.OwnerUserID || target.UserID == in.ActorUserID {
		return memErrPermissionDenied()
	}
	actorMem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	switch memPublicRole(team, actorMem) {
	case domain.TeamRoleOwner:
	case domain.TeamRoleAdmin:
		if target.BaseRole != domain.TeamBaseRoleMember {
			return memErrPermissionDenied()
		}
	default:
		if actorMem == nil {
			return memErrTeamNotFound()
		}
		return memErrPermissionDenied()
	}
	m.closeMembership(target, domain.TeamEndReasonRemoved, now)
	m.bumpAuth(in.TeamID)
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "remove", "membership", in.MembershipID, map[string]any{"userId": target.UserID}, now)
	_, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "membership", in.MembershipID, now)
	return err
}

func (m *MemoryStore) LeaveTeamTx(ctx context.Context, in store.LeaveTeamTxInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return err
	} else if rec != nil {
		return nil
	}
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return memErrTeamNotFound()
	}
	if team.AuthRevision != in.ExpectedAuthRevision {
		return memErrVersionConflict()
	}
	if team.OwnerUserID == in.ActorUserID {
		return memErrPermissionDenied()
	}
	cur := m.userCurrentTeams[in.ActorUserID]
	if cur == nil || cur.TeamID != in.TeamID {
		return memErrTeamNotFound()
	}
	mem := m.teamMemberships[cur.MembershipID]
	m.closeMembership(mem, domain.TeamEndReasonLeft, now)
	m.bumpAuth(in.TeamID)
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "leave", "membership", mem.MembershipID, map[string]any{"result": "left"}, now)
	_, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "team", in.TeamID, now)
	return err
}

func (m *MemoryStore) TransferOwnershipTx(ctx context.Context, in store.TransferOwnershipTxInput) (*domain.TeamContext, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		return m.replayTeamContext(in.ActorUserID, in.TeamID)
	}
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrTeamNotFound()
	}
	if team.OwnerUserID != in.ActorUserID {
		return nil, memErrPermissionDenied()
	}
	if team.AuthRevision != in.ExpectedAuthRevision {
		return nil, memErrVersionConflict()
	}
	if team.Name != in.ConfirmTeamName {
		return nil, memErrInvalid("team name confirmation does not match")
	}
	target := m.teamMemberships[in.TargetMembershipID]
	if target == nil || target.TeamID != in.TeamID || target.EndedAt != nil || target.UserID == in.ActorUserID {
		return nil, memErrResourceNotFound()
	}
	if _, err := m.requireActiveOnboarded(target.UserID); err != nil {
		return nil, err
	}
	cur := m.userCurrentTeams[target.UserID]
	if cur == nil || cur.MembershipID != target.MembershipID {
		return nil, memErrResourceNotFound()
	}
	actorMem := m.currentMembershipOf(in.TeamID, in.ActorUserID)
	team.OwnerUserID = target.UserID
	if actorMem != nil {
		actorMem.BaseRole = domain.TeamBaseRoleAdmin
	}
	m.bumpAuth(in.TeamID)
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "transfer", "membership", target.MembershipID, map[string]any{"toUserId": target.UserID}, now)
	if _, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "team", in.TeamID, now); err != nil {
		return nil, err
	}
	return memContext(*memCloneTeam(team), *memCloneMem(actorMem), false), nil
}

func (m *MemoryStore) DissolveTeamTx(ctx context.Context, in store.DissolveTeamTxInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return err
	} else if rec != nil {
		return nil
	}
	team := m.teams[in.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return memErrTeamNotFound()
	}
	if team.OwnerUserID != in.ActorUserID {
		return memErrPermissionDenied()
	}
	if team.AuthRevision != in.ExpectedAuthRevision {
		return memErrVersionConflict()
	}
	if team.Name != in.ConfirmTeamName {
		return memErrInvalid("team name confirmation does not match")
	}
	var mems []*domain.TeamMembership
	for _, mem := range m.teamMemberships {
		if mem.TeamID == in.TeamID && mem.EndedAt == nil {
			mems = append(mems, mem)
		}
	}
	for _, mem := range mems {
		m.closeMembership(mem, domain.TeamEndReasonDissolved, now)
	}
	for _, inv := range m.teamInvitations {
		if inv.TeamID == in.TeamID && inv.Status == domain.InvitationPending {
			inv.Status = domain.InvitationRevoked
			inv.ActiveRecipientHash = nil
			inv.Version++
		}
	}
	for _, link := range m.teamInviteLinks {
		if link.TeamID == in.TeamID && link.Status == domain.InviteLinkActive {
			rev := now
			link.Status = domain.InviteLinkRevoked
			link.RevokedAt = &rev
			link.Version++
			link.TokenCiphertext = nil
		}
	}
	for _, job := range m.teamExports {
		if job.TeamID == in.TeamID && (job.Status == domain.TeamExportQueued || job.Status == domain.TeamExportRunning || job.Status == domain.TeamExportCompleted) {
			job.Status = domain.TeamExportRevoked
		}
	}
	for _, snap := range m.teamSnapshots {
		if snap.TeamID == in.TeamID && (snap.Status == domain.SnapshotQueued || snap.Status == domain.SnapshotBuilding || snap.Status == domain.SnapshotReady) {
			snap.Status = domain.SnapshotObsolete
			snap.ActiveRequestKey = nil
		}
	}
	dissolved := now
	team.Status = domain.TeamStatusDissolved
	team.DissolvedAt = &dissolved
	team.AuthRevision++
	_ = m.insertAudit(in.TeamID, in.ActorUserID, "dissolve", "team", in.TeamID, map[string]any{"result": "dissolved"}, now)
	_, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "team", in.TeamID, now)
	return err
}

func (m *MemoryStore) QueueTeamExportTx(ctx context.Context, in store.QueueTeamExportTxInput) (*domain.TeamExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := in.Now.UTC()
	if rec, err := m.checkReceipt(in.ActorUserID, in.Idempotency, now); err != nil {
		return nil, err
	} else if rec != nil {
		job := m.teamExports[rec.ResultID]
		if job == nil {
			return nil, memErrCommandUnavailable()
		}
		return memCloneExport(job), nil
	}
	team := m.teams[in.Job.TeamID]
	mem := m.currentMembershipOf(in.Job.TeamID, in.ActorUserID)
	if team == nil || team.Status != domain.TeamStatusActive || !memIsManager(team, mem) {
		if mem == nil {
			return nil, memErrTeamNotFound()
		}
		return nil, memErrPermissionDenied()
	}
	job := in.Job
	if job.ExportID == "" {
		id, err := memNewID(domain.ExportIDPrefix)
		if err != nil {
			return nil, err
		}
		job.ExportID = id
	}
	job.RequesterUserID = in.ActorUserID
	job.RequesterMembershipID = mem.MembershipID
	job.AuthRevision = team.AuthRevision
	job.Status = domain.TeamExportQueued
	job.CreatedAt = now
	if job.ExpiresAt.IsZero() {
		job.ExpiresAt = now.Add(30 * 24 * time.Hour)
	}
	if job.FilterJSON == "" {
		job.FilterJSON = "{}"
	}
	copied := job
	m.teamExports[job.ExportID] = &copied
	_ = m.insertAudit(job.TeamID, in.ActorUserID, "export_create", "export", job.ExportID, map[string]any{"kind": string(job.Kind)}, now)
	if _, err := m.insertReceipt(in.ActorUserID, in.Idempotency, "export", job.ExportID, now); err != nil {
		return nil, err
	}
	return memCloneExport(&copied), nil
}

func (m *MemoryStore) CreateTeamAvatarUploadIntent(ctx context.Context, obj domain.TeamUploadObject) (*domain.TeamUploadObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj.Status == "" {
		obj.Status = "pending"
	}
	copied := obj
	m.teamUploadObjects[obj.ObjectID] = &copied
	return memCloneObject(&copied), nil
}

func (m *MemoryStore) CompleteTeamAvatarUpload(ctx context.Context, teamID, objectID, actorUserID string, meta store.AvatarReadyMeta, expectedProfileVersion uint64, now time.Time) (*domain.Team, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[teamID]
	obj := m.teamUploadObjects[objectID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, memErrTeamNotFound()
	}
	if team.ProfileVersion != expectedProfileVersion {
		return nil, memErrVersionConflict()
	}
	if !memIsManager(team, m.currentMembershipOf(teamID, actorUserID)) {
		return nil, memErrPermissionDenied()
	}
	if obj == nil || obj.TeamID != teamID || obj.UploaderID != actorUserID {
		return nil, memErrResourceNotFound()
	}
	obj.Status = "ready"
	obj.ByteSize = meta.ByteSize
	obj.SHA256 = meta.ContentSha256
	obj.ImageWidth = meta.ImageWidth
	obj.ImageHeight = meta.ImageHeight
	obj.ContentType = meta.ContentType
	id := objectID
	team.AvatarObjectID = &id
	team.ProfileVersion++
	_ = m.insertAudit(teamID, actorUserID, "avatar_change", "object", objectID, map[string]any{"result": "completed"}, now.UTC())
	return memCloneTeam(team), nil
}

func (m *MemoryStore) ClearTeamAvatar(ctx context.Context, teamID, actorUserID string, expectedProfileVersion uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[teamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return memErrTeamNotFound()
	}
	if team.ProfileVersion != expectedProfileVersion {
		return memErrVersionConflict()
	}
	if !memIsManager(team, m.currentMembershipOf(teamID, actorUserID)) {
		return memErrPermissionDenied()
	}
	team.AvatarObjectID = nil
	team.ProfileVersion++
	return m.insertAudit(teamID, actorUserID, "avatar_change", "team", teamID, map[string]any{"result": "cleared"}, now.UTC())
}

func memAnalysisKey(teamID string, from, to time.Time, authRevision uint64, ruleVersion, timezone string) string {
	return teamID + "|" + domain.FormatTeamCalendarDate(from, timezone) + "|" + domain.FormatTeamCalendarDate(to, timezone) + "|" +
		strconv.FormatUint(authRevision, 10) + "|" + ruleVersion
}

func (m *MemoryStore) GetOrQueueAnalysis(ctx context.Context, teamID string, from, toExclusive time.Time, authRevision uint64, ruleVersion string, now time.Time) (*domain.TeamAnalysisSnapshot, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	team := m.teams[teamID]
	if team == nil || team.Status != domain.TeamStatusActive {
		return nil, false, memErrTeamNotFound()
	}
	tNow := now.UTC()
	var best *domain.TeamAnalysisSnapshot
	for _, snap := range m.teamSnapshots {
		if snap.TeamID == teamID && snap.AuthRevision == authRevision && snap.RuleVersion == ruleVersion &&
			snap.Status == domain.SnapshotReady &&
			domain.FormatTeamCalendarDate(snap.FromDate, team.TimezoneName) == domain.FormatTeamCalendarDate(from, team.TimezoneName) &&
			domain.FormatTeamCalendarDate(snap.ToDateExclusive, team.TimezoneName) == domain.FormatTeamCalendarDate(toExclusive, team.TimezoneName) {
			if best == nil || snap.AsOf.After(best.AsOf) {
				best = snap
			}
		}
	}
	if best != nil && tNow.Sub(best.AsOf) <= 30*time.Second {
		return memCloneSnap(best), false, nil
	}
	key := memAnalysisKey(teamID, from, toExclusive, authRevision, ruleVersion, team.TimezoneName)
	for _, snap := range m.teamSnapshots {
		if snap.ActiveRequestKey != nil && *snap.ActiveRequestKey == key {
			queued := snap.Status == domain.SnapshotQueued || snap.Status == domain.SnapshotBuilding
			return memCloneSnap(snap), queued, nil
		}
	}
	id, err := memNewID(domain.SnapshotIDPrefix)
	if err != nil {
		return nil, false, err
	}
	var source uint64
	if rev := m.teamRevisions[teamID]; rev != nil {
		source = rev.SourceRevision
	}
	active := key
	snap := &domain.TeamAnalysisSnapshot{
		SnapshotID:       id,
		TeamID:           teamID,
		FromDate:         from,
		ToDateExclusive:  toExclusive,
		AuthRevision:     authRevision,
		SourceRevision:   source,
		RuleVersion:      ruleVersion,
		Status:           domain.SnapshotQueued,
		ActiveRequestKey: &active,
		AsOf:             tNow,
		NextAttemptAt:    tNow,
		ExpiresAt:        tNow.Add(30 * time.Minute),
	}
	m.teamSnapshots[id] = snap
	return memCloneSnap(snap), true, nil
}

func (m *MemoryStore) GetReadySnapshot(ctx context.Context, teamID, snapshotID string) (*domain.TeamAnalysisSnapshot, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap := m.teamSnapshots[snapshotID]
	if snap == nil || snap.TeamID != teamID || snap.Status != domain.SnapshotReady {
		return nil, memErrResourceNotFound()
	}
	return memCloneSnap(snap), nil
}

func (m *MemoryStore) ListAnalysisRows(ctx context.Context, snapshotID string, generation uint64) ([]domain.TeamAnalysisRow, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []domain.TeamAnalysisRow
	for _, row := range m.teamAnalysisRows[snapshotID] {
		if row.BuildGeneration == generation {
			out = append(out, row)
		}
	}
	if out == nil {
		out = []domain.TeamAnalysisRow{}
	}
	return out, nil
}

func (m *MemoryStore) ClaimAnalysis(ctx context.Context, workerID string, lease time.Duration, now time.Time) (*domain.TeamAnalysisSnapshot, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tNow := now.UTC()
	var chosen *domain.TeamAnalysisSnapshot
	for _, snap := range m.teamSnapshots {
		if snap.Status != domain.SnapshotQueued && snap.Status != domain.SnapshotBuilding {
			continue
		}
		if snap.NextAttemptAt.After(tNow) {
			continue
		}
		if snap.LeaseExpiresAt != nil && snap.LeaseExpiresAt.After(tNow) {
			continue
		}
		if chosen == nil || snap.NextAttemptAt.Before(chosen.NextAttemptAt) {
			chosen = snap
		}
	}
	if chosen == nil {
		return nil, nil
	}
	token := workerID
	if token == "" {
		id, err := memNewID("twk_")
		if err != nil {
			return nil, err
		}
		token = id
	}
	chosen.Status = domain.SnapshotBuilding
	chosen.LeaseToken = &token
	chosen.LeaseGeneration++
	exp := tNow.Add(lease)
	chosen.LeaseExpiresAt = &exp
	chosen.AttemptCount++
	chosen.NextAttemptAt = exp
	return memCloneSnap(chosen), nil
}

func (m *MemoryStore) PublishAnalysis(ctx context.Context, snapshotID, leaseToken string, leaseGeneration, capturedAuth, capturedSource, publishedGeneration uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := m.teamSnapshots[snapshotID]
	if snap == nil {
		return memErrResourceNotFound()
	}
	team := m.teams[snap.TeamID]
	if team == nil || team.Status != domain.TeamStatusActive || team.AuthRevision != capturedAuth {
		snap.Status = domain.SnapshotObsolete
		snap.ActiveRequestKey = nil
		return memErrVersionConflict()
	}
	for _, b := range m.teamBarriers {
		if b.TeamID == snap.TeamID && b.ReleasedAt == nil {
			return memTeamErr(503, "TEAM_TEMPORARILY_UNAVAILABLE", "teams.unavailable", "deletion barrier open", domain.ErrInternal)
		}
	}
	if snap.LeaseToken == nil || *snap.LeaseToken != leaseToken || snap.LeaseGeneration != leaseGeneration {
		return memErrVersionConflict()
	}
	if snap.LeaseExpiresAt != nil && !snap.LeaseExpiresAt.After(now.UTC()) {
		return memErrVersionConflict()
	}
	snap.Status = domain.SnapshotReady
	snap.ActiveRequestKey = nil
	snap.SourceRevision = capturedSource
	snap.PublishedGeneration = publishedGeneration
	snap.AsOf = now.UTC()
	return nil
}

func (m *MemoryStore) MarkAnalysisFailed(ctx context.Context, snapshotID, leaseToken string, leaseGeneration uint64, errorCode string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := m.teamSnapshots[snapshotID]
	if snap == nil || snap.LeaseToken == nil || *snap.LeaseToken != leaseToken || snap.LeaseGeneration != leaseGeneration {
		return memErrResourceNotFound()
	}
	snap.Status = domain.SnapshotFailed
	snap.ActiveRequestKey = nil
	snap.ErrorCode = &errorCode
	return nil
}

func (m *MemoryStore) BumpSourceRevision(ctx context.Context, teamID string, now time.Time) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.teams[teamID] == nil {
		return 0, memErrTeamNotFound()
	}
	rev := m.teamRevisions[teamID]
	if rev == nil {
		rev = &domain.TeamSourceRevision{TeamID: teamID}
		m.teamRevisions[teamID] = rev
	}
	rev.SourceRevision++
	rev.ChangedAt = now.UTC()
	return rev.SourceRevision, nil
}

func (m *MemoryStore) RegisterDeletionBarrier(ctx context.Context, deletionRequestID, teamID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.teams[teamID] == nil {
		return memErrTeamNotFound()
	}
	key := memBarrierKey(deletionRequestID, teamID)
	if existing := m.teamBarriers[key]; existing == nil || existing.ReleasedAt != nil {
		m.teamBarriers[key] = &domain.TeamDeletionBarrier{
			DeletionRequestID: deletionRequestID,
			TeamID:            teamID,
			BlockedAt:         now.UTC(),
		}
	}
	m.bumpAuth(teamID)
	return nil
}

func (m *MemoryStore) ReleaseDeletionBarrier(ctx context.Context, deletionRequestID, teamID string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.teams[teamID] == nil {
		return memErrTeamNotFound()
	}
	key := memBarrierKey(deletionRequestID, teamID)
	if b := m.teamBarriers[key]; b != nil && b.ReleasedAt == nil {
		t := now.UTC()
		b.ReleasedAt = &t
	}
	m.bumpAuth(teamID)
	return nil
}

func (m *MemoryStore) ClaimTeamExport(ctx context.Context, workerID string, lease time.Duration, now time.Time) (*domain.TeamExportJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	tNow := now.UTC()
	var chosen *domain.TeamExportJob
	for _, job := range m.teamExports {
		if job.Status != domain.TeamExportQueued && job.Status != domain.TeamExportRunning {
			continue
		}
		if job.NextAttemptAt.After(tNow) {
			continue
		}
		if job.LeaseExpiresAt != nil && job.LeaseExpiresAt.After(tNow) {
			continue
		}
		if chosen == nil || job.NextAttemptAt.Before(chosen.NextAttemptAt) {
			chosen = job
		}
	}
	if chosen == nil {
		return nil, nil
	}
	token := workerID
	if token == "" {
		id, err := memNewID("twk_")
		if err != nil {
			return nil, err
		}
		token = id
	}
	chosen.Status = domain.TeamExportRunning
	chosen.LeaseToken = &token
	chosen.LeaseGeneration++
	exp := tNow.Add(lease)
	chosen.LeaseExpiresAt = &exp
	chosen.AttemptCount++
	chosen.NextAttemptAt = exp
	return memCloneExport(chosen), nil
}

func (m *MemoryStore) CompleteTeamExport(ctx context.Context, exportID, leaseToken string, leaseGeneration uint64, objectKey string, sha256sum [32]byte, size uint64, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.teamExports[exportID]
	if job == nil || job.LeaseToken == nil || *job.LeaseToken != leaseToken || job.LeaseGeneration != leaseGeneration || job.Status != domain.TeamExportRunning {
		return memErrResourceNotFound()
	}
	job.Status = domain.TeamExportCompleted
	job.ObjectKey = &objectKey
	sum := sha256sum
	job.FileSHA256 = &sum
	job.FileSize = &size
	job.LeaseToken = nil
	job.LeaseExpiresAt = nil
	return nil
}

func (m *MemoryStore) FailTeamExport(ctx context.Context, exportID, leaseToken string, leaseGeneration uint64, errorCode string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	job := m.teamExports[exportID]
	if job == nil || job.LeaseToken == nil || *job.LeaseToken != leaseToken || job.LeaseGeneration != leaseGeneration {
		return memErrResourceNotFound()
	}
	job.Status = domain.TeamExportFailed
	job.ErrorCode = &errorCode
	return nil
}

var _ store.TeamsStore = (*MemoryStore)(nil)
