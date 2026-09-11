package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

const telemetryTaskLeaseTTL = 45 * time.Second
const telemetryTaskClaimLimit = 100

// TelemetryTaskClaim is a leased aggregation task ready for a separate execute tx.
type TelemetryTaskClaim struct {
	TaskID     uint64
	EventRowID uint64
	Consumer   string
	LeaseToken []byte
	LeaseUntil time.Time
}

// ClaimTelemetryTasks leases due tasks for one consumer with SKIP LOCKED.
// Claim and execute are separate transactions by design.
func ClaimTelemetryTasks(ctx context.Context, db *sql.DB, consumer string, now time.Time, limit int) ([]TelemetryTaskClaim, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = telemetryTaskClaimLimit
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim telemetry tasks: %w", err)
	}
	defer tx.Rollback()

	nowMs := now.UnixMilli()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, event_row_id
		FROM telemetry_tasks
		WHERE consumer = ?
		  AND delete_at IS NULL
		  AND runnable_at IS NOT NULL
		  AND runnable_at <= ?
		ORDER BY runnable_at, id
		LIMIT ?
		FOR UPDATE SKIP LOCKED`, consumer, nowMs, limit)
	if err != nil {
		return nil, fmt.Errorf("select due telemetry tasks: %w", err)
	}
	type pending struct {
		id, eventRowID uint64
	}
	var due []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.eventRowID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan telemetry task: %w", err)
		}
		due = append(due, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(due) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	leaseUntil := now.Add(telemetryTaskLeaseTTL)
	leaseUntilMs := leaseUntil.UnixMilli()
	claims := make([]TelemetryTaskClaim, 0, len(due))
	for _, p := range due {
		token, err := crypto.GenerateRandomBytes(16)
		if err != nil {
			return nil, fmt.Errorf("lease token: %w", err)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE telemetry_tasks
			SET lease_token = ?,
			    lease_until = ?,
			    runnable_at = NULL,
			    attempt_count = attempt_count + 1,
			    last_error_code = NULL,
			    updated_at = ?
			WHERE id = ?
			  AND delete_at IS NULL
			  AND runnable_at IS NOT NULL`,
			token, leaseUntilMs, nowMs, p.id,
		)
		if err != nil {
			return nil, fmt.Errorf("lease telemetry task %d: %w", p.id, err)
		}
		affected, _ := res.RowsAffected()
		if affected != 1 {
			continue
		}
		claims = append(claims, TelemetryTaskClaim{
			TaskID:     p.id,
			EventRowID: p.eventRowID,
			Consumer:   consumer,
			LeaseToken: token,
			LeaseUntil: leaseUntil,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim telemetry tasks: %w", err)
	}
	return claims, nil
}

