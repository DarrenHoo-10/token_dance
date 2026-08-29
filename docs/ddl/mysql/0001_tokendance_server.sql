-- TokenDance central server schema
-- Target: MySQL 8.0.34+
-- All timestamps are UTC. The application must execute SET time_zone = '+00:00'
-- on every pooled connection.

SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;
SET time_zone = '+00:00';

CREATE DATABASE IF NOT EXISTS tokendance
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;

USE tokendance;

CREATE TABLE schema_migrations (
  version             VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  checksum_sha256     BINARY(32) NOT NULL,
  applied_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (version)
) ENGINE = InnoDB;

CREATE TABLE users (
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  auth_subject_hash       BINARY(32) NOT NULL,
  email_lookup_hash       BINARY(32) NULL,
  email_ciphertext        VARBINARY(1024) NULL,
  display_name            VARCHAR(80) NOT NULL,
  avatar_url              VARCHAR(1024) NULL,
  account_status          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  leaderboard_visibility  VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'private',
  timezone_name           VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'UTC',
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  deleted_at              DATETIME(3) NULL,
  PRIMARY KEY (user_id),
  UNIQUE KEY uk_users_auth_subject_hash (auth_subject_hash),
  UNIQUE KEY uk_users_email_lookup_hash (email_lookup_hash),
  KEY idx_users_visibility_status (leaderboard_visibility, account_status),
  CONSTRAINT chk_users_account_status
    CHECK (account_status IN ('active', 'suspended', 'deleted')),
  CONSTRAINT chk_users_visibility
    CHECK (leaderboard_visibility IN ('private', 'team', 'public'))
) ENGINE = InnoDB;

CREATE TABLE installations (
  installation_id       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id               CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  device_public_key     BINARY(32) NOT NULL,
  device_name           VARCHAR(120) NULL,
  os_type               VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  os_version            VARCHAR(64) NULL,
  architecture          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  collector_version     VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  installation_status   VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  registered_at         DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_seen_at          DATETIME(3) NULL,
  revoked_at            DATETIME(3) NULL,
  updated_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (installation_id),
  UNIQUE KEY uk_installations_device_public_key (device_public_key),
  KEY idx_installations_user_status (user_id, installation_status),
  KEY idx_installations_last_seen (last_seen_at),
  CONSTRAINT fk_installations_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_installations_os
    CHECK (os_type IN ('windows', 'macos')),
  CONSTRAINT chk_installations_status
    CHECK (installation_status IN ('active', 'revoked', 'disabled'))
) ENGINE = InnoDB;

CREATE TABLE installation_adapter_status (
  installation_id       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  adapter_id            VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  adapter_version       VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_version         VARCHAR(64) NULL,
  runtime_status        VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  capability_json       JSON NOT NULL,
  safe_error_code       VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  last_probe_at         DATETIME(3) NULL,
  last_event_at         DATETIME(3) NULL,
  updated_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (installation_id, adapter_id),
  KEY idx_adapter_status_state (adapter_id, runtime_status),
  CONSTRAINT fk_adapter_status_installation
    FOREIGN KEY (installation_id) REFERENCES installations (installation_id) ON DELETE CASCADE,
  CONSTRAINT chk_adapter_runtime_status
    CHECK (runtime_status IN ('active', 'degraded', 'disabled', 'unavailable', 'error'))
) ENGINE = InnoDB;

-- Redis is the preferred hot anti-replay store. This table is the durable fallback
-- and must be pruned continuously by expires_at.
CREATE TABLE ingest_nonces (
  installation_id       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  nonce_hash            BINARY(32) NOT NULL,
  expires_at            DATETIME(3) NOT NULL,
  created_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (installation_id, nonce_hash),
  KEY idx_ingest_nonces_expiry (expires_at),
  CONSTRAINT fk_ingest_nonces_installation
    FOREIGN KEY (installation_id) REFERENCES installations (installation_id) ON DELETE CASCADE
) ENGINE = InnoDB;

