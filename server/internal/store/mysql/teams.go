package mysql

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	mysqlerr "github.com/go-sql-driver/mysql"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

type teamsStore struct {
	db *sql.DB
}

var _ store.TeamsStore = (*teamsStore)(nil)

type lockedUser struct {
	UserID                string
	AccountStatus         domain.AccountStatus
	OnboardingCompletedAt sql.NullTime
	EmailVerifiedAt       sql.NullTime
	EmailLookupHash       []byte
	DisplayName           string
}

type occupyMembershipInput struct {
	MembershipID string
	TeamID       string
	UserID       string
	BaseRole     domain.TeamBaseRole
	Sharing      domain.SharingFlags
	JoinedAt     time.Time
}

const (
	receiptTTL        = 7 * 24 * time.Hour
	defaultListLimit  = 20
	maxListLimit      = 100
	maxTeamsTxAttempt = 4
)

func teamErr(status int, code, key, msg string, underlying error) error {
	return domain.NewAppError(status, code, key, msg, nil, underlying)
}

func errTeamNotFound() error {
	return teamErr(404, "TEAM_NOT_FOUND", "teams.notFound", "team not found", domain.ErrNotFound)
}
func errInvitationNotFound() error {
	return teamErr(404, "TEAM_INVITATION_NOT_FOUND", "teams.invitationNotFound", "invitation not found", domain.ErrNotFound)
}
func errInviteLinkNotFound() error {
	return teamErr(404, "TEAM_INVITE_LINK_NOT_FOUND", "teams.inviteLinkNotFound", "invite link not found", domain.ErrNotFound)
}
func errResourceNotFound() error {
	return teamErr(404, "RESOURCE_NOT_FOUND", "teams.resourceNotFound", "resource not found", domain.ErrNotFound)
}
func errMembershipExists() error {
	return teamErr(409, "TEAM_MEMBERSHIP_EXISTS", "teams.membershipExists", "user already belongs to a team", domain.ErrConflict)
}
func errIdempotencyReused() error {
	return teamErr(409, "IDEMPOTENCY_KEY_REUSED", "teams.idempotencyKeyReused", "idempotency key reused with a different request", domain.ErrIdempotencyReused)
}
func errVersionConflict() error {
	return teamErr(409, "TEAM_VERSION_CONFLICT", "teams.versionConflict", "team version conflict", domain.ErrConflict)
}
func errInvitationPending() error {
	return teamErr(409, "TEAM_INVITATION_PENDING", "teams.invitationPending", "a pending invitation already exists", domain.ErrConflict)
}
func errPermissionDenied() error {
	return teamErr(403, "TEAM_PERMISSION_DENIED", "teams.permissionDenied", "permission denied", domain.ErrForbidden)
}
func errAccountActionNotAllowed() error {
	return teamErr(403, "ACCOUNT_ACTION_NOT_ALLOWED", "account.actionNotAllowed", "account cannot perform this action", domain.ErrForbidden)
}
func errReinvitationRequired() error {
	return teamErr(403, "TEAM_REINVITATION_REQUIRED", "teams.reinvitationRequired", "removed members must be reinvited by email", domain.ErrForbidden)
}
func errInvitationExpired() error {
	return teamErr(410, "TEAM_INVITATION_EXPIRED", "teams.invitationExpired", "invitation expired", domain.ErrNotFound)
}
func errInvitationRevoked() error {
	return teamErr(410, "TEAM_INVITATION_REVOKED", "teams.invitationRevoked", "invitation revoked", domain.ErrNotFound)
}
func errInviteLinkExpired() error {
	return teamErr(410, "TEAM_INVITE_LINK_EXPIRED", "teams.inviteLinkExpired", "invite link expired", domain.ErrNotFound)
}
func errInviteLinkRevoked() error {
	return teamErr(410, "TEAM_INVITE_LINK_REVOKED", "teams.inviteLinkRevoked", "invite link revoked", domain.ErrNotFound)
}
func errInviteLinkExhausted() error {
	return teamErr(410, "TEAM_INVITE_LINK_EXHAUSTED", "teams.inviteLinkExhausted", "invite link exhausted", domain.ErrNotFound)
}
func errInviteLinkAlreadyUsed() error {
	return teamErr(410, "TEAM_INVITE_LINK_ALREADY_USED", "teams.inviteLinkAlreadyUsed", "invite link already used", domain.ErrNotFound)
}
func errCommandResultUnavailable() error {
	return teamErr(410, "COMMAND_RESULT_UNAVAILABLE", "teams.commandResultUnavailable", "command result is no longer available", domain.ErrNotFound)
}
func errInvalidArgument(msg string) error {
	return teamErr(400, "API_INVALID_ARGUMENT", "api.invalidArgument", msg, domain.ErrInvalidArgument)
}
func errTeamUnavailable(err error) error {
	return teamErr(503, "TEAM_TEMPORARILY_UNAVAILABLE", "teams.unavailable", "team transaction could not complete", err)
}

func isRetryableTeamsTx(err error) bool {
	var me *mysqlerr.MySQLError
	if errors.As(err, &me) {
		return me.Number == 1213 || me.Number == 1205
	}
	return false
}

func (s *teamsStore) doTx(ctx context.Context, fn func(*sql.Tx) error) error {
	var last error
	for attempt := 0; attempt < maxTeamsTxAttempt; attempt++ {
		tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
		if err != nil {
			return fmt.Errorf("begin teams tx: %w", err)
		}
		if err := fn(tx); err != nil {
			_ = tx.Rollback()
			last = err
			if isRetryableTeamsTx(err) {
				continue
			}
			return err
		}
		if err := tx.Commit(); err != nil {
			last = err
			if isRetryableTeamsTx(err) {
				continue
			}
			return fmt.Errorf("commit teams tx: %w", err)
		}
		return nil
	}
	return errTeamUnavailable(last)
}

func sortUniqueIDs(ids ...string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func hashesEqual(a, b [32]byte) bool {
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1
}

func bytesHashEqual(raw []byte, want [32]byte) bool {
	if len(raw) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(raw, want[:]) == 1
}

func clampLimit(limit int) int {
	if limit <= 0 {
		return defaultListLimit
	}
	if limit > maxListLimit {
		return maxListLimit
	}
	return limit
}

func likeContains(q string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + replacer.Replace(q) + "%"
}

func encodeCursor(parts ...string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(parts, "\x1f")))
}

func decodeCursor(cursor string) []string {
	if strings.TrimSpace(cursor) == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil
	}
	return strings.Split(string(raw), "\x1f")
}

func safeJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func newTeamID(prefix string) (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate team id: %w", err)
	}
	id := prefix + token
	if len(id) != 30 {
		return "", fmt.Errorf("team id length %d != 30", len(id))
	}
	return id, nil
}

func (u lockedUser) requireActiveOnboarded() error {
	if u.AccountStatus != domain.AccountStatusActive {
		return errAccountActionNotAllowed()
	}
	if !u.OnboardingCompletedAt.Valid {
		return errAccountActionNotAllowed()
	}
	return nil
}

func (u lockedUser) requireEmailVerified() error {
	if err := u.requireActiveOnboarded(); err != nil {
		return err
	}
	if !u.EmailVerifiedAt.Valid {
		return errAccountActionNotAllowed()
	}
	return nil
}

