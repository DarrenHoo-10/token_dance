"""Generate the proposed MySQL schema and its review document; never connect to a database."""
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent
LOCAL = (ROOT / 'event-pipeline-schema-v3.sqlite.sql').read_text(encoding='utf-8')
TABLES = []


def col(name, sql_type, comment):
    assert "'" not in comment
    return f" {name} {sql_type} COMMENT '{comment}'"


def common():
    return [
        col('id', 'BIGINT UNSIGNED NOT NULL AUTO_INCREMENT', '本表代理主键；业务身份由独立唯一键保证，不作为同步水位'),
        col('created_at', 'BIGINT UNSIGNED NOT NULL', '服务器首次创建本行的 UTC 毫秒时间；插入后不因重试更新'),
        col('updated_at', 'BIGINT UNSIGNED NOT NULL', '本行最近一次实际更新的 UTC 毫秒时间；由同事务代码维护'),
        col('delete_at', 'BIGINT UNSIGNED NULL', '软删除的 UTC 毫秒时间；NULL 为未删除，不自动撤回统计或释放唯一键'),
        col('extra', 'JSON NOT NULL DEFAULT (JSON_OBJECT())', '可选扩展对象；不放必需业务字段、任务状态、凭据或重复事件正文'),
    ]


UID = 'CHAR(30) CHARACTER SET ascii COLLATE ascii_bin NOT NULL'
HARNESS = 'VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL'


def table(name, title, columns, rules):
    rules = ['PRIMARY KEY (id)', "CHECK (JSON_TYPE(extra)='OBJECT')"] + rules
    sql = f'CREATE TABLE {name} (\n' + ',\n'.join(columns + [' ' + r for r in rules])
    sql += f"\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin COMMENT='{title}';\n"
    TABLES.append((name, title, sql))


table('telemetry_models', '公开模型身份维度', common() + [
    col('provider_id', 'VARCHAR(96) NOT NULL', '稳定模型提供商标识；不使用展示别名代替'),
    col('model_id', 'VARCHAR(255) NOT NULL', '提供商内原始模型标识；大小写敏感，插入后身份不可更改'),
], ['UNIQUE KEY uk_tm_identity (provider_id,model_id)',
    'CHECK (CHAR_LENGTH(provider_id)>0 AND CHAR_LENGTH(model_id)>0)'])

table('telemetry_skills', '设备范围的匿名技能身份与公开标签', common() + [
    col('installation_id', UID, '技能匿名身份所属设备；不同设备的私有 hash 不猜测合并'),
    col('skill_key', 'BINARY(32) NOT NULL', '设备匿名化后的稳定技能身份'),
    col('public_name', 'VARCHAR(255) NULL', '经过隐私白名单的展示标签；不参与技能身份或业务内容 hash'),
], ['UNIQUE KEY uk_ts_identity (installation_id,skill_key)',
    'CONSTRAINT fk_ep_skill_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id)'])

