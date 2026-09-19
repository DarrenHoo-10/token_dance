package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
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
	FROM bound_telemetry_model_metrics
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

func (s *communityStatsStore) GetCommunityRollingStats(ctx context.Context, fromBucketMs, toBucketMs int64) (store.CommunityDailyTotals, []store.CommunityHarness, error) {
	var totals store.CommunityDailyTotals
	var updatedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED),
			COUNT(DISTINCT CASE WHEN exact_token_total + derived_token_total > 0 THEN user_id END),
			CAST(COALESCE(SUM(model_request_count), 0) AS UNSIGNED),
			MAX(updated_at)
		FROM bound_telemetry_model_metrics
		WHERE grain = 'hour' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?`, fromBucketMs, toBucketMs).Scan(
		&totals.TokensTotal,
		&totals.Developers,
		&totals.Interactions,
		&updatedAt,
	); err != nil {
		return store.CommunityDailyTotals{}, nil, fmt.Errorf("sum rolling community model metrics: %w", err)
	}
	if updatedAt.Valid {
		totals.ComputedAt = time.UnixMilli(updatedAt.Int64).UTC()
	}
	var harnessUpdatedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT CAST(COALESCE(SUM(code_generated_lines), 0) AS UNSIGNED), MAX(updated_at)
		FROM bound_telemetry_harness_metrics
		WHERE grain = 'hour' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?`, fromBucketMs, toBucketMs).Scan(&totals.CodeLines, &harnessUpdatedAt); err != nil {
		return store.CommunityDailyTotals{}, nil, fmt.Errorf("sum rolling community code lines: %w", err)
	}
	if harnessUpdatedAt.Valid && (!updatedAt.Valid || harnessUpdatedAt.Int64 > updatedAt.Int64) {
		totals.ComputedAt = time.UnixMilli(harnessUpdatedAt.Int64).UTC()
	}

	costRows, err := s.db.QueryContext(ctx, `
		SELECT currency, CAST(COALESCE(SUM(reported_cost_units + estimated_cost_units), 0) AS CHAR)
		FROM bound_telemetry_cost_metrics
		WHERE grain = 'hour' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?
		GROUP BY currency ORDER BY currency`, fromBucketMs, toBucketMs)
	if err != nil {
		return store.CommunityDailyTotals{}, nil, fmt.Errorf("sum rolling community costs: %w", err)
	}
	for costRows.Next() {
		var currency, units string
		if err := costRows.Scan(&currency, &units); err != nil {
			costRows.Close()
			return store.CommunityDailyTotals{}, nil, err
		}
		var amount float64
		fmt.Sscanf(units, "%f", &amount)
		totals.Costs = append(totals.Costs, store.CommunityCost{Currency: currency, Amount: amount / 1e8})
	}
	if err := costRows.Err(); err != nil {
		costRows.Close()
		return store.CommunityDailyTotals{}, nil, err
	}
	if err := costRows.Close(); err != nil {
		return store.CommunityDailyTotals{}, nil, err
	}
	applyCommunityCosts(&totals, totals.Costs)

	modelRows, err := s.db.QueryContext(ctx, `
		SELECT tm.model_id, CAST(COALESCE(SUM(m.exact_token_total + m.derived_token_total), 0) AS UNSIGNED)
		FROM bound_telemetry_model_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.grain = 'hour' AND m.delete_at IS NULL
		  AND m.bucket_start >= ? AND m.bucket_start <= ? AND tm.delete_at IS NULL
		GROUP BY tm.model_id
		ORDER BY SUM(m.exact_token_total + m.derived_token_total) DESC, tm.model_id ASC
		LIMIT ?`, fromBucketMs, toBucketMs, communitySharePersistLimit)
	if err != nil {
		return store.CommunityDailyTotals{}, nil, fmt.Errorf("sum rolling community model shares: %w", err)
	}
	for modelRows.Next() {
		var item store.CommunityModelShare
		if err := modelRows.Scan(&item.ModelID, &item.Tokens); err != nil {
			modelRows.Close()
			return store.CommunityDailyTotals{}, nil, err
		}
		item.Label = item.ModelID
		totals.ModelShares = append(totals.ModelShares, item)
	}
	if err := modelRows.Err(); err != nil {
		modelRows.Close()
		return store.CommunityDailyTotals{}, nil, err
	}
	if err := modelRows.Close(); err != nil {
		return store.CommunityDailyTotals{}, nil, err
	}

	skillRows, err := s.db.QueryContext(ctx, `
		SELECT sk.public_name, CAST(COALESCE(SUM(m.use_count), 0) AS UNSIGNED)
		FROM bound_telemetry_skill_metrics m
		JOIN telemetry_skills sk ON sk.id = m.skill_id
		WHERE m.grain = 'hour' AND m.delete_at IS NULL
		  AND m.bucket_start >= ? AND m.bucket_start <= ?
		  AND sk.delete_at IS NULL AND sk.public_name IS NOT NULL AND CHAR_LENGTH(sk.public_name) > 0
		GROUP BY sk.public_name
		ORDER BY SUM(m.use_count) DESC, sk.public_name ASC
		LIMIT ?`, fromBucketMs, toBucketMs, communitySharePersistLimit)
	if err != nil {
		return store.CommunityDailyTotals{}, nil, fmt.Errorf("sum rolling community skill shares: %w", err)
	}
	for skillRows.Next() {
		var item store.CommunitySkillShare
		if err := skillRows.Scan(&item.Label, &item.Uses); err != nil {
			skillRows.Close()
			return store.CommunityDailyTotals{}, nil, err
		}
		item.SkillID = item.Label
		totals.SkillShares = append(totals.SkillShares, item)
	}
	if err := skillRows.Err(); err != nil {
		skillRows.Close()
		return store.CommunityDailyTotals{}, nil, err
	}
	if err := skillRows.Close(); err != nil {
		return store.CommunityDailyTotals{}, nil, err
	}

	harnessRows, err := s.db.QueryContext(ctx, `
		SELECT harness_id, CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED)
		FROM bound_telemetry_model_metrics
		WHERE grain = 'hour' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?
		GROUP BY harness_id
		ORDER BY SUM(exact_token_total + derived_token_total) DESC, harness_id ASC
		LIMIT 5`, fromBucketMs, toBucketMs)
	if err != nil {
		return store.CommunityDailyTotals{}, nil, fmt.Errorf("sum rolling community harness shares: %w", err)
	}
	var harnesses []store.CommunityHarness
	for harnessRows.Next() {
		var item store.CommunityHarness
		if err := harnessRows.Scan(&item.AgentID, &item.TokensTotal); err != nil {
			harnessRows.Close()
			return store.CommunityDailyTotals{}, nil, err
		}
		item.Label = agentDisplayName(item.AgentID)
		harnesses = append(harnesses, item)
	}
	if err := harnessRows.Err(); err != nil {
		harnessRows.Close()
		return store.CommunityDailyTotals{}, nil, err
	}
	if err := harnessRows.Close(); err != nil {
		return store.CommunityDailyTotals{}, nil, err
	}
	return totals, harnesses, nil
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
		FROM bound_telemetry_harness_metrics
		WHERE grain = 'day' AND delete_at IS NULL AND bucket_start = ?`, bucketStart,
	).Scan(&totals.CodeLines); err != nil {
		return store.CommunityDailyTotals{}, fmt.Errorf("sum community code lines %s: %w", date, err)
	}
	costs, err := queryCommunityCostsByCurrency(ctx, s.db, bucketStart)
	if err != nil {
		return store.CommunityDailyTotals{}, fmt.Errorf("sum community cost %s: %w", date, err)
	}
	applyCommunityCosts(&totals, costs)
	_ = cost
	totals.ComputedAt = time.Now().UTC()
	return totals, nil
}

func queryCommunityCostsByCurrency(ctx context.Context, db *sql.DB, bucketStart int64) ([]store.CommunityCost, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT
			currency,
			CAST(COALESCE(SUM(reported_cost_units + estimated_cost_units), 0) AS CHAR)
		FROM bound_telemetry_cost_metrics
		WHERE grain = 'day' AND delete_at IS NULL AND bucket_start = ?
		GROUP BY currency
		ORDER BY currency`, bucketStart)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var costs []store.CommunityCost
	for rows.Next() {
		var currency string
		var costUnits sql.NullString
		if err := rows.Scan(&currency, &costUnits); err != nil {
			return nil, err
		}
		var amount float64
		if costUnits.Valid && costUnits.String != "" {
			fmt.Sscanf(costUnits.String, "%f", &amount)
		}
		costs = append(costs, store.CommunityCost{
			Currency: currency,
			Amount:   amount / 1e8,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return costs, nil
}

// applyCommunityCosts fills Costs and the legacy scalar. Multiple currencies
// cannot be FX-merged: CostAmount stays 0 and callers must omit the scalar.
func applyCommunityCosts(totals *store.CommunityDailyTotals, costs []store.CommunityCost) {
	totals.Costs = costs
	if len(costs) == 1 {
		totals.CostAmount = costs[0].Amount
		return
	}
	totals.CostAmount = 0
}

func marshalCommunityJSON[T any](items []T) ([]byte, error) {
	if items == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(items)
}

func decodeCommunityJSON[T any](raw sql.NullString) ([]T, error) {
	if !raw.Valid || raw.String == "" || raw.String == "null" {
		return nil, nil
	}
	var items []T
	if err := json.Unmarshal([]byte(raw.String), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *communityStatsStore) UpsertCommunityDailyStats(ctx context.Context, totals store.CommunityDailyTotals) error {
	costJSON, err := marshalCommunityJSON(totals.Costs)
	if err != nil {
		return fmt.Errorf("marshal community costs %s: %w", totals.MetricDate, err)
	}
	modelJSON, err := marshalCommunityJSON(totals.ModelShares)
	if err != nil {
		return fmt.Errorf("marshal community model shares %s: %w", totals.MetricDate, err)
	}
	skillJSON, err := marshalCommunityJSON(totals.SkillShares)
	if err != nil {
		return fmt.Errorf("marshal community skill shares %s: %w", totals.MetricDate, err)
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO community_daily_stats (
			metric_date, tokens_total, developers, code_lines, interactions,
			cost_amount, cost_amounts, model_shares, skill_shares, is_final, computed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			tokens_total = VALUES(tokens_total),
			developers = VALUES(developers),
			code_lines = VALUES(code_lines),
			interactions = VALUES(interactions),
			cost_amount = VALUES(cost_amount),
			cost_amounts = VALUES(cost_amounts),
			model_shares = VALUES(model_shares),
			skill_shares = VALUES(skill_shares),
			is_final = VALUES(is_final),
			computed_at = VALUES(computed_at)`,
		totals.MetricDate, totals.TokensTotal, totals.Developers, totals.CodeLines,
		totals.Interactions, totals.CostAmount, costJSON, modelJSON, skillJSON, totals.IsFinal, totals.ComputedAt,
	); err != nil {
		return fmt.Errorf("upsert community daily stats %s: %w", totals.MetricDate, err)
	}
	return nil
}

func (s *communityStatsStore) GetCommunityDailyStats(ctx context.Context, date string) (*store.CommunityDailyTotals, error) {
	var totals store.CommunityDailyTotals
	var isFinal bool
	var costJSON, modelJSON, skillJSON sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT metric_date, tokens_total, developers, code_lines, interactions,
		       cost_amount, cost_amounts, model_shares, skill_shares, is_final, computed_at
		FROM community_daily_stats
		WHERE metric_date = ?`, date).Scan(
		&totals.MetricDate,
		&totals.TokensTotal,
		&totals.Developers,
		&totals.CodeLines,
		&totals.Interactions,
		&totals.CostAmount,
		&costJSON,
		&modelJSON,
		&skillJSON,
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
	if totals.Costs, err = decodeCommunityJSON[store.CommunityCost](costJSON); err != nil {
		return nil, fmt.Errorf("decode community costs %s: %w", date, err)
	}
	if totals.ModelShares, err = decodeCommunityJSON[store.CommunityModelShare](modelJSON); err != nil {
		return nil, fmt.Errorf("decode community model shares %s: %w", date, err)
	}
	if totals.SkillShares, err = decodeCommunityJSON[store.CommunitySkillShare](skillJSON); err != nil {
		return nil, fmt.Errorf("decode community skill shares %s: %w", date, err)
	}
	return &totals, nil
}

func (s *communityStatsStore) ListCommunityDailyStats(ctx context.Context, fromDate, toDate string) ([]store.CommunityDailyTotals, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT metric_date, tokens_total, developers, code_lines, interactions,
		       cost_amount, cost_amounts, model_shares, skill_shares, is_final, computed_at
		FROM community_daily_stats
		WHERE metric_date >= ? AND metric_date <= ?
		ORDER BY metric_date`, fromDate, toDate)
	if err != nil {
		return nil, fmt.Errorf("list community daily stats %s..%s: %w", fromDate, toDate, err)
	}
	defer rows.Close()
	var result []store.CommunityDailyTotals
	for rows.Next() {
		var item store.CommunityDailyTotals
		var costJSON, modelJSON, skillJSON sql.NullString
		if err := rows.Scan(&item.MetricDate, &item.TokensTotal, &item.Developers, &item.CodeLines,
			&item.Interactions, &item.CostAmount, &costJSON, &modelJSON, &skillJSON, &item.IsFinal, &item.ComputedAt); err != nil {
			return nil, fmt.Errorf("scan community daily stats: %w", err)
		}
		if item.Costs, err = decodeCommunityJSON[store.CommunityCost](costJSON); err != nil {
			return nil, fmt.Errorf("decode community costs %s: %w", item.MetricDate, err)
		}
		if item.ModelShares, err = decodeCommunityJSON[store.CommunityModelShare](modelJSON); err != nil {
			return nil, fmt.Errorf("decode community model shares %s: %w", item.MetricDate, err)
		}
		if item.SkillShares, err = decodeCommunityJSON[store.CommunitySkillShare](skillJSON); err != nil {
			return nil, fmt.Errorf("decode community skill shares %s: %w", item.MetricDate, err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *communityStatsStore) GetCommunityActiveDevelopers(ctx context.Context, window, generation, _, _ string) (uint64, error) {
	var count uint64
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM user_window_scores
		WHERE window_key = ? AND generation = ? AND eligible = TRUE AND token_total > 0`,
		window, generation).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active community developers %s: %w", window, err)
	}
	return count, nil
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

const communitySharePersistLimit = 50

func (s *communityStatsStore) SumCommunityModelShares(ctx context.Context, date string) ([]store.CommunityModelShare, error) {
	bucketStart, err := domain.DayBucketStartMs(date)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT tm.model_id, CAST(COALESCE(SUM(m.exact_token_total + m.derived_token_total), 0) AS UNSIGNED)
		FROM bound_telemetry_model_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.grain = 'day' AND m.delete_at IS NULL AND m.bucket_start = ?
		  AND (tm.delete_at IS NULL)
		GROUP BY tm.model_id
		ORDER BY SUM(m.exact_token_total + m.derived_token_total) DESC, tm.model_id ASC
		LIMIT ?`, bucketStart, communitySharePersistLimit)
	if err != nil {
		return nil, fmt.Errorf("sum community model shares %s: %w", date, err)
	}
	defer rows.Close()
	var shares []store.CommunityModelShare
	for rows.Next() {
		var item store.CommunityModelShare
		if err := rows.Scan(&item.ModelID, &item.Tokens); err != nil {
			return nil, fmt.Errorf("scan community model share %s: %w", date, err)
		}
		item.Label = item.ModelID
		shares = append(shares, item)
	}
	return shares, rows.Err()
}

func (s *communityStatsStore) SumCommunitySkillShares(ctx context.Context, date string) ([]store.CommunitySkillShare, error) {
	bucketStart, err := domain.DayBucketStartMs(date)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.public_name, CAST(COALESCE(SUM(m.use_count), 0) AS UNSIGNED)
		FROM bound_telemetry_skill_metrics m
		JOIN telemetry_skills s ON s.id = m.skill_id
		WHERE m.grain = 'day' AND m.delete_at IS NULL AND m.bucket_start = ?
		  AND s.delete_at IS NULL
		  AND s.public_name IS NOT NULL AND CHAR_LENGTH(s.public_name) > 0
		GROUP BY s.public_name
		ORDER BY SUM(m.use_count) DESC, s.public_name ASC
		LIMIT ?`, bucketStart, communitySharePersistLimit)
	if err != nil {
		return nil, fmt.Errorf("sum community skill shares %s: %w", date, err)
	}
	defer rows.Close()
	var shares []store.CommunitySkillShare
	for rows.Next() {
		var item store.CommunitySkillShare
		if err := rows.Scan(&item.Label, &item.Uses); err != nil {
			return nil, fmt.Errorf("scan community skill share %s: %w", date, err)
		}
		item.SkillID = item.Label
		shares = append(shares, item)
	}
	return shares, rows.Err()
}

func (s *communityStatsStore) GetCommunityHarnessShares(ctx context.Context, date string, limit int) ([]store.CommunityHarness, error) {
	return s.GetCommunityHarnessSharesRange(ctx, date, date, limit)
}

func (s *communityStatsStore) GetCommunityHarnessSharesRange(ctx context.Context, fromDate, toDate string, limit int) ([]store.CommunityHarness, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT agent_id, CAST(SUM(tokens_total) AS UNSIGNED)
		FROM community_agent_daily_stats
		WHERE metric_date >= ? AND metric_date <= ?
		GROUP BY agent_id
		ORDER BY SUM(tokens_total) DESC, agent_id ASC
		LIMIT ?`, fromDate, toDate, limit)
	if err != nil {
		return nil, fmt.Errorf("list community harness shares %s..%s: %w", fromDate, toDate, err)
	}
	defer rows.Close()
	var harnesses []store.CommunityHarness
	for rows.Next() {
		var item store.CommunityHarness
		if err := rows.Scan(&item.AgentID, &item.TokensTotal); err != nil {
			return nil, fmt.Errorf("scan community harness share %s..%s: %w", fromDate, toDate, err)
		}
		item.Label = agentDisplayName(item.AgentID)
		harnesses = append(harnesses, item)
	}
	return harnesses, rows.Err()
}
