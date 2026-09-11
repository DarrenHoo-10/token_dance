# Protocol v2 ACK → `status_json.upload` 映射

客户端仅在逐事件 ACK 且身份/`contentHash`/当前租约一致时更新本地 `events.status_json.upload`。  
请求级 401/403/429/5xx 或超时**不**推断单条结果。

| ACK `result` | upload | 客户端动作 |
| --- | ---: | --- |
| `accepted` | 3 | 删除上传任务；仅表示云端已持久化 |
| `duplicate` | 3 | 同 `accepted`（同 hash 重放） |
| `discarded` | 4 | 结束上传任务（`outside_window` / `deleted` 等）；本地统计状态不变 |
| `retry` | 1 | 更新任务 `runnable_at`；不影响同批其他事件 |
| `blocked` | 5 | 等待兼容服务/客户端更新，保留至 TTL |
| `conflict` | 6 | 隔离并保留错误码，停止自动重试 |
| `invalid` | 6 | 同 `conflict`（非法业务字段/负计数等） |

补充约定：

- 仅服务端 **COMMIT 成功后** 才返回 `accepted` / `duplicate`。
- 同 `eventId` 异 `contentHash` → `conflict`；同 hash 重放 → `duplicate`。
- `GET /v2/telemetry/capabilities` 返回支持的 `schemaVersion` / `metricSemanticsVersion`、批次上限、`serverTimeMs`、`eventReceiveLowerBoundMs`；客户端可预过滤，但只有 ACK 才能置 upload=3。
