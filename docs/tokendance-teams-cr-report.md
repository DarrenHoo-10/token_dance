# TokenDance 团队模块 CR 报告

首次审查：2026-09-06。最近更新：2026-09-12。

当前工作区：`D:/ProgrammingProjects/TokenDance`。分支：`codex/team-init`。本轮基于 `ebde9473760ffa3de7a45bc536a1dfcfed999dfc` 合并 `origin/main` 的 `c7ba493b5cf5a33a891be0e387ed2b5e4f870675`；首次审查基线为 `728033f`。

**本轮审查对象是合并最新 main 后的团队实现及衔接代码。** CR-001–CR-014 和前两轮验证记录保留历史证据；第 6 节保留合并后的失败证据；2026-09-12 的修复与当前结论见第 7 节。

依据：[产品与交互方案](tokendance-teams-product-design-v1.md)、[技术方案](tokendance-teams-technical-design-v1.md)。

## 1. 结论与跟踪规则

累计记录 **17 项问题：8 项 P1、9 项 P2，均已关闭**。CR-015、CR-016 已在合并提交 `3b4d66bbaf964b2e4565ca56907d392ee83963c8` 之后的当前工作区修复，并通过云端隔离 MySQL 的复现与功能回归；详见第 7 节。CR-010、CR-017 的云端复验记录继续保留。此结论不代表第 6.6 节的主干既有失败和环境受限测试已通过。

前两轮未运行真实 MySQL；本轮在云端 MySQL 8.0.46 的独立临时 schema 执行集成测试，结果、失败和环境限制均记录在第 6 节。现有 `tokendance_dev` 账号与团队数据不用于清库测试。

本报告作为本工作区团队模块 CR 的持续跟踪入口：

- 状态使用「待修复 → 待复验 → 已关闭」；复验失败退回「待修复」。
- 修复后填写实际提交或工作区修改位置，以及新增回归测试；不能只凭修改说明关闭问题。
- 关闭时记录复验命令、结果与日期。需要 MySQL 的检查被 skip 时，仍保持待复验。
- 后续问题从 CR-018 继续编号；不重排已有编号。新增结论与状态变化写入末尾更新记录。

| 编号 | 等级 | 问题 | 证据 | 状态 | 修复 / 复验记录 |
| --- | --- | --- | --- | --- | --- |
| CR-001 | P1 | 撤回共享后旧数据重新进入统计 | 聚合函数复现 + SQL 检查 | 已关闭 | 工作区：`server/internal/worker/team_analysis.go`。回归：`TestGrantCoversTimeExcludesRevoked`、`TestBuildMemberAnalysisRowsRevokedBaseMustNotReappear`。2026-09-06 `go test ./internal/worker` 通过。未跑真实 MySQL 撤回链。 |
| CR-002 | P1 | 上海时区统计范围偏移一天 | 日期转换复现 + SQL / Worker 检查 | 已关闭 | 工作区：`domain.FormatTeamCalendarDate`；Store/Memory 写 DATE 改用团队本地日历日。回归：`TestFormatTeamCalendarDateShanghai`、`TestResolveTeamRangeShanghaiCustomStaysOnRequestedDay`。 |
| CR-003 | P1 | 成员响应缺少前端必需字段 | 实际 HTTP Handler 响应复现 | 已关闭 | 工作区：`MemberDTO` / `ListMembers`。回归：`TestTeamMembersPayloadMatchesWeb`。2026-09-06 HTTP 定向测试通过。 |
| CR-004 | P1 | 邮箱邀请预览与前端字段不一致 | 实际 HTTP Handler 响应复现 | 已关闭 | 工作区：`InvitationPreviewDTO` / `InboxInvitationDTO`。回归：`TestEmailInvitationPreviewMatchesWeb`（预览 + 收件箱）。 |
| CR-005 | P1 | 幂等重试生成不可用的邀请链接 | 实际 HTTP 请求链复现 | 已关闭 | 工作区：`inviteLinkResponseToken`。回归：`TestInviteLinkIdempotentRetryReturnsUsableToken`。 |
| CR-006 | P1 | 未登录打开邮箱邀请永久加载 | 前端状态流静态检查 | 已关闭 | 工作区：`InvitationPage.tsx`。回归：`teams-invitation.test.tsx` 未登录展示登录入口。 |
| CR-007 | P2 | 估算费用及覆盖率被固定值覆盖 | 响应组装函数复现 | 已关闭 | 工作区：`assembleAnalysis`。回归：`TestAssembleAnalysisPreservesEstimatedCostAndCoverage`。 |
| CR-008 | P2 | Token 使用字符串比较排序 | 排序函数复现 | 已关闭 | 工作区：`sortKV` / `cmpIntDecimal`。回归：`TestSortKVIsNumeric`。 |
| CR-009 | P2 | 分析首次就绪后不再刷新 | hook 与版本更新机制静态检查 | 已关闭 | 工作区：`useTeamAnalysis.ts` ready 后继续轮询；卸载递增 seq。回归：`teams-analysis.test.tsx` 就绪后刷新并在离开后停止。 |
| CR-010 | P2 | 快照清理被导出外键阻断 | 真实云端 MySQL FK 删除链复验 | 已关闭 | 2026-09-12 `TestTeamCleanupKeepsSnapshotsReferencedByExportsMySQL` 通过：引用保留，未引用删除，导出元数据过期后快照删除。 |
| CR-011 | P2 | 分析分类、贡献榜及筛选选项缺少展示字段 | 实际响应组装函数复现 + 前端消费检查 | 已关闭 | 工作区：`pageBuckets` / `pageContributions` / `setToOptions`。回归：`TestAssembleAnalysisCollectionsMatchWeb`。 |
| CR-012 | P2 | 成员详情仍丢弃费用与分类统计 | 实际 GetMember 调用复现 | 已关闭 | 工作区：`assembleMemberDetail`。回归：`TestAssembleMemberDetailPreservesCosts`。未共享维度返回 unavailable，不返回空费用冒充 available。 |
| CR-013 | P2 | 独立费用无法覆盖同组无金额的 usage | 实际 Worker 聚合函数复现 | 已关闭 | 工作区：`groupTeamCostEvents` 按 session/turn 保留无金额 usage。回归：`TestCostOnlyRecordCoversAssociatedUsage`。 |
| CR-014 | P2 | 从筛选后的分析页导出时丢失筛选条件 | 前端请求 → Handler → 任务 → Worker 静态链路检查 | 已关闭 | 工作区：分析页传 agent/provider/model；分析响应补 `filtersHash`。回归：`teams-analysis.test.tsx` 筛选后导出。未跑真实 CSV Worker 联调。 |