func teamContextOf(team domain.Team, mem domain.TeamMembership, already bool) *domain.TeamContext {
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

func publicRole(team domain.Team, mem *domain.TeamMembership) domain.TeamPublicRole {
	if mem == nil || mem.EndedAt != nil {
		return ""
	}
	return team.PublicRoleFor(mem.UserID, mem.BaseRole)
}

func isManager(team domain.Team, mem *domain.TeamMembership) bool {
	role := publicRole(team, mem)
	return role == domain.TeamRoleOwner || role == domain.TeamRoleAdmin
}

func canGrantRole(team domain.Team, inviter *domain.TeamMembership, role domain.TeamBaseRole) bool {
	switch publicRole(team, inviter) {
	case domain.TeamRoleOwner:
		return role == domain.TeamBaseRoleAdmin || role == domain.TeamBaseRoleMember
	case domain.TeamRoleAdmin:
		return role == domain.TeamBaseRoleMember
	default:
		return false
	}
}

func sharingDimensions() []domain.SharingDimension {
	return []domain.SharingDimension{
		domain.SharingBase,
		domain.SharingNamed,
		domain.SharingClassification,
		domain.SharingCost,
	}
}

func (s *teamsStore) lockUser(ctx context.Context, tx *sql.Tx, userID string) (lockedUser, error) {
	var u lockedUser
	err := tx.QueryRowContext(ctx, `
		SELECT user_id, account_status, onboarding_completed_at, email_verified_at, email_lookup_hash, display_name
		FROM users
		WHERE user_id = ?
		FOR UPDATE`, userID).Scan(
		&u.UserID, &u.AccountStatus, &u.OnboardingCompletedAt, &u.EmailVerifiedAt, &u.EmailLookupHash, &u.DisplayName,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return u, errAccountActionNotAllowed()
	}
	if err != nil {
		return u, fmt.Errorf("lock user %s: %w", userID, err)
	}
	return u, nil
}

func (s *teamsStore) lockUsers(ctx context.Context, tx *sql.Tx, ids ...string) (map[string]lockedUser, error) {
	out := make(map[string]lockedUser, len(ids))
	for _, id := range sortUniqueIDs(ids...) {
		u, err := s.lockUser(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		out[id] = u
	}
	return out, nil
}

func (s *teamsStore) lockTeam(ctx context.Context, tx *sql.Tx, teamID string) (*domain.Team, error) {
	team, err := scanTeam(tx.QueryRowContext(ctx, teamSelectSQL+` WHERE team_id = ? FOR UPDATE`, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errTeamNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("lock team: %w", err)
	}
	return team, nil
}

func (s *teamsStore) lockCurrent(ctx context.Context, tx *sql.Tx, userID string) (*domain.UserCurrentTeam, error) {
	var cur domain.UserCurrentTeam
	err := tx.QueryRowContext(ctx, `
		SELECT user_id, team_id, membership_id, joined_at
		FROM user_current_teams
		WHERE user_id = ?
		FOR UPDATE`, userID).Scan(&cur.UserID, &cur.TeamID, &cur.MembershipID, &cur.JoinedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock current team: %w", err)
	}
	return &cur, nil
}

func (s *teamsStore) lockMembership(ctx context.Context, tx *sql.Tx, membershipID string) (*domain.TeamMembership, error) {
	mem, err := scanMembership(tx.QueryRowContext(ctx, membershipSelectSQL+` WHERE membership_id = ? FOR UPDATE`, membershipID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errResourceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("lock membership: %w", err)
	}
	return mem, nil
}

func (s *teamsStore) lockLatestHistory(ctx context.Context, tx *sql.Tx, teamID, userID string) (*domain.TeamMembership, error) {
	mem, err := scanMembership(tx.QueryRowContext(ctx, membershipSelectSQL+`
		WHERE team_id = ? AND user_id = ?
		ORDER BY joined_at DESC, membership_id DESC
		LIMIT 1
		FOR UPDATE`, teamID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock latest membership: %w", err)
	}
	return mem, nil
}

func (s *teamsStore) lockInvitation(ctx context.Context, tx *sql.Tx, invitationID string) (*domain.TeamInvitation, error) {
	inv, err := scanInvitation(tx.QueryRowContext(ctx, invitationSelectSQL+` WHERE invitation_id = ? FOR UPDATE`, invitationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInvitationNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("lock invitation: %w", err)
	}
	return inv, nil
}

func (s *teamsStore) lockInviteLink(ctx context.Context, tx *sql.Tx, linkID string) (*domain.TeamInviteLink, error) {
	link, err := scanInviteLink(tx.QueryRowContext(ctx, inviteLinkSelectSQL+` WHERE link_id = ? FOR UPDATE`, linkID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteLinkNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("lock invite link: %w", err)
	}
	return link, nil
}

func (s *teamsStore) txNow(ctx context.Context, tx *sql.Tx, fallback time.Time) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT UTC_TIMESTAMP(3)`).Scan(&now); err != nil {
		if !fallback.IsZero() {
			return fallback.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("read database time: %w", err)
	}
	return now.UTC(), nil
}

func (s *teamsStore) loadReceipt(ctx context.Context, tx *sql.Tx, idem store.TeamsIdempotency, actorUserID string) (*domain.TeamCommandReceipt, error) {
	var rec domain.TeamCommandReceipt
	var keyHash, reqHash []byte
	err := tx.QueryRowContext(ctx, `
		SELECT actor_user_id, operation_scope, idempotency_key_hash, request_hash, result_type, result_id, created_at, expires_at
		FROM team_command_receipts
		WHERE actor_user_id = ? AND operation_scope = ? AND idempotency_key_hash = ?
		FOR UPDATE`, actorUserID, idem.Scope, idem.KeyHash[:]).Scan(
		&rec.ActorUserID, &rec.OperationScope, &keyHash, &reqHash, &rec.ResultType, &rec.ResultID, &rec.CreatedAt, &rec.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lock command receipt: %w", err)
	}
	copy(rec.IdempotencyKeyHash[:], keyHash)
	copy(rec.RequestHash[:], reqHash)
	return &rec, nil
}

func (s *teamsStore) checkReceipt(ctx context.Context, tx *sql.Tx, idem store.TeamsIdempotency, actorUserID string, now time.Time) (*domain.TeamCommandReceipt, error) {
	rec, err := s.loadReceipt(ctx, tx, idem, actorUserID)
	if err != nil || rec == nil {
		return rec, err
	}
	if now.After(rec.ExpiresAt) {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM team_command_receipts
			WHERE actor_user_id = ? AND operation_scope = ? AND idempotency_key_hash = ?`,
			actorUserID, idem.Scope, idem.KeyHash[:]); err != nil {
			return nil, fmt.Errorf("expire command receipt: %w", err)
		}
		return nil, nil
	}
	if !hashesEqual(rec.RequestHash, idem.RequestHash) {
		return nil, errIdempotencyReused()
	}
	return rec, nil
}

func (s *teamsStore) insertReceipt(ctx context.Context, tx *sql.Tx, actorUserID string, idem store.TeamsIdempotency, resultType, resultID string, now time.Time) (*domain.TeamCommandReceipt, error) {
	rec := domain.TeamCommandReceipt{
		ActorUserID:        actorUserID,
		OperationScope:     idem.Scope,
		IdempotencyKeyHash: idem.KeyHash,
		RequestHash:        idem.RequestHash,
		ResultType:         resultType,
		ResultID:           resultID,
		CreatedAt:          now,
		ExpiresAt:          now.Add(receiptTTL),
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_command_receipts (
			actor_user_id, operation_scope, idempotency_key_hash, request_hash, result_type, result_id, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ActorUserID, rec.OperationScope, rec.IdempotencyKeyHash[:], rec.RequestHash[:],
		rec.ResultType, rec.ResultID, rec.CreatedAt, rec.ExpiresAt,
	); err != nil {
		if isDuplicateKey(err) {
			return nil, errIdempotencyReused()
		}
		return nil, fmt.Errorf("insert command receipt: %w", err)
	}
	return &rec, nil
}

func (s *teamsStore) insertAudit(ctx context.Context, tx *sql.Tx, teamID, actorUserID, action, targetType, targetID string, details any, now time.Time) error {
	auditID, err := newTeamID(domain.AuditIDPrefix)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_audit_events (
			audit_id, team_id, actor_user_id, action, target_type, target_id, safe_details_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, CAST(? AS JSON), ?)`,
		auditID, teamID, actorUserID, action, targetType, targetID, safeJSON(details), now,
	); err != nil {
		return fmt.Errorf("insert team audit: %w", err)
	}
	return nil
}

func (s *teamsStore) bumpAuth(ctx context.Context, tx *sql.Tx, teamID string) (uint64, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE teams SET auth_revision = auth_revision + 1 WHERE team_id = ?`, teamID); err != nil {
		return 0, fmt.Errorf("bump auth revision: %w", err)
	}
	var rev uint64
	if err := tx.QueryRowContext(ctx, `SELECT auth_revision FROM teams WHERE team_id = ?`, teamID).Scan(&rev); err != nil {
		return 0, fmt.Errorf("read auth revision: %w", err)
	}
	return rev, nil
}

func (s *teamsStore) occupyMembership(ctx context.Context, tx *sql.Tx, in occupyMembershipInput) error {
	if in.BaseRole != domain.TeamBaseRoleAdmin && in.BaseRole != domain.TeamBaseRoleMember {
		return errInvalidArgument("membership base role must be admin or member")
	}
	if !in.Sharing.Valid() {
		return errInvalidArgument("invalid sharing flags")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_memberships (
			membership_id, team_id, user_id, base_role, sharing_version, joined_at, ended_at, end_reason
		) VALUES (?, ?, ?, ?, 1, ?, NULL, NULL)`,
		in.MembershipID, in.TeamID, in.UserID, in.BaseRole, in.JoinedAt,
	); err != nil {
		if isDuplicateKey(err) {
			return errMembershipExists()
		}
		return fmt.Errorf("insert membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_current_teams (user_id, team_id, membership_id, joined_at)
		VALUES (?, ?, ?, ?)`,
		in.UserID, in.TeamID, in.MembershipID, in.JoinedAt,
	); err != nil {
		if isDuplicateKey(err) {
			return errMembershipExists()
		}
		return fmt.Errorf("insert current team: %w", err)
	}
	if err := s.insertEnabledGrants(ctx, tx, in.MembershipID, in.Sharing, in.JoinedAt); err != nil {
		return err
	}
	return s.registerOpenDeletionBarriers(ctx, tx, in.UserID, in.TeamID, in.JoinedAt)
}

func (s *teamsStore) insertEnabledGrants(ctx context.Context, tx *sql.Tx, membershipID string, flags domain.SharingFlags, at time.Time) error {
	for _, dim := range sharingDimensions() {
		if !flags.DimensionEnabled(dim) {
			continue
		}
		if err := s.insertActiveGrant(ctx, tx, membershipID, dim, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *teamsStore) insertActiveGrant(ctx context.Context, tx *sql.Tx, membershipID string, dim domain.SharingDimension, at time.Time) error {
	grantID, err := newTeamID(domain.GrantIDPrefix)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_sharing_grants (
			grant_id, membership_id, dimension, starts_at, ends_at, revoked_at, active_dimension
		) VALUES (?, ?, ?, ?, NULL, NULL, ?)`,
		grantID, membershipID, dim, at, dim,
	); err != nil {
		return fmt.Errorf("insert sharing grant: %w", err)
	}
	return nil
}

func (s *teamsStore) revokeActiveGrant(ctx context.Context, tx *sql.Tx, membershipID string, dim domain.SharingDimension, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_sharing_grants
		SET ends_at = ?, revoked_at = ?, active_dimension = NULL
		WHERE membership_id = ? AND active_dimension = ?`,
		at, at, membershipID, dim,
	); err != nil {
		return fmt.Errorf("revoke sharing grant: %w", err)
	}
	return nil
}

func (s *teamsStore) revokeAllActiveGrants(ctx context.Context, tx *sql.Tx, membershipID string, at time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_sharing_grants
		SET ends_at = ?, revoked_at = ?, active_dimension = NULL
		WHERE membership_id = ? AND active_dimension IS NOT NULL`,
		at, at, membershipID,
	); err != nil {
		return fmt.Errorf("revoke all grants: %w", err)
	}
	return nil
}

func (s *teamsStore) loadActiveGrants(ctx context.Context, q rowQueryer, membershipID string) (map[domain.SharingDimension]domain.TeamSharingGrant, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT grant_id, membership_id, dimension, starts_at, ends_at, revoked_at, active_dimension
		FROM team_sharing_grants
		WHERE membership_id = ? AND active_dimension IS NOT NULL`, membershipID)
	if err != nil {
		return nil, fmt.Errorf("list active grants: %w", err)
	}
	defer rows.Close()
	out := make(map[domain.SharingDimension]domain.TeamSharingGrant)
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out[g.Dimension] = g
	}
	return out, rows.Err()
}

func (s *teamsStore) applySharing(ctx context.Context, tx *sql.Tx, membershipID string, current domain.SharingFlags, target domain.SharingFlags, at time.Time) (bool, error) {
	if !target.Valid() {
		return false, errInvalidArgument("invalid sharing flags")
	}
	if !target.Base {
		target.Named = false
		target.Classification = false
		target.Cost = false
	}
	changed := false
	for _, dim := range sharingDimensions() {
		want := target.DimensionEnabled(dim)
		have := current.DimensionEnabled(dim)
		switch {
		case want && !have:
			if err := s.insertActiveGrant(ctx, tx, membershipID, dim, at); err != nil {
				return false, err
			}
			changed = true
		case !want && have:
			if err := s.revokeActiveGrant(ctx, tx, membershipID, dim, at); err != nil {
				return false, err
			}
			changed = true
		}
	}
	return changed, nil
}

func (s *teamsStore) sharingState(ctx context.Context, q rowQueryer, mem domain.TeamMembership) (*domain.TeamSharingState, error) {
	grants, err := s.loadActiveGrants(ctx, q, mem.MembershipID)
	if err != nil {
		return nil, err
	}
	flags := domain.SharingFlags{}
	effective := make(map[string]time.Time)
	for dim, g := range grants {
		switch dim {
		case domain.SharingBase:
			flags.Base = true
		case domain.SharingNamed:
			flags.Named = true
		case domain.SharingClassification:
			flags.Classification = true
		case domain.SharingCost:
			flags.Cost = true
		}
		effective[string(dim)] = g.StartsAt
	}
	return &domain.TeamSharingState{
		MembershipID:   mem.MembershipID,
		SharingVersion: mem.SharingVersion,
		Sharing:        flags,
		EffectiveFrom:  effective,
	}, nil
}

func (s *teamsStore) registerOpenDeletionBarriers(ctx context.Context, tx *sql.Tx, userID, teamID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT request_id
		FROM data_deletion_requests
		WHERE user_id = ? AND request_status IN ('pending', 'running', 'failed')`, userID)
	if err != nil {
		return fmt.Errorf("list open deletions: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.upsertBarrier(ctx, tx, id, teamID, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *teamsStore) upsertBarrier(ctx context.Context, tx *sql.Tx, deletionRequestID, teamID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_deletion_barriers (deletion_request_id, team_id, blocked_at)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE
			blocked_at = IF(released_at IS NULL, blocked_at, VALUES(blocked_at)),
			released_at = IF(released_at IS NULL, released_at, NULL)`,
		deletionRequestID, teamID, now,
	); err != nil {
		return fmt.Errorf("register deletion barrier: %w", err)
	}
	return nil
}

func (s *teamsStore) currentMembership(ctx context.Context, q rowQueryer, teamID, userID string) (*domain.TeamMembership, error) {
	mem, err := scanMembership(q.QueryRowContext(ctx, membershipSelectSQL+`
		INNER JOIN user_current_teams c
		  ON c.membership_id = m.membership_id AND c.team_id = m.team_id AND c.user_id = m.user_id
		WHERE c.team_id = ? AND c.user_id = ?`, teamID, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load current membership: %w", err)
	}
	return mem, nil
}

func (s *teamsStore) loadTeam(ctx context.Context, q rowQueryer, teamID string) (*domain.Team, error) {
	team, err := scanTeam(q.QueryRowContext(ctx, teamSelectSQL+` WHERE team_id = ?`, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errTeamNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("load team: %w", err)
	}
	return team, nil
}

func (s *teamsStore) loadMembership(ctx context.Context, q rowQueryer, membershipID string) (*domain.TeamMembership, error) {
	mem, err := scanMembership(q.QueryRowContext(ctx, membershipSelectSQL+` WHERE membership_id = ?`, membershipID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errResourceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("load membership: %w", err)
	}
	return mem, nil
}

func (s *teamsStore) replayTeamContext(ctx context.Context, tx *sql.Tx, actorUserID, teamID string) (*domain.TeamContext, error) {
	team, err := s.loadTeam(ctx, tx, teamID)
	if err != nil {
		return nil, errCommandResultUnavailable()
	}
	mem, err := s.currentMembership(ctx, tx, teamID, actorUserID)
	if err != nil {
		return nil, err
	}
	if mem == nil || team.Status != domain.TeamStatusActive {
		return nil, errCommandResultUnavailable()
	}
	return teamContextOf(*team, *mem, false), nil
}

func (s *teamsStore) closeMembership(ctx context.Context, tx *sql.Tx, mem *domain.TeamMembership, reason domain.TeamMembershipEndReason, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_memberships
		SET ended_at = ?, end_reason = ?
		WHERE membership_id = ? AND ended_at IS NULL`,
		now, reason, mem.MembershipID,
	); err != nil {
		return fmt.Errorf("close membership: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_current_teams WHERE membership_id = ?`, mem.MembershipID); err != nil {
		return fmt.Errorf("delete current team: %w", err)
	}
	if err := s.revokeAllActiveGrants(ctx, tx, mem.MembershipID, now); err != nil {
		return err
	}
	if err := s.revokeInvitationsFromUser(ctx, tx, mem.TeamID, mem.UserID, now); err != nil {
		return err
	}
	if err := s.revokeInviteLinksFromUser(ctx, tx, mem.TeamID, mem.UserID, now); err != nil {
		return err
	}
	if err := s.revokePendingInvitesForUserEmail(ctx, tx, mem.TeamID, mem.UserID, now); err != nil {
		return err
	}
	ended := now
	mem.EndedAt = &ended
	mem.EndReason = &reason
	return nil
}

func (s *teamsStore) revokeInvitationsFromUser(ctx context.Context, tx *sql.Tx, teamID, userID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_invitations
		SET status = 'revoked', active_recipient_hash = NULL, version = version + 1
		WHERE team_id = ? AND inviter_user_id = ? AND status = 'pending'`,
		teamID, userID,
	); err != nil {
		return fmt.Errorf("revoke invitations from user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE email_outbox
		SET delivery_status = 'cancelled', updated_at = ?
		WHERE team_invitation_id IN (
			SELECT invitation_id FROM team_invitations
			WHERE team_id = ? AND inviter_user_id = ?
		) AND delivery_status IN ('pending', 'sending')`,
		now, teamID, userID,
	); err != nil {
		return fmt.Errorf("cancel invitation outbox: %w", err)
	}
	return nil
}

func (s *teamsStore) revokeInviteLinksFromUser(ctx context.Context, tx *sql.Tx, teamID, userID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_invite_links
		SET status = 'revoked', revoked_at = ?, version = version + 1, token_ciphertext = x''
		WHERE team_id = ? AND creator_user_id = ? AND status = 'active'`,
		now, teamID, userID,
	); err != nil {
		return fmt.Errorf("revoke invite links from user: %w", err)
	}
	return nil
}

func (s *teamsStore) revokePendingInvitesForUserEmail(ctx context.Context, tx *sql.Tx, teamID, userID string, now time.Time) error {
	var hash []byte
	if err := tx.QueryRowContext(ctx, `SELECT email_lookup_hash FROM users WHERE user_id = ?`, userID).Scan(&hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load user email hash: %w", err)
	}
	if len(hash) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_invitations
		SET status = 'revoked', active_recipient_hash = NULL, version = version + 1
		WHERE team_id = ? AND status = 'pending' AND recipient_lookup_hash = ?`,
		teamID, hash,
	); err != nil {
		return fmt.Errorf("revoke matching pending invitations: %w", err)
	}
	_ = now
	return nil
}

func (s *teamsStore) expireOverdueInvitations(ctx context.Context, tx *sql.Tx, teamID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_invitations
		SET status = 'expired', active_recipient_hash = NULL, version = version + 1
		WHERE team_id = ? AND status = 'pending' AND expires_at <= ?`,
		teamID, now,
	); err != nil {
		return fmt.Errorf("expire invitations: %w", err)
	}
	return nil
}

func (s *teamsStore) insertOutbox(ctx context.Context, tx *sql.Tx, outbox domain.EmailOutbox, invitationID string, version uint64, now time.Time) error {
	if outbox.EmailID == "" {
		return nil
	}
	locale := outbox.Locale
	if locale == "" {
		locale = "en-US"
	}
	next := outbox.NextAttemptAt
	if next.IsZero() {
		next = now
	}
	expires := outbox.ExpiresAt
	if expires.IsZero() || !expires.After(now) {
		expires = now.Add(24 * time.Hour)
	}
	created := outbox.CreatedAt
	if created.IsZero() {
		created = now
	}
	updated := outbox.UpdatedAt
	if updated.IsZero() {
		updated = now
	}
	status := outbox.DeliveryStatus
	if status == "" {
		status = "pending"
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO email_outbox (
			email_id, user_id, challenge_id, team_invitation_id, team_invitation_version,
			idempotency_key, template_key, locale, recipient_ciphertext, payload_ciphertext,
			encryption_key_version, delivery_status, attempt_count, next_attempt_at, expires_at, created_at, updated_at
		) VALUES (?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outbox.EmailID, nullStringFromPtr(outbox.UserID), invitationID, version,
		bytes32Slice(outbox.IdempotencyKey), outbox.TemplateKey, locale,
		outbox.RecipientCiphertext, outbox.PayloadCiphertext, outbox.EncryptionKeyVersion,
		status, outbox.AttemptCount, next, expires, created, updated,
	); err != nil {
		return fmt.Errorf("insert invitation outbox: %w", err)
	}
	return nil
}

func (s *teamsStore) GetCurrentTeam(ctx context.Context, userID string) (*domain.UserCurrentTeam, *domain.Team, *domain.TeamMembership, error) {
	var cur domain.UserCurrentTeam
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, team_id, membership_id, joined_at
		FROM user_current_teams
		WHERE user_id = ?`, userID).Scan(&cur.UserID, &cur.TeamID, &cur.MembershipID, &cur.JoinedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get current team: %w", err)
	}
	team, err := s.GetTeam(ctx, cur.TeamID)
	if err != nil {
		return nil, nil, nil, err
	}
	mem, err := s.GetMembership(ctx, cur.TeamID, cur.MembershipID)
	if err != nil {
		return nil, nil, nil, err
	}
	return &cur, team, mem, nil
}

func (s *teamsStore) GetTeam(ctx context.Context, teamID string) (*domain.Team, error) {
	return s.loadTeam(ctx, s.db, teamID)
}

func (s *teamsStore) GetMembership(ctx context.Context, teamID, membershipID string) (*domain.TeamMembership, error) {
	mem, err := s.loadMembership(ctx, s.db, membershipID)
	if err != nil {
		return nil, err
	}
	if mem.TeamID != teamID {
		return nil, errResourceNotFound()
	}
	return mem, nil
}

func (s *teamsStore) ListMembers(ctx context.Context, teamID string, query string, cursor string, limit int) ([]domain.TeamMembership, []domain.User, string, error) {
	return s.listMembersCombined(ctx, teamID, query, cursor, limit)
}

func (s *teamsStore) listMembersCombined(ctx context.Context, teamID, query, cursor string, limit int) ([]domain.TeamMembership, []domain.User, string, error) {
	limit = clampLimit(limit)
	args := []any{teamID}
	var cond strings.Builder
	cond.WriteString(`m.team_id = ? AND m.ended_at IS NULL`)
	q := strings.TrimSpace(query)
	if q != "" {
		pattern := likeContains(q)
		cond.WriteString(` AND (u.display_name LIKE ? ESCAPE '\\' OR u.handle LIKE ? ESCAPE '\\')`)
		args = append(args, pattern, pattern)
	}
	if parts := decodeCursor(cursor); len(parts) == 2 {
		if joinedAt, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			cond.WriteString(` AND (m.joined_at > ? OR (m.joined_at = ? AND m.membership_id > ?))`)
			args = append(args, joinedAt, joinedAt, parts[1])
		}
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.membership_id, m.team_id, m.user_id, m.base_role, m.sharing_version, m.joined_at, m.ended_at, m.end_reason,
		       u.user_id, u.display_name, u.handle, u.account_status, u.timezone_name, u.locale, u.created_at, u.updated_at
		FROM team_memberships m
		INNER JOIN users u ON u.user_id = m.user_id
		WHERE `+cond.String()+`
		ORDER BY m.joined_at ASC, m.membership_id ASC
		LIMIT ?`, args...)
	if err != nil {
		return nil, nil, "", fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var mems []domain.TeamMembership
	var users []domain.User
	for rows.Next() {
		var mem domain.TeamMembership
		var ended sql.NullTime
		var reason sql.NullString
		var user domain.User
		var handle sql.NullString
		if err := rows.Scan(
			&mem.MembershipID, &mem.TeamID, &mem.UserID, &mem.BaseRole, &mem.SharingVersion, &mem.JoinedAt, &ended, &reason,
			&user.UserID, &user.DisplayName, &handle, &user.AccountStatus, &user.TimezoneName, &user.Locale, &user.CreatedAt, &user.UpdatedAt,
		); err != nil {
			return nil, nil, "", err
		}
		mem.EndedAt = ptrFromNullTime(ended)
		if reason.Valid {
			r := domain.TeamMembershipEndReason(reason.String)
			mem.EndReason = &r
		}
		if handle.Valid {
			user.Handle = &handle.String
		}
		mems = append(mems, mem)
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, "", err
	}
	next := ""
	if len(mems) > limit {
		last := mems[limit-1]
		mems = mems[:limit]
		users = users[:limit]
		next = encodeCursor(last.JoinedAt.UTC().Format(time.RFC3339Nano), last.MembershipID)
	}
	return mems, users, next, nil
}

func (s *teamsStore) ListInvitationsForEmail(ctx context.Context, emailLookupHash [32]byte, cursor string, limit int) ([]domain.TeamInvitation, string, error) {
	return s.listInvitations(ctx, `
		SELECT `+invitationColumns+`
		FROM team_invitations
		WHERE recipient_lookup_hash = ?`, []any{emailLookupHash[:]}, cursor, limit)
}

func (s *teamsStore) ListTeamInvitations(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamInvitation, string, error) {
	return s.listInvitations(ctx, `
		SELECT `+invitationColumns+`
		FROM team_invitations
		WHERE team_id = ?`, []any{teamID}, cursor, limit)
}

func (s *teamsStore) listInvitations(ctx context.Context, base string, args []any, cursor string, limit int) ([]domain.TeamInvitation, string, error) {
	limit = clampLimit(limit)
	if parts := decodeCursor(cursor); len(parts) == 2 {
		if createdAt, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			base += ` AND (created_at > ? OR (created_at = ? AND invitation_id > ?))`
			args = append(args, createdAt, createdAt, parts[1])
		}
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, base+` ORDER BY created_at ASC, invitation_id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()
	var out []domain.TeamInvitation
	for rows.Next() {
		inv, err := scanInvitation(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *inv)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.InvitationID)
	}
	return out, next, nil
}

