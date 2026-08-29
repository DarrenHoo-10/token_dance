USE tokenshow;

ALTER TABLE aggregation_watermarks
  ADD COLUMN committed_through_at DATETIME(3) NOT NULL DEFAULT '1970-01-01 00:00:00.000' AFTER source_max_event_pk;

ALTER TABLE data_deletion_requests
  ADD COLUMN range_start DATETIME(3) NULL AFTER deletion_scope,
  ADD COLUMN range_end DATETIME(3) NULL AFTER range_start;

CREATE TABLE IF NOT EXISTS daily_cost_metrics (
  metric_date             DATE NOT NULL,
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  agent_id                VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  cost_currency           CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  cost_source             VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  cost_amount             DECIMAL(20, 8) NOT NULL DEFAULT 0,
  discount_amount         DECIMAL(20, 8) NOT NULL DEFAULT 0,
  source_max_event_pk     BIGINT UNSIGNED NOT NULL DEFAULT 0,
  aggregation_version     INT UNSIGNED NOT NULL,
  computed_at             DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (metric_date, user_id, agent_id, cost_currency, cost_source),
  KEY idx_daily_cost_user_date (user_id, metric_date),
  CONSTRAINT fk_daily_cost_metrics_user FOREIGN KEY (user_id) REFERENCES users (user_id)
) ENGINE = InnoDB;

CREATE TABLE IF NOT EXISTS teams (
  team_id                 VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_name               VARCHAR(120) NOT NULL,
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (team_id)
) ENGINE = InnoDB;

CREATE TABLE IF NOT EXISTS team_memberships (
  team_id                 VARCHAR(80) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  membership_status       VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (team_id, user_id),
  KEY idx_team_memberships_user (user_id, membership_status),
  CONSTRAINT fk_team_memberships_team FOREIGN KEY (team_id) REFERENCES teams (team_id) ON DELETE CASCADE,
  CONSTRAINT fk_team_memberships_user FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT chk_team_membership_status CHECK (membership_status IN ('active', 'removed'))
) ENGINE = InnoDB;
