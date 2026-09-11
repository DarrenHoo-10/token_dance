-- P8: durable empty-DB rollout generation so repeat starts do not re-wipe stats.
-- Auth / device / desktop release tables are never listed in the reset tool.

CREATE TABLE event_pipeline_rollout_state (
  id TINYINT UNSIGNED NOT NULL,
  generation BIGINT UNSIGNED NOT NULL,
  phase VARCHAR(32) NOT NULL,
  reset_started_at DATETIME(3) NULL,
  reset_completed_at DATETIME(3) NULL,
  notes_json JSON NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  CONSTRAINT chk_event_pipeline_rollout_id CHECK (id = 1),
  CONSTRAINT chk_event_pipeline_rollout_phase CHECK (
    phase IN ('pending', 'resetting', 'ready', 'rolled_back')
  )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO event_pipeline_rollout_state (id, generation, phase, notes_json)
VALUES (1, 0, 'pending', JSON_OBJECT());
