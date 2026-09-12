"""Render and validate the local SQLite design in memory; never open user data."""
from pathlib import Path
import json
import re
import sqlite3

ROOT = Path(__file__).resolve().parent
COMMON = ['id', 'created_at', 'updated_at', 'delete_at', 'extra']
DESCRIPTIONS = {
    'collection_sources': ('采集单元与进度', '来源、独立读取流、游标、解析基线与采集租约同一行；忽略记录和到期未完成事件只存累计数。', '保留来源与读取进度，不随 14 天事件明细清理；正常重启从游标续读。'),
    'model_dimensions': ('模型维度', 'provider_id + model_id 唯一；id=0 为 unknown，初始化显式提供创建和更新时间。', '被引用期间保留；软删除不释放模型身份。'),
    'skill_dimensions': ('技能维度', '匿名 skill_key 为身份，public_name 为展示标签；改标签不改变调用身份。', '被引用期间保留；软删除不释放技能身份。'),
    'events': ('事件细节', '23 个字段：公共字段、身份/来源/版本、时间与关联列、payload_json、status_json。不持久化账号，不复制整份 envelope。', '首次 created_at 起 14 天物理清理，包括已软删除明细。'),
    'processing_tasks': ('执行队列', 'UNIQUE(event_row_id,consumer)；只存租约和重试控制，四个完成状态仅在事件 status_json 中。', '完成或事件到期后物理删除；软删除表示停止调度。'),
    'harness_metrics': ('工具活动统计', '会话、轮次、工具/技能次数、消息、代码和活动时长；Token 与模型请求从 model_metrics 汇总。', '长期统计；不按事件的 14 天规则清理。'),
    'model_metrics': ('模型用量统计', '规范化 Token、覆盖和请求数；按模型求和得到工具用量，必须包含 unknown。', '长期统计。'),
    'skill_metrics': ('技能统计', '调用、成功/失败、耗时与覆盖；比例和活跃天数由基础值计算。', '长期统计。'),
    'cost_metrics': ('费用统计', '同一有效金额只归一个模型或 unknown 和币种；按模型求和得到工具金额，不重复保存工具总额。', '长期统计。'),
    'bucket_entity_state': ('会话与轮次去重状态', '保存各 grain 中的实体身份和必要标志，用于唯一会话数、消息数以及时长优先级。', '依照去重保证保留；不能随事件 TTL 删除或自动软删除。'),
}