func (s *teamsStore) GetInvitation(ctx context.Context, invitationID string) (*domain.TeamInvitation, error) {
	inv, err := scanInvitation(s.db.QueryRowContext(ctx, invitationSelectSQL+` WHERE invitation_id = ?`, invitationID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInvitationNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get invitation: %w", err)
	}
	return inv, nil
}

func (s *teamsStore) ListInviteLinks(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamInviteLink, string, error) {
	limit = clampLimit(limit)
	args := []any{teamID}
	q := inviteLinkSelectSQL + ` WHERE team_id = ?`
	if parts := decodeCursor(cursor); len(parts) == 2 {
		if createdAt, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			q += ` AND (created_at > ? OR (created_at = ? AND link_id > ?))`
			args = append(args, createdAt, createdAt, parts[1])
		}
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at ASC, link_id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list invite links: %w", err)
	}
	defer rows.Close()
	var out []domain.TeamInviteLink
	for rows.Next() {
		link, err := scanInviteLink(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *link)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.LinkID)
	}
	return out, next, nil
}

func (s *teamsStore) GetInviteLink(ctx context.Context, linkID string) (*domain.TeamInviteLink, error) {
	link, err := scanInviteLink(s.db.QueryRowContext(ctx, inviteLinkSelectSQL+` WHERE link_id = ?`, linkID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteLinkNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get invite link: %w", err)
	}
	return link, nil
}

func (s *teamsStore) GetInviteLinkByTokenHash(ctx context.Context, tokenHash [32]byte) (*domain.TeamInviteLink, error) {
	link, err := scanInviteLink(s.db.QueryRowContext(ctx, inviteLinkSelectSQL+` WHERE token_hash = ?`, tokenHash[:]))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteLinkNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get invite link by token: %w", err)
	}
	if !hashesEqual(link.TokenHash, tokenHash) {
		return nil, errInviteLinkNotFound()
	}
	return link, nil
}

