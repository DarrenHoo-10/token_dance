# 采集与上传数据模型调研

日期：2026-09-11。基线：`fe49506634af792cfe1ef668792bf174a562dbdf`，**包含当前工作区尚未提交的数字 ID、schema v7 和游标回绕修改**。

后续架构决定：用户已明确要求并发 harness 采集、内部事件持久化与异步事件上传。目标方案见 [事件链路设计 v2](D:/ProgrammingProjects/TokenDance/docs/event-pipeline-design-v2.md)；本文保留调研发现，其中继续以日快照作为长期上传主链路的建议已被替代。

结论：问题核心是身份、读取位置、解析状态、聚合版本、云端确认被混在一起。单纯增加插件并发只能缓解部分延迟，无法解决吞数据、重建丢历史和假同步。现有“本地采集独立于登录、事务提交、云端日快照覆盖”可以保留，优先修正状态与协议契约。

本轮只调研，新增本文和合成复现脚本，没有修改业务实现、操作用户数据或部署。性能结论来自代码路径与复杂度分析，尚未测量真实用户库的耗时、RSS、磁盘吞吐。

## 1. 对已有结论的核对与修正

| 已有结论 | 核对结果 |
| --- | --- |
| 数字 rowid 被当字符串读取，引发批内序号碰撞 | 当前工作区已修复类型转换；两个适配器共 13 个契约测试通过。但不能据此认定 v7 的恢复流程安全。 |
| 5 秒采集、10 秒上传，采集不依赖登录 | 成立。这是调度周期，不是完成时限；上传还会请求 session 和 cursor，且读取 daemon 状态要等待 service 锁。 |
| 每日一份整机快照，revision 与精确 ACK | 成立，但本地另外存在绕过精确 ACK 的 `ack_aggregates_through`，这条路径破坏了协议保证。 |
| 所有重建 job 完成前都不上传 | `pending_aggregate` 确实阻止发包；**批量 ACK 在这个判断之前执行**，因此全局锁不能阻止历史日被提前确认。 |
| 每拍热文件 8、历史文件 4、每文件 32 行 | 需要限定：SQLite 不占热文件名额，也不受 32 frame 限制。JSONL 的 32 是原始 frame 数，不是解码事件数；并且读取字节数没有被这个限制约束。 |
| 同文件/SQLite 行串行，文件和插件可并行 | 同一有状态流需要保持处理与提交顺序，多个独立文件可并行；SQLite 并不是物理上只能逐 rowid 解码，而是需要一致读取边界、正确处理更新和有序推进 checkpoint。 |

关键入口：[采集循环](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/daemon/mod.rs:34)、[上传循环](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/commands/account.rs:281)、[工作队列](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/rebuild.rs:99)。

## 2. 一致性问题，按优先级排列

### P0：旧事件时间水位不能确认日快照

服务端 `GetIngestCursor` 查询的是 `usage_events` 的最大事件时间，不是 `device_daily_aggregates` 的 day/revision/hash，也没有证明更早日期完整连续。本地却将所有 `day <= through` 的 `acked_revision` 直接设为当前 `revision`。

具体触发：云端已有昨天的旧事件；本地今天补采昨天漏掉的 100 token，日账从 revision 4 变成 5。下一次同步先取 cursor，再把 revision 5 当成已确认，**无需上传这 100 token**。更早的一个从未发送过的日期也会被一起确认。重建完成后同样会发生，等待全局重建结束并不能解决。

依据：[cursor 来源](D:/ProgrammingProjects/TokenDance/server/internal/store/mysql/aggregates.go:16)、[ACK 调用顺序](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/commands/account.rs:313)、[批量更新](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/retention.rs:418)。合成 SQLite 已复现未发送版本和未发送日期被确认。

建议：停止用旧事件 cursor 修改日快照 ACK。恢复同步时交换真正的快照清单及内容摘要；只确认服务器持久化过的那个对象。迁移旧事件需要单独的覆盖核验，不能靠最大日期推断全量历史已存在。

