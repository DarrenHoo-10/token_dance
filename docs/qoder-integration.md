# Qoder 接入设计方案

状态：**P1 已实施**（2026-10-24，transcript adapter 全链路：adapter crate + pipeline 策略 +
注册/检测 + 桌面端与 web 元数据 + config-executor `EnvironmentSet` 支持；同日补齐桌面端触发链
"检测到 Qoder → 自动执行 setup_plan 写环境变量 → 一次性通知"，见 §4.3）。未实施：P2 额度环、
P3 IDE local.db。本机实测 + 公开资料调研时间 2026-10-24；本机装有 Qoder CLI
（`~/.qoder`，CLI 1.1.75-hotfix.1），所有本地数据结论以真实文件为据，来源见 §2.6。

## 1、背景与目标

Qoder 是阿里巴巴（国际 AIDC）的 AI 编程产品，多形态并存：VS Code fork 的 Qoder IDE、独立桌面端
New Qoder（Qoder 1.0，Electron）、Qoder CLI、JetBrains 插件。计费单位是 Credits（订阅制，
Free 300 / Lite $10=1000 / Pro $20=2000 / Pro+ $60=6000 / Ultra $200=20000，加购包 $20=1500，
周期清零；中国版独立站点 qoder.com.cn，¥ 定价、账号与数据均分离）。

**目标**：把 Qoder 作为新 harness 接入——

1. **用量采集**（核心）：采集本地会话记录，产出标准事件；
2. **额度展示**：额度面板显示 Qoder 套餐 credits 消耗（对齐 zcode/cursor 的"用量 + 额度"双面形态）。

**非目标**：不做 prompts/代码内容上传（隐私红线不变）；不做 credits→token 或 credits→USD 的
换算估计（无公开稳定价目，宁缺勿造）；不接组织级 OpenAPI（企业场景，另立项）。

## 2、平台事实摘要

### 2.1 本地数据（三端会话互不相通）

**A. CLI / 桌面端 transcript（本机实测，P1 主路径）**：`~/.qoder/projects/<项目slug>/<session-id>.jsonl`
（+ 同名目录 `state.json`、子代理 `<session-id>/subagents/`）。记录结构与 Claude Code transcript
高度同构：顶层 `type`（user/assistant/active-leaf/runtime-config/file-history-snapshot…），
`uuid` / `parentUuid` / `sessionId` / `isSidechain` / `cwd` / `timestamp`(RFC3339) / `version` /
`entrypoint`。

关键口径（本机 142 行 transcript 实测）：

- `type:"assistant"` 记录的 `message.usage` 含
  `input_tokens/output_tokens/cache_read_input_tokens/cache_creation_input_tokens` 四个 token 字段，
  **默认全部写 0**；真实信号是 `credits`（本次请求消耗，实测 13 条记录 0.48~10.89、**非单调**，
  即单次值而非累计值）、`original_credits`、`billable`、`request_id`、`context_usage_ratio`。
- **已实测验证（2026-10-24，CLI 1.1.75-hotfix.1）**：默认 token 字段全 0；设
  `QODER_EXPOSE_TOKEN_USAGE=true` 并让进程带着该变量启动后写入真实值。置零由运行时函数
  `YGr(A)=开关?A:0` 实现（qoder-worker-runtime 内 `Lr("EXPOSE_TOKEN_USAGE")` 拼前缀
  `QODER_`，取值 "1"/"true" 均可）。暴露后 usage 为**会话累计值**（input 与
  `context_usage_ratio` 强相关：in=27215 ↔ ctx=0.027），decode 需按 high-water mark 差分；
  且 `input_tokens` **包含** cache_read（实测 cr=25728 ⊂ in=27215），标准口径需相减。
  credits 为单次值（跨记录非单调，实测 0.2218→0.0226）。
- 模型字段是路由档位别名：本机见 `kmodel_latest`、`qfmodel`；IDE 侧观测值
  `qmodel/cmodel/gmodel/kmodel/lite/auto`，BYOK 统一记 `custom_model`。