| CR-015 | P1 | main 的 v2 上传未接入团队统计与 source revision | 真实云端 v2 入库 → 个人聚合 → 团队快照复现 | 已关闭 | 新增 `worker/team_telemetry.go`；v2 接收事务更新 source revision，修正刷新版本。原复现、重放/冲突、构建期间新上传及云端完整流程通过，见第 7 节。 |
| CR-016 | P1 | 同组费用借用已授权 usage 的时间窗口，泄露开启共享前的金额 | Worker 聚合函数复现，base / cost 两维度均失败 | 已关闭 | 费用先逐条授权，按自身日期与可见性统计；规则版本升至 2，旧快照及导出拒绝读取。原复现、撤回/重开、跨日与云端 CSV 流程通过，见第 7 节。 |

| CR-017 | P2 | 清库函数遗漏团队表，集成测试相互污染 | 全套云端测试中发现 8 条前序 receipts 残留 | 已关闭 | `migrate/runner.go` 补齐 15 张表；2026-09-12 `TestMigrationRunnerResetRemovesTeamTables` 及受影响清理用例均在云端复验通过。 |

## 2. 问题详情

### CR-001 · P1 · 撤回共享后旧数据重新进入统计

位置：[team_analysis.go](../server/internal/worker/team_analysis.go)，`loadTeamAnalysisGrants`，首次审查行 391–394；相关撤销逻辑在 [teams.go](../server/internal/store/mysql/teams.go) 的 `revokeActiveGrant` / `revokeAllActiveGrants`。

授权撤回会设置 `ends_at`、`revoked_at` 并清空 `active_dimension`，但 Worker 查询仍加载所有历史 grants，也未读取 `revoked_at`。后续只判断事件是否落在 starts_at/ends_at 之间，因此撤回前的数据在新 authRevision 的快照里再次出现。关闭 base、named、classification、cost 均受影响；让旧快照失效本身不能解决。

复现：给聚合函数输入数据库撤销后查询实际投影出的关闭授权窗口，以及窗口内一条 123 Token 事件，仍得到 `tokens=123`，预期该贡献不再可见。

修复要求：源查询排除已撤销授权，保留各维度独立生效时间。补充关闭基础共享、仅撤回 named、关闭后重新开启且不恢复旧历史的测试；最后通过真实 MySQL 的撤回 → 重建 → 查询链验证。

### CR-002 · P1 · 上海时区统计范围偏移一天

位置：[team_analysis.go（Store）](../server/internal/store/mysql/team_analysis.go)，`GetOrQueueAnalysis`，首次审查行 240、272；读取逻辑在 [team_analysis.go（Worker）](../server/internal/worker/team_analysis.go) 的 `readTeamAnalysisSource`。

`ResolveTeamRange` 返回 UTC 时间边界；Store 用 `UTC().Format("2006-01-02")` 写 DATE，Worker 又把该 DATE 当作团队本地日期恢复午夜。对于正 UTC 偏移时区，日期因此提前一天。

复现：上海时区自定义查询 2026-09-06 当天，正确 UTC 范围为 `[09-05 16:00, 09-06 16:00)`，当前转换后 Worker 读取 `[09-04 16:00, 09-05 16:00)`。该复现验证实际代码的日期转换组合，未执行真实 MySQL。

修复要求：明确 DATE 存团队本地日历日期，或改为完整 UTC 时间边界；排队、查找、去重 key、刷新与 Worker 使用同一语义。回归上海、UTC、负偏移时区、DST 及单日 / 7 日查询。

### CR-003 · P1 · 成员响应缺少前端必需字段

位置：[service.go](../server/internal/teams/service.go)，`MemberDTO` / `ListMembers`，首次审查行 306–314；消费位置 [TeamMembersPage.tsx](../web/src/pages/teams/TeamMembersPage.tsx)，首次审查行 160、308。

实际响应使用 `id`，没有前端 `TeamMember` 类型要求的 `membershipId`、`sharing`、`syncStatus`、`canOpenDetail`；`periodTokens` 也使用字符串，而前端声明为指标对象。前端直接读取 `member.sharing.base`，会抛异常；角色调整、移除和详情操作也无法取得正确的 membershipId。

复现：经 HTTP 创建团队后读取 members，返回 200，但唯一 owner 行缺少上述字段。这不是请求失败，而是成功响应的契约错误。

修复要求：统一 DTO 与前端类型及空值约定，提供真实共享 / 同步状态和授权后的周期指标。用实际后端响应驱动成员页测试，验证渲染、详情、改角色和移除，不只使用手写前端 fixture。

### CR-004 · P1 · 邮箱邀请预览与前端字段不一致

位置：[InvitationPage.tsx](../web/src/pages/teams/InvitationPage.tsx)，首次审查行 103–105；响应定义在 [service.go](../server/internal/teams/service.go) 的 `InvitationPreviewDTO`，首次审查行 271 起。

后端返回 `teamName`、`role`，前端读取 `preview.team.name`、`preview.invitedRole`，effect 中也读取 `invitation.team.id`。真实接口成功后会出现属性访问异常，不能正常展示并确认加入。

复现：创建定向邀请后调用预览接口，实际 JSON 含 `teamName` 和 `role`，不含 `team` 与 `invitedRole`。现有前端模拟数据与后端结构不同，因而未发现问题。

修复要求：统一邮箱预览契约，并检查本人待接受邀请列表使用的同类结构。分别覆盖已在本队、无团队、已有其他团队的真实响应；邀请角色和团队身份显示正确后才能关闭。

### CR-005 · P1 · 幂等重试生成不可用的邀请链接

位置：[service.go](../server/internal/teams/service.go)，`CreateInviteLink`，首次审查行 1036–1040；`RegenerateInviteLink` 行 1151 有同类逻辑。

每次请求先生成新 token；命中幂等回执后，Store 返回已存在的旧链接记录，但 Service 仍使用本次新 token 拼接旧 linkId。返回 URL 的凭证与数据库 token_hash 不匹配。

复现：相同请求体、相同 Idempotency-Key 连续创建两次，两次均返回 201；第一次返回链接预览为 200，第二次返回链接预览为 `404 TEAM_INVITE_LINK_NOT_FOUND`。生成成功但响应丢失后的正常重试会触发此问题。

修复要求：重放时使用已保存链接的加密 token 还原 shareUrl，同时校验当前权限与链接终态。覆盖生成与重新生成的响应丢失重试、撤销后重放、到期后重放。

### CR-006 · P1 · 未登录打开邮箱邀请永久加载

位置：[InvitationPage.tsx](../web/src/pages/teams/InvitationPage.tsx)，`loading` 初始化与 effect，首次审查行 23、32–33、48–50。