### P0：v7 清空长期日账，却没有足够事实恢复它

v7 删除**所有插件、所有账户的** `aggregate_days`、`aggregate_pricing`、`aggregate_activity` 和 `daily_*`，再从 `events` 重建。但事件明细只保留最近约七个 UTC 日，日账才是更早历史的长期存储。

因此，老日期的离线未上传用量可能直接丢失。其他插件的旧明细即使仍在 Agent raw，普通重放也不一定救得回来：永久 `event_fingerprints` 仍在，`apply_event` 对“已有指纹但没有 events 行”的情况直接跳过。v7 只强制回绕 ZCode/OpenCode，其他插件也不会因此自动全量重扫。

还存在两个独立问题：

- 日账重建后的 revision 从默认值重新开始，没有与服务端旧 revision 对齐；可能得到 `AGGREGATE_STALE_REVISION` 或同版本不同内容冲突。当前统一延后 1 小时重试，并不能解决冲突。
- `rescan_markers` 在驱动内存回绕后立即删除，并未等新 checkpoint 持久化。若此时退出、崩溃或首次提交失败，重启仍可能从旧 WAL cursor 恢复。修复意图的生命周期短于修复结果的生命周期。

依据：[v7 迁移](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/mod.rs:672)、[明细裁剪](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/retention.rs:448)、[指纹跳过](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/mod.rs:741)、[提前清 marker](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/state.rs:380)、[服务端版本冲突](D:/ProgrammingProjects/TokenDance/server/internal/store/mysql/aggregates.go:88)。合成 SQLite 已复现“清账后无明细恢复，且同 ID 重放被指纹拒绝”的核心条件。

建议：当前 v7 需要先补迁移保障。保留旧账作为有效版本，在影子 generation 中按来源重建；覆盖核验通过后原子切换。无 raw、无明细的历史必须保留并标记覆盖不足。发布版本不得随本地重建归零；重建 marker 与新的持久化 checkpoint 同事务完成。

### P1：用事件数推断 EOF，会使未读完的文件退出调度

`decode_tick` 使用 `batch.events.len() < 32` 判定 `caught_up`。但元信息、不支持的记录和普通上下文行都可能产出 0 个事件，一个 frame 也可能产出多个事件。

例如：100 行静态历史文件，第一批 32 行是上下文，产出 0 个事件，仍有 68 行未读，却被标成 `caught_up`。如果该来源的其他文件也已完成，job 变成 completed，不再进入 `select_scan_work`；文件 mtime/长度没变，后续 discovery 不会重排它。如果 job 尚有其他待办，它也可能偶然再次被热文件名额选中，因此不是每次必然永久漏采，但完成判断本身确定错误。

依据：[错误完成条件](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/rebuild.rs:77)、[仅选择未完成 job](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/rebuild.rs:112)。复现脚本对该条件做了状态模型验证，未运行完整 Rust 解码链。

建议：driver 显式返回 `has_more`、完整记录消费边界和本次读取目标边界；EOF、半行等待、预算耗尽、解析失败必须区分。事件数只用于统计。

### P1：SQLite 身份修好了，增量位置仍不可靠

有三种不同问题：

1. **可变记录不能仅靠 rowid 过滤。** ZCode 查询 `rowid > cursor AND status='completed'`。row 1 尚未完成、row 2 已完成时，cursor 推进到 2；row 1 后来完成就永远不满足过滤条件。已用原始 SQL 和合成 SQLite 复现。真实发生频率取决于 Agent 的写入顺序。
2. **查询向量不能哈希成可比较偏移。** `1:2:1700000000000` 等多查询 cursor 被 wrapping hash 成 `u64`，再转换成 SQLite `i64`。本地 checkpoint UPSERT 却要求偏移单调增加。原始 UPSERT 已复现拒绝更晚的 `1:3:1700000000000`，后续从旧 checkpoint 恢复就会反复扫描。
3. **时间戳也不是完整游标。** 代码变更查询用严格 `time_updated > cursor`，没有持久化 `(time_updated, native_id)` 边界。同时间戳的新提交或更新存在遗漏窗口。复合游标适合确定性分页，但要覆盖迟到提交仍需重叠窗口/变更序列等机制，不能只加一个 tie-breaker 就宣称解决。

