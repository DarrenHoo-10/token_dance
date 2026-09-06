-- TokenDance teams module
-- Target: MySQL 8.0.34+
-- Prerequisite: 0001–0004

SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;
SET time_zone = '+00:00';
USE tokendance;

CREATE TABLE teams (
  team_id            CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  name               VARCHAR(1024) NOT NULL,
  description        VARCHAR(4096) NOT NULL DEFAULT '',
  timezone_name      VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  owner_user_id      CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status             VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  profile_version    BIGINT UNSIGNED NOT NULL DEFAULT 1,
  auth_revision      BIGINT UNSIGNED NOT NULL DEFAULT 1,
  avatar_object_id   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  created_at         DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  dissolved_at       DATETIME(3) NULL,
  PRIMARY KEY (team_id),
  KEY idx_teams_owner (owner_user_id),
  KEY idx_teams_status (status),
  CONSTRAINT fk_teams_owner
    FOREIGN KEY (owner_user_id) REFERENCES users (user_id),
  CONSTRAINT chk_teams_status
    CHECK (status IN ('active', 'dissolved')),
  CONSTRAINT chk_teams_active_owner
    CHECK (status <> 'active' OR owner_user_id IS NOT NULL),
  CONSTRAINT chk_teams_dissolved_state
    CHECK (
      (status = 'dissolved' AND dissolved_at IS NOT NULL)
      OR
      (status = 'active' AND dissolved_at IS NULL)
    )
) ENGINE = InnoDB;

CREATE TABLE team_memberships (
  membership_id    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  base_role        VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  sharing_version  BIGINT UNSIGNED NOT NULL DEFAULT 1,
  joined_at        DATETIME(3) NOT NULL,
  ended_at         DATETIME(3) NULL,
  end_reason       VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NULL,
  PRIMARY KEY (membership_id),
  UNIQUE KEY uk_team_membership_combo (team_id, user_id, membership_id),
  KEY idx_team_memberships_team_ended (team_id, ended_at, user_id),
  KEY idx_team_memberships_user (user_id, ended_at),
  CONSTRAINT fk_team_memberships_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id),
  CONSTRAINT fk_team_memberships_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT chk_team_memberships_role
    CHECK (base_role IN ('admin', 'member')),
  CONSTRAINT chk_team_memberships_end_pair
    CHECK (
      (ended_at IS NULL AND end_reason IS NULL)
      OR
      (ended_at IS NOT NULL AND end_reason IS NOT NULL)
    ),
  CONSTRAINT chk_team_memberships_end_reason
    CHECK (end_reason IS NULL OR end_reason IN ('left', 'removed', 'dissolved', 'account_deleted'))
) ENGINE = InnoDB;

CREATE TABLE user_current_teams (
  user_id        CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id        CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  membership_id  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  joined_at      DATETIME(3) NOT NULL,
  PRIMARY KEY (user_id),
  UNIQUE KEY uk_current_membership (membership_id),
  KEY idx_current_team_user (team_id, user_id),
  CONSTRAINT fk_current_team_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT fk_current_membership
    FOREIGN KEY (team_id, user_id, membership_id)
    REFERENCES team_memberships (team_id, user_id, membership_id)
) ENGINE = InnoDB;

CREATE TABLE team_sharing_grants (
  grant_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  membership_id     CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  dimension         VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  starts_at         DATETIME(3) NOT NULL,
  ends_at           DATETIME(3) NULL,
  revoked_at        DATETIME(3) NULL,
  active_dimension  VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NULL,
  PRIMARY KEY (grant_id),
  UNIQUE KEY uk_grant_active_dimension (membership_id, active_dimension),
  KEY idx_grants_membership_dim (membership_id, dimension, starts_at),
  CONSTRAINT fk_grants_membership
    FOREIGN KEY (membership_id) REFERENCES team_memberships (membership_id),
  CONSTRAINT chk_grants_dimension
    CHECK (dimension IN ('base', 'named', 'classification', 'cost')),
  CONSTRAINT chk_grants_active_state
    CHECK (
      (
        revoked_at IS NULL
        AND ends_at IS NULL
        AND active_dimension IS NOT NULL
        AND active_dimension = dimension
      )
      OR
      (
        revoked_at IS NOT NULL
        AND ends_at IS NOT NULL
        AND active_dimension IS NULL
      )
    ),
  CONSTRAINT chk_grants_ends_not_before_start
    CHECK (ends_at IS NULL OR ends_at >= starts_at)
) ENGINE = InnoDB;