event_columns = common() + [
    col('installation_id', UID, '签名所证明的稳定设备身份；事件幂等键不包含账号'),
    col('event_id', 'BINARY(32) NOT NULL', '不可变事件版本的业务幂等键'),
    col('fact_key', 'BINARY(32) NOT NULL', '逻辑事实稳定身份；不是批次或数据库行序号'),
    col('fact_revision', 'BIGINT UNSIGNED NOT NULL', '来源原生事实版本；普通完成事实固定为 1'),
    col('harness_id', HARNESS, '工具策略标识，与标准协议的命名空间一致'),
    col('event_type', 'VARCHAR(48) CHARACTER SET ascii COLLATE ascii_bin NOT NULL', '标准事件类型；完整 payload 结构由协议校验'),
    col('schema_version', 'SMALLINT UNSIGNED NOT NULL', '标准事件结构版本；服务端显式检查兼容性'),
    col('metric_semantics_version', 'SMALLINT UNSIGNED NOT NULL', '统计公式版本；不与协议结构或软件版本混用'),
    col('content_hash', 'BINARY(32) NOT NULL', '规范业务 envelope 的 SHA-256；不含服务器时间、状态和认证信息'),
    col('occurred_at', 'BIGINT UNSIGNED NOT NULL', '选定事件发生时间，UTC 毫秒；用于 UTC+8 分桶和接收时间过滤'),
    col('model_key', 'BIGINT UNSIGNED NOT NULL DEFAULT 1', '服务器模型维度外键；初始化 id=1 为 unknown，不沿用客户端数值 ID'),
    col('skill_id', 'BIGINT UNSIGNED NULL', '服务器技能维度外键；未适用时 NULL'),
    col('session_key', 'BINARY(32) NULL', '匿名会话关联身份；由设备命名空间限定'),
    col('turn_key', 'BINARY(32) NULL', '匿名轮次关联身份；未知时 NULL'),
    col('cost_scope_key', 'BINARY(32) NULL', '经过证明的费用选择范围身份；用于请求与轮次费用关联'),
    col('payload_json', 'JSON NOT NULL', '冻结业务分组 usage、cost、code、activity、context、meta；规范数字转换后存储'),
    col('status_json', 'JSON NOT NULL', '服务器 hour、day、month 三个整数状态；0 待办、1 重试、2 在途、3 完成、4 不适用、5 阻塞、6 隔离'),
]
event_rules = [
    'UNIQUE KEY uk_te_event (installation_id,event_id)',
    'UNIQUE KEY uk_te_fact (installation_id,fact_key,fact_revision)',
    'KEY idx_te_user_time (installation_id,delete_at,occurred_at,id)',
    'KEY idx_te_retention (occurred_at,id)',
    'KEY idx_te_session (installation_id,harness_id,session_key,occurred_at,id)',
    'KEY idx_te_cost (installation_id,harness_id,cost_scope_key,occurred_at,id)',
    'KEY idx_te_model_fk (model_key)',
    'KEY idx_te_skill_fk (skill_id)',
    'CONSTRAINT fk_ep_event_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id)',
    'CONSTRAINT fk_ep_event_model FOREIGN KEY (model_key) REFERENCES telemetry_models(id)',
    'CONSTRAINT fk_ep_event_skill FOREIGN KEY (skill_id) REFERENCES telemetry_skills(id)',
    'CHECK (fact_revision>0 AND schema_version>0 AND metric_semantics_version>0)',
    "CHECK (JSON_TYPE(payload_json)='OBJECT' AND JSON_TYPE(status_json)='OBJECT')",
    "CHECK (COALESCE(JSON_TYPE(JSON_EXTRACT(payload_json,'$.meta')),'NULL')='OBJECT')",
    "CHECK (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.meta.accuracy')),'') IN ('exact','derived','correlated','unknown'))",
    "CHECK (COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.meta.time_source')),'') IN ('source_record','previous_record','file_mtime'))",
    "CHECK (JSON_CONTAINS_PATH(payload_json,'one','$.usage')=0 OR (JSON_TYPE(JSON_EXTRACT(payload_json,'$.usage'))='OBJECT' AND JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.meta.accuracy')) IN ('exact','derived')))",
]
for consumer in ('hour', 'day', 'month'):
    expr = f"JSON_EXTRACT(status_json,'$.{consumer}')"
    event_rules.append(f"CHECK (COALESCE(JSON_TYPE({expr}),'NULL')='INTEGER' AND CAST(JSON_UNQUOTE({expr}) AS SIGNED) BETWEEN 0 AND 6)")
table('telemetry_events', '已接收的标准事实；与统计完成分离', event_columns, event_rules)

table('telemetry_tasks', '服务端事件统计执行队列；成功后删除', common() + [
    col('event_row_id', 'BIGINT UNSIGNED NOT NULL', '服务器 telemetry_events.id 外键；不是 wire event_id'),
    col('consumer', 'VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL', '统计消费者：hour、day 或 month'),
    col('runnable_at', 'BIGINT UNSIGNED NULL', '下次可执行 UTC 毫秒时间；在途、阻塞或隔离时 NULL'),
    col('lease_token', 'BINARY(16) NULL', '每次领取的随机 128 位租约令牌；提交和续租必须匹配'),
    col('lease_until', 'BIGINT UNSIGNED NULL', '租约截止 UTC 毫秒时间；无租约时 NULL'),
    col('attempt_count', 'BIGINT UNSIGNED NOT NULL DEFAULT 0', '实际开始执行次数；不等于事件修订号'),
    col('last_error_code', 'VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL', '最近失败或阻塞的机器错误码；不存敏感原始异常'),
], [
    'UNIQUE KEY uk_tt_consumer (event_row_id,consumer)',
    'KEY idx_tt_due (consumer,delete_at,runnable_at,id)',
    'KEY idx_tt_lease (consumer,lease_until,id)',
    'CONSTRAINT fk_ep_task_event FOREIGN KEY (event_row_id) REFERENCES telemetry_events(id) ON DELETE CASCADE',
    "CHECK (consumer IN ('hour','day','month'))",
    'CHECK ((lease_token IS NULL AND lease_until IS NULL) OR (lease_token IS NOT NULL AND lease_until IS NOT NULL AND runnable_at IS NULL))',
])


