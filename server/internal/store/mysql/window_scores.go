package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

const (
	scoreWriteBackfill = 1
	scoreWriteAbsolute = 2

	rankingOutboxUpsert = "upsert"
	rankingOutboxRemove = "remove"
)

var leaderboardWindows = []string{"today", "7d", "30d", "all"}

func WindowGeneration(now time.Time) string {
	return now.UTC().Format("2006-01-02")
}

func previousWindowGeneration(now time.Time) string {
	return now.UTC().AddDate(0, 0, -1).Format("2006-01-02")
}

func newRankingOutboxID() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate ranking outbox id: %w", err)
	}
	return "rob_" + token, nil
}

func leaderboardDisplayNameExpr() string {
	return `CASE
		WHEN NULLIF(TRIM(p.display_name), '') IS NOT NULL THEN TRIM(p.display_name)
		WHEN NULLIF(TRIM(u.display_name), '') IS NOT NULL THEN TRIM(u.display_name)
		WHEN u.locale = 'zh-CN' THEN '开发者'
		ELSE 'Developer'
	END`
}

func leaderboardHandleExpr() string {
	return `COALESCE(NULLIF(TRIM(p.handle), ''), NULLIF(TRIM(u.handle), ''), u.user_id)`
}

func InsertZeroWindowScoresTx(ctx context.Context, tx *sql.Tx, userID string, registeredAt, now time.Time) error {
	if registeredAt.IsZero() {
		registeredAt = now
	}
	generation := WindowGeneration(now)
	for _, window := range leaderboardWindows {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_window_scores (
				user_id, window_key, generation, token_total, revision,
				eligible, registered_at, updated_at
			) VALUES (?, ?, ?, 0, 0, TRUE, ?, ?)
			ON DUPLICATE KEY UPDATE user_id = user_id`,
			userID, window, generation, registeredAt, now,
		); err != nil {
			return fmt.Errorf("insert zero window score %s: %w", window, err)
		}
		if err := insertRankingOutboxTx(ctx, tx, userID, window, generation, 0, 0, rankingOutboxUpsert, now); err != nil {
			return err
		}
	}
	return nil
}

func MarkAggregateDirtyDayTx(ctx context.Context, tx *sql.Tx, userID, metricDate string, now time.Time) error {
	if userID == "" || metricDate == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO aggregate_dirty_days (
			user_id, metric_date, dirty_version, applied_version, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, 1, 0, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			dirty_version = dirty_version + 1,
			next_attempt_at = LEAST(next_attempt_at, VALUES(next_attempt_at)),
			last_error_code = NULL,
			updated_at = VALUES(updated_at)`,
		userID, metricDate, now, now, now,
	); err != nil {
		return fmt.Errorf("mark aggregate dirty day: %w", err)
	}
	return nil
}

func ClearAggregateDirtyDaysTx(ctx context.Context, tx *sql.Tx, userID string, dates []string, now time.Time) error {
	if userID == "" || len(dates) == 0 {
		return nil
	}
	query := "UPDATE aggregate_dirty_days SET applied_version = dirty_version, claim_token = NULL, locked_by = NULL, lease_expires_at = NULL, last_error_code = NULL, updated_at = ? WHERE user_id = ? AND metric_date IN (" + placeholders(len(dates)) + ")"
	args := make([]interface{}, 0, 2+len(dates))
	args = append(args, now, userID)
	for _, date := range dates {
		args = append(args, date)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("clear aggregate dirty days: %w", err)
	}
	return nil
}

