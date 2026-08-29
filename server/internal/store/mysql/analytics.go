package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"tokendance/internal/domain"
)

type analyticsStore struct {
	db *sql.DB
}

func rangeDateStrings(r domain.TimeRange) (string, string, *time.Location) {
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		loc = time.UTC
	}
	return r.From.In(loc).Format("2006-01-02"), r.To.In(loc).Format("2006-01-02"), loc
}

func agentDisplayName(id string) string {
	switch id {
	case "claude-code":
		return "Claude Code"
	case "cursor":
		return "Cursor"
	case "codex":
		return "Codex CLI"
	default:
		parts := strings.Split(id, "-")
		for i, p := range parts {
			if len(p) > 0 {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
		return strings.Join(parts, " ")
	}
}

func (s *analyticsStore) GetPersonalSummary(ctx context.Context, userID string, r domain.TimeRange) (*domain.PersonalSummary, error) {
	uAuth := &authStore{db: s.db}
	u, err := uAuth.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			COUNT(*) AS row_count,
			COALESCE(SUM(cost_amount), 0) AS cost_amount,
			COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			COALESCE(SUM(code_generated_lines), 0) AS code_generated_lines,
			SUM(token_input_total) AS token_input_total,
			SUM(token_output_total) AS token_output_total,
			SUM(token_cache_read_total) AS token_cache_read_total,
			SUM(token_cache_write_total) AS token_cache_write_total,
			SUM(token_reasoning_total) AS token_reasoning_total,
			SUM(active_duration_ms) AS active_duration_ms,
			SUM(message_count) AS message_count,
			SUM(user_message_count) AS user_message_count,
			MIN(aggregation_version) AS min_agg_ver,
			MAX(aggregation_version) AS max_agg_ver,
			MAX(computed_at) AS max_computed_at
		FROM daily_user_agent_metrics
		WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`

	fromStr, toStr, _ := rangeDateStrings(r)

	var rowCount int
	var costAmount float64
	var totalTokens, codeLines uint64
	var inputTokensNull, outputTokensNull, cacheReadNull, cacheWriteNull, reasoningNull sql.NullInt64
	var activeDurationNull, messageCountNull, userMsgNull sql.NullInt64
	var minAggVerNull, maxAggVerNull sql.NullInt64
	var maxComputedAtNull sql.NullTime

	err = s.db.QueryRowContext(ctx, query, userID, fromStr, toStr).Scan(
		&rowCount,
		&costAmount,
		&totalTokens,
		&codeLines,
		&inputTokensNull,
		&outputTokensNull,
		&cacheReadNull,
		&cacheWriteNull,
		&reasoningNull,
		&activeDurationNull,
		&messageCountNull,
		&userMsgNull,
		&minAggVerNull,
		&maxAggVerNull,
		&maxComputedAtNull,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query personal summary: %w", err)
	}

	costAmtStr := fmt.Sprintf("%.8f", costAmount)
	costCurr := "USD"
	totTokensStr := fmt.Sprintf("%d", totalTokens)
	codeLinesStr := fmt.Sprintf("%d", codeLines)

	var tokensPerCodeLineStr *string
	if codeLines > 0 {
		str := fmt.Sprintf("%.2f", float64(totalTokens)/float64(codeLines))
		tokensPerCodeLineStr = &str
	}

	aggVer := uint32(2)
	if maxAggVerNull.Valid && maxAggVerNull.Int64 > 0 {
		aggVer = uint32(maxAggVerNull.Int64)
	}

	var inputMetric domain.MetricBigInt
	var outputMetric domain.MetricBigInt
	var cacheHitMetric domain.MetricDecimal
	var durationMetric domain.MetricBigInt
	var messageMetric domain.MetricBigInt
	var userMessageMetric domain.MetricBigInt

	if rowCount == 0 {
		zeroStr := "0"
		inputMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		outputMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		cacheHitMetric = domain.MetricDecimal{Value: nil, Supported: true}
		durationMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		messageMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		userMessageMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
	} else {
		extSupported := minAggVerNull.Valid && minAggVerNull.Int64 >= 2

		if extSupported && inputTokensNull.Valid && cacheReadNull.Valid {
			inpVal := uint64(inputTokensNull.Int64) + uint64(cacheReadNull.Int64)
			sVal := fmt.Sprintf("%d", inpVal)
			inputMetric = domain.MetricBigInt{Value: &sVal, Supported: true}

			denom := inpVal
			if denom > 0 {
				rateStr := fmt.Sprintf("%.3f", float64(cacheReadNull.Int64)/float64(denom))
				cacheHitMetric = domain.MetricDecimal{Value: &rateStr, Supported: true}
			} else {
				cacheHitMetric = domain.MetricDecimal{Value: nil, Supported: true}
			}
		} else {
			inputMetric = domain.MetricBigInt{Value: nil, Supported: false}
			cacheHitMetric = domain.MetricDecimal{Value: nil, Supported: false}
		}

		if extSupported && outputTokensNull.Valid {
			sVal := fmt.Sprintf("%d", outputTokensNull.Int64)
			outputMetric = domain.MetricBigInt{Value: &sVal, Supported: true}
		} else {
			outputMetric = domain.MetricBigInt{Value: nil, Supported: false}
		}

		if extSupported && activeDurationNull.Valid {
			sVal := fmt.Sprintf("%d", activeDurationNull.Int64)
			durationMetric = domain.MetricBigInt{Value: &sVal, Supported: true}
		} else {
			durationMetric = domain.MetricBigInt{Value: nil, Supported: false}
		}

		if extSupported && messageCountNull.Valid {
			sVal := fmt.Sprintf("%d", messageCountNull.Int64)
			messageMetric = domain.MetricBigInt{Value: &sVal, Supported: true}
		} else {
			messageMetric = domain.MetricBigInt{Value: nil, Supported: false}
		}

		if extSupported && userMsgNull.Valid {
			sVal := fmt.Sprintf("%d", userMsgNull.Int64)
			userMessageMetric = domain.MetricBigInt{Value: &sVal, Supported: true}
		} else {
			userMessageMetric = domain.MetricBigInt{Value: nil, Supported: false}
		}
	}

	var rank *int
	var delta *int
	var percentile *float64
	if u.LeaderboardVisibility == domain.LeaderboardVisibilityPublic {
		snapQuery := `
			SELECT e.rank_no, e.previous_rank_no, s.participant_count
			FROM leaderboard_entries e
			JOIN leaderboard_snapshots s ON e.snapshot_id = s.snapshot_id
			JOIN public_user_profiles p ON e.user_id = p.user_id
			JOIN users usr ON usr.user_id = e.user_id
			JOIN user_privacy_settings priv ON priv.user_id = e.user_id
			WHERE e.user_id = ?
			  AND s.board_key = 'global'
			  AND s.snapshot_status = 'published'
			  AND p.profile_status = 'published'
			  AND usr.account_status = 'active'
			  AND usr.leaderboard_visibility = 'public'
			  AND priv.public_profile_enabled = TRUE
			ORDER BY s.published_at DESC
			LIMIT 1`

		var rNo int
		var prevRNo sql.NullInt32
		var pCount int
		if err := s.db.QueryRowContext(ctx, snapQuery, userID).Scan(&rNo, &prevRNo, &pCount); err == nil {
			rVal := rNo
			rank = &rVal
			if prevRNo.Valid {
				dVal := int(prevRNo.Int32 - int32(rNo))
				delta = &dVal
			}
			if pCount > 0 {
				pVal := math.Round((1.0-(float64(rNo-1)/float64(pCount)))*1000.0) / 10.0
				percentile = &pVal
			}
		}
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	return &domain.PersonalSummary{
		Range: r,
		Metrics: domain.PersonalSummaryMetrics{
			EstimatedCost:      domain.MetricCost{Amount: &costAmtStr, Currency: &costCurr, Supported: true},
			TotalTokens:        domain.MetricBigInt{Value: &totTokensStr, Supported: true},
			GeneratedCodeLines: domain.MetricBigInt{Value: &codeLinesStr, Supported: true},
			TokensPerCodeLine:  domain.MetricDecimal{Value: tokensPerCodeLineStr, Supported: true},
			InputContextTokens: inputMetric,
			OutputTokens:       outputMetric,
			CacheHitRate:       cacheHitMetric,
			ActiveDurationMs:   durationMetric,
			MessageCount:       messageMetric,
			UserMessageCount:   userMessageMetric,
		},
		Ranking: domain.PersonalSummaryRanking{
			Visibility: u.LeaderboardVisibility,
			Rank:       rank,
			Delta:      delta,
			Percentile: percentile,
		},
		Sync: domain.PersonalSummarySync{
			LastCommittedAt:   dataWatermarkAt,
			PendingLocalCount: nil,
		},
		DataWatermarkAt:    dataWatermarkAt,
		AggregationVersion: aggVer,
	}, nil
}

func (s *analyticsStore) GetTokenTrend(ctx context.Context, userID string, r domain.TimeRange, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error) {
	fromStr, toStr, _ := rangeDateStrings(r)

	hasModelFilter := (providerID != nil && *providerID != "" && *providerID != "all") || (modelID != nil && *modelID != "" && *modelID != "all")

	var query string
	var args []interface{}
	args = append(args, userID, fromStr, toStr)

	if hasModelFilter {
		query = `
			SELECT metric_date,
			       COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			       SUM(token_input_total) AS input_tokens,
			       SUM(token_output_total) AS output_tokens,
			       SUM(token_cache_read_total) AS cache_read_tokens,
			       SUM(token_cache_write_total) AS cache_write_tokens,
			       SUM(token_reasoning_total) AS reasoning_tokens,
			       MAX(computed_at) AS max_computed_at,
			       MAX(aggregation_version) AS max_agg_ver
			FROM daily_user_agent_model_metrics
			WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`

		if agentID != nil && *agentID != "" && *agentID != "all" {
			query += " AND agent_id = ?"
			args = append(args, *agentID)
		}
		if providerID != nil && *providerID != "" && *providerID != "all" {
			query += " AND provider_id = ?"
			args = append(args, *providerID)
		}
		if modelID != nil && *modelID != "" && *modelID != "all" {
			query += " AND model_id = ?"
			args = append(args, *modelID)
		}
		query += " GROUP BY metric_date ORDER BY metric_date ASC"
	} else {
		query = `
			SELECT metric_date,
			       COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			       SUM(token_input_total) AS input_tokens,
			       SUM(token_output_total) AS output_tokens,
			       SUM(token_cache_read_total) AS cache_read_tokens,
			       SUM(token_cache_write_total) AS cache_write_tokens,
			       SUM(token_reasoning_total) AS reasoning_tokens,
			       MAX(computed_at) AS max_computed_at,
			       MAX(aggregation_version) AS max_agg_ver
			FROM daily_user_agent_metrics
			WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`

		if agentID != nil && *agentID != "" && *agentID != "all" {
			query += " AND agent_id = ?"
			args = append(args, *agentID)
		}
		query += " GROUP BY metric_date ORDER BY metric_date ASC"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query token trend: %w", err)
	}
	defer rows.Close()

	points := make([]domain.TrendPoint, 0)
	var maxComputedAtNull sql.NullTime
	var maxAggVerNull sql.NullInt64

	for rows.Next() {
		var d time.Time
		var tot uint64
		var inpNull, outNull, crNull, cwNull, rsnNull sql.NullInt64
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&d, &tot, &inpNull, &outNull, &crNull, &cwNull, &rsnNull, &compAtNull, &aggVerNull); err != nil {
			return nil, fmt.Errorf("failed to scan token trend row: %w", err)
		}

		if compAtNull.Valid {
			if !maxComputedAtNull.Valid || compAtNull.Time.After(maxComputedAtNull.Time) {
				maxComputedAtNull = compAtNull
			}
		}
		if aggVerNull.Valid {
			if !maxAggVerNull.Valid || aggVerNull.Int64 > maxAggVerNull.Int64 {
				maxAggVerNull = aggVerNull
			}
		}

		dStr := d.Format("2006-01-02")
		totStr := fmt.Sprintf("%d", tot)

		if mode == "structure" {
			inpStr := "0"
			if inpNull.Valid {
				inpStr = fmt.Sprintf("%d", inpNull.Int64)
			}
			outStr := "0"
			if outNull.Valid {
				outStr = fmt.Sprintf("%d", outNull.Int64)
			}
			crStr := "0"
			if crNull.Valid {
				crStr = fmt.Sprintf("%d", crNull.Int64)
			}
			cwStr := "0"
			if cwNull.Valid {
				cwStr = fmt.Sprintf("%d", cwNull.Int64)
			}
			rsnStr := "0"
			if rsnNull.Valid {
				rsnStr = fmt.Sprintf("%d", rsnNull.Int64)
			}

			points = append(points, domain.TrendPoint{
				Date:             dStr,
				InputTokens:      &inpStr,
				OutputTokens:     &outStr,
				CacheReadTokens:  &crStr,
				CacheWriteTokens: &cwStr,
				ReasoningTokens:  &rsnStr,
			})
		} else {
			points = append(points, domain.TrendPoint{
				Date:       dStr,
				TokenTotal: &totStr,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("token trend rows iteration error: %w", err)
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	aggVer := uint32(2)
	if maxAggVerNull.Valid && maxAggVerNull.Int64 > 0 {
		aggVer = uint32(maxAggVerNull.Int64)
	}

	return &domain.TrendResponse{
		Range:              r,
		Mode:               mode,
		AgentID:            agentID,
		ProviderID:         providerID,
		ModelID:            modelID,
		Granularity:        "day",
		Points:             points,
		DataWatermarkAt:    dataWatermarkAt,
		AggregationVersion: aggVer,
	}, nil
}

func (s *analyticsStore) GetAgentBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error) {
	fromStr, toStr, _ := rangeDateStrings(r)

	query := `
		SELECT
			agent_id,
			COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			MAX(computed_at) AS max_computed_at,
			MAX(aggregation_version) AS max_agg_ver
		FROM daily_user_agent_metrics
		WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?
		GROUP BY agent_id
		ORDER BY total_tokens DESC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent breakdown: %w", err)
	}
	defer rows.Close()

	type rawAgentItem struct {
		agentID string
		tokens  uint64
	}

	var rawItems []rawAgentItem
	var sumTokens uint64
	var maxComputedAtNull sql.NullTime
	var maxAggVerNull sql.NullInt64

	for rows.Next() {
		var item rawAgentItem
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&item.agentID, &item.tokens, &compAtNull, &aggVerNull); err != nil {
			return nil, fmt.Errorf("failed to scan agent breakdown row: %w", err)
		}

		if compAtNull.Valid {
			if !maxComputedAtNull.Valid || compAtNull.Time.After(maxComputedAtNull.Time) {
				maxComputedAtNull = compAtNull
			}
		}
		if aggVerNull.Valid {
			if !maxAggVerNull.Valid || aggVerNull.Int64 > maxAggVerNull.Int64 {
				maxAggVerNull = aggVerNull
			}
		}

		rawItems = append(rawItems, item)
		sumTokens += item.tokens
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agent breakdown rows iteration error: %w", err)
	}

	items := make([]domain.BreakdownItem, 0)
	for _, it := range rawItems {
		pct := 0.0
		if sumTokens > 0 {
			pct = math.Round(float64(it.tokens)*1000.0/float64(sumTokens)) / 10.0
		}
		items = append(items, domain.BreakdownItem{
			Key:        it.agentID,
			Label:      agentDisplayName(it.agentID),
			TokenTotal: fmt.Sprintf("%d", it.tokens),
			Percentage: pct,
		})
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	aggVer := uint32(2)
	if maxAggVerNull.Valid && maxAggVerNull.Int64 > 0 {
		aggVer = uint32(maxAggVerNull.Int64)
	}

	return &domain.BreakdownResponse{
		Range:              r,
		Items:              items,
		DataWatermarkAt:    dataWatermarkAt,
		AggregationVersion: aggVer,
	}, nil
}

func (s *analyticsStore) GetModelBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error) {
	fromStr, toStr, _ := rangeDateStrings(r)

	query := `
		SELECT
			model_id,
			COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			MAX(computed_at) AS max_computed_at,
			MAX(aggregation_version) AS max_agg_ver
		FROM daily_user_agent_model_metrics
		WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?
		GROUP BY model_id
		ORDER BY total_tokens DESC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query model breakdown: %w", err)
	}
	defer rows.Close()

	type rawModelItem struct {
		modelID string
		tokens  uint64
	}

	var rawItems []rawModelItem
	var sumTokens uint64
	var maxComputedAtNull sql.NullTime
	var maxAggVerNull sql.NullInt64

	for rows.Next() {
		var item rawModelItem
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&item.modelID, &item.tokens, &compAtNull, &aggVerNull); err != nil {
			return nil, fmt.Errorf("failed to scan model breakdown row: %w", err)
		}

		if compAtNull.Valid {
			if !maxComputedAtNull.Valid || compAtNull.Time.After(maxComputedAtNull.Time) {
				maxComputedAtNull = compAtNull
			}
		}
		if aggVerNull.Valid {
			if !maxAggVerNull.Valid || aggVerNull.Int64 > maxAggVerNull.Int64 {
				maxAggVerNull = aggVerNull
			}
		}

		rawItems = append(rawItems, item)
		sumTokens += item.tokens
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("model breakdown rows iteration error: %w", err)
	}

	items := make([]domain.BreakdownItem, 0)
	for _, it := range rawItems {
		pct := 0.0
		if sumTokens > 0 {
			pct = math.Round(float64(it.tokens)*1000.0/float64(sumTokens)) / 10.0
		}
		items = append(items, domain.BreakdownItem{
			Key:        it.modelID,
			Label:      it.modelID,
			TokenTotal: fmt.Sprintf("%d", it.tokens),
			Percentage: pct,
		})
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	aggVer := uint32(2)
	if maxAggVerNull.Valid && maxAggVerNull.Int64 > 0 {
		aggVer = uint32(maxAggVerNull.Int64)
	}

	return &domain.BreakdownResponse{
		Range:              r,
		Items:              items,
		DataWatermarkAt:    dataWatermarkAt,
		AggregationVersion: aggVer,
	}, nil
}

func (s *analyticsStore) GetSkillRanking(ctx context.Context, userID string, r domain.TimeRange) (*domain.SkillsResponse, error) {
	fromStr, toStr, _ := rangeDateStrings(r)

	query := `
		SELECT
			HEX(skill_key) AS skill_hex,
			COALESCE(skill_public_name, '') AS skill_public_name,
			SUM(use_count) AS total_use_count,
			COUNT(DISTINCT metric_date) AS active_days,
			SUM(success_count) AS total_success,
			SUM(failure_count) AS total_failure,
			MAX(computed_at) AS max_computed_at,
			MAX(aggregation_version) AS max_agg_ver
		FROM daily_skill_metrics
		WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?
		GROUP BY skill_key, skill_public_name
		ORDER BY total_use_count DESC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query skill ranking: %w", err)
	}
	defer rows.Close()

	skills := make([]domain.SkillItem, 0)
	var maxComputedAtNull sql.NullTime
	var maxAggVerNull sql.NullInt64

	for rows.Next() {
		var skillHex, skillPubName string
		var useCount, activeDays, successCount, failureCount uint64
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&skillHex, &skillPubName, &useCount, &activeDays, &successCount, &failureCount, &compAtNull, &aggVerNull); err != nil {
			return nil, fmt.Errorf("failed to scan skill ranking row: %w", err)
		}

		if compAtNull.Valid {
			if !maxComputedAtNull.Valid || compAtNull.Time.After(maxComputedAtNull.Time) {
				maxComputedAtNull = compAtNull
			}
		}
		if aggVerNull.Valid {
			if !maxAggVerNull.Valid || aggVerNull.Int64 > maxAggVerNull.Int64 {
				maxAggVerNull = aggVerNull
			}
		}

		var skillID string
		if len(skillHex) >= 8 {
			skillID = fmt.Sprintf("skl_%s", strings.ToLower(skillHex[:8]))
		} else {
			skillID = fmt.Sprintf("skl_%s", strings.ToLower(skillHex))
		}

		displayName := skillPubName
		if displayName == "" {
			displayName = "Private Skill"
		}

		var successRate *float64
		if (successCount + failureCount) > 0 {
			sr := math.Round(float64(successCount)*1000.0/float64(successCount+failureCount)) / 1000.0
			successRate = &sr
		}

		skills = append(skills, domain.SkillItem{
			SkillID:          skillID,
			SkillPublicName:  displayName,
			UseCount:         fmt.Sprintf("%d", useCount),
			ActiveDays:       int(activeDays),
			SuccessRate:      successRate,
			PreviousDeltaPct: nil,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("skill ranking rows iteration error: %w", err)
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	aggVer := uint32(2)
	if maxAggVerNull.Valid && maxAggVerNull.Int64 > 0 {
		aggVer = uint32(maxAggVerNull.Int64)
	}

	return &domain.SkillsResponse{
		Range:              r,
		Skills:             skills,
		DataWatermarkAt:    dataWatermarkAt,
		AggregationVersion: aggVer,
	}, nil
}

func (s *analyticsStore) GetActivityCalendar(ctx context.Context, userID string, r domain.TimeRange) (*domain.CalendarResponse, error) {
	fromStr, toStr, loc := rangeDateStrings(r)

	query := `
		SELECT
			metric_date,
			COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			MAX(computed_at) AS max_computed_at,
			MAX(aggregation_version) AS max_agg_ver
		FROM daily_user_agent_metrics
		WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?
		GROUP BY metric_date
		ORDER BY metric_date ASC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromStr, toStr)
	if err != nil {
		return nil, fmt.Errorf("failed to query activity calendar: %w", err)
	}
	defer rows.Close()

	dayTokenMap := make(map[string]uint64)
	var maxComputedAtNull sql.NullTime
	var maxAggVerNull sql.NullInt64

	for rows.Next() {
		var d time.Time
		var tokens uint64
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&d, &tokens, &compAtNull, &aggVerNull); err != nil {
			return nil, fmt.Errorf("failed to scan activity calendar row: %w", err)
		}

		if compAtNull.Valid {
			if !maxComputedAtNull.Valid || compAtNull.Time.After(maxComputedAtNull.Time) {
				maxComputedAtNull = compAtNull
			}
		}
		if aggVerNull.Valid {
			if !maxAggVerNull.Valid || aggVerNull.Int64 > maxAggVerNull.Int64 {
				maxAggVerNull = aggVerNull
			}
		}

		dayTokenMap[d.Format("2006-01-02")] = tokens
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("activity calendar rows iteration error: %w", err)
	}

	var days []domain.CalendarDay
	totalActiveDays := 0

	localFrom, localTo := r.From.In(loc), r.To.In(loc)
	startDay := time.Date(localFrom.Year(), localFrom.Month(), localFrom.Day(), 0, 0, 0, 0, loc)
	endDay := time.Date(localTo.Year(), localTo.Month(), localTo.Day(), 0, 0, 0, 0, loc)

	for curr := startDay; !curr.After(endDay); curr = curr.AddDate(0, 0, 1) {
		dateStr := curr.Format("2006-01-02")
		tokens := dayTokenMap[dateStr]
		active := tokens > 0
		level := 0

		if active {
			totalActiveDays++
			if tokens < 1_000_000 {
				level = 1
			} else if tokens < 5_000_000 {
				level = 2
			} else if tokens < 20_000_000 {
				level = 3
			} else {
				level = 4
			}
		}

		days = append(days, domain.CalendarDay{
			Date:       dateStr,
			Active:     active,
			Level:      level,
			TokenTotal: fmt.Sprintf("%d", tokens),
		})
	}

	// Calculate streaks
	longestStreak := 0
	currRunningStreak := 0
	for _, day := range days {
		if day.Active {
			currRunningStreak++
			if currRunningStreak > longestStreak {
				longestStreak = currRunningStreak
			}
		} else {
			currRunningStreak = 0
		}
	}

	currentStreak := 0
	for i := len(days) - 1; i >= 0; i-- {
		if days[i].Active {
			currentStreak++
		} else {
			break
		}
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	aggVer := uint32(2)
	if maxAggVerNull.Valid && maxAggVerNull.Int64 > 0 {
		aggVer = uint32(maxAggVerNull.Int64)
	}

	return &domain.CalendarResponse{
		Days:               days,
		CurrentStreak:      currentStreak,
		LongestStreak:      longestStreak,
		TotalActiveDays:    totalActiveDays,
		DataWatermarkAt:    dataWatermarkAt,
		AggregationVersion: aggVer,
	}, nil
}

func (s *analyticsStore) GetActivity(ctx context.Context, userID string, q domain.ActivityQuery) ([]domain.ActivityRow, error) {
	fromStr, toStr, _ := rangeDateStrings(q.Range)
	modelDetail := q.ProviderID != nil || q.ModelID != nil
	var query string
	args := []interface{}{userID, fromStr, toStr}
	if modelDetail {
		query = `SELECT metric_date, agent_id, provider_id, model_id,
			COALESCE(exact_token_total + derived_token_total, 0), model_request_count
			FROM daily_user_agent_model_metrics
			WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`
		if q.AgentID != nil {
			query += " AND agent_id = ?"
			args = append(args, *q.AgentID)
		}
		if q.ProviderID != nil {
			query += " AND provider_id = ?"
			args = append(args, *q.ProviderID)
		}
		if q.ModelID != nil {
			query += " AND model_id = ?"
			args = append(args, *q.ModelID)
		}
		query += " ORDER BY metric_date DESC, agent_id ASC, provider_id ASC, model_id ASC LIMIT ? OFFSET ?"
	} else {
		query = `SELECT metric_date, agent_id,
			COALESCE(exact_token_total + derived_token_total, 0), message_count,
			active_duration_ms, code_generated_lines
			FROM daily_user_agent_metrics
			WHERE user_id = ? AND metric_date >= ? AND metric_date <= ?`
		if q.AgentID != nil {
			query += " AND agent_id = ?"
			args = append(args, *q.AgentID)
		}
		query += " ORDER BY metric_date DESC, agent_id ASC LIMIT ? OFFSET ?"
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query safe activity rows: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ActivityRow, 0)
	for rows.Next() {
		var date time.Time
		var item domain.ActivityRow
		var tokens uint64
		if modelDetail {
			var providerID, modelID string
			var requestCount uint64
			if err := rows.Scan(&date, &item.AgentID, &providerID, &modelID, &tokens, &requestCount); err != nil {
				return nil, err
			}
			item.ProviderID, item.ModelID = &providerID, &modelID
			messageCount := fmt.Sprintf("%d", requestCount)
			item.MessageCount = &messageCount
			item.GeneratedCodeLines = "0"
		} else {
			var messages, duration sql.NullInt64
			var codeLines uint64
			if err := rows.Scan(&date, &item.AgentID, &tokens, &messages, &duration, &codeLines); err != nil {
				return nil, err
			}
			if messages.Valid {
				value := fmt.Sprintf("%d", messages.Int64)
				item.MessageCount = &value
			}
			if duration.Valid {
				value := fmt.Sprintf("%d", duration.Int64)
				item.ActiveDurationMs = &value
			}
			item.GeneratedCodeLines = fmt.Sprintf("%d", codeLines)
		}
		item.Date = date.Format("2006-01-02")
		item.TokenTotal = fmt.Sprintf("%d", tokens)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *analyticsStore) GetFilterOptions(ctx context.Context, userID string) (*domain.FilterOptions, error) {
	agentRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT agent_id
		FROM daily_user_agent_metrics
		WHERE user_id = ?
		ORDER BY agent_id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query agent filter options: %w", err)
	}
	defer agentRows.Close()

	agents := make([]string, 0)
	for agentRows.Next() {
		var a string
		if err := agentRows.Scan(&a); err != nil {
			return nil, fmt.Errorf("failed to scan agent filter option: %w", err)
		}
		agents = append(agents, a)
	}
	if err := agentRows.Err(); err != nil {
		return nil, fmt.Errorf("agent filter options iteration error: %w", err)
	}

	modelRows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT provider_id, model_id
		FROM daily_user_agent_model_metrics
		WHERE user_id = ?
		ORDER BY provider_id ASC, model_id ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query model filter options: %w", err)
	}
	defer modelRows.Close()

	providers := make([]string, 0)
	models := make([]string, 0)
	seenProviders := make(map[string]bool)
	seenModels := make(map[string]bool)

	for modelRows.Next() {
		var prov, mod string
		if err := modelRows.Scan(&prov, &mod); err != nil {
			return nil, fmt.Errorf("failed to scan model filter option: %w", err)
		}
		if !seenProviders[prov] && prov != "" {
			providers = append(providers, prov)
			seenProviders[prov] = true
		}
		if !seenModels[mod] && mod != "" {
			models = append(models, mod)
			seenModels[mod] = true
		}
	}
	if err := modelRows.Err(); err != nil {
		return nil, fmt.Errorf("model filter options iteration error: %w", err)
	}

	return &domain.FilterOptions{
		Agents:    agents,
		Providers: providers,
		Models:    models,
	}, nil
}