def local_business_columns(name):
    body = re.search(rf'CREATE TABLE {name} \((.*?)\n\) STRICT;', LOCAL, re.S).group(1)
    result = []
    for line in body.splitlines():
        match = re.match(r' ([a-z_]+) (.*?)?, -- (.*)', line)
        if not match:
            continue
        field, definition, comment = match.groups()
        if field not in {'id', 'created_at', 'updated_at', 'delete_at', 'extra'}:
            result.append((field, definition, comment))
    return result


def metric_columns(local_name):
    columns = common() + [
    col('installation_id', UID, '统计贡献所属设备；用于多设备汇总和按设备删除贡献'),
    ]
    for field, definition, comment in local_business_columns(local_name):
        if field == 'grain':
            sql_type = 'VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL'
        elif field == 'bucket_start':
            sql_type = 'BIGINT UNSIGNED NOT NULL'
        elif field == 'harness_id':
            sql_type = HARNESS
        elif field in ('model_key', 'skill_id'):
            sql_type = 'BIGINT UNSIGNED NOT NULL'
            comment = '服务器模型维度外键；unknown 为初始化行 id=1' if field == 'model_key' else '服务器设备内技能维度外键'
        elif field == 'currency':
            sql_type = 'CHAR(3) CHARACTER SET ascii COLLATE ascii_bin NOT NULL'
        elif field == 'metric_semantics_version':
            sql_type = 'SMALLINT UNSIGNED NOT NULL'
        else:
            sql_type = ('DECIMAL(38,0)' if 'token' in field and not field.endswith('_count') or field.endswith('_cost_units') else 'BIGINT UNSIGNED') + ' NOT NULL DEFAULT 0'
        columns.append(col(field, sql_type, comment))
    return columns


for theme in ('harness', 'model', 'skill', 'cost'):
    name = f'telemetry_{theme}_metrics'
    prefix = {'harness': 'thm', 'model': 'tmm', 'skill': 'tsm', 'cost': 'tcm'}[theme]
    suffix = {'harness': '', 'model': ',model_key', 'skill': ',skill_id', 'cost': ',model_key,currency'}[theme]
    rules = [
        f'UNIQUE KEY uk_{prefix}_bucket (installation_id,grain,bucket_start,harness_id{suffix})',
        f'KEY idx_{prefix}_device (installation_id,id)',
        f'CONSTRAINT fk_{prefix}_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id)',
        "CHECK (grain IN ('hour','day','month'))",
        'CHECK (metric_semantics_version>0)',
    ]
    if theme in ('model', 'cost'):
        rules += [f'KEY idx_{prefix}_model_fk (model_key)',
                  f'CONSTRAINT fk_{prefix}_model FOREIGN KEY (model_key) REFERENCES telemetry_models(id)']
    if theme == 'skill':
        rules += ['KEY idx_tsm_skill_fk (skill_id)',
                  'CONSTRAINT fk_tsm_skill FOREIGN KEY (skill_id) REFERENCES telemetry_skills(id)',
                  'CHECK (success_count+failure_count<=use_count)',
                  'CHECK (exact_use_count+derived_use_count+correlated_use_count<=use_count)',
                  'CHECK (duration_known_count<=use_count)']
    if theme in ('harness', 'model'):
        rules += [f'KEY idx_{prefix}_harness (installation_id,grain,harness_id,bucket_start{suffix})']
    if theme == 'model':
        rules += ['KEY idx_tmm_model_filter (installation_id,grain,model_key,bucket_start,harness_id)',
                  'CHECK (cache_eligible_read_tokens<=cache_eligible_input_tokens)']
        for field, _, _ in local_business_columns('model_metrics'):
            if field.endswith('_known_count'):
                rules.append(f'CHECK ({field}<=usage_observed_count)')
    if theme == 'cost':
        rules += ["CHECK (REGEXP_LIKE(currency,'^[A-Z]{3}$','c'))"]
    columns = metric_columns(theme + '_metrics')
    for line in columns:
        if 'DECIMAL(' in line:
            rules.append(f'CHECK ({line.strip().split()[0]}>=0)')
    table(name, '按设备分 grain 保存的' + theme + '统计', columns, rules)