def validate(ddl):
    db = sqlite3.connect(':memory:')
    db.executescript(ddl)
    names = re.findall(r'^CREATE TABLE (\w+)', ddl, re.M)
    assert set(names) == set(DESCRIPTIONS)
    count = 0
    for name in names:
        fields = list(db.execute(f'PRAGMA table_xinfo({name})'))
        assert [r[1] for r in fields[:5]] == COMMON
        assert [r[1] for r in fields if r[5]] == ['id']
        assert not {'account_id', 'first_created_at', 'envelope_json', 'hour_status', 'day_status', 'month_status', 'upload_status'} & {r[1] for r in fields}
        count += len(fields)
    declarations = [line for line in ddl.splitlines() if re.match(r'^ \w+ (INTEGER|TEXT|BLOB)\b', line)]
    assert len(declarations) == count and all(' -- ' in line and line.split(' -- ', 1)[1].strip() for line in declarations)
    assert len(list(db.execute('PRAGMA table_xinfo(events)'))) == 23
    assert 'account_id' not in ddl
    source_sql = "INSERT INTO collection_sources(id,created_at,updated_at,harness_id,source_key,source_kind,locator_ref,stream_key,cursor_kind,cursor_json,decoder_state_version,decoder_state_json,observed_boundary_json,next_poll_at) VALUES(?,1000,1000,'h',?,'sqlite','fixture',?,'sqlite_change','{}',1,'{}','{}',1000)"
    for row_id, stream in [(1, 'usage'), (2, 'tools')]:
        db.execute(source_sql, (row_id, b'a'*32, stream))
    db.execute("UPDATE collection_sources SET cursor_json='{\"rowid\":42}',commit_seq=1,updated_at=1001 WHERE id=1 AND commit_seq=0")
    assert db.execute('SELECT cursor_json,commit_seq FROM collection_sources WHERE id=2').fetchone() == ('{}', 0)
    assert db.execute('UPDATE collection_sources SET commit_seq=2 WHERE id=1 AND commit_seq=0').rowcount == 0
    assert len(list(db.execute('PRAGMA index_list(collection_sources)'))) == 4
    source_queries = [
        ("SELECT id FROM collection_sources WHERE harness_id='h' AND source_key=? AND stream_key='usage'", (b'a'*32,)),
        ("SELECT id FROM collection_sources WHERE delete_at IS NULL AND enabled=1 AND harness_id='h' AND next_poll_at IS NOT NULL AND next_poll_at<=2000 ORDER BY next_poll_at,id LIMIT 8", ()),
        ("SELECT id FROM collection_sources WHERE lease_until IS NOT NULL AND lease_until<=2000 ORDER BY lease_until,id LIMIT 8", ()),
    ]
    for query, params in source_queries:
        plan = [r[3] for r in db.execute('EXPLAIN QUERY PLAN '+query, params)]
        assert any('INDEX' in r for r in plan) and not any('TEMP B-TREE' in r for r in plan), plan
    payload = {'usage': {'token_total': 9007199254740993}, 'meta': {'accuracy': 'exact', 'time_source': 'source_record'}}
    event_sql = "INSERT INTO events(id,created_at,updated_at,event_id,fact_key,fact_revision,collection_source_id,harness_id,event_type,schema_version,metric_semantics_version,content_hash,occurred_at,payload_json) VALUES(?,1000,1000,?,?,1,1,'h','usage',1,1,?,500,?)"
    db.execute(event_sql, (1, b'b'*32, b'c'*32, b'd'*32, json.dumps(payload)))
    db.execute("INSERT INTO processing_tasks(created_at,updated_at,event_row_id,consumer,runnable_at) VALUES(1000,1000,1,'upload',1000)")
    assert db.execute("SELECT json_extract(payload_json,'$.usage.token_total') FROM events WHERE id=1").fetchone()[0] == 9007199254740993
    assert db.execute('SELECT created_at,expire_at FROM events WHERE id=1').fetchone() == (1000, 1209601000)
    invalid = {
        'missing creation time': "INSERT INTO model_dimensions(updated_at,provider_id,model_id) VALUES(0,'p','m')",
        'extra must be object': "UPDATE events SET extra='[]' WHERE id=1",
        'extra must be valid JSON': "UPDATE events SET extra='broken' WHERE id=1",
        'negative delete time': 'UPDATE events SET delete_at=-1 WHERE id=1',
        'source/harness mismatch': "UPDATE events SET harness_id='wrong' WHERE id=1",
        'short event hash': "UPDATE events SET event_id=X'01' WHERE id=1",
        'task foreign key': 'UPDATE processing_tasks SET event_row_id=999 WHERE event_row_id=1',
        'duplicate consumer': "INSERT INTO processing_tasks(created_at,updated_at,event_row_id,consumer) VALUES(1000,1000,1,'upload')",
        'source lease pair': "UPDATE collection_sources SET lease_token='x' WHERE id=1",
        'task lease pair': "UPDATE processing_tasks SET lease_token='x' WHERE event_row_id=1",
        'missing status path': "UPDATE events SET status_json=json_remove(status_json,'$.day') WHERE id=1",
        'null status': "UPDATE events SET status_json=json_set(status_json,'$.day',NULL) WHERE id=1",
        'string status': "UPDATE events SET status_json=json_set(status_json,'$.day','3') WHERE id=1",
        'out of range status': "UPDATE events SET status_json=json_set(status_json,'$.day',7) WHERE id=1",
        'estimated usage': "UPDATE events SET payload_json=json_set(payload_json,'$.meta.accuracy','estimated') WHERE id=1",
        'negative token': "UPDATE events SET payload_json=json_set(payload_json,'$.usage.token_total',-1) WHERE id=1",
        'string token': "UPDATE events SET payload_json=json_set(payload_json,'$.usage.token_total','1') WHERE id=1",
        'real token': "UPDATE events SET payload_json=json_set(payload_json,'$.usage.token_total',1.5) WHERE id=1",
        'boolean token': "UPDATE events SET payload_json=json_set(payload_json,'$.usage.token_total',json('true')) WHERE id=1",
        'overflow token': "UPDATE events SET payload_json=json_set(payload_json,'$.usage.token_total',json('9223372036854775808')) WHERE id=1",
        'cache relationship': "UPDATE events SET payload_json=json_set(payload_json,'$.usage.input_context_tokens',10,'$.usage.cache_read_tokens',11) WHERE id=1",
        'missing cost currency': "UPDATE events SET payload_json=json_set(payload_json,'$.cost',json('{\"units\":1,\"source\":\"provider_reported\"}')) WHERE id=1",
        'created time overflow': 'UPDATE events SET created_at=9223372036854775807 WHERE id=1',
    }
    for label, statement in invalid.items():
        try:
            db.execute(statement)
        except sqlite3.IntegrityError:
            pass
        else:
            raise AssertionError(label)
    db.execute("UPDATE events SET payload_json=json_set(payload_json,'$.usage.token_total',0,'$.cost',json('{\"units\":0,\"currency\":\"USD\",\"source\":\"provider_reported\"}')) WHERE id=1")
    db.execute("UPDATE events SET status_json=json_set(status_json,'$.hour',3),updated_at=1001 WHERE id=1")
    db.execute("UPDATE events SET status_json=json_set(status_json,'$.upload',3),updated_at=1002 WHERE id=1")
    assert json.loads(db.execute('SELECT status_json FROM events WHERE id=1').fetchone()[0]) == {'hour': 3, 'day': 0, 'month': 0, 'upload': 3}
    db.execute('UPDATE events SET delete_at=2000,updated_at=2000 WHERE id=1')
    assert db.execute('SELECT COUNT(*) FROM events WHERE delete_at IS NULL').fetchone()[0] == 0
    assert db.execute('SELECT id FROM events WHERE expire_at<=1209601000').fetchone()[0] == 1
    # Recovery must still see leases left on logically deleted rows.
    db.execute("UPDATE collection_sources SET delete_at=2000,updated_at=2000,next_poll_at=NULL,lease_token='source-lease',lease_until=1800 WHERE id=1")
    assert db.execute(source_queries[2][0]).fetchall() == [(1,)]
    assert 1 not in {r[0] for r in db.execute(source_queries[1][0])}
    db.execute("UPDATE processing_tasks SET delete_at=2000,updated_at=2000,runnable_at=NULL,lease_token='task-lease',lease_until=1800 WHERE event_row_id=1")
    assert db.execute("SELECT event_row_id FROM processing_tasks WHERE consumer='upload' AND lease_until IS NOT NULL AND lease_until<=2000").fetchall() == [(1,)]
    try:
        db.execute(event_sql, (2, b'b'*32, b'c'*32, b'd'*32, json.dumps(payload)))
    except sqlite3.IntegrityError:
        pass
    else:
        raise AssertionError('soft deletion released event identity')
    db.execute('DELETE FROM events WHERE expire_at<=1209601000')
    assert not db.execute('SELECT 1 FROM processing_tasks').fetchone()
    assert db.execute('SELECT cursor_json,commit_seq FROM collection_sources WHERE id=1').fetchone() == ('{"rowid":42}',1)
    assert 'event_identity_ledger' not in names
    assert not db.execute('PRAGMA foreign_key_check').fetchall()
    assert db.execute('PRAGMA integrity_check').fetchone()[0] == 'ok'
    db.close()
    return count, len(invalid)


