-- Local SQLite design only; not an application migration.
PRAGMA foreign_keys=ON;

CREATE TABLE collection_sources (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 source_key BLOB NOT NULL CHECK(length(source_key)=32), -- 稳定逻辑来源的 32 字节匿名摘要，不随扫描批次变化
 source_kind TEXT NOT NULL CHECK(source_kind IN ('jsonl','sqlite','other')), -- 来源格式：jsonl、sqlite 或 other
 locator_ref TEXT NOT NULL CHECK(length(locator_ref)>0), -- 仅本机使用的数据源定位引用，不上传
 enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)), -- 是否启用采集：1 启用、0 停用
 stream_key TEXT NOT NULL CHECK(length(stream_key)>0), -- 来源内独立读取流标识；JSONL 使用 main，SQLite 使用稳定查询流名称
 cursor_kind TEXT NOT NULL CHECK(cursor_kind IN ('byte_offset','sqlite_change','opaque')), -- 游标类型：byte_offset 字节位置、sqlite_change 变更状态、opaque 策略自定义
 cursor_json TEXT NOT NULL CHECK(json_valid(cursor_json)) CHECK(json_type(cursor_json)='object'), -- 带类型的读取游标 JSON 对象；可变行来源还需保存复查边界
 decoder_state_version INTEGER NOT NULL CHECK(decoder_state_version>0), -- 解析状态格式版本，正整数
 decoder_state_json TEXT NOT NULL CHECK(json_valid(decoder_state_json)) CHECK(json_type(decoder_state_json)='object'), -- 必要计数基线和解析状态的 JSON 对象，不保存聊天正文
 observed_boundary_json TEXT NOT NULL CHECK(json_valid(observed_boundary_json)) CHECK(json_type(observed_boundary_json)='object'), -- 本次采集观察到的源边界 JSON 对象，用于校验文件或数据库变化
 commit_seq INTEGER NOT NULL DEFAULT 0 CHECK(commit_seq>=0), -- 已提交游标的单调版本，CAS 比较使用，不是源记录 ID
 next_poll_at INTEGER, -- 下一次允许采集的 UTC 毫秒时间；NULL 表示不进入到期扫描
 lease_token TEXT CHECK(length(lease_token)>0), -- 本次领取的唯一随机租约令牌；提交必须匹配，未领取时 NULL
 lease_until INTEGER, -- 租约到期 UTC 毫秒时间；无租约时 NULL
 last_error_code TEXT, -- 最近一次失败的机器错误码；不保存含隐私的原始异常文本
 ignored_record_count INTEGER NOT NULL DEFAULT 0 CHECK(ignored_record_count>=0), -- 本采集流累计忽略的记录数；与游标同事务更新，不保存被忽略事件正文
 last_ignored_code TEXT, -- 本采集流最近一次忽略原因码，如 missing_event_time 或 outside_admission_day
 expired_incomplete_event_count INTEGER NOT NULL DEFAULT 0 CHECK(expired_incomplete_event_count>=0), -- 本采集流到期时仍有任务未完成的事件累计数；每个事件只计一次，不伪造 ACK
 UNIQUE(harness_id,source_key,stream_key),
 UNIQUE(id,harness_id),
 CHECK((lease_token IS NULL AND lease_until IS NULL)
    OR (lease_token IS NOT NULL AND lease_until IS NOT NULL AND next_poll_at IS NULL))
) STRICT;
CREATE INDEX idx_sources_due ON collection_sources(harness_id,next_poll_at,id)
 WHERE delete_at IS NULL AND enabled=1 AND next_poll_at IS NOT NULL;
CREATE INDEX idx_sources_lease ON collection_sources(lease_until,id)
 WHERE lease_until IS NOT NULL;

