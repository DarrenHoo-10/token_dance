package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	type item struct {
		pk                       uint64
		user, date, agent, model string
		input, output            sql.NullInt64
		read, write              uint64
	}
	var items []item
	for rows.Next() {
		var v item
		var model sql.NullString
		if err = rows.Scan(&v.pk, &v.user, &v.date, &v.agent, &model, &v.input, &v.output, &v.read, &v.write); err != nil {
			rows.Close()
			return 0, err
		}
		v.model = model.String
		items = append(items, v)
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
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	targets := map[string]map[string]struct{}{}
	count := 0
	for _, v := range items {
		model, ok := catalog.Match(v.model)
		if !ok || !v.input.Valid || !v.output.Valid || v.input.Int64 < 0 || v.output.Int64 < 0 {
			continue
		}
		// Codex's input count includes cached input; the normalized Grok/Claude stream uses separate components.
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
		if targets[v.user] == nil {
			targets[v.user] = map[string]struct{}{}
		}
		targets[v.user][v.date] = struct{}{}
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
