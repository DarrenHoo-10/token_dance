package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/store"
)

const communityDaySumSQL = `
	SELECT
		CAST(COALESCE(SUM(exact_token_total + derived_token_total + estimated_token_total), 0) AS UNSIGNED),
		COUNT(DISTINCT CASE WHEN exact_token_total + derived_token_total + estimated_token_total > 0 THEN user_id END),
		CAST(COALESCE(SUM(code_generated_lines), 0) AS UNSIGNED),
		CAST(COALESCE(SUM(interaction_turn_count), 0) AS UNSIGNED),
		COALESCE(SUM(cost_amount), 0)
	FROM daily_user_agent_metrics
	WHERE metric_date = ?`

// EnqueueCommunityStatsOutboxTx marks metric days as dirty inside the same
// transaction that rebuilt their daily aggregates. Events carry only the date;
// the stats worker coalesces them into one whole-day recompute.
func EnqueueCommunityStatsOutboxTx(ctx context.Context, tx *sql.Tx, dates []string, now time.Time) error {
	for _, date := range dates {
		taskID, err := newCommunityStatsTaskID()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO community_stats_outbox (
				task_id, metric_date, task_status, next_attempt_at, created_at, updated_at
			) VALUES (?, ?, 'pending', ?, ?, ?)`,
			taskID, date, now, now, now,
		); err != nil {
			return fmt.Errorf("insert community stats outbox: %w", err)
		}
	}
	return nil
}

func newCommunityStatsTaskID() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate community stats task id: %w", err)
	}
	return "cst_" + token, nil
}

func (s *Store) CommunityStats() store.CommunityStatsStore {
	return &communityStatsStore{db: s.db}
}

type communityStatsStore struct {
	db *sql.DB
}

func (s *communityStatsStore) SumCommunityDay(ctx context.Context, date string) (store.CommunityDailyTotals, error) {
	totals := store.CommunityDailyTotals{MetricDate: date}
	var cost sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, communityDaySumSQL, date).Scan(
		&totals.TokensTotal,
		&totals.Developers,
		&totals.CodeLines,
		&totals.Interactions,
		&cost,
	); err != nil {
		return store.CommunityDailyTotals{}, fmt.Errorf("sum community day %s: %w", date, err)
	}
	totals.CostAmount = cost.Float64
	totals.ComputedAt = time.Now().UTC()
	return totals, nil
}

func (s *communityStatsStore) UpsertCommunityDailyStats(ctx context.Context, totals store.CommunityDailyTotals) error {
	if _, err := s.db.ExecContext(ctx, `
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
		totals.MetricDate, totals.TokensTotal, totals.Developers, totals.CodeLines,
		totals.Interactions, totals.CostAmount, totals.IsFinal, totals.ComputedAt,
	); err != nil {
		return fmt.Errorf("upsert community daily stats %s: %w", totals.MetricDate, err)
	}
	return nil
}

func (s *communityStatsStore) GetCommunityDailyStats(ctx context.Context, date string) (*store.CommunityDailyTotals, error) {
	var totals store.CommunityDailyTotals
	var isFinal bool
	err := s.db.QueryRowContext(ctx, `
		SELECT metric_date, tokens_total, developers, code_lines, interactions,
		       cost_amount, is_final, computed_at
		FROM community_daily_stats
		WHERE metric_date = ?`, date).Scan(
		&totals.MetricDate,
		&totals.TokensTotal,
		&totals.Developers,
		&totals.CodeLines,
		&totals.Interactions,
		&totals.CostAmount,
		&isFinal,
		&totals.ComputedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get community daily stats %s: %w", date, err)
	}
	totals.IsFinal = isFinal
	return &totals, nil
}
