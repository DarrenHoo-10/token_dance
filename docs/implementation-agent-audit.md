# TokenDance 多代理实施审计

> 目标：实现 `docs/tokendance-user-system-technical-design.md`
> 执行日期：2026-08-30
> 最终代码验收提交：`13d4845`
> 最终审计提交前 HEAD：`ff2bf95`
> 最终 reviewer 结论：**Approved，0 open issues**

## 1. 模型路由事实

### 1.1 工具可用模型与用户指定模型

用户最初指定：

1. Explorer：`gemini-3.7-flash-high`。
2. Implementer：优先 `gemini-3.7-flash-high`，其次 `grok-4.6`，再次 `gpt-5.6-sol-medium`。
3. Reviewer：`gpt-5.6-sol-medium`。
4. 所有子代理 fallback：`gpt-5.6-sol-medium`。

本会话的 `spawn_subagent` 工具只接受以下相关 slug：

- `gemini-3.7`
- `grok-4.6`
- `sol-medium`

精确 slug `gemini-3.7-flash-high` 和 `gpt-5.6-sol-medium` 不受当前 harness schema 支持。因此执行时采用显式映射，而不是把近似 slug 静默记为精确命中：

| 用户指定标识 | 实际工具 slug | 审计解释 |
| --- | --- | --- |
| `gemini-3.7-flash-high` | `gemini-3.7` | 工具允许列表中的 Gemini 3.7 标识；不能证明是 flash-high 变体 |
| `gpt-5.6-sol-medium` | `sol-medium` | 工具允许列表中的 Sol medium 标识；失败任务的运行元数据显示底层模型名为 `gpt-5.6-sol` |

### 1.2 fallback 的真实执行方式

`spawn_subagent` 没有自动 fallback 参数；fallback 只能由协调代理在任务失败后重新调用实现。真实结果如下：

- 首轮 `gemini-3.7` explorer 和实现代理均成功完成，没有发生额度耗尽，因此没有真实启动 `grok-4.6` fallback。
- `grok-4.6` 在本目标中 **没有被实际调用**，不能声称完成过 fallback。
- 若干 `sol-medium` 任务因 harness 返回 `unknown variant keepalive` 序列化错误而中断；用户随后要求“子代理都用 sol-medium”，因此重试继续使用 `sol-medium`，没有切换到其他模型。
- 中断任务留下的共享工作区代码均由后续 `sol-medium` 子代理或协调代理核验、补全、测试后提交；未把失败调用本身记为成功。

### 1.3 用户中途模型策略更新

用户在实施过程中明确要求：**后续所有子代理都使用 `sol-medium`**。这是时间上更晚的用户指令，取代了其最初为后续调用设置的 Gemini→Grok→Sol 优先级；从该指令之后，新启动和恢复的实现代理与 reviewer 全部使用 `sol-medium`。之前已完成的 `gemini-3.7` 历史调用保留原始记录。没有单独启动名为 Verifier/Verification 的专用子代理；门禁命令、MySQL、race、API smoke 和浏览器验证由协调代理直接执行，独立正确性复核由下表中的 reviewer 子代理完成。

## 2. Explorer 与初始编排

| 角色 | 子代理 ID | 工具模型 slug | 任务 | 结果 | 代码提交 |
| --- | --- | --- | --- | --- | --- |
| Explorer | `01a04e96-a2a2-7250-8565-2f771ee7a7c2` | `gemini-3.7` | 核对技术设计、产品方案、Collector 文档、三份 DDL、API 路由、USR 验收矩阵、事务不变量与实施顺序 | 完成；输出 API/验收 ID 矩阵、DDL 不变量、前端路由和六阶段集成顺序 | 无，read-only |
| Implementer | `01a04e96-a2a7-7922-a9d3-a0f250110111` | `gemini-3.7` | Go API/Worker、配置、migration、OpenAPI、Repository 与测试骨架 | 完成 | `bd0add2` |
| Implementer | `01a04e96-a2ae-7731-96b9-f91f808ead91` | `gemini-3.7` | React/TypeScript Web 基线、真实 API client、用户路由与页面测试 | 完成 | `71cb9ae` |
| Reviewer | `01a04e96-df46-7292-9168-2898627d6937` | `sol-medium` | 实施前安全、隐私、竞态、API/DDL 漂移和验收门禁检查 | 完成；条件通过，列出 OpenAPI、Collector grant、缓存隐私、DDL、ingest 锁序、Worker fencing 等阻塞项 | 无，read-only |

