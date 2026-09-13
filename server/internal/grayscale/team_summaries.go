package grayscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Compare actual team inputs rather than the mirror execution time. An unchanged
// five-minute refresh must not invalidate otherwise reusable team snapshots.
func (m *Mirror) teamSummaryDigest(ctx context.Context, tx *sql.Tx, ids []string) ([32]byte, error) {
	h := sha256.New()
	enc := json.NewEncoder(h)
	hash := func(query string, cols int) error {
		rows, err := tx.QueryContext(ctx, query, anyStrings(ids)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			vals := make([]sql.NullString, cols)
			dest := make([]any, cols)
			for i := range vals {
				dest[i] = &vals[i]
			}
			if err := rows.Scan(dest...); err != nil {
				return err
			}
			line := make([]string, cols)
			for i, v := range vals {
				line[i] = v.String
			}
			if err := enc.Encode(line); err != nil {
				return err
			}
		}
		return rows.Err()
	}
	in := inClause(len(ids))
	schema := quote(m.cfg.TargetSchema)
	if err := hash(`SELECT user_id, DATE_FORMAT(metric_date, '%Y-%m-%d'), agent_id,
		CAST(exact_token_total AS CHAR), CAST(derived_token_total AS CHAR), computed_at
		FROM `+schema+`.daily_user_agent_metrics
		WHERE user_id IN `+in+` ORDER BY user_id, metric_date, agent_id`, 6); err != nil {
		return [32]byte{}, err
	}
	if err := hash(`SELECT user_id, DATE_FORMAT(metric_date, '%Y-%m-%d'), agent_id, provider_id, model_id,
		CAST(exact_token_total AS CHAR), CAST(derived_token_total AS CHAR), computed_at
		FROM `+schema+`.daily_user_agent_model_metrics
		WHERE user_id IN `+in+` ORDER BY user_id, metric_date, agent_id, provider_id, model_id`, 8); err != nil {
		return [32]byte{}, err
	}
	if err := hash(`SELECT user_id, DATE_FORMAT(metric_date, '%Y-%m-%d'), agent_id, HEX(skill_key),
		COALESCE(skill_public_name, ''), CAST(use_count AS CHAR), computed_at
		FROM `+schema+`.daily_skill_metrics
		WHERE user_id IN `+in+` ORDER BY user_id, metric_date, agent_id, skill_key`, 7); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func (m *Mirror) invalidateTeamSummaries(ctx context.Context, tx *sql.Tx, ids []string, now time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO `+quote(m.cfg.TargetSchema)+`.team_source_revisions (team_id, source_revision, changed_at)
		SELECT DISTINCT c.team_id, 1, ? FROM `+quote(m.cfg.TargetSchema)+`.user_current_teams c
		JOIN `+quote(m.cfg.TargetSchema)+`.teams t ON t.team_id = c.team_id AND t.status = 'active'
		WHERE c.user_id IN `+inClause(len(ids))+`
		ON DUPLICATE KEY UPDATE source_revision = source_revision + 1, changed_at = VALUES(changed_at)`, append([]any{now}, anyStrings(ids)...)...)
	if err != nil {
		return fmt.Errorf("invalidate mirrored team summaries: %w", err)
	}
	return nil
}
