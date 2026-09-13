package mysql

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
	"time"

	"tokendance/internal/domain"
)

type rowQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type rowScanner interface {
	Scan(dest ...any) error
}

const (
	teamSelectSQL = `SELECT team_id, name, description, timezone_name, owner_user_id, status, profile_version, auth_revision, avatar_object_id, created_at, dissolved_at FROM teams`

	membershipSelectSQL = `SELECT m.membership_id, m.team_id, m.user_id, m.base_role, m.sharing_version, m.joined_at, m.ended_at, m.end_reason FROM team_memberships m`

	invitationColumns = `invitation_id, team_id, inviter_user_id, invited_role, recipient_lookup_hash, lookup_key_version, recipient_ciphertext, encryption_key_version, status, active_recipient_hash, created_at, expires_at, accepted_by_user_id, accepted_membership_id, version`

	invitationSelectSQL = `SELECT ` + invitationColumns + ` FROM team_invitations`

	inviteLinkSelectSQL = `SELECT link_id, team_id, creator_user_id, token_hash, token_ciphertext, encryption_key_version, status, version, max_uses, used_count, created_at, expires_at, revoked_at FROM team_invite_links`

	exportSelectSQL = `SELECT export_id, team_id, requester_user_id, requester_membership_id, snapshot_id, auth_revision, export_kind, filter_json, status, lease_token, lease_generation, lease_expires_at, attempt_count, next_attempt_at, object_key, file_sha256, file_size, created_at, expires_at, error_code FROM team_export_jobs`

	uploadSelectSQL = `SELECT object_id, team_id, uploader_user_id, object_key, content_type, byte_size, image_width, image_height, sha256, status, expires_at FROM team_upload_objects`

	snapshotSelectSQL = `SELECT snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision, rule_version, status, active_request_key, as_of, lease_token, lease_generation, published_generation, lease_expires_at, attempt_count, next_attempt_at, error_code, expires_at FROM team_analysis_snapshots`
)

func scanTeam(row rowScanner) (*domain.Team, error) {
	var t domain.Team
	var avatar sql.NullString
	var dissolved sql.NullTime
	if err := row.Scan(
		&t.TeamID, &t.Name, &t.Description, &t.TimezoneName, &t.OwnerUserID, &t.Status,
		&t.ProfileVersion, &t.AuthRevision, &avatar, &t.CreatedAt, &dissolved,
	); err != nil {
		return nil, err
	}
	t.Visibility = "private"
	t.AvatarObjectID = ptrFromNullString(avatar)
	t.DissolvedAt = ptrFromNullTime(dissolved)
	return &t, nil
}

func scanMembership(row rowScanner) (*domain.TeamMembership, error) {
	var m domain.TeamMembership
	var ended sql.NullTime
	var reason sql.NullString
	if err := row.Scan(&m.MembershipID, &m.TeamID, &m.UserID, &m.BaseRole, &m.SharingVersion, &m.JoinedAt, &ended, &reason); err != nil {
		return nil, err
	}
	m.EndedAt = ptrFromNullTime(ended)
	if reason.Valid {
		r := domain.TeamMembershipEndReason(reason.String)
		m.EndReason = &r
	}
	return &m, nil
}

func scanInvitation(row rowScanner) (*domain.TeamInvitation, error) {
	var inv domain.TeamInvitation
	var lookupHash, activeHash []byte
	var acceptedBy, acceptedMem sql.NullString
	if err := row.Scan(
		&inv.InvitationID, &inv.TeamID, &inv.InviterUserID, &inv.InvitedRole, &lookupHash, &inv.LookupKeyVersion,
		&inv.RecipientCiphertext, &inv.EncryptionKeyVersion, &inv.Status, &activeHash, &inv.CreatedAt, &inv.ExpiresAt,
		&acceptedBy, &acceptedMem, &inv.Version,
	); err != nil {
		return nil, err
	}
	copy(inv.RecipientLookupHash[:], lookupHash)
	if len(activeHash) == 32 {
		var h [32]byte
		copy(h[:], activeHash)
		inv.ActiveRecipientHash = &h
	}
	inv.AcceptedByUserID = ptrFromNullString(acceptedBy)
	inv.AcceptedMembershipID = ptrFromNullString(acceptedMem)
	return &inv, nil
}

