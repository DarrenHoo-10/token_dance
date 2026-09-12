-- The 0009 statistics-calendar change cleared daily_user_agent_metrics. Days
-- backed by the raw event log rebuild automatically from usage_events, but
-- aggregate-protocol devices have no event rows to trigger a rebuild: re-mark
-- every stored device aggregate day dirty so the worker re-applies them.
INSERT INTO aggregate_dirty_days (user_id, metric_date, dirty_version, applied_version, next_attempt_at)
SELECT DISTINCT d.user_id, d.metric_date, 1, 0, NOW(3)
FROM device_daily_aggregates d
JOIN users u ON u.user_id = d.user_id AND u.account_status = 'active'
ON DUPLICATE KEY UPDATE
  dirty_version = dirty_version + 1,
  next_attempt_at = LEAST(next_attempt_at, VALUES(next_attempt_at)),
  last_error_code = NULL,
  updated_at = NOW(3);
