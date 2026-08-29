-- TokenDance user system schema expansion
-- Target: MySQL 8.0.34+
-- Prerequisite: 0001_tokendance_server.sql

SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;
SET time_zone = '+00:00';
USE tokendance;

-- Preflight before any persistent DDL: duplicate unfinished account-deletion
-- requests indicate dirty baseline data and must be resolved explicitly.
CREATE TEMPORARY TABLE migration_0002_active_account_guard (
  user_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  PRIMARY KEY (user_id)
) ENGINE = InnoDB;

INSERT INTO migration_0002_active_account_guard (user_id)
SELECT user_id
FROM data_deletion_requests
WHERE deletion_scope = 'account'
  AND request_status IN ('pending', 'running', 'failed');

DROP TEMPORARY TABLE migration_0002_active_account_guard;

ALTER TABLE users
  DROP CHECK chk_users_account_status,
  ADD COLUMN handle
    VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER email_ciphertext,
  ADD COLUMN email_verified_at
    DATETIME(3) NULL
    AFTER email_ciphertext,
  ADD COLUMN avatar_object_id
    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER avatar_url,
  ADD COLUMN bio
    VARCHAR(280) NULL
    AFTER avatar_object_id,
  ADD COLUMN locale
    VARCHAR(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'en-US'
    AFTER timezone_name,
  ADD COLUMN onboarding_completed_at
    DATETIME(3) NULL
    AFTER locale,
  ADD COLUMN profile_version
    BIGINT UNSIGNED NOT NULL DEFAULT 1
    AFTER onboarding_completed_at,
  ADD COLUMN public_profile_updated_at
    DATETIME(3) NULL
    AFTER profile_version,
  ADD UNIQUE KEY uk_users_handle (handle),
  ADD KEY idx_users_avatar_object (avatar_object_id),
  ADD KEY idx_users_public_handle
    (account_status, leaderboard_visibility, handle),
  ADD KEY idx_users_public_name
    (account_status, leaderboard_visibility, display_name),
  ADD CONSTRAINT chk_users_account_status
    CHECK (account_status IN ('active', 'suspended', 'deletion_pending', 'deleted')),
  ADD CONSTRAINT chk_users_locale
    CHECK (locale IN ('zh-CN', 'en-US')),
  ADD CONSTRAINT chk_users_handle
    CHECK (handle IS NULL OR REGEXP_LIKE(handle, '^[a-z][a-z0-9_]{2,31}$', 'c'));

ALTER TABLE installations
  ADD COLUMN disabled_at
    DATETIME(3) NULL
    AFTER last_seen_at,
  ADD COLUMN disabled_reason
    VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER disabled_at,
  ADD COLUMN status_version
    BIGINT UNSIGNED NOT NULL DEFAULT 1
    AFTER disabled_reason,
  ADD CONSTRAINT chk_installations_disabled_reason
    CHECK (disabled_reason IS NULL OR disabled_reason IN
      ('user_paused', 'policy', 'admin', 'security')),
  ADD CONSTRAINT chk_installations_disabled_state
    CHECK (
      (installation_status = 'disabled' AND disabled_at IS NOT NULL AND disabled_reason IS NOT NULL)
      OR
      (installation_status <> 'disabled' AND disabled_at IS NULL AND disabled_reason IS NULL)
    );

CREATE TABLE user_password_credentials (
  user_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  password_hash            VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  password_algorithm       VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'argon2id',
  credential_version       INT UNSIGNED NOT NULL DEFAULT 1,
  failed_login_count       SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  locked_until             DATETIME(3) NULL,
  last_failed_login_at     DATETIME(3) NULL,
  password_changed_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  created_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                            ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id),
  KEY idx_user_credentials_locked (locked_until),
  CONSTRAINT fk_user_credentials_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT chk_user_credentials_algorithm
    CHECK (password_algorithm IN ('argon2id'))
) ENGINE = InnoDB;

