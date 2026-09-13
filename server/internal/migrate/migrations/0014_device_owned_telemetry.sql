-- Device owns immutable facts and aggregates; the installation binding supplies user attribution.

-- Recompute derived buckets from retained event identities before dropping the old attribution columns.

DELETE FROM telemetry_tasks;

DELETE FROM telemetry_harness_metrics;

DELETE FROM telemetry_model_metrics;

DELETE FROM telemetry_skill_metrics;

DELETE FROM telemetry_cost_metrics;

DELETE FROM telemetry_bucket_entities;

UPDATE telemetry_events SET status_json=JSON_OBJECT('hour',0,'day',0,'month',0) WHERE delete_at IS NULL;

INSERT INTO telemetry_tasks (created_at,updated_at,event_row_id,consumer,runnable_at) SELECT e.updated_at,e.updated_at,e.id,c.consumer,e.updated_at FROM telemetry_events e CROSS JOIN (SELECT 'hour' consumer UNION ALL SELECT 'day' UNION ALL SELECT 'month') c WHERE e.delete_at IS NULL;

ALTER TABLE telemetry_events
 DROP INDEX idx_te_user_time,
 DROP FOREIGN KEY fk_ep_event_user,
 DROP COLUMN user_id,
 ADD KEY idx_te_user_time (installation_id,delete_at,occurred_at,id);

ALTER TABLE telemetry_harness_metrics
 DROP INDEX uk_thm_bucket,
 DROP FOREIGN KEY fk_thm_user,
 DROP INDEX idx_thm_harness,
 DROP COLUMN user_id,
 ADD UNIQUE KEY uk_thm_bucket (installation_id,grain,bucket_start,harness_id),
 ADD KEY idx_thm_harness (installation_id,grain,harness_id,bucket_start);

CREATE OR REPLACE VIEW bound_telemetry_harness_metrics AS SELECT m.*,i.user_id FROM telemetry_harness_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;

ALTER TABLE telemetry_model_metrics
 DROP INDEX uk_tmm_bucket,
 DROP FOREIGN KEY fk_tmm_user,
 DROP INDEX idx_tmm_harness,
 DROP INDEX idx_tmm_model_filter,
 DROP COLUMN user_id,
 ADD UNIQUE KEY uk_tmm_bucket (installation_id,grain,bucket_start,harness_id,model_key),
 ADD KEY idx_tmm_harness (installation_id,grain,harness_id,bucket_start,model_key),
 ADD KEY idx_tmm_model_filter (installation_id,grain,model_key,bucket_start,harness_id);

CREATE OR REPLACE VIEW bound_telemetry_model_metrics AS SELECT m.*,i.user_id FROM telemetry_model_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;

ALTER TABLE telemetry_skill_metrics
 DROP INDEX uk_tsm_bucket,
 DROP FOREIGN KEY fk_tsm_user,
 DROP COLUMN user_id,
 ADD UNIQUE KEY uk_tsm_bucket (installation_id,grain,bucket_start,harness_id,skill_id);

CREATE OR REPLACE VIEW bound_telemetry_skill_metrics AS SELECT m.*,i.user_id FROM telemetry_skill_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;

ALTER TABLE telemetry_cost_metrics
 DROP INDEX uk_tcm_bucket,
 DROP FOREIGN KEY fk_tcm_user,
 DROP COLUMN user_id,
 ADD UNIQUE KEY uk_tcm_bucket (installation_id,grain,bucket_start,harness_id,model_key,currency);

CREATE OR REPLACE VIEW bound_telemetry_cost_metrics AS SELECT m.*,i.user_id FROM telemetry_cost_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;

ALTER TABLE telemetry_bucket_entities
 DROP INDEX uk_tbe_entity,
 DROP INDEX idx_tbe_related,
 DROP INDEX idx_tbe_parent,
 DROP FOREIGN KEY fk_tbe_user,
 DROP COLUMN user_id,
 ADD UNIQUE KEY uk_tbe_entity (installation_id,grain,bucket_start,harness_id,entity_kind,entity_key),
 ADD KEY idx_tbe_related (installation_id,harness_id,entity_kind,entity_key,grain,bucket_start),
 ADD KEY idx_tbe_parent (installation_id,grain,bucket_start,harness_id,parent_key);
