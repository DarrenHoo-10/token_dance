-- TokenDance deletion workflow fencing and reconciliation
-- Target: MySQL 8.0.34+
-- Prerequisite: 0001_tokendance_server.sql, 0002_tokendance_user_system.sql, 0003_tokendance_analytics_extensions.sql

SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;
SET time_zone = '+00:00';
USE tokendance;

ALTER TABLE data_deletion_requests
  DROP CHECK chk_deletion_phase_state,
  ADD COLUMN claim_token
    CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER progress_cursor,
  ADD COLUMN claim_generation
    BIGINT UNSIGNED NOT NULL DEFAULT 0
    AFTER claim_token,
  ADD COLUMN locked_by
    VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER claim_generation,
  ADD COLUMN lease_expires_at
    DATETIME(3) NULL
    AFTER locked_by,
  ADD COLUMN attempt_count
    SMALLINT UNSIGNED NOT NULL DEFAULT 0
    AFTER lease_expires_at,
  ADD COLUMN next_attempt_at
    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
    AFTER attempt_count,
  ADD COLUMN last_error_code
    VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER next_attempt_at,
  ADD COLUMN updated_at
    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
      ON UPDATE CURRENT_TIMESTAMP(3)
    AFTER last_error_code,
  ADD KEY idx_deletion_requests_claim
    (request_status, next_attempt_at, lease_expires_at, cancel_before, requested_at),
  ADD CONSTRAINT chk_deletion_phase_state
    CHECK (
      (request_status = 'pending' AND phase IN ('queued', 'grace_period'))
      OR
      (request_status = 'running' AND phase IN
        ('revoking_access', 'deleting_events', 'deleting_aggregates',
         'deleting_objects', 'deleting_identity', 'reconciling'))
      OR (request_status = 'completed' AND phase = 'completed')
      OR (request_status = 'failed' AND phase = 'failed')
      OR (request_status = 'cancelled' AND phase = 'cancelled')
    );

UPDATE data_deletion_requests
SET request_status = 'failed',
    phase = 'failed',
    last_error_code = 'MIGRATION_RECOVERY',
    next_attempt_at = CURRENT_TIMESTAMP(3),
    updated_at = CURRENT_TIMESTAMP(3)
WHERE request_status = 'running';

UPDATE data_deletion_requests
SET next_attempt_at = requested_at,
    updated_at = CURRENT_TIMESTAMP(3)
WHERE request_status <> 'failed';

ALTER TABLE data_deletion_requests
  ADD CONSTRAINT chk_deletion_claim_state
    CHECK (
      (request_status = 'running'
       AND claim_token IS NOT NULL
       AND locked_by IS NOT NULL
       AND lease_expires_at IS NOT NULL)
      OR
      (request_status <> 'running'
       AND claim_token IS NULL
       AND locked_by IS NULL
       AND lease_expires_at IS NULL)
    );

CREATE TABLE deletion_object_keys (
  request_id                CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  object_key                VARCHAR(1024) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  object_kind               VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  deletion_status           VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'pending',
  attempt_count             SMALLINT UNSIGNED NOT NULL DEFAULT 0,
  last_error_code           VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
  deleted_at                DATETIME(3) NULL,
  created_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at                DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
                            ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (request_id, object_key),
  KEY idx_deletion_object_keys_status (request_id, deletion_status, created_at),
  CONSTRAINT fk_deletion_object_keys_request
    FOREIGN KEY (request_id) REFERENCES data_deletion_requests (request_id) ON DELETE CASCADE,
  CONSTRAINT chk_deletion_object_keys_kind
    CHECK (object_kind IN ('export', 'upload')),
  CONSTRAINT chk_deletion_object_keys_status
    CHECK (deletion_status IN ('pending', 'deleted', 'failed')),
  CONSTRAINT chk_deletion_object_keys_deleted_state
    CHECK (
      (deletion_status = 'deleted' AND deleted_at IS NOT NULL)
      OR
      (deletion_status <> 'deleted' AND deleted_at IS NULL)
    )
) ENGINE = InnoDB;
