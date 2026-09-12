# 本地事件流水线完整 DDL v3

状态：设计稿，未接入业务、未迁移真实数据库。当前共 10 张表、183 个字段；events 为 23 列。SQL 文件是唯一建表来源，本文件由脚本生成。

[客户端与服务端完整技术方案](event-pipeline-refactor-technical-plan-v1.md) · [服务端完整 DDL](event-pipeline-server-ddl-v1.md)

2026-09-12 已确认项目仍在内测，旧采集与统计数据不迁移。本次初始化空的新库，不复制旧事件、游标、任务或统计，也不保留旧库历史读取；认证、设备密钥和 Agent raw 不属于统计重置范围。以下为新链路正常运行后的表与保留规则，初始化完成后重启不能再次清库。

## 所有表统一的五个字段

| 字段 | SQLite 类型 | 含义 |
| --- | --- | --- |
| id | INTEGER PRIMARY KEY | 本地行主键；events 使用 AUTOINCREMENT，业务幂等仍由独立 UNIQUE 保证。 |
| created_at | INTEGER NOT NULL | 首次创建的 UTC 毫秒时间，后续更新不改变；不用于替代事件发生时间。 |
| updated_at | INTEGER NOT NULL | 最近实际更新的 UTC 毫秒时间；包括状态、租约、游标、软删除等变更，由 writer 同事务赋值。 |
| delete_at | INTEGER NULL | 软删除时间，UTC 毫秒；NULL 表示未删除。按用户指定使用 delete_at，不改名为 deleted_at。 |
| extra | TEXT NOT NULL DEFAULT 空对象 | 合法 JSON 对象；只容纳本地可选扩展信息，不承载必需业务字段、处理状态、账号或凭据，不默认上传。 |

所有表将这五列放在最前面；每列均有中文 SQL 行注释。创建/更新/删除时间保持独立列，不放 JSON。金额和 Token 使用受检 64 位整数，金额单位为币种的 10^-8；未知允许缺失或 null，明确零值为 0。

## 本地数据与账号分离

本地事件、任务、统计及实体状态均不保存 account_id，也不将账号隐藏在 JSON 中。不增加认领、归属迁移或上传目标表；本机统计汇总本机采集数据。上传时才读取当前认证上下文，由服务端决定账号归属。已 ACK 事件不因换号重传，在途请求固定原认证上下文。

全部本地唯一键、外键和索引已移除账号维度。processing_tasks.event_row_id 直接引用 events.id；原 UNIQUE(id,account_id) 和对应复合外键取消。服务端必须以稳定设备/事件身份防重，首次已接收事实不因换号再次计数或转移归属；现有服务端认证/设备绑定协议仍需在实施时对齐。

## 事件字段聚类

payload_json 按 usage、cost、code、activity、context、meta 分组，仅携带适用内容；status_json 独立保存 hour/day/month/upload 四个状态。保留身份、来源、时间和必要关联列，取消 envelope_json 与展开业务列的重复存储。上传从冻结事件头和 payload 确定性构造，不包含公共管理字段、extra 和 status_json，不在重试时重新估价或取文件时间。

JSON 状态四个键必须存在，值为整数 0–6；3 表示该消费者完成，上传对应 ACK、统计对应已应用。使用 json_set 只修改本消费者路径，不能将 worker 读取的旧 JSON 整块写回。writer 同事务更新统计、实体状态、事件状态与 updated_at，并核验租约、事件未软删/未过期；完成后删除任务。

每 5 秒从 processing_tasks 的到期索引有界领取，再按事件主键核验 JSON 状态；不对全部 events 做 JSON 状态扫描，不为四个状态各增一套索引。统计任务按类型读取 payload，页面读取有数值列的统计表。JSON 语法与关键取值由 DDL 校验，按 event_type 的完整结构、重复键、隐私白名单和规范化规则仍由类型化解码器在入库前校验。

前轮 10 万条内存合成数据对比：取 500 条队列事件，数值状态列约 0.20ms、JSON 约 0.33ms；全表状态扫描约 2.60ms/24.81ms。该测试不含磁盘、WAL 与生产并发，只说明应保留队列索引访问路径，不代表生产吞吐。

## 软删除、幂等与保留期

