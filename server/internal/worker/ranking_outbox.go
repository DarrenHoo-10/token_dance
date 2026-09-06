package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/ranking"
)

type rankingOutboxTask struct {
	taskID       string
	userID       string
	window       string
	generation   string
	tokens       uint64
	revision     uint64
	op           string
	claimToken   string
	registeredAt time.Time
	attemptCount uint16
}

func (w *Worker) processRankingOutbox(ctx context.Context) (int, error) {
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	claimToken, err := newRankingClaimToken()
	if err != nil {
		return 0, err
	}
	leaseUntil := now.Add(time.Minute)
	res, err := w.db.ExecContext(ctx, `
		UPDATE ranking_outbox
		SET task_status = 'leased',
		    claim_token = ?,
		    locked_by = ?,
		    lease_expires_at = ?,
		    attempt_count = attempt_count + 1,
		    last_error_code = NULL,
		    updated_at = ?
		WHERE (
		    (task_status = 'pending' AND next_attempt_at <= ?)
		    OR
		    (task_status = 'leased' AND lease_expires_at <= ?)
		)
		ORDER BY created_at ASC
		LIMIT 25`,
		claimToken, w.workerID, leaseUntil, now, now, now,
	)
	if err != nil {
		return 0, fmt.Errorf("claim ranking outbox: %w", err)
	}
	claimed, err := res.RowsAffected()
	if err != nil || claimed == 0 {
		return 0, nil
	}

	rows, err := w.db.QueryContext(ctx, `
		SELECT o.task_id, o.user_id, o.window_key, o.generation, o.token_total, o.revision, o.op_type, o.claim_token,
		       COALESCE(s.registered_at, u.created_at), o.attempt_count
		FROM ranking_outbox o
		INNER JOIN users u ON u.user_id = o.user_id
		LEFT JOIN user_window_scores s
		  ON s.user_id = o.user_id AND s.window_key = o.window_key AND s.generation = o.generation
		WHERE o.task_status = 'leased' AND o.locked_by = ? AND o.claim_token = ?
		ORDER BY o.created_at ASC`, w.workerID, claimToken)
	if err != nil {
		return 0, fmt.Errorf("list claimed ranking outbox: %w", err)
	}
	defer rows.Close()

	var tasks []rankingOutboxTask
	for rows.Next() {
		var task rankingOutboxTask
		if err := rows.Scan(
			&task.taskID, &task.userID, &task.window, &task.generation, &task.tokens, &task.revision, &task.op, &task.claimToken, &task.registeredAt, &task.attemptCount,
		); err != nil {
			return 0, fmt.Errorf("scan ranking outbox: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate ranking outbox: %w", err)
	}
	_ = rows.Close()

	processed := 0
	promote := map[string]string{}
	for _, task := range tasks {
		if err := w.applyRankingTask(ctx, task, now); err != nil {
			if isPermanentRankingApplyError(err) {
				_ = w.failRankingTask(ctx, task, now, err)
			} else {
				_ = w.retryRankingTask(ctx, task, now, err)
			}
			continue
		}
		if err := w.ackRankingTask(ctx, task, now); err != nil {
			continue
		}
		processed++
		promote[task.window] = task.generation
	}
	for window, generation := range promote {
		if err := w.maybePromoteRankingGeneration(ctx, window, generation); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

func (w *Worker) applyRankingTask(ctx context.Context, task rankingOutboxTask, now time.Time) error {
	result, err := w.ranking.Apply(ctx, ranking.ApplyInput{
		Window:       task.window,
		Generation:   task.generation,
		UserID:       task.userID,
		Tokens:       task.tokens,
		Revision:     task.revision,
		RegisteredAt: task.registeredAt,
		Op:           task.op,
		Now:          now,
	})
	if err != nil {
		return err
	}
	switch result.Status {
	case ranking.StatusApplied, ranking.StatusDuplicate, ranking.StatusStale, ranking.StatusSkippedGeneration:
		return nil
	default:
		return fmt.Errorf("unexpected ranking apply status %q", result.Status)
	}
}

func (w *Worker) ackRankingTask(ctx context.Context, task rankingOutboxTask, now time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE ranking_outbox
		SET task_status = 'applied',
		    applied_at = ?,
		    claim_token = NULL,
		    locked_by = NULL,
		    lease_expires_at = NULL,
		    last_error_code = NULL,
		    updated_at = ?
		WHERE task_id = ? AND claim_token = ? AND locked_by = ? AND task_status = 'leased'`,
		now, now, task.taskID, task.claimToken, w.workerID,
	)
	if err != nil {
		return fmt.Errorf("ack ranking outbox: %w", err)
	}
	return nil
}

func (w *Worker) retryRankingTask(ctx context.Context, task rankingOutboxTask, now time.Time, applyErr error) error {
	shift := task.attemptCount
	if shift > 5 {
		shift = 5
	}
	backoff := time.Duration(1<<shift) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	_, err := w.db.ExecContext(ctx, `
		UPDATE ranking_outbox
		SET task_status = 'pending',
		    next_attempt_at = ?,
		    last_error_code = ?,
		    claim_token = NULL,
		    locked_by = NULL,
		    lease_expires_at = NULL,
		    updated_at = ?
		WHERE task_id = ? AND claim_token = ? AND locked_by = ? AND task_status = 'leased'`,
		now.Add(backoff), "REDIS_APPLY_FAILED", now, task.taskID, task.claimToken, w.workerID,
	)
	if err != nil {
		return err
	}
	return applyErr
}

func (w *Worker) failRankingTask(ctx context.Context, task rankingOutboxTask, now time.Time, applyErr error) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE ranking_outbox
		SET task_status = 'failed',
		    last_error_code = ?,
		    claim_token = NULL,
		    locked_by = NULL,
		    lease_expires_at = NULL,
		    updated_at = ?
		WHERE task_id = ? AND claim_token = ? AND locked_by = ? AND task_status = 'leased'`,
		"INVALID_MEMBER", now, task.taskID, task.claimToken, w.workerID,
	)
	if err != nil {
		return err
	}
	return applyErr
}

func isPermanentRankingApplyError(err error) bool {
	return errors.Is(err, ranking.ErrInvalidUserID) ||
		errors.Is(err, ranking.ErrInvalidMember) ||
		errors.Is(err, ranking.ErrNegativeToken) ||
		errors.Is(err, ranking.ErrTokenOverflow) ||
		errors.Is(err, ranking.ErrInvalidRegisteredAt) ||
		errors.Is(err, ranking.ErrInvalidWindow)
}

func (w *Worker) maybePromoteRankingGeneration(ctx context.Context, window, generation string) error {
	current, err := w.ranking.CurrentGeneration(ctx, window)
	if err != nil {
		return err
	}
	if current == "" || generation <= current {
		return nil
	}
	var mysqlCount int
	if err := w.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM user_window_scores
		WHERE window_key = ? AND generation = ? AND eligible = TRUE`,
		window, generation,
	).Scan(&mysqlCount); err != nil {
		return fmt.Errorf("count window scores for promote: %w", err)
	}
	n, err := w.ranking.Cardinality(ctx, window, generation)
	if err != nil {
		return err
	}
	if mysqlCount == 0 || n < int64(mysqlCount) {
		return nil
	}
	_, err = w.ranking.PromoteGeneration(ctx, window, generation)
	return err
}

func newRankingClaimToken() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate ranking claim token: %w", err)
	}
	return "rck_" + token, nil
}