CREATE TABLE user_sessions (
  session_id               CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  session_token_hash       BINARY(32) NOT NULL,
  csrf_token_hash          BINARY(32) NOT NULL,
  credential_version       INT UNSIGNED NOT NULL,
  session_status           VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'active',
  device_label             VARCHAR(120) NULL,
  user_agent_hash          BINARY(32) NULL,
  ip_prefix_hash           BINARY(32) NULL,
  last_seen_at             DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  idle_expires_at          DATETIME(3) NOT NULL,
  absolute_expires_at      DATETIME(3) NOT NULL,
  revoked_at               DATETIME(3) NULL,
  revoke_reason            VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NULL,
  created_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at               DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                            ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (session_id),
  UNIQUE KEY uk_user_sessions_token_hash (session_token_hash),
  KEY idx_user_sessions_user_status_seen (user_id, session_status, last_seen_at),
  KEY idx_user_sessions_expiry (session_status, idle_expires_at, absolute_expires_at),
  CONSTRAINT fk_user_sessions_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT chk_user_sessions_status
    CHECK (session_status IN ('active', 'revoked', 'expired')),
  CONSTRAINT chk_user_sessions_expiry
    CHECK (absolute_expires_at >= idle_expires_at),
  CONSTRAINT chk_user_sessions_revoke_reason
    CHECK (revoke_reason IS NULL OR revoke_reason IN
      ('logout', 'logout_others', 'password_reset', 'security', 'admin',
       'account_deletion', 'session_limit')),
  CONSTRAINT chk_user_sessions_revoke_state
    CHECK (
      (session_status = 'revoked' AND revoked_at IS NOT NULL AND revoke_reason IS NOT NULL)
      OR
      (session_status <> 'revoked' AND revoked_at IS NULL AND revoke_reason IS NULL)
    )
) ENGINE = InnoDB;

CREATE TABLE email_challenges (
  challenge_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                        CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  email_lookup_hash              BINARY(32) NOT NULL,
  email_ciphertext               VARBINARY(1024) NOT NULL,
  email_key_version              SMALLINT UNSIGNED NOT NULL,
  challenge_type                 VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  code_hash                      BINARY(32) NOT NULL,
  code_key_version               SMALLINT UNSIGNED NOT NULL,
  challenge_status               VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  attempt_count                  SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  max_attempts                   SMALLINT UNSIGNED NOT NULL DEFAULT 6,
  send_count                     SMALLINT UNSIGNED NOT NULL DEFAULT 1,
  requested_ip_prefix_hash       BINARY(32) NULL,
  expires_at                     DATETIME(3) NOT NULL,
  consumed_at                    DATETIME(3) NULL,
  created_at                     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                     DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                  ON UPDATE CURRENT_TIMESTAMP(3),
  active_email_lookup_hash       BINARY(32)
    GENERATED ALWAYS AS (
      CASE WHEN challenge_status = 'pending' THEN email_lookup_hash ELSE NULL END
    ) STORED,
  PRIMARY KEY (challenge_id),
  UNIQUE KEY uk_email_challenges_active
    (challenge_type, active_email_lookup_hash),
  KEY idx_email_challenges_status_expiry (challenge_status, expires_at),
  KEY idx_email_challenges_user_time (user_id, created_at),
  CONSTRAINT fk_email_challenges_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE SET NULL,
  CONSTRAINT chk_email_challenges_type
    CHECK (challenge_type IN ('register', 'password_reset', 'email_change')),
  CONSTRAINT chk_email_challenges_status
    CHECK (challenge_status IN ('pending', 'consumed', 'expired', 'locked', 'cancelled')),
  CONSTRAINT chk_email_challenges_attempts
    CHECK (max_attempts > 0 AND attempt_count <= max_attempts),
  CONSTRAINT chk_email_challenges_expiry
    CHECK (expires_at > created_at),
  CONSTRAINT chk_email_challenges_consumed_state
    CHECK (
      (challenge_status = 'consumed' AND consumed_at IS NOT NULL)
      OR
      (challenge_status <> 'consumed' AND consumed_at IS NULL)
    )
) ENGINE = InnoDB;

CREATE TABLE email_outbox (
  email_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  challenge_id              CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  idempotency_key           BINARY(32) NOT NULL,
  template_key              VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  locale                    VARCHAR(10) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  recipient_ciphertext      VARBINARY(1024) NOT NULL,
  payload_ciphertext        VARBINARY(4096) NOT NULL,
  encryption_key_version    SMALLINT UNSIGNED NOT NULL,
  delivery_status           VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  attempt_count             SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at           DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  locked_at                 DATETIME(3) NULL,
  locked_by                 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  provider_message_id       VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NULL,
  last_error_code           VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  sent_at                   DATETIME(3) NULL,
  expires_at                DATETIME(3) NOT NULL,
  created_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                             ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (email_id),
  UNIQUE KEY uk_email_outbox_idempotency (idempotency_key),
  KEY idx_email_outbox_dispatch (delivery_status, next_attempt_at, created_at),
  KEY idx_email_outbox_user_time (user_id, created_at),
  CONSTRAINT fk_email_outbox_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE SET NULL,
  CONSTRAINT fk_email_outbox_challenge
    FOREIGN KEY (challenge_id) REFERENCES email_challenges (challenge_id) ON DELETE SET NULL,
  CONSTRAINT chk_email_outbox_locale
    CHECK (locale IN ('zh-CN', 'en-US')),
  CONSTRAINT chk_email_outbox_status
    CHECK (delivery_status IN ('pending', 'sending', 'sent', 'failed', 'cancelled')),
  CONSTRAINT chk_email_outbox_expiry
    CHECK (expires_at > created_at),
  CONSTRAINT chk_email_outbox_sent_state
    CHECK (
      (delivery_status = 'sent' AND sent_at IS NOT NULL)
      OR
      (delivery_status <> 'sent' AND sent_at IS NULL)
    )
) ENGINE = InnoDB;

