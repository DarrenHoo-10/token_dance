package worker

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"tokendance/internal/domain"
)

func applyDeviceAggregates(ctx context.Context, tx *sql.Tx, userID string, dates []string) error {
	rows, err := tx.QueryContext(ctx, "SELECT DATE_FORMAT(metric_date,'%Y-%m-%d'),payload_json FROM device_daily_aggregates WHERE user_id=? AND metric_date IN ("+placeholders(len(dates))+")", aggregateArgs(userID, dates, 1)...)
	if err != nil {
		return err
	}
	type item struct {
		day  string
		rows []domain.AggregateRow
	}
	var items []item
	for rows.Next() {
		var it item
		var raw []byte
		if err = rows.Scan(&it.day, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &it.rows); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, it := range items {
		for _, row := range it.rows {
			if err = addAggregateRow(ctx, tx, userID, it.day, row); err != nil {
				return err
			}
		}
	}
	return nil
}

func addAggregateRow(ctx context.Context, tx *sql.Tx, userID, day string, row domain.AggregateRow) error {
	table := map[string]string{"agent": "daily_user_agent_metrics", "model": "daily_user_agent_model_metrics", "skill": "daily_skill_metrics"}[row.Kind]
	if table == "" {
		return domain.ErrInvalidArgument
	}
	columns := []string{"metric_date", "user_id", "agent_id", "aggregation_version"}
	values := []interface{}{day, userID, row.AgentID, aggregationVersion}
	updates := []string{"computed_at=CURRENT_TIMESTAMP(3)", "updated_at=CURRENT_TIMESTAMP(3)"}
	if row.Kind == "model" {
		columns = append(columns, "provider_id", "model_id")
		values = append(values, row.ProviderID, row.ModelID)
	}
	if row.Kind == "skill" {
		key, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(row.SkillKey, "hmac-sha256:"))
		if err != nil || len(key) != 32 {
			return domain.ErrInvalidArgument
		}
		columns = append(columns, "skill_key", "skill_public_name")
		values = append(values, key, row.SkillName)
		updates = append(updates, "skill_public_name=VALUES(skill_public_name)")
	}
	metrics := make(map[string]string, len(row.Metrics))
	for key, value := range row.Metrics {
		metrics[key] = value
	}
	if estimate, ok := metrics["estimated_cost_usd_units"]; ok {
		base, _ := strconv.ParseUint(metrics["cost_usd_units"], 10, 64)
		extra, err := strconv.ParseUint(estimate, 10, 64)
		if err != nil || ^uint64(0)-base < extra {
			return domain.ErrInvalidArgument
		}
		metrics["cost_usd_units"] = strconv.FormatUint(base+extra, 10)
		delete(metrics, "estimated_cost_usd_units")
	}
	keys := make([]string, 0, len(metrics))
	for key := range metrics {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		allowed := false
		for _, column := range domain.AggregateColumns[row.Kind] {
			if column == key {
				allowed = true
				break
			}
		}
		if !allowed {
			return domain.ErrInvalidArgument
		}
		column, value := key, interface{}(metrics[key])
		if key == "cost_usd_units" {
			units, err := strconv.ParseUint(metrics[key], 10, 64)
			if err != nil {
				return err
			}
			column = "cost_amount"
			value = fmt.Sprintf("%d.%08d", units/100000000, units%100000000)
		}
		columns = append(columns, column)
		values = append(values, value)
		updates = append(updates, column+"="+column+"+VALUES("+column+")")
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO "+table+" ("+strings.Join(columns, ",")+") VALUES ("+placeholders(len(values))+") ON DUPLICATE KEY UPDATE "+strings.Join(updates, ","), values...)
	return err
}