CREATE TABLE ingest_batches (
  batch_id              CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  installation_id       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_sha256        BINARY(32) NOT NULL,
  event_count           INT UNSIGNED NOT NULL,
  accepted_count        INT UNSIGNED NOT NULL DEFAULT 0,
  duplicate_count       INT UNSIGNED NOT NULL DEFAULT 0,
  rejected_count        INT UNSIGNED NOT NULL DEFAULT 0,
  batch_status          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'received',
  last_error_code       VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  received_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  committed_at          DATETIME(3) NULL,
  updated_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (batch_id),
  UNIQUE KEY uk_ingest_batches_installation_batch (installation_id, batch_id),
  KEY idx_ingest_batches_installation_received (installation_id, received_at),
  KEY idx_ingest_batches_status_received (batch_status, received_at),
  CONSTRAINT fk_ingest_batches_installation
    FOREIGN KEY (installation_id) REFERENCES installations (installation_id),
  CONSTRAINT chk_ingest_batches_count
    CHECK (event_count <= 1000 AND accepted_count + duplicate_count + rejected_count <= event_count),
  CONSTRAINT chk_ingest_batches_status
    CHECK (batch_status IN ('received', 'committed', 'partial', 'rejected'))
) ENGINE = InnoDB;

CREATE TABLE usage_events (
  event_pk                 BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  event_id                 BINARY(32) NOT NULL,
  schema_version           SMALLINT UNSIGNED NOT NULL,
  batch_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  installation_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  adapter_id               VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  adapter_version          VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_id                 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_version            VARCHAR(64) NULL,
  provider_id              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  model_id                 VARCHAR(160) CHARACTER SET ascii COLLATE ascii_bin NULL,
  event_type               VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  accuracy                 VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  source_kind              VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  occurred_at              DATETIME(3) NOT NULL,
  occurred_date            DATE GENERATED ALWAYS AS (DATE(occurred_at)) STORED,
  received_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  session_hash             BINARY(32) NULL,
  parent_session_hash      BINARY(32) NULL,
  turn_hash                BINARY(32) NULL,
  tool_call_hash           BINARY(32) NULL,
  token_input              BIGINT UNSIGNED NULL,
  token_output             BIGINT UNSIGNED NULL,
  token_cache_read         BIGINT UNSIGNED NULL,
  token_cache_write        BIGINT UNSIGNED NULL,
  token_reasoning          BIGINT UNSIGNED NULL,
  token_total              BIGINT UNSIGNED NULL,
  duration_ms              BIGINT UNSIGNED NULL,
  success                  BOOLEAN NULL,
  tool_category            VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  skill_key                BINARY(32) NULL,
  skill_public_name        VARCHAR(120) NULL,
  skill_invoke_type        VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NULL,
  plugin_key               BINARY(32) NULL,
  code_generated_lines     BIGINT UNSIGNED NULL,
  code_accepted_lines      BIGINT UNSIGNED NULL,
  code_added_lines         BIGINT UNSIGNED NULL,
  code_deleted_lines       BIGINT UNSIGNED NULL,
  code_file_count          INT UNSIGNED NULL,
  cost_amount              DECIMAL(20, 8) NULL,
  cost_currency            CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NULL,
  cost_source              VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
  privacy_policy_version   SMALLINT UNSIGNED NOT NULL,
  safe_extension_json      JSON NULL,
  created_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (event_pk),
  UNIQUE KEY uk_usage_events_installation_event (installation_id, event_id),
  KEY idx_usage_events_user_time (user_id, occurred_at, event_pk),
  KEY idx_usage_events_agent_time (agent_id, occurred_at, event_pk),
  KEY idx_usage_events_date_user_agent (occurred_date, user_id, agent_id),
  KEY idx_usage_events_session_time (session_hash, occurred_at),
  KEY idx_usage_events_skill_time (skill_key, occurred_at),
  KEY idx_usage_events_type_time (event_type, occurred_at),
  KEY idx_usage_events_batch (batch_id),
  CONSTRAINT fk_usage_events_batch
    FOREIGN KEY (batch_id) REFERENCES ingest_batches (batch_id),
  CONSTRAINT fk_usage_events_installation
    FOREIGN KEY (installation_id) REFERENCES installations (installation_id),
  CONSTRAINT fk_usage_events_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_usage_events_type
    CHECK (event_type IN ('session_started', 'session_ended', 'turn_started', 'turn_completed',
                          'model_usage_recorded', 'tool_invoked', 'skill_invoked', 'code_changed',
                          'cost_recorded', 'agent_spawned')),
  CONSTRAINT chk_usage_events_accuracy
    CHECK (accuracy IN ('exact', 'derived', 'estimated', 'correlated')),
  CONSTRAINT chk_usage_events_source_kind
    CHECK (source_kind IN ('otlp', 'jsonl', 'sqlite_snapshot', 'file_snapshot',
                           'runtime_stream', 'local_http_api', 'command_snapshot', 'remote_api')),
  CONSTRAINT chk_usage_events_nonnegative_cost
    CHECK (cost_amount IS NULL OR cost_amount >= 0),
  CONSTRAINT chk_usage_events_cost_source
    CHECK (cost_source IS NULL OR cost_source IN ('provider_reported', 'estimated_price_table'))
) ENGINE = InnoDB;