CREATE TABLE model_dimensions (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 provider_id TEXT NOT NULL CHECK(length(provider_id)>0), -- 模型提供商标识，和 model_id 共同唯一
 model_id TEXT NOT NULL CHECK(length(model_id)>0), -- 提供商内原始模型标识；保留大小写，不用展示名称替代
 UNIQUE(provider_id,model_id)
) STRICT;
INSERT INTO model_dimensions(id,created_at,updated_at,provider_id,model_id)
SELECT 0,CAST(strftime('%s','now') AS INTEGER)*1000,CAST(strftime('%s','now') AS INTEGER)*1000,'unknown','unknown';

CREATE TABLE skill_dimensions (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 skill_key BLOB NOT NULL UNIQUE CHECK(length(skill_key)=32), -- 技能稳定身份的 32 字节匿名摘要
 public_name TEXT -- 允许展示的技能标签；未知或私有时 NULL，不参与身份判重
) STRICT;

CREATE TABLE events (
 id INTEGER PRIMARY KEY AUTOINCREMENT, -- 本地行主键；不复用已删除行号，业务幂等使用 event_id
 created_at INTEGER NOT NULL CHECK(created_at BETWEEN 0 AND 9223372035645175807), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 event_id BLOB NOT NULL UNIQUE CHECK(length(event_id)=32), -- 不可变事件版本的 32 字节上传幂等键
 fact_key BLOB NOT NULL CHECK(length(fact_key)=32), -- 同一逻辑事实的稳定 32 字节身份
 fact_revision INTEGER NOT NULL CHECK(fact_revision>0), -- 事实修订号；普通完成明细固定为 1
 collection_source_id INTEGER NOT NULL, -- 本地采集流外键，和 harness_id 一起校验来源一致性
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 event_type TEXT NOT NULL CHECK(length(event_type)>0), -- 标准事件类型；决定 payload 的结构与业务校验规则
 schema_version INTEGER NOT NULL CHECK(schema_version>0), -- 标准事件协议结构版本
 metric_semantics_version INTEGER NOT NULL CHECK(metric_semantics_version>0), -- 统计公式与含义版本
 content_hash BLOB NOT NULL CHECK(length(content_hash)=32), -- 冻结事件头与 payload 的内容摘要；不包含本地状态、公共管理字段或认证信息
 occurred_at INTEGER NOT NULL CHECK(occurred_at>=0), -- 选定事件发生时间，UTC 毫秒；用于北京时间分桶，时间依据放 payload_json.meta.time_source
 expire_at INTEGER GENERATED ALWAYS AS (created_at+1209600000) STORED, -- 首次 created_at 加 14 天；到期物理清理，软删除和状态更新均不延长
 model_key INTEGER NOT NULL DEFAULT 0 REFERENCES model_dimensions(id), -- 本地模型外键；0 为 unknown，不把本地数字 ID 当作跨端模型身份
 skill_id INTEGER REFERENCES skill_dimensions(id), -- 本地技能外键；不适用或未知时 NULL
 session_key BLOB CHECK(length(session_key)=32), -- 稳定匿名会话身份；未知时 NULL
 turn_key BLOB CHECK(length(turn_key)=32), -- 稳定匿名轮次身份；未知时 NULL
 cost_scope_key BLOB CHECK(length(cost_scope_key)=32), -- 稳定匿名计费范围身份，用于请求或轮次费用关联
 payload_json TEXT NOT NULL CHECK(json_valid(payload_json)) CHECK(json_type(payload_json)='object'), -- 冻结业务 JSON：usage、cost、code、activity、context、meta；只保存适用分组，不复制整份 envelope
 status_json TEXT NOT NULL DEFAULT '{"hour":0,"day":0,"month":0,"upload":0}' CHECK(json_valid(status_json)) CHECK(json_type(status_json)='object'), -- 四个必填整数状态：0 待处理、1 重试、2 处理中、3 完成、4 不适用、5 阻塞、6 隔离
 FOREIGN KEY(collection_source_id,harness_id) REFERENCES collection_sources(id,harness_id),
 UNIQUE(fact_key,fact_revision),
 CHECK(json_type(status_json,'$.hour') IS 'integer' AND json_extract(status_json,'$.hour') BETWEEN 0 AND 6),
 CHECK(json_type(status_json,'$.day') IS 'integer' AND json_extract(status_json,'$.day') BETWEEN 0 AND 6),
 CHECK(json_type(status_json,'$.month') IS 'integer' AND json_extract(status_json,'$.month') BETWEEN 0 AND 6),
 CHECK(json_type(status_json,'$.upload') IS 'integer' AND json_extract(status_json,'$.upload') BETWEEN 0 AND 6),
 CHECK(json_type(payload_json,'$.meta') IS 'object'),
 CHECK(json_type(payload_json,'$.meta.accuracy') IS 'text' AND json_extract(payload_json,'$.meta.accuracy') IN ('exact','derived','correlated','unknown')),
 CHECK(json_type(payload_json,'$.meta.time_source') IS 'text' AND json_extract(payload_json,'$.meta.time_source') IN ('source_record','previous_record','file_mtime')),
 CHECK(json_type(payload_json,'$.usage') IS NULL OR (json_type(payload_json,'$.usage') IS 'object' AND json_extract(payload_json,'$.meta.accuracy') IN ('exact','derived'))),
 CHECK(json_type(payload_json,'$.cost') IS NULL OR json_type(payload_json,'$.cost') IS 'object'),
 CHECK(json_type(payload_json,'$.code') IS NULL OR json_type(payload_json,'$.code') IS 'object'),
 CHECK(json_type(payload_json,'$.activity') IS NULL OR json_type(payload_json,'$.activity') IS 'object'),
 CHECK(json_type(payload_json,'$.context') IS NULL OR json_type(payload_json,'$.context') IS 'object'),
 CHECK(COALESCE(json_type(payload_json,'$.usage.token_total'),'null')='null' OR (json_type(payload_json,'$.usage.token_total') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.token_total'))='integer' AND json_extract(payload_json,'$.usage.token_total')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.input_context_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.input_context_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.input_context_tokens'))='integer' AND json_extract(payload_json,'$.usage.input_context_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.input_uncached_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.input_uncached_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.input_uncached_tokens'))='integer' AND json_extract(payload_json,'$.usage.input_uncached_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.output_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.output_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.output_tokens'))='integer' AND json_extract(payload_json,'$.usage.output_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.cache_read_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.cache_read_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.cache_read_tokens'))='integer' AND json_extract(payload_json,'$.usage.cache_read_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.cache_write_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.cache_write_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.cache_write_tokens'))='integer' AND json_extract(payload_json,'$.usage.cache_write_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.reasoning_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.reasoning_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.reasoning_tokens'))='integer' AND json_extract(payload_json,'$.usage.reasoning_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.usage.tool_extra_tokens'),'null')='null' OR (json_type(payload_json,'$.usage.tool_extra_tokens') IS 'integer' AND typeof(json_extract(payload_json,'$.usage.tool_extra_tokens'))='integer' AND json_extract(payload_json,'$.usage.tool_extra_tokens')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.code.generated_lines'),'null')='null' OR (json_type(payload_json,'$.code.generated_lines') IS 'integer' AND typeof(json_extract(payload_json,'$.code.generated_lines'))='integer' AND json_extract(payload_json,'$.code.generated_lines')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.code.accepted_lines'),'null')='null' OR (json_type(payload_json,'$.code.accepted_lines') IS 'integer' AND typeof(json_extract(payload_json,'$.code.accepted_lines'))='integer' AND json_extract(payload_json,'$.code.accepted_lines')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.code.added_lines'),'null')='null' OR (json_type(payload_json,'$.code.added_lines') IS 'integer' AND typeof(json_extract(payload_json,'$.code.added_lines'))='integer' AND json_extract(payload_json,'$.code.added_lines')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.code.removed_lines'),'null')='null' OR (json_type(payload_json,'$.code.removed_lines') IS 'integer' AND typeof(json_extract(payload_json,'$.code.removed_lines'))='integer' AND json_extract(payload_json,'$.code.removed_lines')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.code.file_count'),'null')='null' OR (json_type(payload_json,'$.code.file_count') IS 'integer' AND typeof(json_extract(payload_json,'$.code.file_count'))='integer' AND json_extract(payload_json,'$.code.file_count')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.activity.duration_ms'),'null')='null' OR (json_type(payload_json,'$.activity.duration_ms') IS 'integer' AND typeof(json_extract(payload_json,'$.activity.duration_ms'))='integer' AND json_extract(payload_json,'$.activity.duration_ms')>=0)),
 CHECK(COALESCE(json_type(payload_json,'$.cost.units'),'null')='null' OR (json_type(payload_json,'$.cost.units') IS 'integer' AND typeof(json_extract(payload_json,'$.cost.units'))='integer' AND json_extract(payload_json,'$.cost.units')>=0)),
 CHECK(json_extract(payload_json,'$.usage.cache_read_tokens') IS NULL OR json_extract(payload_json,'$.usage.input_context_tokens') IS NULL OR json_extract(payload_json,'$.usage.cache_read_tokens')<=json_extract(payload_json,'$.usage.input_context_tokens')),
 CHECK(COALESCE(json_type(payload_json,'$.activity.success'),'null')='null' OR json_type(payload_json,'$.activity.success') IN ('true','false')),
 CHECK(json_extract(payload_json,'$.cost.units') IS NULL OR (
   json_type(payload_json,'$.cost.currency') IS 'text' AND length(json_extract(payload_json,'$.cost.currency'))=3 AND json_extract(payload_json,'$.cost.currency') NOT GLOB '*[^A-Z]*'
   AND json_type(payload_json,'$.cost.source') IS 'text' AND json_extract(payload_json,'$.cost.source') IN ('provider_reported','estimated_price_table')))
) STRICT;
CREATE INDEX idx_events_expire ON events(expire_at,id);
CREATE INDEX idx_events_time ON events(occurred_at DESC,id DESC) WHERE delete_at IS NULL;
CREATE INDEX idx_events_harness_time ON events(harness_id,occurred_at DESC,id DESC) WHERE delete_at IS NULL;
CREATE INDEX idx_events_session_scope ON events(harness_id,session_key,occurred_at,id)
 WHERE delete_at IS NULL AND session_key IS NOT NULL;
