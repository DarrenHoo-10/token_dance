package teammetrics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"tokendance/internal/domain"
)

// RefreshCurrentTeamDaysTx projects the user's personal day metrics into team static rows.
// dates nil/empty means all personal day buckets (join / full history). No joined_at cut.
func RefreshCurrentTeamDaysTx(ctx context.Context, tx *sql.Tx, userID string, dates []string, nowMs int64) error {
	var teamID, membershipID, contribKey string
	var retired sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT c.team_id, c.membership_id
		FROM user_current_teams c
		INNER JOIN users u ON u.user_id = c.user_id
		INNER JOIN teams t ON t.team_id = c.team_id AND t.status = 'active'
		WHERE c.user_id = ? AND u.account_status = 'active'
		FOR UPDATE`, userID,
	).Scan(&teamID, &membershipID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock current team occupancy: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT team_id FROM teams WHERE team_id = ? FOR UPDATE`, teamID); err != nil {
		return fmt.Errorf("lock team: %w", err)
	}
	err = tx.QueryRowContext(ctx, `
		SELECT contributor_key, retired_at FROM team_usage_contributors
		WHERE team_id = ? AND user_id = ? AND delete_at IS NULL
		FOR UPDATE`, teamID, userID,
	).Scan(&contribKey, &retired)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock contributor: %w", err)
	}
	contrib := Contributor{TeamID: teamID, UserID: userID, ContributorKey: contribKey, MembershipID: &membershipID}

	if len(dates) == 0 {
		discovered, err := discoverPersonalDays(ctx, tx, userID)
		if err != nil {
			return err
		}
		dates = discovered
	}
	if len(dates) == 0 {
		return ReplaceTeamMemberDaysTx(ctx, tx, contrib, nil, nil, nowMs)
	}
	rows, err := projectPersonalDays(ctx, tx, contrib, dates)
	if err != nil {
		return err
	}
	return ReplaceTeamMemberDaysTx(ctx, tx, contrib, dates, rows, nowMs)
}

