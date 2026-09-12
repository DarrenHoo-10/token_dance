# 统计口径、表与索引设计 v3

两端模块、事件协议、并发事务及内测直接启用的总方案见 [完整重构技术方案](event-pipeline-refactor-technical-plan-v1.md)；服务端目标表及完整 DDL 见 [MySQL 分册](event-pipeline-server-ddl-v1.md)。2026-09-12 已确认旧采集/统计数据不迁移，新表从空数据开始，页面不兼容读取旧历史。

范围：2026-09-11 当前工作区的桌面端、个人页、公开资料页、排行榜、社区首页、统计 API 和聚合代码。本文是调研与设计，未修改业务实现或数据库。用户确认的约束：并发策略采集、事件先落库、异步统计/上传、5 秒补偿、明细按本地 created_at 保存 14 天；**小时/日/月合表，用 grain 区分**。

建议采用四张统计表：`harness_metrics`、`model_metrics`、`skill_metrics`、`cost_metrics`。每张同时存 hour/day/month，四类数据分别按它们实际拥有的维度建唯一键。不是每个 harness 建一套表，也不需要为三个时间粒度复制三套 DDL。

公式、空值规则、来源规范化与乱序处理样例见 [统计计算契约 v1](D:/ProgrammingProjects/TokenDance/docs/metric-contract-v1.md)。estimated 用量已按用户要求排除；SQL 草案增加对应拒绝约束。

最新时间准入要求见 [事件时间规则](D:/ProgrammingProjects/TokenDance/docs/event-time-admission-v1.md)：时间优先级为源记录 → 同文件前序有效源时间 → 原始文件 mtime，payload_json.meta 标记 time_source。按选定时间筛选北京时间当天；缺少源时间时仅为近似归属，不保证每条记录真实发生在当天。已入库事件继续 14 天内的处理与重试，历史统计保留，不再用 1970 兜底。

完整建表语句见 [本地事件流水线完整 DDL v3](D:/ProgrammingProjects/TokenDance/docs/event-pipeline-ddl-v3.md)：10 张本地表，逐表包含字段、主键、外键、检查约束、初始化与索引。可执行副本见 [完整 SQLite SQL](D:/ProgrammingProjects/TokenDance/docs/event-pipeline-schema-v3.sqlite.sql)。服务端 MySQL 的完整 DDL 不在本文本地建表稿范围内。

## 1. 现有产品真正要统计什么

### 1.1 个人页的十项核心指标

来源：[MetricGrid](D:/ProgrammingProjects/TokenDance/web/src/components/analytics/MetricGrid.tsx:52)、[GetPersonalSummary](D:/ProgrammingProjects/TokenDance/server/internal/store/mysql/analytics.go:51)。

| 展示指标 | 目前实现的口径 | 新表需要的基础量 |
| --- | --- | --- |
| 费用 | 已上报费用与价格表补估的有效费用；页面称 estimatedCost，但实际可能混合两类来源 | 有效 reported/estimated 金额、币种、来源、定价覆盖与报价版本 |
| 总 Token | 个人页、趋势和排行榜为 exact + derived，不包括 estimated | 只保留 exact、derived 分列，总量为两者之和；不保留 estimated 用量分列 |
| 生成代码行数 | exact/derived 的 generated_lines；不是 added_lines，也不是仓库净增行 | generated、accepted、added、removed、correlated 分开 |
| 每行代码 Token | 总 Token / 生成代码行数；分母为 0 返回不可计算 | 存分子与分母，不存逐事件比率或平均比率 |
| 输入上下文 | 目前为 token_input + token_cache_read | 必须先统一各来源缓存包含关系，得到规范化 input_context_tokens |
| 输出 Token | exact/derived 的 token_output | 输出总量、是否包含 reasoning 的源定义与规范化版本 |
| 缓存命中率 | 目前为 cache_read / (input + cache_read)，分母为 0 返回 null | 同一覆盖样本中的缓存读取分子、输入上下文分母 |
| 活跃时长 | 每个日桶/主会话 session_ended 时长优先；没有会话时长则使用 turn_completed 时长 | session/turn 关联及已选贡献状态；累计毫秒 |
| 消息数 | 去重 turn 的 started 标记 + completed 标记，不是模型请求数或原始聊天行数 | turn_started_count、turn_completed_count |
| 用户消息数 | 去重 turn 中 trigger=user 的 started 数 | user_turn_started_count |

