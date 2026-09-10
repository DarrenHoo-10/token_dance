package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/ranking"
	"tokendance/internal/store"
	mysqlstore "tokendance/internal/store/mysql"
)

// communityStatsRecomputeInterval coalesces bursts: a day is recomputed at
// most once per interval even when thousands of syncs land between passes, so
// the recompute cost stays independent of sync volume (see 1M DAU budget).
const communityStatsRecomputeInterval = time.Minute

const communityStatsClaimBatch = 50

const communityOutboxRetention = 48 * time.Hour

type communityStatsTask struct {
	taskID       string
	metricDate   string
	claimToken   string
	attemptCount uint16
}

func newCommunityStatsClaimToken() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate community stats claim token: %w", err)
	}
	return "cck_" + token, nil
}

// ProcessCommunityStatsOutbox consumes "day changed" signals: it recomputes
// the whole day from daily_user_agent_metrics once per coalescing window and
// overwrites the precomputed MySQL row plus the Redis hash. Request paths
// never aggregate usage tables themselves.
func (w *Worker) ProcessCommunityStatsOutbox(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	claimToken, err := newCommunityStatsClaimToken()
	if err != nil {
		return 0, err
	}
	leaseUntil := now.Add(time.Minute)
	res, err := w.db.ExecContext(ctx, `
		UPDATE community_stats_outbox
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
		LIMIT ?`,
		claimToken, w.workerID, leaseUntil, now, now, now, communityStatsClaimBatch,
	)
	if err != nil {
		return 0, fmt.Errorf("claim community stats outbox: %w", err)
	}
	if claimed, err := res.RowsAffected(); err != nil || claimed == 0 {
		return 0, nil
	}

	rows, err := w.db.QueryContext(ctx, `
		SELECT task_id, DATE_FORMAT(metric_date, '%Y-%m-%d'), claim_token, attempt_count
		FROM community_stats_outbox
		WHERE task_status = 'leased' AND locked_by = ? AND claim_token = ?
		ORDER BY created_at ASC`, w.workerID, claimToken)
	if err != nil {
		return 0, fmt.Errorf("list claimed community stats outbox: %w", err)
	}
	var tasks []communityStatsTask
	for rows.Next() {
		var task communityStatsTask
		if err := rows.Scan(&task.taskID, &task.metricDate, &task.claimToken, &task.attemptCount); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan community stats task: %w", err)
		}
		tasks = append(tasks, task)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate community stats tasks: %w", err)
	}

	processedDates := make(map[string]struct{}, len(tasks))
	processed := 0
	for _, task := range tasks {
		if _, done := processedDates[task.metricDate]; done {
			if err := w.ackCommunityStatsTask(ctx, task, now); err != nil {
				log.Printf("[Worker %s] ack community stats task error: %v", w.workerID, err)
				continue
			}
			processed++
			continue
		}
		fresh, err := w.communityDayIsFresh(ctx, task.metricDate, now)
		if err != nil {
			w.retryCommunityStatsTask(ctx, task, now)
			continue
		}
		if !fresh {
			if err := w.recomputeCommunityDay(ctx, task.metricDate, now, false); err != nil {
				w.retryCommunityStatsTask(ctx, task, now)
				continue
			}
		}
		processedDates[task.metricDate] = struct{}{}
		if err := w.ackCommunityStatsTask(ctx, task, now); err != nil {
			log.Printf("[Worker %s] ack community stats task error: %v", w.workerID, err)
			continue
		}
		processed++
	}
	return processed, nil
}

// communityDayIsFresh reports whether the day row was recomputed inside the
// coalescing window; skipping those recomputes caps the cost at one query per
// day per interval regardless of sync volume.
func (w *Worker) communityDayIsFresh(ctx context.Context, date string, now time.Time) (bool, error) {
	var computedAt sql.NullTime
	if err := w.db.QueryRowContext(ctx, `
		SELECT computed_at FROM community_daily_stats WHERE metric_date = ?`, date).Scan(&computedAt); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("check community day freshness %s: %w", date, err)
	}
	return computedAt.Valid && now.Sub(computedAt.Time.UTC()) < communityStatsRecomputeInterval, nil
}