loading 初始为 true；未登录时 effect 直接返回，不会执行清除 loading。渲染先判断 `authLoading || loading`，因此下面的登录 / 注册入口不可达，收到邮件的未登录用户停留在加载状态。

证据：前端状态流静态检查，本轮未另跑浏览器复现。

修复要求：区分认证初始化、未登录和邀请加载状态；未登录时优先显示入口。补充未登录打开邮件、完成登录回跳、注册建档回跳以及请求失败状态的测试。

### CR-007 · P2 · 估算费用及覆盖率被固定值覆盖

位置：[service.go](../server/internal/teams/service.go)，`assembleAnalysis`，首次审查行 2051–2054。

响应硬编码 `estimatedUncovered=[]` 与覆盖率计数 `0/0`，没有汇总快照中的估算费用、已记录费用覆盖计数及有效用量分母。已有估算数据时页面仍显示费用不可用，已有费用记录也无法展示真实覆盖率。

复现：输入一条有效用量和 3.50000000 USD 估算费用、对应覆盖计数，响应仍为空估算列表和零分母。

修复要求：按当前 filters、币种和统计口径汇总所有费用 / 覆盖字段，避免重复计数；分母为零、真实零费用、费用未共享与来源缺失应区别处理。验证页面与同快照导出一致。

### CR-008 · P2 · Token 使用字符串比较排序

位置：[service.go](../server/internal/teams/service.go)，`sortKV`，首次审查行 2072–2075。

十进制数字字符串直接使用 `>` 比较，得到字典序而非数值顺序，影响贡献榜和 Agent / 模型列表及其分页。

复现：输入 larger=100、smaller=9，结果把 smaller 排在 larger 前面。

修复要求：使用精确整数比较，不能转换浮点数损失大整数精度；同值继续使用稳定 ID 排序。测试跨位数、超出 JS 安全整数范围、相同值与分页边界。

### CR-009 · P2 · 分析首次就绪后不再刷新

位置：[useTeamAnalysis.ts](../web/src/pages/teams/useTeamAnalysis.ts)，首次审查行 76–79、99；相关机制见 [TeamContext.tsx](../web/src/context/TeamContext.tsx)。

hook 仅在 updating / 版本不符等分支安排下一次请求，ready 后停止轮询，`refreshing=true` 也只更新提示。普通新事件只增加 sourceRevision，不增加 authRevision；TeamContext 的定时刷新不能触发依赖 authRevision 的分析 effect。因此页面停留时不会持续展示新用量，需切换页面或筛选才会重查。

证据：hook 依赖及状态流静态检查，本轮未运行新的前端定时器复现。

修复要求：为 ready 状态提供合理的定时 / 焦点刷新；refreshing 时继续获取更新。用假时钟测试授权版本不变但数据增长、离开页面清除定时器，以及旧请求迟到不覆盖新结果。

### CR-010 · P2 · 快照清理被导出外键阻断

位置：[team_cleanup.go](../server/internal/worker/team_cleanup.go)，首次审查行 117–123；[0005_tokendance_teams.sql](../server/db/migrations/0005_tokendance_teams.sql)，首次审查行 331–332。

删除快照时，只排除部分状态且未过期的导出引用。过期、撤销或失败导出仍可能保留对应 snapshot_id，外键却没有 ON DELETE CASCADE；导出元信息按设计保留 30 天。因此导出到期后清理关联快照会触发外键错误，导致该批快照清理中断。

证据：迁移外键和清理 SQL 的静态交叉检查；本轮 MySQL 集成测试不可用，没有实际执行此 DELETE 复现。

修复要求：明确快照和导出元信息的依赖生命周期，删除前处理全部引用，或保留所有仍被引用的快照。不要直接级联删除仍需展示的导出记录。用真实 MySQL 覆盖完成后到期、撤销、失败及多快照混合批次。

### CR-011 · P2 · 分析集合响应仍缺少前端展示字段

位置：[service.go](../server/internal/teams/service.go)，`pageMaps` 第 2189 行，`setToOptions` 第 2307 行；调用位于 `assembleAnalysis`。

有真实统计数据时，agents / models 只返回 agentId / modelId 与 tokens，前端 `AnalysisBucketItem` 却读取 id、label；贡献榜只返回 membershipId 与 tokens，概览读取 displayName、rank。Agent 图例和模型表名称为空，贡献榜缺少成员名称和排名。筛选选项同样仅有 id，`TeamAnalyticsPage` 的 option 文本读取 label，因此选项无名称。

复现：输入一条 codex / openai / gpt-test、123 Token 的实名统计行，调用实际 `assembleAnalysis`；隔离测试报告 agents / models 缺少 id、label，contributions 缺少 displayName、rank。对照消费点：`TeamAnalyticsPage.tsx:83–89,117–121,168–176`、`TeamOverviewPage.tsx:146–152`。无需数据库即可复现。

修复要求：为分类、贡献及筛选结果定义与前端一致的 DTO，填充稳定 id、展示名和排名；未共享分类桶提供 bucketType。贡献身份仅在 named 授权允许时补充。增加由实际后端响应驱动图表、榜单和筛选下拉框的测试。

### CR-012 · P2 · 成员详情仍返回固定空费用

位置：[service.go](../server/internal/teams/service.go)，`GetMember` 第 671–673 行。

CR-007 已修复分析总览的组装，但成员详情仍将 reported、estimatedUncovered、agents、models 硬编码为空数组，覆盖率固定 0/0，三个维度固定 available。已共享费用的成员即使快照中存在费用记录，打开详情也看不到金额；关闭分类或费用共享时，dimensions 仍错误声明可用。

复现：提供当前授权版本下、named / classification / cost 均已授权的一条成员统计行（USD 2，usage 1，reported covered 1），通过最小 Store 替身调用实际 `GetMember`，返回费用为空、覆盖率 0/0。测试 `TestCR2MemberDetailPreservesCosts` 失败，输入行与 Service 代码均非前端模拟响应。

修复要求：按目标 membership 和每行 visibility mask 汇总费用、分类及覆盖率；维度不可用时返回真实状态，不能用空数组和 available 掩盖缺失。成员列表修复 CR-003 不代表详情指标已实现。

### CR-013 · P2 · 独立费用记录的 usage 覆盖率恒为零

位置：[team_analysis.go](../server/internal/worker/team_analysis.go)，`groupTeamCostEvents` 第 541–547 行；相关 `applyCostGroup` 第 585–586 行。

分组阶段直接跳过没有 costAmount 的 model_usage_recorded，而后续仅从 group.events 提取 usageIDs。常见输入是 usage 只带 Token，同一 user / installation / agent / session / turn 的 cost_recorded 单独提供费用；此时组里只剩费用事件，金额虽进入统计，但没有 usage ID 可计入 reportedCovered，费用覆盖率被低估。