CREATE INDEX idx_events_cost_scope ON events(harness_id,cost_scope_key,occurred_at,id)
 WHERE delete_at IS NULL AND cost_scope_key IS NOT NULL;

CREATE TABLE processing_tasks (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 event_row_id INTEGER NOT NULL, -- 本地事件行外键，引用 events.id；不是业务 event_id
 consumer TEXT NOT NULL CHECK(consumer IN ('hour','day','month','upload')), -- 消费任务类型，具体允许值由 CHECK 限定；不代表 harness
 runnable_at INTEGER, -- 任务下次允许执行的 UTC 毫秒时间；领取、阻塞或隔离时 NULL
 lease_token TEXT CHECK(length(lease_token)>0), -- 本次领取的唯一随机租约令牌；提交必须匹配，未领取时 NULL
 lease_until INTEGER, -- 租约到期 UTC 毫秒时间；无租约时 NULL
 attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count>=0), -- 已发起执行次数，非负整数
 last_error_code TEXT, -- 最近一次失败的机器错误码；不保存含隐私的原始异常文本
 UNIQUE(event_row_id,consumer),
 CHECK((lease_token IS NULL AND lease_until IS NULL) OR
       (lease_token IS NOT NULL AND lease_until IS NOT NULL AND runnable_at IS NULL)),
 FOREIGN KEY(event_row_id) REFERENCES events(id) ON DELETE CASCADE
) STRICT;
CREATE INDEX idx_tasks_due ON processing_tasks(consumer,runnable_at,event_row_id)
 WHERE delete_at IS NULL AND runnable_at IS NOT NULL;
