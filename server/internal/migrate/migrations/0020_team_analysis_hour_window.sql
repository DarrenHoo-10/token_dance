-- Preserve the exact UTC hour range behind a team analysis snapshot.
ALTER TABLE team_analysis_snapshots
  ADD COLUMN from_at DATETIME(3) NULL
    COMMENT '精确查询下界UTC；NULL表示旧版按from_date自然日起算' AFTER to_date_exclusive,
  ADD COLUMN to_at_exclusive DATETIME(3) NULL
    COMMENT '精确查询上界UTC；NULL表示旧版按to_date_exclusive自然日结束' AFTER from_at,
  ADD COLUMN range_key VARCHAR(16) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT 'custom'
    COMMENT '统计范围键；today使用滚动24小时，其余为自然日范围' AFTER to_at_exclusive;