func ListPendingDirtyDaysTx(ctx context.Context, tx *sql.Tx, now time.Time, limit int) (map[string][]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT user_id, DATE_FORMAT(metric_date, '%Y-%m-%d') AS metric_date
		FROM aggregate_dirty_days
		WHERE applied_version < dirty_version AND next_attempt_at <= ?
		ORDER BY user_id, metric_date
		LIMIT ?`, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending dirty days: %w", err)
	}
	defer rows.Close()
	targets := make(map[string][]string)
	for rows.Next() {
		var userID, date string
		if err := rows.Scan(&userID, &date); err != nil {
			return nil, fmt.Errorf("scan pending dirty day: %w", err)
		}
		targets[userID] = append(targets[userID], date)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending dirty days: %w", err)
	}
	return targets, nil
}

func SyncWindowScoreEligibilityTx(ctx context.Context, tx *sql.Tx, userID string, eligible bool, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT window_key, generation, token_total, revision, eligible
		FROM user_window_scores
		WHERE user_id = ?
		FOR UPDATE`, userID)
	if err != nil {
		return fmt.Errorf("lock window scores for eligibility: %w", err)
	}
	type scoreRow struct {
		window, generation string
		tokens, revision   uint64
		eligible           bool
	}
	var scores []scoreRow
	for rows.Next() {
		var row scoreRow
		if err := rows.Scan(&row.window, &row.generation, &row.tokens, &row.revision, &row.eligible); err != nil {
			rows.Close()
			return fmt.Errorf("scan window score eligibility: %w", err)
		}
		scores = append(scores, row)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close window score eligibility: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate window score eligibility: %w", err)
	}
	for _, row := range scores {
		if row.eligible == eligible {
			continue
		}
		newRevision := row.revision + 1
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_window_scores
			SET eligible = ?, revision = ?, updated_at = ?
			WHERE user_id = ? AND window_key = ? AND generation = ?`,
			eligible, newRevision, now, userID, row.window, row.generation,
		); err != nil {
			return fmt.Errorf("update window score eligibility: %w", err)
		}
		op := rankingOutboxUpsert
		if !eligible {
			op = rankingOutboxRemove
		}
		if err := insertRankingOutboxTx(ctx, tx, userID, row.window, row.generation, row.tokens, newRevision, op, now); err != nil {
			return err
		}
	}
	return nil
}

func UpsertCurrentWindowScoresTx(ctx context.Context, tx *sql.Tx, userID string, now time.Time) error {
	return writeUserWindowScoresTx(ctx, tx, userID, now, scoreWriteAbsolute)
}

func BackfillWindowScores(ctx context.Context, db *sql.DB, now time.Time, limit int) (int, error) {
	if db == nil {
		return 0, nil
	}
	if limit <= 0 {
		limit = 100
	}
	generation := WindowGeneration(now)
	rows, err := db.QueryContext(ctx, `
		SELECT u.user_id
		FROM users u
		LEFT JOIN user_window_scores s
		  ON s.user_id = u.user_id AND s.window_key = 'today' AND s.generation = ?
		WHERE u.account_status = 'active'
		  AND (s.user_id IS NULL OR s.revision = 0)
		ORDER BY u.user_id
		LIMIT ?`, generation, limit)
	if err != nil {
		return 0, fmt.Errorf("list window score backfill targets: %w", err)
	}
	defer rows.Close()
	var userIDs []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return 0, fmt.Errorf("scan window score backfill target: %w", err)
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate window score backfill targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close window score backfill targets: %w", err)
	}
	processed := 0
	for _, userID := range userIDs {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return processed, fmt.Errorf("begin window score backfill: %w", err)
		}
		if err := writeUserWindowScoresTx(ctx, tx, userID, now, scoreWriteBackfill); err != nil {
			_ = tx.Rollback()
			return processed, err
		}
		if err := tx.Commit(); err != nil {
			return processed, fmt.Errorf("commit window score backfill: %w", err)
		}
		processed++
	}
	return processed, nil
}

func PruneOldWindowScoresTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	cutoff := now.UTC().AddDate(0, 0, -2).Format("2006-01-02")
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_window_scores WHERE generation < ?`, cutoff); err != nil {
		return fmt.Errorf("prune old window scores: %w", err)
	}
	return nil
}

