package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
	"tokendance/internal/store"
)

const communityDaySumSQL = `
	SELECT
		CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED),
		COUNT(DISTINCT CASE WHEN exact_token_total + derived_token_total > 0 THEN user_id END),
		0,
		CAST(COALESCE(SUM(model_request_count), 0) AS UNSIGNED),
		0
	FROM telemetry_model_metrics
	WHERE grain = 'day'
	  AND delete_at IS NULL
	  AND bucket_start = ?`

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
	bucketStart, err := domain.DayBucketStartMs(date)
	if err != nil {
		return store.CommunityDailyTotals{}, err
	}
	var cost sql.NullFloat64
	if err := s.db.QueryRowContext(ctx, communityDaySumSQL, bucketStart).Scan(
		&totals.TokensTotal,
		&totals.Developers,
		&totals.CodeLines,
		&totals.Interactions,
		&cost,
	); err != nil {
		return store.CommunityDailyTotals{}, fmt.Errorf("sum community day %s: %w", date, err)
	}
	// Trusted code lines + cost from companion tables for the same day bucket.
	if err := s.db.QueryRowContext(ctx, `
		SELECT CAST(COALESCE(SUM(code_generated_lines), 0) AS UNSIGNED)
		FROM telemetry_harness_metrics
		WHERE grain = 'day' AND delete_at IS NULL AND bucket_start = ?`, bucketStart,
	).Scan(&totals.CodeLines); err != nil {
		return store.CommunityDailyTotals{}, fmt.Errorf("sum community code lines %s: %w", date, err)
	}
	var costUnits sql.NullString
	if err := s.db.QueryRowContext(ctx, `
		SELECT CAST(COALESCE(SUM(reported_cost_units + estimated_cost_units), 0) AS CHAR)
		FROM telemetry_cost_metrics
		WHERE grain = 'day' AND delete_at IS NULL AND bucket_start = ?`, bucketStart,
	).Scan(&costUnits); err != nil {
		return store.CommunityDailyTotals{}, fmt.Errorf("sum community cost %s: %w", date, err)
	}
	if costUnits.Valid && costUnits.String != "" && costUnits.String != "0" {
		var units float64
		if _, err := fmt.Sscanf(costUnits.String, "%f", &units); err == nil {
			totals.CostAmount = units / 1e8
		}
	}
	_ = cost
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

// ReplaceCommunityAgentDay swaps the day's per-harness totals in one go; the
// delete-then-insert keeps agents that dropped to zero from lingering.
func (s *communityStatsStore) ReplaceCommunityAgentDay(ctx context.Context, date string, rows []store.CommunityAgentTokens) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM community_agent_daily_stats WHERE metric_date = ?`, date); err != nil {
		return fmt.Errorf("clear community agent day %s: %w", date, err)
	}
	for _, row := range rows {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO community_agent_daily_stats (metric_date, agent_id, tokens_total, computed_at)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE tokens_total = VALUES(tokens_total)`,
			date, row.AgentID, row.TokensTotal, time.Now().UTC(),
		); err != nil {
			return fmt.Errorf("insert community agent day %s/%s: %w", date, row.AgentID, err)
		}
	}
	return nil
}

func (s *communityStatsStore) GetCommunityHarnessShares(ctx context.Context, date string, limit int) ([]store.CommunityHarness, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, tokens_total
		FROM community_agent_daily_stats
		WHERE metric_date = ?
		ORDER BY tokens_total DESC, agent_id ASC
		LIMIT ?`, date, limit)
	if err != nil {
		return nil, fmt.Errorf("list community harness shares %s: %w", date, err)
	}
	defer rows.Close()
	var harnesses []store.CommunityHarness
	for rows.Next() {
		var item store.CommunityHarness
		if err := rows.Scan(&item.AgentID, &item.TokensTotal); err != nil {
			return nil, fmt.Errorf("scan community harness share %s: %w", date, err)
		}
		item.Label = agentDisplayName(item.AgentID)
		harnesses = append(harnesses, item)
	}
	return harnesses, rows.Err()
}
