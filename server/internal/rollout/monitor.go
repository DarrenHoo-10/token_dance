package rollout

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// MonitorSnapshot is the P8 minimum ops panel for empty-DB cutover.
type MonitorSnapshot struct {
	CapturedAt              time.Time `json:"capturedAt"`
	RolloutPhase            string    `json:"rolloutPhase"`
	RolloutGeneration       uint64    `json:"rolloutGeneration"`
	PendingTelemetryTasks   int64     `json:"pendingTelemetryTasks"`
	OldestPendingTaskAgeSec int64     `json:"oldestPendingTaskAgeSec"`
	DirtyUnappliedDays      int64     `json:"dirtyUnappliedDays"`
	TelemetryEventCount     int64     `json:"telemetryEventCount"`
	LegacyUsageEventCount   int64     `json:"legacyUsageEventCount"`
	RankingOutboxPending    int64     `json:"rankingOutboxPending"`
}

// CollectMonitorSnapshot gathers backlog / dirty / legacy residual counts.
func CollectMonitorSnapshot(ctx context.Context, db *sql.DB) (*MonitorSnapshot, error) {
	st, err := LoadState(ctx, db)
	if err != nil {
		return nil, err
	}
	nowMs := time.Now().UTC().UnixMilli()
	snap := &MonitorSnapshot{
		CapturedAt:        time.Now().UTC(),
		RolloutPhase:      st.Phase,
		RolloutGeneration: st.Generation,
	}

	_ = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM telemetry_tasks
		WHERE delete_at IS NULL`).Scan(&snap.PendingTelemetryTasks)

	var oldest sql.NullInt64
	_ = db.QueryRowContext(ctx, `
		SELECT MIN(created_at) FROM telemetry_tasks
		WHERE delete_at IS NULL`).Scan(&oldest)
	if oldest.Valid && oldest.Int64 > 0 {
		age := (nowMs - oldest.Int64) / 1000
		if age < 0 {
			age = 0
		}
		snap.OldestPendingTaskAgeSec = age
	}

	_ = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM aggregate_dirty_days
		WHERE dirty_version > applied_version AND delete_at IS NULL`).Scan(&snap.DirtyUnappliedDays)

	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_events WHERE delete_at IS NULL`).Scan(&snap.TelemetryEventCount)
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events`).Scan(&snap.LegacyUsageEventCount)
	_ = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ranking_outbox
		WHERE task_status IN ('pending','leased','failed')`).Scan(&snap.RankingOutboxPending)

	return snap, nil
}

// AssertNoLegacyStatsResidual fails acceptance when old usage/outbox rows remain after reset.
func AssertNoLegacyStatsResidual(ctx context.Context, db *sql.DB) error {
	snap, err := CollectMonitorSnapshot(ctx, db)
	if err != nil {
		return err
	}
	if snap.LegacyUsageEventCount != 0 {
		return fmt.Errorf("legacy usage_events residual count=%d", snap.LegacyUsageEventCount)
	}
	if snap.RankingOutboxPending != 0 {
		return fmt.Errorf("ranking_outbox residual pending=%d", snap.RankingOutboxPending)
	}
	return nil
}