func writeUserWindowScoresTx(ctx context.Context, tx *sql.Tx, userID string, now time.Time, mode int) error {
	var registeredAt time.Time
	var status domain.AccountStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT created_at, account_status
		FROM users
		WHERE user_id = ?`, userID).Scan(&registeredAt, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load user for window scores: %w", err)
	}
	eligible := status == domain.AccountStatusActive
	if mode == scoreWriteBackfill && !eligible {
		return nil
	}
	generation := WindowGeneration(now)
	for _, window := range leaderboardWindows {
		from, to, err := leaderboardDates(window, now)
		if err != nil {
			return err
		}
		var computedRaw sql.NullInt64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(exact_token_total + derived_token_total), 0)
			FROM daily_user_agent_metrics
			WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`,
			userID, from, to,
		).Scan(&computedRaw); err != nil {
			return fmt.Errorf("sum window tokens for %s: %w", window, err)
		}
		computed := uint64(0)
		if computedRaw.Valid && computedRaw.Int64 > 0 {
			computed = uint64(computedRaw.Int64)
		}
		var existingTokens, existingRevision uint64
		var existingEligible bool
		err = tx.QueryRowContext(ctx, `
			SELECT token_total, revision, eligible
			FROM user_window_scores
			WHERE user_id = ? AND window_key = ? AND generation = ?
			FOR UPDATE`, userID, window, generation,
		).Scan(&existingTokens, &existingRevision, &existingEligible)
		if errors.Is(err, sql.ErrNoRows) {
			revision := uint64(1)
			if mode == scoreWriteBackfill && computed == 0 {
				revision = 1
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO user_window_scores (
					user_id, window_key, generation, token_total, revision,
					eligible, registered_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				userID, window, generation, computed, revision, eligible, registeredAt, now,
			); err != nil {
				return fmt.Errorf("insert window score %s: %w", window, err)
			}
			op := rankingOutboxUpsert
			if !eligible {
				op = rankingOutboxRemove
			}
			if err := insertRankingOutboxTx(ctx, tx, userID, window, generation, computed, revision, op, now); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("lock window score %s: %w", window, err)
		}
		nextTokens := computed
		if mode == scoreWriteBackfill && existingTokens > 0 {
			nextTokens = existingTokens
		}
		if nextTokens == existingTokens && existingEligible == eligible && existingRevision > 0 {
			continue
		}
		newRevision := existingRevision + 1
		if existingRevision == 0 {
			newRevision = 1
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE user_window_scores
			SET token_total = ?, revision = ?, eligible = ?, updated_at = ?
			WHERE user_id = ? AND window_key = ? AND generation = ?`,
			nextTokens, newRevision, eligible, now, userID, window, generation,
		); err != nil {
			return fmt.Errorf("update window score %s: %w", window, err)
		}
		op := rankingOutboxUpsert
		if !eligible {
			op = rankingOutboxRemove
		}
		if err := insertRankingOutboxTx(ctx, tx, userID, window, generation, nextTokens, newRevision, op, now); err != nil {
			return err
		}
	}
	return nil
}

func insertRankingOutboxTx(ctx context.Context, tx *sql.Tx, userID, window, generation string, tokens, revision uint64, op string, now time.Time) error {
	taskID, err := newRankingOutboxID()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO ranking_outbox (
			task_id, user_id, window_key, generation, token_total, revision,
			op_type, task_status, next_attempt_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)
		ON DUPLICATE KEY UPDATE task_id = task_id`,
		taskID, userID, window, generation, tokens, revision, op, now, now, now,
	); err != nil {
		return fmt.Errorf("insert ranking outbox: %w", err)
	}
	return nil
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	buf := make([]byte, 0, count*2)
	for i := 0; i < count; i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, '?')
	}
	return string(buf)
}

func savedPreviousWindowExists(ctx context.Context, db *sql.DB, window, generation string) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM user_window_scores
			WHERE window_key = ? AND generation = ? AND eligible = TRUE
			LIMIT 1
		)`, window, generation).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check previous window scores: %w", err)
	}
	return exists == 1, nil
}