func discoverPersonalDays(ctx context.Context, tx *sql.Tx, userID string) ([]string, error) {
	seen := map[string]struct{}{}
	q := func(query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				return err
			}
			if d != "" {
				seen[d] = struct{}{}
			}
		}
		return rows.Err()
	}
	if err := q(`
		SELECT DISTINCT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d')
		FROM telemetry_model_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, userID); err != nil {
		return nil, err
	}
	if err := q(`
		SELECT DISTINCT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d')
		FROM telemetry_harness_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, userID); err != nil {
		return nil, err
	}
	if err := q(`
		SELECT DISTINCT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d')
		FROM telemetry_skill_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, userID); err != nil {
		return nil, err
	}
	if err := q(`
		SELECT DISTINCT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d')
		FROM telemetry_cost_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL`, userID); err != nil {
		return nil, err
	}
	if err := q(`
		SELECT DISTINCT DATE_FORMAT(metric_date, '%Y-%m-%d')
		FROM daily_user_agent_metrics WHERE user_id = ?`, userID); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	return out, nil
}

func projectPersonalDays(ctx context.Context, tx *sql.Tx, contrib Contributor, dates []string) ([]DayRow, error) {
	var out []DayRow
	in := strings.Repeat("?,", len(dates))
	in = in[:len(in)-1]
	dateArgs := datesToAny(dates)

	type usageKey struct{ date, agent, provider, model string }
	v2Days := map[usageKey]struct{}{}

	urows, err := tx.QueryContext(ctx, `
		SELECT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(m.bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d'),
		       m.harness_id, tm.provider_id, tm.model_id,
		       CAST(SUM(m.exact_token_total) AS CHAR), CAST(SUM(m.derived_token_total) AS CHAR),
		       CAST(SUM(m.usage_observed_count) AS CHAR),
		       CAST(SUM(m.input_context_tokens) AS CHAR), CAST(SUM(m.output_tokens) AS CHAR),
		       CAST(SUM(m.cache_eligible_input_tokens) AS CHAR), CAST(SUM(m.cache_eligible_read_tokens) AS CHAR),
		       CAST(SUM(m.cache_pair_known_count) AS CHAR),
		       CAST(SUM(m.input_context_known_count) AS CHAR), CAST(SUM(m.output_known_count) AS CHAR),
		       CAST(SUM(m.usage_observed_count) AS CHAR)
		FROM telemetry_model_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
		  AND DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(m.bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d') IN (`+in+`)
		GROUP BY 1, m.harness_id, tm.provider_id, tm.model_id`,
		append([]any{contrib.UserID}, dateArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("project model metrics: %w", err)
	}
	type usageScan struct {
		date, agent, provider, model, exact, derived, events, input, output, cacheIn, cacheRead, cacheKnown, inKnown, outKnown, observed string
	}
	var usages []usageScan
	for urows.Next() {
		var u usageScan
		if err := urows.Scan(&u.date, &u.agent, &u.provider, &u.model, &u.exact, &u.derived, &u.events, &u.input, &u.output, &u.cacheIn, &u.cacheRead, &u.cacheKnown, &u.inKnown, &u.outKnown, &u.observed); err != nil {
			urows.Close()
			return nil, err
		}
		usages = append(usages, u)
		v2Days[usageKey{u.date, u.agent, u.provider, u.model}] = struct{}{}
	}
	err = urows.Err()
	urows.Close()
	if err != nil {
		return nil, err
	}
	for _, u := range usages {
		hourly, err := loadHourly(ctx, tx, contrib.UserID, u.date, u.agent, u.provider, u.model)
		if err != nil {
			return nil, err
		}
		row, err := usageRow(contrib, u.date, u.agent, u.provider, u.model, u.exact, u.derived, u.events, u.input, u.output, u.cacheIn, u.cacheRead, u.cacheKnown, u.inKnown, u.outKnown, u.observed, hourly, false)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}

	lrows, err := tx.QueryContext(ctx, `
		SELECT DATE_FORMAT(metric_date, '%Y-%m-%d'), agent_id,
		       CAST(exact_token_total AS CHAR), CAST(derived_token_total AS CHAR)
		FROM daily_user_agent_metrics
		WHERE user_id = ? AND metric_date IN (`+in+`)`,
		append([]any{contrib.UserID}, dateArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("project legacy daily: %w", err)
	}
	defer lrows.Close()
	for lrows.Next() {
		var date, agent, exact, derived string
		if err := lrows.Scan(&date, &agent, &exact, &derived); err != nil {
			return nil, err
		}
		if _, ok := v2Days[usageKey{date, agent, "", ""}]; ok {
			continue
		}
		skip := false
		for k := range v2Days {
			if k.date == date && k.agent == agent {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		row, err := usageRow(contrib, date, agent, "", "", exact, derived, "0", "0", "0", "0", "0", "0", "0", "0", "0", defaultHourly(), true)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := lrows.Err(); err != nil {
		return nil, err
	}

	arows, err := tx.QueryContext(ctx, `
		SELECT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d'),
		       harness_id,
		       CAST(SUM(code_generated_lines) AS CHAR), CAST(SUM(active_duration_ms) AS CHAR),
		       CAST(SUM(turn_started_count + turn_completed_count) AS CHAR),
		       CAST(SUM(user_turn_started_count) AS CHAR),
		       CAST(SUM(code_known_count) AS CHAR), CAST(SUM(duration_known_count) AS CHAR),
		       CAST(SUM(message_known_count) AS CHAR)
		FROM telemetry_harness_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d') IN (`+in+`)
		GROUP BY 1, harness_id`,
		append([]any{contrib.UserID}, dateArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("project harness metrics: %w", err)
	}
	defer arows.Close()
	for arows.Next() {
		var date, agent, code, dur, msgs, userMsgs, codeKnown, durKnown, msgKnown string
		if err := arows.Scan(&date, &agent, &code, &dur, &msgs, &userMsgs, &codeKnown, &durKnown, &msgKnown); err != nil {
			return nil, err
		}
		row, err := activityRow(contrib, date, agent, code, dur, msgs, userMsgs, codeKnown, durKnown, msgKnown)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := arows.Err(); err != nil {
		return nil, err
	}

	crows, err := tx.QueryContext(ctx, `
		SELECT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(m.bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d'),
		       m.harness_id, tm.provider_id, tm.model_id, m.currency,
		       CAST(SUM(m.reported_cost_units)/100000000 AS CHAR),
		       CAST(SUM(m.estimated_cost_units)/100000000 AS CHAR)
		FROM telemetry_cost_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.user_id = ? AND m.grain = 'day' AND m.delete_at IS NULL
		  AND DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(m.bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d') IN (`+in+`)
		GROUP BY 1, m.harness_id, tm.provider_id, tm.model_id, m.currency`,
		append([]any{contrib.UserID}, dateArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("project cost metrics: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var date, agent, provider, model, currency, reported, estimated string
		if err := crows.Scan(&date, &agent, &provider, &model, &currency, &reported, &estimated); err != nil {
			return nil, err
		}
		row, err := costRow(contrib, date, agent, provider, model, currency, reported, estimated)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	srows, err := tx.QueryContext(ctx, `
		SELECT DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d'),
		       harness_id, skill_id,
		       CAST(SUM(use_count) AS CHAR), CAST(SUM(exact_use_count) AS CHAR),
		       CAST(SUM(derived_use_count) AS CHAR), CAST(SUM(correlated_use_count) AS CHAR),
		       CAST(SUM(success_count) AS CHAR), CAST(SUM(failure_count) AS CHAR),
		       CAST(SUM(duration_ms) AS CHAR), CAST(SUM(duration_known_count) AS CHAR)
		FROM telemetry_skill_metrics
		WHERE user_id = ? AND grain = 'day' AND delete_at IS NULL
		  AND DATE_FORMAT(DATE_ADD(FROM_UNIXTIME(bucket_start/1000), INTERVAL 8 HOUR), '%Y-%m-%d') IN (`+in+`)
		GROUP BY 1, harness_id, skill_id`,
		append([]any{contrib.UserID}, dateArgs...)...,
	)
	if err != nil {
		return nil, fmt.Errorf("project skill metrics: %w", err)
	}
	defer srows.Close()
	for srows.Next() {
		var date, agent, uses, exact, derived, corr, success, failure, dur, durKnown string
		var skillID int64
		if err := srows.Scan(&date, &agent, &skillID, &uses, &exact, &derived, &corr, &success, &failure, &dur, &durKnown); err != nil {
			return nil, err
		}
		row, err := skillRow(contrib, date, agent, skillID, uses, exact, derived, corr, success, failure, dur, durKnown)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, srows.Err()
}

func loadHourly(ctx context.Context, tx *sql.Tx, userID, date, agent, provider, model string) (jsonRaw, error) {
	startMs, err := domain.DayBucketStartMs(date)
	if err != nil {
		return defaultHourly(), nil
	}
	endMs := startMs + 24*60*60*1000
	rows, err := tx.QueryContext(ctx, `
		SELECT m.bucket_start, CAST(SUM(m.exact_token_total) AS CHAR), CAST(SUM(m.derived_token_total) AS CHAR)
		FROM telemetry_model_metrics m
		JOIN telemetry_models tm ON tm.id = m.model_key
		WHERE m.user_id = ? AND m.grain = 'hour' AND m.delete_at IS NULL
		  AND m.bucket_start >= ? AND m.bucket_start < ?
		  AND m.harness_id = ? AND tm.provider_id = ? AND tm.model_id = ?
		GROUP BY m.bucket_start
		ORDER BY m.bucket_start`,
		userID, startMs, endMs, agent, provider, model,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	h := hourlyV1{SchemaVersion: 1, Coverage: "complete", UnbucketedTokenTotal: "0", Buckets: []hourlyBucket{}}
	for rows.Next() {
		var start int64
		var exact, derived string
		if err := rows.Scan(&start, &exact, &derived); err != nil {
			return nil, err
		}
		h.Buckets = append(h.Buckets, hourlyBucket{StartMs: fmt.Sprintf("%d", start), Exact: exact, Derived: derived})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(h.Buckets) == 0 {
		h.Coverage = "none"
	}
	return mustJSON(h), nil
}

type jsonRaw = []byte

func usageRow(c Contributor, date, agent, provider, model, exact, derived, events, input, output, cacheIn, cacheRead, cacheKnown, inKnown, outKnown, observed string, hourly jsonRaw, legacy bool) (DayRow, error) {
	key, err := RowKey(c.ContributorKey, date, KindUsage, strPtr(agent), strPtr(provider), strPtr(model), nil, nil)
	if err != nil {
		return DayRow{}, err
	}
	res := resourcesV1{
		SchemaVersion:      1,
		InputContextTokens: coveredSum{Sum: strPtr(decOrZero(input)), Known: decOrZero(inKnown), Observed: decOrZero(observed)},
		OutputTokens:       coveredSum{Sum: strPtr(decOrZero(output)), Known: decOrZero(outKnown), Observed: decOrZero(observed)},
		CachePairs:         cachePairs{Input: decOrZero(cacheIn), Read: decOrZero(cacheRead), Known: decOrZero(cacheKnown), Observed: decOrZero(observed)},
	}
	q := defaultQuality(legacy)
	return DayRow{
		TeamID: c.TeamID, ContributorKey: c.ContributorKey, MetricDate: date, RowKey: key, MetricKind: KindUsage,
		AgentID: strPtr(agent), ProviderID: strPtr(provider), ModelID: strPtr(model),
		TokenExact: decOrZero(exact), TokenDerived: decOrZero(derived), UsageEventCount: decOrZero(events),
		ReportedCost: "0", EstimatedCost: "0", SkillUseCount: "0",
		SkillStats: emptyJSONObject(), Resources: mustJSON(res), Activity: emptyJSONObject(),
		Hourly: hourly, Quality: q, RuleVersion: RuleVersion,
	}, nil
}

func activityRow(c Contributor, date, agent, code, dur, msgs, userMsgs, codeKnown, durKnown, msgKnown string) (DayRow, error) {
	key, err := RowKey(c.ContributorKey, date, KindActivity, strPtr(agent), nil, nil, nil, nil)
	if err != nil {
		return DayRow{}, err
	}
	act := activityV1{
		SchemaVersion:      1,
		GeneratedCodeLines: coveredSum{Sum: strPtr(decOrZero(code)), Known: decOrZero(codeKnown), Observed: decOrZero(codeKnown)},
		ActiveDurationMs:   coveredSum{Sum: strPtr(decOrZero(dur)), Known: decOrZero(durKnown), Observed: decOrZero(durKnown)},
		MessageCount:       coveredSum{Sum: strPtr(decOrZero(msgs)), Known: decOrZero(msgKnown), Observed: decOrZero(msgKnown)},
		UserMessageCount:   coveredSum{Sum: strPtr(decOrZero(userMsgs)), Known: decOrZero(msgKnown), Observed: decOrZero(msgKnown)},
	}
	return DayRow{
		TeamID: c.TeamID, ContributorKey: c.ContributorKey, MetricDate: date, RowKey: key, MetricKind: KindActivity,
		AgentID: strPtr(agent), TokenExact: "0", TokenDerived: "0", UsageEventCount: "0",
		ReportedCost: "0", EstimatedCost: "0", SkillUseCount: "0",
		SkillStats: emptyJSONObject(), Resources: emptyJSONObject(), Activity: mustJSON(act),
		Hourly: emptyJSONObject(), Quality: defaultQuality(false), RuleVersion: RuleVersion,
	}, nil
}

func costRow(c Contributor, date, agent, provider, model, currency, reported, estimated string) (DayRow, error) {
	key, err := RowKey(c.ContributorKey, date, KindCost, strPtr(agent), strPtr(provider), strPtr(model), strPtr(currency), nil)
	if err != nil {
		return DayRow{}, err
	}
	return DayRow{
		TeamID: c.TeamID, ContributorKey: c.ContributorKey, MetricDate: date, RowKey: key, MetricKind: KindCost,
		AgentID: strPtr(agent), ProviderID: strPtr(provider), ModelID: strPtr(model), Currency: strPtr(currency),
		TokenExact: "0", TokenDerived: "0", UsageEventCount: "0",
		ReportedCost: decOrZero(reported), EstimatedCost: decOrZero(estimated), SkillUseCount: "0",
		SkillStats: emptyJSONObject(), Resources: emptyJSONObject(), Activity: emptyJSONObject(),
		Hourly: emptyJSONObject(), Quality: defaultQuality(false), RuleVersion: RuleVersion,
	}, nil
}

func skillRow(c Contributor, date, agent string, skillID int64, uses, exact, derived, corr, success, failure, dur, durKnown string) (DayRow, error) {
	sid := skillID
	key, err := RowKey(c.ContributorKey, date, KindSkill, strPtr(agent), nil, nil, nil, &sid)
	if err != nil {
		return DayRow{}, err
	}
	stats := skillStatsV1{
		SchemaVersion: 1, ExactCount: decOrZero(exact), DerivedCount: decOrZero(derived),
		CorrelatedCount: decOrZero(corr), EstimatedCount: "0", UnknownAccuracyCount: "0",
		SuccessCount: decOrZero(success), FailureCount: decOrZero(failure),
		DurationMs: strPtr(decOrZero(dur)), DurationKnownCount: decOrZero(durKnown),
	}
	return DayRow{
		TeamID: c.TeamID, ContributorKey: c.ContributorKey, MetricDate: date, RowKey: key, MetricKind: KindSkill,
		AgentID: strPtr(agent), SkillID: &sid, TokenExact: "0", TokenDerived: "0", UsageEventCount: "0",
		ReportedCost: "0", EstimatedCost: "0", SkillUseCount: decOrZero(uses),
		SkillStats: mustJSON(stats), Resources: emptyJSONObject(), Activity: emptyJSONObject(),
		Hourly: emptyJSONObject(), Quality: defaultQuality(false), RuleVersion: RuleVersion,
	}, nil
}
