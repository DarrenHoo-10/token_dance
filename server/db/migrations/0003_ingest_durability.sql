USE tokenshow;

CREATE TABLE IF NOT EXISTS ingest_batch_rejections (
  batch_id              CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  event_ordinal         INT UNSIGNED NOT NULL,
  event_id              VARCHAR(43) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  error_code            VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
  retryable             BOOLEAN NOT NULL,
  created_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (batch_id, event_ordinal),
  CONSTRAINT fk_ingest_batch_rejections_batch
    FOREIGN KEY (batch_id) REFERENCES ingest_batches (batch_id) ON DELETE CASCADE
) ENGINE = InnoDB;

ALTER TABLE usage_events
  ADD COLUMN child_session_hash BINARY(32) NULL AFTER parent_session_hash,
  ADD COLUMN spawned_agent_type VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER child_session_hash,
  ADD COLUMN code_language VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER code_file_count,
  ADD COLUMN cost_discount_amount DECIMAL(20, 8) NULL AFTER cost_source;
