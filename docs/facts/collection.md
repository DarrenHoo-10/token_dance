# 采集与本地统计

最近核对：2026-09-13；适用 `codex/fix-dashboard-metrics` 工作分支，未等同于已发布桌面版本。验收与各工具限制见 [全工具采集修复](../process/harness-coverage-audit/README.md)。

采集由 harness 策略负责发现来源、分页读原始记录和解码；runner 复用准入、游标提交、事件落库与任务调度。主要入口为 `collector/apps/desktop/src-tauri/src/local_store/pipeline/`，安装/来源发现位于 `collector/apps/service/src/detect.rs`。

`adapters/native_jsonl.rs` 处理 Claude/Pi/WorkBuddy 原生消息、Grok 包装和 DeepSeek 原生信封；`jsonl_harness.rs` 负责共享读取及旧格式兼容；`zstd_jsonl.rs` 为 DeepSeek 按完整压缩帧读取，游标同时记录物理偏移与帧内行数。ZCode/OpenCode 策略分别执行原生 SQLite 投影，用数字 rowid 定位记录。

工具调用的待配对信息保存在 decoder state，可跨批次续扫。只保存识别和计数需要的内容，不保存完整编辑正文。成功结果才形成成功编辑；失败 Skill 保留失败；仅加载 Skill 不形成调用。Skill 展示采集到的真实名称；文件读取型 Skill 使用技能目录名称，绝对路径仍只用于本地稳定身份。Cursor API 用量与 transcript 非 Token 活动独立。

本地 `apply.rs` 和服务端 `telemetry_aggregation.go` 对用量、代码的同 fact_key 修订执行贡献替换，避免新旧累计。事实键与设备身份保持稳定。事件协议与计数口径沿用 [统计计量契约](../metric-contract-v1.md)。

真实来源目前存在限制：豆包桌面适配器读取 Chromium IndexedDB/LevelDB 中可验证的工作模式活动与 Skill 使用标记；Token 保持未知，context_window_usage 不能当累计消耗。Cursor 的当前 transcript 缺完整工具结果，不能据此推算成功编辑。无原始证据的指标保持未知，不用估算补齐。

豆包两种工作模式均已在本机验证：runtime_type=1 为云电脑，2 为本地电脑，消息保存在 IndexedDB；大值可能另有一层 Snappy/Blink/V8 包装。当前工作模式已确认上下文窗口构成，旧普通聊天中的 token_count 不代表当前工作任务总消耗。

豆包读取 SST/WAL 与 Snappy/Blink/V8 包装，按稳定会话/消息身份合并缓存历史版本，缓存淘汰不撤销已观测活动。用户消息与 reply_id 配对，第一条用户消息确认会话开始；明确 is_finish 才计回复完成。moa_skill_usage 按消息与技能名称去重，表示消息级使用证据（correlated），不推算内部调用总数、成功率或耗时；选择/加载列表不计调用。只缓存文件的精简统计记录，文件变化才重新解析，不上传正文或原始路径，Skill 名称作为展示元数据上传。无完整历史文件时不能承诺补齐全量历史。
