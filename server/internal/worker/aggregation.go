package worker

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"tokendance/internal/crypto"
	mysqlstore "tokendance/internal/store/mysql"
)

const aggregationVersion = 2
const aggregationLockName = "tokendance_aggregate_rebuild"

func placeholders(count int) string {
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func aggregateArgs(userID string, dates []string, repeats int) []interface{} {
	args := make([]interface{}, 0, repeats*(len(dates)+1))
	for i := 0; i < repeats; i++ {
		args = append(args, userID)
		for _, date := range dates {
			args = append(args, date)
		}
	}
	return args
}

func canonicalAggregateStatements(datePlaceholders string) []struct {
	query   string
	repeats int
} {
	filter := "user_id = ? AND occurred_date IN (" + datePlaceholders + ") AND NOT EXISTS (SELECT 1 FROM device_daily_aggregates da WHERE da.installation_id=usage_events.installation_id AND da.metric_date=usage_events.occurred_date)"
	return []struct {
		query   string
		repeats int
	}{
		{query: `
			INSERT INTO daily_user_agent_metrics (
				metric_date, user_id, agent_id, exact_token_total, derived_token_total,
				estimated_token_total, session_count, child_session_count,
				interaction_turn_count, model_request_count, tool_call_count, skill_use_count,
				code_generated_lines, code_accepted_lines, correlated_code_lines,
				cost_amount, cost_currency, source_max_event_pk, aggregation_version,
				computed_at, updated_at, token_input_total, token_output_total,
				token_cache_read_total, token_cache_write_total, token_reasoning_total,
				active_duration_ms, message_count, user_message_count
			)
			WITH filtered AS (
				SELECT * FROM usage_events WHERE ` + filter + `
			), base AS (
				SELECT occurred_date, user_id, agent_id,
					SUM(CASE WHEN accuracy = 'exact' THEN COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0)) ELSE 0 END) exact_tokens,
					SUM(CASE WHEN accuracy = 'derived' THEN COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0)) ELSE 0 END) derived_tokens,
					SUM(CASE WHEN accuracy = 'estimated' THEN COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0)) ELSE 0 END) estimated_tokens,
					COUNT(DISTINCT CASE WHEN parent_session_hash IS NULL THEN session_hash END) session_count,
					COUNT(DISTINCT CASE WHEN parent_session_hash IS NOT NULL THEN session_hash END) child_session_count,
					COUNT(DISTINCT turn_hash) interaction_turn_count,
					SUM(event_type = 'model_usage_recorded') model_request_count,
					SUM(event_type = 'tool_invoked') tool_call_count,
					SUM(event_type = 'skill_invoked') skill_use_count,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(code_generated_lines, 0) ELSE 0 END) code_generated_lines,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(code_accepted_lines, 0) ELSE 0 END) code_accepted_lines,
					SUM(CASE WHEN accuracy = 'correlated' THEN COALESCE(code_generated_lines, 0) ELSE 0 END) correlated_code_lines,
					MAX(event_pk) source_max_event_pk,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_input, 0) ELSE 0 END) token_input_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_output, 0) ELSE 0 END) token_output_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_cache_read, 0) ELSE 0 END) token_cache_read_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_cache_write, 0) ELSE 0 END) token_cache_write_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_reasoning, 0) ELSE 0 END) token_reasoning_total
				FROM filtered GROUP BY occurred_date, user_id, agent_id
			), turns AS (
				SELECT occurred_date, user_id, agent_id, session_hash, turn_hash,
					MAX(event_type = 'turn_started') has_started,
					MAX(event_type = 'turn_completed') has_completed,
					MAX(event_type = 'turn_started' AND turn_trigger = 'user') has_user_start,
					MAX(CASE WHEN event_type = 'turn_completed' THEN duration_ms END) turn_duration_ms
				FROM filtered WHERE turn_hash IS NOT NULL
				GROUP BY occurred_date, user_id, agent_id, session_hash, turn_hash
			), sessions AS (
				SELECT occurred_date, user_id, agent_id, session_hash,
					MAX(CASE WHEN event_type = 'session_ended' AND parent_session_hash IS NULL THEN duration_ms END) session_duration_ms
				FROM filtered WHERE session_hash IS NOT NULL
				GROUP BY occurred_date, user_id, agent_id, session_hash
			), activity AS (
				SELECT t.occurred_date, t.user_id, t.agent_id,
					SUM(t.has_started + t.has_completed) message_count,
					SUM(t.has_user_start) user_message_count,
					SUM(CASE WHEN s.session_duration_ms IS NULL THEN COALESCE(t.turn_duration_ms, 0) ELSE 0 END) turn_fallback_ms
				FROM turns t LEFT JOIN sessions s
				  ON s.occurred_date = t.occurred_date AND s.user_id = t.user_id
				 AND s.agent_id = t.agent_id AND s.session_hash = t.session_hash
				GROUP BY t.occurred_date, t.user_id, t.agent_id
			), session_activity AS (
				SELECT occurred_date, user_id, agent_id, SUM(COALESCE(session_duration_ms, 0)) session_duration_ms
				FROM (
					SELECT occurred_date, user_id, agent_id, session_hash,
						MAX(CASE WHEN event_type = 'session_ended' AND parent_session_hash IS NULL THEN duration_ms END) session_duration_ms
					FROM filtered WHERE session_hash IS NOT NULL
					GROUP BY occurred_date, user_id, agent_id, session_hash
				) session_rows
				GROUP BY occurred_date, user_id, agent_id
			), cost_ranked AS (
				SELECT occurred_date, user_id, agent_id, cost_amount, cost_currency,
					ROW_NUMBER() OVER (
						PARTITION BY occurred_date, user_id, agent_id, CASE WHEN JSON_EXTRACT(safe_extension_json, '$.openrouter') IS NOT NULL THEN event_id ELSE COALESCE(turn_hash, event_id) END
						ORDER BY (cost_source = 'provider_reported') DESC, event_pk DESC
					) authority_rank
				FROM filtered f WHERE cost_amount IS NOT NULL
 AND (JSON_EXTRACT(f.safe_extension_json, '$.openrouter') IS NULL OR f.turn_hash IS NULL OR NOT EXISTS (
 SELECT 1 FROM filtered reported WHERE reported.user_id=f.user_id AND reported.agent_id=f.agent_id
 AND reported.turn_hash=f.turn_hash AND reported.cost_source='provider_reported' AND reported.cost_amount IS NOT NULL))
			), costs AS (
				SELECT occurred_date, user_id, agent_id, SUM(cost_amount) cost_amount, MAX(cost_currency) cost_currency
				FROM cost_ranked WHERE authority_rank = 1 GROUP BY occurred_date, user_id, agent_id
			)
			SELECT b.occurred_date, b.user_id, b.agent_id,
				MAX(b.exact_tokens), MAX(b.derived_tokens), MAX(b.estimated_tokens),
				MAX(b.session_count), MAX(b.child_session_count),
				MAX(b.interaction_turn_count), MAX(b.model_request_count), MAX(b.tool_call_count), MAX(b.skill_use_count),
				MAX(b.code_generated_lines), MAX(b.code_accepted_lines), MAX(b.correlated_code_lines),
				COALESCE(MAX(c.cost_amount), 0), COALESCE(MAX(c.cost_currency), 'USD'), MAX(b.source_max_event_pk), ` + fmt.Sprint(aggregationVersion) + `,
				CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3), MAX(b.token_input_total), MAX(b.token_output_total),
				MAX(b.token_cache_read_total), MAX(b.token_cache_write_total), MAX(b.token_reasoning_total),
				COALESCE(MAX(sa.session_duration_ms), 0) + COALESCE(MAX(a.turn_fallback_ms), 0),
				COALESCE(MAX(a.message_count), 0), COALESCE(MAX(a.user_message_count), 0)
			FROM base b
			LEFT JOIN activity a ON a.occurred_date = b.occurred_date AND a.user_id = b.user_id AND a.agent_id = b.agent_id
			LEFT JOIN session_activity sa ON sa.occurred_date = b.occurred_date AND sa.user_id = b.user_id AND sa.agent_id = b.agent_id
			LEFT JOIN costs c ON c.occurred_date = b.occurred_date AND c.user_id = b.user_id AND c.agent_id = b.agent_id
			GROUP BY b.occurred_date, b.user_id, b.agent_id
			ON DUPLICATE KEY UPDATE
				exact_token_total = VALUES(exact_token_total),
				derived_token_total = VALUES(derived_token_total),
				estimated_token_total = VALUES(estimated_token_total),
				session_count = VALUES(session_count),
				child_session_count = VALUES(child_session_count),
				interaction_turn_count = VALUES(interaction_turn_count),
				model_request_count = VALUES(model_request_count),
				tool_call_count = VALUES(tool_call_count),
				skill_use_count = VALUES(skill_use_count),
				code_generated_lines = VALUES(code_generated_lines),
				code_accepted_lines = VALUES(code_accepted_lines),
				correlated_code_lines = VALUES(correlated_code_lines),
				cost_amount = VALUES(cost_amount),
				cost_currency = VALUES(cost_currency),
				source_max_event_pk = VALUES(source_max_event_pk),
				aggregation_version = VALUES(aggregation_version),
				computed_at = VALUES(computed_at),
				updated_at = VALUES(updated_at),
				token_input_total = VALUES(token_input_total),
				token_output_total = VALUES(token_output_total),
				token_cache_read_total = VALUES(token_cache_read_total),
				token_cache_write_total = VALUES(token_cache_write_total),
				token_reasoning_total = VALUES(token_reasoning_total),
				active_duration_ms = VALUES(active_duration_ms),
				message_count = VALUES(message_count),
				user_message_count = VALUES(user_message_count)`, repeats: 1},
		{query: `
			INSERT INTO daily_user_agent_model_metrics (
				metric_date, user_id, agent_id, provider_id, model_id,
				exact_token_total, derived_token_total, estimated_token_total,
				model_request_count, cost_amount, cost_currency, source_max_event_pk,
				aggregation_version, computed_at, updated_at, token_input_total,
				token_output_total, token_cache_read_total, token_cache_write_total,
				token_reasoning_total
			)
			WITH filtered AS (
				SELECT * FROM usage_events WHERE ` + filter + ` AND provider_id IS NOT NULL AND model_id IS NOT NULL
			), base AS (
				SELECT occurred_date, user_id, agent_id, provider_id, model_id,
					SUM(CASE WHEN accuracy = 'exact' THEN COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0)) ELSE 0 END) exact_tokens,
					SUM(CASE WHEN accuracy = 'derived' THEN COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0)) ELSE 0 END) derived_tokens,
					SUM(CASE WHEN accuracy = 'estimated' THEN COALESCE(token_total, COALESCE(token_input, 0) + COALESCE(token_output, 0) + COALESCE(token_cache_read, 0) + COALESCE(token_cache_write, 0) + COALESCE(token_reasoning, 0)) ELSE 0 END) estimated_tokens,
					SUM(event_type = 'model_usage_recorded') model_request_count,
					MAX(event_pk) source_max_event_pk,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_input, 0) ELSE 0 END) token_input_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_output, 0) ELSE 0 END) token_output_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_cache_read, 0) ELSE 0 END) token_cache_read_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_cache_write, 0) ELSE 0 END) token_cache_write_total,
					SUM(CASE WHEN accuracy IN ('exact', 'derived') THEN COALESCE(token_reasoning, 0) ELSE 0 END) token_reasoning_total
				FROM filtered GROUP BY occurred_date, user_id, agent_id, provider_id, model_id
			), cost_ranked AS (
				SELECT occurred_date, user_id, agent_id, provider_id, model_id, cost_amount, cost_currency,
					ROW_NUMBER() OVER (
						PARTITION BY occurred_date, user_id, agent_id, provider_id, model_id, CASE WHEN JSON_EXTRACT(safe_extension_json, '$.openrouter') IS NOT NULL THEN event_id ELSE COALESCE(turn_hash, event_id) END
						ORDER BY (cost_source = 'provider_reported') DESC, event_pk DESC
					) authority_rank
				FROM filtered f WHERE cost_amount IS NOT NULL
 AND (JSON_EXTRACT(f.safe_extension_json, '$.openrouter') IS NULL OR f.turn_hash IS NULL OR NOT EXISTS (
 SELECT 1 FROM filtered reported WHERE reported.user_id=f.user_id AND reported.agent_id=f.agent_id
 AND reported.turn_hash=f.turn_hash AND reported.cost_source='provider_reported' AND reported.cost_amount IS NOT NULL))
			), costs AS (
				SELECT occurred_date, user_id, agent_id, provider_id, model_id, SUM(cost_amount) cost_amount, MAX(cost_currency) cost_currency
				FROM cost_ranked WHERE authority_rank = 1
				GROUP BY occurred_date, user_id, agent_id, provider_id, model_id
			)
			SELECT b.occurred_date, b.user_id, b.agent_id, b.provider_id, b.model_id,
				MAX(b.exact_tokens), MAX(b.derived_tokens), MAX(b.estimated_tokens), MAX(b.model_request_count),
				COALESCE(MAX(c.cost_amount), 0), COALESCE(MAX(c.cost_currency), 'USD'), MAX(b.source_max_event_pk), ` + fmt.Sprint(aggregationVersion) + `,
				CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3), MAX(b.token_input_total), MAX(b.token_output_total),
				MAX(b.token_cache_read_total), MAX(b.token_cache_write_total), MAX(b.token_reasoning_total)
			FROM base b LEFT JOIN costs c
			  ON c.occurred_date = b.occurred_date AND c.user_id = b.user_id AND c.agent_id = b.agent_id
			 AND c.provider_id = b.provider_id AND c.model_id = b.model_id
			GROUP BY b.occurred_date, b.user_id, b.agent_id, b.provider_id, b.model_id
			ON DUPLICATE KEY UPDATE
				exact_token_total = VALUES(exact_token_total),
				derived_token_total = VALUES(derived_token_total),
				estimated_token_total = VALUES(estimated_token_total),
				model_request_count = VALUES(model_request_count),
				cost_amount = VALUES(cost_amount),
				cost_currency = VALUES(cost_currency),
				source_max_event_pk = VALUES(source_max_event_pk),
				aggregation_version = VALUES(aggregation_version),
				computed_at = VALUES(computed_at),
				updated_at = VALUES(updated_at),
				token_input_total = VALUES(token_input_total),
				token_output_total = VALUES(token_output_total),
				token_cache_read_total = VALUES(token_cache_read_total),
				token_cache_write_total = VALUES(token_cache_write_total),
				token_reasoning_total = VALUES(token_reasoning_total)`, repeats: 1},
		{query: `
			INSERT INTO daily_skill_metrics (
				metric_date, user_id, agent_id, skill_key, skill_public_name, use_count,
				exact_use_count, derived_use_count, correlated_use_count, estimated_use_count,
				success_count, failure_count, duration_ms, source_max_event_pk,
				aggregation_version, computed_at, updated_at
			)
			SELECT occurred_date, user_id, agent_id, skill_key, MAX(skill_public_name), COUNT(*),
				SUM(accuracy = 'exact'), SUM(accuracy = 'derived'), SUM(accuracy = 'correlated'),
				SUM(accuracy = 'estimated'), SUM(success = TRUE), SUM(success = FALSE),
				COALESCE(SUM(duration_ms), 0), MAX(event_pk), ` + fmt.Sprint(aggregationVersion) + `,
				CURRENT_TIMESTAMP(3), CURRENT_TIMESTAMP(3)
			FROM usage_events WHERE ` + filter + `
			  AND event_type = 'skill_invoked' AND skill_key IS NOT NULL
			GROUP BY occurred_date, user_id, agent_id, skill_key
			ON DUPLICATE KEY UPDATE
				skill_public_name = VALUES(skill_public_name),
				use_count = VALUES(use_count),
				exact_use_count = VALUES(exact_use_count),
				derived_use_count = VALUES(derived_use_count),
				correlated_use_count = VALUES(correlated_use_count),
				estimated_use_count = VALUES(estimated_use_count),
				success_count = VALUES(success_count),
				failure_count = VALUES(failure_count),
				duration_ms = VALUES(duration_ms),
				source_max_event_pk = VALUES(source_max_event_pk),
				aggregation_version = VALUES(aggregation_version),
				computed_at = VALUES(computed_at),
				updated_at = VALUES(updated_at)`, repeats: 1},
	}
}

func rebuildUserAggregates(ctx context.Context, tx *sql.Tx, userID string, affectedDates []string) error {
	if len(affectedDates) == 0 {
		return nil
	}
	sort.Strings(affectedDates)
	datePlaceholders := placeholders(len(affectedDates))
	args := aggregateArgs(userID, affectedDates, 1)
	for _, table := range []string{"daily_user_agent_metrics", "daily_user_agent_model_metrics", "daily_skill_metrics"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE user_id = ? AND metric_date IN ("+datePlaceholders+")", args...); err != nil {
			return fmt.Errorf("clear %s before rebuild: %w", table, err)
		}
	}
	for _, statement := range canonicalAggregateStatements(datePlaceholders) {
		if _, err := tx.ExecContext(ctx, statement.query, aggregateArgs(userID, affectedDates, statement.repeats)...); err != nil {
			return fmt.Errorf("rebuild user aggregates: %w", err)
		}
	}
	return applyDeviceAggregates(ctx, tx, userID, affectedDates)
}