func (s *teamsStore) GetMySharing(ctx context.Context, teamID, userID string) (*domain.TeamSharingState, error) {
	mem, err := s.currentMembership(ctx, s.db, teamID, userID)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, errTeamNotFound()
	}
	return s.sharingState(ctx, s.db, *mem)
}

func (s *teamsStore) ListAuditEvents(ctx context.Context, teamID string, cursor string, limit int) ([]domain.TeamAuditEvent, string, error) {
	limit = clampLimit(limit)
	args := []any{teamID}
	q := `
		SELECT audit_id, team_id, actor_user_id, action, target_type, target_id, safe_details_json, created_at
		FROM team_audit_events
		WHERE team_id = ?`
	if parts := decodeCursor(cursor); len(parts) == 2 {
		if createdAt, err := time.Parse(time.RFC3339Nano, parts[0]); err == nil {
			q += ` AND (created_at > ? OR (created_at = ? AND audit_id > ?))`
			args = append(args, createdAt, createdAt, parts[1])
		}
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY created_at ASC, audit_id ASC LIMIT ?`, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	var out []domain.TeamAuditEvent
	for rows.Next() {
		var ev domain.TeamAuditEvent
		var actor sql.NullString
		var details []byte
		if err := rows.Scan(&ev.AuditID, &ev.TeamID, &actor, &ev.Action, &ev.TargetType, &ev.TargetID, &details, &ev.CreatedAt); err != nil {
			return nil, "", err
		}
		ev.ActorUserID = ptrFromNullString(actor)
		ev.SafeDetailsJSON = string(details)
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		last := out[limit-1]
		out = out[:limit]
		next = encodeCursor(last.CreatedAt.UTC().Format(time.RFC3339Nano), last.AuditID)
	}
	return out, next, nil
}

func (s *teamsStore) ListExports(ctx context.Context, teamID, requesterUserID string) ([]domain.TeamExportJob, error) {
	rows, err := s.db.QueryContext(ctx, exportSelectSQL+`
		WHERE team_id = ? AND requester_user_id = ?
		ORDER BY created_at DESC, export_id DESC`, teamID, requesterUserID)
	if err != nil {
		return nil, fmt.Errorf("list exports: %w", err)
	}
	defer rows.Close()
	var out []domain.TeamExportJob
	for rows.Next() {
		job, err := scanExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

func (s *teamsStore) GetExport(ctx context.Context, teamID, exportID string) (*domain.TeamExportJob, error) {
	job, err := scanExport(s.db.QueryRowContext(ctx, exportSelectSQL+` WHERE export_id = ? AND team_id = ?`, exportID, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errResourceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get export: %w", err)
	}
	return job, nil
}

func (s *teamsStore) GetAvatarObject(ctx context.Context, teamID, objectID string) (*domain.TeamUploadObject, error) {
	obj, err := scanUploadObject(s.db.QueryRowContext(ctx, uploadSelectSQL+` WHERE object_id = ? AND team_id = ?`, objectID, teamID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errResourceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("get avatar object: %w", err)
	}
	return obj, nil
}

func (s *teamsStore) HasOpenDeletionBarrier(ctx context.Context, teamID string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM team_deletion_barriers
		WHERE team_id = ? AND released_at IS NULL
		LIMIT 1`, teamID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("has open deletion barrier: %w", err)
	}
	return true, nil
}