CREATE TABLE team_invitations (
  invitation_id            CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  inviter_user_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  invited_role             VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  recipient_lookup_hash    BINARY(32) NOT NULL,
  lookup_key_version       SMALLINT UNSIGNED NOT NULL,
  recipient_ciphertext     VARBINARY(1024) NOT NULL,
  encryption_key_version   SMALLINT UNSIGNED NOT NULL,
  status                   VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  active_recipient_hash    BINARY(32) NULL,
  created_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at               DATETIME(3) NOT NULL,
  accepted_by_user_id      CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  accepted_membership_id   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  version                  BIGINT UNSIGNED NOT NULL DEFAULT 1,
  PRIMARY KEY (invitation_id),
  UNIQUE KEY uk_team_active_recipient (team_id, active_recipient_hash),
  KEY idx_invitations_recipient (recipient_lookup_hash, status, expires_at),
  KEY idx_invitations_team (team_id, status, created_at),
  CONSTRAINT fk_invitations_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id),
  CONSTRAINT fk_invitations_inviter
    FOREIGN KEY (inviter_user_id) REFERENCES users (user_id),
  CONSTRAINT chk_invitations_role
    CHECK (invited_role IN ('admin', 'member')),
  CONSTRAINT chk_invitations_status
    CHECK (status IN ('pending', 'accepted', 'revoked', 'expired')),
  CONSTRAINT chk_invitations_active_hash
    CHECK (
      (
        status = 'pending'
        AND active_recipient_hash IS NOT NULL
        AND active_recipient_hash = recipient_lookup_hash
      )
      OR
      (
        status <> 'pending'
        AND active_recipient_hash IS NULL
      )
    )
) ENGINE = InnoDB;

CREATE TABLE team_invite_links (
  link_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  creator_user_id          CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  token_hash               BINARY(32) NOT NULL,
  token_ciphertext         VARBINARY(1024) NOT NULL,
  encryption_key_version   SMALLINT UNSIGNED NOT NULL,
  status                   VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  version                  BIGINT UNSIGNED NOT NULL DEFAULT 1,
  max_uses                 INT UNSIGNED NOT NULL,
  used_count               INT UNSIGNED NOT NULL DEFAULT 0,
  created_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at               DATETIME(3) NOT NULL,
  revoked_at               DATETIME(3) NULL,
  PRIMARY KEY (link_id),
  UNIQUE KEY uk_invite_link_token (token_hash),
  KEY idx_invite_links_team (team_id, created_at, link_id),
  CONSTRAINT fk_invite_links_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id),
  CONSTRAINT fk_invite_links_creator
    FOREIGN KEY (creator_user_id) REFERENCES users (user_id),
  CONSTRAINT chk_invite_links_status
    CHECK (status IN ('active', 'revoked')),
  CONSTRAINT chk_invite_links_max_uses
    CHECK (max_uses >= 1 AND max_uses <= 100),
  CONSTRAINT chk_invite_links_used_count
    CHECK (used_count >= 0 AND used_count <= max_uses),
  CONSTRAINT chk_invite_links_expiry
    CHECK (expires_at > created_at),
  CONSTRAINT chk_invite_links_revoked_pair
    CHECK (
      (status = 'revoked' AND revoked_at IS NOT NULL)
      OR
      (status = 'active' AND revoked_at IS NULL)
    )
) ENGINE = InnoDB;

CREATE TABLE team_invite_link_joins (
  link_id        CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id        CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  membership_id  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  joined_at      DATETIME(3) NOT NULL,
  PRIMARY KEY (link_id, user_id),
  UNIQUE KEY uk_link_join_membership (membership_id),
  CONSTRAINT fk_link_joins_link
    FOREIGN KEY (link_id) REFERENCES team_invite_links (link_id),
  CONSTRAINT fk_link_joins_user
    FOREIGN KEY (user_id) REFERENCES users (user_id),
  CONSTRAINT fk_link_joins_membership
    FOREIGN KEY (membership_id) REFERENCES team_memberships (membership_id)
) ENGINE = InnoDB;

