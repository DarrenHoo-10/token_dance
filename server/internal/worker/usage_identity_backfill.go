package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
	mysqlstore "tokendance/internal/store/mysql"
	"tokendance/internal/teammetrics"
	"tokendance/internal/telemetry"
	"tokendance/internal/telemetryagg"
)

// UsageIdentityBackfillOptions bounds one BackfillUsageIdentity invocation.
type UsageIdentityBackfillOptions struct {
	// HMACKey is telemetry.UsageIdentityKey over the deployment HMAC secret.
	HMACKey []byte
	// MaxGroups limits how many duplicate groups are repaired per pass (0 = 200).
	MaxGroups int
	// SinceDays limits the scan to recent events (0 = all history).
	SinceDays int
	// Now is the clock used for updated_at and dirty-day leases.
	Now time.Time
	// DryRun only reports the groups that would be repaired.
	DryRun bool
}

// UsageIdentityBackfillReport summarizes one pass.
type UsageIdentityBackfillReport struct {
	GroupsExamined  int
	GroupsRepaired  int
	GroupsSkipped   int
	RowsIdentified  int
	RowsReversed    int
	DirtyDaysMarked int
}

const usageIdentityBackfillGroupLimit = 200

// BackfillUsageIdentity repairs account totals that were double counted before
// wire usageIdentity existed. Claude Code SSH remote sessions mirror the same
// transcript to two machines, so the same usage arrived under two
// installation_ids and account aggregates summed both copies.
//
// Pass 1 marks each duplicate group with a synthetic usage_identity_hash so the
// normal ownership rule (first ingested copy wins) has something to key on.
// Pass 2 reverses the contributions that non-owner copies already applied, so
// marking alone does not change history silently. Pending copies need nothing:
// their tasks run later and the ownership check shadows them.
//
// The pass is idempotent and resumable: repaired groups gain an identity and
// stop matching, in-flight groups are skipped and retried by re-running.
func (w *Worker) BackfillUsageIdentity(ctx context.Context, opts UsageIdentityBackfillOptions) (UsageIdentityBackfillReport, error) {
	var report UsageIdentityBackfillReport
	if w.db == nil || len(opts.HMACKey) == 0 {
		return report, nil
	}
	maxGroups := opts.MaxGroups
	if maxGroups <= 0 {
		maxGroups = usageIdentityBackfillGroupLimit
	}

	for _, eventType := range []string{"model_usage_recorded", "cost_recorded"} {
		groups, err := w.findUsageIdentityGroups(ctx, eventType, opts.SinceDays, maxGroups)
		if err != nil {
			return report, err
		}
		report.GroupsExamined += len(groups)
		if opts.DryRun {
			continue
		}
		for _, group := range groups {
			repaired, reversed, rows, dirtyDays, err := w.repairUsageIdentityGroup(ctx, group, eventType, opts)
			if err != nil {
				return report, err
			}
			report.RowsIdentified += rows
			report.DirtyDaysMarked += dirtyDays
			if repaired {
				report.GroupsRepaired++
				report.RowsReversed += reversed
			} else {
				report.GroupsSkipped++
			}
		}
	}
	return report, nil
}

type usageIdentityGroup struct {
	UserID    string
	Occurred  int64
	ModelKey  uint64
	Signature string
	MinID     int64
	Copies    int64
	Devices   int64
}

// payloadUsageSignature is NULL-safe per component: missing paths become '-' and
// stored values are decimal strings, so the concatenation is unambiguous.
const payloadUsageSignature = `CONCAT_WS('|',
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.token_total')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.input_context_tokens')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.output_tokens')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.cache_read_tokens')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.cache_write_tokens')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.reasoning_tokens')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.usage.request_count')),'-'))`

const payloadCostSignature = `CONCAT_WS('|',
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.cost.currency')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.cost.units')),'-'),
		COALESCE(JSON_UNQUOTE(JSON_EXTRACT(payload_json,'$.cost.source')),'-'))`

