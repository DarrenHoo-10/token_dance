package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
	"tokendance/internal/domain"
	mysqlstore "tokendance/internal/store/mysql"
)

// Extents persist independently of retained event details and separately per consumer.
func applySessionExtent(ctx context.Context, tx *sql.Tx, grain string, ev *telemetryEventRow, now int64) (bool, error) {
	if len(ev.SessionKey) != 32 || ev.EventType == "cost_recorded" {
		return false, nil
	}
	var oldStart, oldEnd int64
	err := tx.QueryRowContext(ctx, "SELECT first_event_at,last_event_at FROM telemetry_session_extents WHERE installation_id=? AND harness_id=? AND session_key=? AND grain=? AND delete_at IS NULL FOR UPDATE", ev.InstallationID, ev.HarnessID, ev.SessionKey, grain).Scan(&oldStart, &oldEnd)
	exists := err == nil
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	start, end := ev.OccurredAtMs, ev.OccurredAtMs
	if exists {
		start = min(start, oldStart)
		end = max(end, oldEnd)
		if start == oldStart && end == oldEnd {
			return false, nil
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO telemetry_session_extents(created_at,updated_at,installation_id,harness_id,session_key,grain,first_event_at,last_event_at) VALUES(?,?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE first_event_at=VALUES(first_event_at),last_event_at=VALUES(last_event_at),updated_at=VALUES(updated_at)`, now, now, ev.InstallationID, ev.HarnessID, ev.SessionKey, grain, start, end)
	if err != nil {
		return false, err
	}
	bucket, err := domain.BucketStartMs(grain, start)
	if err != nil {
		return false, err
	}
	oldFirst, _ := domain.BucketStartMs(grain, oldStart)
	oldLast, _ := domain.BucketStartMs(grain, oldEnd)
	for {
		next := bucket + 3600000
		if grain == "day" {
			next = bucket + 86400000
		}
		if grain == "month" {
			next, _ = domain.BucketStartMs(grain, bucket+32*86400000)
		}
		duration := max(int64(0), min(end, next)-max(start, bucket))
		prior := int64(0)
		known := int64(1)
		if exists {
			prior = max(int64(0), min(oldEnd, next)-max(oldStart, bucket))
			if oldFirst <= bucket && bucket <= oldLast {
				known = 0
			}
		}
		if duration != prior || known != 0 {
			err = mysqlstore.ApplyHarnessMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucket, ev.HarnessID, ev.MetricSemanticsVersion, now, map[string]int64{"active_duration_ms": duration - prior, "duration_known_count": known})
			if err != nil {
				return false, err
			}
			if grain == "day" {
				if err = mysqlstore.MarkAggregateDirtyDayTx(ctx, tx, ev.UserID, domain.DayDate(time.UnixMilli(bucket)), time.UnixMilli(now)); err != nil {
					return false, err
				}
			}
		}
		if next > end {
			break
		}
		bucket = next
		if exists && bucket > oldFirst && bucket < oldLast {
			bucket = oldLast
		}
	}
	return true, nil
}

// Replay only the duration projection, without changing event consumer status or
// replaying token/code counters. Cursor need not persist: extents are idempotent.
func (w *Worker) backfillSessionExtents(ctx context.Context, now int64) (int, error) {
	w.sessionBackfillMu.Lock()
	defer w.sessionBackfillMu.Unlock()
	rows, err := w.db.QueryContext(ctx, "SELECT id FROM telemetry_events WHERE id>? AND delete_at IS NULL ORDER BY id LIMIT 128", w.sessionBackfillCursor)
	if err != nil {
		return 0, err
	}
	var ids []uint64
	for rows.Next() {
		var id uint64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		var ev telemetryEventRow
		err = tx.QueryRowContext(ctx, `SELECT e.id,i.user_id,e.installation_id,e.harness_id,e.event_type,e.occurred_at,e.session_key,e.status_json,e.metric_semantics_version FROM telemetry_events e JOIN installations i ON i.installation_id=e.installation_id WHERE e.id=? AND e.delete_at IS NULL FOR UPDATE`, id).Scan(&ev.ID, &ev.UserID, &ev.InstallationID, &ev.HarnessID, &ev.EventType, &ev.OccurredAtMs, &ev.SessionKey, &ev.StatusJSON, &ev.MetricSemanticsVersion)
		if err == sql.ErrNoRows {
			tx.Rollback()
			w.sessionBackfillCursor = id
			continue
		}
		if err != nil {
			tx.Rollback()
			return 0, err
		}
		var status map[string]int
		if err = json.Unmarshal(ev.StatusJSON, &status); err != nil {
			tx.Rollback()
			return 0, err
		}
		for _, grain := range []string{"hour", "day", "month"} {
			if status[grain] == 3 {
				if _, err = applySessionExtent(ctx, tx, grain, &ev, now); err != nil {
					tx.Rollback()
					return 0, err
				}
			}
		}
		if err = tx.Commit(); err != nil {
			return 0, err
		}
		w.sessionBackfillCursor = id
	}
	return len(ids), nil
}
