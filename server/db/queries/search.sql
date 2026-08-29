-- name: SearchPublicUsers :many
SELECT p.handle, p.display_name, p.avatar_url, p.bio
FROM public_user_profiles p
JOIN users u ON p.user_id = u.user_id
JOIN user_privacy_settings priv ON p.user_id = priv.user_id
WHERE p.profile_status = 'published'
  AND u.account_status = 'active'
  AND u.leaderboard_visibility = 'public'
  AND priv.public_profile_enabled = TRUE
  AND (LOWER(p.handle) LIKE sqlc.arg(search_pattern) OR LOWER(p.display_name) LIKE sqlc.arg(search_pattern))
ORDER BY (LOWER(p.handle) = LOWER(sqlc.arg(exact_handle))) DESC, p.handle ASC
LIMIT ?;

-- name: SearchPublicSkills :many
SELECT
    HEX(s.skill_key) AS skill_hex,
    s.skill_public_name,
    SUM(s.use_count) AS total_use_count,
    COUNT(DISTINCT s.user_id) AS public_user_count,
    COUNT(DISTINCT s.metric_date) AS active_days
FROM daily_skill_metrics s
JOIN users u ON s.user_id = u.user_id
JOIN public_user_profiles p ON s.user_id = p.user_id
JOIN user_privacy_settings priv ON s.user_id = priv.user_id
WHERE u.account_status = 'active'
  AND u.leaderboard_visibility = 'public'
  AND p.profile_status = 'published'
  AND priv.public_profile_enabled = TRUE
  AND priv.show_skill_ranking = TRUE
  AND s.skill_public_name IS NOT NULL
  AND s.skill_public_name != ''
  AND (LOWER(s.skill_public_name) LIKE sqlc.arg(name_pattern) OR LOWER(HEX(s.skill_key)) LIKE sqlc.arg(key_pattern))
GROUP BY s.skill_key, s.skill_public_name
HAVING public_user_count >= 5 AND active_days >= 3
ORDER BY total_use_count DESC
LIMIT ?;
