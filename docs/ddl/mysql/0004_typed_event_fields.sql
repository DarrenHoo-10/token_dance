USE tokenshow;

ALTER TABLE usage_events
  ADD COLUMN workspace_hash BINARY(32) NULL AFTER parent_session_hash,
  ADD COLUMN session_end_reason VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER workspace_hash,
  ADD COLUMN turn_trigger VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL AFTER session_end_reason,
  ADD CONSTRAINT chk_usage_events_session_end_reason
    CHECK (session_end_reason IS NULL OR session_end_reason IN ('completed', 'cancelled', 'error', 'timeout', 'unknown')),
  ADD CONSTRAINT chk_usage_events_turn_trigger
    CHECK (turn_trigger IS NULL OR turn_trigger IN ('user', 'system', 'scheduled', 'subagent'));