func (w *Worker) findUsageIdentityGroups(ctx context.Context, eventType string, sinceDays, limit int) ([]usageIdentityGroup, error) {
	signature := payloadUsageSignature
	if eventType == "cost_recorded" {
		signature = payloadCostSignature
	}
	query := `
		SELECT i.user_id, e.occurred_at, e.model_key, ` + signature + `, MIN(e.id), COUNT(*), COUNT(DISTINCT e.installation_id)
		FROM telemetry_events e
		JOIN installations i ON i.installation_id = e.installation_id
		WHERE e.harness_id = 'claude-code'
		  AND e.event_type = ?
		  AND e.delete_at IS NULL
		  AND e.usage_identity_hash IS NULL`
	args := []any{eventType}
	if sinceDays > 0 {
		query += ` AND e.occurred_at >= ?`
		args = append(args, w.clk.Now().AddDate(0, 0, -sinceDays).UnixMilli())
	}
	query += `
		GROUP BY i.user_id, e.occurred_at, e.model_key, ` + signature + `
		HAVING COUNT(*) > 1 AND COUNT(DISTINCT e.installation_id) > 1
		LIMIT ?`
	args = append(args, limit)

	rows, err := w.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("find usage identity groups: %w", err)
	}
	defer rows.Close()
	var groups []usageIdentityGroup
	for rows.Next() {
		var g usageIdentityGroup
		if err := rows.Scan(&g.UserID, &g.Occurred, &g.ModelKey, &g.Signature, &g.MinID, &g.Copies, &g.Devices); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, rows.Err()
}

