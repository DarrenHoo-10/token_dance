package worker

import (
	"context"
	"fmt"

	"tokendance/internal/teammetrics"
)

// ProcessStaleTeamStatic re-projects current occupants whose personal model/skill
// summaries were not written into team_member_day_metrics (for example after a
// projection-rule change with unchanged personal totals).
func (w *Worker) ProcessStaleTeamStatic(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	ids, err := teammetrics.ListStaleTeamProjectionUsers(ctx, w.db, 2)
	if err != nil {
		return 0, err
	}
	nowMs := w.clk.Now().UTC().UnixMilli()
	n := 0
	for _, userID := range ids {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			return n, err
		}
		if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, userID, nil, nowMs); err != nil {
			_ = tx.Rollback()
			return n, fmt.Errorf("refresh stale team static %s: %w", userID, err)
		}
		if err := tx.Commit(); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