“活跃时长”目前是工具活动的累计时长，不是多会话时间区间去重后的人的实际工作时长；并发会话可以重叠。重构不能无意中改变其含义。

### 1.2 已展示/已有 API 的其他统计

| 功能 | 所需指标、筛选与范围 | 数据来源设计 |
| --- | --- | --- |
| 桌面今日曲线 | 每小时 Token；按 harness 看今日、近 7 天、累计 | model_metrics 的 hour/day/month，按 harness 汇总并包含 unknown 模型 |
| Token 趋势/结构 | 总量、输入、输出、缓存读写、reasoning；harness/provider/model 筛选 | model_metrics，同一次查询固定 grain |
| Harness 构成 | 各 harness Token、占比及接口已有费用等字段 | model_metrics 按 harness 求和；费用读 cost_metrics；比例分母范围一致 |
| 模型构成 | provider+model 身份、Token、请求数、占比 | model_metrics + model_dimensions |
| Skill 排行 | 使用数、现有精度分类、成功数、失败数、耗时、活跃天数、成功率 | skill_metrics；活跃天数由日桶按日期去重 |
| 活跃日历 | 日 Token、是否活跃、等级、活跃天数、当前/最长连续天数 | grain=day；不能只保留月汇总 |
| 近期活动接口 | 当前返回的是日/harness 或日/model 汇总行，不是原始会话事件 | 继续读统计表，不能误改成 14 天细节接口 |
| 排行榜 | today/7d/30d/all 的可信 Token、排名、变动、百分位、Top N | 服务端独立 user_window_scores/read model |
| 社区首页 | 日 Token、活跃开发者、生成代码行、交互量、费用、昨日变化、harness 占比 | 服务端 community_daily_stats 与 harness 日统计 |
| 同步/采集状态 | 各任务积压、租约、错误、最后 ACK 与各投影刷新时间 | processing_tasks、事件状态与运行状态；不混入用量指标 |
| 额度/套餐 | 使用百分比、重置时间、套餐、连接状态 | 独立 quota snapshot；不是可累计的使用事件 |

统计范围包含 today、7d、30d、10w、all，以及最多 366 天跨度的自定义日期范围：[ResolveTimeRange](D:/ProgrammingProjects/TokenDance/server/internal/analytics/service.go:37)。月桶是自然月，不能替代“近 30 天”。桌面还有年度日历，需要保留日粒度历史。

已有聚合还维护主/子会话数、交互轮次数、模型请求数、工具调用数、Skill 次数、代码采纳量。它们即使不是十个卡片中的独立项，也是应保留的统计基础。文件触达次数是事件 file_count 的累计，**不是去重文件数**，后者当前缺少可安全关联的文件身份。

Teams 当前页面是 unavailable 占位：[TeamDashboardPage](D:/ProgrammingProjects/TokenDance/web/src/pages/teams/TeamDashboardPage.tsx:7)。现阶段不把未上线的团队统计混入“已有口径”；保留服务端按 user/device 汇总再接团队成员关系的扩展点。

### 1.3 聚合算法分三类

| 类型 | 指标 | 处理方式 |
| --- | --- | --- |
| 可加值 | Token、有效费用、请求/工具/Skill 调用、代码行、规范化活动贡献 | 幂等处理事实版本后按桶合并；更正是替换贡献，不是再加一遍 |
| 非可加值 | 会话、轮次、活跃天数、跨事件的时长优先级 | 保存最小实体状态或从对应粒度成员集合计算 |
| 派生值 | 命中率、成功率、每行 Token、占比、环比、排名、连续天数 | 查询或读模型任务基于基础量生成；不平均各行比率 |

## 2. 已发现的口径分歧

以下是必须显式处理的规则，不是简单换表名就能解决。