## 3. 第一轮并行实现

| 角色 | 子代理 ID | 工具模型 slug | 任务 | 结果 | 提交/集成位置 |
| --- | --- | --- | --- | --- | --- |
| Implementer | `01a04ea1-89d2-7410-9ed1-1a6dda1655db` | `gemini-3.7` | MySQL Repository、生产运行时接线、migration runner、Worker lease | 初版完成；真实 MySQL 非跳过测试随后发现 SQL splitter 问题 | 与并行 auth 变更合并进入 `d5a2cbe` |
| Implementer | `01a04ea1-89d8-75a2-a61e-11b2da9eec2b` | `gemini-3.7` | Auth/Profile/Privacy/Media，USR-001–010、021–022 | 完成 | `d5a2cbe` |
| Implementer | `01a04ea1-89de-73e1-8546-e29ec8bef748` | `gemini-3.7` | Analytics、公开查询、设备、Export/Deletion，USR-011–020、023、101–107 | 完成 | `f09b07c` |
| Implementer | `01a04ea1-89e4-7d80-8990-a9c78e7c1eb6` | `gemini-3.7` | Web 去除 Mock fallback、补齐真实 API 状态与页面测试 | 完成 | `b221d03` |
| Implementer-fix（resume） | `01a04eae-1713-7581-8ba9-8f570130f3df` | `gemini-3.7` | 修复真实 MySQL migration splitter、`schema_migrations` 冲突、0001 upgrade 与 privacy backfill | 完成；该 resume 调用继承原代理模型，clean/upgrade/dirty 三路径真实通过 | `d07c6c5` |

## 4. Reviewer 第一轮与 Gemini 修复阶段

| 角色 | 子代理 ID | 工具模型 slug | 任务 | 结果 | 提交/集成位置 |
| --- | --- | --- | --- | --- | --- |
| Reviewer（resume） | `01a04eb7-211a-7211-aef1-bf2bb62d0602` | `sol-medium` | 审查 `d07c6c5` 生产路径和测试真实性 | **Reject**；该 resume 调用继承原 reviewer 模型；发现 PII 明文、不安全 Cookie/默认密钥、Session IDOR、删除/Provider/analytics/media 占位、契约漂移等 | 无，read-only |
| Implementer-fix | `01a04ec2-894a-7d23-9465-5eafcd6f11d4` | `gemini-3.7` | AEAD、邮件抽象、独立 migrate 入口、schema readiness | 完成；并行共享工作区导致 Git 无法证明逐代理文件所有权；这些结果首次完整出现在 `5208c3` 的树中 | `5208c3`（shared-tree primary integration） |
| Implementer-fix | `01a04ec3-6bc5-7791-aba1-1f6bf6aeb8ce` | `gemini-3.7` | Session/CSRF/IDOR/Device grant/Ingest/Onboarding 安全 | 完成；并行共享工作区导致 Git 无法证明逐代理文件所有权；生产集成首次完整出现在 `5208c3`，`bba635f` 增加 auth/security 测试和部分 MySQL store 修复；`4a2a638` 是空的完成标记，无 tree change | `5208c3`（primary）、`bba635f`（follow-up）、`4a2a638`（empty marker） |
| Implementer-fix | `01a04ec3-6bcd-7443-bd84-583d824431e7` | `gemini-3.7` | MySQL analytics、公开榜单/compare 隐私、DTO | 完成；并行共享工作区导致 Git 无法证明逐代理文件所有权；结果首次完整出现在 `5208c3`，MySQL 测试补充出现在 `5f828fd` | `5208c3`（primary）、`5f828fd`（test follow-up） |
| Implementer-fix | `01a04ec3-6bd3-7db3-88e4-07d0b866afd6` | `gemini-3.7` | Email/Export/Deletion/Media Provider、Worker fencing | 完成；并行共享工作区导致 Git 无法证明逐代理文件所有权；Provider/Worker 实现出现在 `5208c3`，lease/fencing follow-up 出现在 `6d1514a`，MySQL 测试补充出现在 `5f828fd` | `5208c3`（primary）、`6d1514a`（worker follow-up）、`5f828fd`（test follow-up） |
| Implementer-fix | `01a04ec3-6bda-77e1-9188-e6bb87684d5d` | `gemini-3.7` | OpenAPI、Web 契约、本地化、returnTo | 完成 | `5208c3` |
| Reviewer（resume） | `01a04ee5-0ac4-7590-9538-f3124fe7f045` | `sol-medium` | 复审修复后的生产路径 | **Reject**；该 resume 调用继承原 reviewer 模型；仍发现 memory Provider、假 ingest ACK、删除竞态、key domain、Web payload 等 | 无，read-only |
| Implementer | `01a04ee5-47d4-7791-adf7-b52f9a96e37d` | `sol-medium` | 初次 sqlc 集成 | **失败**；harness `keepalive` 序列化错误，未记成功 | 无最终提交 |

