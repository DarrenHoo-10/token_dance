-- TokenDance precomputed leaderboard window scores and ranking outbox
-- Target: MySQL 8.0.34+
-- Prerequisite: 0001_tokendance_server.sql, 0002_tokendance_user_system.sql,
--               0003_tokendance_analytics_extensions.sql, 0004_deletion_workflow_fencing.sql

SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;
SET time_zone = '+00:00';
USE tokendance;

ALTER TABLE users
  ADD KEY idx_users_leaderboard_eligible
    (account_status, created_at, user_id);

CREATE TABLE user_window_scores (
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  window_key              VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  generation              VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  token_total             BIGINT UNSIGNED NOT NULL DEFAULT 0,
  revision                BIGINT UNSIGNED NOT NULL DEFAULT 0,
  eligible                BOOLEAN NOT NULL DEFAULT TRUE,
  registered_at           DATETIME(3) NOT NULL,
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                          ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id, window_key, generation),
  KEY idx_user_window_scores_rank
    (window_key, generation, eligible, token_total, registered_at, user_id),
  KEY idx_user_window_scores_generation (generation),
  CONSTRAINT fk_user_window_scores_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_user_window_scores_window
    CHECK (window_key IN ('today', '7d', '30d', 'all'))
) ENGINE = InnoDB;

CREATE TABLE aggregate_dirty_days (
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  metric_date             DATE NOT NULL,
  dirty_version           BIGINT UNSIGNED NOT NULL DEFAULT 1,
  applied_version         BIGINT UNSIGNED NOT NULL DEFAULT 0,
  claim_token             CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  locked_by               VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_expires_at        DATETIME(3) NULL,
  attempt_count           SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at         DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_error_code         VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                          ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id, metric_date),
  KEY idx_aggregate_dirty_days_claim
    (next_attempt_at, lease_expires_at),
  CONSTRAINT fk_aggregate_dirty_days_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_aggregate_dirty_days_versions
    CHECK (applied_version <= dirty_version)
) ENGINE = InnoDB;

CREATE TABLE ranking_outbox (
  task_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  window_key              VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  generation              VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  token_total             BIGINT UNSIGNED NOT NULL DEFAULT 0,
  revision                BIGINT UNSIGNED NOT NULL,
  op_type                 VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  task_status             VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  claim_token             CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  locked_by               VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_expires_at        DATETIME(3) NULL,
  attempt_count           SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at         DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_error_code         VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  applied_at              DATETIME(3) NULL,
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                          ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (task_id),
  UNIQUE KEY uk_ranking_outbox_revision
    (user_id, window_key, generation, revision),
  KEY idx_ranking_outbox_dispatch
    (task_status, next_attempt_at),
  CONSTRAINT fk_ranking_outbox_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_ranking_outbox_window
    CHECK (window_key IN ('today', '7d', '30d', 'all')),
  CONSTRAINT chk_ranking_outbox_op
    CHECK (op_type IN ('upsert', 'remove')),
  CONSTRAINT chk_ranking_outbox_status
    CHECK (task_status IN ('pending', 'leased', 'applied', 'failed')),
  CONSTRAINT chk_ranking_outbox_applied_state
    CHECK (
      (task_status = 'applied' AND applied_at IS NOT NULL)
      OR
      (task_status <> 'applied' AND applied_at IS NULL)
    ),
  CONSTRAINT chk_ranking_outbox_lease_state
    CHECK (
      (task_status = 'leased'
       AND claim_token IS NOT NULL
       AND locked_by IS NOT NULL
       AND lease_expires_at IS NOT NULL)
      OR
      (task_status <> 'leased'
       AND claim_token IS NULL
       AND locked_by IS NULL
       AND lease_expires_at IS NULL)
    )
) ENGINE = InnoDB;