CREATE TABLE team_command_receipts (
  actor_user_id            CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  operation_scope          VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  idempotency_key_hash     BINARY(32) NOT NULL,
  request_hash             BINARY(32) NOT NULL,
  result_type              VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  result_id                CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  created_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at               DATETIME(3) NOT NULL,
  PRIMARY KEY (actor_user_id, operation_scope, idempotency_key_hash),
  KEY idx_team_receipts_expiry (expires_at),
  CONSTRAINT fk_team_receipts_actor
    FOREIGN KEY (actor_user_id) REFERENCES users (user_id)
) ENGINE = InnoDB;

CREATE TABLE team_source_revisions (
  team_id           CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  source_revision   BIGINT UNSIGNED NOT NULL DEFAULT 0,
  changed_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (team_id),
  CONSTRAINT fk_team_source_revisions_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id)
) ENGINE = InnoDB;

CREATE TABLE team_analysis_snapshots (
  snapshot_id            CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id                CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  from_date              DATE NOT NULL,
  to_date_exclusive      DATE NOT NULL,
  auth_revision          BIGINT UNSIGNED NOT NULL,
  source_revision        BIGINT UNSIGNED NOT NULL,
  rule_version           VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  status                 VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  active_request_key     VARCHAR(191) CHARACTER SET ascii COLLATE ascii_bin NULL,
  as_of                  DATETIME(3) NOT NULL,
  lease_token            CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_generation       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  published_generation   BIGINT UNSIGNED NOT NULL DEFAULT 0,
  lease_expires_at       DATETIME(3) NULL,
  attempt_count          SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at        DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  error_code             VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  expires_at             DATETIME(3) NOT NULL,
  PRIMARY KEY (snapshot_id),
  UNIQUE KEY uk_snapshot_active_request (active_request_key),
  KEY idx_snapshots_query (team_id, from_date, to_date_exclusive, auth_revision, status, as_of),
  KEY idx_snapshots_claim (status, next_attempt_at, lease_expires_at),
  CONSTRAINT fk_snapshots_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id),
  CONSTRAINT chk_snapshots_status
    CHECK (status IN ('queued', 'building', 'ready', 'obsolete', 'failed', 'expired')),
  CONSTRAINT chk_snapshots_active_key
    CHECK (
      (
        status IN ('queued', 'building')
        AND active_request_key IS NOT NULL
      )
      OR
      (
        status IN ('ready', 'obsolete', 'failed', 'expired')
        AND active_request_key IS NULL
      )
    )
) ENGINE = InnoDB;

CREATE TABLE team_analysis_rows (
  snapshot_id                    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  build_generation               BIGINT UNSIGNED NOT NULL,
  row_key                        CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  membership_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  metric_date                    DATE NULL,
  visibility_mask                INT UNSIGNED NOT NULL DEFAULT 0,
  agent_id                       VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  provider_id                    VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  model_id                       VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL,
  currency                       VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NULL,
  token_exact_total              DECIMAL(30,0) NOT NULL DEFAULT 0,
  token_derived_total            DECIMAL(30,0) NOT NULL DEFAULT 0,
  usage_event_count              DECIMAL(30,0) NOT NULL DEFAULT 0,
  token_supported_event_count    DECIMAL(30,0) NOT NULL DEFAULT 0,
  reported_cost_amount           DECIMAL(30,8) NOT NULL DEFAULT 0,
  estimated_cost_amount          DECIMAL(30,8) NOT NULL DEFAULT 0,
  reported_cost_event_count      DECIMAL(30,0) NOT NULL DEFAULT 0,
  estimated_cost_event_count     DECIMAL(30,0) NOT NULL DEFAULT 0,
  reported_covered_usage_count   DECIMAL(30,0) NOT NULL DEFAULT 0,
  estimated_covered_usage_count  DECIMAL(30,0) NOT NULL DEFAULT 0,
  unattributed_cost_count        DECIMAL(30,0) NOT NULL DEFAULT 0,
  max_received_at                DATETIME(3) NULL,
  PRIMARY KEY (snapshot_id, build_generation, row_key),
  KEY idx_analysis_rows_member_date (snapshot_id, build_generation, membership_id, metric_date),
  CONSTRAINT fk_analysis_rows_snapshot
    FOREIGN KEY (snapshot_id) REFERENCES team_analysis_snapshots (snapshot_id) ON DELETE CASCADE
) ENGINE = InnoDB;