CREATE TABLE daily_user_agent_metrics (
  metric_date               DATE NOT NULL,
  user_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_id                  VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  exact_token_total         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  derived_token_total       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  estimated_token_total     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  session_count             BIGINT UNSIGNED NOT NULL DEFAULT 0,
  child_session_count       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  interaction_turn_count    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  model_request_count       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  tool_call_count           BIGINT UNSIGNED NOT NULL DEFAULT 0,
  skill_use_count           BIGINT UNSIGNED NOT NULL DEFAULT 0,
  code_generated_lines      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  code_accepted_lines       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  correlated_code_lines     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cost_amount               DECIMAL(20, 8) NOT NULL DEFAULT 0,
  cost_currency             CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'USD',
  source_max_event_pk       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  aggregation_version       INT UNSIGNED NOT NULL,
  computed_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date, user_id, agent_id),
  KEY idx_daily_agent_date_tokens (agent_id, metric_date, exact_token_total, derived_token_total),
  KEY idx_daily_user_date (user_id, metric_date),
  CONSTRAINT fk_daily_metrics_user
    FOREIGN KEY (user_id) REFERENCES users (user_id)
) ENGINE = InnoDB;

CREATE TABLE daily_user_agent_model_metrics (
  metric_date               DATE NOT NULL,
  user_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_id                  VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  provider_id               VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  model_id                  VARCHAR(160) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  exact_token_total         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  derived_token_total       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  estimated_token_total     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  model_request_count       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cost_amount               DECIMAL(20, 8) NOT NULL DEFAULT 0,
  cost_currency             CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'USD',
  source_max_event_pk       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  aggregation_version       INT UNSIGNED NOT NULL,
  computed_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date, user_id, agent_id, provider_id, model_id),
  KEY idx_daily_model_date_tokens (provider_id, model_id, metric_date, exact_token_total, derived_token_total),
  KEY idx_daily_model_user_date (user_id, metric_date),
  CONSTRAINT fk_daily_model_metrics_user
    FOREIGN KEY (user_id) REFERENCES users (user_id)
) ENGINE = InnoDB;

CREATE TABLE daily_skill_metrics (
  metric_date             DATE NOT NULL,
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_id                VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  skill_key               BINARY(32) NOT NULL,
  skill_public_name       VARCHAR(120) NULL,
  use_count               BIGINT UNSIGNED NOT NULL DEFAULT 0,
  exact_use_count         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  derived_use_count       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  correlated_use_count    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  estimated_use_count     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  success_count           BIGINT UNSIGNED NOT NULL DEFAULT 0,
  failure_count           BIGINT UNSIGNED NOT NULL DEFAULT 0,
  duration_ms             BIGINT UNSIGNED NOT NULL DEFAULT 0,
  source_max_event_pk     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  aggregation_version     INT UNSIGNED NOT NULL,
  computed_at             DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date, user_id, agent_id, skill_key),
  KEY idx_daily_skill_date_count (skill_key, metric_date, use_count),
  KEY idx_daily_skill_user_date (user_id, metric_date),
  CONSTRAINT fk_daily_skill_metrics_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_daily_skill_counts
    CHECK (success_count + failure_count <= use_count AND
           exact_use_count + derived_use_count + correlated_use_count + estimated_use_count <= use_count)
) ENGINE = InnoDB;