// recomputeCommunityDay overwrites the precomputed stores for one metric date.
func (w *Worker) recomputeCommunityDay(ctx context.Context, date string, now time.Time, final bool) error {
	var tokens, developers, codeLines, interactions uint64
	var costAmount float64
	if err := w.db.QueryRowContext(ctx, `
		SELECT
			CAST(COALESCE(SUM(exact_token_total + derived_token_total + estimated_token_total), 0) AS UNSIGNED),
			COUNT(DISTINCT CASE WHEN exact_token_total + derived_token_total + estimated_token_total > 0 THEN user_id END),
			CAST(COALESCE(SUM(code_generated_lines), 0) AS UNSIGNED),
			CAST(COALESCE(SUM(model_request_count), 0) AS UNSIGNED),
			COALESCE(SUM(cost_amount), 0)
		FROM daily_user_agent_metrics
		WHERE metric_date = ?`, date).Scan(
		&tokens, &developers, &codeLines, &interactions, &costAmount,
	); err != nil {
		return fmt.Errorf("sum community day %s: %w", date, err)
	}
	if _, err := w.db.ExecContext(ctx, `
		INSERT INTO community_daily_stats (
			metric_date, tokens_total, developers, code_lines, interactions,
			cost_amount, is_final, computed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			tokens_total = VALUES(tokens_total),
			developers = VALUES(developers),
			code_lines = VALUES(code_lines),
			interactions = VALUES(interactions),
			cost_amount = VALUES(cost_amount),
			is_final = VALUES(is_final),
			computed_at = VALUES(computed_at)`,
		date, tokens, developers, codeLines, interactions, costAmount, final, now,
	); err != nil {
		return fmt.Errorf("store community day %s: %w", date, err)
	}
	if err := w.recomputeCommunityAgentDay(ctx, date); err != nil {
		return err
	}
	// MySQL first, Redis second: the row is the authoritative precomputed
	// copy, the hash is the hot read path. Acking only happens after both.
	return w.publishCommunityStats(ctx, ranking.CommunityStatsSnapshot{
		Date: date, Tokens: tokens, Developers: developers, CodeLines: codeLines,
		Interactions: interactions, CostAmount: costAmount, ComputedAt: now,
	})
}

// recomputeCommunityAgentDay refreshes the per-harness token share of a day.
func (w *Worker) recomputeCommunityAgentDay(ctx context.Context, date string) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT agent_id, CAST(COALESCE(SUM(exact_token_total + derived_token_total + estimated_token_total), 0) AS UNSIGNED)
		FROM daily_user_agent_metrics
		WHERE metric_date = ?
		GROUP BY agent_id`, date)
	if err != nil {
		return fmt.Errorf("group community agent day %s: %w", date, err)
	}
	var agentRows []store.CommunityAgentTokens
	for rows.Next() {
		var row store.CommunityAgentTokens
		if err := rows.Scan(&row.AgentID, &row.TokensTotal); err != nil {
			rows.Close()
			return fmt.Errorf("scan community agent day %s: %w", date, err)
		}
		agentRows = append(agentRows, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate community agent day %s: %w", date, err)
	}
	if err := w.communityStatsStore().ReplaceCommunityAgentDay(ctx, date, agentRows); err != nil {
		return fmt.Errorf("replace community agent day %s: %w", date, err)
	}
	return nil
}

// communityStatsStore wraps the raw worker connection with the shared
// precompute-store SQL so worker and API never duplicate day-total queries.
func (w *Worker) communityStatsStore() store.CommunityStatsStore {
	return mysqlstore.NewStore(w.db).CommunityStats()
}

func (w *Worker) publishCommunityStats(ctx context.Context, snapshot ranking.CommunityStatsSnapshot) error {
	if err := w.ranking.PublishCommunityStats(ctx, snapshot); err != nil {
		return fmt.Errorf("publish community stats %s: %w", snapshot.Date, err)
	}
	return nil
}

func (w *Worker) ackCommunityStatsTask(ctx context.Context, task communityStatsTask, now time.Time) error {
	_, err := w.db.ExecContext(ctx, `
		UPDATE community_stats_outbox
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
		return fmt.Errorf("ack community stats outbox: %w", err)
	}
	return nil
}