CREATE INDEX idx_tasks_lease ON processing_tasks(consumer,lease_until,event_row_id)
 WHERE lease_until IS NOT NULL;

CREATE TABLE harness_metrics (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 时间粒度：hour 小时、day 自然日、month 自然月
 bucket_start INTEGER NOT NULL, -- 北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 session_count INTEGER NOT NULL DEFAULT 0 CHECK(session_count>=0), -- 本粒度桶内去重主会话数，不能跨日直接相加当作月去重数
 child_session_count INTEGER NOT NULL DEFAULT 0 CHECK(child_session_count>=0), -- 本粒度桶内去重子会话数
 interaction_turn_count INTEGER NOT NULL DEFAULT 0 CHECK(interaction_turn_count>=0), -- 本粒度桶内去重交互轮次数
 turn_started_count INTEGER NOT NULL DEFAULT 0 CHECK(turn_started_count>=0), -- 轮次开始次数，与完成次数分别统计
 turn_completed_count INTEGER NOT NULL DEFAULT 0 CHECK(turn_completed_count>=0), -- 轮次完成次数
 user_turn_started_count INTEGER NOT NULL DEFAULT 0 CHECK(user_turn_started_count>=0), -- 由用户触发的轮次开始次数，作为用户消息数
 tool_call_count INTEGER NOT NULL DEFAULT 0 CHECK(tool_call_count>=0), -- 工具调用次数，按稳定调用身份去重
 skill_use_count INTEGER NOT NULL DEFAULT 0 CHECK(skill_use_count>=0), -- 技能使用次数
 code_generated_lines INTEGER NOT NULL DEFAULT 0 CHECK(code_generated_lines>=0), -- 明确生成代码行数累计值
 code_accepted_lines INTEGER NOT NULL DEFAULT 0 CHECK(code_accepted_lines>=0), -- 明确被接受代码行数累计值
 code_added_lines INTEGER NOT NULL DEFAULT 0 CHECK(code_added_lines>=0), -- 代码变更新增行数累计值
 code_removed_lines INTEGER NOT NULL DEFAULT 0 CHECK(code_removed_lines>=0), -- 代码变更删除行数累计值
 code_file_touch_count INTEGER NOT NULL DEFAULT 0 CHECK(code_file_touch_count>=0), -- 文件触达次数累计值，不是去重文件数
 correlated_code_lines INTEGER NOT NULL DEFAULT 0 CHECK(correlated_code_lines>=0), -- 仅通过关联归因得到的代码行数，单独保留口径
 active_duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(active_duration_ms>=0), -- 规范化活动时长贡献，毫秒；会话权威值替换同日轮次兜底
 code_known_count INTEGER NOT NULL DEFAULT 0 CHECK(code_known_count>=0), -- 代码数据明确已知的样本数；已知为零也计入，未知不计入
 duration_known_count INTEGER NOT NULL DEFAULT 0 CHECK(duration_known_count>=0), -- 耗时明确已知的样本数；已知为零也计入，未知不计入
 message_known_count INTEGER NOT NULL DEFAULT 0 CHECK(message_known_count>=0), -- 消息数据明确已知的样本数；已知为零也计入，未知不计入
 metric_semantics_version INTEGER NOT NULL CHECK(metric_semantics_version>0), -- 统计含义与公式版本，正整数
 UNIQUE(grain,bucket_start,harness_id)
) STRICT;
CREATE INDEX idx_harness_filter ON harness_metrics(grain,harness_id,bucket_start) WHERE delete_at IS NULL;