def build():
    ddl = (ROOT/'event-pipeline-schema-v3.sqlite.sql').read_text(encoding='utf-8')
    count, invalid_count = validate(ddl)
    positions = list(re.finditer(r'^CREATE TABLE (\w+) \(', ddl, re.M))
    lines = [
        '# 本地事件流水线完整 DDL v3', '',
        f'状态：设计稿，未接入业务、未迁移真实数据库。当前共 10 张表、{count} 个字段；events 为 23 列。SQL 文件是唯一建表来源，本文件由脚本生成。', '',
        '[客户端与服务端完整技术方案](event-pipeline-refactor-technical-plan-v1.md) · [服务端完整 DDL](event-pipeline-server-ddl-v1.md)', '',
        '2026-09-12 已确认项目仍在内测，旧采集与统计数据不迁移。本次初始化空的新库，不复制旧事件、游标、任务或统计，也不保留旧库历史读取；认证、设备密钥和 Agent raw 不属于统计重置范围。以下为新链路正常运行后的表与保留规则，初始化完成后重启不能再次清库。', '',
        '## 所有表统一的五个字段', '',
        '| 字段 | SQLite 类型 | 含义 |', '| --- | --- | --- |',
        '| id | INTEGER PRIMARY KEY | 本地行主键；events 使用 AUTOINCREMENT，业务幂等仍由独立 UNIQUE 保证。 |',
        '| created_at | INTEGER NOT NULL | 首次创建的 UTC 毫秒时间，后续更新不改变；不用于替代事件发生时间。 |',
        '| updated_at | INTEGER NOT NULL | 最近实际更新的 UTC 毫秒时间；包括状态、租约、游标、软删除等变更，由 writer 同事务赋值。 |',
        '| delete_at | INTEGER NULL | 软删除时间，UTC 毫秒；NULL 表示未删除。按用户指定使用 delete_at，不改名为 deleted_at。 |',
        '| extra | TEXT NOT NULL DEFAULT 空对象 | 合法 JSON 对象；只容纳本地可选扩展信息，不承载必需业务字段、处理状态、账号或凭据，不默认上传。 |', '',
        '所有表将这五列放在最前面；每列均有中文 SQL 行注释。创建/更新/删除时间保持独立列，不放 JSON。金额和 Token 使用受检 64 位整数，金额单位为币种的 10^-8；未知允许缺失或 null，明确零值为 0。', '',
        '## 本地数据与账号分离', '',
        '本地事件、任务、统计及实体状态均不保存 account_id，也不将账号隐藏在 JSON 中。不增加认领、归属迁移或上传目标表；本机统计汇总本机采集数据。上传时才读取当前认证上下文，由服务端决定账号归属。已 ACK 事件不因换号重传，在途请求固定原认证上下文。', '',
        '全部本地唯一键、外键和索引已移除账号维度。processing_tasks.event_row_id 直接引用 events.id；原 UNIQUE(id,account_id) 和对应复合外键取消。服务端必须以稳定设备/事件身份防重，首次已接收事实不因换号再次计数或转移归属；现有服务端认证/设备绑定协议仍需在实施时对齐。', '',
        '## 事件字段聚类', '',
        'payload_json 按 usage、cost、code、activity、context、meta 分组，仅携带适用内容；status_json 独立保存 hour/day/month/upload 四个状态。保留身份、来源、时间和必要关联列，取消 envelope_json 与展开业务列的重复存储。上传从冻结事件头和 payload 确定性构造，不包含公共管理字段、extra 和 status_json，不在重试时重新估价或取文件时间。', '',
        'JSON 状态四个键必须存在，值为整数 0–6；3 表示该消费者完成，上传对应 ACK、统计对应已应用。使用 json_set 只修改本消费者路径，不能将 worker 读取的旧 JSON 整块写回。writer 同事务更新统计、实体状态、事件状态与 updated_at，并核验租约、事件未软删/未过期；完成后删除任务。', '',
        '每 5 秒从 processing_tasks 的到期索引有界领取，再按事件主键核验 JSON 状态；不对全部 events 做 JSON 状态扫描，不为四个状态各增一套索引。统计任务按类型读取 payload，页面读取有数值列的统计表。JSON 语法与关键取值由 DDL 校验，按 event_type 的完整结构、重复键、隐私白名单和规范化规则仍由类型化解码器在入库前校验。', '',
        '前轮 10 万条内存合成数据对比：取 500 条队列事件，数值状态列约 0.20ms、JSON 约 0.33ms；全表状态扫描约 2.60ms/24.81ms。该测试不含磁盘、WAL 与生产并发，只说明应保留队列索引访问路径，不代表生产吞吐。', '',
        '## 软删除、幂等与保留期', '',
        '- 常规列表、统计读取和到期领取只读取 delete_at IS NULL；来源/维度的身份查找和保留期内的事件去重检查包含软删除记录，历史引用不能因标签被软删除而断开。',
        '- 业务唯一键继续覆盖全部行，不加入 delete_at，也不改成仅未删除行唯一。同一事件、来源或统计桶不会因软删除得到第二个身份；普通采集/upsert 不得隐式复活已软删记录。',
        '- delete_at 不触发外键级联。逻辑删除事件若需要停止处理，writer 同事务取消其任务和租约；消费者回写再次检查事件和任务均未软删除。租约回收索引仍覆盖已软删的在途租约，不能漏掉清理。',
        '- 每表具备公共列不等于新增一套自动软删除业务。统计桶删除若需撤回贡献，必须连同相关状态处理；已贡献实体状态不执行按明细 TTL 的自动软删除。',
        '- events 到 created_at + 14 天物理删除，包括此前已经软删除的事件；任务随外键级联清理。updated_at/delete_at 不续期；到期未完成事件按来源累计一次，不伪造 ACK。',
        '- 删除 event_identity_ledger，不增加替代的永久事件身份记录。collection_sources 游标不随明细到期清理；正常采集按游标增量读取，保留期内由 events 唯一键防重。',
        '- 不统一为 updated_at、delete_at、extra 建索引。现有明细/到期热路径使用未删除部分索引；事件过期索引与租约回收索引保留软删除行。', '',
        '## 时间与类型', '',
        'occurred_at 独立于创建/更新时间。按源记录时间 → 同流前序有效时间 → 原始文件 mtime 选择，并在 payload_json.meta.time_source 标记依据。前序锚点仍随来源 decoder_state 和游标原子提交。首次只准入北京时间当天；已有事件跨日继续处理；保留期内重复读取先查 events，复用冻结时间。事件明细到期后不提供在原库中从头重放的保证；需要重新采集时采用重装并重新初始化本地数据的流程。详见 [事件时间规则](event-time-admission-v1.md)。', '',
        '服务端按事件 occurred_at 做时间范围过滤，重放时间、上传时间和本地 created_at 均不替代事件发生时间。正常重试沿用原 occurred_at 和 event_id；时间范围内的重复仍由服务端唯一键处理。完整技术方案建议服务端接收今天及此前 14 个北京时间日期，以覆盖本地 14 天重试；这是服务端建议策略，不改变本地 created_at 起算的 TTL，也不将最大已接收时间当成并发上传全部完成的水位。', '',
        'events 的 session_scope、cost_scope 两个部分索引用于时长权威替换和费用选择的关联范围读取，不为 JSON 字段逐项建索引。硬 TTL 清理必要旧贡献前，必须同事务取消相关未完成复合任务租约并阻塞，避免用残缺输入覆盖统计；具体事务边界见完整技术方案。', '',
        'SQLite STRICT 使用 INTEGER/TEXT/BLOB；摘要为固定 32 字节 BLOB，JSON 为 TEXT。SQLite 不支持列级 COMMENT，全部含义以同行 -- 注释保存。DDL 中的公共时间没有自动更新触发器，由统一 writer 维护；重复观察无实际变更时不更新 created_at/updated_at。', '',
        '## 表清单', '', '| 表 | 用途 | 保留规则 |', '| --- | --- | --- |',
    ]
    for match in positions:
        title, _, retention = DESCRIPTIONS[match[1]]
        lines.append(f'| {match[1]} | {title} | {retention} |')
    lines += ['', '## 建库设置', '', '```sql', ddl[:positions[0].start()].strip(), '```', '']
    for i, match in enumerate(positions):
        name = match[1]
        end = positions[i+1].start() if i+1 < len(positions) else len(ddl)
        title, purpose, retention = DESCRIPTIONS[name]
        lines += [f'## {i+1}. {name} — {title}', '', purpose, '', f'保留：{retention}', '', '```sql', ddl[match.start():end].strip(), '```', '']
        if name == 'collection_sources':
            lines += ['本表保留四个辅助索引：来源业务唯一键、来源/harness 外键所需 UNIQUE(id,harness_id)、未软删启用流的 due 索引、包含软删在途任务的 lease 回收索引。不单建 enabled 或 delete_at 索引。', '']
    lines += ['## 验证与相关方案', '',
              f'生成前执行全部 SQL：10 张表的五个公共字段、全部 {count} 个字段注释、无账号列、23 个 events 字段与 {invalid_count} 项非法写入拒绝检查；验证 JSON 路径更新、软删不释放幂等身份、物理 TTL 仍包含软删行、外键级联及来源索引。只访问内存 SQLite。', '',
              '[统计口径与索引](statistics-model-and-indexes-v3.md) · [精简审查](schema-simplification-review-v1.md) · [计算契约](metric-contract-v1.md) · [索引验证](statistics-index-validation-v3.md)', '']
    (ROOT/'event-pipeline-ddl-v3.md').write_text('\n'.join(lines), encoding='utf-8')
    (ROOT/'statistics-schema-v3.sqlite.sql').write_text('-- GENERATED from event-pipeline-schema-v3.sqlite.sql; do not edit.\n'+ddl, encoding='utf-8')
    print(f'Validated 10 tables, {count} commented fields, 23 event fields, 5 common fields per table; {invalid_count} invalid writes rejected; JSON, tombstone idempotency, TTL, source indexes and FK checks passed.')


if __name__ == '__main__':
    build()