func (w *Worker) retryCommunityStatsTask(ctx context.Context, task communityStatsTask, now time.Time) {
	shift := task.attemptCount
	if shift > 5 {
		shift = 5
	}
	backoff := time.Duration(1<<shift) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	_, err := w.db.ExecContext(ctx, `
		UPDATE community_stats_outbox
		SET task_status = 'pending',
		    next_attempt_at = ?,
		    claim_token = NULL,
		    locked_by = NULL,
		    lease_expires_at = NULL,
		    updated_at = ?
		WHERE task_id = ? AND claim_token = ? AND locked_by = ? AND task_status = 'leased'`,
		now.Add(backoff), now, task.taskID, task.claimToken, w.workerID,
	)
	if err != nil {
		log.Printf("[Worker %s] retry community stats task error: %v", w.workerID, err)
	}
}

// ProcessCommunityStatsFinalize closes yesterday's row once per UTC day,
// backfills any missing rows from the last 30 days, and prunes applied
// outbox events. Recomputes here are batch precompute, not request work.
func (w *Worker) ProcessCommunityStatsFinalize(ctx context.Context) error {
	if w.db == nil {
		return nil
	}
	now := w.clk.Now()
	if sameStatsDay(w.lastStatsFinalized, now) {
		return nil
	}
	w.lastStatsFinalized = now

	yesterday := domain.DayDate(now.AddDate(0, 0, -1))
	if err := w.finalizeCommunityDay(ctx, yesterday, now); err != nil {
		return err
	}
	if err := w.backfillCommunityDays(ctx, now, 30); err != nil {
		return err
	}
	return w.pruneCommunityStatsOutbox(ctx, now)
}

func (w *Worker) finalizeCommunityDay(ctx context.Context, date string, now time.Time) error {
	if err := w.recomputeCommunityDay(ctx, date, now, true); err != nil {
		return fmt.Errorf("finalize community day %s: %w", date, err)
	}
	return nil
}

func (w *Worker) backfillCommunityDays(ctx context.Context, now time.Time, days int) error {
	rows, err := w.db.QueryContext(ctx, `
		SELECT metric_date FROM community_daily_stats
		WHERE metric_date >= ? AND metric_date < ?`,
		domain.DayDate(now.AddDate(0, 0, -days)), domain.DayDate(now))
	if err != nil {
		return fmt.Errorf("list community stats backfill gaps: %w", err)
	}
	present := map[string]struct{}{}
	for rows.Next() {
		var date string
		if err := rows.Scan(&date); err != nil {
			rows.Close()
			return fmt.Errorf("scan community stats backfill gap: %w", err)
		}
		present[date] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate community stats backfill gaps: %w", err)
	}
	for i := 1; i <= days; i++ {
		date := domain.DayDate(now.AddDate(0, 0, -i))
		if _, ok := present[date]; ok {
			continue
		}
		if err := w.recomputeCommunityDay(ctx, date, now, true); err != nil {
			return fmt.Errorf("backfill community day %s: %w", date, err)
		}
	}
	return nil
}

func (w *Worker) pruneCommunityStatsOutbox(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-communityOutboxRetention)
	for {
		res, err := w.db.ExecContext(ctx, `
			DELETE FROM community_stats_outbox
			WHERE task_status = 'applied' AND applied_at < ?
			LIMIT 5000`, cutoff)
		if err != nil {
			return fmt.Errorf("prune community stats outbox: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil || affected < 5000 {
			return nil
		}
	}
}

func sameStatsDay(a, b time.Time) bool {
	return domain.DayDate(a) == domain.DayDate(b)
}
