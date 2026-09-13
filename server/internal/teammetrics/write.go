package teammetrics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"tokendance/internal/crypto"
	"tokendance/internal/domain"
)

func newContributorKey() (string, error) {
	token, err := crypto.GenerateOpaqueToken(13)
	if err != nil {
		return "", err
	}
	id := domain.ContributorIDPrefix + token
	if len(id) != 30 {
		return "", fmt.Errorf("contributor key length %d != 30", len(id))
	}
	return id, nil
}

// BindContributorTx creates or rebinds the stable (team,user) contributor to membershipID.
func BindContributorTx(ctx context.Context, tx *sql.Tx, teamID, userID, membershipID string, nowMs int64) (Contributor, error) {
	var c Contributor
	err := tx.QueryRowContext(ctx, `
		SELECT contributor_key, membership_id, retired_at, created_at, updated_at
		FROM team_usage_contributors
		WHERE team_id = ? AND user_id = ? AND delete_at IS NULL
		FOR UPDATE`, teamID, userID,
	).Scan(&c.ContributorKey, &c.MembershipID, &c.RetiredAtMs, &c.CreatedAtMs, &c.UpdatedAtMs)
	if err == nil {
		if _, err := tx.ExecContext(ctx, `
			UPDATE team_usage_contributors
			SET membership_id = ?, retired_at = NULL, updated_at = ?
			WHERE team_id = ? AND user_id = ? AND delete_at IS NULL`,
			membershipID, nowMs, teamID, userID,
		); err != nil {
			return Contributor{}, fmt.Errorf("rebind contributor: %w", err)
		}
		mid := membershipID
		c.TeamID, c.UserID, c.MembershipID, c.RetiredAtMs, c.UpdatedAtMs = teamID, userID, &mid, nil, nowMs
		return c, nil
	}
	if err != sql.ErrNoRows {
		return Contributor{}, fmt.Errorf("load contributor: %w", err)
	}
	key, err := newContributorKey()
	if err != nil {
		return Contributor{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_usage_contributors (
			created_at, updated_at, extra, team_id, user_id, contributor_key, membership_id, retired_at
		) VALUES (?, ?, JSON_OBJECT(), ?, ?, ?, ?, NULL)`,
		nowMs, nowMs, teamID, userID, key, membershipID,
	); err != nil {
		return Contributor{}, fmt.Errorf("insert contributor: %w", err)
	}
	mid := membershipID
	return Contributor{
		TeamID: teamID, UserID: userID, ContributorKey: key,
		MembershipID: &mid, CreatedAtMs: nowMs, UpdatedAtMs: nowMs,
	}, nil
}

// UnbindContributorTx freezes the contributor without deleting static rows.
func UnbindContributorTx(ctx context.Context, tx *sql.Tx, teamID, userID string, nowMs int64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE team_usage_contributors
		SET membership_id = NULL, retired_at = ?, updated_at = ?
		WHERE team_id = ? AND user_id = ? AND delete_at IS NULL`,
		nowMs, nowMs, teamID, userID)
	if err != nil {
		return fmt.Errorf("unbind contributor: %w", err)
	}
	return nil
}

func ReplaceTeamMemberDaysTx(ctx context.Context, tx *sql.Tx, contributor Contributor, dates []string, rows []DayRow, nowMs int64) error {
	if len(dates) == 0 {
		return nil
	}
	if AfterPersonalBeforeTeam != nil {
		if err := AfterPersonalBeforeTeam(); err != nil {
			return err
		}
	}
	placeholders := strings.Repeat("?,", len(dates))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 2+len(dates))
	args = append(args, contributor.ContributorKey, nowMs)
	for _, d := range dates {
		args = append(args, d)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE team_member_day_metrics
		SET delete_at = ?, updated_at = ?
		WHERE contributor_key = ? AND delete_at IS NULL AND metric_date IN (`+placeholders+`)`,
		append([]any{nowMs, nowMs, contributor.ContributorKey}, datesToAny(dates)...)...,
	); err != nil {
		return fmt.Errorf("soft-delete stale team days: %w", err)
	}
	_ = args
	for _, row := range rows {
		if err := validateDayRowJSON(row); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO team_member_day_metrics (
				created_at, updated_at, extra, team_id, contributor_key, metric_date, row_key, metric_kind,
				agent_id, provider_id, model_id, currency,
				token_exact_total, token_derived_total, usage_event_count,
				reported_cost_amount, estimated_cost_amount,
				skill_id, skill_use_count, skill_stats, resources, activity, hourly, quality,
				max_received_at, rule_version
			) VALUES (?, ?, JSON_OBJECT(), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				updated_at = VALUES(updated_at),
				delete_at = NULL,
				metric_kind = VALUES(metric_kind),
				agent_id = VALUES(agent_id),
				provider_id = VALUES(provider_id),
				model_id = VALUES(model_id),
				currency = VALUES(currency),
				token_exact_total = VALUES(token_exact_total),
				token_derived_total = VALUES(token_derived_total),
				usage_event_count = VALUES(usage_event_count),
				reported_cost_amount = VALUES(reported_cost_amount),
				estimated_cost_amount = VALUES(estimated_cost_amount),
				skill_id = VALUES(skill_id),
				skill_use_count = VALUES(skill_use_count),
				skill_stats = VALUES(skill_stats),
				resources = VALUES(resources),
				activity = VALUES(activity),
				hourly = VALUES(hourly),
				quality = VALUES(quality),
				max_received_at = VALUES(max_received_at),
				rule_version = VALUES(rule_version)`,
			nowMs, nowMs, contributor.TeamID, contributor.ContributorKey, row.MetricDate, row.RowKey, row.MetricKind,
			row.AgentID, row.ProviderID, row.ModelID, row.Currency,
			decOrZero(row.TokenExact), decOrZero(row.TokenDerived), decOrZero(row.UsageEventCount),
			decOrZero(row.ReportedCost), decOrZero(row.EstimatedCost),
			row.SkillID, decOrZero(row.SkillUseCount),
			[]byte(row.SkillStats), []byte(row.Resources), []byte(row.Activity), []byte(row.Hourly), []byte(row.Quality),
			row.MaxReceivedAt, row.RuleVersion,
		); err != nil {
			return fmt.Errorf("upsert team day row: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO team_source_revisions (team_id, source_revision, changed_at)
		VALUES (?, 1, ?)
		ON DUPLICATE KEY UPDATE source_revision = source_revision + 1, changed_at = VALUES(changed_at)`,
		contributor.TeamID, time.UnixMilli(nowMs).UTC(),
	); err != nil {
		return fmt.Errorf("bump team source revision: %w", err)
	}
	return nil
}

func datesToAny(dates []string) []any {
	out := make([]any, len(dates))
	for i, d := range dates {
		out[i] = d
	}
	return out
}

func DeleteTeamStaticTx(ctx context.Context, tx *sql.Tx, teamID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_member_day_metrics WHERE team_id = ?`, teamID); err != nil {
		return fmt.Errorf("delete team day metrics: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_usage_contributors WHERE team_id = ?`, teamID); err != nil {
		return fmt.Errorf("delete team contributors: %w", err)
	}
	return nil
}

func DeleteUserStaticTx(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `
		DELETE m FROM team_member_day_metrics m
		INNER JOIN team_usage_contributors c ON c.contributor_key = m.contributor_key
		WHERE c.user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user team day metrics: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM team_usage_contributors WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user contributors: %w", err)
	}
	return nil
}

func ListDayMetrics(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, teamID string, from, toExclusive time.Time) ([]DayRow, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT team_id, contributor_key, DATE_FORMAT(metric_date, '%Y-%m-%d'), row_key, metric_kind,
		       agent_id, provider_id, model_id, currency, skill_id,
		       CAST(token_exact_total AS CHAR), CAST(token_derived_total AS CHAR), CAST(usage_event_count AS CHAR),
		       CAST(reported_cost_amount AS CHAR), CAST(estimated_cost_amount AS CHAR),
		       CAST(skill_use_count AS CHAR), skill_stats, resources, activity, hourly, quality,
		       max_received_at, rule_version
		FROM team_member_day_metrics
		WHERE team_id = ? AND delete_at IS NULL
		  AND metric_date >= ? AND metric_date < ?
		ORDER BY metric_date ASC, contributor_key ASC, row_key ASC`,
		teamID, calendarDate(from), calendarDate(toExclusive),
	)
	if err != nil {
		return nil, fmt.Errorf("list team day metrics: %w", err)
	}
	defer rows.Close()
	var out []DayRow
	for rows.Next() {
		var r DayRow
		var skill sql.NullInt64
		var maxAt sql.NullTime
		if err := rows.Scan(
			&r.TeamID, &r.ContributorKey, &r.MetricDate, &r.RowKey, &r.MetricKind,
			&r.AgentID, &r.ProviderID, &r.ModelID, &r.Currency, &skill,
			&r.TokenExact, &r.TokenDerived, &r.UsageEventCount,
			&r.ReportedCost, &r.EstimatedCost, &r.SkillUseCount,
			&r.SkillStats, &r.Resources, &r.Activity, &r.Hourly, &r.Quality,
			&maxAt, &r.RuleVersion,
		); err != nil {
			return nil, err
		}
		if skill.Valid {
			v := skill.Int64
			r.SkillID = &v
		}
		if maxAt.Valid {
			t := maxAt.Time
			r.MaxReceivedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func ListContributors(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, teamID string) ([]Contributor, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT team_id, user_id, contributor_key, membership_id, retired_at, created_at, updated_at
		FROM team_usage_contributors
		WHERE team_id = ? AND delete_at IS NULL`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contributor
	for rows.Next() {
		var c Contributor
		if err := rows.Scan(&c.TeamID, &c.UserID, &c.ContributorKey, &c.MembershipID, &c.RetiredAtMs, &c.CreatedAtMs, &c.UpdatedAtMs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
