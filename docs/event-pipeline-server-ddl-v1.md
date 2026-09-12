# 服务端事件流水线完整 DDL v1

状态：2026-09-12 修订；设备归属变更在 0014_device_owned_telemetry.sql 实施，尚未发布。内测旧采集/统计数据不迁移。共 10 张目标表：9 张新事件业务表及空表重建的 aggregate_dirty_days；均采用 id 主键和 created_at、updated_at、delete_at、extra 公共字段。installations 继续复用现有结构，不为历史兼容新增字段或改主键。

[完整技术方案](event-pipeline-refactor-technical-plan-v1.md) · [SQL 文件](event-pipeline-server-schema-v1.mysql.sql) · [本地 DDL](event-pipeline-ddl-v3.md)

## 执行边界和类型

SQL 以 MySQL 8.4/InnoDB 为设计基线，依赖现有 users.user_id 和 installations.installation_id（CHAR(30) ASCII ascii_bin）唯一身份。这里给出空表初始化的完整目标 CREATE TABLE，不执行旧业务记录转换。账号、会话、设备及认证设施保留；排行榜和 community 复用结构/刷新机制但清空旧统计与发布待办，旧快照退出读写链路；不在本方案复制整套认证系统。

软件版本发布设施不变：desktop_releases、desktop_release_channels、desktop_release_publication 的结构与数据均保留，不纳入本次统计重置。现有 stable.json 生成、版本检查和安装包发布流程继续使用；统计 outbox 清理不包含软件版本发布状态。

服务器管理时间和事件时间均用 BIGINT UNSIGNED UTC 毫秒；业务日用 DATE；固定摘要 BINARY(32)、租约 BINARY(16)、短标识 VARCHAR/CHAR、扩展与分组数据原生 JSON。大 Token/金额累计用 DECIMAL(38,0)，其他计数用 BIGINT UNSIGNED；业务代码受检运算，不允许负差额落库或隐式浮点转换。所有索引均围绕已知查询/唯一性/外键，不建立通用 JSON GIN 或每个状态独立索引。

extra 仅放可选扩展；必需字段不能藏到其中。status_json 是各自统计完成状态唯一来源，任务表不重复存完成状态。delete_at 不释放任何业务唯一键；软删不自动撤销统计，必须由业务事务处理。

MySQL 不提供 SQLite 式部分索引；due 查询以 consumer/delete_at/runnable_at 定位，租约回收包含软删行。外键列的辅助索引已明确列出，避免把 InnoDB 自动创建的索引漏算。字段范围、隐私白名单、JSON 类型和状态转换仍必须经过类型化 API 校验，DDL 不等于完整协议验证器。

模型 provider_id/model_id 使用 utf8mb4_0900_bin，保持大小写和尾部空格区分；入口拒绝空或带不合法空白的标识。设备和用户自然 ID 的 ASCII 定义与既有 users 外键一致。事件和设备统计不保存 user_id；查询视图关联 installations 的当前有效绑定，用户汇总和缓存仍使用 user_id。

## 表目录

| 表 | 用途 |
| --- | --- |
| telemetry_models | 公开模型身份维度 |
| telemetry_skills | 设备范围的匿名技能身份与公开标签 |
| telemetry_events | 已接收的标准事实；与统计完成分离 |
| telemetry_tasks | 服务端事件统计执行队列；成功后删除 |
| telemetry_harness_metrics | 按设备分 grain 保存的harness统计 |
| telemetry_model_metrics | 按设备分 grain 保存的model统计 |
| telemetry_skill_metrics | 按设备分 grain 保存的skill统计 |
| telemetry_cost_metrics | 按设备分 grain 保存的cost统计 |
| telemetry_bucket_entities | 粒度内会话轮次成员与必要状态；不是永久事件账本 |
| aggregate_dirty_days | 重构已有用户日读模型刷新队列；合并多个事件变更 |

## telemetry_models

公开模型身份维度。

