// Package rollout implements P8 empty-DB enablement: table inventories,
// idempotent stats reset, feature-flag helpers, and acceptance probes.
package rollout

// StatsClearTables lists statistics / telemetry tables truncated during the
// closed-beta empty-DB cutover, ordered for foreign-key safety (children first).
// Auth, device, nonce, and desktop release tables are intentionally absent.
var StatsClearTables = []string{
	// Outbox / derived publish first (may reference scores / community).
	"ranking_outbox",
	"community_stats_outbox",
	// Leaderboard / community / window scores.
	"leaderboard_entries",
	"leaderboard_snapshots",
	"community_agent_daily_stats",
	"community_daily_stats",
	"user_window_scores",
	"device_daily_aggregates",
	// Legacy daily snapshots / metrics.
	"daily_skill_metrics",
	"daily_user_agent_model_metrics",
	"daily_user_agent_metrics",
	// Dirty refresh (rebuild empty under 0011 shape).
	"aggregate_dirty_days",
	// Legacy raw ingest.
	"usage_events",
	"ingest_batches",
	// Telemetry v2 task + metrics + facts (children before parents).
	"telemetry_tasks",
	"telemetry_harness_metrics",
	"telemetry_model_metrics",
	"telemetry_skill_metrics",
	"telemetry_cost_metrics",
	"telemetry_bucket_entities",
	"telemetry_events",
	"telemetry_skills",
	"telemetry_models",
}

// PreservedTables must never be truncated by the reset tool. Documented for
// acceptance checks; the reset path refuses to touch them.
var PreservedTables = []string{
	"users",
	"user_sessions",
	"user_password_credentials",
	"user_privacy_settings",
	"public_user_profiles",
	"installations",
	"installation_adapter_status",
	"ingest_nonces",
	"desktop_releases",
	"desktop_release_channels",
	"desktop_release_publication",
	"schema_migrations",
	"event_pipeline_rollout_state",
	"email_challenges",
	"email_outbox",
	"device_binding_challenges",
	"data_deletion_requests",
	"data_export_jobs",
}

// RedisStatsKeyPrefixes cleared after MySQL reset (ranking / community caches).
// Auth/session keys are not matched by these prefixes.
var RedisStatsKeyPrefixes = []string{
	"ranking:",
	"lb:",
	"community:",
	"window_score:",
	"tokendance:ranking:",
	"tokendance:community:",
}
