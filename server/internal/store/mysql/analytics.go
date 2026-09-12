package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"tokendance/internal/domain"
)

type analyticsStore struct {
	db *sql.DB
}

func rangeDateStrings(r domain.TimeRange) (string, string, *time.Location) {
	loc := domain.DayTZ
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
	case "opencode":
		return "OpenCode"
	case "workbuddy":
		return "WorkBuddy"
	case "doubao-work":
		return "Doubao Work"
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

	plan := planUTCAggregates(r)
	fromStr, toStr := plan.fromDate, plan.toDate
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}

	var rowCount int
	var totalTokens, codeLines uint64
	var inputTokensNull, outputTokensNull, cacheReadNull, cacheWriteNull, reasoningNull sql.NullInt64
	var eligibleInputNull, eligibleReadNull sql.NullInt64
	var cachePairKnown, usageObserved int64
	var activeDurationNull, messageCountNull, userMsgNull sql.NullInt64
	var minAggVerNull, maxAggVerNull sql.NullInt64
	var maxComputedAtNull sql.NullTime

	// Tokens + token structure from telemetry_model_metrics (trusted = exact+derived).
	// Cache hit rate uses paired cache_eligible_* columns, not unpaired totals.
	err = s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED),
			SUM(input_context_tokens),
			SUM(output_tokens),
			SUM(cache_read_tokens),
			SUM(cache_write_tokens),
			SUM(reasoning_tokens),
			SUM(cache_eligible_input_tokens),
			SUM(cache_eligible_read_tokens),
			CAST(COALESCE(SUM(cache_pair_known_count), 0) AS SIGNED),
			CAST(COALESCE(SUM(usage_observed_count), 0) AS SIGNED),
			MIN(metric_semantics_version),
			MAX(metric_semantics_version),
			`+telemetryWatermarkSQL+`
		FROM telemetry_model_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?`,
		userID, fromMs, toMs,
	).Scan(
		&rowCount, &totalTokens,
		&inputTokensNull, &outputTokensNull, &cacheReadNull, &cacheWriteNull, &reasoningNull,
		&eligibleInputNull, &eligibleReadNull, &cachePairKnown, &usageObserved,
		&minAggVerNull, &maxAggVerNull, &maxComputedAtNull,
	)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query personal summary tokens: %w", err)
	}

	// Harness activity: code lines, duration, messages.
	var harnessRows int
	var harnessWatermark sql.NullTime
	err = s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			COALESCE(SUM(code_generated_lines), 0),
			SUM(active_duration_ms),
			SUM(turn_started_count + turn_completed_count),
			SUM(user_turn_started_count),
			`+telemetryWatermarkSQL+`
		FROM telemetry_harness_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?`,
		userID, fromMs, toMs,
	).Scan(&harnessRows, &codeLines, &activeDurationNull, &messageCountNull, &userMsgNull, &harnessWatermark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("failed to query personal summary harness: %w", err)
	}
	if harnessRows > rowCount {
		rowCount = harnessRows
	}
	maxNullTime(&maxComputedAtNull, harnessWatermark)

	// Costs grouped by currency — never silently SUM across currencies as USD.
	costRows, err := s.db.QueryContext(ctx, `
		SELECT
			currency,
			CAST(COALESCE(SUM(reported_cost_units + estimated_cost_units), 0) AS CHAR),
			CAST(COALESCE(SUM(cost_known_count), 0) AS UNSIGNED),
			CAST(COALESCE(SUM(reported_request_count + estimated_request_count), 0) AS UNSIGNED),
			CAST(COALESCE(SUM(reported_request_count + estimated_request_count + unpriced_request_count), 0) AS UNSIGNED)
		FROM telemetry_cost_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?
		GROUP BY currency
		ORDER BY currency`,
		userID, fromMs, toMs,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query personal summary cost: %w", err)
	}
	defer costRows.Close()

	var estimatedCosts []domain.MetricCost
	var pricedRequestsTotal, totalRequestsTotal, costRecords int
	for costRows.Next() {
		var currency string
		var costUnits string
		var costKnown, pricedRequests, totalRequests int
		if err := costRows.Scan(&currency, &costUnits, &costKnown, &pricedRequests, &totalRequests); err != nil {
			return nil, fmt.Errorf("failed to scan personal summary cost: %w", err)
		}
		metric, err := costMetricFromUnits(currency, costUnits, costKnown, pricedRequests, totalRequests)
		if err != nil {
			return nil, fmt.Errorf("failed to format personal summary cost: %w", err)
		}
		estimatedCosts = append(estimatedCosts, metric)
		pricedRequestsTotal += pricedRequests
		totalRequestsTotal += totalRequests
		costRecords += costKnown
	}
	if err := costRows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate personal summary cost: %w", err)
	}

	raw, err := s.queryRawSummary(ctx, userID, plan.raw)
	if err != nil {
		return nil, err
	}
	rowCount += int(raw.rowCount)
	// Legacy usage_events boundary path has no FX; do not fold raw.cost into a
	// USD scalar when telemetry already reports multi-currency (or at all as USD).
	_ = raw.cost
	totalTokens += raw.tokens
	codeLines += raw.codeLines
	addNullInt64(&inputTokensNull, raw.input)
	addNullInt64(&outputTokensNull, raw.output)
	addNullInt64(&cacheReadNull, raw.cacheRead)
	addNullInt64(&cacheWriteNull, raw.cacheWrite)
	addNullInt64(&reasoningNull, raw.reasoning)
	addNullInt64(&activeDurationNull, raw.duration)
	addNullInt64(&messageCountNull, raw.messages)
	addNullInt64(&userMsgNull, raw.userMessages)
	maxNullTime(&maxComputedAtNull, raw.maxReceivedAt)

	var codeRecords, durationRecords int
	_ = s.db.QueryRowContext(ctx, `
		SELECT
			CAST(COALESCE(SUM(code_known_count), 0) AS UNSIGNED),
			CAST(COALESCE(SUM(duration_known_count), 0) AS UNSIGNED)
		FROM telemetry_harness_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?`,
		userID, fromMs, toMs,
	).Scan(&codeRecords, &durationRecords)
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

	eligibleInput := int64(0)
	eligibleRead := int64(0)
	if eligibleInputNull.Valid {
		eligibleInput = eligibleInputNull.Int64
	}
	if eligibleReadNull.Valid {
		eligibleRead = eligibleReadNull.Int64
	}

	if rowCount == 0 {
		zeroStr := "0"
		inputMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		outputMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		cacheHitMetric = cacheHitRateMetric(0, 0, 0, 0, true)
		durationMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		messageMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
		userMessageMetric = domain.MetricBigInt{Value: &zeroStr, Supported: true}
	} else {
		// telemetry_* always carries token structure columns; treat any semantics version as supported.
		extSupported := true

		if extSupported && inputTokensNull.Valid {
			inpVal := uint64(inputTokensNull.Int64)
			sVal := fmt.Sprintf("%d", inpVal)
			inputMetric = domain.MetricBigInt{Value: &sVal, Supported: true}
		} else {
			inputMetric = domain.MetricBigInt{Value: nil, Supported: false}
		}
		cacheHitMetric = cacheHitRateMetric(eligibleInput, eligibleRead, cachePairKnown, usageObserved, extSupported)

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
	var ownEntry *domain.LeaderboardEntry
	if u.AccountStatus == domain.AccountStatusActive {
		var rankErr error
		ownEntry, percentile, rankErr = (&leaderboardStore{db: s.db}).liveOwnTokenEntry(ctx, userID, string(r.Key), time.Now())
		if rankErr != nil {
			return nil, fmt.Errorf("query personal token rank: %w", rankErr)
		}
		if ownEntry != nil {
			rank = &ownEntry.RankNo
			delta = ownEntry.RankDelta
		}
	}

	var dataWatermarkAt *time.Time
	if maxComputedAtNull.Valid {
		tVal := maxComputedAtNull.Time
		dataWatermarkAt = &tVal
	}

	if durationRecords == 0 && (!activeDurationNull.Valid || activeDurationNull.Int64 == 0) {
		durationMetric = domain.MetricBigInt{Supported: false}
	}
	costMetric := scalarCostFromCurrencies(estimatedCosts, pricedRequestsTotal, totalRequestsTotal, costRecords)
	codeMetric := domain.MetricBigInt{Value: &codeLinesStr, Supported: codeRecords > 0 || codeLines > 0}
	if !codeMetric.Supported {
		codeMetric.Value = nil
	}
	return &domain.PersonalSummary{
		Range: r,
		Metrics: domain.PersonalSummaryMetrics{
			EstimatedCost:      costMetric,
			EstimatedCosts:     estimatedCosts,
			TotalTokens:        domain.MetricBigInt{Value: &totTokensStr, Supported: true},
			GeneratedCodeLines: codeMetric,
			TokensPerCodeLine:  domain.MetricDecimal{Value: tokensPerCodeLineStr, Supported: tokensPerCodeLineStr != nil},
			InputContextTokens: inputMetric,
			OutputTokens:       outputMetric,
			CacheHitRate:       cacheHitMetric,
			ActiveDurationMs:   durationMetric,
			MessageCount:       messageMetric,
			UserMessageCount:   userMessageMetric,
		},
		Ranking: domain.PersonalSummaryRanking{
			Entry:      ownEntry,
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
	plan := planUTCAggregates(r)
	fromStr, toStr := plan.fromDate, plan.toDate
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}

	hasModelFilter := (providerID != nil && *providerID != "" && *providerID != "all") || (modelID != nil && *modelID != "" && *modelID != "all")

	var query string
	var args []interface{}
	args = append(args, userID, fromMs, toMs)

	if hasModelFilter {
		query = `
			SELECT ` + telemetryMetricDateSQLPrefixed("m") + ` AS metric_date,
			       CAST(COALESCE(SUM(m.exact_token_total + m.derived_token_total), 0) AS UNSIGNED) AS total_tokens,
			       SUM(m.input_context_tokens) AS input_tokens,
			       SUM(m.output_tokens) AS output_tokens,
			       SUM(m.cache_read_tokens) AS cache_read_tokens,
			       SUM(m.cache_write_tokens) AS cache_write_tokens,
			       SUM(m.reasoning_tokens) AS reasoning_tokens,
			       FROM_UNIXTIME(MAX(m.updated_at) / 1000) AS max_computed_at,
			       MAX(m.metric_semantics_version) AS max_agg_ver
			FROM telemetry_model_metrics m
			JOIN telemetry_models tm ON tm.id = m.model_key
			WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
			  AND m.bucket_start >= ? AND m.bucket_start <= ?`

		if agentID != nil && *agentID != "" && *agentID != "all" {
			query += " AND m.harness_id = ?"
			args = append(args, *agentID)
		}
		if providerID != nil && *providerID != "" && *providerID != "all" {
			query += " AND tm.provider_id = ?"
			args = append(args, *providerID)
		}
		if modelID != nil && *modelID != "" && *modelID != "all" {
			query += " AND tm.model_id = ?"
			args = append(args, *modelID)
		}
		query += " GROUP BY metric_date ORDER BY metric_date ASC"
	} else {
		query = `
			SELECT ` + telemetryMetricDateSQL + ` AS metric_date,
			       CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED) AS total_tokens,
			       SUM(input_context_tokens) AS input_tokens,
			       SUM(output_tokens) AS output_tokens,
			       SUM(cache_read_tokens) AS cache_read_tokens,
			       SUM(cache_write_tokens) AS cache_write_tokens,
			       SUM(reasoning_tokens) AS reasoning_tokens,
			       ` + telemetryWatermarkSQL + ` AS max_computed_at,
			       MAX(metric_semantics_version) AS max_agg_ver
			FROM telemetry_model_metrics
			WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
			  AND bucket_start >= ? AND bucket_start <= ?`

		if agentID != nil && *agentID != "" && *agentID != "all" {
			query += " AND harness_id = ?"
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
		var dStr string
		var tot uint64
		var inpNull, outNull, crNull, cwNull, rsnNull sql.NullInt64
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&dStr, &tot, &inpNull, &outNull, &crNull, &cwNull, &rsnNull, &compAtNull, &aggVerNull); err != nil {
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
			totCopy := totStr
			points = append(points, domain.TrendPoint{
				Date:       dStr,
				TokenTotal: &totCopy,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("token trend rows iteration error: %w", err)
	}

	rawPoints, err := s.queryRawTokenPoints(ctx, userID, r.Timezone, plan.raw, agentID, providerID, modelID)
	if err != nil {
		return nil, err
	}
	pointByDate := make(map[string]domain.TrendPoint, len(points)+len(rawPoints))
	for _, point := range points {
		pointByDate[point.Date] = point
	}
	addValue := func(target **string, delta uint64) {
		value := delta
		if *target != nil {
			if parsed, parseErr := strconv.ParseUint(**target, 10, 64); parseErr == nil {
				value += parsed
			}
		}
		formatted := fmt.Sprintf("%d", value)
		*target = &formatted
	}
	for _, rawPoint := range rawPoints {
		point := pointByDate[rawPoint.date]
		point.Date = rawPoint.date
		if mode == "structure" {
			addValue(&point.InputTokens, rawPoint.input)
			addValue(&point.OutputTokens, rawPoint.output)
			addValue(&point.CacheReadTokens, rawPoint.cacheRead)
			addValue(&point.CacheWriteTokens, rawPoint.cacheWrite)
			addValue(&point.ReasoningTokens, rawPoint.reasoning)
		} else {
			addValue(&point.TokenTotal, rawPoint.total)
		}
		pointByDate[rawPoint.date] = point
		maxNullTime(&maxComputedAtNull, rawPoint.watermark)
	}
	points = points[:0]
	for _, point := range pointByDate {
		points = append(points, point)
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Date < points[j].Date })

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
	plan := planUTCAggregates(r)
	fromStr, toStr := plan.fromDate, plan.toDate
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			harness_id,
			CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED) AS total_tokens,
			` + telemetryWatermarkSQL + ` AS max_computed_at,
			MAX(metric_semantics_version) AS max_agg_ver
		FROM telemetry_model_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?
		GROUP BY harness_id
		ORDER BY total_tokens DESC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromMs, toMs)
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

	rawBoundaryItems, err := s.queryRawBreakdown(ctx, userID, plan.raw, "agent_id")
	if err != nil {
		return nil, err
	}
	byAgent := make(map[string]uint64, len(rawItems)+len(rawBoundaryItems))
	for _, item := range rawItems {
		byAgent[item.agentID] += item.tokens
	}
	for _, item := range rawBoundaryItems {
		byAgent[item.key] += item.tokens
		sumTokens += item.tokens
		maxNullTime(&maxComputedAtNull, item.watermark)
	}
	rawItems = rawItems[:0]
	for agentID, tokens := range byAgent {
		rawItems = append(rawItems, rawAgentItem{agentID: agentID, tokens: tokens})
	}
	sort.Slice(rawItems, func(i, j int) bool {
		if rawItems[i].tokens == rawItems[j].tokens {
			return rawItems[i].agentID < rawItems[j].agentID
		}
		return rawItems[i].tokens > rawItems[j].tokens
	})

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
	plan := planUTCAggregates(r)
	fromStr, toStr := plan.fromDate, plan.toDate
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			tm.model_id,
			CAST(COALESCE(SUM(m.exact_token_total + m.derived_token_total), 0) AS UNSIGNED) AS total_tokens,
			FROM_UNIXTIME(MAX(m.updated_at) / 1000) AS max_computed_at,
			MAX(m.metric_semantics_version) AS max_agg_ver
		FROM telemetry_model_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
		  AND m.bucket_start >= ? AND m.bucket_start <= ?
		GROUP BY tm.model_id
		ORDER BY total_tokens DESC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromMs, toMs)
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

	rawBoundaryItems, err := s.queryRawBreakdown(ctx, userID, plan.raw, "model_id")
	if err != nil {
		return nil, err
	}
	byModel := make(map[string]uint64, len(rawItems)+len(rawBoundaryItems))
	for _, item := range rawItems {
		byModel[item.modelID] += item.tokens
	}
	for _, item := range rawBoundaryItems {
		byModel[item.key] += item.tokens
		sumTokens += item.tokens
		maxNullTime(&maxComputedAtNull, item.watermark)
	}
	rawItems = rawItems[:0]
	for modelID, tokens := range byModel {
		rawItems = append(rawItems, rawModelItem{modelID: modelID, tokens: tokens})
	}
	sort.Slice(rawItems, func(i, j int) bool {
		if rawItems[i].tokens == rawItems[j].tokens {
			return rawItems[i].modelID < rawItems[j].modelID
		}
		return rawItems[i].tokens > rawItems[j].tokens
	})

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
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			HEX(s.skill_key) AS skill_hex,
			COALESCE(s.public_name, '') AS skill_public_name,
			SUM(m.use_count) AS total_use_count,
			COUNT(DISTINCT m.bucket_start) AS active_days,
			SUM(m.success_count) AS total_success,
			SUM(m.failure_count) AS total_failure,
			FROM_UNIXTIME(MAX(m.updated_at) / 1000) AS max_computed_at,
			MAX(m.metric_semantics_version) AS max_agg_ver
		FROM telemetry_skill_metrics m
		JOIN telemetry_skills s ON s.id = m.skill_id
		WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
		  AND m.bucket_start >= ? AND m.bucket_start <= ?
		GROUP BY s.skill_key, s.public_name
		ORDER BY total_use_count DESC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromMs, toMs)
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
	plan := planUTCAggregates(r)
	fromStr, toStr, loc := plan.fromDate, plan.toDate, plan.loc
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT
			` + telemetryMetricDateSQL + ` AS metric_date,
			CAST(COALESCE(SUM(exact_token_total + derived_token_total), 0) AS UNSIGNED) AS total_tokens,
			` + telemetryWatermarkSQL + ` AS max_computed_at,
			MAX(metric_semantics_version) AS max_agg_ver
		FROM telemetry_model_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND bucket_start >= ? AND bucket_start <= ?
		GROUP BY metric_date
		ORDER BY metric_date ASC`

	rows, err := s.db.QueryContext(ctx, query, userID, fromMs, toMs)
	if err != nil {
		return nil, fmt.Errorf("failed to query activity calendar: %w", err)
	}
	defer rows.Close()

	dayTokenMap := make(map[string]uint64)
	var maxComputedAtNull sql.NullTime
	var maxAggVerNull sql.NullInt64

	for rows.Next() {
		var dStr string
		var tokens uint64
		var compAtNull sql.NullTime
		var aggVerNull sql.NullInt64

		if err := rows.Scan(&dStr, &tokens, &compAtNull, &aggVerNull); err != nil {
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

		dayTokenMap[dStr] = tokens
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("activity calendar rows iteration error: %w", err)
	}

	rawPoints, err := s.queryRawTokenPoints(ctx, userID, r.Timezone, plan.raw, nil, nil, nil)
	if err != nil {
		return nil, err
	}
	for _, point := range rawPoints {
		dayTokenMap[point.date] += point.total
		maxNullTime(&maxComputedAtNull, point.watermark)
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
	i := len(days) - 1
	if i >= 0 && !days[i].Active {
		// Today is still in progress; an empty current day does not reset the streak.
		i--
	}
	for ; i >= 0; i-- {
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
	fromMs, toMs, err := dayGrainBucketRange(fromStr, toStr)
	if err != nil {
		return nil, err
	}
	modelDetail := q.ProviderID != nil || q.ModelID != nil
	// sqlc-dynamic-reviewed: validated optional filters and pagination require a bounded query builder.
	var query string
	args := []interface{}{userID, fromMs, toMs}
	if modelDetail {
		query = `SELECT ` + telemetryMetricDateSQLPrefixed("m") + `, m.harness_id, tm.provider_id, tm.model_id,
			CAST(COALESCE(m.exact_token_total + m.derived_token_total, 0) AS UNSIGNED), m.model_request_count
			FROM telemetry_model_metrics m
			JOIN telemetry_models tm ON tm.id = m.model_key
			WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
			  AND m.bucket_start >= ? AND m.bucket_start <= ?`
		if q.AgentID != nil {
			query += " AND m.harness_id = ?"
			args = append(args, *q.AgentID)
		}
		if q.ProviderID != nil {
			query += " AND tm.provider_id = ?"
			args = append(args, *q.ProviderID)
		}
		if q.ModelID != nil {
			query += " AND tm.model_id = ?"
			args = append(args, *q.ModelID)
		}
		query += " ORDER BY metric_date DESC, m.harness_id ASC, tm.provider_id ASC, tm.model_id ASC LIMIT ? OFFSET ?"
	} else {
		query = `SELECT ` + telemetryMetricDateSQLPrefixed("h") + `, harness_id,
			CAST(COALESCE((
				SELECT SUM(mm.exact_token_total + mm.derived_token_total)
				FROM telemetry_model_metrics mm
				WHERE mm.user_id = h.user_id AND mm.grain = h.grain AND mm.bucket_start = h.bucket_start
				  AND mm.installation_id = h.installation_id AND mm.harness_id = h.harness_id
				  AND mm.delete_at IS NULL
			), 0) AS UNSIGNED),
			(h.turn_started_count + h.turn_completed_count),
			h.active_duration_ms, h.code_generated_lines
			FROM telemetry_harness_metrics h
			WHERE h.user_id = ? AND h.grain = 'day' AND h.delete_at IS NULL
			  AND h.bucket_start >= ? AND h.bucket_start <= ?`
		if q.AgentID != nil {
			query += " AND h.harness_id = ?"
			args = append(args, *q.AgentID)
		}
		query += " ORDER BY metric_date DESC, h.harness_id ASC LIMIT ? OFFSET ?"
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query safe activity rows: %w", err)
	}
	defer rows.Close()
	items := make([]domain.ActivityRow, 0)
	for rows.Next() {
		var dateStr string
		var item domain.ActivityRow
		var tokens uint64
		if modelDetail {
			var providerID, modelID string
			var requestCount uint64
			if err := rows.Scan(&dateStr, &item.AgentID, &providerID, &modelID, &tokens, &requestCount); err != nil {
				return nil, err
			}
			item.ProviderID, item.ModelID = &providerID, &modelID
			messageCount := fmt.Sprintf("%d", requestCount)
			item.MessageCount = &messageCount
			item.GeneratedCodeLines = "0"
		} else {
			var messages, duration sql.NullInt64
			var codeLines uint64
			if err := rows.Scan(&dateStr, &item.AgentID, &tokens, &messages, &duration, &codeLines); err != nil {
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
		item.Date = dateStr
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
		SELECT DISTINCT harness_id
		FROM telemetry_model_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		ORDER BY harness_id ASC`, userID)
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
		SELECT DISTINCT tm.provider_id, tm.model_id
		FROM telemetry_model_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
		ORDER BY tm.provider_id ASC, tm.model_id ASC`, userID)
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
