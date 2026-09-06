package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"tokendance/internal/pricing"
)

func (w *Worker) SetPricing(client *pricing.Client) { w.pricing = client }

// Fill missing prices only. Provider-reported charges and saved historical
// estimates are immutable; new estimates record the catalog timestamp and rates.
func (w *Worker) ProcessPrices(ctx context.Context) (int, error) {
	if w.db == nil || w.pricing == nil {
		return 0, nil
	}
	now := w.clk.Now()
	if now.Before(w.priceRetry) {
		return 0, nil
	}
	catalog, fetched, err := w.pricing.Load(ctx)
	if err != nil {
		w.priceRetry = now.Add(time.Minute)
		return 0, err
	}
	conn, err := w.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	var locked int
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 5)", aggregationLockName).Scan(&locked); err != nil {
		return 0, err
	}
	if locked != 1 {
		return 0, nil
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "SELECT RELEASE_LOCK(?)", aggregationLockName)
	}()
	rows, err := conn.QueryContext(ctx, `SELECT e.event_pk,e.user_id,DATE_FORMAT(e.occurred_date,'%Y-%m-%d'),e.agent_id,e.model_id,
 e.token_input,e.token_output,COALESCE(e.token_cache_read,0),COALESCE(e.token_cache_write,0)
 FROM usage_events e JOIN users u ON u.user_id=e.user_id
 WHERE u.account_status='active' AND e.event_type='model_usage_recorded' AND e.cost_amount IS NULL
 AND e.accuracy IN ('exact','derived') AND e.event_pk>? ORDER BY e.event_pk LIMIT 500`, w.priceCursor)
	if err != nil {
		return 0, err
	}
	var items []priceEvent
	userSet := map[string]struct{}{}
	for rows.Next() {
		var v priceEvent
		var model sql.NullString
		if err = rows.Scan(&v.pk, &v.user, &v.date, &v.agent, &model, &v.input, &v.output, &v.read, &v.write); err != nil {
			rows.Close()
			return 0, err
		}
		v.model = model.String
		items = append(items, v)
		userSet[v.user] = struct{}{}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	if len(items) == 0 {
		w.priceCursor = 0
		w.priceRetry = now.Add(5 * time.Minute)
		return 0, nil
	}

	userIDs := make([]string, 0, len(userSet))
	for userID := range userSet {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if err := lockUsersThenTeams(ctx, tx, userIDs); err != nil {
		return 0, err
	}

	pending, err := rereadPricelessEvents(ctx, tx, items)
	if err != nil {
		return 0, err
	}

	targets := map[string]map[string]struct{}{}
	pricedUsers := map[string]struct{}{}
	count := 0
	for _, v := range pending {
		model, ok := catalog.Match(v.model)
		if !ok || !v.input.Valid || !v.output.Valid || v.input.Int64 < 0 || v.output.Int64 < 0 {
			continue
		}
		amount, ok := pricing.Estimate(model, pricing.Usage{Input: uint64(v.input.Int64), Output: uint64(v.output.Int64), CacheRead: v.read, CacheWrite: v.write, InputIncludesCache: v.agent == "codex"})
		if !ok {
			continue
		}
		metadata, _ := json.Marshal(map[string]interface{}{"model": model.ID, "catalogAt": fetched.Format(time.RFC3339), "basis": "catalog_at_estimation", "rates": model.Pricing})
		result, err := tx.ExecContext(ctx, `UPDATE usage_events SET cost_amount=?,cost_currency='USD',cost_source='estimated_price_table',
  safe_extension_json=JSON_SET(COALESCE(safe_extension_json,JSON_OBJECT()),'$.openrouter',CAST(? AS JSON))
  WHERE event_pk=? AND cost_amount IS NULL`, amount, string(metadata), v.pk)
		if err != nil {
			return 0, fmt.Errorf("save estimated cost: %w", err)
		}
		changed, _ := result.RowsAffected()
		if changed == 0 {
			continue
		}
		count++
		pricedUsers[v.user] = struct{}{}
		if targets[v.user] == nil {
			targets[v.user] = map[string]struct{}{}
		}
		targets[v.user][v.date] = struct{}{}
	}
	if err := bumpSourceRevisionsForUsers(ctx, tx, pricedUsers, now); err != nil {
		return 0, err
	}
	for user, dates := range targets {
		if err = rebuildUserAggregates(ctx, tx, user, mapKeys(dates)); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	w.priceCursor = items[len(items)-1].pk
	return count, nil
}

type priceEvent struct {
	pk                       uint64
	user, date, agent, model string
	input, output            sql.NullInt64
	read, write              uint64
}

func lockUsersThenTeams(ctx context.Context, tx *sql.Tx, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	query := "SELECT user_id FROM users WHERE user_id IN (" + placeholders(len(userIDs)) + ") ORDER BY user_id ASC FOR UPDATE"
	args := make([]interface{}, 0, len(userIDs))
	for _, id := range userIDs {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("lock pricing users: %w", err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("scan locked pricing user: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}

	teamQuery := `
		SELECT t.team_id
		FROM teams t
		JOIN user_current_teams uct ON uct.team_id = t.team_id
		WHERE uct.user_id IN (` + placeholders(len(userIDs)) + `)
		ORDER BY t.team_id ASC
		FOR UPDATE`
	teamRows, err := tx.QueryContext(ctx, teamQuery, args...)
	if err != nil {
		return fmt.Errorf("lock pricing teams: %w", err)
	}
	teamIDs := make([]string, 0)
	for teamRows.Next() {
		var teamID string
		if err := teamRows.Scan(&teamID); err != nil {
			teamRows.Close()
			return fmt.Errorf("scan locked pricing team: %w", err)
		}
		teamIDs = append(teamIDs, teamID)
	}
	if err := teamRows.Err(); err != nil {
		teamRows.Close()
		return err
	}
	if err := teamRows.Close(); err != nil {
		return err
	}
	if len(teamIDs) == 0 {
		return nil
	}
	sourceArgs := make([]interface{}, 0, len(teamIDs))
	for _, id := range teamIDs {
		sourceArgs = append(sourceArgs, id)
	}
	sourceRows, err := tx.QueryContext(ctx, `
		SELECT team_id FROM team_source_revisions
		WHERE team_id IN (`+placeholders(len(teamIDs))+`)
		ORDER BY team_id ASC
		FOR UPDATE`, sourceArgs...)
	if err != nil {
		return fmt.Errorf("lock pricing team source revisions: %w", err)
	}
	defer sourceRows.Close()
	for sourceRows.Next() {
		var teamID string
		if err := sourceRows.Scan(&teamID); err != nil {
			return fmt.Errorf("scan locked team source revision: %w", err)
		}
	}
	return sourceRows.Err()
}

func rereadPricelessEvents(ctx context.Context, tx *sql.Tx, planned []priceEvent) ([]priceEvent, error) {
	if len(planned) == 0 {
		return nil, nil
	}
	pks := make([]interface{}, 0, len(planned))
	for _, item := range planned {
		pks = append(pks, item.pk)
	}
	query := `SELECT e.event_pk,e.user_id,DATE_FORMAT(e.occurred_date,'%Y-%m-%d'),e.agent_id,e.model_id,
 e.token_input,e.token_output,COALESCE(e.token_cache_read,0),COALESCE(e.token_cache_write,0)
 FROM usage_events e
 WHERE e.cost_amount IS NULL AND e.event_pk IN (` + placeholders(len(pks)) + `)
 ORDER BY e.event_pk`
	rows, err := tx.QueryContext(ctx, query, pks...)
	if err != nil {
		return nil, fmt.Errorf("reread priceless events: %w", err)
	}
	defer rows.Close()
	var out []priceEvent
	for rows.Next() {
		var v priceEvent
		var model sql.NullString
		if err := rows.Scan(&v.pk, &v.user, &v.date, &v.agent, &model, &v.input, &v.output, &v.read, &v.write); err != nil {
			return nil, fmt.Errorf("scan priceless event: %w", err)
		}
		v.model = model.String
		out = append(out, v)
	}
	return out, rows.Err()
}

func bumpSourceRevisionsForUsers(ctx context.Context, tx *sql.Tx, users map[string]struct{}, now time.Time) error {
	if len(users) == 0 {
		return nil
	}
	ids := make([]string, 0, len(users))
	for userID := range users {
		ids = append(ids, userID)
	}
	sort.Strings(ids)
	query := `
		INSERT INTO team_source_revisions (team_id, source_revision, changed_at)
		SELECT uct.team_id, 1, ?
		FROM user_current_teams uct
		WHERE uct.user_id IN (` + placeholders(len(ids)) + `)
		ON DUPLICATE KEY UPDATE
		  source_revision = source_revision + 1,
		  changed_at = VALUES(changed_at)`
	insertArgs := make([]interface{}, 0, len(ids)+1)
	insertArgs = append(insertArgs, now)
	for _, id := range ids {
		insertArgs = append(insertArgs, id)
	}
	if _, err := tx.ExecContext(ctx, query, insertArgs...); err != nil {
		return fmt.Errorf("bump priced team source revision: %w", err)
	}
	return nil
}