- 辅助目录：`tasks/<sessionId>/N.json`（任务名、model_key、begin_at、stage），
  `logs/runs/<run-id>/manifest.json`（cli_version/platform/argv），`.qoder-app-status.json`
  （登录态、schema_version=1），`.models/<uid>/catalog-v7`（模型目录，本地加密，不依赖）。
- 目录覆盖：`QODER_CONFIG_DIR`；中国版 CLI 用 `~/.qoder-cn/`（`QODERCN_CONFIG_DIR`）。
- 会话保留：`general.sessionRetention.enabled` 默认 false（不清理）；logs 默认保留 30 天。

**B. IDE local.db（P3 token 主路径）**：VS Code fork 的 SQLite
`%APPDATA%\Qoder\SharedClientCache\cache\db\local.db`（macOS
`~/Library/Application Support/Qoder/...`；CN 版 `QoderCN`）。表 `chat_session`（含
`project_name`）、`chat_record`、`chat_message`：token 在 `chat_message.token_info`
（`prompt/completion/cached`，**cached 是 prompt 的子集**→ 映射 cache_read=input−cached 口径）、
模型在 `model_info.model_key`、时间 `gmt_create`(ms)。这是**默认配置下唯一有真实 token 数的
地方**，也是 BYOK（`custom_model`）用量唯一可观测处。`state.vscdb`（lingma.* 键）只存会话元数据，
对用量无用。本机未装 IDE，此路径按公开字段级资料设计，实施前需实测建 fixture。

### 2.2 凭据

- CLI `/login`（浏览器 OAuth）：凭据在 `~/.qoder/.auth/user`（本机只见 `machine_id`，token 可能
  在系统凭据库或已清理，不依赖）。
- **PAT**：qoder.com/account/integrations 创建，环境变量 `QODER_PERSONAL_ACCESS_TOKEN` 使用，
  优先级高于本地凭据。
- 桌面端：`%APPDATA%\com.qoder.app.stable\auth.v1.dat`（CN 版 `com.qodercn.app.stable`）。

### 2.3 额度查询（社区逆向，CodexBar 在用）

- 个人：`GET https://qoder.com/api/v2/me/usages/big_model_credits`（中国版
  `https://qoder.com.cn/api/v2/...`），浏览器会话 Cookie 认证；返回 used/total credits、百分比、
  `nextResetAt`；`totalQuota` 与 `sharedQuota` 同返时需合并。**PAT 能否调用此端点未验证**（§10）。
- 组织：api.qoder.com OpenAPI（org API key，usage-events/summary/resource-packages）——非目标。

### 2.4 用量查询的官方途径

IDE 右下角 Credits 按钮、官网 Settings→Usage、CLI `/usage`、`/status` Usage 标签——均为交互式，
无个人公开 API 文档；API key 型查询只有组织 OpenAPI。

### 2.5 计费语义

credits 按模型与输入/输出 token 计费，失败调用不扣费；Qwen3.7 系列有分时段计费；默认档位模型为
Qwen/DeepSeek/GLM/Kimi/MiniMax 系（无 Claude/GPT）。

### 2.6 来源

本机实测（`~/.qoder` 全树勘察）；docs.qoder.com（cli/sessions、cli/authentication、Credits、
cli/usage、cli/settings-reference、user-guide/chat/model-tier-selector、extensions/hooks）；
qoder.com/en/pricing、qoder.com/blog/qoder-subscriptions-are-here；社区逆向工程
github.com/vibe-cafe/vibe-usage、github.com/dgjin/qoder-token-usage、github.com/steipete/CodexBar
（docs/qoder.md）、github.com/camtrik/agent-trail、forum.qoder.com。

## 3、接入定位与总体架构

**新 harness `qoder`（用量）+ 额度提供方（quota）**，整体对齐 cursor（本地用量 + 账号额度）与
zcode（本地用量 + 官方域额度 API）的复合形态：

```
P1  transcript JSONL 采集（CLI/桌面端）——credits 口径，本机可完全验证
P2  额度提供方（big_model_credits 端点）——PAT 验证通过则自动发现，否则 Cookie 兜底
P3  IDE local.db 采集（token 主路径 + BYOK 可见性）
```