复现：一条 123 Token usage + 同一可靠关联组的 USD 2 provider_reported cost_recorded，基础和费用共享均有效。调用实际 `buildMemberAnalysisRows` 后，覆盖率为 **0/1，预期 1/1**；`TestCR2CostOnlyRecordCoversAssociatedUsage` 失败。技术方案第 292、298 行要求按可关联的有效 usage ID 计覆盖率。

修复要求：费用关联保留无金额的 usage，先应用各事件授权，再关联费用记录并去重计覆盖；继续保证币种展开不会复制 Token 或 usage 分母。补充 usage 无金额、费用独立上报及多币种的 Worker 测试。

### CR-014 · P2 · 导出未携带当前筛选条件

位置：[TeamAnalyticsPage.tsx](../web/src/pages/teams/TeamAnalyticsPage.tsx)，`startExport` 第 92–94 行；相关 `web/src/api/teams.ts:646`、`server/internal/httpapi/teams.go:593–598`、`server/internal/teams/service.go:1461`。

选择 Agent 或模型后，页面导出只提交 snapshotId、kind、filtersHash。当前分析响应不提供 filtersHash，JSON 序列化会省略 undefined；后端创建任务实际读取 agent / provider / model，三项均得到 nil，存为未筛选的 FilterJSON。Worker 再按该 FilterJSON 过滤整份快照，因此 CSV 会包含页面已筛除的数据。即使以后前端传出 hash，现有 Handler 也没有用 hash 还原筛选的实现。

复现链：分析页设置 agent / model → `startExport` 不传这两个值 → Handler 的 CreateExportInput 筛选字段为空 → Service 保存三个 null → `rowMatchesExportFilter` 不限制分类。本轮为静态链路确认，未声称执行了真实 CSV 端到端测试。

修复要求：统一导出契约，在请求中传递当前 agent / provider / model，由服务端规范化和计算 hash；或实现服务端可验证的筛选引用。增加包含两个 Agent / 模型的测试，筛选一个后验证导出仅含选中范围。

## 3. 首次验证记录与证据边界

以下是首次审查已执行的检查，后续代码变化不会自动继承这些结果。

| 检查 | 结果 | 限制 |
| --- | --- | --- |
| server：`go test ./...` | 通过 | MySQL 环境相关用例被 skip，不代表 SQL / 事务集成通过 |
| server：团队 Service、Worker、memory、mysql 包定向测试 | 通过 | mysql 包同样存在环境跳过 |
| server：`go test ./internal/httpapi -run Team -timeout 45s` | 通过 | 现有团队 HTTP 用例未覆盖本报告中的全部真实响应契约 |
| web：`npm run typecheck` | 通过 | TypeScript 声明不能验证后端运行时 JSON |
| web：`npm test -- --reporter=dot` | 19 个文件、92 项测试通过 | 出现现有 Router / act 警告；手写 fixture 掩盖契约问题 |
| `git diff --check` | 通过 | 仅格式检查 |
| 三份 0005 迁移 SHA-256 比较 | 一致 | 不等同于实际迁移执行成功 |
| 临时 Go overlay 定向复现 | 7 项均失败，暴露对应缺陷 | 测试通过 overlay 注入，未修改工作区业务代码或正式测试 |

临时复现与问题映射：

| 临时测试名称 | 对应问题 | 观察结果 |
| --- | --- | --- |
| TestCRRevokedBaseMustNotReappear | CR-001 | 已撤回的 123 Token 仍被聚合 |
| TestCRSnapshotDateRoundTripShanghai | CR-002 | 请求 9 月 6 日，转换后读取前一天 |
| TestCRActualMembersPayloadMatchesWeb | CR-003 | 200 响应缺少 membershipId / sharing / syncStatus / canOpenDetail |
| TestCRActualEmailPreviewMatchesWeb | CR-004 | 200 响应没有 team / invitedRole |
| TestCRInviteLinkIdempotentRetryReturnsUsableToken | CR-005 | 第二次生成返回 201，返回链接预览 404 |
| TestCRAnalysisPreservesEstimatedCostAndCoverage | CR-007 | 已有 3.50 USD 估算被丢弃，覆盖分母为 0 |
| TestCRContributionSortIsNumeric | CR-008 | 9 排在 100 前面 |

HTTP 复现经过现有 Router、Handler、Service 与 Memory Store；没有证明 MySQL 路径已通过。CR-001 的输入模拟实际撤销查询投影；CR-002 组合了现有序列化和本地日期恢复逻辑，不应标记为数据库端到端测试。

本机临时证据目录：`C:/Users/Administrator/AppData/Local/Temp/tokendance-team-cr-7829d15febe2413ab91c7c606b5673e3`，含三个测试文件与 overlay.json。该目录不是仓库交付物，也不是本报告成立的前提；修复时应把相应用例落实到正式测试。报告中的复现输入、预期和实际结果保留作持续跟踪证据。

临时目录仍存在时，在 server 目录可按以下形式复跑：

```powershell
go test -overlay "C:/Users/Administrator/AppData/Local/Temp/tokendance-team-cr-7829d15febe2413ab91c7c606b5673e3/overlay.json" ./internal/worker ./internal/teams ./internal/httpapi -run '^TestCR' -count=1 -timeout 45s
```

**尚未完成的验证：** 配置专用 `TOKENDANCE_TEST_MYSQL_DSN` 后的迁移与并发测试、CR-006 / CR-009 的新增前端状态测试、CR-010 的真实外键删除测试，以及修复后的完整前后端联调。没有执行生产迁移、部署或向真实用户发送邀请。

## 4. 修复复验记录

每个问题修复后，将下面信息追加到对应条目，并同步顶部状态表：

```text
问题编号：CR-xxx
修复提交 / 工作区位置：
新增或修改的回归测试：
复验日期及命令：
实际结果（含 skip / 环境限制）：
状态：待复验 / 已关闭 / 待修复
```

2026-09-06 工作区修复复验（未提交）：

| 命令 | 结果 | 限制 |
| --- | --- | --- |
| `go test ./internal/teams ./internal/worker ./internal/store/memory -count=1 -timeout 60s` | 通过 | Worker MySQL 用例无 DSN 时 skip |
| `go test ./internal/httpapi -run "TestTeam\|TestInvite\|TestCreateTeam\|TestCreateDisabled\|TestTeams\|TestEmailInvitation\|TestLeave" -timeout 45s` | 通过 | 含成员契约、邮箱预览/收件箱、邀请链接幂等重放 |
| `cd web && npm run typecheck` | 通过 | — |
| `cd web && npx vitest run --reporter=dot` | 19 文件、95 项通过 | 无浏览器联调；含导出筛选用例 |