CREATE TABLE model_metrics (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 时间粒度：hour 小时、day 自然日、month 自然月
 bucket_start INTEGER NOT NULL, -- 北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 model_key INTEGER NOT NULL REFERENCES model_dimensions(id), -- 模型维度外键，引用 model_dimensions.id；0 表示未知模型
 exact_token_total INTEGER NOT NULL DEFAULT 0 CHECK(exact_token_total>=0), -- 原始明确总 Token 的累计值，不含估算用量
 derived_token_total INTEGER NOT NULL DEFAULT 0 CHECK(derived_token_total>=0), -- 确定性推导总 Token 的累计值，不含猜测值
 input_context_tokens INTEGER NOT NULL DEFAULT 0 CHECK(input_context_tokens>=0), -- 完整输入上下文 Token 数，包含命中缓存的输入
 input_uncached_tokens INTEGER NOT NULL DEFAULT 0 CHECK(input_uncached_tokens>=0), -- 未命中缓存的输入 Token 数
 output_tokens INTEGER NOT NULL DEFAULT 0 CHECK(output_tokens>=0), -- 完整输出 Token 数；可能已包含 reasoning_tokens
 cache_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_tokens>=0), -- 缓存读取 Token 数，是完整输入的组成部分，不重复加总
 cache_write_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_tokens>=0), -- 缓存写入 Token 数；不能未经确认再加到总量
 reasoning_tokens INTEGER NOT NULL DEFAULT 0 CHECK(reasoning_tokens>=0), -- 推理 Token 数；可能为输出的子集，不重复加总
 tool_extra_tokens INTEGER NOT NULL DEFAULT 0 CHECK(tool_extra_tokens>=0), -- 已证明没有计入其他组成的额外工具 Token 数
 model_request_count INTEGER NOT NULL DEFAULT 0 CHECK(model_request_count>=0), -- 模型请求次数，不等同于轮次、用户消息或事件条数
 usage_observed_count INTEGER NOT NULL DEFAULT 0 CHECK(usage_observed_count>=0), -- 观测到的有效用量样本数，用于计算字段覆盖
 token_total_known_count INTEGER NOT NULL DEFAULT 0 CHECK(token_total_known_count>=0), -- 总 Token明确已知的样本数；已知为零也计入，未知不计入
 input_context_known_count INTEGER NOT NULL DEFAULT 0 CHECK(input_context_known_count>=0), -- 完整输入明确已知的样本数；已知为零也计入，未知不计入
 input_uncached_known_count INTEGER NOT NULL DEFAULT 0 CHECK(input_uncached_known_count>=0), -- 未缓存输入明确已知的样本数；已知为零也计入，未知不计入
 output_known_count INTEGER NOT NULL DEFAULT 0 CHECK(output_known_count>=0), -- 输出明确已知的样本数；已知为零也计入，未知不计入
 cache_read_known_count INTEGER NOT NULL DEFAULT 0 CHECK(cache_read_known_count>=0), -- 缓存读取明确已知的样本数；已知为零也计入，未知不计入
 cache_write_known_count INTEGER NOT NULL DEFAULT 0 CHECK(cache_write_known_count>=0), -- 缓存写入明确已知的样本数；已知为零也计入，未知不计入
 reasoning_known_count INTEGER NOT NULL DEFAULT 0 CHECK(reasoning_known_count>=0), -- 推理 Token明确已知的样本数；已知为零也计入，未知不计入
 tool_extra_known_count INTEGER NOT NULL DEFAULT 0 CHECK(tool_extra_known_count>=0), -- 额外工具 Token明确已知的样本数；已知为零也计入，未知不计入
 cache_eligible_input_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_eligible_input_tokens>=0), -- 输入和缓存读取均已知的同一请求集合之输入 Token 总和
 cache_eligible_read_tokens INTEGER NOT NULL DEFAULT 0 CHECK(cache_eligible_read_tokens>=0), -- 与 cache_eligible_input_tokens 同一集合的缓存读取 Token 总和
 cache_pair_known_count INTEGER NOT NULL DEFAULT 0 CHECK(cache_pair_known_count>=0), -- 完整输入和缓存读取同时已知的请求样本数
 metric_semantics_version INTEGER NOT NULL CHECK(metric_semantics_version>0), -- 统计含义与公式版本，正整数
 UNIQUE(grain,bucket_start,harness_id,model_key),
 CHECK(cache_eligible_read_tokens<=cache_eligible_input_tokens),
 CHECK(token_total_known_count<=usage_observed_count),
 CHECK(input_context_known_count<=usage_observed_count),
 CHECK(input_uncached_known_count<=usage_observed_count),
 CHECK(output_known_count<=usage_observed_count),
 CHECK(cache_read_known_count<=usage_observed_count),
 CHECK(cache_write_known_count<=usage_observed_count),
 CHECK(reasoning_known_count<=usage_observed_count),
 CHECK(tool_extra_known_count<=usage_observed_count),
 CHECK(cache_pair_known_count<=usage_observed_count)
) STRICT;
CREATE INDEX idx_model_filter ON model_metrics(grain,harness_id,bucket_start,model_key) WHERE delete_at IS NULL;

