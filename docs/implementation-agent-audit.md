# TokenDance 多代理实施审计

> 开始时间：2026-08-30  
> 目标：实现 `docs/tokendance-user-system-technical-design.md`

## 模型标识兼容说明

用户指定的精确模型标识 `gemini-3.7-flash-high` 与 `gpt-5.6-sol-medium` 不在当前子代理工具允许的模型 slug 列表中。当前工具提供 `gemini-3.7`、`grok-4.6` 与 `sol-medium`。本次执行不会静默声称精确标识已满足：

- explorer 与首选实现代理使用工具可用的 `gemini-3.7`，作为对 `gemini-3.7-flash-high` 的显式兼容替代。
- reviewer 使用工具可用的 `sol-medium`，作为对 `gpt-5.6-sol-medium` 的显式兼容替代。
- 子代理失败时按用户要求优先尝试 `grok-4.6`，最终 fallback 使用 `sol-medium`。

## 调用记录

| 角色 | 子代理 ID | 工具模型 slug | 任务 | 状态/结论 |
| --- | --- | --- | --- | --- |
| Explorer | `01a04e96-a2a2-7250-8565-2f771ee7a7c2` | `gemini-3.7` | 核对设计、DDL、API、验收 ID 和集成顺序 | 完成：输出完整路由/验收矩阵、DDL 不变量、事务要求与六阶段集成顺序 |
| Implementer | `01a04e96-a2a7-7922-a9d3-a0f250110111` | `gemini-3.7` | Go API/Worker、migration、OpenAPI 与测试骨架 | 运行中 |
| Implementer | `01a04e96-a2ae-7731-96b9-f91f808ead91` | `gemini-3.7` | React/TypeScript Web、真实 API client 与页面测试 | 完成：清理所有生产 API catch fallback、硬编码假数据和 Mock 标识；完善 10 项指标与 supported/null/0 区分；强化中英文切换保留 URL/状态；更新前后端契约与状态流转；添加全部页面与错误分支驱动测试并通过构建 |
| Reviewer | `01a04e96-df46-7292-9168-2898627d6937` | `sol-medium` | 预审安全、隐私、竞态、API/DDL 漂移和验收门禁 | 完成：条件通过；要求解决 OpenAPI 单一事实源、Collector 短期授权、缓存隐私、DDL 部分失败、ingest 锁序、Worker fencing 和 clean build |

## 预审结论纳入实施

后端实现采用 OpenAPI 作为前后端契约事实源；未登录 Session 固定为 `204`。公共缓存不得脱离 MySQL 当前投影状态直接返回。设备 revoke/pause 与 ingest 使用相同 installation 行锁顺序。Worker 使用数据库时间、租约和条件更新 fencing。Migration runner 不声称 MySQL DDL 可事务回滚，提供 advisory lock、checksum、durable dirty/statement progress；`migrate -check` 报告部分 DDL，`migrate -repair` 从最后持久 checkpoint 恢复。Collector 浏览器授权补充短期、installation-scoped grant，禁止复用 Web Session Cookie 作为 bearer。

## 独立 reviewer 第一轮结论

Reviewer `01a04eb7-211a-7211-aef1-bf2bb62d0602`（`sol-medium`）拒绝集成，确认绿色测试包含 memory-only/test-theater 路径。关键问题包括：PII/验证码伪“密文”、不安全重复 Cookie/默认共享密钥、Session IDOR、删除/导出/邮件/媒体 Worker 占位实现、公共榜单隐私泄露、MySQL analytics 使用错误列并伪造结果、CSRF reload 失效、密码重置竞态、设备授权与 ingest 锁序缺失、Go/OpenAPI/Web 契约漂移及中英文/returnTo 问题。

