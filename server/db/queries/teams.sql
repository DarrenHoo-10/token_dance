-- name: LockUserForTeam :one
SELECT user_id, account_status, onboarding_completed_at, email_verified_at, email_lookup_hash, display_name
FROM users
WHERE user_id = ?
FOR UPDATE;

-- name: LockTeam :one
SELECT team_id, name, description, timezone_name, owner_user_id, status, profile_version, auth_revision,
       avatar_object_id, created_at, dissolved_at
FROM teams
WHERE team_id = ?
FOR UPDATE;

-- name: LockUserCurrentTeam :one
SELECT user_id, team_id, membership_id, joined_at
FROM user_current_teams
WHERE user_id = ?
FOR UPDATE;

-- name: InsertTeam :exec
INSERT INTO teams (
  team_id, name, description, timezone_name, owner_user_id, status,
  profile_version, auth_revision, avatar_object_id, created_at, dissolved_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertTeamMembership :exec
INSERT INTO team_memberships (
  membership_id, team_id, user_id, base_role, sharing_version, joined_at, ended_at, end_reason
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertUserCurrentTeam :exec
INSERT INTO user_current_teams (user_id, team_id, membership_id, joined_at)
VALUES (?, ?, ?, ?);

-- name: InsertTeamSourceRevision :exec
INSERT INTO team_source_revisions (team_id, source_revision, changed_at)
VALUES (?, ?, ?);

-- name: InsertTeamSharingGrant :exec
INSERT INTO team_sharing_grants (
  grant_id, membership_id, dimension, starts_at, ends_at, revoked_at, active_dimension
) VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: RevokeActiveGrant :exec
UPDATE team_sharing_grants
SET ends_at = ?, revoked_at = ?, active_dimension = NULL
WHERE membership_id = ? AND active_dimension = ?;

-- name: GetTeamCommandReceipt :one
SELECT actor_user_id, operation_scope, idempotency_key_hash, request_hash, result_type, result_id, created_at, expires_at
FROM team_command_receipts
WHERE actor_user_id = ? AND operation_scope = ? AND idempotency_key_hash = ?
FOR UPDATE;

-- name: InsertTeamCommandReceipt :exec
INSERT INTO team_command_receipts (
  actor_user_id, operation_scope, idempotency_key_hash, request_hash, result_type, result_id, created_at, expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: InsertTeamAuditEvent :exec
INSERT INTO team_audit_events (
  audit_id, team_id, actor_user_id, action, target_type, target_id, safe_details_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetInviteLinkByTokenHash :one
SELECT link_id, team_id, creator_user_id, token_hash, token_ciphertext, encryption_key_version,
       status, version, max_uses, used_count, created_at, expires_at, revoked_at
FROM team_invite_links
WHERE token_hash = ?;

-- name: IncrementInviteLinkUse :execrows
UPDATE team_invite_links
SET used_count = used_count + 1
WHERE link_id = ?
  AND status = 'active'
  AND expires_at > ?
  AND used_count < max_uses;

-- name: InsertInviteLinkJoin :exec
INSERT INTO team_invite_link_joins (link_id, user_id, membership_id, joined_at)
VALUES (?, ?, ?, ?);

-- name: BumpTeamAuthRevision :exec
UPDATE teams
SET auth_revision = auth_revision + 1
WHERE team_id = ?;

-- name: GetOrQueueAnalysisSnapshot :one
SELECT snapshot_id, team_id, from_date, to_date_exclusive, auth_revision, source_revision, rule_version,
       status, active_request_key, as_of, lease_token, lease_generation, published_generation,
       lease_expires_at, attempt_count, next_attempt_at, error_code, expires_at
FROM team_analysis_snapshots
WHERE team_id = ?
  AND from_date = ?
  AND to_date_exclusive = ?
  AND auth_revision = ?
  AND rule_version = ?
  AND status = 'ready'
ORDER BY as_of DESC
LIMIT 1;