```sql
CREATE TABLE telemetry_models (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 provider_id VARCHAR(96) NOT NULL COMMENT '稳定模型提供商标识；不使用展示别名代替',
 model_id VARCHAR(255) NOT NULL COMMENT '提供商内原始模型标识；大小写敏感，插入后身份不可更改',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_tm_identity (provider_id,model_id),
 CHECK (CHAR_LENGTH(provider_id)>0 AND CHAR_LENGTH(model_id)>0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='公开模型身份维度';
```

## telemetry_skills

设备范围的匿名技能身份与公开标签。

```sql
CREATE TABLE telemetry_skills (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '技能匿名身份所属设备；不同设备的私有 hash 不猜测合并',
 skill_key BINARY(32) NOT NULL COMMENT '设备匿名化后的稳定技能身份',
 public_name VARCHAR(255) NULL COMMENT '经过隐私白名单的展示标签；不参与技能身份或业务内容 hash',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_ts_identity (installation_id,skill_key),
 CONSTRAINT fk_ep_skill_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='设备范围的匿名技能身份与公开标签';
```

## telemetry_events

已接收的标准事实；与统计完成分离。

```sql
CREATE TABLE telemetry_events (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '签名所证明的稳定设备身份；事件幂等键不包含账号',
 event_id BINARY(32) NOT NULL COMMENT '不可变事件版本的业务幂等键',
 fact_key BINARY(32) NOT NULL COMMENT '逻辑事实稳定身份；不是批次或数据库行序号',
 fact_revision BIGINT UNSIGNED NOT NULL COMMENT '来源原生事实版本；普通完成事实固定为 1',
 harness_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '工具策略标识，与标准协议的命名空间一致',
 event_type VARCHAR(48) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '标准事件类型；完整 payload 结构由协议校验',
 schema_version SMALLINT UNSIGNED NOT NULL COMMENT '标准事件结构版本；服务端显式检查兼容性',
 metric_semantics_version SMALLINT UNSIGNED NOT NULL COMMENT '统计公式版本；不与协议结构或软件版本混用',
 content_hash BINARY(32) NOT NULL COMMENT '规范业务 envelope 的 SHA-256；不含服务器时间、状态和认证信息',
 occurred_at BIGINT UNSIGNED NOT NULL COMMENT '选定事件发生时间，UTC 毫秒；用于 UTC+8 分桶和接收时间过滤',
 model_key BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '服务器模型维度外键；初始化 id=1 为 unknown，不沿用客户端数值 ID',
 skill_id BIGINT UNSIGNED NULL COMMENT '服务器技能维度外键；未适用时 NULL',
 session_key BINARY(32) NULL COMMENT '匿名会话关联身份；由设备命名空间限定',
 turn_key BINARY(32) NULL COMMENT '匿名轮次关联身份；未知时 NULL',
 cost_scope_key BINARY(32) NULL COMMENT '经过证明的费用选择范围身份；用于请求与轮次费用关联',
 payload_json JSON NOT NULL COMMENT '冻结业务分组 usage、cost、code、activity、context、meta；规范数字转换后存储',
 status_json JSON NOT NULL COMMENT '服务器 hour、day、month 三个整数状态；0 待办、1 重试、2 在途、3 完成、4 不适用、5 阻塞、6 隔离',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_te_event (installation_id,event_id),
 UNIQUE KEY uk_te_fact (installation_id,fact_key,fact_revision),
 KEY idx_te_user_time (installation_id,delete_at,occurred_at,id),
 KEY idx_te_retention (occurred_at,id),
 KEY idx_te_session (installation_id,harness_id,session_key,occurred_at,id),
 KEY idx_te_cost (installation_id,harness_id,cost_scope_key,occurred_at,id),
 KEY idx_te_model_fk (model_key),
 KEY idx_te_skill_fk (skill_id),
 CONSTRAINT fk_ep_event_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CONSTRAINT fk_ep_event_model FOREIGN KEY (model_key) REFERENCES telemetry_models(id),
 CONSTRAINT fk_ep_event_skill FOREIGN KEY (skill_id) REFERENCES telemetry_skills(id),
 CHECK (fact_revision>0 AND schema_version>0 AND metric_semantics_version>0),
 CHECK (JSON_TYPE(payload_json)='OBJECT' AND JSON_TYPE(status_json)='OBJECT'),
 CHECK (COALESCE(JSON_TYPE(JSON_EXTRACT(payload_json,'$.meta')),'NULL')='OBJECT'),
 CHECK (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.meta.accuracy')),'') IN ('exact','derived','correlated','unknown')),
 CHECK (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.meta.time_source')),'') IN ('source_record','previous_record','file_mtime')),
 CHECK (JSON_CONTAINS_PATH(payload_json,'one','$.usage')=0 OR (JSON_TYPE(JSON_EXTRACT(payload_json,'$.usage'))='OBJECT' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.meta.accuracy')) IN ('exact','derived'))),
 CHECK (COALESCE(JSON_TYPE(JSON_EXTRACT(status_json,'$.hour')),'NULL')='INTEGER' AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json,'$.hour')) AS SIGNED) BETWEEN 0 AND 6),
 CHECK (COALESCE(JSON_TYPE(JSON_EXTRACT(status_json,'$.day')),'NULL')='INTEGER' AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json,'$.day')) AS SIGNED) BETWEEN 0 AND 6),
 CHECK (COALESCE(JSON_TYPE(JSON_EXTRACT(status_json,'$.month')),'NULL')='INTEGER' AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json,'$.month')) AS SIGNED) BETWEEN 0 AND 6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='已接收的标准事实；与统计完成分离';
```

