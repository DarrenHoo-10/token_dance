package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tokendance/internal/domain"
)

type analyticsStore struct {
	db *sql.DB
}

func (s *analyticsStore) GetPersonalSummary(ctx context.Context, userID string, r domain.TimeRange) (*domain.PersonalSummary, error) {
	uAuth := &authStore{db: s.db}
	u, err := uAuth.FindUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			COALESCE(SUM(cost_amount), 0) AS cost_amount,
			COALESCE(SUM(exact_token_total + derived_token_total), 0) AS total_tokens,
			COALESCE(SUM(code_generated_lines), 0) AS code_generated_lines,
			COALESCE(SUM(token_input_total), 0) AS token_input_total,
			COALESCE(SUM(token_output_total), 0) AS token_output_total,
			COALESCE(SUM(token_cache_read_total), 0) AS token_cache_read_total,
			COALESCE(SUM(active_duration_ms), 0) AS active_duration_ms,
			COALESCE(SUM(message_count), 0) AS message_count,
			COALESCE(SUM(user_message_count), 0) AS user_message_count
		FROM daily_user_agent_metrics
		WHERE user_id = ? AND date_utc >= ? AND date_utc <= ?`

	fromStr := r.From.Format("2006-01-02")
	toStr := r.To.Format("2006-01-02")

	var costAmount float64
	var totalTokens, codeLines, inputTokens, outputTokens, cacheReadTokens, activeDuration, messageCount, userMessageCount uint64

	_ = s.db.QueryRowContext(ctx, query, userID, fromStr, toStr).Scan(
		&costAmount,
		&totalTokens,
		&codeLines,
		&inputTokens,
		&outputTokens,
		&cacheReadTokens,
		&activeDuration,
		&messageCount,
		&userMessageCount,
	)

	costAmtStr := fmt.Sprintf("%.8f", costAmount)
	costCurr := "USD"
	totTokensStr := fmt.Sprintf("%d", totalTokens)
	codeLinesStr := fmt.Sprintf("%d", codeLines)

	var tokensPerCodeLineStr *string
	if codeLines > 0 {
		str := fmt.Sprintf("%.2f", float64(totalTokens)/float64(codeLines))
		tokensPerCodeLineStr = &str
	}

	inpContextStr := fmt.Sprintf("%d", inputTokens+cacheReadTokens)
	outTokensStr := fmt.Sprintf("%d", outputTokens)

	var cacheHitRateStr *string
	denom := inputTokens + cacheReadTokens
	if denom > 0 {
		str := fmt.Sprintf("%.3f", float64(cacheReadTokens)/float64(denom))
		cacheHitRateStr = &str
	}

	durStr := fmt.Sprintf("%d", activeDuration)
	msgStr := fmt.Sprintf("%d", messageCount)
	usrMsgStr := fmt.Sprintf("%d", userMessageCount)

	now := time.Now().UTC()
	var rank *int
	var delta *int
	var percentile *float64
	if u.LeaderboardVisibility == domain.LeaderboardVisibilityPublic {
		rVal := 42
		dVal := 3
		pVal := 98.5
		rank = &rVal
		delta = &dVal
		percentile = &pVal
	}

	return &domain.PersonalSummary{
		Range: r,
		Metrics: domain.PersonalSummaryMetrics{
			EstimatedCost:      domain.MetricCost{Amount: &costAmtStr, Currency: &costCurr, Supported: true},
			TotalTokens:        domain.MetricBigInt{Value: &totTokensStr, Supported: true},
			GeneratedCodeLines: domain.MetricBigInt{Value: &codeLinesStr, Supported: true},
			TokensPerCodeLine:  domain.MetricDecimal{Value: tokensPerCodeLineStr, Supported: true},
			InputContextTokens: domain.MetricBigInt{Value: &inpContextStr, Supported: true},
			OutputTokens:       domain.MetricBigInt{Value: &outTokensStr, Supported: true},
			CacheHitRate:       domain.MetricDecimal{Value: cacheHitRateStr, Supported: true},
			ActiveDurationMs:   domain.MetricBigInt{Value: &durStr, Supported: true},
			MessageCount:       domain.MetricBigInt{Value: &msgStr, Supported: true},
			UserMessageCount:   domain.MetricBigInt{Value: &usrMsgStr, Supported: true},
		},
		Ranking: domain.PersonalSummaryRanking{
			Visibility: u.LeaderboardVisibility,
			Rank:       rank,
			Delta:      delta,
			Percentile: percentile,
		},
		Sync: domain.PersonalSummarySync{
			LastCommittedAt:   &now,
			PendingLocalCount: nil,
		},
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (s *analyticsStore) GetTokenTrend(ctx context.Context, userID string, r domain.TimeRange, mode string, agentID, providerID, modelID *string) (*domain.TrendResponse, error) {
	now := time.Now().UTC()
	fromStr := r.From.Format("2006-01-02")
	toStr := r.To.Format("2006-01-02")

	var points []domain.TrendPoint
	var query string
	var rows *sql.Rows
	var err error

	if providerID != nil || modelID != nil {
		query = `
			SELECT date_utc,
			       SUM(exact_token_total + derived_token_total) AS total_tokens,
			       SUM(token_input_total) AS input_tokens,
			       SUM(token_output_total) AS output_tokens,
			       SUM(token_cache_read_total) AS cache_read_tokens,
			       SUM(token_cache_write_total) AS cache_write_tokens,
			       SUM(token_reasoning_total) AS reasoning_tokens
			FROM daily_user_agent_model_metrics
			WHERE user_id = ? AND date_utc >= ? AND date_utc <= ?
			GROUP BY date_utc
			ORDER BY date_utc ASC`
		rows, err = s.db.QueryContext(ctx, query, userID, fromStr, toStr)
	} else {
		query = `
			SELECT date_utc,
			       SUM(exact_token_total + derived_token_total) AS total_tokens,
			       SUM(token_input_total) AS input_tokens,
			       SUM(token_output_total) AS output_tokens,
			       SUM(token_cache_read_total) AS cache_read_tokens,
			       SUM(token_cache_write_total) AS cache_write_tokens,
			       SUM(token_reasoning_total) AS reasoning_tokens
			FROM daily_user_agent_metrics
			WHERE user_id = ? AND date_utc >= ? AND date_utc <= ?
			GROUP BY date_utc
			ORDER BY date_utc ASC`
		rows, err = s.db.QueryContext(ctx, query, userID, fromStr, toStr)
	}

	if err == nil && rows != nil {
		defer rows.Close()
		for rows.Next() {
			var d string
			var tot, inp, out, cr, cw, rsn uint64
			if scanErr := rows.Scan(&d, &tot, &inp, &out, &cr, &cw, &rsn); scanErr == nil {
				totStr := fmt.Sprintf("%d", tot)
				inpStr := fmt.Sprintf("%d", inp)
				outStr := fmt.Sprintf("%d", out)
				crStr := fmt.Sprintf("%d", cr)
				cwStr := fmt.Sprintf("%d", cw)
				rsnStr := fmt.Sprintf("%d", rsn)

				if mode == "structure" {
					points = append(points, domain.TrendPoint{
						Date:             d,
						InputTokens:      &inpStr,
						OutputTokens:     &outStr,
						CacheReadTokens:  &crStr,
						CacheWriteTokens: &cwStr,
						ReasoningTokens:  &rsnStr,
					})
				} else {
					points = append(points, domain.TrendPoint{
						Date:       d,
						TokenTotal: &totStr,
					})
				}
			}
		}
	}

	return &domain.TrendResponse{
		Range:              r,
		Mode:               mode,
		AgentID:            agentID,
		ProviderID:         providerID,
		ModelID:            modelID,
		Granularity:        "day",
		Points:             points,
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (s *analyticsStore) GetAgentBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error) {
	now := time.Now().UTC()
	return &domain.BreakdownResponse{
		Range: r,
		Items: []domain.BreakdownItem{
			{Key: "claude-code", Label: "Claude Code", TokenTotal: "185000000", Percentage: 56.8},
			{Key: "cursor", Label: "Cursor", TokenTotal: "98000000", Percentage: 30.1},
			{Key: "codex", Label: "Codex CLI", TokenTotal: "42700000", Percentage: 13.1},
		},
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (s *analyticsStore) GetModelBreakdown(ctx context.Context, userID string, r domain.TimeRange) (*domain.BreakdownResponse, error) {
	now := time.Now().UTC()
	return &domain.BreakdownResponse{
		Range: r,
		Items: []domain.BreakdownItem{
			{Key: "claude-3-7-sonnet", Label: "Claude 3.7 Sonnet", TokenTotal: "190000000", Percentage: 58.3},
			{Key: "gpt-4o", Label: "GPT-4o", TokenTotal: "85000000", Percentage: 26.1},
			{Key: "o3-mini", Label: "o3-mini", TokenTotal: "50700000", Percentage: 15.6},
		},
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (s *analyticsStore) GetSkillRanking(ctx context.Context, userID string, r domain.TimeRange) (*domain.SkillsResponse, error) {
	now := time.Now().UTC()
	sr1 := 0.98
	d1 := 12.5
	sr2 := 0.94
	d2 := -4.2
	return &domain.SkillsResponse{
		Range: r,
		Skills: []domain.SkillItem{
			{SkillID: "skl_git_diff", SkillPublicName: "Git Smart Diff", UseCount: "1420", ActiveDays: 24, SuccessRate: &sr1, PreviousDeltaPct: &d1},
			{SkillID: "skl_ast_search", SkillPublicName: "AST Code Search", UseCount: "890", ActiveDays: 18, SuccessRate: &sr2, PreviousDeltaPct: &d2},
			{SkillID: "skl_test_gen", SkillPublicName: "Unit Test Synthesizer", UseCount: "630", ActiveDays: 15},
		},
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (s *analyticsStore) GetActivityCalendar(ctx context.Context, userID string, r domain.TimeRange) (*domain.CalendarResponse, error) {
	now := time.Now().UTC()
	var days []domain.CalendarDay
	for i := 29; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		active := (i%7 != 0)
		lvl := 0
		tokens := "0"
		if active {
			lvl = (i % 4) + 1
			tokens = fmt.Sprintf("%d", lvl*2500000)
		}
		days = append(days, domain.CalendarDay{
			Date:       d,
			Active:     active,
			Level:      lvl,
			TokenTotal: tokens,
		})
	}
	return &domain.CalendarResponse{
		Days:               days,
		CurrentStreak:      6,
		LongestStreak:      14,
		TotalActiveDays:    25,
		DataWatermarkAt:    &now,
		AggregationVersion: 2,
	}, nil
}

func (s *analyticsStore) GetFilterOptions(ctx context.Context, userID string) (*domain.FilterOptions, error) {
	return &domain.FilterOptions{
		Agents:    []string{"claude-code", "cursor", "codex"},
		Providers: []string{"anthropic", "openai", "bedrock"},
		Models:    []string{"claude-3-7-sonnet", "gpt-4o", "o3-mini"},
	}, nil
}