此外，SQLite 将 hash offset 同时放入 `file_len`，重建进度又把它保存成文件长度；下次发现时拿真实数据库字节数比较，通常再次判定有变。这会把完整性错误转化为反复全库快照的性能成本。后续修正该混用时，还要补 WAL 感知，不能只依赖主 DB 的 mtime/长度。

依据：[查询计划](D:/ProgrammingProjects/TokenDance/collector/crates/acquisition/src/drivers.rs:515)、[hash 与伪长度](D:/ProgrammingProjects/TokenDance/collector/apps/service/src/lib.rs:726)、[偏移比较](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/mod.rs:1060)、[文件变动检测](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/rebuild.rs:60)。

建议：分离 native record ID、内容版本和 typed cursor。源表有自己的 `id`，应评估保留原生逻辑 ID，rowid 可以是扫描位置但不应无条件当作跨数据库重建的永久身份。对未完成记录持久化待复查集合，或使用可靠变更游标；对同 ID 内容更新采用可修正事实，而非一律去重丢弃。

### P1：解码失败仍推进 checkpoint，SQLite 一条坏记录可能吞整批

JSONL 和 SQLite 的 `collector.decode` 返回错误时均执行 `Err(_) => {}`，随后依旧返回读取后的 checkpoint。SQLite 所有查询结果装在一个 frame 中，适配器的逐 record 解码又使用 `?` 传播错误：一条错误记录可以让整个 frame 的已解码结果都丢掉，但外层仍提交新 cursor。

SQLite driver 在后续查询失败前，还可能已经修改前面查询的内存 cursor；只有存在可恢复的持久 checkpoint 时，下一拍才有机会回绕。这意味着“本地 SQLite 事务原子”不足以保证整个采集状态原子。

依据：[JSONL 吞错](D:/ProgrammingProjects/TokenDance/collector/apps/service/src/lib.rs:610)、[SQLite 吞错](D:/ProgrammingProjects/TokenDance/collector/apps/service/src/lib.rs:719)、[整批错误传播](D:/ProgrammingProjects/TokenDance/collector/adapters/opencode/src/lib.rs:165)、[提前改内存 cursor](D:/ProgrammingProjects/TokenDance/collector/crates/acquisition/src/drivers.rs:633)。本项为代码路径确认，尚未做故障注入端到端测试。

建议：返回逐记录 accepted/ignored/rejected/retryable 结果。允许跳过的拒绝必须有持久诊断和安全的重放定位；临时失败不能确认相应读取边界。driver 的 next cursor 应是候选结果，由 writer 成功提交后才生效。

### P1：事实更正、解析上下文与多个聚合副本缺少统一契约

同一事件 ID 的新内容目前基本不能更正旧 token 数。特殊的 `maybe_enrich_unknown_model` 只更新 events JSON，不同步重算原有 daily_model 和 aggregate_days；而 `retention::record` 只对新插入事件执行。事实、界面和上传账可以得到不同的模型归属。

此外，Codex 的 session/model/skill 解析上下文保存在内存，按文件游标 key 管理且超过 2048 项会整体清空；恢复文件偏移不等于恢复解析状态。并发化之前必须定义上下文的提交、恢复和淘汰规则。

依据：[只更新事件 JSON](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/mod.rs:810)、[只聚合新插入事件](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/mod.rs:321)、[Codex 上下文](D:/ProgrammingProjects/TokenDance/collector/adapters/codex/src/lib.rs:194)。本项未测量用户实际受影响比例。

## 3. 慢的具体来源

