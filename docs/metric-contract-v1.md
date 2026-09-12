# 统计计算契约 v1

客户端/服务端模块、并发、ACK、空库启用和清理依赖实现见 [完整重构方案](event-pipeline-refactor-technical-plan-v1.md)。内测旧数据不迁移；本文件定义新链路的统计口径，两端以共享 fixture 验收。

用途：作为事件标准化、三个粒度投影、接口返回和对账测试的共同依据。目标架构与表见 [v3 表设计](D:/ProgrammingProjects/TokenDance/docs/statistics-model-and-indexes-v3.md)。本文件描述目标行为，尚未接入生产代码。

## 1. 时间与身份

- 首次准入按选定 occurred_at 筛选北京时间当天事件；源明确给出的历史时间不能覆盖。已准入事件跨日继续原任务。完整规则与真实缺时间样本见 [事件时间规则](D:/ProgrammingProjects/TokenDance/docs/event-time-admission-v1.md)。
- 会话文件缺失记录时间时，优先继承同文件前序有效源时间，再用读取时原始文件 mtime；payload_json.meta 记录 time_source，前序锚点随同流 decoder_state 与游标同事务提交。二者是近似时间依据，不能保证每条记录的真实日期。源时间非法或无可用兜底时 ignored，禁止补 1970 或采集时间。
- 业务桶：occurred_at 按 UTC+8 划分小时、自然日、自然月；查询采用 `[bucket_start, next_bucket_start)`。
- 明细生命周期：expire_at = 本地首次 created_at + 14 天；同一库内重试不刷新首次时间。事件发生时间不参与 TTL。
- 一次事件版本身份为 event_id；逻辑事实为 fact_key。重复相同版本不增量累计，相同 ID 不同内容报冲突。
- 普通完成明细固定 fact_revision=1。仅对有已验证修订语义、可靠先后顺序和旧贡献的来源，选择最新有效版本并撤回旧贡献；旧版本不能反盖新版本。只使用来源原生顺序和保留期内旧事件，不保存永久身份账本或本地递增版本账。
- 正常采集只推进持久化游标，事件和游标原子提交；清理事件不清理来源进度。原库不提供历史从头重放，需要重新采集时走重装并初始化本地数据。
- 服务端按 occurred_at 做时间范围过滤，事件归属不取决于何时重放；接收范围内的重复仍以稳定设备/事件唯一键处理。
- 本地不保存账号，统计是本机范围；认证上下文仅在同步时使用，服务端决定归属并以稳定设备/事件身份防重。已 ACK 事件不因换号重传。

## 2. Token 与采集覆盖

只接受 exact 和 derived 的统计用量；derived 必须能通过已知事实确定性计算。estimated 用量忽略，不展示、不上传、不落其统计分列；保留最小处理诊断与读取进度。correlated 继续仅用于已有的关联代码/Skill 类指标，不混入 Token 总量。

```text
total_tokens = exact_token_total + derived_token_total
```

原始 total 已明确报告时优先使用。没有 total，只能在来源策略确认组成完整且互斥时计算；否则 total 未知，不把 NULL 当实际零。缺总量但已知某些组件时允许保存组件、对应 coverage 和已观察请求数，界面总量为已知部分并标明覆盖，而不是猜出完整请求总量。

规范化输入必须统一到“完整输入上下文”。策略需要知道源 input 是否包含 cache read/write，不能在聚合 SQL 中按 harness 名做减法猜测。output 同理区分 reasoning 是包含关系还是额外量，不对其无条件二次相加。

| 来源事实（示例） | 规范化结果 |
| --- | --- |
| input=1000，已包含 cache_read=800；output=100，已包含 reasoning=20；total=1100 | input_context=1000，cache_read=800，output=100，reasoning=20，total=1100 |
| uncached_input=200、cache_read=800、cache_write=0 为互斥完整组成；output=100；total=1100 | 与上一行的上下文、输出和总量一致 |
| 仅报告 total=1100，没有组成 | total=1100；input/output/cache 的已知计数不增长 |
| 明确报告 total=0 | total=0 且 total_known_count 增长；区别于上一种字段缺失 |
| accuracy=estimated，声称 total=1100 | 不进入统计；不能改标 derived 或按零值入账 |

unknown model 使用稳定 unknown 维度，不能因此丢掉已知 Token。Token、覆盖和模型请求只存 model_metrics；按模型求和得到 harness 总量，必须包含 unknown。若策略支持模型信息更正且能撤回旧贡献，只移动模型归属，harness 总量不变。