func (w *Worker) repairUsageIdentityGroup(ctx context.Context, group usageIdentityGroup, eventType string, opts UsageIdentityBackfillOptions) (repaired bool, reversed int, rows int, dirtyDays int, err error) {
	nowMs := opts.Now.UnixMilli()
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, 0, 0, 0, err
	}
	defer tx.Rollback()

	members, err := loadUsageIdentityGroupMembers(ctx, tx, group, eventType)
	if err != nil {
		return false, 0, 0, 0, err
	}
	if len(members) < 2 {
		// Concurrently repaired or partially purged; treat as done.
		if err := tx.Commit(); err != nil {
			return false, 0, 0, 0, err
		}
		return true, 0, 0, 0, nil
	}
	for _, member := range members {
		if statusHasInFlight(member.StatusJSON) {
			// A task is holding this row; come back on a later pass.
			return false, 0, 0, 0, nil
		}
	}

	identityHash := telemetry.BackfillUsageIdentityHash(opts.HMACKey, "claude-code", eventType, group.Occurred, int64(group.ModelKey), group.Signature)
	owner := members[0]
	for _, member := range members {
		if _, err := tx.ExecContext(ctx, `
			UPDATE telemetry_events
			SET usage_identity_hash = ?, usage_identity_source = 0, updated_at = ?
			WHERE id = ? AND usage_identity_hash IS NULL`,
			identityHash[:], nowMs, member.ID,
		); err != nil {
			return false, 0, 0, 0, fmt.Errorf("mark usage identity: %w", err)
		}
	}

	affectedDates := map[string]struct{}{}
	for _, member := range members {
		// Same fact_key means this member is the owner's own revision chain;
		// prepareFactRevision semantics already governed those rows.
		if member.ID == owner.ID || sameBytes(member.FactKey, owner.FactKey) {
			continue
		}
		didReverse, err := w.reverseUsageIdentityMember(ctx, tx, group.UserID, member, eventType, nowMs)
		if err != nil {
			return false, 0, 0, 0, err
		}
		if didReverse {
			reversed++
			affectedDates[domain.DayDate(time.UnixMilli(member.OccurredAt))] = struct{}{}
		}
	}

	if len(affectedDates) > 0 {
		dates := make([]string, 0, len(affectedDates))
		for date := range affectedDates {
			dates = append(dates, date)
		}
		dirtyDays = len(dates)
		for _, date := range dates {
			if err := mysqlstore.MarkAggregateDirtyDayTx(ctx, tx, group.UserID, date, opts.Now); err != nil {
				return false, 0, 0, 0, fmt.Errorf("mark dirty day: %w", err)
			}
		}
		if err := teammetrics.RefreshCurrentTeamDaysTx(ctx, tx, group.UserID, dates, nowMs); err != nil {
			return false, 0, 0, 0, fmt.Errorf("refresh team days: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return false, 0, 0, 0, fmt.Errorf("commit usage identity group: %w", err)
	}
	return true, reversed, len(members), dirtyDays, nil
}

type usageIdentityMember struct {
	ID                     int64
	InstallationID         string
	FactKey                []byte
	OccurredAt             int64
	ModelKey               uint64
	CostScopeKey           []byte
	PayloadJSON            []byte
	StatusJSON             []byte
	MetricSemanticsVersion uint16
}

func loadUsageIdentityGroupMembers(ctx context.Context, tx *sql.Tx, group usageIdentityGroup, eventType string) ([]usageIdentityMember, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.installation_id, e.fact_key, e.occurred_at, e.model_key, e.cost_scope_key,
		       e.payload_json, e.status_json, e.metric_semantics_version
		FROM telemetry_events e
		JOIN installations i ON i.installation_id = e.installation_id
		WHERE i.user_id = ? AND e.event_type = ? AND e.occurred_at = ? AND e.model_key = ?
		  AND e.delete_at IS NULL
		ORDER BY e.id FOR UPDATE`,
		group.UserID, eventType, group.Occurred, group.ModelKey,
	)
	if err != nil {
		return nil, fmt.Errorf("load usage identity group: %w", err)
	}
	defer rows.Close()
	var members []usageIdentityMember
	for rows.Next() {
		var m usageIdentityMember
		if err := rows.Scan(&m.ID, &m.InstallationID, &m.FactKey, &m.OccurredAt, &m.ModelKey,
			&m.CostScopeKey, &m.PayloadJSON, &m.StatusJSON, &m.MetricSemanticsVersion); err != nil {
			return nil, err
		}
		sig, err := memberSignature(m, eventType)
		if err != nil {
			return nil, err
		}
		if sig != group.Signature {
			continue
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func memberSignature(m usageIdentityMember, eventType string) (string, error) {
	var payload v2.EventPayload
	if err := json.Unmarshal(m.PayloadJSON, &payload); err != nil {
		return "", fmt.Errorf("decode payload for signature: %w", err)
	}
	join := func(fields ...*string) string {
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			if f == nil {
				out = append(out, "-")
			} else {
				out = append(out, *f)
			}
		}
		return strings.Join(out, "|")
	}
	if eventType == "cost_recorded" {
		if payload.Cost == nil {
			return "-", nil
		}
		units := string(payload.Cost.Units)
		currency := payload.Cost.Currency
		source := string(payload.Cost.Source)
		return join(&currency, &units, &source), nil
	}
	if payload.Usage == nil {
		return "-", nil
	}
	u := payload.Usage
	return join(
		optString(u.TokenTotal), optString(u.InputContextTokens), optString(u.OutputTokens),
		optString(u.CacheReadTokens), optString(u.CacheWriteTokens), optString(u.ReasoningTokens),
		optString(u.RequestCount),
	), nil
}

func optString(raw *v2.UInt64String) *string {
	if raw == nil {
		return nil
	}
	s := string(*raw)
	return &s
}

// reverseUsageIdentityMember subtracts the contribution an already-applied
// shadow copy added to its buckets and pins the row to not_applicable, so no
// future replay resurrects it. Returns false when nothing was applied.
func (w *Worker) reverseUsageIdentityMember(ctx context.Context, tx *sql.Tx, userID string, member usageIdentityMember, eventType string, nowMs int64) (bool, error) {
	statuses, err := parseStatusJSON(member.StatusJSON)
	if err != nil {
		return false, err
	}
	reversedAny := false
	for _, grain := range []string{domain.TelemetryGrainHour, domain.TelemetryGrainDay, domain.TelemetryGrainMonth} {
		if statuses[grain] != domain.TelemetryStatusApplied {
			continue
		}
		bucketStart, err := domain.BucketStartMs(grain, member.OccurredAt)
		if err != nil {
			return false, err
		}
		if eventType == "cost_recorded" {
			err = w.reverseCostContribution(ctx, tx, userID, member, grain, bucketStart, nowMs)
		} else {
			err = w.reverseUsageContribution(ctx, tx, userID, member, grain, bucketStart, nowMs)
		}
		if err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE telemetry_events
			SET status_json = JSON_SET(status_json, ?, ?), updated_at = ?
			WHERE id = ?`,
			"$."+grain, domain.TelemetryStatusNotApplicable, nowMs, member.ID,
		); err != nil {
			return false, fmt.Errorf("pin shadow status: %w", err)
		}
		reversedAny = true
	}
	return reversedAny, nil
}

