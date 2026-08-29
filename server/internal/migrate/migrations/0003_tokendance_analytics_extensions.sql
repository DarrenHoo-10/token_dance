-- TokenDance analytics compatibility expansion
-- Target: MySQL 8.0.34+
-- Prerequisite: 0001_tokendance_server.sql, 0002_tokendance_user_system.sql

SET NAMES utf8mb4 COLLATE utf8mb4_0900_ai_ci;
SET time_zone = '+00:00';
USE tokendance;

ALTER TABLE usage_events
  ADD COLUMN turn_trigger
    VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NULL
    AFTER turn_hash,
  ADD CONSTRAINT chk_usage_events_turn_trigger
    CHECK (turn_trigger IS NULL OR turn_trigger IN
      ('user', 'system', 'automation', 'resume', 'unknown'));

ALTER TABLE daily_user_agent_metrics
  ADD COLUMN token_input_total BIGINT UNSIGNED NULL AFTER estimated_token_total,
  ADD COLUMN token_output_total BIGINT UNSIGNED NULL AFTER token_input_total,
  ADD COLUMN token_cache_read_total BIGINT UNSIGNED NULL AFTER token_output_total,
  ADD COLUMN token_cache_write_total BIGINT UNSIGNED NULL AFTER token_cache_read_total,
  ADD COLUMN token_reasoning_total BIGINT UNSIGNED NULL AFTER token_cache_write_total,
  ADD COLUMN active_duration_ms BIGINT UNSIGNED NULL AFTER correlated_code_lines,
  ADD COLUMN message_count BIGINT UNSIGNED NULL AFTER active_duration_ms,
  ADD COLUMN user_message_count BIGINT UNSIGNED NULL AFTER message_count;

ALTER TABLE daily_user_agent_model_metrics
  ADD COLUMN token_input_total BIGINT UNSIGNED NULL AFTER estimated_token_total,
  ADD COLUMN token_output_total BIGINT UNSIGNED NULL AFTER token_input_total,
  ADD COLUMN token_cache_read_total BIGINT UNSIGNED NULL AFTER token_output_total,
  ADD COLUMN token_cache_write_total BIGINT UNSIGNED NULL AFTER token_cache_read_total,
  ADD COLUMN token_reasoning_total BIGINT UNSIGNED NULL AFTER token_cache_write_total;
