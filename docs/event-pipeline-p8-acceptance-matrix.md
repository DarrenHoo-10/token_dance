# Event pipeline P8：空库启用验收矩阵

对齐大方案 §13。P0–P7 包内已有自动化的类别在此交叉引用；本文件强调 **首次启用 / 开关 / 回滚 / 隐私** 与运维清单。

## 发布顺序（必须）

1. 停旧接收/聚合/outbox → 旧入口保持 `CLIENT_UPGRADE_REQUIRED`
2. `go run ./cmd/event-pipeline-reset --dry-run` → `--confirm`（清统计、建/空 telemetry、清榜缓存）
3. 确认 `TOKENDANCE_EVENT_PIPELINE_V2_INGEST=true` 与 `TOKENDANCE_EVENT_PIPELINE_V2_WORKERS=true`
4. 启 API + worker
5. 发内测客户端（`TOKENDANCE_EVENT_PIPELINE_V2_CLIENT` 默认 true）
6. 核验：空库 → 采集 → 上传 → 两端统计 → 页面

**禁止**：旧入口写新统计；半新半旧双写。

## 类别勾选

| 类别 | 覆盖位置 / 命令 | 首次启用关注点 |
| --- | --- | --- |
| 身份 | P2/P3 adapter + runner tests | 不迁移旧指纹 |
| JSONL | P2/P3 | 仅当天准入 |
| SQLite | P3 | 同上 |
| 时间 | P0/P2 admission | 非法时间不入 1970 |
| 源事务 | P1/P2 | CAS / 租约 |
| 消费者 | P4 / P6 | hour/day/month 独立 |
| 关联统计 / 费用 | P4 / metric fixtures | unknown 模型保留 |
| 协议 | P0 golden + P5/P7 | 部分 ACK |
| 认证 | P5 rebind fencing | 登录/设备保留 |
| 保留期 | P1 TTL tests | created_at+14d |
| 服务端窗口 | P5 | 窗外拒绝 |
| 并发数据库 | P5/P6 | SKIP LOCKED |
| 公共读模型 | P6 dirty version | 旧刷新不吞新版本 |
| **首次启用** | 下方脚本 + rollout tests | 中断可恢复；重复启动不清库 |
| **隐私** | canary 检查 | 不上传 prompt/path/正文 |
| 删除 | deletion worker | telemetry 表纳入残差清单 |

## 首次启用自动化

```bash
# 服务端清单与开关单测
cd server && go test ./internal/rollout/ ./internal/httpapi/ -count=1 -run 'TestPreserved|TestStatsClear|TestTelemetryV2Paused|TestTelemetryV1'

# 客户端空库状态机
cd collector/apps/desktop/src-tauri && cargo test --lib local_store::pipeline::rollout -- --nocapture
```

仓库脚本：`scripts/event-pipeline-p8-acceptance.sh`

## 监控最小集

`rollout.CollectMonitorSnapshot`：

- pending telemetry tasks + 最老年龄
- dirty 未应用版本天数
- telemetry / legacy usage 计数（reset 后 legacy 必须为 0）
- ranking_outbox pending

## 回滚演练

见 [event-pipeline-p8-rollback.md](./event-pipeline-p8-rollback.md)。
