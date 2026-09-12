"""Exercise the proposed local schema in memory; no production database access."""
from pathlib import Path
import hashlib
import json
import sqlite3

ROOT = Path(__file__).resolve().parent
NOW = 1800000000000
TTL = 14 * 86400000

def digest(value):
    return hashlib.sha256(str(value).encode()).digest()


def run():
    db = sqlite3.connect(':memory:')
    db.executescript((ROOT/'statistics-schema-v3.sqlite.sql').read_text(encoding='utf-8'))
    for n in range(3):
        db.execute("INSERT INTO collection_sources(id,created_at,updated_at,harness_id,source_key,source_kind,locator_ref,stream_key,cursor_kind,cursor_json,decoder_state_version,decoder_state_json,observed_boundary_json) VALUES(?,?,?, ?,?,'jsonl','fixture','main','byte_offset','{}',1,'{}','{}')", (n+1,NOW,NOW,f'h{n}',digest(n)))
    for n in range(1,9):
        db.execute('INSERT INTO model_dimensions(id,created_at,updated_at,provider_id,model_id) VALUES(?,?,?,?,?)', (n,NOW,NOW,f'p{n%3}',f'm{n}'))
    payload = json.dumps({'usage': {'token_total': 10}, 'meta': {'accuracy': 'exact', 'time_source': 'source_record'}})
    for n in range(1,4001):
        db.execute("INSERT INTO events(created_at,updated_at,event_id,fact_key,fact_revision,collection_source_id,harness_id,event_type,schema_version,metric_semantics_version,content_hash,occurred_at,payload_json) VALUES(?,?,?,?,1,?,?,'usage',1,1,?,?,?)", (NOW+n,NOW+n,digest(n),digest(n),n%3+1,f'h{n%3}',digest(n),NOW-1000+n,payload))
        db.execute("INSERT INTO processing_tasks(created_at,updated_at,event_row_id,consumer,runnable_at) VALUES(?,?,?,'upload',?)", (NOW+n,NOW+n,n,NOW+n))
        for family in ['harness','model']:
            dimension, placeholder, args = (',model_key',',?',(n%8+1,)) if family=='model' else ('','',())
            db.execute(f"INSERT OR IGNORE INTO {family}_metrics(created_at,updated_at,grain,bucket_start,harness_id{dimension},metric_semantics_version) VALUES(?,?,'day',?,?{placeholder},1)", (NOW,NOW,NOW+(n//12)*86400000,f'h{n%3}',*args))
    db.execute('UPDATE events SET delete_at=?,updated_at=? WHERE id=1', (NOW+5000,NOW+5000))
    db.execute('UPDATE events SET session_key=?,cost_scope_key=? WHERE id BETWEEN 1 AND 40', (digest('session'),digest('cost')))
    db.execute('UPDATE processing_tasks SET delete_at=?,updated_at=?,runnable_at=NULL WHERE event_row_id=1', (NOW+5000,NOW+5000))
    db.commit()
    queries = {
        'expiry includes deleted events': ('SELECT id FROM events WHERE expire_at<=? ORDER BY expire_at,id LIMIT 200', (NOW+TTL+5000,)),
        'live recent detail': ('SELECT id FROM events WHERE delete_at IS NULL AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at DESC,id DESC LIMIT 100', (NOW-86400000,NOW+86400000)),
        'live harness detail': ("SELECT id FROM events WHERE delete_at IS NULL AND harness_id='h1' AND occurred_at>=? ORDER BY occurred_at DESC,id DESC LIMIT 100", (NOW-86400000,)),
        'session contribution scope': ("SELECT id,payload_json,status_json FROM events WHERE delete_at IS NULL AND harness_id='h1' AND session_key=? AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at,id", (digest('session'),NOW-86400000,NOW+86400000)),
        'cost contribution scope': ("SELECT id,payload_json,status_json FROM events WHERE delete_at IS NULL AND harness_id='h1' AND cost_scope_key=? AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at,id", (digest('cost'),NOW-86400000,NOW+86400000)),
        'due task candidates': ("SELECT event_row_id FROM processing_tasks WHERE delete_at IS NULL AND consumer='upload' AND runnable_at IS NOT NULL AND runnable_at<=? ORDER BY runnable_at,event_row_id LIMIT 100", (NOW+5000,)),
        'bounded JSON status lookup': ("SELECT id FROM events WHERE id IN (1,2,3,4,5) AND delete_at IS NULL AND expire_at>? AND json_extract(status_json,'$.upload') IN(0,1)", (NOW+5000,)),
        'lease recovery including deleted tasks': ("SELECT event_row_id FROM processing_tasks WHERE consumer='upload' AND lease_until IS NOT NULL AND lease_until<=? ORDER BY lease_until,event_row_id LIMIT 100", (NOW+5000,)),
        'daily token summary': ("SELECT SUM(exact_token_total+derived_token_total) FROM model_metrics WHERE delete_at IS NULL AND grain='day' AND bucket_start>=? AND bucket_start<?", (NOW,NOW+100000000000,)),
        'harness activity trend': ("SELECT bucket_start,active_duration_ms FROM harness_metrics WHERE delete_at IS NULL AND grain='day' AND harness_id='h1' AND bucket_start>=? ORDER BY bucket_start", (NOW,)),
        'model token trend': ("SELECT bucket_start,exact_token_total FROM model_metrics WHERE delete_at IS NULL AND grain='day' AND model_key=2 AND bucket_start>=? ORDER BY bucket_start", (NOW,)),
    }
    lines = ['# SQLite 索引与公共字段验证', '', '4000 条内存合成事件；不是磁盘性能或 MySQL 并发验收。所有本地查询不依赖账号。队列先有界选择任务，再按选中事件主键核验 JSON 状态，避免全表解析状态。', '']
    for name,(query,args) in queries.items():
        plan = [r[3] for r in db.execute('EXPLAIN QUERY PLAN '+query,args)]
        assert any('INDEX' in r or 'INTEGER PRIMARY KEY' in r for r in plan), (name,plan)
        assert not any('TEMP B-TREE FOR ORDER BY' in r for r in plan), (name,plan)
        expected_index = {'session contribution scope': 'idx_events_session_scope', 'cost contribution scope': 'idx_events_cost_scope'}.get(name)
        if expected_index:
            assert any(expected_index in row for row in plan), (name,plan)
            ids = {r[0] for r in db.execute(query,args)}
            assert ids and 1 not in ids and max(ids)<=40, (name,ids)
        lines += [f'## {name}', '', '```sql', query, '```', '', '```text', *plan, '```', '']
    assert 1 not in {r[0] for r in db.execute(queries['bounded JSON status lookup'][0],queries['bounded JSON status lookup'][1])}
    assert 1 not in {r[0] for r in db.execute(queries['due task candidates'][0],queries['due task candidates'][1])}
    assert db.execute('SELECT id FROM events WHERE id=1 AND expire_at<=?',(NOW+TTL+1,)).fetchone() == (1,)
    db.execute('DELETE FROM events WHERE id=1')
    assert not db.execute('SELECT 1 FROM processing_tasks WHERE event_row_id=1').fetchone()
    for grain in ['hour','day','month']:
        db.execute('INSERT INTO harness_metrics(created_at,updated_at,grain,bucket_start,harness_id,active_duration_ms,metric_semantics_version) VALUES(?,?,?,?,?,?,1)', (NOW,NOW,grain,NOW,'rollup',7))
    assert db.execute("SELECT SUM(active_duration_ms) FROM harness_metrics WHERE delete_at IS NULL AND harness_id='rollup' AND grain='day'").fetchone()[0] == 7
    for model,exact,derived,requests in [(0,30,0,1),(1,70,20,2)]:
        db.execute("INSERT INTO model_metrics(created_at,updated_at,grain,bucket_start,harness_id,model_key,exact_token_total,derived_token_total,model_request_count,metric_semantics_version) VALUES(?,?,'day',?,'rollup',?,?,?,?,1)", (NOW,NOW,NOW,model,exact,derived,requests))
    assert db.execute("SELECT SUM(exact_token_total+derived_token_total),SUM(model_request_count) FROM model_metrics WHERE delete_at IS NULL AND harness_id='rollup' AND grain='day'").fetchone() == (120,3)
    for model,currency,reported,calculated in [(0,'USD',100,0),(1,'USD',0,200),(1,'CNY',700,0)]:
        db.execute("INSERT INTO cost_metrics(created_at,updated_at,grain,bucket_start,harness_id,model_key,currency,reported_cost_units,estimated_cost_units,metric_semantics_version) VALUES(?,?,'day',?,'rollup',?,?,?,?,1)", (NOW,NOW,NOW,model,currency,reported,calculated))
    assert db.execute("SELECT currency,SUM(reported_cost_units+estimated_cost_units) FROM cost_metrics WHERE delete_at IS NULL AND harness_id='rollup' AND grain='day' GROUP BY currency ORDER BY currency").fetchall() == [('CNY',700),('USD',300)]
    db.execute("UPDATE model_metrics SET delete_at=?,updated_at=? WHERE harness_id='rollup' AND model_key=0", (NOW+1,NOW+1))
    assert db.execute("SELECT SUM(exact_token_total+derived_token_total) FROM model_metrics WHERE delete_at IS NULL AND harness_id='rollup' AND grain='day'").fetchone()[0] == 90
    try:
        db.execute("INSERT INTO model_metrics(created_at,updated_at,grain,bucket_start,harness_id,model_key,metric_semantics_version) VALUES(?,?,'day',?,'rollup',0,1)",(NOW,NOW,NOW))
    except sqlite3.IntegrityError:
        pass
    else:
        raise AssertionError('soft deleted metric bucket was recreated')
    for statement in [
        "UPDATE model_metrics SET usage_observed_count=1,token_total_known_count=2 WHERE harness_id='h1'",
        "UPDATE model_metrics SET cache_eligible_input_tokens=1,cache_eligible_read_tokens=2 WHERE harness_id='h1'",
    ]:
        try:
            db.execute(statement)
        except sqlite3.IntegrityError:
            pass
        else:
            raise AssertionError(statement)
    assert not db.execute('PRAGMA foreign_key_check').fetchall()
    lines += ['## 验证结论', '',
              f'- {len(queries)} 条查询使用索引或整数主键，无额外 ORDER BY 临时排序；时长/费用关联查询命中各自范围索引。',
              '- 软删事件不进入业务读取和领取，物理 TTL 仍能找到它，删除事件级联删除任务。',
              '- 三个 grain 同表共存，单粒度查询隔离；软删除不释放统计桶业务唯一键。',
              '- unknown 30 Token + 已知模型 90 Token = 工具 120 Token、3 次请求；不同币种费用分开汇总。',
              '- 模型统计已软删桶被普通统计读取排除；该操作仅测试过滤，实际删除业务还需同步贡献状态。',
              '- 统计覆盖数和缓存分子分母约束有效；公共字段、JSON 类型/整数精度及事件幂等由生成脚本另行校验。',
              '- 尚未实现业务 worker；这些检查不能替代时长选择、费用替换、认证切换和故障注入验收。', '']
    (ROOT/'statistics-index-validation-v3.md').write_text('\n'.join(lines),encoding='utf-8')
    print(f'Validated 4000 synthetic events, {len(queries)} indexed access paths, soft-delete visibility/uniqueness, hard TTL, grain isolation and known/unknown model rollups.')
    db.close()


if __name__=='__main__':
    run()
