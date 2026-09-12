# 生产前 N 名 → 测试库灰度镜像

把当前**全部时间**排行榜前 N 名（默认 10）的用量数据，异步复制到 `tokendance_dev`，给测试服务做灰度。不进生产 worker 循环。

## 复制什么

- 用户公开资料（昵称、handle、头像 URL、隐私开关）
- `daily_user_agent_metrics` / `daily_user_agent_model_metrics` / `daily_skill_metrics`
- `device_daily_aggregates`
- `user_window_scores`（today / 7d / 30d / all）
- 设备行（`installations` + adapter 状态），**公钥替换为测试密钥**
- 目标库写入 `ranking_outbox` / `community_stats_outbox` pending，由**测试 worker** 灌 Redis

## 不复制什么

密码、session、邮箱密文、验证码、安全事件、ingest nonce、原始 `usage_events`、生产设备公钥、对象存储 key、删除/导出任务、社区全局总量行。

邮箱和登录哈希在测试库改写成确定性的 grayscale 哈希，生产账号不能登录测试环境。可选 `TOKENDANCE_GRAYSCALE_PASSWORD` 给这 N 个账号写入同一测试密码。

## 运行

在能同时连生产库（只读）和测试库的机器上：

```text
TOKENDANCE_SOURCE_MYSQL_DSN=.../tokendance_prod?...
TOKENDANCE_MYSQL_DSN=.../tokendance_dev?...
TOKENDANCE_SOURCE_SCHEMA=tokendance_prod
TOKENDANCE_TARGET_SCHEMA=tokendance_dev
TOKENDANCE_GRAYSCALE_LOOP=true
TOKENDANCE_GRAYSCALE_INTERVAL=5m
```

```text
go run ./cmd/grayscale-sync --loop
```

目标库名是 `tokendance_prod` 时进程会拒绝启动。每轮在目标事务内完整替换选中成员的日汇总和设备日汇总，补齐历史缺口并同步源端删除。`TOKENDANCE_GRAYSCALE_REFRESH_DAYS`（默认 2）只影响社区统计刷新日期，不再截断个人日汇总历史。日志以 `summary_history=full` 标识。

同步前后比较团队统计输入，仅在数据变化时推进相关团队的 source revision；无变化的定时同步不使缓存失效。保留已有测试邮箱/登录绑定。团队通过规则版本 4 读取历史 exact + derived Token；来源与去重边界见[团队技术方案](tokendance-teams-technical-design-v1.md)。原始 v2 事件不由本镜像复制。

测试 API / worker 照常连 `tokendance_dev` 和 `redis_dev`。镜像只负责把 MySQL 写进去；榜单 Redis 由测试 worker 消化 outbox。
