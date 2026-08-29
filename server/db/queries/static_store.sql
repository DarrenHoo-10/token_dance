-- Static store queries. Optional-filter query builders remain in reviewed Go call sites.

-- name: GetPendingEmailChallenge :one
SELECT challenge_id, user_id, email_lookup_hash, email_ciphertext, email_key_version,
       challenge_type, code_hash, code_key_version, challenge_status, attempt_count,
       max_attempts, send_count, requested_ip_prefix_hash, expires_at, consumed_at,
       created_at, updated_at
FROM email_challenges
WHERE challenge_type = ? AND email_lookup_hash = ? AND challenge_status = 'pending'
LIMIT 1;

-- name: UpdateEmailChallengeAttempt :execrows
UPDATE email_challenges
SET attempt_count = ?, challenge_status = ?, updated_at = ?
WHERE challenge_id = ?;

-- name: ListSessionsByUser :many
SELECT session_id, user_id, session_token_hash, csrf_token_hash, credential_version,
       session_status, device_label, user_agent_hash, ip_prefix_hash, last_seen_at,
       idle_expires_at, absolute_expires_at, revoked_at, revoke_reason, created_at, updated_at
FROM user_sessions
WHERE user_id = ?
ORDER BY created_at DESC;

-- name: GetPrivacyByUser :one
SELECT p.user_id, p.public_profile_enabled, u.leaderboard_visibility,
       p.show_bio, p.show_token_total, p.show_trends, p.show_activity_calendar,
       p.show_agent_breakdown, p.show_skill_ranking, p.show_achievements,
       p.privacy_version, p.created_at, p.updated_at
FROM user_privacy_settings p
JOIN users u ON u.user_id = p.user_id
WHERE p.user_id = ?
LIMIT 1;

-- name: LockPrivacyVersion :one
SELECT privacy_version
FROM user_privacy_settings
WHERE user_id = ?
FOR UPDATE;

-- name: GetPublishedProfileByHandle :one
SELECT p.user_id, p.handle, p.display_name, p.avatar_url, p.bio, p.profile_status,
       p.show_bio, p.show_token_total, p.show_trends, p.show_activity_calendar,
       p.show_agent_breakdown, p.show_skill_ranking, p.show_achievements,
       p.source_profile_version, p.source_privacy_version, p.projection_version,
       p.published_at, p.created_at, p.updated_at
FROM public_user_profiles p
JOIN users u ON p.user_id = u.user_id
WHERE p.handle = ? AND p.profile_status = 'published' AND u.account_status = 'active'
LIMIT 1;

-- name: ListInstallationsByUser :many
SELECT installation_id, user_id, device_public_key, device_name, os_type, os_version,
       architecture, collector_version, installation_status, disabled_at, disabled_reason,
       status_version, registered_at, last_seen_at, revoked_at, updated_at
FROM installations
WHERE user_id = ?
ORDER BY registered_at DESC;

-- name: GetInstallationByOwner :one
SELECT installation_id, user_id, device_public_key, device_name, os_type, os_version,
       architecture, collector_version, installation_status, disabled_at, disabled_reason,
       status_version, registered_at, last_seen_at, revoked_at, updated_at
FROM installations
WHERE installation_id = ? AND user_id = ?
LIMIT 1;

-- name: CancelBindingChallengeByOwner :execrows
UPDATE device_binding_challenges
SET challenge_status = 'cancelled', active_session_key = NULL, updated_at = ?
WHERE challenge_id = ? AND user_id = ?;