entity_columns = common() + [
    col('installation_id', UID, '实体匿名命名空间所属设备'),
]
for field, definition, comment in local_business_columns('bucket_entity_state'):
    if field == 'grain':
        typ = 'VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL'
    elif field == 'harness_id':
        typ = HARNESS
    elif field == 'entity_kind':
        typ = 'VARCHAR(8) CHARACTER SET ascii COLLATE ascii_bin NOT NULL'
    elif field in ('entity_key', 'parent_key'):
        typ = 'BINARY(32)' + (' NOT NULL' if field == 'entity_key' else ' NULL')
    elif field.startswith('has_'):
        typ = 'TINYINT UNSIGNED NOT NULL DEFAULT 0'
    else:
        typ = 'BIGINT UNSIGNED' + (' NOT NULL' if 'NOT NULL' in definition else ' NULL')
    entity_columns.append(col(field, typ, comment))
table('telemetry_bucket_entities', '粒度内会话轮次成员与必要状态；不是永久事件账本', entity_columns, [
    'UNIQUE KEY uk_tbe_entity (installation_id,grain,bucket_start,harness_id,entity_kind,entity_key)',
    'KEY idx_tbe_related (installation_id,harness_id,entity_kind,entity_key,grain,bucket_start)',
    'KEY idx_tbe_parent (installation_id,grain,bucket_start,harness_id,parent_key)',
    'CONSTRAINT fk_tbe_device FOREIGN KEY (installation_id) REFERENCES installations(installation_id)',
    "CHECK (grain IN ('hour','day','month'))",
    "CHECK (entity_kind IN ('session','turn'))",
    'CHECK (has_started IN (0,1) AND has_completed IN (0,1) AND has_user_start IN (0,1) AND has_user_start<=has_started)',
])

table('aggregate_dirty_days', '重构已有用户日读模型刷新队列；合并多个事件变更', common() + [
    col('user_id', UID, '需要刷新派生读模型的账号'),
    col('metric_date', 'DATE NOT NULL', '受影响的北京时间业务日期'),
    col('dirty_version', 'BIGINT UNSIGNED NOT NULL DEFAULT 1', '该账号日期的累计刷新请求版本；不是事实修订或统计桶代次'),
    col('applied_version', 'BIGINT UNSIGNED NOT NULL DEFAULT 0', '已发布刷新结果所覆盖的领取版本；不能盲目追平最新 dirty_version'),
    col('claim_token', 'BINARY(16) NULL', '本次刷新领取令牌；完成和续租必须匹配'),
    col('lease_expires_at', 'BIGINT UNSIGNED NULL', '刷新租约截止时间，UTC 毫秒；未领取为 NULL'),
    col('attempt_count', 'BIGINT UNSIGNED NOT NULL DEFAULT 0', '刷新实际尝试次数'),
    col('next_attempt_at', 'BIGINT UNSIGNED NULL', '下次刷新时间，UTC 毫秒；完成或在途为 NULL'),
    col('last_error_code', 'VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL', '最近读模型刷新失败原因码；不存敏感异常正文'),
], [
    'UNIQUE KEY uk_add_user_day (user_id,metric_date)',
    'KEY idx_add_due (delete_at,next_attempt_at,id)',
    'KEY idx_add_lease (lease_expires_at,id)',
    'CONSTRAINT fk_ep_dirty_user FOREIGN KEY (user_id) REFERENCES users(user_id)',
    'CHECK (applied_version<=dirty_version)',
    'CHECK ((claim_token IS NULL AND lease_expires_at IS NULL) OR (claim_token IS NOT NULL AND lease_expires_at IS NOT NULL AND next_attempt_at IS NULL))',
])

HEADER = """-- Target schema for review, NOT an executable migration against the existing application.
-- Requires MySQL 8.4 / InnoDB and the existing users and installations natural IDs (CHAR(30) ASCII ascii_bin).
-- Closed-beta reset: initialize empty telemetry tables; do not import legacy data.
-- Rebuild aggregate_dirty_days as an empty target table; retain existing authentication/device structures.
-- All UTC millisecond values are supplied by the server transaction code.
-- Do not execute this file against a live database.

"""
SEED = """-- Only in a newly created telemetry_models table; id 0 is not used with MySQL AUTO_INCREMENT.
INSERT INTO telemetry_models(id,created_at,updated_at,provider_id,model_id)
VALUES(1,CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS UNSIGNED),
         CAST(UNIX_TIMESTAMP(CURRENT_TIMESTAMP(3))*1000 AS UNSIGNED),'unknown','unknown');
"""