## 5. 用户要求全量 `sol-medium` 后的实现与验证

### 5.1 浏览器与生产边界修复

| 子代理 ID | 模型 | 任务 | 真实结果 | 提交 |
| --- | --- | --- | --- | --- |
| `01a04eeb-89c0-7e80-9200-cffaf6f68e0a` | `sol-medium` | 修复真实浏览器发现的 privacy payload、本地化和硬编码指标 | 完成 | `29089ec` |
| `01a04ef5-c723-7fc2-b6e3-1df6b8eb6b32` | `sol-medium` | Production SMTP 与共享 S3-compatible storage | 完成 | `6fb7248` |
| `01a04ef5-c729-7850-ad68-ed79d816ee83` | `sol-medium` | Ed25519 签名 telemetry ingest | **失败**；`keepalive` 序列化错误，留下部分代码 | 无 |
| `01a04ef5-c72f-78e0-9304-ffc5780761d1` | `sol-medium` | 删除 claim/fencing/cancellation/残留对账 | 完成 | `b86ef6f` |
| `01a04ef5-c735-7162-98ab-05d522a17d1e` | `sol-medium` | 独立密钥环、稳定 CSRF、限流、OpenAPI、媒体哈希、时间范围、Activity API、整合 ingest | 完成；吸收失败 ingest 任务的共享代码并补齐测试 | `3f4f313` |
| `01a04ef5-c73c-7523-8dd0-2c4be0e02fb8` | `sol-medium` | sqlc 集成重试 | **失败**；`keepalive` 序列化错误 | 无 |
| `01a04f03-2ec6-7a61-bdfc-df298d730026` | `sol-medium` | telemetry ingest 第二次重试 | **失败**；`keepalive` 序列化错误；代码后由 `3f4f313` 核验集成 | 无 |
| `01a04f03-2ecc-7763-b85f-8a51b95c6db9` | `sol-medium` | sqlc 第二次重试 | 完成 | `4c5790d` |

### 5.2 Reviewer 与后续收敛

