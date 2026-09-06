# OpenRouter 费用估算与补充采集

- 后台读取 `https://openrouter.ai/api/v1/models`，价格缓存 6 小时；失败时保留缓存并退避重试。
- 金额采用十进制定点结果（USD，8 位小数），按输入、输出、缓存读写及请求费估算，并应用 `min_prompt_tokens` 阶梯价。推理 Token 作为输出的子集，不重复计费。
- Codex 的输入 Token 包含缓存读入，计算费用时先扣除缓存部分；Grok/Claude 标准化流的输入与缓存是独立字段。
- 仅精确模型 ID、唯一 slug 或明确列出的别名可匹配。别名：`grok-4.6-build` → `x-ai/grok-4.6`、`gemini-3.7-flash-high` → `google/gemini-3.7-flash`、`claude-opus-4-6-thinking` → `anthropic/claude-opus-4.6`。
- 仅补算没有费用的记录，并保存价格表抓取时间、映射模型和参考单价。历史补算采用补算时的参考价格，不代表历史账单；不覆盖已保存费用。
- 每次模型调用单独计费；相同回合有服务商实报费用时，以实报费用为准。未匹配模型不按免费处理，页面显示未定价请求数。
- Codex 新增读取 `turn_context.model` 和 `task_complete.duration_ms`；从成功执行的 `apply_patch` 中统计新增/删除行，只保留计数，不上传代码或文件路径。
- 支持直接 `apply_patch` 和显式输出单个静态 JSON 字符串参数的 `text(await tools.apply_patch(...))` 包装；复杂动态脚本不猜测行数。
- 启动时重扫 Codex 日志，以稳定事件 ID 去重，补上过去未支持的时长和代码事件。已上传且标为 `unknown` 的历史模型不会凭当前模型猜测替换。
