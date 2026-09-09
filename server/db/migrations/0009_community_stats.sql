-- Community totals are precomputed, never aggregated on the request path.
-- community_stats_outbox carries "this metric date changed" signals written in
-- the same transaction as the daily metric rebuild; the worker recomputes the
-- whole day from daily_user_agent_metrics and overwrites both stores.

-- Statistics days follow the product calendar (UTC+8). occurred_at stays a
-- universal UTC timestamp; only the derived day key shifts. Daily tables are
-- cleared so the worker rebuilds them from usage_events under the new keys.
ALTER TABLE usage_events
  MODIFY COLUMN occurred_date DATE GENERATED ALWAYS AS (DATE(occurred_at + INTERVAL 8 HOUR)) STORED;

DELETE FROM daily_user_agent_metrics;
DELETE FROM daily_user_agent_model_metrics;
DELETE FROM daily_skill_metrics;
DELETE FROM user_window_scores;
DELETE FROM ranking_outbox;
DELETE FROM leaderboard_entries;
DELETE FROM leaderboard_snapshots;
DELETE FROM community_daily_stats;
DELETE FROM community_agent_daily_stats;
DELETE FROM community_stats_outbox;

CREATE TABLE community_daily_stats (
  metric_date     DATE NOT NULL,
  tokens_total    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  developers      INT UNSIGNED NOT NULL DEFAULT 0,
  code_lines      BIGINT UNSIGNED NOT NULL DEFAULT 0,
  interactions    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cost_amount     DECIMAL(20, 6) NOT NULL DEFAULT 0,
  is_final        TINYINT(1) NOT NULL DEFAULT 0,
  computed_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                  ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date)
) ENGINE = InnoDB;

-- Per-harness (agent) token share of the community day, same precompute
-- pipeline: the stats worker replaces the day's rows on every recompute.
CREATE TABLE community_agent_daily_stats (
  metric_date     DATE NOT NULL,
  agent_id        VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  tokens_total    BIGINT UNSIGNED NOT NULL DEFAULT 0,
  computed_at     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                  ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date, agent_id)
) ENGINE = InnoDB;

CREATE TABLE community_stats_outbox (
  task_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  metric_date      DATE NOT NULL,
  task_status      VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  claim_token      CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  locked_by        VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_expires_at DATETIME(3) NULL,
  attempt_count    SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  last_error_code  VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  applied_at       DATETIME(3) NULL,
  created_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at       DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                   ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (task_id),
  KEY idx_community_outbox_poll (task_status, next_attempt_at, created_at),
  KEY idx_community_outbox_date (metric_date),
  KEY idx_community_outbox_cleanup (task_status, applied_at)
) ENGINE = InnoDB;