func (s *teamsStore) CreateTeamTx(ctx context.Context, in store.CreateTeamTxInput) (*store.CreateTeamTxResult, error) {
	var result *store.CreateTeamTxResult
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		out, err := s.createTeamTx(ctx, tx, in)
		result = out
		return err
	})
	return result, err
}

func (s *teamsStore) createTeamTx(ctx context.Context, tx *sql.Tx, in store.CreateTeamTxInput) (*store.CreateTeamTxResult, error) {
	if !in.Sharing.Valid() {
		return nil, errInvalidArgument("invalid sharing flags")
	}
	if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
		return nil, err
	}
	actor, err := s.lockUser(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if err := actor.requireActiveOnboarded(); err != nil {
		return nil, err
	}
	now, err := s.txNow(ctx, tx, in.Now)
	if err != nil {
		return nil, err
	}
	if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
		return nil, err
	} else if rec != nil {
		ctxn, err := s.replayTeamContext(ctx, tx, in.ActorUserID, rec.ResultID)
		if err != nil {
			return nil, err
		}
		return &store.CreateTeamTxResult{Outcome: store.TeamsTxReplay, Context: ctxn, Receipt: rec}, nil
	}
	current, err := s.lockCurrent(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if current != nil {
		return nil, errMembershipExists()
	}
	team := in.Team
	if team.TeamID == "" {
		id, err := newTeamID(domain.TeamIDPrefix)
		if err != nil {
			return nil, err
		}
		team.TeamID = id
	}
	mem := in.Membership
	if mem.MembershipID == "" {
		id, err := newTeamID(domain.MembershipIDPrefix)
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
	mem.TeamID = team.TeamID
	mem.UserID = in.ActorUserID
	mem.BaseRole = domain.TeamBaseRoleAdmin
	mem.SharingVersion = 1
	mem.JoinedAt = now
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO teams (
			team_id, name, description, timezone_name, owner_user_id, status,
			profile_version, auth_revision, avatar_object_id, created_at, dissolved_at
		) VALUES (?, ?, ?, ?, ?, 'active', ?, ?, NULL, ?, NULL)`,
		team.TeamID, team.Name, team.Description, team.TimezoneName, team.OwnerUserID,
		team.ProfileVersion, team.AuthRevision, now,
	); err != nil {
		if isDuplicateKey(err) {
			return nil, errTeamUnavailable(err)
		}
		return nil, fmt.Errorf("insert team: %w", err)
	}
	if err := s.occupyMembership(ctx, tx, occupyMembershipInput{
		MembershipID: mem.MembershipID,
		TeamID:       team.TeamID,
		UserID:       in.ActorUserID,
		BaseRole:     domain.TeamBaseRoleAdmin,
		Sharing:      in.Sharing,
		JoinedAt:     now,
	}); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_source_revisions (team_id, source_revision, changed_at)
		VALUES (?, 0, ?)`, team.TeamID, now); err != nil {
		return nil, fmt.Errorf("insert source revision: %w", err)
	}
	if err := s.insertAudit(ctx, tx, team.TeamID, in.ActorUserID, "create", "team", team.TeamID, map[string]any{
		"result": "created",
	}, now); err != nil {
		return nil, err
	}
	rec, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now)
	if err != nil {
		return nil, err
	}
	loaded, err := s.loadTeam(ctx, tx, team.TeamID)
	if err != nil {
		return nil, err
	}
	loadedMem, err := s.loadMembership(ctx, tx, mem.MembershipID)
	if err != nil {
		return nil, err
	}
	return &store.CreateTeamTxResult{
		Outcome: store.TeamsTxChanged,
		Context: teamContextOf(*loaded, *loadedMem, false),
		Receipt: rec,
	}, nil
}

func (s *teamsStore) UpdateTeamProfileTx(ctx context.Context, in store.UpdateTeamProfileTxInput) (*domain.Team, error) {
	var result *domain.Team
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		mem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if team.ProfileVersion != in.ExpectedProfileVersion {
			return errVersionConflict()
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		name := team.Name
		desc := team.Description
		if in.Name != nil {
			name = *in.Name
		}
		if in.Description != nil {
			desc = *in.Description
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE teams
			SET name = ?, description = ?, profile_version = profile_version + 1
			WHERE team_id = ? AND profile_version = ?`,
			name, desc, in.TeamID, in.ExpectedProfileVersion,
		); err != nil {
			return fmt.Errorf("update team profile: %w", err)
		}
		updated, err := s.loadTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "profile_change", "team", in.TeamID, map[string]any{
			"profileVersion": strconv.FormatUint(updated.ProfileVersion, 10),
		}, now); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *teamsStore) CreateInvitationTx(ctx context.Context, in store.CreateInvitationTxInput) (*store.CreateInvitationTxResult, error) {
	var result *store.CreateInvitationTxResult
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		out, err := s.createInvitationTx(ctx, tx, in)
		result = out
		return err
	})
	return result, err
}

func (s *teamsStore) createInvitationTx(ctx context.Context, tx *sql.Tx, in store.CreateInvitationTxInput) (*store.CreateInvitationTxResult, error) {
	if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
		return nil, err
	}
	actor, err := s.lockUser(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if err := actor.requireActiveOnboarded(); err != nil {
		return nil, err
	}
	team, err := s.lockTeam(ctx, tx, in.TeamID)
	if err != nil {
		return nil, err
	}
	if team.Status != domain.TeamStatusActive {
		return nil, errTeamNotFound()
	}
	mem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if !canGrantRole(*team, mem, in.InvitedRole) {
		if mem == nil {
			return nil, errTeamNotFound()
		}
		return nil, errPermissionDenied()
	}
	now, err := s.txNow(ctx, tx, in.Now)
	if err != nil {
		return nil, err
	}
	if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
		return nil, err
	} else if rec != nil {
		inv, err := s.GetInvitation(ctx, rec.ResultID)
		if err != nil {
			inv, err = scanInvitation(tx.QueryRowContext(ctx, invitationSelectSQL+` WHERE invitation_id = ?`, rec.ResultID))
			if err != nil {
				return nil, errCommandResultUnavailable()
			}
		}
		return &store.CreateInvitationTxResult{Outcome: store.TeamsTxReplay, Invitation: inv}, nil
	}
	if err := s.expireOverdueInvitations(ctx, tx, in.TeamID, now); err != nil {
		return nil, err
	}
	inv := in.Invitation
	if inv.InvitationID == "" {
		id, err := newTeamID(domain.InvitationIDPrefix)
		if err != nil {
			return nil, err
		}
		inv.InvitationID = id
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
	active := inv.RecipientLookupHash
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_invitations (
			invitation_id, team_id, inviter_user_id, invited_role, recipient_lookup_hash, lookup_key_version,
			recipient_ciphertext, encryption_key_version, status, active_recipient_hash, created_at, expires_at, version
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, 1)`,
		inv.InvitationID, inv.TeamID, inv.InviterUserID, inv.InvitedRole, inv.RecipientLookupHash[:], inv.LookupKeyVersion,
		inv.RecipientCiphertext, inv.EncryptionKeyVersion, active[:], inv.CreatedAt, inv.ExpiresAt,
	); err != nil {
		if isDuplicateKey(err) {
			return nil, errInvitationPending()
		}
		return nil, fmt.Errorf("insert invitation: %w", err)
	}
	if err := s.insertOutbox(ctx, tx, in.Outbox, inv.InvitationID, inv.Version, now); err != nil {
		return nil, err
	}
	if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "invite", "invitation", inv.InvitationID, map[string]any{
		"role": string(inv.InvitedRole),
	}, now); err != nil {
		return nil, err
	}
	if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "invitation", inv.InvitationID, now); err != nil {
		return nil, err
	}
	inv.ActiveRecipientHash = &active
	return &store.CreateInvitationTxResult{Outcome: store.TeamsTxChanged, Invitation: &inv}, nil
}