## telemetry_tasks

服务端事件统计执行队列；成功后删除。

```sql
CREATE TABLE telemetry_tasks (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 event_row_id BIGINT UNSIGNED NOT NULL COMMENT '服务器 telemetry_events.id 外键；不是 wire event_id',
 consumer VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '统计消费者：hour、day 或 month',
 runnable_at BIGINT UNSIGNED NULL COMMENT '下次可执行 UTC 毫秒时间；在途、阻塞或隔离时 NULL',
 lease_token BINARY(16) NULL COMMENT '每次领取的随机 128 位租约令牌；提交和续租必须匹配',
 lease_until BIGINT UNSIGNED NULL COMMENT '租约截止 UTC 毫秒时间；无租约时 NULL',
 attempt_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '实际开始执行次数；不等于事件修订号',
 last_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '最近失败或阻塞的机器错误码；不存敏感原始异常',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_tt_consumer (event_row_id,consumer),
 KEY idx_tt_due (consumer,delete_at,runnable_at,id),
 KEY idx_tt_lease (consumer,lease_until,id),
 CONSTRAINT fk_ep_task_event FOREIGN KEY (event_row_id) REFERENCES telemetry_events(id) ON DELETE CASCADE,
 CHECK (consumer IN ('hour','day','month')),
 CHECK ((lease_token IS NULL AND lease_until IS NULL) OR (lease_token IS NOT NULL AND lease_until IS NOT NULL AND runnable_at IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='服务端事件统计执行队列；成功后删除';
```

## telemetry_harness_metrics

按设备分 grain 保存的harness统计。

```sql
CREATE TABLE telemetry_harness_metrics (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '统计贡献所属设备；用于多设备汇总和按设备删除贡献',
 grain VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '时间粒度：hour 小时、day 自然日、month 自然月',
 bucket_start BIGINT UNSIGNED NOT NULL COMMENT '北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶',
 harness_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '工具策略标识，如 codex、opencode、zcode',
 session_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '本粒度桶内去重主会话数，不能跨日直接相加当作月去重数',
 child_session_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '本粒度桶内去重子会话数',
 interaction_turn_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '本粒度桶内去重交互轮次数',
 turn_started_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '轮次开始次数，与完成次数分别统计',
 turn_completed_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '轮次完成次数',
 user_turn_started_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '由用户触发的轮次开始次数，作为用户消息数',
 tool_call_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '工具调用次数，按稳定调用身份去重',
 skill_use_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '技能使用次数',
 code_generated_lines BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '明确生成代码行数累计值',
 code_accepted_lines BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '明确被接受代码行数累计值',
 code_added_lines BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '代码变更新增行数累计值',
 code_removed_lines BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '代码变更删除行数累计值',
 code_file_touch_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '文件触达次数累计值，不是去重文件数',
 correlated_code_lines BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '仅通过关联归因得到的代码行数，单独保留口径',
 active_duration_ms BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '规范化活动时长贡献，毫秒；会话权威值替换同日轮次兜底',
 code_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '代码数据明确已知的样本数；已知为零也计入，未知不计入',
 duration_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '耗时明确已知的样本数；已知为零也计入，未知不计入',
 message_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '消息数据明确已知的样本数；已知为零也计入，未知不计入',
 metric_semantics_version SMALLINT UNSIGNED NOT NULL COMMENT '统计含义与公式版本，正整数',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_thm_bucket (installation_id,grain,bucket_start,harness_id),
 KEY idx_thm_device (installation_id,id),
 CONSTRAINT fk_thm_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CHECK (grain IN ('hour','day','month')),
 CHECK (metric_semantics_version>0),
 KEY idx_thm_harness (installation_id,grain,harness_id,bucket_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='按设备分 grain 保存的harness统计';
```