func scanInviteLink(row rowScanner) (*domain.TeamInviteLink, error) {
	var link domain.TeamInviteLink
	var tokenHash []byte
	var revoked sql.NullTime
	if err := row.Scan(
		&link.LinkID, &link.TeamID, &link.CreatorUserID, &tokenHash, &link.TokenCiphertext, &link.EncryptionKeyVersion,
		&link.Status, &link.Version, &link.MaxUses, &link.UsedCount, &link.CreatedAt, &link.ExpiresAt, &revoked,
	); err != nil {
		return nil, err
	}
	copy(link.TokenHash[:], tokenHash)
	link.RevokedAt = ptrFromNullTime(revoked)
	return &link, nil
}

func scanGrant(row rowScanner) (domain.TeamSharingGrant, error) {
	var g domain.TeamSharingGrant
	var ends, revoked sql.NullTime
	var active sql.NullString
	if err := row.Scan(&g.GrantID, &g.MembershipID, &g.Dimension, &g.StartsAt, &ends, &revoked, &active); err != nil {
		return g, err
	}
	g.EndsAt = ptrFromNullTime(ends)
	g.RevokedAt = ptrFromNullTime(revoked)
	g.ActiveDimension = ptrFromNullString(active)
	return g, nil
}

func scanExport(row rowScanner) (*domain.TeamExportJob, error) {
	var job domain.TeamExportJob
	var leaseToken, objectKey, errCode sql.NullString
	var leaseExp sql.NullTime
	var fileSHA []byte
	var fileSize sql.NullInt64
	var filter []byte
	if err := row.Scan(
		&job.ExportID, &job.TeamID, &job.RequesterUserID, &job.RequesterMembershipID, &job.SnapshotID, &job.AuthRevision,
		&job.Kind, &filter, &job.Status, &leaseToken, &job.LeaseGeneration, &leaseExp, &job.AttemptCount, &job.NextAttemptAt,
		&objectKey, &fileSHA, &fileSize, &job.CreatedAt, &job.ExpiresAt, &errCode,
	); err != nil {
		return nil, err
	}
	job.FilterJSON = string(filter)
	if len(filter) > 0 {
		sum := sha256.Sum256(filter)
		job.FiltersHash = hex.EncodeToString(sum[:])
	}
	job.LeaseToken = ptrFromNullString(leaseToken)
	job.LeaseExpiresAt = ptrFromNullTime(leaseExp)
	job.ObjectKey = ptrFromNullString(objectKey)
	job.ErrorCode = ptrFromNullString(errCode)
	if len(fileSHA) == 32 {
		var h [32]byte
		copy(h[:], fileSHA)
		job.FileSHA256 = &h
	}
	if fileSize.Valid {
		v := uint64(fileSize.Int64)
		job.FileSize = &v
	}
	return &job, nil
}

func scanUploadObject(row rowScanner) (*domain.TeamUploadObject, error) {
	var obj domain.TeamUploadObject
	var sha []byte
	var expires sql.NullTime
	if err := row.Scan(
		&obj.ObjectID, &obj.TeamID, &obj.UploaderID, &obj.ObjectKey, &obj.ContentType, &obj.ByteSize,
		&obj.ImageWidth, &obj.ImageHeight, &sha, &obj.Status, &expires,
	); err != nil {
		return nil, err
	}
	copy(obj.SHA256[:], sha)
	obj.ExpiresAt = ptrFromNullTime(expires)
	return &obj, nil
}

func scanSnapshot(row rowScanner) (*domain.TeamAnalysisSnapshot, error) {
	var snap domain.TeamAnalysisSnapshot
	var activeKey, leaseToken, errCode sql.NullString
	var leaseExp sql.NullTime
	if err := row.Scan(
		&snap.SnapshotID, &snap.TeamID, &snap.FromDate, &snap.ToDateExclusive, &snap.AuthRevision, &snap.SourceRevision,
		&snap.RuleVersion, &snap.Status, &activeKey, &snap.AsOf, &leaseToken, &snap.LeaseGeneration, &snap.PublishedGeneration,
		&leaseExp, &snap.AttemptCount, &snap.NextAttemptAt, &errCode, &snap.ExpiresAt,
	); err != nil {
		return nil, err
	}
	snap.ActiveRequestKey = ptrFromNullString(activeKey)
	snap.LeaseToken = ptrFromNullString(leaseToken)
	snap.LeaseExpiresAt = ptrFromNullTime(leaseExp)
	snap.ErrorCode = ptrFromNullString(errCode)
	return &snap, nil
}

func analysisRequestKey(teamID string, from, to time.Time, authRevision uint64, ruleVersion string, timezone string) string {
	return teamID + "|" + domain.FormatTeamCalendarDate(from, timezone) + "|" + domain.FormatTeamCalendarDate(to, timezone) + "|" +
		strconv.FormatUint(authRevision, 10) + "|" + ruleVersion
}
