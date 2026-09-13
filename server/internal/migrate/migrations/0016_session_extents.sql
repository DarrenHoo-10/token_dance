CREATE TABLE telemetry_session_extents (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY COMMENT '代理主键',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '首次创建 UTC 毫秒',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '最近更新 UTC 毫秒',
 delete_at BIGINT UNSIGNED NULL COMMENT '删除 UTC 毫秒；NULL 未删除',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展；不存必需业务状态',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '设备身份；账号绑定不影响会话范围',
 harness_id VARCHAR(64) NOT NULL COMMENT '工具标识',
 session_key BINARY(32) NOT NULL COMMENT '设备内稳定匿名会话身份',
 grain VARCHAR(8) NOT NULL COMMENT '独立统计消费者 hour/day/month',
 first_event_at BIGINT UNSIGNED NOT NULL COMMENT '已处理最早事件 UTC 毫秒',
 last_event_at BIGINT UNSIGNED NOT NULL COMMENT '已处理最晚事件 UTC 毫秒',
 UNIQUE KEY uk_tse_session (installation_id,harness_id,session_key,grain),
 CONSTRAINT fk_tse_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CHECK(last_event_at>=first_event_at)
);

-- Replace explicit activity duration with session spans; the worker fills the
-- retained event history in bounded batches without replaying other counters.
UPDATE telemetry_harness_metrics SET active_duration_ms=0,duration_known_count=0;