1. **Token 精度不一致。** 社区统计 `exact+derived+estimated`，个人/排行榜 `exact+derived`；桌面 daily_agent_metrics 将能取到的 Token 合并，再记录最弱精度。依据：[社区](D:/ProgrammingProjects/TokenDance/server/internal/worker/community_stats.go:152)、[个人](D:/ProgrammingProjects/TokenDance/server/internal/store/mysql/analytics.go:62)、[桌面](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/mod.rs:902)。**用户已明确：不需要 estimated 用量。目标统一为 exact+derived，社区、个人、桌面和排行榜均不计入、不展示 estimated；统计表删除对应分列。derived 仅指可验证的确定性推导，不包含猜测值。**
2. **缓存包含关系不统一。** Codex 的 input 包含缓存，现有价格计算明确减去缓存；页面却统一 `input+cache_read`，会重复算这部分。依据：[定价说明](D:/ProgrammingProjects/TokenDance/server/internal/worker/pricing.go:112)、[输入与命中率](D:/ProgrammingProjects/TokenDance/server/internal/store/mysql/analytics.go:176)。策略必须给出完整输入上下文和缓存关系，聚合不能再判断 harness 名称来猜。
3. **总量兜底不同。** 桌面缺 total 时把 input/output/cache/reasoning/tool 全加，服务端兜底不包含 tool；而 reasoning 或 cache 本来可能已包含在其他字段中。依据：[本地 token 计算](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/usage_ledger.rs:65)、[服务端聚合](D:/ProgrammingProjects/TokenDance/server/internal/worker/aggregation.go:55)。优先保留源明确总量；只有证明组成互斥且完整时才能推导总量。
4. **时区不同。** 网站、上传日账统一 UTC+8，本地 usage_ledger/桌面日期曲线使用系统本地时区。新投影统一 UTC+8，created_at 则统一 UTC 时间戳；非北京时间用户的展示变化要明确。依据：[本地日期](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/usage_ledger.rs:49)。
5. **会话定义不同。** 服务端按桶去重“出现过的 session”，桌面 daily_* 计 SessionStarted；上传 activity 又保存所有 session 的集合。不能把“新建会话数”和“活跃会话数”叫成同一个字段。建议新 session_count 定义为该桶出现的去重主会话数，另保留 child_session_count；需要新建会话数时用明确新字段。
6. **社区交互量就是 model_request_count。** 不等于 turn_count，也不等于 message_count。[社区 SQL](D:/ProgrammingProjects/TokenDance/server/internal/worker/community_stats.go:152)；新 API 应将机器字段命名为 modelRequestCount，展示文案可以另定。现有活动接口在 model 筛选下还把 requestCount 填入 MessageCount：[GetActivity](D:/ProgrammingProjects/TokenDance/server/internal/store/mysql/analytics.go:1017)，应拆成两个字段。
7. **费用授权与估价生命周期不同。** 服务端估价只补缺失并保存价格版本，桌面可重估。费用必须按同一逻辑请求/轮次去重并明确 provider-reported 优先；不能把同范围上报值和估值相加。多币种不能相加或用 MAX(currency) 命名总额。建议历史价格计算结果冻结；只有具备旧贡献和可靠来源顺序时才做明确更正，不新增常驻重估版本体系。
8. **unsupported 不能靠明细是否还在。** 目前 summary 会查询 usage_events 判断费用、代码、时长覆盖。细节裁剪后这一判断会漂移。统计表保存 observed/known 计数，区分“采到了 0”与“没有能力/没有数据”，并报告部分覆盖。

上述规则用 `metric_semantics_version` 固定；与事件 schema_version、软件版本分开；不再另建投影 generation。本文给出建议，但没有修改现有产品计算结果。

## 3. 表设计：时间合表，统计主题分表

本次删除的是 accuracy=estimated 的用量/调用估计。基于已知 Token 和价格表计算费用是独立的定价来源（cost_source），目前仍沿用既有费用设计，不与估计用量混为一类。

### 3.1 公共字段与业务唯一键

本地按本机汇总，不持久化 account_id，也不在 JSON 中保存账号；上传阶段读取认证上下文，服务端确定归属。每张表前五列统一为 id、created_at、updated_at、delete_at、extra。created_at 首次写入后不变，updated_at 由 writer 在实际变更事务中维护，delete_at 为可空软删除时间，extra 为默认空对象的本地扩展信息。统计公共字段：

```text
id, created_at, updated_at, delete_at, extra
grain, bucket_start, harness_id, metric_semantics_version
```

grain 仅 hour/day/month。bucket_start 为 UTC 毫秒，含义是 UTC+8 对应小时、自然日、自然月的起点。不能将近 30 天写成 grain=month，也不能跨 grain 直接求和。每个桶只保留一份已提交结果，不设置多套 generation、版本头或重建进度表。