func (w *Worker) reverseUsageContribution(ctx context.Context, tx *sql.Tx, userID string, member usageIdentityMember, grain string, bucketStart int64, nowMs int64) error {
	var payload v2.EventPayload
	if err := json.Unmarshal(member.PayloadJSON, &payload); err != nil {
		return fmt.Errorf("decode payload for reversal: %w", err)
	}
	delta := map[string]int64{}
	if err := accumulateUsage(&payload, delta); err != nil {
		return err
	}
	if len(delta) == 0 {
		return nil
	}
	for k, v := range delta {
		delta[k] = -v
	}
	semantics := member.MetricSemanticsVersion
	if semantics == 0 {
		semantics = 1
	}
	return mysqlstore.ApplyModelMetricDeltaTx(ctx, tx, userID, member.InstallationID, grain, bucketStart, "claude-code", member.ModelKey, semantics, nowMs, delta)
}

// reverseCostContribution recomputes the effective-cost selection of the
// member's cost scope without this fact and applies the difference. Applied
// cost diffs telescope to eff(applied facts), so this inverse is exact.
func (w *Worker) reverseCostContribution(ctx context.Context, tx *sql.Tx, userID string, member usageIdentityMember, grain string, bucketStart int64, nowMs int64) error {
	var payload v2.EventPayload
	if err := json.Unmarshal(member.PayloadJSON, &payload); err != nil {
		return fmt.Errorf("decode payload for cost reversal: %w", err)
	}
	if payload.Cost == nil {
		return nil
	}
	units, err := strconv.ParseUint(string(payload.Cost.Units), 10, 64)
	if err != nil {
		return err
	}
	statusPath := "$." + grain
	var facts []telemetryagg.CostFact
	if len(member.CostScopeKey) == 32 {
		rows, err := tx.QueryContext(ctx, `
			SELECT id, occurred_at, model_key,
			       JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.cost.currency')),
			       CAST(JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.cost.units')) AS UNSIGNED),
			       JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.cost.source')), HEX(fact_key), fact_revision
			FROM telemetry_events
			WHERE installation_id = ?
			  AND harness_id = 'claude-code'
			  AND cost_scope_key = ?
			  AND delete_at IS NULL
			  AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json, ?)) AS UNSIGNED) = ?
			  AND JSON_EXTRACT(payload_json, '$.cost') IS NOT NULL`,
			member.InstallationID, member.CostScopeKey, statusPath, domain.TelemetryStatusApplied,
		)
		if err != nil {
			return fmt.Errorf("load cost facts for reversal: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var f telemetryagg.CostFact
			var source string
			if err := rows.Scan(&f.EventRowID, &f.OccurredAt, &f.ModelKey, &f.Currency, &f.Units, &source, &f.FactKey, &f.Revision); err != nil {
				return err
			}
			f.Source = v2.CostSource(source)
			facts = append(facts, f)
		}
		if err := rows.Err(); err != nil {
			return err
		}
	} else {
		// Unscoped costs are independent additive facts.
		facts = append(facts, telemetryagg.CostFact{
			EventRowID: member.ID,
			OccurredAt: member.OccurredAt,
			ModelKey:   member.ModelKey,
			Currency:   payload.Cost.Currency,
			Units:      units,
			Source:     payload.Cost.Source,
		})
	}

	effWith := telemetryagg.SelectEffectiveCosts(facts)
	withoutFact := make([]telemetryagg.CostFact, 0, len(facts))
	for _, f := range facts {
		if f.EventRowID != member.ID {
			withoutFact = append(withoutFact, f)
		}
	}
	effWithout := telemetryagg.SelectEffectiveCosts(withoutFact)
	semantics := member.MetricSemanticsVersion
	if semantics == 0 {
		semantics = 1
	}
	for _, delta := range telemetryagg.DiffEffectiveCosts(effWith, effWithout) {
		d := map[string]int64{
			"reported_cost_units":     delta.ReportedUnits,
			"estimated_cost_units":    delta.EstimatedUnits,
			"reported_request_count":  delta.ReportedRequestCount,
			"estimated_request_count": delta.EstimatedRequestCount,
			"unpriced_request_count":  delta.UnpricedRequestCount,
			"cost_known_count":        delta.CostKnownCount,
		}
		if err := mysqlstore.ApplyCostMetricDeltaTx(ctx, tx, userID, member.InstallationID, grain, bucketStart, "claude-code", delta.ModelKey, delta.Currency, semantics, nowMs, d); err != nil {
			return err
		}
	}
	return nil
}

func sameBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func statusHasInFlight(statusJSON []byte) bool {
	statuses, err := parseStatusJSON(statusJSON)
	if err != nil {
		// Unreadable state must not be mutated by the backfill.
		return true
	}
	for _, grain := range []string{domain.TelemetryGrainHour, domain.TelemetryGrainDay, domain.TelemetryGrainMonth} {
		if statuses[grain] == domain.TelemetryStatusInFlight {
			return true
		}
	}
	return false
}

func parseStatusJSON(statusJSON []byte) (map[string]int, error) {
	var statuses struct {
		Hour  *int `json:"hour"`
		Day   *int `json:"day"`
		Month *int `json:"month"`
	}
	if err := json.Unmarshal(statusJSON, &statuses); err != nil {
		return nil, fmt.Errorf("decode status_json: %w", err)
	}
	out := map[string]int{}
	if statuses.Hour != nil {
		out[domain.TelemetryGrainHour] = *statuses.Hour
	}
	if statuses.Day != nil {
		out[domain.TelemetryGrainDay] = *statuses.Day
	}
	if statuses.Month != nil {
		out[domain.TelemetryGrainMonth] = *statuses.Month
	}
	return out, nil
}

// ReopenUsageIdentityShadowsTx re-opens shadowed usage and cost copies of a
// user after the owning installation's events were deleted: the ownership rule
// counts the first ingested copy, so a device deletion would otherwise leave
// the surviving copies pinned to not_applicable and undercount the account.
// Superseded revisions (a newer revision of the same fact applied) stay closed.
func ReopenUsageIdentityShadowsTx(ctx context.Context, tx *sql.Tx, userID string, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id
		FROM telemetry_events e
		JOIN installations i ON i.installation_id = e.installation_id
		WHERE i.user_id = ?
		  AND e.delete_at IS NULL
		  AND e.usage_identity_hash IS NOT NULL
		  AND e.event_type IN ('model_usage_recorded', 'cost_recorded')
		  AND (
		        JSON_EXTRACT(e.status_json,'$.hour') = 4
		        OR JSON_EXTRACT(e.status_json,'$.day') = 4
		        OR JSON_EXTRACT(e.status_json,'$.month') = 4
		      )
		  AND NOT EXISTS (
		        SELECT 1 FROM telemetry_events o
		        JOIN installations oi ON oi.installation_id = o.installation_id
		        WHERE oi.user_id = i.user_id
		          AND o.usage_identity_hash = e.usage_identity_hash
		          AND o.id < e.id
		          AND o.fact_key <> e.fact_key
		          AND o.delete_at IS NULL
		      )
		  AND NOT EXISTS (
		        SELECT 1 FROM telemetry_events r
		        WHERE r.fact_key = e.fact_key
		          AND r.delete_at IS NULL
		          AND (
		                JSON_EXTRACT(r.status_json,'$.hour') = 3
		                OR JSON_EXTRACT(r.status_json,'$.day') = 3
		                OR JSON_EXTRACT(r.status_json,'$.month') = 3
		              )
		      )`,
		userID,
	)
	if err != nil {
		return fmt.Errorf("find reopenable usage identity shadows: %w", err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	nowMs := now.UnixMilli()
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `
			UPDATE telemetry_events
			SET status_json = JSON_SET(status_json, '$.hour', 0, '$.day', 0, '$.month', 0),
			    updated_at = ?
			WHERE id = ?`,
			nowMs, id,
		); err != nil {
			return fmt.Errorf("reopen usage identity shadow: %w", err)
		}
		for _, consumer := range []string{domain.TelemetryGrainHour, domain.TelemetryGrainDay, domain.TelemetryGrainMonth} {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO telemetry_tasks (created_at, updated_at, extra, event_row_id, consumer, runnable_at, attempt_count)
				VALUES (?, ?, JSON_OBJECT(), ?, ?, ?, 0)
				ON DUPLICATE KEY UPDATE updated_at = VALUES(updated_at), delete_at = NULL, runnable_at = VALUES(runnable_at)`,
				nowMs, nowMs, id, consumer, nowMs,
			); err != nil {
				return fmt.Errorf("requeue usage identity shadow task: %w", err)
			}
		}
	}
	return nil
}