- **agent_id 统一为 `qoder`**：三端会话互不相通但同产品同额度，聚合到同一 harness（对齐
  cursor-cli/cursor-agent → `cursor` 的别名先例）；entrypoint（cli/ide）不进 agent_id。
- 检测/发现同时覆盖 `~/.qoder` 与 `~/.qoder-cn`，尊重 `QODER_CONFIG_DIR` / `QODERCN_CONFIG_DIR`。
- 事件沿用 EventEnvelope v1.0；credits 走 `cost_recorded`（多币种成本管线已支持
  `total_costs[currency]`，pricing.rs 已有 provider_reported 先例）。

## 4、P1：transcript 采集 Adapter（详细方案）

### 4.1 检测

`detect_qoder`：`~/.qoder/projects`（或 `~/.qoder-cn/projects`、`$QODER_CONFIG_DIR/projects`）
存在且含 `.jsonl` → DETECTED。纯只读，无需权限 → ACTIVE；目录存在但无会话 → UNDETECTED。

### 4.2 数据源优先级

1. `JsonlTail`：`projects/<slug>/<session>.jsonl`（含 `subagents/*.jsonl`）——append-only，按
   (文件, 偏移) checkpoint 增量读；新会话文件靠目录扫描发现（复用现有 discovery budget 机制）。
2. 辅助（不产事件，仅 enrich）：`tasks/<sessionId>/N.json` 任务名、`logs/runs/*/manifest.json`
   版本信息——P1 可不读。

### 4.3 配置计划

setup_plan 走现有 §3.6 配置变更计划：`ConfigMutation::EnvironmentSet`（User scope，
`QODER_EXPOSE_TOKEN_USAGE=true`），`required_permissions` 为空（产品决策：非危险权限，无感
开启，不设审批门槛）；§3.6 强制的备份/原子写入/读回验证/失败回滚流程全部保留，卸载/禁用时按
第 8 条恢复。写入后以**通知**（而非询问）告知用户：已开启 Qoder token 明细、重启 Qoder 后生效。
**平台边界**：Windows 写用户环境变量直接可行；macOS GUI 应用不继承 shell 环境
（launchctl setenv 仅当次登录、LaunchAgents 过重），macOS 降级为"仅 UI 建议 + 手动指引"。
生效检测在运行时：新会话 token 仍全 0 → 提示重启 Qoder；变量在但长期无效（Qoder 改版）→
仅提示不降级（credits 采集不受影响）。

**实施状态（2026-10-24）**：全链路已落地。

- `config-executor` 已支持 `EnvironmentSet`——User scope、字面量 `value://` 引用（计划里
  绝不解析 secret/host 引用）、Windows `HKCU\Environment` 写入 + `WM_SETTINGCHANGE` 广播、
  读回验证、加密备份与回滚（恢复旧值或删除新键）、非 Windows 平台显式拒绝；单测见
  `crates/config-executor/src/tests.rs`。
- 协议与权限：`AdapterPermissions` 新增可选 `environmentKeys`（精确键名 allowlist，大小写
  不敏感匹配，schema 见 `schemas/protocol/v1`）；`adapter-host` 在 setup_plan 出口校验
  `EnvironmentSet` 的 key 必须在 manifest 声明（否则 `MANIFEST_PERMISSION_DENIED`），
  adapter-sdk manifest 校验键名字符集与 executor 一致。qoder manifest 已声明
  `QODER_EXPOSE_TOKEN_USAGE`。