## telemetry_model_metrics

按设备分 grain 保存的model统计。

```sql
CREATE TABLE telemetry_model_metrics (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '统计贡献所属设备；用于多设备汇总和按设备删除贡献',
 grain VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '时间粒度：hour 小时、day 自然日、month 自然月',
 bucket_start BIGINT UNSIGNED NOT NULL COMMENT '北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶',
 harness_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '工具策略标识，如 codex、opencode、zcode',
 model_key BIGINT UNSIGNED NOT NULL COMMENT '服务器模型维度外键；unknown 为初始化行 id=1',
 exact_token_total DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '原始明确总 Token 的累计值，不含估算用量',
 derived_token_total DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '确定性推导总 Token 的累计值，不含猜测值',
 input_context_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '完整输入上下文 Token 数，包含命中缓存的输入',
 input_uncached_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '未命中缓存的输入 Token 数',
 output_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '完整输出 Token 数；可能已包含 reasoning_tokens',
 cache_read_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '缓存读取 Token 数，是完整输入的组成部分，不重复加总',
 cache_write_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '缓存写入 Token 数；不能未经确认再加到总量',
 reasoning_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '推理 Token 数；可能为输出的子集，不重复加总',
 tool_extra_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '已证明没有计入其他组成的额外工具 Token 数',
 model_request_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '模型请求次数，不等同于轮次、用户消息或事件条数',
 usage_observed_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '观测到的有效用量样本数，用于计算字段覆盖',
 token_total_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '总 Token明确已知的样本数；已知为零也计入，未知不计入',
 input_context_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '完整输入明确已知的样本数；已知为零也计入，未知不计入',
 input_uncached_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '未缓存输入明确已知的样本数；已知为零也计入，未知不计入',
 output_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '输出明确已知的样本数；已知为零也计入，未知不计入',
 cache_read_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '缓存读取明确已知的样本数；已知为零也计入，未知不计入',
 cache_write_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '缓存写入明确已知的样本数；已知为零也计入，未知不计入',
 reasoning_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '推理 Token明确已知的样本数；已知为零也计入，未知不计入',
 tool_extra_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '额外工具 Token明确已知的样本数；已知为零也计入，未知不计入',
 cache_eligible_input_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '输入和缓存读取均已知的同一请求集合之输入 Token 总和',
 cache_eligible_read_tokens DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '与 cache_eligible_input_tokens 同一集合的缓存读取 Token 总和',
 cache_pair_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '完整输入和缓存读取同时已知的请求样本数',
 metric_semantics_version SMALLINT UNSIGNED NOT NULL COMMENT '统计含义与公式版本，正整数',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_tmm_bucket (installation_id,grain,bucket_start,harness_id,model_key),
 KEY idx_tmm_device (installation_id,id),
 CONSTRAINT fk_tmm_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CHECK (grain IN ('hour','day','month')),
 CHECK (metric_semantics_version>0),
 KEY idx_tmm_model_fk (model_key),
 CONSTRAINT fk_tmm_model FOREIGN KEY (model_key) REFERENCES telemetry_models(id),
 KEY idx_tmm_harness (installation_id,grain,harness_id,bucket_start,model_key),
 KEY idx_tmm_model_filter (installation_id,grain,model_key,bucket_start,harness_id),
 CHECK (cache_eligible_read_tokens<=cache_eligible_input_tokens),
 CHECK (token_total_known_count<=usage_observed_count),
 CHECK (input_context_known_count<=usage_observed_count),
 CHECK (input_uncached_known_count<=usage_observed_count),
 CHECK (output_known_count<=usage_observed_count),
 CHECK (cache_read_known_count<=usage_observed_count),
 CHECK (cache_write_known_count<=usage_observed_count),
 CHECK (reasoning_known_count<=usage_observed_count),
 CHECK (tool_extra_known_count<=usage_observed_count),
 CHECK (cache_pair_known_count<=usage_observed_count),
 CHECK (exact_token_total>=0),
 CHECK (derived_token_total>=0),
 CHECK (input_context_tokens>=0),
 CHECK (input_uncached_tokens>=0),
 CHECK (output_tokens>=0),
 CHECK (cache_read_tokens>=0),
 CHECK (cache_write_tokens>=0),
 CHECK (reasoning_tokens>=0),
 CHECK (tool_extra_tokens>=0),
 CHECK (cache_eligible_input_tokens>=0),
 CHECK (cache_eligible_read_tokens>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='按设备分 grain 保存的model统计';
```