| 表 | 业务唯一键（含公共前缀） | 存什么 |
| --- | --- | --- |
| harness_metrics | grain、bucket_start、harness_id | 工具/Skill 次数、主子会话/轮次、消息、代码、活动时长；不再重复保存 Token、覆盖和模型请求 |
| model_metrics | 公共前缀 + model_key | Token/覆盖、模型请求数；不强行给会话/代码分配模型 |
| skill_metrics | 公共前缀 + skill_id | 调用/精度、成功/失败、耗时/覆盖 |
| cost_metrics | 公共前缀 + model_key、currency | 一份生效的上报/价格表计算金额及定价覆盖；按模型求和得到工具金额 |

model_dimensions 用 `(provider_id, model_id)` 唯一，不能单用 model 名；skill_dimensions 用完整匿名 skill_key 唯一，public_name 是可变标签，不参与事实去重。同一私有 Skill 在不同安装密钥下的 hash 不应被猜测合并。

Token、覆盖和模型请求只存 model_metrics，按模型求和得到工具总量，包含 unknown 模型。费用只存一份模型归属，不另存工具总额；按模型求和得到同币种 harness 金额。无法关联模型的费用归 unknown model，不能复制到轮次内每个模型。同币种内累计，当前网页美元卡片只读 USD，其余币种在桌面按币种展示。价格缺失时，估计目标币种与缺失记录来源应明确，不能伪造 0 美元已定价。

### 3.2 核心数值字段

| 类别 | 字段 |
| --- | --- |
| Token（model_metrics） | exact_token_total、derived_token_total；规范化 input_context_tokens、input_uncached_tokens、output_tokens、cache_read_tokens、cache_write_tokens、reasoning_tokens、tool_extra_tokens |
| 用量覆盖（model_metrics） | usage_observed_count、token_total_known_count、各组成 known_count；缓存比率另存 cache_pair_known_count、cache_eligible_input_tokens、cache_eligible_read_tokens |
| 行为 | model_request_count 在 model_metrics；harness_metrics 保存 tool_call_count、skill_use_count、session_count、child_session_count、interaction_turn_count、turn_started_count、turn_completed_count、user_turn_started_count |
| 代码 | code_generated_lines、code_accepted_lines、code_added_lines、code_removed_lines、code_file_touch_count、correlated_code_lines、code_known_count |
| 活动 | active_duration_ms、duration_known_count、message_known_count |
| Skill | use_count、各精度 use_count、success_count、failure_count、duration_ms、duration_known_count |
| 费用 | reported_cost_units、estimated_cost_units、reported_request_count、estimated_request_count、unpriced_request_count、cost_known_count |

规范化 input_context_tokens 表示完整输入上下文（包含缓存命中的输入）；output_tokens 为该来源定义下完整输出，reasoning 可以是其子集，不参与再次叠加。标准化业务字段在 14 天 payload_json 中按主题保存，不再同时保存 envelope 和展开业务列；统计表仍使用数值列。tool_extra 仅表达已证明未计入总量其他组成的额外量，无法证明时为未知。不强求各展示组成之和一定等于源报告 total。

Token 总量和分解量只来自 exact/derived；estimated 用量不进入标准事件统计链路，不保存估计用量的聚合列，也不以实际 0 替代未知。Skill 同样不保留 estimated_use_count；原有 correlated 分类与 estimated 区分，不因本次要求改写其含义。策略识别到 estimated 用量时返回明确 ignored 原因，由公共层记录最小诊断并原子推进读取状态，避免反复重采。known_count 依据实际可用字段增长，不能因为 SQL COALESCE 到 0 就算已知。缓存比率的分子分母必须来自同时有完整输入与缓存信息的相同请求集合，否则缺字段会让比率偏高或偏低。

代码/费用/时长已知为 0 时仍应增加 known_count。成功率 = success/(success+failure)，不把未知结果当失败；用户消息是 user_turn_started_count，消息总数由 started+completed 得到。

本地 DDL 使用受检 INTEGER（有符号 64 位）、金额按 10^-8 单位、时长毫秒；不存 FLOAT 金额。每次求和/更正以受检宽整数计算，溢出应失败并诊断，不能饱和或转 REAL。服务端可用更宽 DECIMAL(38,0) 存累计计数/金额单位，避免把 SQLite 上限当成全球汇总上限。接口以十进制字符串返回大数，UI 最后格式化。

### 3.3 events：14 天细节表