| 热路径 | 当前工作量 | 应调整的边界 |
| --- | --- | --- |
| JSONL 读取 | `read_to_end` 读取剩余全部字节，再只消费最多 32 frames；长文件分批扫描时反复读后缀。 | BufRead/流式读取，同时限定 records、bytes 和时间预算，正确处理跨块与半行。 |
| SQLite 读取 | 每次 `open_snapshot` backup 整库到内存；增量 SQL 无分页，所有查询结果序列化成单个 JSON 再解码。 | 有界分页；短生命周期的一致只读事务或分块 snapshot；选择方案前测量对 Agent WAL/checkpoint 的影响。 |
| 本地聚合 | 每个事件反序列化/重写整日 JSON、价格账和活动账；价格刷新还可能再次改 revision。 | 每批按 owner/day/维度合并变更；SQL 增量投影；日快照在发布时合并序列化。 |
| 调度与锁 | 每拍枚举所有路径并写 discovery；全局 service 锁包住全部串行解码，daemon/UI/上传的状态读取共享此锁。 | 增量发现配合周期兜底；来源间公平调度；独立解析 worker；单写入队列；UI 读状态快照。 |
| 上传 | 每 10 秒只选一个最早日期，先检查 session/cursor，然后发整日快照。 | 精确 ACK 后按有界预算连续排空/批量同步；活跃日保留名额；网络重试与事实版本分开。 |

依据：[JSONL 全后缀读取](D:/ProgrammingProjects/TokenDance/collector/crates/acquisition/src/jsonl.rs:143)、[SQLite 整库 backup](D:/ProgrammingProjects/TokenDance/collector/crates/acquisition/src/drivers.rs:704)、[逐事件日账更新](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/local_store/retention.rs:174)、[持有 service 锁](D:/ProgrammingProjects/TokenDance/collector/apps/desktop/src-tauri/src/daemon/mod.rs:63)。

量级示例：一个持续进入调度、每拍消费 32 frames 的文件，正常不积压时名义消费率仅 6.4 frames/秒。32,000 条等长记录需要 1,000 批；按当前重复读取后缀的方式，累计读取约等于整文件的 500.5 倍。这是代码复杂度推导，不是实机跑分，且实际可能先触发错误的 caught_up。

90 个待传日期在每拍一份、每拍约 10 秒且没有失败的理想情况下，仅轮询节拍就约 15 分钟。将网络并发打开之前必须先修掉日期水位 ACK，否则乱序发送会让旧协议的错误假设更加危险。

## 4. 建议的数据模型与处理链

```text
增量发现 / 定期核对
        ↓
按来源与文件公平调度，固定读取边界
        ↓
有界读取与解码 worker（跨独立流并行，同一流保持顺序）
        ↓
单 writer 事务：事实/更正 + 解析状态 + typed checkpoint + 覆盖进度 + dirty day
        ↓
按 dirty day 物化可发布版本 → 不可变日快照 outbox
        ↓
上传、精确 ACK；云端事务写快照 + 标记统计投影待刷新
```

最小需要明确以下实体，不建议继续把 JSON payload、各种 cursor 和布尔 rebuild flag 当作相互可替代的状态：

| 实体 | 关键身份/字段 | 职责 |
| --- | --- | --- |
| source_instance | adapter/source、物理来源 incarnation、类型 | 区分数据库替换、文件轮转以及同一插件多个来源。 |
| source_checkpoint | typed cursor、读目标边界、decoder state/version、commit sequence | 表示已可靠处理到哪里；不存不可比较的 cursor hash 来充当 offset。 |
| fact/contribution | source instance、native record ID、fact kind、content version/hash、owner、day | 明确区分重复、修正、撤销；session 作为关联维度，不要求原始文件按会话组织。 |
| daily projection | owner/day/agent/model/skill、projection version、可长期保留的贡献基底 | 用于展示与生成快照，历史压缩后仍可保留基数并应用更正。 |
| rebuild generation / coverage | source、枚举边界、处理边界、缺失/失败、候选 generation | 重建是版本切换流程，不能一边清有效账一边上传半成品。 |
| snapshot outbox | sync target/installation/day、单调发布版本、不可变 payload/hash、retry、ACK | 采集继续修改事实时，正在发送的对象内容不变；旧 ACK 只确认旧对象。 |