CREATE INDEX idx_model_lookup ON model_metrics(grain,model_key,bucket_start,harness_id) WHERE delete_at IS NULL;

CREATE TABLE skill_metrics (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 时间粒度：hour 小时、day 自然日、month 自然月
 bucket_start INTEGER NOT NULL, -- 北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 skill_id INTEGER NOT NULL REFERENCES skill_dimensions(id), -- 技能维度外键，引用 skill_dimensions.id；本统计行必须有明确技能身份
 use_count INTEGER NOT NULL DEFAULT 0 CHECK(use_count>=0), -- 技能使用总次数，不包含 estimated 调用
 exact_use_count INTEGER NOT NULL DEFAULT 0 CHECK(exact_use_count>=0), -- 原始明确记录的技能使用次数
 derived_use_count INTEGER NOT NULL DEFAULT 0 CHECK(derived_use_count>=0), -- 确定性推导的技能使用次数
 correlated_use_count INTEGER NOT NULL DEFAULT 0 CHECK(correlated_use_count>=0), -- 关联归因的技能使用次数
 success_count INTEGER NOT NULL DEFAULT 0 CHECK(success_count>=0), -- 结果明确成功的次数
 failure_count INTEGER NOT NULL DEFAULT 0 CHECK(failure_count>=0), -- 结果明确失败的次数；未知结果不计入
 duration_ms INTEGER NOT NULL DEFAULT 0 CHECK(duration_ms>=0), -- 执行耗时，单位毫秒；事件中未知为 NULL，汇总表为已知耗时之和
 duration_known_count INTEGER NOT NULL DEFAULT 0 CHECK(duration_known_count>=0), -- 耗时明确已知的样本数；已知为零也计入，未知不计入
 metric_semantics_version INTEGER NOT NULL CHECK(metric_semantics_version>0), -- 统计含义与公式版本，正整数
 UNIQUE(grain,bucket_start,harness_id,skill_id),
 CHECK(success_count+failure_count<=use_count),
 CHECK(exact_use_count+derived_use_count+correlated_use_count<=use_count),
 CHECK(duration_known_count<=use_count)
) STRICT;