- 桌面端触发链（`apps/desktop/src-tauri/src/qoder_setup.rs`）：`AppState::build` 装配服务后
  调用 `ensure_exposed`——检测到 Qoder 且变量未开启时，经 `Collector::propose_setup` 取计划、
  形状自检（恰好 1 条 user-scope `QODER_EXPOSE_TOKEN_USAGE` 写入）、`begin_configure` →
  executor apply（备份/读回验证/失败回滚）→ `finish_configure_ok/err`，runtime
  setup_plan_status 随之 Proposed/Applied/Failed（沿用现有 UI 展示）。成功后写一次性通知
  水位 `qoder_env_enabled_at`（持久化 control，旧文件 serde 默认 None），UsagePanel 顶部
  显示可关闭横幅（重启 Qoder 后生效），关闭命令 `dismiss_qoder_env_notice` 只清水位、
  不动环境变量。fixture / local-test 构建跳过；macOS 返回 SuggestOnly 不写任何东西。
  单测用注入的 FakeEnvironment store，绝不碰真实 `HKCU\Environment`。

### 4.4 指标映射（核心口径）

下表为实施口径；标 **[P1 已实施]** 的行已在 adapter/pipeline 中落地并通过契约测试，其余为 P2+ 规划。

| transcript 记录 | 标准事件 | 字段与口径 |
|---|---|---|
| 会话首条记录 / `runtime-config` | `session_started`（derived）**[P1 已实施]** | model_id 取 runtime-config.model；sessionId 走 HMAC |
| `type:"user"`（`origin.kind=human`） | `turn_started`（derived）**[P1 已实施]** | promptId 作轮次键（缺失回退记录 uuid）；含 tool_result 的 user 记录不产轮次 |
| `type:"assistant"` 且 `message.usage` 存在 | `model_usage_recorded`（exact）**[P1 已实施]** | provider_id=`qoder`；model_id=`message.model`（路由别名原样上报，不猜具体模型）；**tokens**：四字段全 0（开关未生效/缺失≠0）不产事件；生效时按 **HWM 差分**取单次值（实测 input 含 cache_read、四字段为会话累计值）：input_context=Δinput+Δcache_creation、token_total=Δinput+Δcache_creation+Δoutput、cache_read=Δcache_read、cache_write=Δcache_creation；协议 `input_tokens`（未缓存输入）=Δinput−Δcache_read（饱和减）；首条非零观测只建基线不产事件，计数回落视为重置基线并跳过，accuracy=exact |
| 同上，`usage.credits > 0` | `cost_recorded`（exact，provider_reported）**[P1 已实施]** | units=credits×10^8 取整（i64），currency=`QODER_CREDIT`（§10.4 定名），cost_scope_key=事件自身键；credits 为单次值无需差分，守卫 (0, 1e6) 区间外拒收不造 0；`original_credits`/`billable` 明细暂不入 payload（P2 enrich） |
| assistant `stop_reason:"end_turn"` | `turn_completed`（derived）**[P1 已实施]** | 失败口径对齐 claude-code（§5.2/§979 行先例） |
| content 块 `type:"tool_use"` | `tool_invoked`（exact）**[P2]** | 实施时降级到 P2：result 记录跨行关联成本高，P1 不产出 |
| `subagents/*.jsonl` | `agent_spawned`（derived）**[P2]** | P1 已把 `subagents/*.jsonl` 纳入 discovery（同 harness 计量、`isSidechain` 原样参与 decode），独立 spawn 事件推迟 |
| 文件末条 + 30 分钟无追加 | `session_ended`（derived）**[P2]** | 会话边界兜底口径同现有 adapter，随现有 session 边界机制统一处理 |

- **不产出的事件**：`code_changed`（P1 无法可靠映射 Qoder 的编辑类工具语义；`file-history-snapshot`
  记录结构未定，P2 评估）、`skill_invoked`（skill_listing 存在但调用语义未知）、`tool_invoked` /
  `agent_spawned` / `session_ended`（实施时降级到 P2，见上表标注）。
- 幂等键：event_id 派生自 (sessionId, uuid 或 request_id) 的 HMAC——transcript 内 uuid 稳定，
  追加式文件的 (文件,偏移) checkpoint 保证 at-least-once + 服务端双幂等去重，与现有链路一致。

### 4.5 隐私

transcript 含用户 prompt 与代码正文：decode 白名单只取 §4.4 列出的字段，content 文本**不进**
FactDraft；`cwd` 只作项目路径 HMAC（对齐 claude-code adapter）；`requestTokenAnchor` 等 opaque
字段忽略；fixtures 的 privacy 用例断言事件负载不含正文片段。