| 角色 | 子代理 ID | 模型 | 审查对象/任务 | 结论或结果 | 提交 |
| --- | --- | --- | --- | --- | --- |
| Reviewer | `01a04f23-4491-7ef2-8bca-7186a87806bd` | `sol-medium` | Production Provider、ingest、deletion、keyring、Web 契约复审 | **Reject**；指出 metadata、安装删除、SMTP、迁移与 sqlc 等剩余问题 | 无 |
| Implementer | `01a04f2e-40ac-74d3-9b8b-7cff6b3da087` | `sol-medium` | Metadata/Auth/SMTP/Media 安全 | 完成；并行变更与 Web/analytics 任务共同收敛 | `e26f199` |
| Implementer | `01a04f2e-40b2-7392-b129-51f93c67818c` | `sol-medium` | 删除 scope、Export key rotation、migration dirty recovery | 完成 | `4236f8a` |
| Implementer | `01a04f2e-40b9-7011-b7dc-8e6185716608` | `sol-medium` | Web avatar、中文、非 UTC/DST、sqlc 覆盖 | 完成；与并行安全修复合并 | `e26f199` |
| Reviewer | `01a04f44-b053-7e10-8f58-e2f1538e4486` | `sol-medium` | 上述修复复审 | **Reject**；4 个 open issue | 无 |
| Implementer | `01a04f58-5ed7-70d0-abf6-60467e204a5e` | `sol-medium` | Metadata 严格 enum/identifier | 完成 | `22dea19` |
| Implementer | `01a04f58-5edd-7e82-82e9-f2a2dcd3a176` | `sol-medium` | 安装删除后的 canonical aggregation v2 与 leaderboard | 完成 | `faa1b83` |
| Implementer | `01a04f58-5ee3-7d53-a799-071c2c3875ac` | `sol-medium` | MySQL 隐式 DDL 提交窗口恢复 | 完成 | `0a7e1f8` |
| Implementer | `01a04f58-5eea-7a51-9222-2cf43224f49c` | `sol-medium` | 将生成的 sqlc 查询真正接入生产 Repository | 完成 | `afb1de3` |
| Reviewer | `01a04f6d-d112-7720-ae6f-d47156b137e9` | `sol-medium` | metadata、aggregate、migration、sqlc 复审 | **Reject**；剩余 immutable leaderboard 与临时表重放问题 | 无 |
| Implementer | `01a04f75-c55a-71a2-895f-fe38287f8ddd` | `sol-medium` | Scope-aware immutable leaderboard revisions | 完成 | `040f839` |
| Reviewer | `01a04f81-1a68-74c1-8606-026e8f179863` | `sol-medium` | Leaderboard 与 migration 临时表复审 | **Reject**；剩余 `previous_rank_no` 计算问题 | 无 |
| Reviewer | `01a04f8a-836a-7581-907c-29381bd1a6f1` | `sol-medium` | 即时历史排名修复复审 | **Approved，0 open issues** | 无 |
| Reviewer | `01a04f98-d6a0-74d1-8901-bc2275e114c5` | `sol-medium` | 真实 API smoke 发现的 nullable deletion status 修复复审 | **Approved，0 open issues** | 无 |
| Audit Reviewer | `01a04fa6-aba9-7cf2-91b1-d0d899a6ab7c` | `sol-medium` | 核对本多代理 ledger 的 ID、模型、任务、失败、fallback 和提交归属 | 首轮 **Reject**：指出 shared-tree 提交归属、专用 verification inventory 和 resume slug 表述不精确 | 无，read-only |
| Audit Reviewer（resume） | `01a04fac-46bf-7d21-8db6-12375e4edbee` | `sol-medium` | 复核更正后的完整 ledger | **Approved，0 audit gaps**；确认精确提交归属、空 marker、无专用 verification 子代理、literal slug、fallback 和 keepalive 记录均准确 | 无，read-only |

## 6. 协调代理直接完成并由 reviewer 覆盖的修复

以下问题由协调代理在真实路径验证时发现并直接修复，随后均由测试和 `sol-medium` reviewer 覆盖：