## telemetry_skill_metrics

按设备分 grain 保存的skill统计。

```sql
CREATE TABLE telemetry_skill_metrics (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '统计贡献所属设备；用于多设备汇总和按设备删除贡献',
 grain VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '时间粒度：hour 小时、day 自然日、month 自然月',
 bucket_start BIGINT UNSIGNED NOT NULL COMMENT '北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶',
 harness_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '工具策略标识，如 codex、opencode、zcode',
 skill_id BIGINT UNSIGNED NOT NULL COMMENT '服务器设备内技能维度外键',
 use_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '技能使用总次数，不包含 estimated 调用',
 exact_use_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '原始明确记录的技能使用次数',
 derived_use_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '确定性推导的技能使用次数',
 correlated_use_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '关联归因的技能使用次数',
 success_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '结果明确成功的次数',
 failure_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '结果明确失败的次数；未知结果不计入',
 duration_ms BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '执行耗时，单位毫秒；事件中未知为 NULL，汇总表为已知耗时之和',
 duration_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '耗时明确已知的样本数；已知为零也计入，未知不计入',
 metric_semantics_version SMALLINT UNSIGNED NOT NULL COMMENT '统计含义与公式版本，正整数',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_tsm_bucket (installation_id,grain,bucket_start,harness_id,skill_id),
 KEY idx_tsm_device (installation_id,id),
 CONSTRAINT fk_tsm_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CHECK (grain IN ('hour','day','month')),
 CHECK (metric_semantics_version>0),
 KEY idx_tsm_skill_fk (skill_id),
 CONSTRAINT fk_tsm_skill FOREIGN KEY (skill_id) REFERENCES telemetry_skills(id),
 CHECK (success_count+failure_count<=use_count),
 CHECK (exact_use_count+derived_use_count+correlated_use_count<=use_count),
 CHECK (duration_known_count<=use_count)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='按设备分 grain 保存的skill统计';
```

## telemetry_cost_metrics

按设备分 grain 保存的cost统计。