def validate():
    assert len(TABLES) == 10
    count = 0
    constraint_names = []
    for name, _, sql in TABLES:
        columns = re.findall(r'^ ([a-z][a-z0-9_]*) (.+)$', sql, re.M)
        assert [c[0] for c in columns[:5]] == ['id', 'created_at', 'updated_at', 'delete_at', 'extra'], name
        assert 'PRIMARY KEY (id)' in sql
        assert all(" COMMENT '" in spec for _, spec in columns), name
        count += len(columns)
        assert not re.search(r'CREATE INDEX.*WHERE', sql)
        for key_name in re.findall(r'(?:KEY|CONSTRAINT) ([a-z_]+)', sql):
            assert len(key_name) <= 64, key_name
        constraint_names.extend(re.findall(r'CONSTRAINT ([a-z_]+)', sql))
        for index_columns in re.findall(r'(?:KEY \w+|PRIMARY KEY) \(([^)]+)\)', sql):
            assert all(c in {f[0] for f in columns} for c in index_columns.split(',')), (name, index_columns)
    assert len(constraint_names) == len(set(constraint_names))
    assert 'account_id' not in ''.join(t[2] for t in TABLES)
    assert not any(token in ''.join(t[2] for t in TABLES) for token in ('events_from_day','ingest_mode','CREATE TABLE installations'))
    for theme in ('harness', 'model', 'skill', 'cost'):
        local_name = theme + '_metrics'
        parsed = {field for field, _, _ in local_business_columns(local_name)}
        body = re.search(rf'CREATE TABLE {local_name} \((.*?)\n\) STRICT;', LOCAL, re.S).group(1)
        physical = set(re.findall(r'^ ([a-z_]+) (?:INTEGER|TEXT|BLOB)\b', body, re.M))
        assert parsed == physical - {'id', 'created_at', 'updated_at', 'delete_at', 'extra'}, local_name
        server_body = next(sql for name, _, sql in TABLES if name == 'telemetry_' + local_name)
        server_fields = set(re.findall(r'^ ([a-z_]+) ', server_body, re.M))
        assert server_fields == physical | {'installation_id'}, local_name
    names = {name for name, _, _ in TABLES}
    for name, _, sql in TABLES:
        for target in re.findall(r'REFERENCES ([a-z_]+)\(', sql):
            assert target in {'users','installations'} or target in names, (name,target)
    return count


VIEWS = '\n'.join(f"CREATE OR REPLACE VIEW bound_telemetry_{theme}_metrics AS SELECT m.*,i.user_id FROM telemetry_{theme}_metrics m JOIN installations i ON i.installation_id=m.installation_id WHERE i.installation_status <> 'revoked' AND i.revoked_at IS NULL;" for theme in ('harness','model','skill','cost'))