| 问题 | 修复提交 | 证据 |
| --- | --- | --- |
| `mediaStore.CreateAvatarUploadIntent` 对可空 SHA 指针解引用 | `3bbd162` | MySQL media CHECK 测试、全套 Go 测试 |
| Export versioned idempotency HMAC 超过 `VARCHAR(64)` | `914ab45` | 真实 API smoke、service→MySQL 测试 |
| Dashboard `10w` calendar range 被 API 拒绝 | `84c64cb` | Orca 浏览器重试成功、analytics/HTTP 测试 |
| Migration 临时 guard DROP 重放 | `d174502` | `TestTemporaryGuardDropIsReplaySafe` |
| 新排行榜快照 `previous_rank_no` 应使用直接旧排名 | `b0f3762` | 非巧合值和旧值为 NULL 的 MySQL 测试；reviewer approved |
| Nullable `scope_filter_json` 导致 deletion status sqlc 扫描失败 | `13d4845` | 真实 API 双启动 smoke、`TestMySQL_GetDeletionRequestWithNullScopeFilter`；reviewer approved |

## 7. Reviewer 结论演进

| 轮次 | Reviewer 子代理 ID | 模型 | 结论 |
| --- | --- | --- | --- |
| 预审 | `01a04e96-df46-7292-9168-2898627d6937` | `sol-medium` | 条件通过，列出实施门禁 |
| 第一轮代码审查 | `01a04eb7-211a-7211-aef1-bf2bb62d0602` | `sol-medium` | Reject |
| 第二轮复审 | `01a04ee5-0ac4-7590-9538-f3124fe7f045` | `sol-medium` | Reject |
| Provider/ingest/deletion 后复审 | `01a04f23-4491-7ef2-8bca-7186a87806bd` | `sol-medium` | Reject |
| Metadata/sqlc/analytics 后复审 | `01a04f44-b053-7e10-8f58-e2f1538e4486` | `sol-medium` | Reject |
| Immutable leaderboard 前复审 | `01a04f6d-d112-7720-ae6f-d47156b137e9` | `sol-medium` | Reject |
| Leaderboard revision 复审 | `01a04f81-1a68-74c1-8606-026e8f179863` | `sol-medium` | Reject：仅剩 previous rank |
| 即时历史排名复审 | `01a04f8a-836a-7581-907c-29381bd1a6f1` | `sol-medium` | **Approved，0 open issues at `b0f3762`** |
| Deletion status 复审 | `01a04f98-d6a0-74d1-8901-bc2275e114c5` | `sol-medium` | **Approved，0 open issues at `13d4845`** |
| 多代理审计 ledger 首轮 | `01a04fa6-aba9-7cf2-91b1-d0d899a6ab7c` | `sol-medium` | Reject：要求精确提交归属、明确无专用 verification 子代理、resume 记录 literal slug |
| 多代理审计 ledger 复审 | `01a04fac-46bf-7d21-8db6-12375e4edbee` | `sol-medium` | **Approved，0 audit gaps** |

## 8. 最终提交与证据

- 最终实现 reviewer-approved commit：`13d4845`。
- 最终文档审计 HEAD：`ff2bf95`。
- Go/MySQL、race、migration、clean-build、API smoke、desktop/mobile browser、security/performance 证据位于目标专用 scratch：
  - `tests.log`
  - `tests-race.log`
  - `migrations.log`
  - `clean-build.log`
  - `api-smoke.log`
  - `web-e2e.log`
  - `security-performance.log`
  - `openapi-lint.log`
  - `screenshots/`

## 9. 审计结论

- 存在真实 Explorer 调用。
- 存在多个独立实现子代理调用。
- 存在独立 reviewer 多轮审查，且 reviewer 全部使用 `sol-medium`。
- 模型精确 slug 不可用的限制已明确记录，没有把近似 slug 伪装为精确命中。
- `grok-4.6` fallback 没有被实际触发，文档明确记为“未调用”。
- `sol-medium` 失败调用、重试和后续集成均保留真实结果，没有把失败记录改写为成功。
- 最终 reviewer 对 `13d4845` 给出 **Approved，0 open issues**。