CR-010 复验命令（配置 DSN 后）：

```powershell
go test ./internal/worker -run TestTeamCleanupKeepsSnapshotsReferencedByExportsMySQL -count=1 -timeout 60s
```

### 第二轮独立复验（2026-09-06）

HEAD 仍为 `728033f`，实现及修复仍未提交。本轮只修改本报告，隔离复现代码放在系统临时目录；没有修改业务代码或仓库测试。

| 命令 / 检查 | 实际结果 |
| --- | --- |
| `go test ./... -timeout 180s` | 通过，HTTP 包约 89 秒；MySQL 用例未配置 DSN 时 skip |
| `go test ./internal/teams ./internal/worker ./internal/store/memory ./internal/store/mysql ./internal/httpapi -run 'Team\|Invite\|Invitation\|Assemble\|SortKV\|GrantCovers\|BuildMember\|ResolveTeam\|FormatTeam' -count=1 -timeout 90s` | 通过 |
| 上轮实际 HTTP 复现：成员响应、邮箱预览、邀请链接幂等重放 | 3 项通过；邮箱用例按当前创建响应的 invitation 包装读取 ID 后重跑通过 |
| 上轮 Service 复现：费用组装、数值排序 | 2 项通过 |
| `npm run typecheck` / `npm test -- --reporter=dot` / `npm run build` | 均通过；19 文件、94 项测试。仍有 React act / Router 提示 |
| `TestTeamCleanupKeepsSnapshotsReferencedByExportsMySQL`（单独 -v 执行） | 明确 SKIP：TOKENDANCE_TEST_MYSQL_DSN not set |
| 三份 0005 迁移文件 SHA-256 | 一致 |
| `git diff --check` | 通过；该命令不覆盖未跟踪文件 |
| 本轮新增隔离复现 `TestCR2*` | **3 项失败，分别证实 CR-011 / CR-012 / CR-013**；编译成功后运行得到业务断言失败 |
| CR-014 导出筛选链 | 静态确认，尚未运行浏览器与真实导出 Worker 联调 |

临时证据目录：
`C:/Users/Administrator/AppData/Local/Temp/tokendance-team-cr2-4a266cb782db462ab2e0ad31cbef445c`

在 server 目录复现新增问题：

```powershell
go test -overlay "C:/Users/Administrator/AppData/Local/Temp/tokendance-team-cr2-4a266cb782db462ab2e0ad31cbef445c/overlay.json" ./internal/teams ./internal/worker -run '^TestCR2' -count=1 -timeout 45s
```

该目录的 `http-overlay.json` 可复验三个已修复 HTTP 问题：

```powershell
go test -overlay "C:/Users/Administrator/AppData/Local/Temp/tokendance-team-cr2-4a266cb782db462ab2e0ad31cbef445c/http-overlay.json" ./internal/httpapi -run '^TestCR' -count=1 -timeout 45s
```

注意：第 3 节保存的是首次审查结果。不能原样重跑旧日期模拟用例来判断 CR-002 是否修复；它内嵌旧转换逻辑。第二轮已检查新的 `FormatTeamCalendarDate` 调用及本地日期回归测试。真实 MySQL 撤回、日期范围、外键清理和完整邀请 / 授权 / 导出浏览器链路仍属于验证边界。

## 5. 更新记录

| 日期 | 更新 |
| --- | --- |
| 2026-09-06 | 完成首次代码审查，记录 CR-001–CR-010；建立持续维护报告，全部标记待修复。记录现有测试通过情况、7 项失败复现和 MySQL 验证缺口。 |
| 2026-09-06 | 工作区落地 CR-001–CR-010 修复。定向 Go / 全量 web 测试与 typecheck 通过。CR-001–CR-009 已关闭；CR-010 因真实 MySQL 外键删除链 skip，保持待复验。未 commit。 |
| 2026-09-06 | 第二轮独立 CR：新增 CR-011–CR-014（4 项 P2，待修复）；3 项隔离失败复现，1 项导出静态链路证据。Go 全量、web 94 项、类型检查及构建通过；原 9 项维持关闭，CR-010 仍待 MySQL 复验。只更新报告，未提交。 |
| 2026-09-06 | 工作区落地 CR-011–CR-014。`go test ./internal/teams ./internal/worker`、HTTP 团队定向测试、`npm run typecheck`、web 19 文件 95 项通过。四项关闭；CR-010 仍待 MySQL 复验。未 commit。 |


## 6. 合并 main 后的 CR 与功能验证（2026-09-12，修复前历史记录）

### 6.1 合并范围与处理

- 起点：`codex/team-init` / `ebde9473760ffa3de7a45bc536a1dfcfed999dfc`，开始时工作区干净。
- 拉取并合入：`origin/main` / `c7ba493b5cf5a33a891be0e387ed2b5e4f870675`。
- main 历史经过清理，直接使用旧共同祖先合并会重复引入大量已更新内容。以最新 main 文件树为基准，按本分支相对已合入的旧 main `5fb84e7427c8b5ca2ca8b0b7621c933d08eb78b4` 的 76 个文件增量三方合并，保留团队实现。
- 手动合并配置开关、内存存储字段与初始化、迁移测试。迁移总数为 13，包含团队 0005 和 main 的 0008–0013；保留历史 SQL 原始字节。
- 修复合并后的编译兼容问题：main 删除了 `MetricCard.hint`，团队概览和分析页仍依赖它。恢复可选提示参数，并在团队页面保留提示样式。
- 仅执行构建、测试和本地 Git 合并；本轮不部署应用，不向 GitHub 推送。

### 6.2 CR-015 · P1 · v2 上传后个人统计增长，团队统计保持空值

位置：`server/internal/worker/team_analysis.go:427`。相关：`server/internal/httpapi/router.go:120`、`server/internal/store/mysql/telemetry_ingest.go:265`、`server/internal/store/mysql/ingest.go` 的旧链路 source revision 更新。

main 将旧 `/v1/telemetry/*` 上传入口关闭，桌面端改走 `/v2/telemetry/events` 并写入 `telemetry_events`。团队 Worker 仍只从 `usage_events` 读取事实，v2 接收/聚合链路也未推进 `team_source_revisions`。因此旧数据可显示，但之后新增的用量不会进入团队快照、成员统计或 CSV；前端继续轮询无法补救。

复现用例 `TestTeamCR015V2UploadReachesTeamAnalysis`：同一活跃用户已入队并开启 base，提交合法 v2 事件、运行 v2 聚合和团队快照。个人 day 指标为 10 Token，团队为 0，团队 source revision 仍为 0。

修复要求：让团队事实读取与当前 v2 的去重、fact revision、可信指标及授权时间窗口语义一致，并在正确的事务边界推进团队数据版本。不能重新开放旧上传端点作为修复。新增上传、重放、事实修订、删除、不同共享维度的团队统计回归后关闭。

