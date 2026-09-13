# 全工具采集缺口修复

核对日期：2026-09-13；客户端实现位于 `codex/fix-dashboard-metrics`，尚未发布桌面安装包。服务端修订替换在独立 release 分支验证并发布测试服务；正式服务未部署本次改动。最终测试服务构建：`release` / `5c181da05c7d478cf2d5a5af1483d9cb37d1dfd7`，干净工作区；API、worker、HTTPS readiness 与静态资源路径验证通过。模块现状见 [采集事实](../../facts/collection.md)，事件字段沿用 [统计计量契约](../../metric-contract-v1.md)，未新增线上表或上传协议字段。

## 根因与修复

新事件管线原先只识别部分简化顶层格式，漏掉多种工具实际使用的消息包装、SQLite 字段和压缩会话。旧 Skill 分支还把加载算调用、失败算成功，并用名称作身份，导致同名调用碰撞。

| 工具 | 本次完成的修复 | 验证范围 |
| --- | --- | --- |
| Codex | 加载/开始不计 Skill，失败保留失败；不同调用使用不同事实身份 | 原生与兼容格式回归；此前已有原生采集修复 |
| Claude | message.usage、缓存细项、用户轮次、工具结果配对、成功编辑/Skill；命名事件兼容 | 本机原始文件只读探针、原生夹具及跨批次配对回归 |
| Grok Build | session/update 解包、完成用量、代码、轮次；配对成功 SKILL.md 读取，兼容命名事件 | 本机成功读出 Skill；包装、失败结果、重扫身份回归 |
| DeepSeek Harness | `.dsh/sessions`、多帧 Zstandard JSONL、帧内游标、seq/time/data、Skill 结果；排除 seedLength 继承历史 | 本机压缩来源只读扫描、半帧续扫、分页、修订身份回归 |
| Pi | message.usage、模型上下文、轮次、工具结果、Skill/成功编辑；支持 oldText/newText | 本机原始消息、原生协议及失败/中止用量夹具 |
| WorkBuddy | 优先发现 projects 原始记录；message、function_call/result、step_finish 和 Skill | 本机读出用量、代码及 Skill；跨批次配对回归 |
| Cursor | Token API 不再阻断 transcript 的会话/消息/轮次；不重复采 transcript Token | 本机 transcript、API 通道回归及 API 开启时的活动事件回归 |
| ZCode | 数字 rowid 保持稳定；缓存/reasoning/模型、session、part Skill 成功/失败 | 本机 SQLite 实际 SQL 读取、数值身份及投影夹具 |
| OpenCode | step-finish 缓存总量、模型关联、session、part Skill；保留数字 rowid | 本机 SQLite 实际 SQL 读取、缓存构成和 Skill 投影夹具 |
| 豆包 | 原生 IndexedDB/LevelDB 解码、工作模式活动与消息级 Skill 使用证据；未知 Token 不估算 | 本机云电脑/本地电脑缓存只读验证；消息去重、回复关联、压缩与不完整 WAL 回归 |

豆包确实已安装。后续解开 LevelDB 的 Snappy 压缩块后，已在 `User Data/<profile>/IndexedDB/chrome_doubao-chat_0.indexeddb.leveldb` 和 `chrome_doubao-launcher_0.indexeddb.leveldb` 定位会话记录，包含 `context_window_usage`、`token_count`、`moa_skill_usage`。此前未解压，仅搜索明文字段，导致漏查。安装资源包中的前端实现确认 context_window_usage 从消息 ext 读取，包含 system_prompt/messages/skills/tools/total_window_size；这是上下文窗口构成，不能把容量或缓存历史版本直接相加为累计消耗。Token 完整口径、最新记录去重及生产解码仍待完成。本机 Cursor transcript 的 tool_use 缺调用 id 和成功结果，不能把它直接计为成功代码修改或 Skill 使用。未观测到 Skill 的工具保留这一事实，不假设用户一定没用过。

## 统计与幂等

- Claude/Pi/DeepSeek/OpenCode 的输入与缓存按原生语义组合；ZCode 和当前 WorkBuddy 输入已包含缓存，不能再加一次。缺失字段不伪造为已知零；请求耗时不直接当用户活跃时长。
- Skill 仅按具体调用身份计数；明确成功/失败才填 success，不补 duration=0。成功读 SKILL.md 的关联证据标 correlated，读取失败不计 Skill。未确认公开的名称只本地匿名化，不上传原始路径。
- 用量和代码修订保持 fact_key，提升 revision。客户端与服务端在同一事务撤销该粒度的旧贡献、应用新贡献、更新处理状态。新旧顺序均测试；上传状态不被本地统计改写。
- 服务端撤销用量使用 UPDATE，避免 MySQL 对 unsigned INSERT 的负数校验；客户端相关模型列一次 UPDATE，避免逐列更新触发中间态约束。小时、日、月分别处理。

## 验收

- 桌面完整回归：295 passed、0 failed、13 ignored。ignored 包含需要真实登录或显式本机目录的诊断，不能算通过。
- Grok 独立适配器：23 passed、1 ignored；来源发现专项 7 项通过，包含现代 DeepSeek 路径与 WorkBuddy projects。共享发现器还验证了重叠目录、Windows 混合分隔符去重与分页。
- 全工具合同测试已从只打印报告改为关键 Token 构成/Skill 断言。另验证 Claude、Pi、WorkBuddy、Grok 的跨批次调用结果配对、失败、同名不同调用和重扫上传内容哈希稳定；Cursor 验证开启 API 后仍产活动事件。
- 本机可访问原始来源的只读探针不写应用库，不触发上传；8 个剩余工具扫描中豆包为零来源，其余均读出事件且错误数为零。DeepSeek 压缩来源独立扫描通过。
- 云上 `tokendance_dev`：连接内临时表验证用量、代码的新旧修订正序/倒序，覆盖小时/日/月。未清空共享业务表，未使用生产库。Go worker/store 单测通过。
- [夹具探针结果](probe-results.json) 只含仓库合成样本，不保存本机原始正文、私人用量、路径或凭据。

