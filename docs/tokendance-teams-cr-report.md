# TokenDance 团队模块 CR 报告

首次审查：2026-09-06。最近更新：2026-09-06。

工作区：`C:/Users/Administrator/orca/workspaces/TokenDance/codex-team-init`。分支：`codex/team-init`。审查时 HEAD：`728033f`。

**审查对象是该 HEAD 之上的未提交实现，包括新增文件；不是仅审查 728033f 提交。** CR-001–CR-010 的原问题详情及第 3 节保留首次审查证据；CR-011–CR-014 和第二轮复验记录对应本轮工作区。

依据：[产品与交互方案](tokendance-teams-product-design-v1.md)、[技术方案](tokendance-teams-technical-design-v1.md)。

## 1. 结论与跟踪规则

累计记录 **14 项问题：6 项 P1、8 项 P2**。2026-09-06 第二轮新增的 CR-011–CR-014 已在工作区落地修复并关闭。CR-001–CR-009 维持已关闭；**CR-010 仍待复验**（真实 MySQL 外键删除链未执行）。

**MySQL 集成用例因未配置 `TOKENDANCE_TEST_MYSQL_DSN` 跳过，不能据此宣称撤回、时间范围或外键清理的真实数据库链路已验证。** 原问题详情保留首次审查证据，当前状态以表格及后续复验记录为准。

本报告作为本工作区团队模块 CR 的持续跟踪入口：

- 状态使用「待修复 → 待复验 → 已关闭」；复验失败退回「待修复」。
- 修复后填写实际提交或工作区修改位置，以及新增回归测试；不能只凭修改说明关闭问题。
- 关闭时记录复验命令、结果与日期。需要 MySQL 的检查被 skip 时，仍保持待复验。
- 后续问题继续编号 CR-011、CR-012；不重排已有编号。新增结论与状态变化写入末尾更新记录。

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
| CR-010 | P2 | 快照清理被导出外键阻断 | 迁移 FK 与清理 SQL 静态检查 | 待复验 | 工作区：`team_cleanup.go` 先清导出再删快照，DELETE 排除全部仍被 `team_export_jobs` 引用的 snapshot。回归：`TestTeamCleanupKeepsSnapshotsReferencedByExportsMySQL`（本机无 DSN，skip）。 |
| CR-011 | P2 | 分析分类、贡献榜及筛选选项缺少展示字段 | 实际响应组装函数复现 + 前端消费检查 | 已关闭 | 工作区：`pageBuckets` / `pageContributions` / `setToOptions`。回归：`TestAssembleAnalysisCollectionsMatchWeb`。 |
| CR-012 | P2 | 成员详情仍丢弃费用与分类统计 | 实际 GetMember 调用复现 | 已关闭 | 工作区：`assembleMemberDetail`。回归：`TestAssembleMemberDetailPreservesCosts`。未共享维度返回 unavailable，不返回空费用冒充 available。 |
| CR-013 | P2 | 独立费用无法覆盖同组无金额的 usage | 实际 Worker 聚合函数复现 | 已关闭 | 工作区：`groupTeamCostEvents` 按 session/turn 保留无金额 usage。回归：`TestCostOnlyRecordCoversAssociatedUsage`。 |
| CR-014 | P2 | 从筛选后的分析页导出时丢失筛选条件 | 前端请求 → Handler → 任务 → Worker 静态链路检查 | 已关闭 | 工作区：分析页传 agent/provider/model；分析响应补 `filtersHash`。回归：`teams-analysis.test.tsx` 筛选后导出。未跑真实 CSV Worker 联调。 |

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
