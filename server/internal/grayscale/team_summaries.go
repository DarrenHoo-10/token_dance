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
	rows, err := tx.QueryContext(ctx, `SELECT user_id, DATE_FORMAT(metric_date, '%Y-%m-%d'), agent_id,
		exact_token_total, derived_token_total, computed_at
		FROM `+quote(m.cfg.TargetSchema)+`.daily_user_agent_metrics
		WHERE user_id IN `+inClause(len(ids))+` ORDER BY user_id, metric_date, agent_id`, anyStrings(ids)...)
	if err != nil {
		return [32]byte{}, err
	}
	defer rows.Close()
	h := sha256.New()
	enc := json.NewEncoder(h)
	for rows.Next() {
		var user, day, agent, exact, derived, computed string
		if err := rows.Scan(&user, &day, &agent, &exact, &derived, &computed); err != nil {
			return [32]byte{}, err
		}
		if err := enc.Encode([]string{user, day, agent, exact, derived, computed}); err != nil {
			return [32]byte{}, err
		}
	}
	if err := rows.Err(); err != nil {
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
