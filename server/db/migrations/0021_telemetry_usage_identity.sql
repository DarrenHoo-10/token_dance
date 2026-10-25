-- Account-scoped usage identity for cross-device deduplication.
-- Claude Code SSH remote sessions mirror the same transcript to two machines;
-- each collector uploads its own copy under a different installation_id, and
-- account aggregates sum installations, so the same usage was counted twice.
-- The identity is HMAC'd server-side from wire usageIdentity fields; raw
-- session/message ids are never persisted.

ALTER TABLE telemetry_events
	ADD COLUMN usage_identity_hash BINARY(32) NULL COMMENT '账号内跨设备用量去重身份，HMAC-SHA256(harness,会话原生id,消息原生id)；由服务端在接收时计算，不落原始值；NULL 表示无身份、不参与去重',
	ADD COLUMN usage_identity_source TINYINT UNSIGNED NULL COMMENT '用量身份的副本来源：0 原始来源、1 ssh_mirror（Claude Code SSH 远程会话的本机镜像副本）；NULL 表示无用量身份',
	ADD KEY idx_te_identity (user_id, usage_identity_hash, id);