- 常规列表、统计读取和到期领取只读取 delete_at IS NULL；来源/维度的身份查找和保留期内的事件去重检查包含软删除记录，历史引用不能因标签被软删除而断开。
- 业务唯一键继续覆盖全部行，不加入 delete_at，也不改成仅未删除行唯一。同一事件、来源或统计桶不会因软删除得到第二个身份；普通采集/upsert 不得隐式复活已软删记录。
- delete_at 不触发外键级联。逻辑删除事件若需要停止处理，writer 同事务取消其任务和租约；消费者回写再次检查事件和任务均未软删除。租约回收索引仍覆盖已软删的在途租约，不能漏掉清理。
- 每表具备公共列不等于新增一套自动软删除业务。统计桶删除若需撤回贡献，必须连同相关状态处理；已贡献实体状态不执行按明细 TTL 的自动软删除。
- events 到 created_at + 14 天物理删除，包括此前已经软删除的事件；任务随外键级联清理。updated_at/delete_at 不续期；到期未完成事件按来源累计一次，不伪造 ACK。
- 删除 event_identity_ledger，不增加替代的永久事件身份记录。collection_sources 游标不随明细到期清理；正常采集按游标增量读取，保留期内由 events 唯一键防重。
- 不统一为 updated_at、delete_at、extra 建索引。现有明细/到期热路径使用未删除部分索引；事件过期索引与租约回收索引保留软删除行。

## 时间与类型

occurred_at 独立于创建/更新时间。按源记录时间 → 同流前序有效时间 → 原始文件 mtime 选择，并在 payload_json.meta.time_source 标记依据。前序锚点仍随来源 decoder_state 和游标原子提交。首次只准入北京时间当天；已有事件跨日继续处理；保留期内重复读取先查 events，复用冻结时间。事件明细到期后不提供在原库中从头重放的保证；需要重新采集时采用重装并重新初始化本地数据的流程。详见 [事件时间规则](event-time-admission-v1.md)。

服务端按事件 occurred_at 做时间范围过滤，重放时间、上传时间和本地 created_at 均不替代事件发生时间。正常重试沿用原 occurred_at 和 event_id；时间范围内的重复仍由服务端唯一键处理。完整技术方案建议服务端接收今天及此前 14 个北京时间日期，以覆盖本地 14 天重试；这是服务端建议策略，不改变本地 created_at 起算的 TTL，也不将最大已接收时间当成并发上传全部完成的水位。

events 的 session_scope、cost_scope 两个部分索引用于时长权威替换和费用选择的关联范围读取，不为 JSON 字段逐项建索引。硬 TTL 清理必要旧贡献前，必须同事务取消相关未完成复合任务租约并阻塞，避免用残缺输入覆盖统计；具体事务边界见完整技术方案。

SQLite STRICT 使用 INTEGER/TEXT/BLOB；摘要为固定 32 字节 BLOB，JSON 为 TEXT。SQLite 不支持列级 COMMENT，全部含义以同行 -- 注释保存。DDL 中的公共时间没有自动更新触发器，由统一 writer 维护；重复观察无实际变更时不更新 created_at/updated_at。

## 表清单

| 表 | 用途 | 保留规则 |
| --- | --- | --- |
| collection_sources | 采集单元与进度 | 保留来源与读取进度，不随 14 天事件明细清理；正常重启从游标续读。 |
| model_dimensions | 模型维度 | 被引用期间保留；软删除不释放模型身份。 |
| skill_dimensions | 技能维度 | 被引用期间保留；软删除不释放技能身份。 |
| events | 事件细节 | 首次 created_at 起 14 天物理清理，包括已软删除明细。 |
| processing_tasks | 执行队列 | 完成或事件到期后物理删除；软删除表示停止调度。 |
| harness_metrics | 工具活动统计 | 长期统计；不按事件的 14 天规则清理。 |
| model_metrics | 模型用量统计 | 长期统计。 |
| skill_metrics | 技能统计 | 长期统计。 |
| cost_metrics | 费用统计 | 长期统计。 |
| bucket_entity_state | 会话与轮次去重状态 | 依照去重保证保留；不能随事件 TTL 删除或自动软删除。 |

## 建库设置