func (w *Worker) ProcessAggregates(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return 0, fmt.Errorf("open aggregate connection: %w", err)
	}
	defer conn.Close()

	var acquired int
	if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 30)`, aggregationLockName).Scan(&acquired); err != nil {
		return 0, fmt.Errorf("acquire aggregate rebuild lock: %w", err)
	}
	if acquired != 1 {
		return 0, fmt.Errorf("aggregate rebuild lock is busy")
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(releaseCtx, `SELECT RELEASE_LOCK(?)`, aggregationLockName)
	}()

	rows, err := conn.QueryContext(ctx, `
		SELECT DISTINCT e.user_id, DATE_FORMAT(e.occurred_date, '%Y-%m-%d') AS metric_date
		FROM usage_events e
		LEFT JOIN (
			SELECT metric_date, user_id, MAX(source_max_event_pk) source_max_event_pk
			FROM daily_user_agent_metrics GROUP BY metric_date, user_id
		) a ON a.metric_date = e.occurred_date AND a.user_id = e.user_id
		WHERE e.event_pk > COALESCE(a.source_max_event_pk, 0)
		  AND NOT EXISTS (SELECT 1 FROM device_daily_aggregates da WHERE da.installation_id=e.installation_id AND da.metric_date=e.occurred_date)
		ORDER BY e.user_id, metric_date
		LIMIT 500`)
	if err != nil {
		return 0, fmt.Errorf("find aggregate rebuild targets: %w", err)
	}
	defer rows.Close()
	targets := make(map[string][]string)
	for rows.Next() {
		var userID, date string
		if err := rows.Scan(&userID, &date); err != nil {
			return 0, fmt.Errorf("scan aggregate rebuild target: %w", err)
		}
		targets[userID] = append(targets[userID], date)
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate aggregate rebuild targets: %w", err)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close aggregate rebuild targets: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin aggregate rebuild: %w", err)
	}
	defer tx.Rollback()

	now := w.clk.Now()
	for userID, dates := range targets {
		for _, date := range dates {
			if err := mysqlstore.MarkAggregateDirtyDayTx(ctx, tx, userID, date, now); err != nil {
				return 0, err
			}
		}
	}
	dirtyTargets, err := mysqlstore.ListPendingDirtyDaysTx(ctx, tx, now, 500)
	if err != nil {
		return 0, err
	}
	for userID, dates := range dirtyTargets {
		targets[userID] = mergeDates(targets[userID], dates)
	}
	if len(targets) == 0 {
		if err := mysqlstore.PruneOldWindowScoresTx(ctx, tx, now); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("commit empty aggregate rebuild: %w", err)
		}
		if _, err := mysqlstore.BackfillWindowScores(ctx, w.db, now, 100); err != nil {
			return 0, err
		}
		return 0, nil
	}

	allDates := make(map[string]struct{})
	processed := 0
	for userID, dates := range targets {
		if err := rebuildUserAggregates(ctx, tx, userID, dates); err != nil {
			return 0, err
		}
		if err := mysqlstore.UpsertCurrentWindowScoresTx(ctx, tx, userID, now); err != nil {
			return 0, err
		}
		if err := mysqlstore.ClearAggregateDirtyDaysTx(ctx, tx, userID, dates, now); err != nil {
			return 0, err
		}
		processed += len(dates)
		for _, date := range dates {
			allDates[date] = struct{}{}
		}
	}
	if err := rebuildPublishedLeaderboards(ctx, tx, mapKeys(allDates), now); err != nil {
		return 0, err
	}
	if err := mysqlstore.PruneOldWindowScoresTx(ctx, tx, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit aggregate rebuild: %w", err)
	}
	if _, err := mysqlstore.BackfillWindowScores(ctx, w.db, now, 100); err != nil {
		return processed, err
	}
	return processed, nil
}

func (w *Worker) BackfillWindowScores(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	return mysqlstore.BackfillWindowScores(ctx, w.db, w.clk.Now(), 200)
}

func mergeDates(existing, extra []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(extra))
	out := make([]string, 0, len(existing)+len(extra))
	for _, date := range existing {
		if _, ok := seen[date]; ok {
			continue
		}
		seen[date] = struct{}{}
		out = append(out, date)
	}
	for _, date := range extra {
		if _, ok := seen[date]; ok {
			continue
		}
		seen[date] = struct{}{}
		out = append(out, date)
	}
	sort.Strings(out)
	return out
}

func mapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	return keys
}

func leaderboardMetricExpression(metric string) (string, bool) {
	switch metric {
	case "tokens":
		return "SUM(m.exact_token_total + m.derived_token_total)", true
	case "cost":
		return "SUM(m.cost_amount)", true
	case "code_lines":
		return "SUM(m.code_generated_lines)", true
	case "messages":
		return "SUM(m.message_count)", true
	case "duration":
		return "SUM(m.active_duration_ms)", true
	case "sessions":
		return "SUM(m.session_count)", true
	case "turns":
		return "SUM(m.interaction_turn_count)", true
	case "tools":
		return "SUM(m.tool_call_count)", true
	case "skills":
		return "SUM(m.skill_use_count)", true
	default:
		return "", false
	}
}

func newLeaderboardSnapshotID() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", fmt.Errorf("generate leaderboard snapshot id: %w", err)
	}
	return "snp_" + token, nil
}

func rebuildPublishedLeaderboards(ctx context.Context, tx *sql.Tx, affectedDates []string, now time.Time) error {
	if len(affectedDates) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT snapshot_id, board_key, scope_type, scope_key, metric_key,
			window_start, window_end, timezone_name, ranking_rule_version
		FROM leaderboard_snapshots
		WHERE snapshot_status = 'published'
		FOR UPDATE`)
	if err != nil {
		return fmt.Errorf("lock published leaderboard snapshots: %w", err)
	}
	type snapshot struct {
		id, board, scopeType, scopeKey, metric, timezone string
		start, end                                       time.Time
		ruleVersion                                      uint32
	}
	var snapshots []snapshot
	for rows.Next() {
		var item snapshot
		if err := rows.Scan(
			&item.id, &item.board, &item.scopeType, &item.scopeKey, &item.metric,
			&item.start, &item.end, &item.timezone, &item.ruleVersion,
		); err != nil {
			rows.Close()
			return fmt.Errorf("scan published leaderboard snapshot: %w", err)
		}
		for _, date := range affectedDates {
			if date >= item.start.UTC().Format("2006-01-02") && date <= item.end.UTC().Format("2006-01-02") {
				snapshots = append(snapshots, item)
				break
			}
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close published leaderboard snapshots: %w", err)
	}

	for _, item := range snapshots {
		expression, supported := leaderboardMetricExpression(item.metric)
		if !supported {
			continue
		}
		newID, err := newLeaderboardSnapshotID()
		if err != nil {
			return err
		}
		var nextRuleVersion uint32
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(MAX(ranking_rule_version), 0) + 1
			FROM leaderboard_snapshots
			WHERE board_key = ? AND scope_type = ? AND scope_key = ? AND metric_key = ?
			  AND window_start = ? AND window_end = ?`,
			item.board, item.scopeType, item.scopeKey, item.metric, item.start, item.end,
		).Scan(&nextRuleVersion); err != nil {
			return fmt.Errorf("select next leaderboard snapshot revision for %s: %w", item.id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO leaderboard_snapshots (
				snapshot_id, board_key, scope_type, scope_key, metric_key,
				window_start, window_end, timezone_name, ranking_rule_version,
				participant_count, source_max_event_pk, data_watermark_at,
				snapshot_status, generated_at, published_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, ?, 'building', ?, NULL)`,
			newID, item.board, item.scopeType, item.scopeKey, item.metric,
			item.start, item.end, item.timezone, nextRuleVersion, now, now,
		); err != nil {
			return fmt.Errorf("create leaderboard snapshot revision for %s: %w", item.id, err)
		}

		participantJoins := ""
		oldEntryJoin := "LEFT JOIN"
		scopeFilter := `
				  AND u.leaderboard_visibility = 'public'
				  AND p.profile_status = 'published' AND priv.public_profile_enabled = TRUE`
		displayExpr := "MAX(p.display_name)"
		avatarExpr := "MAX(p.avatar_url)"
		args := []interface{}{newID, item.id, item.start, item.end}
		switch item.scopeType {
		case "global":
			participantJoins = `
					JOIN public_user_profiles p ON p.user_id = m.user_id
					JOIN user_privacy_settings priv ON priv.user_id = m.user_id`
		case "team":
			oldEntryJoin = "JOIN"
			scopeFilter = " AND u.leaderboard_visibility IN ('team', 'public')"
			displayExpr = "MAX(old_entry.display_name_snapshot)"
			avatarExpr = "MAX(old_entry.avatar_url_snapshot)"
		case "private":
			oldEntryJoin = "JOIN"
			scopeFilter = " AND m.user_id = ?"
			displayExpr = "MAX(old_entry.display_name_snapshot)"
			avatarExpr = "MAX(old_entry.avatar_url_snapshot)"
			args = []interface{}{newID, item.id, item.start, item.end, item.scopeKey}
		default:
			return fmt.Errorf("unsupported leaderboard scope %q for snapshot %s", item.scopeType, item.id)
		}
		insert := `
			INSERT INTO leaderboard_entries (
				snapshot_id, rank_no, user_id, metric_value, previous_rank_no,
				display_name_snapshot, avatar_url_snapshot
			)
			SELECT ?, rebuilt.rank_no, rebuilt.user_id, rebuilt.metric_value,
				rebuilt.old_rank_no,
				rebuilt.display_name, rebuilt.avatar_url
			FROM (
				SELECT ranked.*, ROW_NUMBER() OVER (ORDER BY ranked.metric_value DESC, ranked.user_id ASC) rank_no
				FROM (
					SELECT m.user_id, ` + expression + ` metric_value,
						` + displayExpr + ` display_name, ` + avatarExpr + ` avatar_url,
						MAX(old_entry.rank_no) old_rank_no
					FROM daily_user_agent_metrics m
					JOIN users u ON u.user_id = m.user_id
					` + oldEntryJoin + ` leaderboard_entries old_entry ON old_entry.snapshot_id = ? AND old_entry.user_id = m.user_id
					` + participantJoins + `
					WHERE m.metric_date >= DATE(?) AND m.metric_date <= DATE(?)
					  AND u.account_status = 'active'` + scopeFilter + `
					GROUP BY m.user_id
					HAVING metric_value > 0
				) ranked
			) rebuilt`
		if _, err := tx.ExecContext(ctx, insert, args...); err != nil {
			return fmt.Errorf("build leaderboard snapshot revision %s from %s: %w", newID, item.id, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE leaderboard_snapshots s
			SET participant_count = (SELECT COUNT(*) FROM leaderboard_entries e WHERE e.snapshot_id = s.snapshot_id),
				source_max_event_pk = COALESCE((
					SELECT MAX(m.source_max_event_pk)
					FROM daily_user_agent_metrics m
					JOIN leaderboard_entries e ON e.snapshot_id = s.snapshot_id AND e.user_id = m.user_id
					WHERE m.metric_date >= DATE(s.window_start) AND m.metric_date <= DATE(s.window_end)
				), 0),
				data_watermark_at = ?, generated_at = ?
			WHERE snapshot_id = ?`, now, now, newID); err != nil {
			return fmt.Errorf("finalize leaderboard snapshot revision %s: %w", newID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE leaderboard_snapshots
			SET snapshot_status = CASE snapshot_id WHEN ? THEN 'published' ELSE 'superseded' END,
				published_at = CASE snapshot_id WHEN ? THEN ? ELSE published_at END
			WHERE snapshot_id IN (?, ?)`, newID, newID, now, newID, item.id); err != nil {
			return fmt.Errorf("publish leaderboard snapshot revision %s: %w", newID, err)
		}
	}
	return nil
}
