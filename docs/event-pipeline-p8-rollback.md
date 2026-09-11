# Event pipeline P8：回滚手册

内测空库启用后的回滚：**关闭新链路开关**，**不**恢复旧上传入口，**不**把旧历史混入新统计表。

## 开关

| 开关 | 环境变量 | 关闭效果 |
| --- | --- | --- |
| `event_pipeline_v2_ingest` | `TOKENDANCE_EVENT_PIPELINE_V2_INGEST=false` | `/v2/telemetry/*` 返回 `503 EVENT_PIPELINE_V2_PAUSED`；旧 `/v1/telemetry/*` 仍为 `426 CLIENT_UPGRADE_REQUIRED` |
| `event_pipeline_v2_workers` | `TOKENDANCE_EVENT_PIPELINE_V2_WORKERS=false` | worker 跳过 telemetry 聚合 / dirty 刷新；**不**回退 `usage_events` 聚合 |
| `event_pipeline_v2_client` | `TOKENDANCE_EVENT_PIPELINE_V2_CLIENT=false` | 桌面暂停上传（`PIPELINE_PAUSED`）；不启新 writer；不走旧日快照上传 |

## 步骤

1. 设上述开关为 false，滚动重启 API / worker / 客户端。
2. （可选）`go run ./cmd/event-pipeline-reset --mark-rollback --confirm` 记录 `rolled_back` 世代状态。
3. 确认监控：无新 ACK；legacy usage 仍为空（若已 reset）；排行榜不因旧 outbox 回填。
4. 修复后重新打开 ingest → workers → client；**不要**对正式环境使用 `--force` 再清库。

## 硬约束

- 回滚版本必须仍识别新表与 v2 协议；不能靠旧上传救场。
- 内测允许再次空库（`--force`，非 production）；正式后禁止把“每次升级清库”当常规。
- 认证 / 设备 / `desktop_releases*` 从不在重置清单中。
