ALTER TABLE team_analysis_rows
    ADD COLUMN legacy_aggregate BOOLEAN NOT NULL DEFAULT FALSE;

CREATE INDEX idx_te_team_legacy_partition
    ON telemetry_events (user_id, harness_id, event_type, occurred_at);