覆盖接口至少能表达：`value`、`knownCount`、`observedCount`、`coverage=none/partial/complete`。`supported` 兼容旧 API，但不能只靠“值大于零”决定；不支持、未观察到、已观察且为零是不同情况。

## 3. 比例与派生指标

| 指标 | 公式与边界 |
| --- | --- |
| 缓存命中率 | 相同完整样本的 cache_read 总和 / input_context 总和；只纳入两个字段均已知且关系有效的请求；分母为零返回 null |
| 每行代码 Token | 已知可信 Token 总和 / 可信 generated_lines 总和；分母为零返回 null，同时附相关覆盖信息 |
| Skill 成功率 | success_count / (success_count + failure_count)；未知结果不入分母；分母为零返回 null |
| Harness/模型占比 | 相同统计主体、时间范围和精度下的分子 / 同范围总量；本地按本机、服务端按授权账号；不能混用 grain 或忽略 unknown 模型贡献 |
| 昨日变化率 | (今天−昨天)/昨天；昨天为零或缺失返回 null，不伪造增长百分比 |

例：两次请求分别命中 90/100 和 10/1000，合计命中率为 100/1100≈9.09%，不是 45.5%。若第三次只知道缓存 100、不知道输入，不将其混入分子；coverage 说明第三次未参与。

## 4. 会话、轮次、消息

- session_count：该粒度桶内出现过的去重主会话数；child_session_count 单列。它不等于 SessionStarted 的数量。
- interaction_turn_count：该粒度内出现过的去重逻辑轮次数。
- model_request_count：去重后的模型请求事实数；一次轮次可以调用模型多次，不当成消息数。
- message_count = 去重 turn_started_count + 去重 turn_completed_count。
- user_message_count = trigger=user 的去重 turn_started_count。
- tool_call_count、skill_use_count 按各自逻辑调用事实计数；工具调用不自动成为一条用户消息。

身份由 source/device 的稳定命名空间限定；同一原生 turn 字符串出现在不同 session 时不能撞键。主/子身份来自明确关联，不用是否含嵌套目录推断。

一轮 start/user、两次模型请求、一次 complete：模型请求=2、轮次=1、消息=2、用户消息=1。任意记录重复接收，四个数字都不变。

同会话跨两天：每日各计 1，月粒度唯一会话为 1，不能 SUM 两天会话数当月唯一值。没有完整身份状态时不可从日总数反推去重月数。当前不新增全历史唯一会话卡片。

## 5. 活动时长

保持现有业务日的权威来源规则：同一主会话、同一 UTC+8 日期，有已报告会话结束时长就选它；否则累计该会话的去重轮次结束时长。单位毫秒，已知零时长也有观测意义。

这是工具活动累计时长，不是多会话重叠区间去重后的人的工作时长。跨日按目前“业务日内选择、结束归属”的语义处理，不把只知道一个 duration 的记录均匀摊成每小时真实活动时间。

例：09 点轮次 100ms、10 点轮次 200ms，初始日合计 300ms；11 点得到会话结束 500ms，应撤回旧 300ms 再加 500ms，不能变成 800ms。对应小时投影也撤回 09/10 点旧 fallback，计入 11 点 500ms。先收到 session_end 再收到两个 turn_end，最终结果仍为 500ms。

小时/日/月任务各自独立，但使用同一权威选择规则。一次更正可能修改多个小时桶和两个日期/月桶；consumer 的完成状态只能在所有关联桶更新成功后提交。

## 6. 代码与 Skill

generated、accepted、added、removed 分开存储；只有明确来源才能给相应字段赋值。added 不是 generated，工具 Write 次数不是 accepted。correlated generated_lines 单独记录，不计入可信生成行和每行 Token 的分母。

file_count 累计命名为 file_touch_count（触达次数），没有稳定安全文件身份时不能称为“唯一文件数”。

Skill 的名称是标签，完整匿名 skill_key 是身份。重复调用按调用事实键去重；改 public_name 不重算调用次数。活跃天数从日粒度取日期去重；同一天多个 harness 的同一 Skill 不重复增加活跃日。不同设备下不可关联的私有 hash 不猜测合并。

## 7. 费用

这里仍保留“已知 Token × 已知价格”的计算费用，它是独立 cost_source，不是 accuracy=estimated 的猜测用量。当前规则：相同计费范围内 provider_reported 优先，其余有可靠价格和用量的请求可按冻结的价格版本计算；缺价格或计费基础则 unpriced。

先定义 cost_scope_key（请求或明确覆盖的轮次），再做来源替换。不能将请求估价和覆盖这些请求的轮次上报费用一起累计。