CREATE TABLE cost_metrics (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 时间粒度：hour 小时、day 自然日、month 自然月
 bucket_start INTEGER NOT NULL, -- 北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 model_key INTEGER NOT NULL REFERENCES model_dimensions(id), -- 模型维度外键，引用 model_dimensions.id；0 表示未知模型
 currency TEXT NOT NULL CHECK(length(currency)=3 AND currency NOT GLOB '*[^A-Z]*'), -- 三个大写 ASCII 字母币种代码；不同币种分别累计
 reported_cost_units INTEGER NOT NULL DEFAULT 0 CHECK(reported_cost_units>=0), -- 生效的提供商报告金额累计值，单位为币种的 10^-8
 estimated_cost_units INTEGER NOT NULL DEFAULT 0 CHECK(estimated_cost_units>=0), -- 生效的价格表计算金额累计值，单位为币种的 10^-8；不代表实际账单
 reported_request_count INTEGER NOT NULL DEFAULT 0 CHECK(reported_request_count>=0), -- 生效费用来源为提供商报告的请求数
 estimated_request_count INTEGER NOT NULL DEFAULT 0 CHECK(estimated_request_count>=0), -- 生效费用来源为价格表计算的请求数
 unpriced_request_count INTEGER NOT NULL DEFAULT 0 CHECK(unpriced_request_count>=0), -- 缺少可用价格、尚未定价的请求数，不伪装为已知零费用
 cost_known_count INTEGER NOT NULL DEFAULT 0 CHECK(cost_known_count>=0), -- 费用明确已知的样本数；已知为零也计入，未知不计入
 metric_semantics_version INTEGER NOT NULL CHECK(metric_semantics_version>0), -- 统计含义与公式版本，正整数
 UNIQUE(grain,bucket_start,harness_id,model_key,currency)
) STRICT;

