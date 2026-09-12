-- Per-currency community costs. cost_amount stays the single-currency
-- scalar (0 when mixed or empty). cost_amounts is the FX-free breakdown;
-- never SUM distinct currencies into one number.
ALTER TABLE community_daily_stats
  ADD COLUMN cost_amounts JSON NULL AFTER cost_amount;