events 共 23 列。前五列是 id、created_at、updated_at、delete_at、extra；其余保留事实身份/版本、来源/类型、内容摘要、occurred_at/expire_at、模型/技能/会话/轮次/费用范围关联，以及 payload_json 和 status_json。

payload_json 按 usage、cost、code、activity、context、meta 分组。只保存该事件适用内容：金额为 cost.units/currency/source/price_basis_id，Token 为 usage 下的各组成；辅助关联和精度/时间来源分别进入 context/meta。不再保存 envelope_json 和重复业务列。extra 仅为本地可选扩展，不替代 payload、状态或账号持久化，不默认上传。

`expire_at = created_at + 1,209,600,000ms`（14 天）。首次只准入选定时间属于北京时间当天的事件；updated_at、delete_at 与重试都不刷新 TTL。到期物理删除包括已软删明细，任务级联清理，未完成事件按来源累计一次，不伪造 ACK。

删除 event_identity_ledger。正常采集按持久化来源游标增量读取；明细到期不清理游标。events 在保留期内保存身份、内容摘要、冻结时间和状态，唯一键覆盖仍存在的软删行。普通完成明细 revision=1；确有可靠原生修订时可利用尚在保留期的旧事件更正，不新增永久事实版本表。

status_json 中 hour/day/month/upload 四个键必填且为整数 0–6：pending、retry_wait、inflight、applied/acked、not_applicable、blocked、quarantined。processing_tasks 只保存租约、重试和执行时间；完成状态只有这一处。使用 json_set 修改单一状态路径，在同一 writer 事务里更新统计、实体状态、updated_at 并删除已完成任务。

本机业务列表与统计读取排除软删行；来源、维度、事实身份查找保留软删记录，避免断开历史引用。软删除不释放业务唯一键、不触发外键级联，也不自动撤回既有贡献；涉及停止处理或撤回统计的操作必须在业务事务中处理。正常读取与 upsert 不得隐式复活尚存在的软删数据。原库不提供任意历史重放，重新开始本地采集走重装并初始化本地数据的流程。

## 4. 非可加统计需要的最小状态

不能只保留累计值和 applied 标记，就声称任何会话、任何月底都可精确去重。

- `bucket_entity_state`：按 grain/bucket_start/harness_id/entity_kind/entity_key 唯一，保存 session/turn 哈希、父关系、started/completed/user 标记、必要的已知 duration；删除泛用 revision 字段，writer 事务内读取最新状态再更新。没有 prompt、聊天内容或完整事件。
- 日会话数和月会话数分别按该粒度去重。同一会话出现两天：两天各 1，月唯一会话仍为 1。不能用日会话数相加得到月唯一会话；跨月/全历史唯一会话同样要相应集合，当前十项卡片不要求全历史会话唯一数，不能无谓增加一张全历史事实副本。
- 活动时长兼容现有日规则：主会话时长替换同日轮次兜底，必须撤销原先贡献，再加权威贡献。为了小时和日可对账，小时处理也按该业务日的相同选择规则，撤回已计入其他小时的同日 fallback，将已确认会话时长归到结束事件小时。这里是结束归属的工具累计时长，不把 duration 均匀摊成实际墙钟占用。月累加这些规范化日贡献。若后续改成区间占用，需要新的语义版本。
- 费用先确定请求/轮次的权威 cost_scope_key 和有效来源。14 天内的候选可从 events 有界查询，必要时加局部执行状态；明细删除后不能再从残缺候选重新“选最优”。新修订也必须满足当天首次准入；被拒绝的历史记录不能借更正重新进入。已准入更正的投影替换必须有旧贡献，不能直接叠加新金额。
- Skill 活跃天数、活跃日历和 streak 读日桶日期集合。多个 harness 同一天调用同一 Skill，只算一个活跃日；不要 SUM 各 harness 的 active_days。日历活跃目前是可信 Token > 0，等级阈值 100 万/500 万/2000 万；今天暂时为空不立即断 streak。

这些状态不是另一份完整事件明细，但仍有存储成本。日/月/小时实体状态保留期按查询与更正保证单独约定；没有可证明安全的删除边界前，不能承诺任意古老会话重新出现还能精确去重。优先只保留实际需要的实体粒度，不默认保存全部事件的永久贡献明细。

## 5. 索引按查询设计

### 5.1 明细与队列