事实明细不一定必须永久保存，但必须有明确的修正窗口和长期贡献基底。仅保留不可逆指纹、同时删除唯一日账，不可能保证历史可恢复。

保留日快照覆盖是合理的，尤其服务器已经支持低成本幂等替换。不建议直接切回逐事件上传。第一阶段保持现有 `(installation, day)` 协议，先解决覆盖与精确确认；只有后续确认整机日账隔离需求明显，再考虑增加 `(installation, source/agent, day)` 分区。分区协议还需要分区清单、移除/清零语义及迁移规则，不能只把 rows 拆包。

当前日也不能简单绕过重建锁：如果服务端已有完整日账，发送本地半份重建结果仍可能覆盖掉另一半。应在有效 generation 上继续采集和发布，影子重建完成后切换；没有旧基底时按可证明的覆盖发布。历史日允许修正，不能因为日期已过去就永久封账。

并发建议：先试固定 2–4 个读取/解码 worker，按来源公平分配预算，writer 单线程。这个数只是压测起点。处理单元是拥有独立解析状态的流，而不是简单一个插件一个线程。某些适配器有全局上下文锁，需要相应拆分后才能获得文件级并行收益。

## 5. 实施顺序与验收标准

1. **先止损。** 修日期水位假 ACK、v7 历史保留与发布版本、迁移 marker 生命周期、JSONL 完成判断、SQLite typed cursor/迟到完成、解码错误确认。继续保留必要的发布覆盖保护。
2. **再统一事实与投影。** 建立来源身份和更正语义；把 checkpoint、解析上下文和事实放入同一提交协议；按批生成 dirty day，影子重建原子切换。
3. **最后提吞吐。** JSONL 有界读取、SQLite 分页、批量聚合、来源公平调度与有界 worker、不可变 outbox 连续排空；用阶段耗时和队列等待数据决定下一步。

必须新增的验收场景：

- 前 32 条无事件但后面有用量的静态 JSONL，最终全部采到；半行不会错误完成或跳过。
- SQLite 小 rowid 晚完成、同时间戳迟到记录、同 ID 内容更正、数据库替换。
- cursor hash 非单调的回归案例改为 typed cursor 后正常持久化；任一查询/解码/提交失败不越过未确认边界。
- v6 → 新 schema：已有 90 天历史且明细已裁剪；包括未上传历史、其他插件、其他账户；修复不可毁掉已有账。
- 清 marker 前后、提交前后、上传成功但 ACK 未落盘等故障点重启。
- 服务器已有旧事件最大日期，本地补历史仍会发送；同一天发送中继续增加 revision，旧 ACK 不吞新数据。
- 重建不生成半份覆盖，切换后能向上/向下修正旧统计；缺失 raw 有显式覆盖状态。
- 性能记录 discovery/read/decode/commit/materialize/upload 的 p50/p95、原始 bytes/frames、实际新增 facts、去重数、失败数、backlog age、重扫量和内存峰值。

## 6. 本轮验证边界

- `cargo test -p adapter-zcode -p adapter-opencode --test contract`：13/13 通过。
- `cargo test --manifest-path collector/apps/desktop/src-tauri/Cargo.toml local_store:: -- --test-threads=1`：21/21 通过。
- [合成复现脚本](D:/ProgrammingProjects/TokenDance/docs/collection-sync-research-2026-09-11.py)：5 项通过；前三项使用当前仓库 SQL，第四项验证迁移删除与指纹拒绝条件，第五项是完成谓词模型。不是完整桌面端到服务器端到端测试。
- 没有运行真实 Agent 库压测、完整迁移故障注入或线上服务验证。现有测试通过说明已有局部保证仍在，并不能否定上述未覆盖的状态组合。