CREATE TABLE user_privacy_settings (
  user_id                       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  public_profile_enabled        BOOLEAN NOT NULL DEFAULT FALSE,
  show_bio                      BOOLEAN NOT NULL DEFAULT FALSE,
  show_token_total              BOOLEAN NOT NULL DEFAULT FALSE,
  show_trends                   BOOLEAN NOT NULL DEFAULT FALSE,
  show_activity_calendar        BOOLEAN NOT NULL DEFAULT FALSE,
  show_agent_breakdown          BOOLEAN NOT NULL DEFAULT FALSE,
  show_skill_ranking            BOOLEAN NOT NULL DEFAULT FALSE,
  show_achievements             BOOLEAN NOT NULL DEFAULT FALSE,
  privacy_version               BIGINT UNSIGNED NOT NULL DEFAULT 1,
  created_at                    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                 ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id),
  CONSTRAINT fk_user_privacy_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE
) ENGINE = InnoDB;

CREATE TABLE public_user_profiles (
  user_id                       CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  handle                        VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  display_name                  VARCHAR(80) NOT NULL,
  avatar_url                    VARCHAR(1024) NULL,
  bio                           VARCHAR(280) NULL,
  profile_status                VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'hidden',
  show_bio                      BOOLEAN NOT NULL DEFAULT FALSE,
  show_token_total              BOOLEAN NOT NULL DEFAULT FALSE,
  show_trends                   BOOLEAN NOT NULL DEFAULT FALSE,
  show_activity_calendar        BOOLEAN NOT NULL DEFAULT FALSE,
  show_agent_breakdown          BOOLEAN NOT NULL DEFAULT FALSE,
  show_skill_ranking            BOOLEAN NOT NULL DEFAULT FALSE,
  show_achievements             BOOLEAN NOT NULL DEFAULT FALSE,
  source_profile_version        BIGINT UNSIGNED NOT NULL,
  source_privacy_version        BIGINT UNSIGNED NOT NULL,
  projection_version            BIGINT UNSIGNED NOT NULL DEFAULT 1,
  published_at                  DATETIME(3) NULL,
  created_at                    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                                 ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id),
  UNIQUE KEY uk_public_user_profiles_handle (handle),
  KEY idx_public_profiles_handle (profile_status, handle),
  KEY idx_public_profiles_name (profile_status, display_name),
  CONSTRAINT fk_public_user_profiles_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT chk_public_user_profiles_status
    CHECK (profile_status IN ('published', 'hidden')),
  CONSTRAINT chk_public_user_profiles_bio
    CHECK (show_bio = TRUE OR bio IS NULL)
) ENGINE = InnoDB;

CREATE TABLE user_handle_history (
  handle                    VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  redirect_until            DATETIME(3) NOT NULL,
  reserved_until            DATETIME(3) NOT NULL,
  created_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (handle),
  KEY idx_user_handle_history_user (user_id, reserved_until),
  KEY idx_user_handle_history_expiry (reserved_until),
  CONSTRAINT fk_user_handle_history_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT chk_user_handle_history_handle
    CHECK (REGEXP_LIKE(handle, '^[a-z][a-z0-9_]{2,31}$', 'c')),
  CONSTRAINT chk_user_handle_history_windows
    CHECK (reserved_until >= redirect_until)
) ENGINE = InnoDB;