-- name: CreateUploadObject :exec
INSERT INTO user_upload_objects (
    object_id, user_id, object_type, object_key, content_type, byte_size,
    content_sha256, upload_status, expires_at, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetUploadObjectByOwner :one
SELECT object_id, user_id, object_type, object_key, content_type, byte_size,
       content_sha256, image_width, image_height, upload_status, expires_at,
       last_error_code, uploaded_at, ready_at, deleted_at, created_at, updated_at
FROM user_upload_objects
WHERE object_id = ? AND user_id = ?
LIMIT 1;

-- name: UpdateUploadObjectStatus :execrows
UPDATE user_upload_objects
SET upload_status = ?, last_error_code = ?, updated_at = ?
WHERE object_id = ?;

-- name: GetExportJobByOwner :one
SELECT export_id, user_id, idempotency_key, request_hash, export_scope, export_format,
       filter_json, job_status, attempt_count, next_attempt_at, locked_at, locked_by,
       object_key, file_sha256, file_size, last_error_code, started_at, completed_at,
       expires_at, created_at, updated_at
FROM data_export_jobs
WHERE export_id = ? AND user_id = ?
LIMIT 1;

-- name: ListExportJobsByUser :many
SELECT export_id, user_id, idempotency_key, request_hash, export_scope, export_format,
       filter_json, job_status, attempt_count, next_attempt_at, locked_at, locked_by,
       object_key, file_sha256, file_size, last_error_code, started_at, completed_at,
       expires_at, created_at, updated_at
FROM data_export_jobs
WHERE user_id = ?
ORDER BY created_at DESC;

-- name: CompleteExportJob :execrows
UPDATE data_export_jobs
SET job_status = 'completed', object_key = ?, file_sha256 = ?, file_size = ?,
    completed_at = ?, expires_at = ?, locked_at = NULL, locked_by = NULL, updated_at = ?
WHERE export_id = ? AND (locked_by = ? OR locked_by IS NULL);

-- name: GetLatestPublishedSnapshot :one
SELECT snapshot_id, data_watermark_at
FROM leaderboard_snapshots
WHERE board_key = ? AND metric_key = ? AND snapshot_status = 'published'
ORDER BY published_at DESC
LIMIT 1;

-- name: ListVisibleLeaderboardEntries :many
SELECT e.rank_no, p.handle, p.display_name, p.avatar_url, e.metric_value, e.previous_rank_no
FROM leaderboard_entries e
JOIN users u ON e.user_id = u.user_id
JOIN public_user_profiles p ON e.user_id = p.user_id
JOIN user_privacy_settings priv ON e.user_id = priv.user_id
WHERE e.snapshot_id = ? AND u.account_status = 'active'
  AND u.leaderboard_visibility = 'public' AND p.profile_status = 'published'
  AND priv.public_profile_enabled = TRUE
ORDER BY e.rank_no ASC
LIMIT ?;

-- name: DeleteSnapshotEntries :exec
DELETE FROM leaderboard_entries
WHERE snapshot_id = ?;

-- name: GetIngestInstallationByID :one
SELECT installation_id, user_id, device_public_key, device_name, os_type, os_version,
       architecture, collector_version, installation_status, disabled_at, disabled_reason,
       status_version, registered_at, last_seen_at, revoked_at, updated_at
FROM installations
WHERE installation_id = ?;

-- name: LockIngestBatch :one
SELECT installation_id, request_sha256, accepted_count, duplicate_count, rejected_count, committed_at
FROM ingest_batches
WHERE batch_id = ?
FOR UPDATE;

-- name: UpdateInstallationLastSeen :exec
UPDATE installations
SET last_seen_at = ?, updated_at = ?
WHERE installation_id = ?;

-- name: GetDeletionRequestByOwner :one
SELECT request_id, user_id, deletion_scope, scope_filter_json, request_status, phase,
       progress_cursor, cancel_before, cancelled_at, requested_at, completed_at, audit_reference
FROM data_deletion_requests
WHERE request_id = ? AND user_id = ?
LIMIT 1;

-- name: LockDeletionRequestForCancel :one
SELECT request_status, deletion_scope,
       cancel_before IS NOT NULL AND cancel_before > CURRENT_TIMESTAMP(3) AS cancel_window_open
FROM data_deletion_requests
WHERE request_id = ? AND user_id = ?
FOR UPDATE;

-- name: HidePublicProfileForDeletion :exec
UPDATE public_user_profiles
SET profile_status = 'hidden', projection_version = projection_version + 1, updated_at = ?
WHERE user_id = ?;