| 角色 | 子代理 ID | 工具模型 slug | 修复范围 | 状态 |
| --- | --- | --- | --- | --- |
| Implementer-fix | `01a04ec2-894a-7d23-9465-5eafcd6f11d4` | `gemini-3.7` | AEAD 密钥、邮件本地/Provider 边界、独立 migrate 入口 | 运行中 |
| Implementer-fix | `01a04ec3-6bc5-7791-aba1-1f6bf6aeb8ce` | `gemini-3.7` | Auth/Session/CSRF/Device/Grant/Ingest/Onboarding 安全 | 运行中 |
| Implementer-fix | `01a04ec3-6bcd-7443-bd84-583d824431e7` | `gemini-3.7` | MySQL analytics、公开榜单/compare 隐私与 DTO | 运行中 |
| Implementer-fix | `01a04ec3-6bd3-7db3-88e4-07d0b866afd6` | `gemini-3.7` | Worker/Email/Export/Deletion/Media 真实 Provider 和 fencing | 运行中 |
| Implementer-fix | `01a04ec3-6bda-77e1-9188-e6bb87684d5d` | `gemini-3.7` | OpenAPI lint、生成/校验客户端、Web 契约/本地化/returnTo | 运行中 |

## 模型策略更新

用户在实施过程中明确要求：后续所有子代理统一使用工具可用的 `sol-medium`。从该指令起，不再为新子代理选择 `gemini-3.7` 或 `grok-4.6`；已完成的历史调用保留原模型记录以供审计。

## `sol-medium` 全量修复阶段

在用户更新模型要求后，新增子代理全部使用 `sol-medium`：

| 子代理 ID | 任务 | 结果 |
| --- | --- | --- |
| `01a04eeb-89c0-7e80-9200-cffaf6f68e0a` | 真实浏览器发现的隐私 payload、本地化和硬编码指标 | 完成，提交 `29089ec` |
| `01a04ef5-c723-7fc2-b6e3-1df6b8eb6b32` | Production SMTP 与共享 S3-compatible Provider | 完成，提交 `6fb7248` |
| `01a04ef5-c72f-78e0-9304-ffc5780761d1` | 删除任务 claim/fencing/cancellation/残留对账 | 完成，提交 `b86ef6f` |
| `01a04ef5-c735-7162-98ab-05d522a17d1e` | 独立密钥环、稳定 CSRF、限流、OpenAPI、媒体哈希、时间范围与活动 API | 完成，提交 `3f4f313` |
| `01a04f03-2ecc-7763-b85f-8a51b95c6db9` | sqlc 生成代码与 clean-diff 校验 | 完成，提交 `4c5790d` |
| `01a04ef5-c729-7850-ad68-ed79d816ee83`、`01a04f03-2ec6-7a61-bdfc-df298d730026` | Ed25519 telemetry ingest | 两次均因 harness `keepalive` 序列化错误中断；其代码与测试已留在共享工作区，随后由 `3f4f313` 集成并通过 MySQL ingest 测试 |

协调代理在真实 MySQL 全套测试中额外发现 `mediaStore.CreateAvatarUploadIntent` 对空 SHA 指针解引用并修复为可空参数绑定；修复后全套 Go/MySQL 测试通过。

## 最终 reviewer 结论

Reviewer `01a04f8a-836a-7581-907c-29381bd1a6f1` 使用 `sol-medium` 对 `b0f3762` 完成复审并给出 0 个 open issue。随后真实 API smoke 发现 deletion status 对 nullable JSON 的 sqlc 扫描问题并修复；Reviewer `01a04f98-d6a0-74d1-8901-bc2275e114c5` 再次使用 `sol-medium` 对 `13d4845` 完成最终复审，结论为 **Approved，0 个 open issue**。复审确认不可变且 scope-aware 的排行榜新快照、即时历史排名、迁移重放、metadata 白名单、签名 ingest、fenced deletion、Provider、独立密钥环、稳定 CSRF、OpenAPI/sqlc、非 UTC 边界、Web payload、中英文和 owner-scoped deletion status 均保持通过。

后续集成、复审、fallback 和最终 reviewer 结论继续追加到本文件。