CREATE TABLE user_upload_objects (
  object_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  object_type                VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  object_key                 VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  content_type               VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  byte_size                  BIGINT UNSIGNED NULL,
  content_sha256             BINARY(32) NULL,
  image_width                INT UNSIGNED NULL,
  image_height               INT UNSIGNED NULL,
  upload_status              VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  expires_at                 DATETIME(3) NOT NULL,
  last_error_code            VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  uploaded_at                DATETIME(3) NULL,
  ready_at                   DATETIME(3) NULL,
  deleted_at                 DATETIME(3) NULL,
  created_at                 DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                 DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                              ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (object_id),
  UNIQUE KEY uk_user_upload_objects_key (object_key),
  KEY idx_upload_objects_user_status (user_id, upload_status, created_at),
  KEY idx_upload_objects_expiry (upload_status, expires_at),
  CONSTRAINT fk_user_upload_objects_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE RESTRICT,
  CONSTRAINT chk_user_upload_objects_type
    CHECK (object_type IN ('avatar')),
  CONSTRAINT chk_user_upload_objects_status
    CHECK (upload_status IN
      ('pending', 'uploaded', 'ready', 'rejected', 'deleted_pending', 'deleted')),
  CONSTRAINT chk_user_upload_objects_dimensions
    CHECK ((image_width IS NULL AND image_height IS NULL)
      OR (image_width > 0 AND image_height > 0)),
  CONSTRAINT chk_user_upload_objects_ready
    CHECK (upload_status <> 'ready' OR
      (content_type IS NOT NULL AND byte_size IS NOT NULL AND
       content_sha256 IS NOT NULL AND image_width IS NOT NULL AND
       image_height IS NOT NULL AND ready_at IS NOT NULL))
) ENGINE = InnoDB;

CREATE TABLE device_binding_challenges (
  challenge_id              CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  session_id                CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  code_lookup_hash          BINARY(32) NOT NULL,
  code_key_version          SMALLINT UNSIGNED NOT NULL,
  challenge_status          VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  expires_at                DATETIME(3) NOT NULL,
  consumed_installation_id  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  consumed_at               DATETIME(3) NULL,
  created_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                             ON UPDATE CURRENT_TIMESTAMP(3),
  active_session_key        CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  PRIMARY KEY (challenge_id),
  UNIQUE KEY uk_device_binding_code_hash (code_lookup_hash),
  UNIQUE KEY uk_device_binding_active_session (active_session_key),
  KEY idx_device_binding_expiry (challenge_status, expires_at),
  KEY idx_device_binding_user_time (user_id, created_at),
  CONSTRAINT fk_device_binding_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT fk_device_binding_session
    FOREIGN KEY (session_id) REFERENCES user_sessions (session_id) ON DELETE CASCADE,
  CONSTRAINT fk_device_binding_installation
    FOREIGN KEY (consumed_installation_id) REFERENCES installations (installation_id)
      ON DELETE SET NULL,
  CONSTRAINT chk_device_binding_status
    CHECK (challenge_status IN ('pending', 'consumed', 'expired', 'cancelled')),
  CONSTRAINT chk_device_binding_active_slot
    CHECK (
      (challenge_status = 'pending' AND active_session_key = session_id)
      OR
      (challenge_status <> 'pending' AND active_session_key IS NULL)
    ),
  CONSTRAINT chk_device_binding_consumed_state
    CHECK (
      (challenge_status = 'consumed' AND consumed_at IS NOT NULL)
      OR
      (challenge_status <> 'consumed' AND consumed_at IS NULL)
    ),
  CONSTRAINT chk_device_binding_expiry
    CHECK (expires_at > created_at)
) ENGINE = InnoDB;

CREATE TABLE user_security_events (
  event_id                   CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  session_id                 CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL,
  event_type                 VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  outcome                    VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  subject_lookup_hash        BINARY(32) NULL,
  ip_prefix_hash             BINARY(32) NULL,
  user_agent_hash            BINARY(32) NULL,
  metadata_json              JSON NULL,
  created_at                 DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (event_id),
  KEY idx_user_security_events_user_time (user_id, created_at),
  KEY idx_user_security_events_type_time (event_type, created_at),
  CONSTRAINT fk_user_security_events_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE SET NULL,
  CONSTRAINT fk_user_security_events_session
    FOREIGN KEY (session_id) REFERENCES user_sessions (session_id) ON DELETE SET NULL,
  CONSTRAINT chk_user_security_events_outcome
    CHECK (outcome IN ('success', 'denied', 'failure'))
) ENGINE = InnoDB;