CREATE TABLE leaderboard_snapshots (
  snapshot_id             CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  board_key               VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  scope_type              VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  scope_key               VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  metric_key              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  window_start            DATETIME(3) NOT NULL,
  window_end              DATETIME(3) NOT NULL,
  timezone_name           VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  ranking_rule_version    INT UNSIGNED NOT NULL,
  participant_count       INT UNSIGNED NOT NULL DEFAULT 0,
  source_max_event_pk     BIGINT UNSIGNED NOT NULL,
  data_watermark_at       DATETIME(3) NOT NULL,
  snapshot_status         VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'building',
  generated_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  published_at            DATETIME(3) NULL,
  PRIMARY KEY (snapshot_id),
  UNIQUE KEY uk_leaderboard_snapshot_version
    (board_key, scope_type, scope_key, metric_key, window_start, window_end, ranking_rule_version),
  KEY idx_leaderboard_snapshots_lookup
    (board_key, scope_type, scope_key, metric_key, snapshot_status, window_end),
  CONSTRAINT chk_leaderboard_scope
    CHECK (scope_type IN ('global', 'team', 'private')),
  CONSTRAINT chk_leaderboard_snapshot_status
    CHECK (snapshot_status IN ('building', 'published', 'superseded', 'failed')),
  CONSTRAINT chk_leaderboard_window
    CHECK (window_end > window_start)
) ENGINE = InnoDB;

CREATE TABLE leaderboard_entries (
  snapshot_id             CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  rank_no                 INT UNSIGNED NOT NULL,
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  metric_value            DECIMAL(30, 6) NOT NULL,
  previous_rank_no        INT UNSIGNED NULL,
  display_name_snapshot   VARCHAR(80) NOT NULL,
  avatar_url_snapshot     VARCHAR(1024) NULL,
  breakdown_json          JSON NULL,
  PRIMARY KEY (snapshot_id, rank_no),
  UNIQUE KEY uk_leaderboard_entries_snapshot_user (snapshot_id, user_id),
  KEY idx_leaderboard_entries_user_snapshot (user_id, snapshot_id),
  CONSTRAINT fk_leaderboard_entries_snapshot
    FOREIGN KEY (snapshot_id) REFERENCES leaderboard_snapshots (snapshot_id) ON DELETE CASCADE,
  CONSTRAINT fk_leaderboard_entries_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_leaderboard_rank_positive
    CHECK (rank_no > 0)
) ENGINE = InnoDB;

CREATE TABLE adapter_releases (
  adapter_id              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  adapter_version         VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  os_type                 VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  architecture            VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  artifact_url            VARCHAR(2048) NOT NULL,
  artifact_sha256         BINARY(32) NOT NULL,
  signature_ed25519       VARBINARY(128) NOT NULL,
  manifest_json           JSON NOT NULL,
  compatibility_json      JSON NOT NULL,
  release_status          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'draft',
  rollout_percent         TINYINT UNSIGNED NOT NULL DEFAULT 0,
  published_at            DATETIME(3) NULL,
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (adapter_id, adapter_version, os_type, architecture),
  KEY idx_adapter_releases_rollout (adapter_id, release_status, rollout_percent),
  CONSTRAINT chk_adapter_releases_os
    CHECK (os_type IN ('windows', 'macos')),
  CONSTRAINT chk_adapter_releases_status
    CHECK (release_status IN ('draft', 'canary', 'stable', 'revoked')),
  CONSTRAINT chk_adapter_releases_rollout
    CHECK (rollout_percent <= 100)
) ENGINE = InnoDB;

CREATE TABLE data_deletion_requests (
  request_id              CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  installation_id         CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  deletion_scope          VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_status          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  requested_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  started_at              DATETIME(3) NULL,
  completed_at            DATETIME(3) NULL,
  error_code              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  audit_reference         CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  PRIMARY KEY (request_id),
  KEY idx_deletion_requests_status_time (request_status, requested_at),
  KEY idx_deletion_requests_user_time (user_id, requested_at),
  CONSTRAINT fk_deletion_requests_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE SET NULL,
  CONSTRAINT fk_deletion_requests_installation
    FOREIGN KEY (installation_id) REFERENCES installations (installation_id) ON DELETE SET NULL,
  CONSTRAINT chk_deletion_scope
    CHECK (deletion_scope IN ('installation', 'time_range', 'all_usage', 'account')),
  CONSTRAINT chk_deletion_status
    CHECK (request_status IN ('pending', 'running', 'completed', 'failed'))
) ENGINE = InnoDB;