| 索引 | 对应查询/保证 |
| --- | --- |
| events UNIQUE(event_id) | 重采集幂等；同 ID 的内容冲突由事务显式检测 |
| events UNIQUE(fact_key,fact_revision) | 同一事实版本唯一，避免不同 event_id 绕过版本身份 |
| events(expire_at,id) | 14 天物理清理，包括软删记录；范围扫描 + 有界批次 |
| events(occurred_at DESC,id DESC) WHERE delete_at IS NULL | 本机近期细节；keyset 翻页 |
| events(harness_id,occurred_at DESC,id DESC) WHERE delete_at IS NULL | 单 harness 细节/定位 |
| events(harness_id,session_key,occurred_at,id) WHERE 未删除且 session_key 非空 | 按同会话/日期读取时长权威选择的已应用事实；不扫同日所有会话 |
| events(harness_id,cost_scope_key,occurred_at,id) WHERE 未删除且 cost_scope_key 非空 | 按计费范围读取价格与报告费用，计算旧/新生效贡献差额 |
| tasks(consumer,runnable_at,event_row_id) WHERE delete_at IS NULL AND runnable_at IS NOT NULL | 5 秒到期待办扫描、有界领取 |
| tasks(consumer,lease_until,event_row_id) WHERE lease_until IS NOT NULL | 回收超时租约，包括已软删在途任务；与 pending 分开扫描 |
| tasks UNIQUE(event_row_id,consumer) | 每个事件/消费者只有一项任务；同一索引前缀支持过期级联清理，不再建 event_row_id 单列索引 |

事件 status_json 是完成状态唯一来源；runnable_at 是任务调度入口。先按任务索引有界选择，再按主键核验事件状态、未软删和未过期；上传额外检查运行时认证上下文。四个 JSON 状态不各建索引。JSON 全表条件扫描代价明显高于数值列，因此不把补偿实现为每 5 秒扫描全部事件 JSON。事件和任务同事务创建，避免漏建任务。

失败重试设置 runnable_at，领取后设 NULL 并写租约，blocked/隔离也不进入到期集合；认证恢复时重排对应任务。每轮按 `(runnable_at, event_row_id)` 分页，不把 last_seq 当永久 ACK 水位，低序号失败可以回到队列。

### 5.2 统计表

所有表主键为 `id`；统计公共业务唯一键：`(grain, bucket_start, harness_id, [主题维度])`。

选择这个顺序是因为主要查询都是“某粒度、时间范围”，既能读取全 harness 汇总，也能读取日历。harness 在 range 后，对单 harness 长历史不够高效，因此另加筛选索引：

```text
harness_metrics(grain, harness_id, bucket_start)
model_metrics(grain, harness_id, bucket_start, model_key)
model_metrics(grain, model_key, bucket_start, harness_id)
```

这些工具/模型筛选索引采用 WHERE delete_at IS NULL；统计业务唯一键仍覆盖全部行，软删不释放桶身份。无需为每表的 delete_at 或 updated_at 单独建索引。

provider 筛选先在 model_dimensions 通过 `(provider_id,model_id)` 找 model_key，再走模型索引。暂不为每一种可选筛选排列都建索引。

Skill 的主要路径是“本机某段时间全部 Skill 的 SUM(use_count) 排名”，业务唯一索引前缀足够缩小数据；聚合结果仍需排序。给 use_count 单独建索引不能解决任意时间窗口下的 SUM 排名，因此不加无效索引。费用同理先按范围唯一索引读取；若单币种/单模型查询成为热点，再实测补 `(grain,currency,bucket_start,...)`。

hour/day/month 合表后，所有统计查询必须带 grain。比例先在选定范围汇总分子分母再相除，不对 hourly cacheHitRate 取平均。完整月份可用月桶加速长期**可加指标**，不足整月的边界读日桶，两个范围严格不重叠；不能把整月统计与同月日统计一起加。日历和任意日期范围仍由日桶支撑。

### 5.3 来源进度

collection_sources 保留来源业务唯一键、来源与 harness 复合外键所需唯一键、due 和 lease 四个辅助索引。enabled 列表没有单独索引；来源精确查找和两个扫描路径已验证。events 的 UNIQUE(fact_key,fact_revision) 支持保留期内某事实的版本查找。来源游标长期保留，但不在 decoder_state 或 extra 中转存永久逐事件身份集合。