// ReclaimExpiredTelemetryLeases restores runnable_at for expired leases (all consumers).
func ReclaimExpiredTelemetryLeases(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	if db == nil {
		return 0, nil
	}
	nowMs := now.UnixMilli()
	res, err := db.ExecContext(ctx, `
		UPDATE telemetry_tasks
		SET runnable_at = ?,
		    lease_token = NULL,
		    lease_until = NULL,
		    last_error_code = 'lease_expired',
		    updated_at = ?
		WHERE delete_at IS NULL
		  AND lease_until IS NOT NULL
		  AND lease_until <= ?`, nowMs, nowMs, nowMs)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired telemetry leases: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ReclaimExpiredDirtyDayLeases restores next_attempt_at for expired dirty refresh leases.
func ReclaimExpiredDirtyDayLeases(ctx context.Context, db *sql.DB, now time.Time) (int, error) {
	if db == nil {
		return 0, nil
	}
	nowMs := now.UnixMilli()
	res, err := db.ExecContext(ctx, `
		UPDATE aggregate_dirty_days
		SET next_attempt_at = ?,
		    claim_token = NULL,
		    lease_expires_at = NULL,
		    last_error_code = 'lease_expired',
		    updated_at = ?
		WHERE delete_at IS NULL
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= ?`, nowMs, nowMs, nowMs)
	if err != nil {
		return 0, fmt.Errorf("reclaim expired dirty day leases: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DirtyDayClaim is a leased read-model refresh for one user×day.
type DirtyDayClaim struct {
	ID           uint64
	UserID       string
	MetricDate   string
	DirtyVersion uint64
	ClaimToken   []byte
}

const dirtyDayLeaseTTL = 60 * time.Second

// ClaimDirtyDays leases pending dirty days with SKIP LOCKED and remembers dirty_version.
func ClaimDirtyDays(ctx context.Context, db *sql.DB, now time.Time, limit int) ([]DirtyDayClaim, error) {
	if db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin claim dirty days: %w", err)
	}
	defer tx.Rollback()

	nowMs := now.UnixMilli()
	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id, DATE_FORMAT(metric_date, '%Y-%m-%d'), dirty_version
		FROM aggregate_dirty_days
		WHERE delete_at IS NULL
		  AND applied_version < dirty_version
		  AND next_attempt_at IS NOT NULL
		  AND next_attempt_at <= ?
		ORDER BY next_attempt_at, id
		LIMIT ?
		FOR UPDATE SKIP LOCKED`, nowMs, limit)
	if err != nil {
		return nil, fmt.Errorf("select due dirty days: %w", err)
	}
	type pending struct {
		id, version uint64
		userID, date string
	}
	var due []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.userID, &p.date, &p.version); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan dirty day: %w", err)
		}
		due = append(due, p)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(due) == 0 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}

	leaseUntilMs := now.Add(dirtyDayLeaseTTL).UnixMilli()
	claims := make([]DirtyDayClaim, 0, len(due))
	for _, p := range due {
		token, err := crypto.GenerateRandomBytes(16)
		if err != nil {
			return nil, err
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE aggregate_dirty_days
			SET claim_token = ?,
			    lease_expires_at = ?,
			    next_attempt_at = NULL,
			    attempt_count = attempt_count + 1,
			    last_error_code = NULL,
			    updated_at = ?
			WHERE id = ?
			  AND delete_at IS NULL
			  AND next_attempt_at IS NOT NULL
			  AND applied_version < dirty_version`,
			token, leaseUntilMs, nowMs, p.id,
		)
		if err != nil {
			return nil, fmt.Errorf("lease dirty day %d: %w", p.id, err)
		}
		affected, _ := res.RowsAffected()
		if affected != 1 {
			continue
		}
		claims = append(claims, DirtyDayClaim{
			ID:           p.id,
			UserID:       p.userID,
			MetricDate:   p.date,
			DirtyVersion: p.version,
			ClaimToken:   token,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit claim dirty days: %w", err)
	}
	return claims, nil
}

// ConfirmDirtyDayAtVersionTx confirms applied_version=v when still matching claim.
// If dirty_version grew during refresh, requeues with next_attempt_at=now.
func ConfirmDirtyDayAtVersionTx(ctx context.Context, tx *sql.Tx, claim DirtyDayClaim, now time.Time) (confirmed bool, requeued bool, err error) {
	nowMs := now.UnixMilli()
	var dirtyVersion, appliedVersion uint64
	err = tx.QueryRowContext(ctx, `
		SELECT dirty_version, applied_version
		FROM aggregate_dirty_days
		WHERE id = ? AND claim_token = ?
		FOR UPDATE`, claim.ID, claim.ClaimToken).Scan(&dirtyVersion, &appliedVersion)
	if err != nil {
		if err == sql.ErrNoRows {
			return false, false, nil
		}
		return false, false, fmt.Errorf("lock dirty day for confirm: %w", err)
	}
	if dirtyVersion > claim.DirtyVersion {
		if _, err := tx.ExecContext(ctx, `
			UPDATE aggregate_dirty_days
			SET claim_token = NULL,
			    lease_expires_at = NULL,
			    next_attempt_at = ?,
			    updated_at = ?
			WHERE id = ? AND claim_token = ?`,
			nowMs, nowMs, claim.ID, claim.ClaimToken,
		); err != nil {
			return false, false, fmt.Errorf("requeue dirty day: %w", err)
		}
		return false, true, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE aggregate_dirty_days
		SET applied_version = ?,
		    claim_token = NULL,
		    lease_expires_at = NULL,
		    next_attempt_at = NULL,
		    last_error_code = NULL,
		    updated_at = ?
		WHERE id = ? AND claim_token = ?`,
		claim.DirtyVersion, nowMs, claim.ID, claim.ClaimToken,
	); err != nil {
		return false, false, fmt.Errorf("confirm dirty day: %w", err)
	}
	_ = appliedVersion
	_ = domain.TelemetryGrainDay
	return true, false, nil
}