if __name__ == '__main__':
    count = validate()
    sql = HEADER + '\n'.join(t[2] for t in TABLES) + '\n' + SEED + '\n' + VIEWS + '\n'
    (ROOT / 'event-pipeline-server-schema-v1.mysql.sql').write_text(sql, encoding='utf-8', newline='\n')
    lines = [
        '# 服务端事件流水线完整 DDL v1', '',
        '状态：2026-09-12 修订；设备归属变更在 0014_device_owned_telemetry.sql 实施，尚未发布。内测旧采集/统计数据不迁移。共 10 张目标表：9 张新事件业务表及空表重建的 aggregate_dirty_days；均采用 id 主键和 created_at、updated_at、delete_at、extra 公共字段。installations 继续复用现有结构，不为历史兼容新增字段或改主键。', '',
        '[完整技术方案](event-pipeline-refactor-technical-plan-v1.md) · [SQL 文件](event-pipeline-server-schema-v1.mysql.sql) · [本地 DDL](event-pipeline-ddl-v3.md)', '',
        '## 执行边界和类型', '',
        'SQL 以 MySQL 8.4/InnoDB 为设计基线，依赖现有 users.user_id 和 installations.installation_id（CHAR(30) ASCII ascii_bin）唯一身份。这里给出空表初始化的完整目标 CREATE TABLE，不执行旧业务记录转换。账号、会话、设备及认证设施保留；排行榜和 community 复用结构/刷新机制但清空旧统计与发布待办，旧快照退出读写链路；不在本方案复制整套认证系统。', '',
        '软件版本发布设施不变：desktop_releases、desktop_release_channels、desktop_release_publication 的结构与数据均保留，不纳入本次统计重置。现有 stable.json 生成、版本检查和安装包发布流程继续使用；统计 outbox 清理不包含软件版本发布状态。', '',
        '服务器管理时间和事件时间均用 BIGINT UNSIGNED UTC 毫秒；业务日用 DATE；固定摘要 BINARY(32)、租约 BINARY(16)、短标识 VARCHAR/CHAR、扩展与分组数据原生 JSON。大 Token/金额累计用 DECIMAL(38,0)，其他计数用 BIGINT UNSIGNED；业务代码受检运算，不允许负差额落库或隐式浮点转换。所有索引均围绕已知查询/唯一性/外键，不建立通用 JSON GIN 或每个状态独立索引。', '',
        'extra 仅放可选扩展；必需字段不能藏到其中。status_json 是各自统计完成状态唯一来源，任务表不重复存完成状态。delete_at 不释放任何业务唯一键；软删不自动撤销统计，必须由业务事务处理。', '',
        'MySQL 不提供 SQLite 式部分索引；due 查询以 consumer/delete_at/runnable_at 定位，租约回收包含软删行。外键列的辅助索引已明确列出，避免把 InnoDB 自动创建的索引漏算。字段范围、隐私白名单、JSON 类型和状态转换仍必须经过类型化 API 校验，DDL 不等于完整协议验证器。', '',
        '模型 provider_id/model_id 使用 utf8mb4_0900_bin，保持大小写和尾部空格区分；入口拒绝空或带不合法空白的标识。设备和用户自然 ID 的 ASCII 定义与既有 users 外键一致。事件和设备统计不保存 user_id；查询视图关联 installations 的当前有效绑定，用户汇总和缓存仍使用 user_id。', '',
        '## 表目录', '',
        '| 表 | 用途 |', '| --- | --- |',
    ]
    lines += [f'| {name} | {title} |' for name, title, _ in TABLES]
    for name, title, body in TABLES:
        lines += ['', f'## {name}', '', title + '。', '', '```sql', body.rstrip(), '```']
    lines += ['', '## 当前绑定的用户统计视图', '', '视图不复制数据、不增加实体表；解绑立即排除，重新绑定计入新账号。', '', '```sql', VIEWS, '```', '', '## 空表初始化要求', '', '```sql', SEED.rstrip(), '```', '',
              'unknown 必须在任何事件写入之前初始化。先停止旧接收、聚合和缓存发布工作器，再按外键依赖清理旧统计业务数据并创建空目标表；不复制旧明细、日快照、统计或任务。installations 的身份、主键、注册时间沿用现表，账号和设备数据不属于本次统计重置范围。初始化完成后重复启动不得再清库。', '',
              'aggregate_dirty_days 按目标结构空表重建，不迁移旧 claim_token、租约、版本或时间字段；旧消费者同时停用。新任务领取时把 next_attempt_at 置 NULL，执行期间新变更只增加 dirty_version；完成只确认领取版本 v，若 dirty_version>v 则重排，不能混用旧 ClearAggregateDirtyDaysTx。', '',
              '服务器事件不直接照搬客户端 created_at+14 天硬 TTL。服务端保留事件身份及统计完成状态，保证任意历史重建仍能去重；不能按 occurred_at 窗口清掉已计数身份。模型/技能软删标签不能让历史外键失效；物理删除账号/设备数据遵循依赖顺序和服务端授权归属。', '',
              '## 本轮验证', '',
              f'生成器结构检查：10 张表、{count} 个有 COMMENT 的字段、每表五个公共字段及 id 主键、索引字段存在、显式外键名称不冲突，且不存在逐设备协议切换字段。与本地四个统计主题共享字段来源，防止两端漏列。该检查不执行 MySQL SQL，不验证锁、优化器、真实外键或性能。', '',
              '本轮另在云端隔离测试 schema 验证了实际迁移、并发幂等、历史重建和解绑/重新绑定。此生成器自身仍仅做结构检查，不能代替真实数据库测试。', '']
    (ROOT / 'event-pipeline-server-ddl-v1.md').write_text('\n'.join(lines), encoding='utf-8', newline='\n')
    print(f'Generated 10 MySQL target tables, {count} commented fields; structural validation only, no database connection.')