```sql
CREATE TABLE telemetry_cost_metrics (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '统计贡献所属设备；用于多设备汇总和按设备删除贡献',
 grain VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '时间粒度：hour 小时、day 自然日、month 自然月',
 bucket_start BIGINT UNSIGNED NOT NULL COMMENT '北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶',
 harness_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '工具策略标识，如 codex、opencode、zcode',
 model_key BIGINT UNSIGNED NOT NULL COMMENT '服务器模型维度外键；unknown 为初始化行 id=1',
 currency CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '三个大写 ASCII 字母币种代码；不同币种分别累计',
 reported_cost_units DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '生效的提供商报告金额累计值，单位为币种的 10^-8',
 estimated_cost_units DECIMAL(38,0) NOT NULL DEFAULT 0 COMMENT '生效的价格表计算金额累计值，单位为币种的 10^-8；不代表实际账单',
 reported_request_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '生效费用来源为提供商报告的请求数',
 estimated_request_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '生效费用来源为价格表计算的请求数',
 unpriced_request_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '缺少可用价格、尚未定价的请求数，不伪装为已知零费用',
 cost_known_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '费用明确已知的样本数；已知为零也计入，未知不计入',
 metric_semantics_version SMALLINT UNSIGNED NOT NULL COMMENT '统计含义与公式版本，正整数',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_tcm_bucket (installation_id,grain,bucket_start,harness_id,model_key,currency),
 KEY idx_tcm_device (installation_id,id),
 CONSTRAINT fk_tcm_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CHECK (grain IN ('hour','day','month')),
 CHECK (metric_semantics_version>0),
 KEY idx_tcm_model_fk (model_key),
 CONSTRAINT fk_tcm_model FOREIGN KEY (model_key) REFERENCES telemetry_models(id),
 CHECK (REGEXP_LIKE(currency,'^[A-Z]{3}$','c')),
 CHECK (reported_cost_units>=0),
 CHECK (estimated_cost_units>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='按设备分 grain 保存的cost统计';
```

## telemetry_bucket_entities

粒度内会话轮次成员与必要状态；不是永久事件账本。

```sql
CREATE TABLE telemetry_bucket_entities (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 installation_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '实体匿名命名空间所属设备',
 grain VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '时间粒度：hour 小时、day 自然日、month 自然月',
 bucket_start BIGINT UNSIGNED NOT NULL COMMENT '北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶',
 harness_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '工具策略标识，如 codex、opencode、zcode',
 entity_kind VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '需要去重的实体类型：session 会话、turn 轮次',
 entity_key BINARY(32) NOT NULL COMMENT '实体的 32 字节匿名身份，在同一统计桶内唯一',
 parent_key BINARY(32) NULL COMMENT '父实体的 32 字节匿名身份；无父关联时 NULL',
 has_started TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '是否观察到开始：1 是、0 否',
 has_completed TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '是否观察到完成：1 是、0 否',
 has_user_start TINYINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '是否观察到用户触发的开始；为 1 时 has_started 必须为 1',
 session_duration_ms BIGINT UNSIGNED NULL COMMENT '权威会话耗时，毫秒；未知为 NULL',
 turn_duration_ms BIGINT UNSIGNED NULL COMMENT '轮次耗时兜底，毫秒；未知为 NULL',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_tbe_entity (installation_id,grain,bucket_start,harness_id,entity_kind,entity_key),
 KEY idx_tbe_related (installation_id,harness_id,entity_kind,entity_key,grain,bucket_start),
 KEY idx_tbe_parent (installation_id,grain,bucket_start,harness_id,parent_key),
 CONSTRAINT fk_tbe_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id),
 CHECK (grain IN ('hour','day','month')),
 CHECK (entity_kind IN ('session','turn')),
 CHECK (has_started IN (0,1) AND has_completed IN (0,1) AND has_user_start IN (0,1) AND has_user_start<=has_started)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='粒度内会话轮次成员与必要状态；不是永久事件账本';
```

## aggregate_dirty_days

重构已有用户日读模型刷新队列；合并多个事件变更。