### 4.6 兼容性边界

- 未知记录类型/未知 `type` 一律跳过（本机已见 20+ 种，天然前向兼容）。
- transcript 结构破坏性变更（assistant/usage 字段消失）：契约测试 pin 结构指纹，失配 → DEGRADED
  + 静默停采，不猜（对齐 zcode 的 schema 指纹先例；jsonl 用 fixture 形状而非 SQL 指纹）。
- credits 非单调实测已排除"累计值"歧义；若后续版本改为累计，HWM 差分规则对 credits 同样适用
  （decode 对 credits 与 exposed tokens 统一走 HWM 差分，单调性检测在 fixture 中固化）。
- CN 版 `~/.qoder-cn` 与国际版可并存：两源都采，同 harness 聚合；额度端点按 §5 分域。

## 5、P2：额度提供方（quota ring）

- 端点：国际版 `https://qoder.com/api/v2/me/usages/big_model_credits`，CN 版
  `https://qoder.com.cn/api/v2/...`；返回 used/total、百分比、`nextResetAt`；
  `totalQuota`+`sharedQuota` 同返时合并（CodexBar 口径）。
- 映射：单窗口 `QuotaWindow{used_percent, window_minutes=月度(按 nextResetAt 推), resets_at,
  label="credits"}`；`plan` 取套餐名。
- **认证两档**：
  1. 首选：验证 PAT（`QODER_PERSONAL_ACCESS_TOKEN` 环境变量自动发现）能否调通该端点——若通，
     全自动、零输入（对齐 zcode 的本地凭据自动发现体验）；
  2. 兜底：设置卡片粘贴 Cookie（CodexBar 模式，明文只存 platform_credentials，不跨 IPC）。
- 安全约束逐条复用 zcode.rs 先例：host 精确绑定（qoder.com / qoder.com.cn 白名单）、https、
  不跟随重定向、8s/4s 超时、256KB 响应上限、300s 缓存（keyed on 凭据 SHA256）、失败保留旧观测
  且不刷新 observed_at、local_test 不发网。

## 6、P3：IDE local.db 采集（token 主路径 + BYOK）

- `SqliteSnapshot` 安全读（§6.4 只读快照/WAL 规则）：`chat_message.token_info{prompt, completion,
  cached}` → input=prompt−cached（cached 为 prompt 子集）、output=completion、cache_read=cached；
  `model_info.model_key` → model_id；`gmt_create`(ms) → 时间；`chat_session.project_name` → 项目
  维度（HMAC）。
- schema 指纹保护：对齐 zcode——版本白名单（表/列存在性 + 字段级 fixture），未知 schema → DEGRADED，
  不跑猜测 SQL。本机无 IDE，实施第一步是在装有 IDE 的机器上导出真实 `local.db` 建 fixture。
- 与 transcript 源的**去重边界**：IDE 与 CLI 会话互不相通（官方论坛确认），两源事件以
  (源, sessionId) 区分，天然无重叠；若未来共享需引入源前缀。

## 7、触点清单（精确文件）

| # | 文件 | 改动 |
|---|---|---|
| 1 | `collector/adapters/qoder/`（新建 crate） | `src/lib.rs`（AgentAdapter：probe/setup_plan/discover_sources/decode/health）+ manifest + fixtures（见 §8）+ `tests/contract.rs`；模板对照 `adapters/droid/` |
| 2 | `collector/apps/service/src/local_store/pipeline/adapters/qoder.rs` + `registry.rs` | HarnessStrategy（jsonl tail 解码、HWM 差分、事件产出）+ 注册 |
| 3 | `collector/apps/service/src/detect.rs` | `detect_qoder`（~/.qoder、~/.qoder-cn、QODER_CONFIG_DIR） |
| 4 | `collector/apps/desktop/src-tauri/src/state.rs` `agent_metadata()`（11→12） | `("qoder", "Qoder", "dev.tokenshow.adapter.qoder")` |
| 5 | `commands/quotas/qoder.rs` + `quotas.rs`（P2） | 额度提供方 + `QODER_REFRESH` 注册 |
| 6 | 显示名/品牌三处：`orb/summary.rs`、`orb/model.rs`、`usage-analytics.ts`；web `harnessBrand.ts`（KNOWN+ALIASES：qoder/qoder-cli/qoder-ide/qoder-cn→qoder，色值取官网品牌主色） | 新 harness 展示 |
| 7 | `README.md` 工具表 | `| Qoder | 采集 CLI/桌面端会话 credits 消耗；额度查询支持个人套餐 |` |
| 8 | `docs/collector-plugin-architecture-and-acceptance.md` §7.x | 增补 Qoder Adapter 小节（本文档 §4/§6 的正式化） |