### 6.3 CR-016 · P1 · 开启共享之前的费用被同轮 usage 带入

位置：`server/internal/worker/team_analysis.go:493`–`497`，分组来源同文件 `535`、`584`。

`buildMemberAnalysisRows` 在逐事件授权之前调用 `groupTeamCostEvents`。分组只按 user / installation / agent / session / turn 关联，并把 usage 放到组首；费用处理只检查这个首条事件的授权和维度掩码，然后累加组内全部费用。

可达场景：用户已在团队中；10:00 记录同一轮对话的费用，10:01 开启 base 或 cost，10:02 产生该轮后续 usage。费用自己的发生时间早于共享起点，但新 usage 的可见权限被应用到旧费用上。复现用例使用无 `revoked_at`、正常开放的授权，base / cost 两种情形均返回 2.00000000 USD，预期该旧费用不可见。

修复要求：每条费用先独立通过加入时间和各维度授权判断，再按兼容的可见性、日期及分类归并；不得借用关联 usage 的共享权限。保留同组独立费用覆盖 usage 的已有正常行为，补开启、撤回、重新开启及跨日边界用例。

### 6.4 CR-017 · P2 · 清库遗漏团队表导致测试结果受前序用例影响

`server/internal/migrate/runner.go` 的 `ResetCleanSchema` 维护显式表列表，却没有团队 0005 迁移的 15 张表。先运行 Store 用例，再运行 Worker 清理用例时，前序新建团队的 8 条未过期命令收据继续留在库中，导致 `TestTeamCleanupExpiresInvitationsAndReceiptsMySQL` 错报残留。

已将这 15 张表加入清理列表，并增加独立测试验证“迁移后存在 15 张团队表 → reset 后为 0 → 再次迁移成功”。修复的是测试库清理基础设施，邀请清理的业务 SQL 未因此修改。最终结果以下方重跑为准，不把首次受污染的用例结果当成业务缺陷。

### 6.5 验证方式与结果

本地命令从 `server` / `web` 目录执行；云端通过 `go test -c` 生成的 Linux 测试二进制执行相同 Go 测试。临时数据库使用独立账号，仅授予该 schema 权限；服务端没有启动新应用进程。

| 验证项 | 结果 |
| --- | --- |
| `go test -json ./...`（本地，无 MySQL DSN） | 275 个测试用例通过（含子测试），84 个数据库用例跳过；不能替代下方云端测试。 |
| `go vet ./...` | 通过。 |
| `npm test` | 139 / 139 通过，包含 14 项团队页面用例。 |
| `npm run typecheck` | 通过。 |
| `npm run build` | 通过；仍有 Three.js 分块超过 500 kB 的构建提示。 |
| 云端 MySQL 迁移 | 最终 8 / 8 通过（包含 15 张团队表清库及重新迁移），明确排除 1 项要求精确 MySQL 8.0.34 的 DDL 恢复用例；原始失败仍保留。 |
| 云端 MySQL Store | 首轮全套 57 通过 / 3 失败；其中 1 项已在原始 main 复现，2 项因触发器权限失败，见 6.6。清库修正后 5 项团队流程全部通过。 |
| 云端 Worker | 清库修正后 31 / 31 通过；本次重跑明确排除 8 项要求 MySQL 8.0.34 的用例和 2 项 P1 验收探针，限制见 6.6。 |
| 新增所有权 / 退出 / 解散完整流程 | `TestMySQLTeams_OwnershipLeaveAndDissolveWorkflow` 通过；包含普通成员不能自提权、owner 不能直接退出、转移后旧 owner 可退出、解散后成员与邀请链接清理。 |
| P1 验收复现 | 两项均在云端复现失败：10 Token 个人 / 0 Token 团队；共享前的 2 USD 被带入。使用显式 `team_cr_probes` build tag，断言目标为正确行为；问题未修复。 |
| Gitleaks 8.30.1 staged scan | 通过，输出已脱敏。 |

团队页面回归覆盖：已有团队禁止创建、确认无团队后展示创建页、未登录邮箱邀请的登录入口、默认关闭共享、分享链接 token 的 fragment 清理与 15 分钟有效期、登录返回路径不带 token、分析等待态、权限版本变化清空旧统计、持续轮询与卸载停止、导出携带当前筛选条件，以及中英文文案。

真实数据库团队 Store 回归覆盖：重复建队 / 跨队接受邀请被拒绝、命令幂等重放、邀请链接次数耗尽、共享开关更新授权。上述 4 个用例均已通过。

复现命令（为避免清空共享库，DSN 必须指向可销毁的独立测试 schema）：

```powershell
# 无数据库即可复现费用共享边界问题
cd server
go test -tags team_cr_probes ./internal/worker -run TestTeamCR016 -count=1 -v

# 配置独立测试 schema 的 TOKENDANCE_TEST_MYSQL_DSN 后
go test -tags team_cr_probes ./internal/worker -run TestTeamCR015 -count=1 -v
go test ./internal/store/mysql -run TestMySQLTeams_OwnershipLeaveAndDissolveWorkflow -count=1 -v
```

原始测试输出保存在本地忽略目录 `build/team-cr-go-tests-final.jsonl`、`build/team-cr-web-tests.json`、`build/team-cr-run/`，不提交运行凭据。UI 测试使用组件交互与 HTTP Handler 测试；本轮没有对已部署环境进行浏览器手工验收，也没有验证真实 SMTP 发送或云对象存储上传。


### 6.6 主干既有失败与测试环境限制

- `TestUSR107_CompareHiddenMetricPrivacyMySQL` 在合并分支失败；从 `origin/main` 精确提交独立导出、编译并在同一隔离库执行，也出现相同 Token、agent breakdown、active days 断言失败。确认是 main 已存在的测试失败，本轮没有将其归因于团队改动。
- MySQL 8.0.46 被 9 个要求精确 8.0.34 的顶层用例拒绝：1 个迁移 DDL 恢复用例，以及 Worker 的 7 个删除强化用例和团队删除屏障用例。没有放宽这些版本断言来获得通过结果。
- `TestUSR021_MySQLAccountSuspensionAtomicallyHidesPublicProjection`、`TestUSR021_MySQLAccountDeletionAtomicallyHidesPublicProjection` 需要在开启 binlog 的实例上创建触发器；本轮临时账号没有实例级 SUPER，两个用例在建立故障注入条件时失败。未修改共享实例全局参数或扩大测试账号的实例级权限。
- 因上述原因，本轮不能宣称全部数据库回归通过。P1 问题修复后仍需在版本与故障注入权限均匹配的隔离环境补齐这些测试。


