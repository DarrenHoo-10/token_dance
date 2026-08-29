# TokenDance 用户系统验收测试映射

> 运行命令：`cd server; go test -count=1 -p 1 -v -run '^TestUSR' ./internal/...`
>
> MySQL 验收设置：`TOKENDANCE_TEST_MYSQL_DSN` 指向 MySQL 8.0.34。测试输出保存为目标 scratch 下的 `acceptance-usr.log`，并合并进入 `tests.log`。

| 验收 ID | 真实测试入口 | 层级 | 关键断言 |
| --- | --- | --- | --- |
| USR-001 | `TestUSR001_RequestCodeWithoutUserCreation` | HTTP + Repository | 验证码申请只创建 challenge/outbox，不创建用户或凭据 |
| USR-002 | `TestUSR002_AtomicRegistrationAndFaultInjection` | HTTP + 事务故障注入 | 注册任一步失败全部回滚，challenge 保持可用 |
| USR-003 | `TestUSR003_ConcurrentEmailRegistrationUniqueness` | HTTP 并发 | 同邮箱 20 个并发请求仅一个成功，无 500 |
| USR-004 | `TestUSR004_CodeReplayPrevention` | HTTP | consumed challenge 不创建第二用户或 Session |
| USR-005 | `TestUSR005_Argon2idAntiEnumeration` | 真实 HTTP 登录入口 | 错误密码和未知邮箱各 1,000 次；状态、错误结构一致并比较 P50/P95 延迟分布 |
| USR-006 | `TestUSR006_SessionImmediateRevocationAndInvariants` | HTTP | revoke-others 提交后旧 Cookie 立即 401 |
| USR-007 | `TestUSR007_SafeReturnToFuzzingAndEncodedAttacks` | Auth service | 拒绝绝对 URL、双斜线、编码反斜线和认证循环，保留安全相对路径 |
| USR-008 | `TestUSR008_MandatoryOnboardingRouteGate` | HTTP | onboarding 前仅允许 Session/onboarding/logout，完成后恢复受保护页面 |
| USR-009 | `TestUSR009_ConcurrentHandleUniqueness` | HTTP 并发 | 两用户并发声明 Handle，仅一个成功 |
| USR-010 | `TestUSR010_LocaleAndMessageKeyConsistency` | HTTP | locale 只改变 messageKey/UI 文案，不改变指标数据 |
| USR-011 | `TestUSR011_TenMetricsMySQLSupportedVsZero` | MySQL production Repository | 十项公式、aggregation version、unsupported/null/真实零值 |
| USR-012 | `TestUSR012_TokenTrendFiltersAndBreakdownsMySQL` | MySQL production Repository | Agent/provider/model 组合过滤、趋势总和、构成与筛选项 |
| USR-013 | `TestUSR013_MessageCountingTriggerCriteriaMySQL` | MySQL canonical aggregation | turn 去重、user/system trigger、模型请求排除、总消息与用户消息公式 |
| USR-014 | `TestUSR014_PersonalSkillRankingAcrossDaysAndAgentsMySQL` | MySQL production Repository | Skill 跨日/Agent 聚合、活跃天数、私有 Skill 掩码 |
| USR-015 | `TestUSR015_ImmediatePrivacyClosureMySQL` | MySQL public query | 隐私关闭后旧快照立即过滤，资料更新使用当前投影 |
| USR-016 | `TestUSR016_PublicDTOWhitelistMySQL` | MySQL + HTTP public DTO | 公开字段白名单，不含 user ID、邮箱、时区、locale、密码 |
| USR-017 | `TestUSR017_OneTimeDeviceBindingConcurrencyAndIdempotencyMySQL` | MySQL 并发 | 同绑定码 20 个同公钥并发重试只生成一个 installation；不同公钥失败 |
| USR-018 | `TestUSR018_DeviceRevocationRejectsIngestMySQL` | MySQL row-lock boundary | revoke 后 ingest 授权返回 DEVICE_REVOKED |
| USR-019 | `TestUSR019_ImmutablePublishedLeaderboardSnapshotsMySQL` | MySQL Worker | 新建 immutable revision，旧 snapshot 分页不变，scope 和全部指标正确 |
| USR-020 | `TestUSR020_RedisUnavailablePreservesCorrectness` | HTTP + Redis 故障降级 | Redis 地址不可达时注册、Session、onboarding、隐私、公开查询和任务创建仍正确 |
| USR-021 | `TestUSR021_PublicProfileProjectionAndSameTransactionHidden` | HTTP + Repository | projection version 单调，关闭路径同步 hidden，无私有字段 |
| USR-022 | `TestUSR022_AvatarValidationAndPixelBombProtection` | HTTP + Media service | MIME、大小、像素、owner、ready 状态验证 |
| USR-023 | `TestUSR023_DevicePauseResumeRevokeLifecycle` | Device service | active→disabled→active→revoked 状态机与恢复限制 |
| USR-101 | `TestUSR101_ExportIdempotencyAndKeyRotationMySQL` | MySQL production Repository | 同 key/body 返回原任务，异 body 冲突，密钥轮换后仍幂等 |
| USR-102 | `TestUSR102_ExportAuthorizationAndSignedURL` | Export service + ObjectStorage | 跨用户 404、完成前拒绝、完成后生成 60 秒签名 URL |
| USR-103 | `TestUSR103_DeletionCancellationRaceMySQL` | MySQL Worker 并发 | grace period 内取消不能被 Worker 重新 claim |
| USR-104 | `TestUSR104_DeletionTombstoneAndPIIScrubbingMySQL` | MySQL Worker | 四种 scope、PII/凭据/Session/事件/对象清理和安全 tombstone |
| USR-105 | `TestUSR105_WorkerCrashTakeoverAndFencingMySQL` | MySQL 双 Worker | SKIP LOCKED、lease takeover、过期 Worker completion 被 fencing 拒绝 |
| USR-106 | `TestUSR106_PublicSkillMinimumSampleMySQL` | MySQL Search Repository | 至少 5 个公开用户和 3 个活跃日；隐私关闭后阈值立即重算 |
| USR-107 | `TestUSR107_CompareHiddenMetricPrivacyMySQL` | MySQL + HTTP compare | 按用户和指标独立权限；隐藏值不返回可差分推断数据 |
