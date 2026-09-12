package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tokendance/internal/domain"
)

type rawInterval struct {
	from time.Time
	to   time.Time
}

type utcAggregatePlan struct {
	fromDate string
	toDate   string
	hasFull  bool
	raw      []rawInterval
	loc      *time.Location
}

func planUTCAggregates(r domain.TimeRange) utcAggregatePlan {
	// daily_user_agent_metrics.metric_date is the product statistics day
	// (UTC+8). Personal summary, trends and breakdowns must use the same
	// buckets as the public board. In-progress days are read from those
	// precomputed rows; the request path does not SUM usage_events.
	from := r.From.In(domain.DayTZ)
	to := r.To.In(domain.DayTZ)
	plan := utcAggregatePlan{fromDate: "9999-12-31", toDate: "1000-01-01", loc: domain.DayTZ}
	if to.Before(from) {
		return plan
	}
	plan.hasFull = true
	plan.fromDate = from.Format("2006-01-02")
	plan.toDate = to.Format("2006-01-02")
	return plan
}

type rawSummary struct {
	rowCount      int64
	cost          float64
	tokens        uint64
	codeLines     uint64
	input         uint64
	output        uint64
	cacheRead     uint64
	cacheWrite    uint64
	reasoning     uint64
	duration      uint64
	messages      uint64
	userMessages  uint64
	maxReceivedAt sql.NullTime
}

func (s *analyticsStore) queryRawSummary(ctx context.Context, userID string, intervals []rawInterval) (rawSummary, error) {
	var total rawSummary
	const query = `
		SELECT COUNT(*), COALESCE(SUM(CASE WHEN JSON_EXTRACT(e.safe_extension_json,'$.openrouter') IS NOT NULL
 AND e.turn_hash IS NOT NULL AND EXISTS(SELECT 1 FROM usage_events reported WHERE reported.user_id=e.user_id
 AND reported.agent_id=e.agent_id AND reported.turn_hash=e.turn_hash AND reported.occurred_date=e.occurred_date
 AND reported.cost_source='provider_reported' AND reported.cost_amount IS NOT NULL) THEN 0 ELSE e.cost_amount END),0),
		       COALESCE(SUM(COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0))), 0),
		       COALESCE(SUM(COALESCE(code_generated_lines, 0)), 0),
		       COALESCE(SUM(COALESCE(token_input, 0)), 0),
		       COALESCE(SUM(COALESCE(token_output, 0)), 0),
		       COALESCE(SUM(COALESCE(token_cache_read, 0)), 0),
		       COALESCE(SUM(COALESCE(token_cache_write, 0)), 0),
		       COALESCE(SUM(COALESCE(token_reasoning, 0)), 0),
		       COALESCE(SUM(CASE WHEN e.event_type='session_ended' AND e.parent_session_hash IS NULL THEN COALESCE(e.duration_ms,0)
 WHEN e.event_type='turn_completed' AND NOT EXISTS(SELECT 1 FROM usage_events ended WHERE ended.user_id=e.user_id
 AND ended.agent_id=e.agent_id AND ended.session_hash=e.session_hash AND ended.occurred_date=e.occurred_date
 AND ended.event_type='session_ended' AND ended.parent_session_hash IS NULL AND ended.duration_ms IS NOT NULL) THEN COALESCE(e.duration_ms,0) ELSE 0 END),0),
		       COALESCE(SUM(event_type IN ('turn_started','turn_completed')), 0),
		       COALESCE(SUM(event_type = 'turn_started' AND turn_trigger = 'user'), 0),
		       MAX(received_at)
		FROM usage_events e
		WHERE user_id = ? AND occurred_at >= ? AND occurred_at < ?
		  AND accuracy IN ('exact', 'derived')`
	for _, interval := range intervals {
		var part rawSummary
		if err := s.db.QueryRowContext(ctx, query, userID, interval.from, interval.to).Scan(
			&part.rowCount, &part.cost, &part.tokens, &part.codeLines,
			&part.input, &part.output, &part.cacheRead, &part.cacheWrite, &part.reasoning,
			&part.duration, &part.messages, &part.userMessages, &part.maxReceivedAt,
		); err != nil {
			return rawSummary{}, fmt.Errorf("failed to query usage event boundary summary: %w", err)
		}
		total.rowCount += part.rowCount
		total.cost += part.cost
		total.tokens += part.tokens
		total.codeLines += part.codeLines
		total.input += part.input
		total.output += part.output
		total.cacheRead += part.cacheRead
		total.cacheWrite += part.cacheWrite
		total.reasoning += part.reasoning
		total.duration += part.duration
		total.messages += part.messages
		total.userMessages += part.userMessages
		if part.maxReceivedAt.Valid && (!total.maxReceivedAt.Valid || part.maxReceivedAt.Time.After(total.maxReceivedAt.Time)) {
			total.maxReceivedAt = part.maxReceivedAt
		}
	}
	return total, nil
}