```sql
-- Local SQLite design only; not an application migration.
PRAGMA foreign_keys=ON;
```

## 1. collection_sources — 采集单元与进度

来源、独立读取流、游标、解析基线与采集租约同一行；忽略记录和到期未完成事件只存累计数。

保留：保留来源与读取进度，不随 14 天事件明细清理；正常重启从游标续读。

```sql
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
```

本表保留四个辅助索引：来源业务唯一键、来源/harness 外键所需 UNIQUE(id,harness_id)、未软删启用流的 due 索引、包含软删在途任务的 lease 回收索引。不单建 enabled 或 delete_at 索引。

## 2. model_dimensions — 模型维度

provider_id + model_id 唯一；id=0 为 unknown，初始化显式提供创建和更新时间。

保留：被引用期间保留；软删除不释放模型身份。

```sql
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
```

## 3. skill_dimensions — 技能维度

匿名 skill_key 为身份，public_name 为展示标签；改标签不改变调用身份。

保留：被引用期间保留；软删除不释放技能身份。

```sql
CREATE TABLE skill_dimensions (
 id INTEGER PRIMARY KEY, -- 本地 64 位整数代理主键；业务幂等由独立 UNIQUE 保证
 created_at INTEGER NOT NULL CHECK(created_at>=0), -- 首次本地创建时间，UTC 毫秒；插入后不因更新或重试改变
 updated_at INTEGER NOT NULL CHECK(updated_at>=0), -- 本行最近一次实际更新的 UTC 毫秒时间；由 writer 在同一事务维护
 delete_at INTEGER CHECK(delete_at>=0), -- 软删除的 UTC 毫秒时间；NULL 表示未删除，不表示物理清理完成
 extra TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(extra)) CHECK(json_type(extra)='object'), -- 本地扩展 JSON 对象；不放必需业务字段、处理状态、账号或凭据，不默认上传
 skill_key BLOB NOT NULL UNIQUE CHECK(length(skill_key)=32), -- 技能稳定身份的 32 字节匿名摘要
 public_name TEXT -- 允许展示的技能标签；未知或私有时 NULL，不参与身份判重
) STRICT;
```

## 4. events — 事件细节

23 个字段：公共字段、身份/来源/版本、时间与关联列、payload_json、status_json。不持久化账号，不复制整份 envelope。

保留：首次 created_at 起 14 天物理清理，包括已软删除明细。

```sql
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
```

## 5. processing_tasks — 执行队列

UNIQUE(event_row_id,consumer)；只存租约和重试控制，四个完成状态仅在事件 status_json 中。

保留：完成或事件到期后物理删除；软删除表示停止调度。

```sql
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
```

## 6. harness_metrics — 工具活动统计

会话、轮次、工具/技能次数、消息、代码和活动时长；Token 与模型请求从 model_metrics 汇总。

保留：长期统计；不按事件的 14 天规则清理。

```sql
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
```

## 7. model_metrics — 模型用量统计

规范化 Token、覆盖和请求数；按模型求和得到工具用量，必须包含 unknown。

保留：长期统计。

```sql
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
```

## 8. skill_metrics — 技能统计

调用、成功/失败、耗时与覆盖；比例和活跃天数由基础值计算。

保留：长期统计。

```sql
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
```

## 9. cost_metrics — 费用统计

同一有效金额只归一个模型或 unknown 和币种；按模型求和得到工具金额，不重复保存工具总额。

保留：长期统计。

```sql
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
```

## 10. bucket_entity_state — 会话与轮次去重状态

保存各 grain 中的实体身份和必要标志，用于唯一会话数、消息数以及时长优先级。

保留：依照去重保证保留；不能随事件 TTL 删除或自动软删除。

```sql
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
```

## 验证与相关方案

生成前执行全部 SQL：10 张表的五个公共字段、全部 183 个字段注释、无账号列、23 个 events 字段与 23 项非法写入拒绝检查；验证 JSON 路径更新、软删不释放幂等身份、物理 TTL 仍包含软删行、外键级联及来源索引。只访问内存 SQLite。

[统计口径与索引](statistics-model-and-indexes-v3.md) · [精简审查](schema-simplification-review-v1.md) · [计算契约](metric-contract-v1.md) · [索引验证](statistics-index-validation-v3.md)