CREATE TABLE team_export_jobs (
  export_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  requester_user_id         CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  requester_membership_id   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  snapshot_id               CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  auth_revision             BIGINT UNSIGNED NOT NULL,
  export_kind               VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  filter_json               JSON NOT NULL,
  status                    VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'queued',
  lease_token               CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  lease_generation          BIGINT UNSIGNED NOT NULL DEFAULT 0,
  lease_expires_at          DATETIME(3) NULL,
  attempt_count             SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  object_key                VARCHAR(512) CHARACTER SET ascii COLLATE ascii_bin NULL,
  file_sha256               BINARY(32) NULL,
  file_size                 BIGINT UNSIGNED NULL,
  created_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at                DATETIME(3) NOT NULL,
  error_code                VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  PRIMARY KEY (export_id),
  KEY idx_exports_requester (team_id, requester_user_id, created_at),
  KEY idx_exports_claim (status, next_attempt_at, lease_expires_at),
  CONSTRAINT fk_exports_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id),
  CONSTRAINT fk_exports_snapshot
    FOREIGN KEY (snapshot_id) REFERENCES team_analysis_snapshots (snapshot_id),
  CONSTRAINT chk_exports_kind
    CHECK (export_kind IN ('daily', 'agents', 'models', 'members')),
  CONSTRAINT chk_exports_status
    CHECK (status IN ('queued', 'running', 'completed', 'revoked', 'failed', 'expired'))
) ENGINE = InnoDB;

CREATE TABLE team_audit_events (
  audit_id            CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id             CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  actor_user_id       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  action              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target_type         VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  target_id           CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  safe_details_json   JSON NOT NULL,
  created_at          DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (audit_id),
  KEY idx_audit_team_time (team_id, created_at, audit_id),
  CONSTRAINT fk_audit_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id)
) ENGINE = InnoDB;

CREATE TABLE team_upload_objects (
  object_id       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id         CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  uploader_user_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  object_key      VARCHAR(512) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  content_type    VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  byte_size       BIGINT UNSIGNED NOT NULL DEFAULT 0,
  image_width     INT UNSIGNED NOT NULL DEFAULT 0,
  image_height    INT UNSIGNED NOT NULL DEFAULT 0,
  sha256          BINARY(32) NOT NULL,
  status          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  expires_at      DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (object_id),
  UNIQUE KEY uk_team_upload_object_key (object_key),
  KEY idx_team_upload_team (team_id, status),
  CONSTRAINT fk_team_upload_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id),
  CONSTRAINT fk_team_upload_uploader
    FOREIGN KEY (uploader_user_id) REFERENCES users (user_id)
) ENGINE = InnoDB;

CREATE TABLE team_deletion_barriers (
  deletion_request_id  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  team_id              CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  blocked_at           DATETIME(3) NOT NULL,
  released_at          DATETIME(3) NULL,
  PRIMARY KEY (deletion_request_id, team_id),
  KEY idx_deletion_barriers_team (team_id, released_at),
  CONSTRAINT fk_deletion_barriers_request
    FOREIGN KEY (deletion_request_id) REFERENCES data_deletion_requests (request_id),
  CONSTRAINT fk_deletion_barriers_team
    FOREIGN KEY (team_id) REFERENCES teams (team_id)
) ENGINE = InnoDB;

ALTER TABLE teams
  ADD CONSTRAINT fk_teams_avatar_object
    FOREIGN KEY (avatar_object_id) REFERENCES team_upload_objects (object_id);

ALTER TABLE email_outbox
  ADD COLUMN team_invitation_id
    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER challenge_id,
  ADD COLUMN team_invitation_version
    BIGINT UNSIGNED NULL
    AFTER team_invitation_id,
  ADD KEY idx_email_outbox_team_invitation (team_invitation_id, delivery_status),
  ADD CONSTRAINT fk_email_outbox_team_invitation
    FOREIGN KEY (team_invitation_id) REFERENCES team_invitations (invitation_id);