例：同一轮次两个请求原估价 2、3 美元，后收到覆盖该轮次的上报费用 4 美元：生效金额从 5 改为 4，不是 9。只有确知轮次覆盖哪些请求时才同步调整 reported/estimated request_count；无法确定时报告范围覆盖，不能假装一条轮次账单等于一个模型请求。

cost_metrics 每笔有效金额只存一个模型归属；按模型求和就是 harness 金额，不另存一份工具总额行。金额以 10^-8 币种单位存整数；按币种分别汇总。USD 1 与 CNY 7 不能直接等于“USD 8”。按模型拆解无法定位的金额保留 unknown model，不能复制给轮次内每个模型。

## 8. 三个统计消费者、上传状态与事务验收

事件使用 status_json 保存四个独立状态：同一事件可以 hour=applied、day=retry、month=applied、upload=acked；重试 day 不能把其他三个状态回退。

领取任务先获取有效租约；在 writer 事务内再次验证租约、事实顺序、事件和任务未软删、事件存在且未过期及本 consumer 尚未完成，读取最新实体状态后应用贡献，同时提交相关桶、实体状态和本 consumer 状态并删除任务、维护实际更新行的 updated_at。JSON 状态用 json_set 只修改本 consumer 路径。旧 worker 的迟到响应不能改变新租约结果；不得按事务外过时状态整行覆盖。

每个桶只保存一份已提交结果，不设 generation、input_revision/applied_revision 或全局 head。完成状态以事件为准，任务只保存执行信息；桶更新时间和最大完成事件 ID 都不能证明其他任务没有缺口。统计修复需有完整已准入事实或可信基底，暂停受影响消费者后事务替换，失败回滚。

每表统一 id、created_at、updated_at、delete_at、extra。created_at 首次创建后不变；updated_at/delete_at 不参与 TTL 和业务内容 hash。extra 为本地可选扩展对象，不替代类型化 payload、状态或账号持久化。

14 天期满物理清理明细和执行任务，包括已软删除明细，保留统计和来源进度，不保留永久逐事件身份摘要。未完成事件按来源累计过期数量，每个事件只计一次；不伪造成功、不创建延期完整明细存储。

## 9. 实施验收样例

| 场景 | 必须得到的结果 |
| --- | --- |
| 同一库保留期内事件重复读取、换批次重传 | 数值不变；服务端按唯一键处理重复 |
| 同 ID 不同事实内容 | 冲突，不能静默忽略 |
| 已验证支持修订的来源，同事实 v3 先于 v2 到达 | 最新贡献不回退；普通完成明细不启用版本递增 |
| 模型 unknown 后补齐 | 只移动模型归属，harness 总量不增加 |
| 输入含缓存和互斥组件两种来源 | 规范化后的相同业务量得到相同统计 |
| 两个不同请求量的缓存比率 | 加分子分母后相除，不平均比例 |
| 会话时长后到/先到 | 都替代同日轮次 fallback，最终相同 |
| 日任务失败，其他任务完成 | 只重试日任务，不重复累计其他粒度 |
| 昨天发生、今天首次采到 | 丢弃业务事件，不入明细、不生成统计/上传任务；记录最小诊断并推进游标 |
| 昨天已经合法入库、今天继续重试 | 保留原任务，从首次 created_at 起保存 14 天，不刷新 TTL |
| 会话文件记录没有时间 | 前序有效源时间 → 原始文件 mtime；标记 time_source，按选定日期准入，入库后时间冻结 |
| 源时间非法，或缺失且没有可用兜底 | 明确 ignored，不能填 1970 或当前时间 |
| 清理/软删除与在途计算/ACK 竞争 | 已过期或软删事件不复活，不提交无有效租约的统计 |
| 明细尚存在时软删后重复读取相同身份 | 仍判重，业务唯一键不释放；不能直接生成第二行 |
| 明细满 14 天清理 | 来源游标仍保留，正常启动继续增量读取，不重新读已提交部分 |
| 重装后重新采集源事件 | 事件归日与服务端时间过滤仍按 occurred_at，不按重放时间 |
| 更新日状态时小时/上传状态已变化 | json_set 仅修改日路径，保留其他路径，并在同事务维护 updated_at |
| 统计覆盖为零/部分/全部 | 与数值零区分，比例只使用同覆盖样本 |

SQL 草案已验证基础约束及索引；本表中的时长选择、费用替换、身份修订和事务故障场景仍需在实际 Rust/Go 处理器实现中验收，不能用 DDL 检查代替。