func addNullInt64(value *sql.NullInt64, delta uint64) {
	if !value.Valid {
		value.Valid = true
	}
	value.Int64 += int64(delta)
}

func maxNullTime(current *sql.NullTime, candidate sql.NullTime) {
	if candidate.Valid && (!current.Valid || candidate.Time.After(current.Time)) {
		*current = candidate
	}
}

type rawTokenPoint struct {
	date       string
	total      uint64
	input      uint64
	output     uint64
	cacheRead  uint64
	cacheWrite uint64
	reasoning  uint64
	watermark  sql.NullTime
}

func (s *analyticsStore) queryRawTokenPoints(ctx context.Context, userID, timezone string, intervals []rawInterval, agentID, providerID, modelID *string) ([]rawTokenPoint, error) {
	points := make(map[string]*rawTokenPoint)
	for _, interval := range intervals {
		query := `SELECT DATE_FORMAT(CONVERT_TZ(occurred_at, '+00:00', '` + domain.DayTZOffset + `'), '%Y-%m-%d'),
			COALESCE(SUM(COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0))), 0),
			COALESCE(SUM(COALESCE(token_input, 0)), 0),
			COALESCE(SUM(COALESCE(token_output, 0)), 0),
			COALESCE(SUM(COALESCE(token_cache_read, 0)), 0),
			COALESCE(SUM(COALESCE(token_cache_write, 0)), 0),
			COALESCE(SUM(COALESCE(token_reasoning, 0)), 0), MAX(received_at)
			FROM usage_events WHERE user_id = ? AND occurred_at >= ? AND occurred_at < ?
			AND accuracy IN ('exact', 'derived')`
		args := []interface{}{userID, interval.from, interval.to}
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
		query += " GROUP BY DATE_FORMAT(CONVERT_TZ(occurred_at, '+00:00', '" + domain.DayTZOffset + "'), '%Y-%m-%d')"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("failed to query usage event boundary trend: %w", err)
		}
		for rows.Next() {
			var part rawTokenPoint
			if err := rows.Scan(&part.date, &part.total, &part.input, &part.output, &part.cacheRead, &part.cacheWrite, &part.reasoning, &part.watermark); err != nil {
				rows.Close()
				return nil, err
			}
			point := points[part.date]
			if point == nil {
				point = &rawTokenPoint{date: part.date}
				points[part.date] = point
			}
			point.total += part.total
			point.input += part.input
			point.output += part.output
			point.cacheRead += part.cacheRead
			point.cacheWrite += part.cacheWrite
			point.reasoning += part.reasoning
			maxNullTime(&point.watermark, part.watermark)
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	out := make([]rawTokenPoint, 0, len(points))
	for _, point := range points {
		out = append(out, *point)
	}
	return out, nil
}

type rawBreakdownItem struct {
	key       string
	tokens    uint64
	watermark sql.NullTime
}

func (s *analyticsStore) queryRawBreakdown(ctx context.Context, userID string, intervals []rawInterval, dimension string) ([]rawBreakdownItem, error) {
	if dimension != "agent_id" && dimension != "model_id" {
		return nil, domain.ErrInvalidArgument
	}
	items := make(map[string]*rawBreakdownItem)
	for _, interval := range intervals {
		query := "SELECT COALESCE(" + dimension + ", ''), COALESCE(SUM(COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0))), 0), MAX(received_at) FROM usage_events WHERE user_id = ? AND occurred_at >= ? AND occurred_at < ? AND accuracy IN ('exact', 'derived') GROUP BY " + dimension
		rows, err := s.db.QueryContext(ctx, query, userID, interval.from, interval.to)
		if err != nil {
			return nil, fmt.Errorf("failed to query usage event boundary breakdown: %w", err)
		}
		for rows.Next() {
			var part rawBreakdownItem
			if err := rows.Scan(&part.key, &part.tokens, &part.watermark); err != nil {
				rows.Close()
				return nil, err
			}
			if part.key == "" {
				continue
			}
			item := items[part.key]
			if item == nil {
				item = &rawBreakdownItem{key: part.key}
				items[part.key] = item
			}
			item.tokens += part.tokens
			maxNullTime(&item.watermark, part.watermark)
		}
		rows.Close()
	}
	out := make([]rawBreakdownItem, 0, len(items))
	for _, item := range items {
		out = append(out, *item)
	}
	return out, nil
}
