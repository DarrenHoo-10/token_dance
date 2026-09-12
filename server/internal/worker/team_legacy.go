package worker

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"time"

	"tokendance/internal/domain"
)

// Legacy daily summaries are a separate source, never fabricated telemetry
// events. Their UTC calendar date cannot be rebucketed into another timezone.
// A v2 fact (including a tombstone) makes its user/harness/UTC-day authoritative
// for v2, so replayed/deleted facts cannot be resurrected by a legacy summary.
func readTeamLegacyRows(ctx context.Context, tx *sql.Tx, member teamMemberSource, grants []teamGrantWindow, from, to, asOf time.Time) ([]analysisAggRow, error) {
	if !member.accountOK || !grantCoversTime(grants, string(domain.SharingBase), asOf) {
		return nil, nil
	}
	end := to.Format("2006-01-02")
	if to.Hour() != 0 || to.Minute() != 0 || to.Second() != 0 || to.Nanosecond() != 0 {
		end = to.AddDate(0, 0, 1).Format("2006-01-02")
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT DATE_FORMAT(d.metric_date, '%Y-%m-%d'), d.agent_id,
		       d.exact_token_total, d.derived_token_total, d.computed_at
		FROM daily_user_agent_metrics d
		WHERE d.user_id = ? AND d.metric_date >= ? AND d.metric_date < ?
		  AND NOT EXISTS (
		    SELECT 1 FROM telemetry_events e
		    WHERE e.user_id = d.user_id AND e.harness_id = d.agent_id
		      AND e.schema_version = 2
		      AND e.event_type = 'model_usage_recorded'
		      AND e.occurred_at >= TIMESTAMPDIFF(SECOND, '1970-01-01', d.metric_date) * 1000
		      AND e.occurred_at < TIMESTAMPDIFF(SECOND, '1970-01-01', d.metric_date + INTERVAL 1 DAY) * 1000
		  )
		ORDER BY d.metric_date, d.agent_id`, member.userID, from.Format("2006-01-02"), end)
	if err != nil {
		return nil, fmt.Errorf("read legacy team summaries: %w", err)
	}
	defer rows.Close()
	acc := map[string]*analysisAggRow{}
	mask := analysisVisibilityMask(grants, asOf)
	for rows.Next() {
		var day, agent, exact, derived string
		var computed time.Time
		if err := rows.Scan(&day, &agent, &exact, &derived, &computed); err != nil {
			return nil, err
		}
		if mask&visClassification == 0 {
			agent = unsharedClassificationBucket
		}
		key := analysisRowKey(member.membershipID, day, mask, agent, "", "", "")
		row := acc[key]
		if row == nil {
			row = newAnalysisAggRow(member.membershipID, day, mask, agent, "", "", "")
			row.legacyAggregate = true
			acc[key] = row
		}
		for i, value := range []string{exact, derived} {
			n, ok := new(big.Int).SetString(value, 10)
			if !ok || n.Sign() < 0 {
				return nil, fmt.Errorf("invalid legacy team token total")
			}
			if i == 0 {
				row.tokenExact.Add(row.tokenExact, n)
			} else {
				row.tokenDerived.Add(row.tokenDerived, n)
			}
		}
		// No event counts, coverage or billing provenance can be inferred from
		// the daily totals. Keep those fields empty; expose the source via API.
		touchReceived(row, computed)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]analysisAggRow, 0, len(acc))
	for _, row := range acc {
		out = append(out, *row)
	}
	return out, nil
}