服务端/数据库无 schema 改动（事件走现有管线；`cost_recorded.currency` 为既有自由字符串字段）；
web 端需确认未知货币的展示（§10.4）。

## 8、测试与验收（对照 §16 fixtures 与 §18.2）

fixtures：

- `minimal`：单会话、credits-only（token 全 0）——断言 events 含 cost_recorded 且 tokens 全 null；
- `complete`：`QODER_EXPOSE_TOKEN_USAGE=true` 形态（真实 tokens，HWM 差分）+ subagents 子会话 +
  多轮 stop_reason；
- `error`：截断行、非法 JSON、usage 缺字段、credits 负数/NaN（拒收，不造 0）;
- `legacy`：旧版 CLI 记录（version 字段差异容忍）；
- `privacy`：断言事件负载不含 prompt 正文/cwd 明文/credential；
- `large-sample`：千行级 transcript 性能与增量 checkpoint 正确性。

golden summary + 契约测试（对齐 zcode 的 known/runtime/unknown 三态 fixture）；live 验证：
`TOKENDANCE_VERIFY_QODER_TRANSCRIPT=1` 的 ignored 测试直读本机真实 transcript（本机已有真实数据，
可直接跑）。验收项：装了 Qoder 的机器上自动检测→ACTIVE→事件产出→上传→网站出现在 Qoder 分组
（cost 维度有值、token 维度为空而非 0）；额度环 P2 验收同 zcode 形态。

## 9、分期与工作量

| 阶段 | 内容 | 估计 |
|---|---|---|
| P1 | transcript adapter 全链路（crate + pipeline + 注册 + 展示 + fixtures/测试 + 文档） | 2~3 人日 |
| P2 | 额度提供方（PAT 验证 + Cookie 兜底 + UI） | 1~2 人日（含验证） |
| P3 | IDE local.db token 路径 + BYOK | 2 人日（需先取得真实 local.db 样本） |

## 10、开放问题

1. **PAT 能否调用 `api/v2/me/usages/big_model_credits`**（决定额度是全自动还是 Cookie 手动）——
   实施时拿真 PAT 实测一次即可，风险低。
2. ~~usage 累计 vs 单次~~ **已实测关闭（2026-10-24）**：tokens 为会话累计值（HWM 差分必要，
   且 input 含 cache_read 需相减换算），credits 为单次值；契约 fixture 直接采用本机实测
   transcript（07fe7561 会话，含开关生效前后对照）。
3. ~~`QODER_EXPOSE_TOKEN_USAGE` 的写入边界~~ **已定（产品决策）**：非危险权限，Windows 检测到
   Qoder 后无感写入（无审批，保留备份/验证/回滚 + 通知 + 卸载恢复）；macOS 仅建议模式
   （GUI 应用不继承 shell 环境的硬限制）。
4. **credits 货币命名与 web 展示**：建议 `QODER_CREDIT`（避免与未来其它 credit 型 harness 混淆）；
   web 成本面板按多币种键展示还是折算留白，需产品确认（TokenBoard token 排行对 Qoder 天然为空，
   属口径而非缺陷）。
5. **模型别名展示**：`kmodel_latest`/`qfmodel` 等路由档位原样上报；web 是否做别名→友好名映射
   （需要官方公开映射表，目前 catalog 本地加密）。
