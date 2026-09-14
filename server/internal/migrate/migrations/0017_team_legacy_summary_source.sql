ALTER TABLE team_analysis_rows
    ADD COLUMN legacy_aggregate BOOLEAN NOT NULL DEFAULT FALSE;