```sql
CREATE TABLE aggregate_dirty_days (
 id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT COMMENT '本表代理主键；业务身份由独立唯一键保证，不作为同步水位',
 created_at BIGINT UNSIGNED NOT NULL COMMENT '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新',
 updated_at BIGINT UNSIGNED NOT NULL COMMENT '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护',
 delete_at BIGINT UNSIGNED NULL COMMENT '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键',
 extra JSON NOT NULL DEFAULT (JSON_OBJECT()) COMMENT '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文',
 user_id CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '需要刷新派生读模型的账号',
 metric_date DATE NOT NULL COMMENT '受影响的北京时间业务日期',
 dirty_version BIGINT UNSIGNED NOT NULL DEFAULT 1 COMMENT '该账号日期的累计刷新请求版本；不是事实修订或统计桶代次',
 applied_version BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '已发布刷新结果所覆盖的领取版本；不能盲目追平最新 dirty_version',
 claim_token BINARY(16) NULL COMMENT '本次刷新领取令牌；完成和续租必须匹配',
 lease_expires_at BIGINT UNSIGNED NULL COMMENT '刷新租约截止时间，UTC 毫秒；未领取为 NULL',
 attempt_count BIGINT UNSIGNED NOT NULL DEFAULT 0 COMMENT '刷新实际尝试次数',
 next_attempt_at BIGINT UNSIGNED NULL COMMENT '下次刷新时间，UTC 毫秒；完成或在途为 NULL',
 last_error_code VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '最近读模型刷新失败原因码；不存敏感异常正文',
 PRIMARY KEY (id),
 CHECK (JSON_TYPE(extra)='OBJECT'),
 UNIQUE KEY uk_add_user_day (user_id,metric_date),
 KEY idx_add_due (delete_at,next_attempt_at,id),
 KEY idx_add_lease (lease_expires_at,id),
 CONSTRAINT fk_ep_dirty_user FOREIGN KEY (user_id) REFERENCES users(user_id),
 CHECK (applied_version<=dirty_version),
 CHECK ((claim_token IS NULL AND lease_expires_at IS NULL) OR (claim_token IS NOT NULL AND lease_expires_at IS NOT NULL AND next_attempt_at IS NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='重构已有用户日读模型刷新队列；合并多个事件变更';
```

## 当前绑定的用户统计视图

视图不复制数据、不增加实体表；解绑立即排除，重新绑定计入新账号。

```sql
CREATE OR REPLACE VIEW bound_telemetry_harness_metrics AS SELECT m.*,i.user_id FROM telemetry_harness_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;
CREATE OR REPLACE VIEW bound_telemetry_model_metrics AS SELECT m.*,i.user_id FROM telemetry_model_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;
CREATE OR REPLACE VIEW bound_telemetry_skill_metrics AS SELECT m.*,i.user_id FROM telemetry_skill_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;
CREATE OR REPLACE VIEW bound_telemetry_cost_metrics AS SELECT m.*,i.user_id FROM telemetry_cost_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;
```

## 空表初始化要求

```sql
-- Only in a newly created telemetry_models table; id 0 is not used with MySQL AUTO_INCREMENT.
INSERT INTO telemetry_models(id,created_at,updated_at,provider_id,model_id)
VALUES(1,CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS UNSIGNED),
         CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS UNSIGNED),'unknown','unknown');
```

unknown 必须在任何事件写入之前初始化。先停止旧接收、聚合和缓存发布工作器，再按外键依赖清理旧统计业务数据并创建空目标表；不复制旧明细、日快照、统计或任务。installations 的身份、主键、注册时间沿用现表，账号和设备数据不属于本次统计重置范围。初始化完成后重复启动不得再清库。

aggregate_dirty_days 按目标结构空表重建，不迁移旧 claim_token、租约、版本或时间字段；旧消费者同时停用。新任务领取时把 next_attempt_at 置 NULL，执行期间新变更只增加 dirty_version；完成只确认领取版本 v，若 dirty_version>v 则重排，不能混用旧 ClearAggregateDirtyDaysTx。

服务器事件不直接照搬客户端 created_at+14 天硬 TTL。服务端保留事件身份及统计完成状态，保证任意历史重建仍能去重；不能按 occurred_at 窗口清掉已计数身份。模型/技能软删标签不能让历史外键失效；物理删除账号/设备数据遵循依赖顺序和服务端授权归属。

## 本轮验证

生成器结构检查：10 张表、178 个有 COMMENT 的字段、每表五个公共字段及 id 主键、索引字段存在、显式外键名称不冲突，且不存在逐设备协议切换字段。与本地四个统计主题共享字段来源，防止两端漏列。该检查不执行 MySQL SQL，不验证锁、优化器、真实外键或性能。

本轮另在云端隔离测试 schema 验证了实际迁移、并发幂等、历史重建和解绑/重新绑定。此生成器自身仍仅做结构检查，不能代替真实数据库测试。