func (s *teamsStore) RevokeInvitationTx(ctx context.Context, actorUserID, teamID, invitationID string, expectedVersion uint64, idem store.TeamsIdempotency, now time.Time) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, actorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, teamID)
		if err != nil {
			return err
		}
		inv, err := s.lockInvitation(ctx, tx, invitationID)
		if err != nil {
			return err
		}
		if inv.TeamID != teamID {
			return errInvitationNotFound()
		}
		tNow, err := s.txNow(ctx, tx, now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, idem, actorUserID, tNow); err != nil {
			return err
		} else if rec != nil {
			return nil
		}
		mem, err := s.currentMembership(ctx, tx, teamID, actorUserID)
		if err != nil {
			return err
		}
		if !canGrantRole(*team, mem, inv.InvitedRole) && !isManager(*team, mem) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if inv.Version != expectedVersion {
			return errVersionConflict()
		}
		if inv.Status != domain.InvitationPending {
			if _, err := s.insertReceipt(ctx, tx, actorUserID, idem, "invitation", invitationID, tNow); err != nil {
				return err
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_invitations
			SET status = 'revoked', active_recipient_hash = NULL, version = version + 1
			WHERE invitation_id = ?`, invitationID); err != nil {
			return fmt.Errorf("revoke invitation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE email_outbox
			SET delivery_status = 'cancelled', updated_at = ?
			WHERE team_invitation_id = ? AND delivery_status IN ('pending', 'sending')`,
			tNow, invitationID); err != nil {
			return fmt.Errorf("cancel invitation outbox: %w", err)
		}
		if err := s.insertAudit(ctx, tx, teamID, actorUserID, "revoke", "invitation", invitationID, map[string]any{
			"result": "revoked",
		}, tNow); err != nil {
			return err
		}
		_, err = s.insertReceipt(ctx, tx, actorUserID, idem, "invitation", invitationID, tNow)
		return err
	})
}

