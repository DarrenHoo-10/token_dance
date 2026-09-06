CREATE TABLE device_daily_aggregates (
 installation_id VARCHAR(64) NOT NULL,
 user_id VARCHAR(64) NOT NULL,
 metric_date DATE NOT NULL,
 revision BIGINT NOT NULL,
 content_hash BINARY(32) NOT NULL,
 payload_json JSON NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 PRIMARY KEY (installation_id, metric_date),
 KEY idx_device_aggregates_user_date (user_id, metric_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