每个索引和每张表的具体取舍见 [精简审查](schema-simplification-review-v1.md)。所有索引服务当前查询或数据库约束；不会因为字段可能被筛选就预先创建索引。

## 6. 服务端 MySQL 与全局读模型

服务端按事件 occurred_at 过滤允许接收的时间范围，与重放时间、上传时间、本地 created_at 无关；范围内重复仍用事件唯一键处理。具体时间边界由服务端策略定义，不能将最大收到时间当成并发任务已全部完成的水位。客户端明细 14 天不自动决定服务端事实保留期。服务端接收按 `(installation_id,event_id)` 唯一，并比较内容摘要；created_at/received_at 由各层分别定义，不能信任客户端时间替服务器分配持久化顺序。

服务端统计应保留 device/installation 贡献边界，支持多设备汇总与设备撤销/删除。基础业务唯一键可为 `(user_id, grain, bucket_start, installation_id, harness_id,...)`；当前单用户查询按前缀范围读取多设备贡献。异步复用现有用户读模型刷新机制，避免网页请求扫描原始 events；旧统计结果不迁移，由新事件重新生成。

不同安装的匿名 session/turn 身份默认隔离。Token、调用数等可加指标可跨设备相加；“同一个人多设备的同一会话”去重没有可靠全局身份时不承诺自动合并。活跃开发者则按 user_id 去重，不能把 harness 或 device 计数相加。

独立保留：

- user_window_scores：复用现有用户 today/7d/30d/all 的可信 Token 读模型。排名索引围绕窗口、参与资格和排序字段设计；相同 Token 以注册时间、user_id 打破并列。本地表精简不新增服务端排名版本表或每种时间范围一张表。
- community_daily_stats：按 day 主键；community_agent_daily_stats 按 day+harness。汇总应明确用户资格范围，并与主数字精度口径一致；社区环比基数为 0/缺失时返回 null，不伪造 0%。
- 公开资料仍应用现有隐私规则，统计表不能代替访问控制。Teams 若上线，通过成员身份/权限和日期有效区间生成团队读模型，不把个人事件复制为新的计数事实。

MySQL 不照搬 SQLite 的部分索引语法：到期任务采用普通复合索引或可索引的生成列；用租约/行锁协议领取。本文执行计划只验证 SQLite，不代表已完成 MySQL 压测、锁竞争或全球数据量的容量验收。

## 7. 交付与验证

- [基础统计 SQLite DDL](D:/ProgrammingProjects/TokenDance/docs/statistics-schema-v3.sqlite.sql)：10 张表的派生统计/索引实验输入（含来源外键依赖）。[完整 DDL 文档](D:/ProgrammingProjects/TokenDance/docs/event-pipeline-ddl-v3.md) 作为唯一建表来源，包含合并的来源/游标、事件/队列、维度与统计，共 10 张表；不再包含身份账本、独立事实版本、重建版本或诊断表；本地不保存账号，每表统一五个公共字段，events 采用业务/状态 JSON。基础 SQL 从完整 SQL 派生。两者都不是生产 migration。
- [索引验证结果](D:/ProgrammingProjects/TokenDance/docs/statistics-index-validation-v3.md)：4000 条合成事件，9 条关键查询使用相应索引或整数主键且无额外 ORDER BY 临时排序；验证 created_at 起算 14 天和删除事件级联删除任务。
- 生成脚本校验 10 张表、183 个字段注释、每表五个公共字段和 23 列 events；23 项非法写入包括 JSON 状态缺失/错误类型、负数或溢出 Token、缓存关系、金额缺币种、extra 非对象、时间与外键等。统计脚本另验证覆盖约束、含 unknown 模型的总量、币种隔离、软删过滤/唯一性以及物理 TTL。
- [生成/验证脚本](D:/ProgrammingProjects/TokenDance/docs/statistics-schema-lab.py)：只创建内存 SQLite，不连接用户库或 MySQL。先运行 build-event-pipeline-ddl.py 验证已有字段注释并生成文档和派生 SQL，再运行索引实验。结果是可执行性和访问路径检查，不是吞吐基准，也没有实现统计 worker。

下一步实现必须先固定 Token 缓存/精度、会话、消息、时长和费用规则的语义版本，针对重复/乱序/跨日跨月/部分覆盖写对账场景；再接公共投影 runner。仅验证 DDL 创建成功不能证明这些统计口径已正确实现。