历史缺失需要新客户端重建后补传；源码修复和测试服务发布不会自动修复用户仍在运行的旧桌面安装包。正式发布继续要求合入 main 后构建。

## 豆包云电脑与本地电脑核对

后续用本机原始缓存验证两种工作模式。程序自带资源包的枚举明确 `RuntimeType_Cloud_VM=1`、`RuntimeType_Local_PC=2`；消息 `ext.general_task_param` 保存 `runtime_type`，本地测试的用户消息与回复均为 2。两种模式都能在 chat/launcher 的 IndexedDB 找到会话对象，本地模式另有 agent_infra 执行日志。

存储不是 JSONL：LevelDB 数据块可有 Snappy 压缩；IndexedDB 大值还有 `FF 11 02` 包装及内层 Snappy，随后是 Blink/V8 序列化。只解开外层或直接搜字符串都可能遗漏字段。诊断已成功反序列化本机大值，未将真实缓存、会话正文或凭据纳入仓库。

- 本地工作回复带 `context_window_usage`。程序实现将 system_prompt、tools、messages、skills 相加为 used，并单独读取 total_window_size。它描述窗口占用，不能当累计请求消耗，更不能把流式缓存的多次快照相加。
- 当前云端工作回复可见 `moa_skill_usage`；本地测试的已读回复只有 input_skill 等上下文字段，未见该使用标记。选择/加载 Skill 和窗口中的 skills Token 均不能直接算调用次数。
- 深层解压后确认 `token_count` 出现在旧普通聊天的消息 ext 中，样本属于 2024 年，未在本次两种工作模式的已读回复找到。尚不能用该旧字段证明当前工作模式的完整输入/输出/总消耗可采。
- 本地 agent_infra 日志没有发现 input_tokens/output_tokens/total_tokens/token_usage，切换本地电脑不会自动产生适配器原先寻找的用量 JSONL。
- 正式读取需要按配置隔离会话、按稳定消息/调用身份去重，并按 LevelDB 序列号与删除标记处理当前值；不能把 SST/log 的历史版本当独立事件。当前诊断的多版本样本用于确认结构，不是生产采集器，也没有写入用户统计。

结论：两种模式的本地会话文件已定位并确认；完整消耗口径仍未证实，生产读取尚未接入。不能报告豆包已完成用量采集。

### 最终复核结论

再次扫描当前缓存，并对 IndexedDB 内层压缩值补充解码后，云电脑与本地电脑两种模式均确认存在上下文 Token 构成和 Skill 使用标记；本地模式最新记录较前一轮更新，已出现 moa_skill_usage，因此此前“本地未见标记”仅是当时样本结论。两种工作模式均未读到完整输入、输出或总 Token 计数；token_count 仍只出现在旧普通聊天记录中。执行日志也没有补齐这些字段。

当前本机版本可定位、解析会话、活动与 Skill 使用证据，但现有已验证的持久化记录不足以准确还原工作模式累计 Token 消耗。不能把 context_window_usage 的窗口占用当每次模型请求用量，不能估算填充为真实总量。生产适配器尚未接通；后续若接入，应独立声明可用指标，未知 Token 保持未知。此结论适用于本次核对版本与来源，不等价于豆包没有消耗，也不声称所有未来版本或服务端接口均不提供用量。

### 本机测试接入（2026-09-13）

用户确认先接入能够验证的指标。本节更新此前“生产读取尚未接入”的调查状态：现已增加纯 Rust 原生桌面读取，不依赖 Node/Python。两种工作模式均读取用户消息、明确完成的回复、会话开始及 moa_skill_usage。Token、费用、代码行、成功率和耗时没有证据时均不补值。用户消息 ID 与回复 reply_id 配对，Skill 为消息级关联证据，不等价于模型内部全部调用次数。

此前“只处理当前值”的建议不适用此缓存：实测豆包已淘汰当前任务键，但 SST/WAL 中仍有真实消息历史版本。读取保留这些观测，按稳定消息身份合并；相同事件不因配置目录、文件、扫描批次变化而重新计数。检查完整块校验和，未写完的 WAL 尾部留待文件更新后重读；变化文件才重新解码，缓存仅保留精简统计元数据。

本次为功能分支本机测试安装，不是 main 正式桌面发布。保留设备身份、登录、设置及事件数据库，不自动清库重建；日常过滤规则继续生效，需要补历史时由用户在设置中触发重建。

此次接入验证：桌面完整回归 301 passed、14 ignored；前端用量相关 42 项及生产资源构建通过；显式本机只读验证通过，未产生 model_usage_recorded。

### Skill 真实名称展示

按用户最新要求，取消客户端清空 Skill 名称的处理，所有工具传递真实名称；路径形式保留技能目录名称作为标签，不上传绝对路径。名称不参与事件身份和内容哈希。已有匿名维度首次补齐名称时只重排上传任务，小时/日/月统计保持原状态，服务端已有维度 upsert 可补齐名称，不重复计数。此前匿名展示的描述由此替代。