最终复验中，`TestTeamCleanupExpiresInvitationsAndReceiptsMySQL`、`TestTeamCleanupKeepsSnapshotsReferencedByExportsMySQL` 和 `TestTeamAnalysisClaimAuthDiscardAndSourceRefreshMySQL` 均通过。CR-010 关闭依据是实际外键链路执行；CR-017 修复后，过期邀请与 receipts 清理恢复通过。


本轮结论：合并兼容问题和测试清库遗漏已处理；CR 与功能测试已执行完成，但团队功能验收不通过，阻塞项为 CR-015、CR-016。8.0.34 / 触发器故障注入用例及 main 原有失败仍需后续处理，未标记为已通过。

最终原始记录：`build/team-cr-run/migrate-final.log`、`mysql-teams-fixed.log`、`worker-fixed.log`、`probes-fixed.log`、`baseline-main.log`。前几次尝试日志也保留；中途停止的 `migrate-fixed.log` 不作为最终迁移通过证据。

本轮云端临时 schema、专用数据库账号及测试二进制已清理；仅本地保留测试日志。


## 7. CR-015 / CR-016 修复与复验（2026-09-12）

### 7.1 修复范围

基于本地合并提交 `3b4d66bbaf964b2e4565ca56907d392ee83963c8`，修改留在 `codex/team-init` 工作区。本轮没有再次合并 main，也没有提交、推送或部署。

- **CR-015**：团队 Worker 改读 `telemetry_events`，连接新版模型维度，排除软删除事实；可信 Token 只使用 v2 `token_total`，不将 input/output 等已知分项推算为总量，使用大整数保留 uint64 精度。沿用现有 v2 的不可变事实接收与去重结果，不双读旧 `usage_events`，不新增原生修订替换能力。
- v2 接收事务仅在批次包含新接受事件时推进团队 `source_revision`，整批只推进一次；重复与内容冲突不推进。新事实与版本一起提交。快照发布时检查构建期间是否又有新上传，刷新任务携带已捕获的源版本，追平后停止重排。
- **CR-016**：每条费用独立检查入队时间、base 与 cost 授权；费用使用自身的实名/分类可见性和日期，不能借关联 usage 的授权。旧聚合辅助逻辑也先按授权过滤再分组，保留原 CR-013 正常覆盖率用例。
- 新版费用使用 `cost_scope_key` 和 1e8 单位：同一 scope 的最新 provider bill 替换该 scope 各模型估算；无 scope 的金额各自相加。先决定整个 scope 的有效账单再裁切查询日期，避免查询历史日时恢复已被替换的估算。覆盖率仅关联授权、日期及可见分类兼容的 usage。
- 统一 `domain.TeamAnalysisRuleVersion = "2"`。新请求按版本 2 构建；旧版 queued 快照被标记 obsolete；MySQL/Memory 的快照读取、导出读取以及 Worker 导出前校验拒绝旧版结果，已生成文件不能继续通过下载接口获取。

主要代码：`server/internal/worker/team_telemetry.go`、`team_analysis.go`、`team_exports.go`，`server/internal/store/mysql/telemetry_ingest.go`、`team_analysis.go`、`teams.go`，及 Memory 对应读取校验。

### 7.2 实际测试结果

数据库使用云端 MySQL 8.0.46 的新建临时 schema 和专用受限账号，未重置共享 `tokendance_dev`。Linux 测试二进制由当前工作区编译；这些是数据库与 Worker 功能测试，没有启动或部署功能分支应用。

| 验证项 | 结果 |
| --- | --- |
| 本地 `go test -json ./...` | 301 个用例通过（含子测试），86 个数据库用例因本地未设 DSN 跳过；29 个有测试包通过，10 个包无测试。随后增加构建中上传的数据库回归并定向重跑 Worker 通过，其实际执行见云端最终记录。 |
| 本地 `go vet ./...` | 通过。 |
| 前端 `npm test -- --run --reporter=json --outputFile=../build/team-cr-web-tests-p1.json` | 139 / 139 通过，无跳过。 |
| 前端 `npm run typecheck` | 通过。 |
| 云端 Worker 广泛回归 | 38 / 38 顶层用例通过，无隐式 skip；包含两个原 P1 复现、新版统计/费用/CSV/共享流程及已有聚合、清理、邮件和导出回归。明确排除的 8 个版本受限用例仍遵循第 6.6 节限制。 |
| 云端团队 Store 回归 | 5 / 5 通过：建队/接受邀请冲突、幂等、分享链接次数、共享开关、所有权/退出/解散。 |
| 云端最终团队 Worker 定向回归 | 补充不可变事实内容冲突、旧 queued 快照淘汰及构建中上传场景后，23 / 23 顶层用例通过，无 skip。最终测试二进制为 `worker-p1-final.test`。 |

关键回归：

- `TestTeamCR015V2UploadReachesTeamAnalysis`：同一次 v2 上传在个人与团队均为 **10 Token**；重复上传和同一事实内容冲突不增加 Token 或 source revision；旧源版本触发补建后队列归零；旧规则 queued 快照变为 obsolete。
- `TestTeamCR016CostBeforeSharingStartIsNotExposed`：base / cost 开启前费用均为 **0 USD**；原两项失败已进入默认测试集，移除 `team_cr_probes` build tag。
- `TestTeamTelemetrySharingCostExportMySQL`：授权前的 **999 USD 不可见**，已共享的 **2 USD 正式账单替换 1 USD 估算**；查询较早日期不会恢复估算；CSV 可生成、导出任务完成；旧规则快照与文件读取被拒绝；撤回费用共享后为 0，重新开启不会恢复旧费用；软删除 usage 不会重新计入新快照。
- `TestTeamTelemetryUploadDuringSnapshotBuildMySQL`：一致性读取结束后再上传一条事实，发布时发现新版本并补建，最终 **20 Token**，后续无无限重排。
- 单元回归覆盖 uint64 最大 Token、exact/derived、禁止用分项推导总量、无 scope 费用、跨模型正式账单替换、授权起止边界、撤回、入队边界、跨日和实名/分类掩码。
- `TestMemoryTeamsRejectOldAnalysisRules`：Memory 与 MySQL 一致拒绝旧版本快照及导出。

可复验命令（从 `server` 执行；数据库 DSN 仅允许指向可销毁的隔离 schema）：

```powershell
go test ./internal/worker -run 'TestTeamCR016|TestTeamTelemetry(Token|Cost)' -count=1
go test ./internal/worker -run 'TestTeamCR015|TestTeamTelemetry.*MySQL' -count=1
go test ./internal/store/mysql -run '^TestMySQLTeams_' -count=1
```