CREATE TABLE data_export_jobs (
  export_id                  CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  user_id                    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  idempotency_key            VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  request_hash               BINARY(32) NOT NULL,
  export_scope               VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  export_format              VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'csv',
  filter_json                JSON NOT NULL,
  job_status                 VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  attempt_count              SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  next_attempt_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  locked_at                  DATETIME(3) NULL,
  locked_by                  VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  object_key                 VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NULL,
  file_sha256                BINARY(32) NULL,
  file_size                  BIGINT UNSIGNED NULL,
  last_error_code            VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  started_at                 DATETIME(3) NULL,
  completed_at               DATETIME(3) NULL,
  expires_at                 DATETIME(3) NULL,
  created_at                 DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                 DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                             ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (export_id),
  UNIQUE KEY uk_data_export_user_idempotency (user_id, idempotency_key),
  KEY idx_data_export_jobs_dispatch (job_status, next_attempt_at, created_at),
  KEY idx_data_export_jobs_user_time (user_id, created_at),
  CONSTRAINT fk_data_export_jobs_user
    FOREIGN KEY (user_id) REFERENCES users (user_id) ON DELETE CASCADE,
  CONSTRAINT chk_data_export_scope
    CHECK (export_scope IN ('summary', 'activity', 'all_aggregates')),
  CONSTRAINT chk_data_export_format
    CHECK (export_format IN ('csv', 'json')),
  CONSTRAINT chk_data_export_status
    CHECK (job_status IN ('pending', 'running', 'completed', 'failed', 'expired', 'cancelled')),
  CONSTRAINT chk_data_export_completed_state
    CHECK (
      (job_status = 'completed' AND completed_at IS NOT NULL AND object_key IS NOT NULL
       AND file_sha256 IS NOT NULL AND file_size IS NOT NULL AND expires_at IS NOT NULL)
      OR
      (job_status <> 'completed')
    )
) ENGINE = InnoDB;

ALTER TABLE data_deletion_requests
  ADD COLUMN cancel_before
    DATETIME(3) NULL
    AFTER requested_at,
  ADD COLUMN cancelled_at
    DATETIME(3) NULL
    AFTER completed_at,
  ADD COLUMN scope_filter_json
    JSON NULL
    AFTER deletion_scope,
  ADD COLUMN phase
    VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'queued'
    AFTER request_status,
  ADD COLUMN progress_cursor
    BIGINT UNSIGNED NOT NULL DEFAULT 0
    AFTER phase,
  ADD COLUMN active_account_key
    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL;

UPDATE data_deletion_requests
SET cancel_before = DATE_ADD(requested_at, INTERVAL 7 DAY)
WHERE deletion_scope = 'account'
  AND cancel_before IS NULL;

UPDATE data_deletion_requests
SET phase = CASE request_status
  WHEN 'pending' THEN 'queued'
  WHEN 'running' THEN 'revoking_access'
  WHEN 'completed' THEN 'completed'
  WHEN 'failed' THEN 'failed'
  ELSE phase
END;

UPDATE data_deletion_requests
SET active_account_key = user_id
WHERE deletion_scope = 'account'
  AND request_status IN ('pending', 'running', 'failed');

ALTER TABLE data_deletion_requests
  DROP CHECK chk_deletion_status,
  ADD UNIQUE KEY uk_deletion_active_account_user (active_account_key),
  ADD KEY idx_deletion_requests_dispatch
    (request_status, cancel_before, requested_at),
  ADD CONSTRAINT chk_deletion_status
    CHECK (request_status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
  ADD CONSTRAINT chk_deletion_phase_state
    CHECK (
      (request_status = 'pending' AND phase IN ('queued', 'grace_period'))
      OR
      (request_status = 'running' AND phase IN
        ('revoking_access', 'deleting_events', 'deleting_aggregates', 'deleting_identity'))
      OR (request_status = 'completed' AND phase = 'completed')
      OR (request_status = 'failed' AND phase = 'failed')
      OR (request_status = 'cancelled' AND phase = 'cancelled')
    ),
  ADD CONSTRAINT chk_deletion_active_slot
    CHECK (
      (deletion_scope = 'account'
       AND request_status IN ('pending', 'running', 'failed')
       AND active_account_key IS NOT NULL)
      OR
      ((deletion_scope <> 'account'
        OR request_status NOT IN ('pending', 'running', 'failed'))
       AND active_account_key IS NULL)
    ),
  ADD CONSTRAINT chk_deletion_cancel_window
    CHECK (deletion_scope <> 'account' OR cancel_before IS NOT NULL),
  ADD CONSTRAINT chk_deletion_cancelled_state
    CHECK (
      (request_status = 'cancelled' AND cancelled_at IS NOT NULL)
      OR
      (request_status <> 'cancelled' AND cancelled_at IS NULL)
    );