CREATE TABLE bucket_entity_state (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 时间粒度：hour 小时、day 自然日、month 自然月
 bucket_start INTEGER NOT NULL, -- 北京时间统计桶起点对应的 UTC 毫秒时间，按 occurred_at 归桶
 harness_id TEXT NOT NULL CHECK(length(harness_id)>0), -- 工具策略标识，如 codex、opencode、zcode
 entity_kind TEXT NOT NULL CHECK(entity_kind IN ('session','turn')), -- 需要去重的实体类型：session 会话、turn 轮次
 entity_key BLOB NOT NULL CHECK(length(entity_key)=32), -- 实体的 32 字节匿名身份，在同一统计桶内唯一
 parent_key BLOB CHECK(length(parent_key)=32), -- 父实体的 32 字节匿名身份；无父关联时 NULL
 has_started INTEGER NOT NULL DEFAULT 0 CHECK(has_started IN (0,1)), -- 是否观察到开始：1 是、0 否
 has_completed INTEGER NOT NULL DEFAULT 0 CHECK(has_completed IN (0,1)), -- 是否观察到完成：1 是、0 否
 has_user_start INTEGER NOT NULL DEFAULT 0 CHECK(has_user_start IN (0,1) AND has_user_start<=has_started), -- 是否观察到用户触发的开始；为 1 时 has_started 必须为 1
 session_duration_ms INTEGER CHECK(session_duration_ms>=0), -- 权威会话耗时，毫秒；未知为 NULL
 turn_duration_ms INTEGER CHECK(turn_duration_ms>=0), -- 轮次耗时兜底，毫秒；未知为 NULL
 UNIQUE(grain,bucket_start,harness_id,entity_kind,entity_key)
) STRICT;
CREATE INDEX idx_entity_related ON bucket_entity_state(harness_id,entity_kind,entity_key,grain,bucket_start);
CREATE INDEX idx_entity_parent ON bucket_entity_state(grain,bucket_start,harness_id,parent_key);

CREATE TABLE IF NOT EXISTS session_extents (
 id INTEGER PRIMARY KEY, -- 本地代理主键
 created_at INTEGER NOT NULL, -- 首次创建 UTC 毫秒
 updated_at INTEGER NOT NULL, -- 最近更新 UTC 毫秒
 delete_at INTEGER, -- 删除 UTC 毫秒；NULL 未删除
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)), -- 可选扩展；不存必需业务状态
 harness_id TEXT NOT NULL, -- 工具标识
 session_key BLOB NOT NULL CHECK(length(session_key)=32), -- 稳定匿名会话身份
 grain TEXT NOT NULL CHECK(grain IN ('hour','day','month')), -- 独立消费者的处理进度
 first_event_at INTEGER NOT NULL, -- 已处理的最早事件 UTC 毫秒
 last_event_at INTEGER NOT NULL CHECK(last_event_at>=first_event_at), -- 已处理的最晚事件 UTC 毫秒
 UNIQUE(harness_id,session_key,grain)
 ) STRICT;
