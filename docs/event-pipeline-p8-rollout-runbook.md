# Event pipeline P8：空库启用操作说明

## 工具

```bash
# 预览将清空的统计表（不含 users/sessions/installations/nonce/desktop_releases*）
cd server && go run ./cmd/event-pipeline-reset --dry-run

# 执行一次（幂等：同 generation 已 ready 则 no-op）
TOKENDANCE_MYSQL_DSN='...' go run ./cmd/event-pipeline-reset --confirm

# 内测再次空库（production 拒绝）
TOKENDANCE_ENVIRONMENT=development go run ./cmd/event-pipeline-reset --confirm --force
```

## 客户端

- 首次启动走 `local_store::pipeline::rollout::ensure_rollout`
- `schema_meta.event_pipeline_v3=1` + `extra.rollout_phase=ready` 后才启 writer
- 重复启动不清 events；初始化中断可恢复

## 合入顺序建议

P0 → P1∥P5 → P2∥P4∥P6 → P3∥P7 → **P8**（本包，集成 tip）。
