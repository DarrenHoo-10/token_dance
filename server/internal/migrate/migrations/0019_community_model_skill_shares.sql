-- Community model and skill boards reuse community_daily_stats.
-- Request paths still only read precomputed rows; the stats worker fills
-- these JSON arrays from day-grain telemetry during the existing recompute.
ALTER TABLE community_daily_stats
  ADD COLUMN model_shares JSON NOT NULL DEFAULT (JSON_ARRAY())
    COMMENT '当日社区模型 Token 用量；元素含 modelId、label、tokens；空数组表示当日无模型用量，不把零值当成缺测'
    AFTER cost_amounts,
  ADD COLUMN skill_shares JSON NOT NULL DEFAULT (JSON_ARRAY())
    COMMENT '当日社区公开 Skill 调用次数；元素含 skillId、label、uses；仅含有公开名称的 Skill；空数组表示当日无公开 Skill 用量'
    AFTER model_shares;
