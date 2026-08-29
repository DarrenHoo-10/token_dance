-- name: GetUserByID :one
SELECT user_id, auth_subject_hash, email_lookup_hash, email_ciphertext,
       handle, email_verified_at, display_name, avatar_url, avatar_object_id,
       bio, account_status, leaderboard_visibility, timezone_name, locale,
       onboarding_completed_at, profile_version, public_profile_updated_at,
       created_at, updated_at, deleted_at
FROM users
WHERE user_id = ?
LIMIT 1;