func (s *teamsStore) ResendInvitationTx(ctx context.Context, actorUserID, teamID, invitationID string, expectedVersion uint64, newInvitation domain.TeamInvitation, outbox domain.EmailOutbox, idem store.TeamsIdempotency, now time.Time) (*domain.TeamInvitation, error) {
	var result *domain.TeamInvitation
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, actorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, teamID)
		if err != nil {
			return err
		}
		old, err := s.lockInvitation(ctx, tx, invitationID)
		if err != nil {
			return err
		}
		if old.TeamID != teamID {
			return errInvitationNotFound()
		}
		tNow, err := s.txNow(ctx, tx, now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, idem, actorUserID, tNow); err != nil {
			return err
		} else if rec != nil {
			inv, err := scanInvitation(tx.QueryRowContext(ctx, invitationSelectSQL+` WHERE invitation_id = ?`, rec.ResultID))
			if err != nil {
				return errCommandResultUnavailable()
			}
			result = inv
			return nil
		}
		mem, err := s.currentMembership(ctx, tx, teamID, actorUserID)
		if err != nil {
			return err
		}
		if !canGrantRole(*team, mem, old.InvitedRole) {
			if mem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if old.Version != expectedVersion {
			return errVersionConflict()
		}
		if old.Status != domain.InvitationPending {
			return errInvitationNotFound()
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_invitations
			SET status = 'revoked', active_recipient_hash = NULL, version = version + 1
			WHERE invitation_id = ?`, invitationID); err != nil {
			return fmt.Errorf("revoke old invitation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE email_outbox
			SET delivery_status = 'cancelled', updated_at = ?
			WHERE team_invitation_id = ? AND delivery_status IN ('pending', 'sending')`,
			tNow, invitationID); err != nil {
			return fmt.Errorf("cancel old invitation outbox: %w", err)
		}
		if newInvitation.InvitationID == "" {
			id, err := newTeamID(domain.InvitationIDPrefix)
			if err != nil {
				return err
			}
			newInvitation.InvitationID = id
		}
		newInvitation.TeamID = teamID
		newInvitation.InviterUserID = actorUserID
		if newInvitation.InvitedRole == "" {
			newInvitation.InvitedRole = old.InvitedRole
		}
		if !canGrantRole(*team, mem, newInvitation.InvitedRole) {
			return errPermissionDenied()
		}
		newInvitation.RecipientLookupHash = old.RecipientLookupHash
		if len(newInvitation.RecipientCiphertext) == 0 {
			newInvitation.RecipientCiphertext = old.RecipientCiphertext
		}
		if newInvitation.LookupKeyVersion == 0 {
			newInvitation.LookupKeyVersion = old.LookupKeyVersion
		}
		if newInvitation.EncryptionKeyVersion == 0 {
			newInvitation.EncryptionKeyVersion = old.EncryptionKeyVersion
		}
		newInvitation.Status = domain.InvitationPending
		newInvitation.Version = 1
		newInvitation.CreatedAt = tNow
		if newInvitation.ExpiresAt.IsZero() || !newInvitation.ExpiresAt.After(tNow) {
			newInvitation.ExpiresAt = tNow.Add(7 * 24 * time.Hour)
		}
		active := newInvitation.RecipientLookupHash
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO team_invitations (
				invitation_id, team_id, inviter_user_id, invited_role, recipient_lookup_hash, lookup_key_version,
				recipient_ciphertext, encryption_key_version, status, active_recipient_hash, created_at, expires_at, version
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, 1)`,
			newInvitation.InvitationID, newInvitation.TeamID, newInvitation.InviterUserID, newInvitation.InvitedRole,
			newInvitation.RecipientLookupHash[:], newInvitation.LookupKeyVersion, newInvitation.RecipientCiphertext,
			newInvitation.EncryptionKeyVersion, active[:], newInvitation.CreatedAt, newInvitation.ExpiresAt,
		); err != nil {
			if isDuplicateKey(err) {
				return errInvitationPending()
			}
			return fmt.Errorf("insert resent invitation: %w", err)
		}
		if err := s.insertOutbox(ctx, tx, outbox, newInvitation.InvitationID, 1, tNow); err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, teamID, actorUserID, "resend", "invitation", newInvitation.InvitationID, map[string]any{
			"replaced": invitationID,
		}, tNow); err != nil {
			return err
		}
		if _, err := s.insertReceipt(ctx, tx, actorUserID, idem, "invitation", newInvitation.InvitationID, tNow); err != nil {
			return err
		}
		newInvitation.ActiveRecipientHash = &active
		result = &newInvitation
		return nil
	})
	return result, err
}

func (s *teamsStore) AcceptInvitationTx(ctx context.Context, in store.AcceptInvitationTxInput) (*store.AcceptInvitationTxResult, error) {
	var peek domain.TeamInvitation
	if err := s.db.QueryRowContext(ctx, `
		SELECT team_id, inviter_user_id FROM team_invitations WHERE invitation_id = ?`, in.InvitationID,
	).Scan(&peek.TeamID, &peek.InviterUserID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errInvitationNotFound()
		}
		return nil, fmt.Errorf("peek invitation: %w", err)
	}
	var result *store.AcceptInvitationTxResult
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		out, err := s.acceptInvitationTx(ctx, tx, in, peek)
		result = out
		return err
	})
	return result, err
}

func (s *teamsStore) acceptInvitationTx(ctx context.Context, tx *sql.Tx, in store.AcceptInvitationTxInput, peek domain.TeamInvitation) (*store.AcceptInvitationTxResult, error) {
	if !in.Sharing.Valid() {
		return nil, errInvalidArgument("invalid sharing flags")
	}
	if _, err := s.lockUsers(ctx, tx, in.ActorUserID, peek.InviterUserID); err != nil {
		return nil, err
	}
	actor, err := s.lockUser(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if err := actor.requireEmailVerified(); err != nil {
		return nil, err
	}
	team, err := s.lockTeam(ctx, tx, peek.TeamID)
	if err != nil {
		return nil, err
	}
	inv, err := s.lockInvitation(ctx, tx, in.InvitationID)
	if err != nil {
		return nil, err
	}
	if inv.TeamID != peek.TeamID || inv.InviterUserID != peek.InviterUserID {
		return nil, errInvitationNotFound()
	}
	if !bytesHashEqual(inv.RecipientLookupHash[:], in.VerifiedEmailLookupHash) {
		return nil, errInvitationNotFound()
	}
	now, err := s.txNow(ctx, tx, in.Now)
	if err != nil {
		return nil, err
	}
	if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
		return nil, err
	} else if rec != nil {
		ctxn, err := s.replayTeamContext(ctx, tx, in.ActorUserID, team.TeamID)
		if err != nil {
			return nil, err
		}
		return &store.AcceptInvitationTxResult{Outcome: store.TeamsTxReplay, Context: ctxn}, nil
	}
	if team.Status != domain.TeamStatusActive {
		return nil, errTeamNotFound()
	}
	current, err := s.lockCurrent(ctx, tx, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	history, err := s.lockLatestHistory(ctx, tx, team.TeamID, in.ActorUserID)
	if err != nil {
		return nil, err
	}
	if inv.Status == domain.InvitationAccepted {
		if inv.AcceptedByUserID != nil && *inv.AcceptedByUserID == in.ActorUserID && inv.AcceptedMembershipID != nil {
			if current != nil && current.MembershipID == *inv.AcceptedMembershipID {
				mem, err := s.loadMembership(ctx, tx, current.MembershipID)
				if err != nil {
					return nil, err
				}
				if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
					return nil, err
				}
				return &store.AcceptInvitationTxResult{
					Outcome: store.TeamsTxAlreadyMember,
					Context: teamContextOf(*team, *mem, true),
				}, nil
			}
			return nil, errInvitationNotFound()
		}
		return nil, errInvitationNotFound()
	}
	if inv.Status == domain.InvitationRevoked {
		return nil, errInvitationRevoked()
	}
	if inv.Status == domain.InvitationExpired || !inv.ExpiresAt.After(now) {
		if inv.Status == domain.InvitationPending {
			if _, err := tx.ExecContext(ctx, `
				UPDATE team_invitations
				SET status = 'expired', active_recipient_hash = NULL, version = version + 1
				WHERE invitation_id = ?`, inv.InvitationID); err != nil {
				return nil, fmt.Errorf("expire invitation: %w", err)
			}
		}
		return nil, errInvitationExpired()
	}
	if inv.Status != domain.InvitationPending {
		return nil, errInvitationNotFound()
	}
	if inv.Version != in.ExpectedVersion {
		return nil, errVersionConflict()
	}
	inviterMem, err := s.currentMembership(ctx, tx, team.TeamID, inv.InviterUserID)
	if err != nil {
		return nil, err
	}
	if !canGrantRole(*team, inviterMem, inv.InvitedRole) {
		return nil, errInvitationNotFound()
	}
	if history != nil && history.EndReason != nil && *history.EndReason == domain.TeamEndReasonRemoved {
		if !inv.CreatedAt.After(derefTime(history.EndedAt)) {
			return nil, errInvitationNotFound()
		}
	}
	if current != nil && current.TeamID != team.TeamID {
		return nil, errMembershipExists()
	}
	if current != nil && current.TeamID == team.TeamID {
		mem, err := s.loadMembership(ctx, tx, current.MembershipID)
		if err != nil {
			return nil, err
		}
		if err := s.markInvitationAccepted(ctx, tx, inv.InvitationID, in.ActorUserID, mem.MembershipID); err != nil {
			return nil, err
		}
		if err := s.insertAudit(ctx, tx, team.TeamID, in.ActorUserID, "accept", "invitation", inv.InvitationID, map[string]any{
			"alreadyMember": true,
		}, now); err != nil {
			return nil, err
		}
		if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
			return nil, err
		}
		return &store.AcceptInvitationTxResult{
			Outcome: store.TeamsTxAlreadyMember,
			Context: teamContextOf(*team, *mem, true),
		}, nil
	}
	membershipID, err := newTeamID(domain.MembershipIDPrefix)
	if err != nil {
		return nil, err
	}
	if err := s.occupyMembership(ctx, tx, occupyMembershipInput{
		MembershipID: membershipID,
		TeamID:       team.TeamID,
		UserID:       in.ActorUserID,
		BaseRole:     inv.InvitedRole,
		Sharing:      in.Sharing,
		JoinedAt:     now,
	}); err != nil {
		return nil, err
	}
	if err := s.markInvitationAccepted(ctx, tx, inv.InvitationID, in.ActorUserID, membershipID); err != nil {
		return nil, err
	}
	if _, err := s.bumpAuth(ctx, tx, team.TeamID); err != nil {
		return nil, err
	}
	if err := s.insertAudit(ctx, tx, team.TeamID, in.ActorUserID, "accept", "invitation", inv.InvitationID, map[string]any{
		"membershipId": membershipID,
	}, now); err != nil {
		return nil, err
	}
	if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", team.TeamID, now); err != nil {
		return nil, err
	}
	loadedTeam, err := s.loadTeam(ctx, tx, team.TeamID)
	if err != nil {
		return nil, err
	}
	loadedMem, err := s.loadMembership(ctx, tx, membershipID)
	if err != nil {
		return nil, err
	}
	return &store.AcceptInvitationTxResult{
		Outcome: store.TeamsTxChanged,
		Context: teamContextOf(*loadedTeam, *loadedMem, false),
	}, nil
}

func (s *teamsStore) markInvitationAccepted(ctx context.Context, tx *sql.Tx, invitationID, userID, membershipID string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_invitations
		SET status = 'accepted', active_recipient_hash = NULL,
		    accepted_by_user_id = ?, accepted_membership_id = ?, version = version + 1
		WHERE invitation_id = ?`, userID, membershipID, invitationID); err != nil {
		return fmt.Errorf("mark invitation accepted: %w", err)
	}
	return nil
}

func (s *teamsStore) UpdateSharingTx(ctx context.Context, in store.UpdateSharingTxInput) (*domain.TeamSharingState, error) {
	var result *domain.TeamSharingState
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		mem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if mem == nil {
			return errTeamNotFound()
		}
		if mem.SharingVersion != in.ExpectedVersion {
			return errVersionConflict()
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		state, err := s.sharingState(ctx, tx, *mem)
		if err != nil {
			return err
		}
		changed, err := s.applySharing(ctx, tx, mem.MembershipID, state.Sharing, in.Sharing, now)
		if err != nil {
			return err
		}
		if changed {
			if _, err := tx.ExecContext(ctx, `
				UPDATE team_memberships SET sharing_version = sharing_version + 1 WHERE membership_id = ?`, mem.MembershipID); err != nil {
				return fmt.Errorf("bump sharing version: %w", err)
			}
			if _, err := s.bumpAuth(ctx, tx, in.TeamID); err != nil {
				return err
			}
			if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "sharing_change", "membership", mem.MembershipID, map[string]any{
				"sharing": in.Sharing,
			}, now); err != nil {
				return err
			}
			updated, err := s.loadMembership(ctx, tx, mem.MembershipID)
			if err != nil {
				return err
			}
			mem = updated
		}
		state, err = s.sharingState(ctx, tx, *mem)
		if err != nil {
			return err
		}
		result = state
		return nil
	})
	return result, err
}