本地原始日志：`build/team-cr-go-tests-p1.jsonl`、`build/team-cr-web-tests-p1.json`、`build/team-cr-run/worker-p1.log`、`mysql-teams-p1.log`、`worker-teams-p1-final.log`。这些忽略目录不提交运行凭据。

### 7.3 当前结论与边界

CR-015、CR-016 均按真实云端复现和功能回归关闭；当前报告的 17 项团队问题全部关闭。第 6.6 节已确认的 main 既有失败、MySQL 8.0.34 精确版本断言和触发器权限限制仍保留，未宣称全部数据库测试已通过。没有进行浏览器手工验收、真实 SMTP 或云对象存储验证；本轮 CSV 生成与异步导出使用内存对象存储。

| 日期 | 更新 |
| --- | --- |
| 2026-09-12 | 完成 CR-015、CR-016 修复并关闭；新增 v2 团队统计、费用授权、版本失效、构建中上传的回归。更新当前结论，保留修复前失败与环境限制。修改留在工作区，未提交、推送或部署。 |

本轮收尾：Gitleaks 8.30.1 对本次变更和新增源码的脱敏扫描通过，`git diff --check` 通过；云端临时 schema、专用账号和测试二进制已清理，本地保留脱敏测试日志。


## 8. 测试服务发布（2026-09-12）

### 测试成员无数据核查（2026-09-12，修复前记录）

- **[P1] 测试数据镜像与团队统计数据源不一致。** 云端 `token-dance-grayscale-sync` 正常运行，16:03 CST 最近一次成功同步了 9 名符合筛选条件的成员（上限 10），截图中的四名成员均在名单中。镜像复制旧版 `daily_*`、设备汇总和排名表，团队 Worker 则只读取 v2 `telemetry_events`。四名成员在生产库和测试库的 v2 事件数均为 0；因此仅给镜像增加 v2 表复制仍无法恢复这些成员现有的历史团队统计。需要明确旧版汇总兼容方案及与 v2 的去重边界，不能将日汇总伪造成原始事件。
- **[P2] 镜像历史完整性缺少修复机制。** 四名成员在生产库的 `daily_user_agent_metrics` 行数分别为 72、42、67、124，测试库对应只有 1、3、1、2。当前同步从 9 月 11 日开始；`refreshSince` 只在未记录成员名单或名单变化时全量同步，通常只刷新最近两天，无法自动发现并补齐更早的缺失记录。历史缺失最初如何形成尚未确认，需要补齐及完整性验证，不能以服务运行成功代表数据完整。
- **日期边界：** 四名成员旧版日汇总最新日期均为 9 月 11 日，9 月 12 日“今天”的汇总为 0；但改选 7 天也不能绕过上述数据源不一致问题。
- 本轮仅进行了服务状态、日志和数据库只读核查；未修改生产/测试数据或部署代码。此前 v2 上传端到端测试通过不代表旧版生产数据镜像已覆盖。

### 历史团队数据修复与回归（2026-09-12）

- 接入独立的旧版日汇总读取路径：exact + derived Token，保留原始 UTC 日；同成员/工具/UTC 日出现 v2 用量事实时以 v2 为准，包括删除标记。共享掩码继续控制成员与工具披露，历史汇总不伪造事件、费用来源、模型或覆盖率。API、页面和 CSV 明示来源；规则版本升为 4，迁移 0014 增加来源标记及分区查询索引。
- 镜像每轮完整对账选中成员的日汇总，不再依赖“名单是否变化”补历史；只有输入变化才使团队缓存失效。修正列元数据将 `DEFAULT_GENERATED` 误判为不可写字段的问题，保持源时间并防止无变化同步制造更新时间。
- 成员页补齐日期选择、缺日期及请求失败状态。总览/用量分析/成员页的日期和来源说明回归，加上双语校验，共 14 项前端测试通过；TypeScript 检查通过。
- 独立云端 MySQL schema：团队 Worker 22 项顶层用例通过、无跳过；新增旧汇总 Token、大整数、历史授权、撤回、分类掩码、新旧重叠、删除不复活、行键合并、来源持久化与 CSV 回归。镜像完整回填、源端删除、连续同步缓存稳定及本地邮箱绑定保留，1 项集成用例通过、无跳过。
- 旧测试中规则版本 2 的硬编码、授权前历史费用排除/重新共享后仍排除的断言与用户后续要求冲突，改为当前规则常量和当前共享覆盖历史的期望；撤回共享仍不可见。此前历史测试结果保留，不再用旧口径作为当前验收依据。
- 发布后的真实成员数据核对与部署 SHA 另记测试服务发布记录；此处不宣称所有项目数据库测试通过。

### 2026-09-12 后续口径与性能修正

后续截图复现发现独立的前端问题：`?range=custom` 缺少起止日期时，Hook 不发送请求，但页面把“无结果”当成加载中，又在提前返回时隐藏日期选择器，形成永久骨架屏。修复为显式等待日期状态，所有加载/错误状态保留日期选择器。新增总览和分析页的缺日期深链接、补齐日期、清空日期及切回预设范围回归。此前 30 天接口耗时样本不能代表该路径；该路径原本没有任何统计请求。

用户明确取消加入时间及共享开始时间限制：规则版本 3 使用当前成员身份和当前有效共享维度，覆盖所选日期内的历史记录；关闭授权、退出团队、删除屏障仍然阻止披露。此前 CR-016 关于授权开始前数据排除的结论由本次产品要求取代，费用按自身日期归属和撤销授权保护继续保留。

加载延迟来源为两秒串行 Worker 调度、两秒页面轮询，以及 30 秒后无条件重算。改为独立 500ms 团队任务调度及轮询，按数据版本复用未过期结果，有新数据时保留同授权版本结果并后台刷新。授权版本变化仍须立即隐藏旧结果。

新增历史用量、费用、撤销授权与退出成员回归；新增缓存超过 30 秒复用、数据变化后台刷新、任务去重、授权变化及过期失效回归。发布耗时验证与数据库测试边界记录在测试发布文档。

按用户最新要求，测试环境改为独立 `release` 分支。CR-015 / CR-016 修复已包含在 `5f8cdef2c27d047f2695927fb23aec6142952703` 并发布到 [测试服务](https://nexorai.com.cn/token-dance-test/)。完成测试库备份、13 个迁移、实际浏览器登录、临时团队创建/成员/概览/异步分析就绪及解散清理、OSS 读写检查。完整分支、SHA、备份位置及测试边界见 [发布记录](test-service-release-2026-09-12.md)。第 7 节的未提交/未部署描述保留为发布前历史状态。

本次历史数据问题已在测试服务验证关闭：四名成员的 7 天统计与源库一致；测试发布分支、完整 SHA、备份及耗时样本见测试服务发布记录。
