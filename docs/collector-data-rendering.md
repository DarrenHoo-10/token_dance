# Collector 数据读取与前端渲染

## 数据链路

TokenDance 用户页面不直接查询 `usage_events`。完整链路为：

1. Collector 将事件写入 `usage_events`，批次状态写入 `ingest_batches`。
2. Worker 根据 `user_id + occurred_date` 重建以下日聚合表：
   - `daily_user_agent_metrics`
   - `daily_user_agent_model_metrics`
   - `daily_skill_metrics`
3. 用户 API 从日聚合表读取数据；时间范围边界内尚未聚合的事件会从 `usage_events` 补齐，避免页面短暂少算。
4. 前端通过 `/api/v1/me/*` 接口渲染个人数据页。

主要接口：

| 页面区域 | 接口 | 数据来源 |
| --- | --- | --- |
| 十项核心指标 | `GET /api/v1/me/summary` | 用户/Agent 日聚合与边界事件 |
| Token 趋势 | `GET /api/v1/me/trends/tokens` | 用户/Agent 或模型日聚合 |
| Agent 构成 | `GET /api/v1/me/breakdowns/agents` | 用户/Agent 日聚合 |
| 模型筛选 | `GET /api/v1/me/filter-options` | 用户已有 Agent、Provider、Model |
| Skill 排行 | `GET /api/v1/me/skills` | Skill 日聚合 |
| 活跃日历 | `GET /api/v1/me/calendar` | 用户/Agent 日聚合 |

## 旧采集库回填

`cmd/import-collector` 用于把旧采集库中的已有数据一次性回填到 TokenDance 用户库。它具有以下行为：

- 源库只读，目标库使用单事务写入。
- 只复制源表和目标表共有的列。
- `event_pk` 与生成列由目标库重新生成。
- 将旧采集用户映射到指定 TokenDance 用户。
- 重复执行不会重复写入相同 Installation、Batch 或 Event。
- 回填完成后立即重建日聚合。

运行前设置：

```text
TOKENDANCE_SOURCE_MYSQL_DSN
TOKENDANCE_MYSQL_DSN
TOKENDANCE_SOURCE_USER_ID
TOKENDANCE_TARGET_USER_ID
TOKENDANCE_SOURCE_SCHEMA=tokenshow
TOKENDANCE_TARGET_SCHEMA=tokendance
```

然后在 `server` 目录运行：

```text
go run ./cmd/import-collector
```

只重建聚合、不重复扫描源表时增加：

```text
TOKENDANCE_BACKFILL_AGGREGATE_ONLY=true
```

## 本地端口

- `8080`：旧 Collector API，仅保留旧采集链路。
- `8081`：TokenDance 用户 API，同时提供 `/api/v1` 用户接口和 `/v1` 新采集接口。
- `3001`：前端开发服务，`/api/v1` 与 `/v1` 默认代理到 `8081`。

前端只有在显式设置 `VITE_ENABLE_MOCK_ANALYTICS=true` 时才展示 Mock 分析数据；默认严格显示 API 返回值，包括真实的零值和空状态。
