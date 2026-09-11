# SQLite 索引与公共字段验证

4000 条内存合成事件；不是磁盘性能或 MySQL 并发验收。所有本地查询不依赖账号。队列先有界选择任务，再按选中事件主键核验 JSON 状态，避免全表解析状态。

## expiry includes deleted events

```sql
SELECT id FROM events WHERE expire_at<=? ORDER BY expire_at,id LIMIT 200
```

```text
SEARCH events USING INDEX idx_events_expire (expire_at<?)
```

## live recent detail

```sql
SELECT id FROM events WHERE delete_at IS NULL AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at DESC,id DESC LIMIT 100
```

```text
SEARCH events USING INDEX idx_events_time (occurred_at>? AND occurred_at<?)
```

## live harness detail

```sql
SELECT id FROM events WHERE delete_at IS NULL AND harness_id='h1' AND occurred_at>=? ORDER BY occurred_at DESC,id DESC LIMIT 100
```

```text
SEARCH events USING INDEX idx_events_harness_time (harness_id=? AND occurred_at>?)
```

## session contribution scope

```sql
SELECT id,payload_json,status_json FROM events WHERE delete_at IS NULL AND harness_id='h1' AND session_key=? AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at,id
```

```text
SEARCH events USING INDEX idx_events_session_scope (harness_id=? AND session_key=? AND occurred_at>? AND occurred_at<?)
```

## cost contribution scope

```sql
SELECT id,payload_json,status_json FROM events WHERE delete_at IS NULL AND harness_id='h1' AND cost_scope_key=? AND occurred_at>=? AND occurred_at<? ORDER BY occurred_at,id
```

```text
SEARCH events USING INDEX idx_events_cost_scope (harness_id=? AND cost_scope_key=? AND occurred_at>? AND occurred_at<?)
```

## due task candidates

```sql
SELECT event_row_id FROM processing_tasks WHERE delete_at IS NULL AND consumer='upload' AND runnable_at IS NOT NULL AND runnable_at<=? ORDER BY runnable_at,event_row_id LIMIT 100
```

```text
SEARCH processing_tasks USING INDEX idx_tasks_due (consumer=? AND runnable_at>? AND runnable_at<?)
```

## bounded JSON status lookup

```sql
SELECT id FROM events WHERE id IN (1,2,3,4,5) AND delete_at IS NULL AND expire_at>? AND json_extract(status_json,'$.upload') IN(0,1)
```

```text
SEARCH events USING INTEGER PRIMARY KEY (rowid=?)
```

## lease recovery including deleted tasks

```sql
SELECT event_row_id FROM processing_tasks WHERE consumer='upload' AND lease_until IS NOT NULL AND lease_until<=? ORDER BY lease_until,event_row_id LIMIT 100
```

```text
SEARCH processing_tasks USING COVERING INDEX idx_tasks_lease (consumer=? AND lease_until>? AND lease_until<?)
```

## daily token summary

```sql
SELECT SUM(exact_token_total+derived_token_total) FROM model_metrics WHERE delete_at IS NULL AND grain='day' AND bucket_start>=? AND bucket_start<?
```

```text
SEARCH model_metrics USING INDEX sqlite_autoindex_model_metrics_1 (grain=? AND bucket_start>? AND bucket_start<?)
```

## harness activity trend

```sql
SELECT bucket_start,active_duration_ms FROM harness_metrics WHERE delete_at IS NULL AND grain='day' AND harness_id='h1' AND bucket_start>=? ORDER BY bucket_start
```

```text
SEARCH harness_metrics USING INDEX idx_harness_filter (grain=? AND harness_id=? AND bucket_start>?)
```

## model token trend

```sql
SELECT bucket_start,exact_token_total FROM model_metrics WHERE delete_at IS NULL AND grain='day' AND model_key=2 AND bucket_start>=? ORDER BY bucket_start
```

```text
SEARCH model_metrics USING INDEX idx_model_lookup (grain=? AND model_key=? AND bucket_start>?)
```

## 验证结论

- 11 条查询使用索引或整数主键，无额外 ORDER BY 临时排序；时长/费用关联查询命中各自范围索引。
- 软删事件不进入业务读取和领取，物理 TTL 仍能找到它，删除事件级联删除任务。
- 三个 grain 同表共存，单粒度查询隔离；软删除不释放统计桶业务唯一键。
- unknown 30 Token + 已知模型 90 Token = 工具 120 Token、3 次请求；不同币种费用分开汇总。
- 模型统计已软删桶被普通统计读取排除；该操作仅测试过滤，实际删除业务还需同步贡献状态。
- 统计覆盖数和缓存分子分母约束有效；公共字段、JSON 类型/整数精度及事件幂等由生成脚本另行校验。
- 尚未实现业务 worker；这些检查不能替代时长选择、费用替换、认证切换和故障注入验收。