func (s *teamsStore) ChangeMemberRoleTx(ctx context.Context, in store.ChangeMemberRoleTxInput) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		actorMem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if publicRole(*team, actorMem) != domain.TeamRoleOwner {
			if actorMem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if team.AuthRevision != in.ExpectedAuthRevision {
			return errVersionConflict()
		}
		target, err := s.lockMembership(ctx, tx, in.MembershipID)
		if err != nil {
			return err
		}
		if target.TeamID != in.TeamID || target.EndedAt != nil {
			return errResourceNotFound()
		}
		if target.UserID == team.OwnerUserID {
			return errPermissionDenied()
		}
		if in.NewRole != domain.TeamBaseRoleAdmin && in.NewRole != domain.TeamBaseRoleMember {
			return errInvalidArgument("role must be admin or member")
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if target.BaseRole == in.NewRole {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE team_memberships SET base_role = ? WHERE membership_id = ?`, in.NewRole, in.MembershipID); err != nil {
			return fmt.Errorf("change member role: %w", err)
		}
		if in.NewRole == domain.TeamBaseRoleMember {
			if err := s.revokeInviteLinksFromUser(ctx, tx, in.TeamID, target.UserID, now); err != nil {
				return err
			}
			if err := s.revokeInvitationsFromUser(ctx, tx, in.TeamID, target.UserID, now); err != nil {
				return err
			}
		}
		if _, err := s.bumpAuth(ctx, tx, in.TeamID); err != nil {
			return err
		}
		return s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "role_change", "membership", in.MembershipID, map[string]any{
			"from": string(target.BaseRole),
			"to":   string(in.NewRole),
		}, now)
	})
}

func (s *teamsStore) RemoveMemberTx(ctx context.Context, in store.RemoveMemberTxInput) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		targetPeek, err := s.loadMembership(ctx, s.db, in.MembershipID)
		if err != nil {
			return err
		}
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID, targetPeek.UserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			return nil
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		if team.AuthRevision != in.ExpectedAuthRevision {
			return errVersionConflict()
		}
		actorMem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		target, err := s.lockMembership(ctx, tx, in.MembershipID)
		if err != nil {
			return err
		}
		if target.TeamID != in.TeamID || target.EndedAt != nil {
			return errResourceNotFound()
		}
		if target.UserID == team.OwnerUserID || target.UserID == in.ActorUserID {
			return errPermissionDenied()
		}
		role := publicRole(*team, actorMem)
		switch role {
		case domain.TeamRoleOwner:
		case domain.TeamRoleAdmin:
			if target.BaseRole != domain.TeamBaseRoleMember {
				return errPermissionDenied()
			}
		default:
			if actorMem == nil {
				return errTeamNotFound()
			}
			return errPermissionDenied()
		}
		if _, err := s.lockCurrent(ctx, tx, target.UserID); err != nil {
			return err
		}
		if err := s.closeMembership(ctx, tx, target, domain.TeamEndReasonRemoved, now); err != nil {
			return err
		}
		if _, err := s.bumpAuth(ctx, tx, in.TeamID); err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "remove", "membership", in.MembershipID, map[string]any{
			"userId": target.UserID,
		}, now); err != nil {
			return err
		}
		_, err = s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "membership", in.MembershipID, now)
		return err
	})
}

func (s *teamsStore) LeaveTeamTx(ctx context.Context, in store.LeaveTeamTxInput) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			return nil
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		if team.AuthRevision != in.ExpectedAuthRevision {
			return errVersionConflict()
		}
		if team.OwnerUserID == in.ActorUserID {
			return errPermissionDenied()
		}
		current, err := s.lockCurrent(ctx, tx, in.ActorUserID)
		if err != nil {
			return err
		}
		if current == nil || current.TeamID != in.TeamID {
			return errTeamNotFound()
		}
		mem, err := s.lockMembership(ctx, tx, current.MembershipID)
		if err != nil {
			return err
		}
		if err := s.closeMembership(ctx, tx, mem, domain.TeamEndReasonLeft, now); err != nil {
			return err
		}
		if _, err := s.bumpAuth(ctx, tx, in.TeamID); err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "leave", "membership", mem.MembershipID, map[string]any{
			"result": "left",
		}, now); err != nil {
			return err
		}
		_, err = s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", in.TeamID, now)
		return err
	})
}

func (s *teamsStore) TransferOwnershipTx(ctx context.Context, in store.TransferOwnershipTxInput) (*domain.TeamContext, error) {
	var result *domain.TeamContext
	err := s.doTx(ctx, func(tx *sql.Tx) error {
		targetPeek, err := s.loadMembership(ctx, s.db, in.TargetMembershipID)
		if err != nil {
			return err
		}
		if _, err := s.lockUsers(ctx, tx, in.ActorUserID, targetPeek.UserID); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			ctxn, err := s.replayTeamContext(ctx, tx, in.ActorUserID, in.TeamID)
			if err != nil {
				return err
			}
			result = ctxn
			return nil
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		if team.OwnerUserID != in.ActorUserID {
			return errPermissionDenied()
		}
		if team.AuthRevision != in.ExpectedAuthRevision {
			return errVersionConflict()
		}
		if team.Name != in.ConfirmTeamName {
			return errInvalidArgument("team name confirmation does not match")
		}
		target, err := s.lockMembership(ctx, tx, in.TargetMembershipID)
		if err != nil {
			return err
		}
		if target.TeamID != in.TeamID || target.EndedAt != nil || target.UserID == in.ActorUserID {
			return errResourceNotFound()
		}
		users, err := s.lockUsers(ctx, tx, target.UserID)
		if err != nil {
			return err
		}
		if err := users[target.UserID].requireActiveOnboarded(); err != nil {
			return err
		}
		current, err := s.lockCurrent(ctx, tx, target.UserID)
		if err != nil {
			return err
		}
		if current == nil || current.MembershipID != target.MembershipID {
			return errResourceNotFound()
		}
		actorMem, err := s.currentMembership(ctx, tx, in.TeamID, in.ActorUserID)
		if err != nil {
			return err
		}
		if actorMem == nil {
			return errTeamNotFound()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE teams SET owner_user_id = ? WHERE team_id = ?`, target.UserID, in.TeamID); err != nil {
			return fmt.Errorf("transfer owner: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE team_memberships SET base_role = ? WHERE membership_id = ?`, domain.TeamBaseRoleAdmin, actorMem.MembershipID); err != nil {
			return fmt.Errorf("downgrade previous owner: %w", err)
		}
		if _, err := s.bumpAuth(ctx, tx, in.TeamID); err != nil {
			return err
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "transfer", "membership", target.MembershipID, map[string]any{
			"toUserId": target.UserID,
		}, now); err != nil {
			return err
		}
		if _, err := s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", in.TeamID, now); err != nil {
			return err
		}
		loadedTeam, err := s.loadTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		loadedMem, err := s.loadMembership(ctx, tx, actorMem.MembershipID)
		if err != nil {
			return err
		}
		result = teamContextOf(*loadedTeam, *loadedMem, false)
		return nil
	})
	return result, err
}

func (s *teamsStore) DissolveTeamTx(ctx context.Context, in store.DissolveTeamTxInput) error {
	return s.doTx(ctx, func(tx *sql.Tx) error {
		memberIDs, err := s.listCurrentMemberIDs(ctx, s.db, in.TeamID)
		if err != nil {
			return err
		}
		lockIDs := append([]string{in.ActorUserID}, memberIDs...)
		if _, err := s.lockUsers(ctx, tx, lockIDs...); err != nil {
			return err
		}
		team, err := s.lockTeam(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		now, err := s.txNow(ctx, tx, in.Now)
		if err != nil {
			return err
		}
		if rec, err := s.checkReceipt(ctx, tx, in.Idempotency, in.ActorUserID, now); err != nil {
			return err
		} else if rec != nil {
			return nil
		}
		freshIDs, err := s.listCurrentMemberIDs(ctx, tx, in.TeamID)
		if err != nil {
			return err
		}
		if !sameIDSet(memberIDs, freshIDs) || team.AuthRevision != in.ExpectedAuthRevision {
			return errVersionConflict()
		}
		if team.Status != domain.TeamStatusActive {
			return errTeamNotFound()
		}
		if team.OwnerUserID != in.ActorUserID {
			return errPermissionDenied()
		}
		if team.Name != in.ConfirmTeamName {
			return errInvalidArgument("team name confirmation does not match")
		}
		rows, err := tx.QueryContext(ctx, membershipSelectSQL+` INNER JOIN user_current_teams c ON c.membership_id = m.membership_id WHERE c.team_id = ? FOR UPDATE`, in.TeamID)
		if err != nil {
			return fmt.Errorf("lock dissolve memberships: %w", err)
		}
		var mems []*domain.TeamMembership
		for rows.Next() {
			mem, err := scanMembership(rows)
			if err != nil {
				rows.Close()
				return err
			}
			mems = append(mems, mem)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, mem := range mems {
			if err := s.closeMembership(ctx, tx, mem, domain.TeamEndReasonDissolved, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_invitations
			SET status = 'revoked', active_recipient_hash = NULL, version = version + 1
			WHERE team_id = ? AND status = 'pending'`, in.TeamID); err != nil {
			return fmt.Errorf("revoke invitations on dissolve: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_invite_links
			SET status = 'revoked', revoked_at = ?, version = version + 1, token_ciphertext = x''
			WHERE team_id = ? AND status = 'active'`, now, in.TeamID); err != nil {
			return fmt.Errorf("revoke links on dissolve: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_export_jobs
			SET status = 'revoked'
			WHERE team_id = ? AND status IN ('queued', 'running', 'completed')`, in.TeamID); err != nil {
			return fmt.Errorf("revoke exports on dissolve: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_analysis_snapshots
			SET status = 'obsolete', active_request_key = NULL
			WHERE team_id = ? AND status IN ('queued', 'building', 'ready')`, in.TeamID); err != nil {
			return fmt.Errorf("obsolete snapshots on dissolve: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE teams SET status = 'dissolved', dissolved_at = ?, auth_revision = auth_revision + 1
			WHERE team_id = ?`, now, in.TeamID); err != nil {
			return fmt.Errorf("dissolve team: %w", err)
		}
		if err := s.insertAudit(ctx, tx, in.TeamID, in.ActorUserID, "dissolve", "team", in.TeamID, map[string]any{
			"result": "dissolved",
		}, now); err != nil {
			return err
		}
		_, err = s.insertReceipt(ctx, tx, in.ActorUserID, in.Idempotency, "team", in.TeamID, now)
		return err
	})
}

func (s *teamsStore) listCurrentMemberIDs(ctx context.Context, q rowQueryer, teamID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id FROM user_current_teams WHERE team_id = ? ORDER BY user_id`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list current members: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func sameIDSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]string(nil), a...)
	right := append([]string(nil), b...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}