package worker

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"

	"tokendance/internal/domain"
	v2 "tokendance/internal/protocol/v2"
	mysqlstore "tokendance/internal/store/mysql"
	"tokendance/internal/telemetryagg"
)

const telemetryAggMaxAttempts = 8

// ProcessTelemetryAggregation claims and executes hour/day/month telemetry tasks.
func (w *Worker) ProcessTelemetryAggregation(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	if _, err := mysqlstore.ReclaimExpiredTelemetryLeases(ctx, w.db, now); err != nil {
		return 0, err
	}
	processed := 0
	for _, consumer := range []string{domain.TelemetryGrainHour, domain.TelemetryGrainDay, domain.TelemetryGrainMonth} {
		claims, err := mysqlstore.ClaimTelemetryTasks(ctx, w.db, consumer, now, 50)
		if err != nil {
			return processed, err
		}
		for _, claim := range claims {
			if err := w.executeTelemetryTaskWithRetry(ctx, claim, now); err != nil {
				return processed, err
			}
			// Only count successful applies; failed tasks are requeued via failTelemetryTask.
			var left int
			if err := w.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_tasks WHERE id = ?`, claim.TaskID).Scan(&left); err != nil {
				return processed, err
			}
			if left == 0 {
				processed++
			}
		}
	}
	return processed, nil
}

// ProcessDirtyDayRefresh claims dirty days, rebuilds window scores from telemetry, version-confirms.
// Community outbox enqueue happens inside refreshDirtyDay's confirm transaction so a crash after
// confirm cannot leave dirty applied without the community refresh signal (and vice versa).
func (w *Worker) ProcessDirtyDayRefresh(ctx context.Context) (int, error) {
	if w.db == nil {
		return 0, nil
	}
	now := w.clk.Now().UTC().Truncate(time.Millisecond)
	if _, err := mysqlstore.ReclaimExpiredDirtyDayLeases(ctx, w.db, now); err != nil {
		return 0, err
	}
	claims, err := mysqlstore.ClaimDirtyDays(ctx, w.db, now, 50)
	if err != nil {
		return 0, err
	}
	processed := 0
	confirmedAny := false
	for _, claim := range claims {
		ok, err := w.refreshDirtyDay(ctx, claim, now)
		if err != nil {
			return processed, err
		}
		if ok {
			processed++
			confirmedAny = true
		}
	}
	if confirmedAny {
		tx, err := w.db.BeginTx(ctx, nil)
		if err != nil {
			return processed, err
		}
		if err := mysqlstore.PruneOldWindowScoresTx(ctx, tx, now); err != nil {
			_ = tx.Rollback()
			return processed, err
		}
		if err := tx.Commit(); err != nil {
			return processed, err
		}
	}
	return processed, nil
}

// dirtyRefreshAfterConfirmHook is an optional test failpoint invoked after dirty
// confirm + community outbox staging, before commit. Non-nil error rolls back the tx.
var dirtyRefreshAfterConfirmHook func() error

func (w *Worker) refreshDirtyDay(ctx context.Context, claim mysqlstore.DirtyDayClaim, now time.Time) (bool, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if err := mysqlstore.UpsertCurrentWindowScoresTx(ctx, tx, claim.UserID, now); err != nil {
		return false, err
	}
	confirmed, requeued, err := mysqlstore.ConfirmDirtyDayAtVersionTx(ctx, tx, claim, now)
	if err != nil {
		return false, err
	}
	if confirmed {
		if err := mysqlstore.EnqueueCommunityStatsOutboxTx(ctx, tx, []string{claim.MetricDate}, now); err != nil {
			return false, err
		}
		if dirtyRefreshAfterConfirmHook != nil {
			if err := dirtyRefreshAfterConfirmHook(); err != nil {
				return false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	_ = requeued
	return confirmed, nil
}

func (w *Worker) executeTelemetryTaskWithRetry(ctx context.Context, claim mysqlstore.TelemetryTaskClaim, now time.Time) error {
	var last error
	for attempt := 0; attempt < telemetryAggMaxAttempts; attempt++ {
		err := w.executeTelemetryTask(ctx, claim, now)
		if err == nil {
			return nil
		}
		last = err
		if !isRetryableTxError(err) {
			return w.failTelemetryTask(ctx, claim, err, now)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(20+rand.Intn(80)) * time.Millisecond):
		}
	}
	return w.failTelemetryTask(ctx, claim, last, now)
}

func (w *Worker) failTelemetryTask(ctx context.Context, claim mysqlstore.TelemetryTaskClaim, cause error, now time.Time) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	code := "execute_failed"
	if cause != nil {
		msg := cause.Error()
		if len(msg) > 60 {
			msg = msg[:60]
		}
		code = sanitizeErrorCode(msg)
	}
	retryAt := now.Add(5 * time.Second)
	if err := mysqlstore.FailTelemetryTaskTx(ctx, tx, claim.TaskID, claim.LeaseToken, code, retryAt, now.UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func sanitizeErrorCode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "execute_failed"
	}
	return out
}

func isRetryableTxError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Deadlock") ||
		strings.Contains(msg, "1213") ||
		strings.Contains(msg, "Lock wait timeout") ||
		strings.Contains(msg, "1205")
}

type telemetryEventRow struct {
	ID                     uint64
	UserID                 string
	InstallationID         string
	HarnessID              string
	EventType              string
	OccurredAtMs           int64
	ModelKey               uint64
	SkillID                sql.NullInt64
	SessionKey             []byte
	TurnKey                []byte
	CostScopeKey           []byte
	PayloadJSON            []byte
	StatusJSON             []byte
	MetricSemanticsVersion uint16
}

func (w *Worker) executeTelemetryTask(ctx context.Context, claim mysqlstore.TelemetryTaskClaim, now time.Time) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	nowMs := now.UnixMilli()
	var ev telemetryEventRow
	var statusRaw []byte
	err = tx.QueryRowContext(ctx, `
		SELECT e.id, i.user_id, e.installation_id, e.harness_id, e.event_type, e.occurred_at,
		       e.model_key, e.skill_id, e.session_key, e.turn_key, e.cost_scope_key,
		       e.payload_json, e.status_json, e.metric_semantics_version
		FROM telemetry_events e
        JOIN installations i ON i.installation_id=e.installation_id
		INNER JOIN telemetry_tasks t ON t.event_row_id = e.id AND t.id = ?
		WHERE e.id = ?
		  AND e.delete_at IS NULL
		  AND t.delete_at IS NULL
		  AND t.lease_token = ?
		  AND t.consumer = ?
		FOR UPDATE`,
		claim.TaskID, claim.EventRowID, claim.LeaseToken, claim.Consumer,
	).Scan(
		&ev.ID, &ev.UserID, &ev.InstallationID, &ev.HarnessID, &ev.EventType, &ev.OccurredAtMs,
		&ev.ModelKey, &ev.SkillID, &ev.SessionKey, &ev.TurnKey, &ev.CostScopeKey,
		&ev.PayloadJSON, &statusRaw, &ev.MetricSemanticsVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		// Stale lease / deleted event — drop task if still owned.
		_, _ = tx.ExecContext(ctx, `DELETE FROM telemetry_tasks WHERE id = ? AND lease_token = ?`, claim.TaskID, claim.LeaseToken)
		return tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("load telemetry event for aggregate: %w", err)
	}
	ev.StatusJSON = statusRaw

	// Fencing: lock historical attribution rows in users → installations order.
	var accountStatus domain.AccountStatus
	if err := tx.QueryRowContext(ctx, `SELECT account_status FROM users WHERE user_id = ? FOR SHARE`, ev.UserID).Scan(&accountStatus); err != nil {
		return fmt.Errorf("fence user: %w", err)
	}
	var installStatus domain.InstallationStatus
	if err := tx.QueryRowContext(ctx, `
		SELECT installation_status FROM installations WHERE installation_id = ? FOR SHARE`,
		ev.InstallationID,
	).Scan(&installStatus); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("fence installation: %w", err)
	}

	statusPath := "$." + claim.Consumer
	var statusVal int
	_ = json.Unmarshal(ev.StatusJSON, &map[string]int{})
	if err := tx.QueryRowContext(ctx, `
		SELECT CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json, ?)) AS UNSIGNED)
		FROM telemetry_events WHERE id = ?`, statusPath, ev.ID,
	).Scan(&statusVal); err != nil {
		return err
	}
	if statusVal == domain.TelemetryStatusApplied {
		_, _ = tx.ExecContext(ctx, `DELETE FROM telemetry_tasks WHERE id = ? AND lease_token = ?`, claim.TaskID, claim.LeaseToken)
		return tx.Commit()
	}

	var payload v2.EventPayload
	if err := json.Unmarshal(ev.PayloadJSON, &payload); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}

	bucketStart, err := domain.BucketStartMs(claim.Consumer, ev.OccurredAtMs)
	if err != nil {
		return err
	}

	changed, err := w.applyTelemetryContribution(ctx, tx, claim.Consumer, bucketStart, &ev, &payload, nowMs)
	if err != nil {
		return err
	}

	if err := mysqlstore.MarkEventConsumerAppliedTx(ctx, tx, ev.ID, claim.TaskID, claim.Consumer, claim.LeaseToken, nowMs); err != nil {
		return err
	}

	if changed && claim.Consumer == domain.TelemetryGrainDay {
		metricDate := domain.DayDate(time.UnixMilli(ev.OccurredAtMs))
		if err := mysqlstore.MarkAggregateDirtyDayTx(ctx, tx, ev.UserID, metricDate, now); err != nil {
			return err
		}
	}

	_ = accountStatus
	_ = installStatus
	return tx.Commit()
}

func (w *Worker) applyTelemetryContribution(
	ctx context.Context,
	tx *sql.Tx,
	grain string,
	bucketStart int64,
	ev *telemetryEventRow,
	payload *v2.EventPayload,
	nowMs int64,
) (bool, error) {
	changed := false
	semantics := ev.MetricSemanticsVersion
	if semantics == 0 {
		semantics = 1
	}

	// Lock entity anchors in deterministic order before mutating metrics.
	entityKeys := collectEntityKeys(ev, payload)
	sort.Slice(entityKeys, func(i, j int) bool {
		if entityKeys[i].kind != entityKeys[j].kind {
			return entityKeys[i].kind < entityKeys[j].kind
		}
		return bytes.Compare(entityKeys[i].key, entityKeys[j].key) < 0
	})
	for _, ek := range entityKeys {
		if err := mysqlstore.LockBucketEntityTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, ek.kind, ek.key, nowMs); err != nil {
			return false, err
		}
	}

	harnessDelta := map[string]int64{}
	modelDelta := map[string]int64{}
	skillDelta := map[string]int64{}

	switch v2.EventType(ev.EventType) {
	case v2.EventTypeModelUsageRecorded:
		if err := accumulateUsage(payload, modelDelta); err != nil {
			return false, err
		}
	case v2.EventTypeCodeChanged:
		accumulateCode(payload, harnessDelta)
	case v2.EventTypeToolInvoked:
		harnessDelta["tool_call_count"] = 1
	case v2.EventTypeSkillInvoked:
		if !ev.SkillID.Valid {
			return false, fmt.Errorf("skill_invoked without skill_id")
		}
		harnessDelta["skill_use_count"] = 1
		accumulateSkill(payload, skillDelta)
	case v2.EventTypeSessionStarted, v2.EventTypeSessionEnded, v2.EventTypeTurnStarted, v2.EventTypeTurnCompleted:
		d, err := applyEntityEvent(ctx, tx, grain, bucketStart, ev, payload, nowMs)
		if err != nil {
			return false, err
		}
		for k, v := range d {
			harnessDelta[k] += v
		}
		if err := applyDurationDiff(ctx, tx, grain, bucketStart, ev, payload, nowMs, harnessDelta); err != nil {
			return false, err
		}
	case v2.EventTypeCostRecorded:
		if err := applyCostDiff(ctx, tx, grain, bucketStart, ev, payload, nowMs); err != nil {
			return false, err
		}
		changed = true
	}

	if len(harnessDelta) > 0 {
		if err := mysqlstore.ApplyHarnessMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, semantics, nowMs, harnessDelta); err != nil {
			return false, err
		}
		changed = true
	}
	if len(modelDelta) > 0 {
		if err := mysqlstore.ApplyModelMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, ev.ModelKey, semantics, nowMs, modelDelta); err != nil {
			return false, err
		}
		changed = true
	}
	if len(skillDelta) > 0 && ev.SkillID.Valid {
		if err := mysqlstore.ApplySkillMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, ev.SkillID.Int64, semantics, nowMs, skillDelta); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

type entityKey struct {
	kind string
	key  []byte
}

func collectEntityKeys(ev *telemetryEventRow, payload *v2.EventPayload) []entityKey {
	var keys []entityKey
	switch v2.EventType(ev.EventType) {
	case v2.EventTypeSessionStarted, v2.EventTypeSessionEnded:
		if len(ev.SessionKey) == 32 {
			keys = append(keys, entityKey{kind: telemetryagg.EntityKindSession, key: append([]byte(nil), ev.SessionKey...)})
		}
	case v2.EventTypeTurnStarted, v2.EventTypeTurnCompleted:
		if len(ev.TurnKey) == 32 {
			keys = append(keys, entityKey{kind: telemetryagg.EntityKindTurn, key: append([]byte(nil), ev.TurnKey...)})
		}
		if len(ev.SessionKey) == 32 {
			keys = append(keys, entityKey{kind: telemetryagg.EntityKindSession, key: append([]byte(nil), ev.SessionKey...)})
		}
	}
	_ = payload
	return keys
}

func applyEntityEvent(
	ctx context.Context,
	tx *sql.Tx,
	grain string,
	bucketStart int64,
	ev *telemetryEventRow,
	payload *v2.EventPayload,
	nowMs int64,
) (map[string]int64, error) {
	out := map[string]int64{}
	switch v2.EventType(ev.EventType) {
	case v2.EventTypeSessionStarted, v2.EventTypeSessionEnded:
		if len(ev.SessionKey) != 32 {
			return out, nil
		}
		started, completed, userStart, sessionDur, turnDur, parent, err := mysqlstore.LoadBucketEntityTx(
			ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, telemetryagg.EntityKindSession, ev.SessionKey)
		if err != nil {
			return nil, err
		}
		ent := telemetryagg.EntitySnapshot{
			Kind: telemetryagg.EntityKindSession, HasStarted: started, HasCompleted: completed, HasUserStart: userStart,
			SessionDurationMs: mysqlstoreUintPtr(sessionDur), TurnDurationMs: mysqlstoreUintPtr(turnDur),
		}
		isChild := false
		if payload.Context != nil && payload.Context.ParentSessionKey != nil {
			isChild = true
		}
		if len(parent) == 32 {
			isChild = true
		}
		var flag telemetryagg.EntityFlagDelta
		var dur *uint64
		if payload.Activity != nil && payload.Activity.DurationMs != nil {
			v, err := strconv.ParseUint(string(*payload.Activity.DurationMs), 10, 64)
			if err == nil {
				dur = &v
			}
		}
		if v2.EventType(ev.EventType) == v2.EventTypeSessionStarted {
			flag = telemetryagg.ApplySessionStarted(&ent, isChild)
		} else {
			flag = telemetryagg.ApplySessionEnded(&ent, isChild, dur)
		}
		addFlag(out, flag)
		var parentBytes []byte
		if payload.Context != nil && payload.Context.ParentSessionKey != nil {
			decoded, err := decodeB64URL32(string(*payload.Context.ParentSessionKey))
			if err == nil {
				parentBytes = decoded[:]
			}
		}
		sessNull := sql.NullInt64{}
		if ent.SessionDurationMs != nil {
			sessNull = sql.NullInt64{Int64: int64(*ent.SessionDurationMs), Valid: true}
		}
		turnNull := sql.NullInt64{}
		if ent.TurnDurationMs != nil {
			turnNull = sql.NullInt64{Int64: int64(*ent.TurnDurationMs), Valid: true}
		}
		if err := mysqlstore.UpsertBucketEntityTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID,
			telemetryagg.EntityKindSession, ev.SessionKey, parentBytes, ent.HasStarted, ent.HasCompleted, ent.HasUserStart, sessNull, turnNull, nowMs); err != nil {
			return nil, err
		}
	case v2.EventTypeTurnStarted, v2.EventTypeTurnCompleted:
		if len(ev.TurnKey) != 32 {
			return out, nil
		}
		started, completed, userStart, sessionDur, turnDur, _, err := mysqlstore.LoadBucketEntityTx(
			ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, telemetryagg.EntityKindTurn, ev.TurnKey)
		if err != nil {
			return nil, err
		}
		ent := telemetryagg.EntitySnapshot{
			Kind: telemetryagg.EntityKindTurn, HasStarted: started, HasCompleted: completed, HasUserStart: userStart,
			SessionDurationMs: mysqlstoreUintPtr(sessionDur), TurnDurationMs: mysqlstoreUintPtr(turnDur),
		}
		var flag telemetryagg.EntityFlagDelta
		var dur *uint64
		if payload.Activity != nil && payload.Activity.DurationMs != nil {
			v, err := strconv.ParseUint(string(*payload.Activity.DurationMs), 10, 64)
			if err == nil {
				dur = &v
			}
		}
		if v2.EventType(ev.EventType) == v2.EventTypeTurnStarted {
			userTrig := payload.Activity != nil && payload.Activity.Trigger != nil && *payload.Activity.Trigger == v2.TurnTriggerUser
			flag = telemetryagg.ApplyTurnStarted(&ent, userTrig)
		} else {
			flag = telemetryagg.ApplyTurnCompleted(&ent, dur)
		}
		addFlag(out, flag)
		var parentBytes []byte
		if len(ev.SessionKey) == 32 {
			parentBytes = ev.SessionKey
		}
		sessNull := sql.NullInt64{}
		turnNull := sql.NullInt64{}
		if ent.TurnDurationMs != nil {
			turnNull = sql.NullInt64{Int64: int64(*ent.TurnDurationMs), Valid: true}
		}
		if err := mysqlstore.UpsertBucketEntityTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID,
			telemetryagg.EntityKindTurn, ev.TurnKey, parentBytes, ent.HasStarted, ent.HasCompleted, ent.HasUserStart, sessNull, turnNull, nowMs); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func addFlag(out map[string]int64, f telemetryagg.EntityFlagDelta) {
	out["session_count"] += int64(f.SessionCount)
	out["child_session_count"] += int64(f.ChildSessionCount)
	out["interaction_turn_count"] += int64(f.InteractionTurnCount)
	out["turn_started_count"] += int64(f.TurnStartedCount)
	out["turn_completed_count"] += int64(f.TurnCompletedCount)
	out["user_turn_started_count"] += int64(f.UserTurnStartedCount)
}

func mysqlstoreUintPtr(n sql.NullInt64) *uint64 {
	if !n.Valid {
		return nil
	}
	v := uint64(n.Int64)
	return &v
}

func accumulateUsage(payload *v2.EventPayload, d map[string]int64) error {
	if payload.Meta == nil {
		return nil
	}
	acc := payload.Meta.Accuracy
	if acc == v2.AccuracyEstimated {
		return nil
	}
	if payload.Usage == nil {
		return nil
	}
	u := payload.Usage
	d["usage_observed_count"] = 1
	d["model_request_count"] = 1
	if u.RequestCount != nil {
		if v, err := strconv.ParseUint(string(*u.RequestCount), 10, 64); err == nil && v > 0 {
			d["model_request_count"] = int64(v)
		}
	}
	addToken := func(field string, knownField string, raw *v2.UInt64String) {
		if raw == nil {
			return
		}
		v, err := strconv.ParseUint(string(*raw), 10, 64)
		if err != nil {
			return
		}
		d[field] += int64(v)
		d[knownField] += 1
	}
	var total *v2.UInt64String
	if u.TokenTotal != nil {
		total = u.TokenTotal
		d["token_total_known_count"] = 1
		switch acc {
		case v2.AccuracyExact:
			v, _ := strconv.ParseUint(string(*total), 10, 64)
			d["exact_token_total"] += int64(v)
		case v2.AccuracyDerived:
			v, _ := strconv.ParseUint(string(*total), 10, 64)
			d["derived_token_total"] += int64(v)
		}
	}
	addToken("input_context_tokens", "input_context_known_count", u.InputContextTokens)
	addToken("output_tokens", "output_known_count", u.OutputTokens)
	addToken("cache_read_tokens", "cache_read_known_count", u.CacheReadTokens)
	addToken("cache_write_tokens", "cache_write_known_count", u.CacheWriteTokens)
	addToken("reasoning_tokens", "reasoning_known_count", u.ReasoningTokens)
	if u.InputContextTokens != nil && u.CacheReadTokens != nil {
		inV, err1 := strconv.ParseUint(string(*u.InputContextTokens), 10, 64)
		rdV, err2 := strconv.ParseUint(string(*u.CacheReadTokens), 10, 64)
		if err1 == nil && err2 == nil && rdV <= inV {
			d["cache_eligible_input_tokens"] += int64(inV)
			d["cache_eligible_read_tokens"] += int64(rdV)
			d["cache_pair_known_count"] += 1
		}
	}
	_ = total
	return nil
}

func accumulateCode(payload *v2.EventPayload, d map[string]int64) {
	if payload.Meta == nil || payload.Code == nil {
		return
	}
	acc := payload.Meta.Accuracy
	c := payload.Code
	parse := func(raw *v2.UInt64String) (uint64, bool) {
		if raw == nil {
			return 0, false
		}
		v, err := strconv.ParseUint(string(*raw), 10, 64)
		return v, err == nil
	}
	if acc == v2.AccuracyCorrelated {
		if v, ok := parse(c.Generated); ok {
			d["correlated_code_lines"] += int64(v)
			d["code_known_count"] += 1
		}
		return
	}
	if acc != v2.AccuracyExact && acc != v2.AccuracyDerived {
		return
	}
	known := false
	if v, ok := parse(c.Generated); ok {
		d["code_generated_lines"] += int64(v)
		known = true
	}
	if v, ok := parse(c.Accepted); ok {
		d["code_accepted_lines"] += int64(v)
		known = true
	}
	if v, ok := parse(c.Added); ok {
		d["code_added_lines"] += int64(v)
		known = true
	}
	if v, ok := parse(c.Removed); ok {
		d["code_removed_lines"] += int64(v)
		known = true
	}
	if v, ok := parse(c.FileTouchCount); ok {
		d["code_file_touch_count"] += int64(v)
		known = true
	}
	if known {
		d["code_known_count"] += 1
	}
}

func accumulateSkill(payload *v2.EventPayload, d map[string]int64) {
	d["use_count"] = 1
	if payload.Meta != nil {
		switch payload.Meta.Accuracy {
		case v2.AccuracyExact:
			d["exact_use_count"] = 1
		case v2.AccuracyDerived:
			d["derived_use_count"] = 1
		case v2.AccuracyCorrelated:
			d["correlated_use_count"] = 1
		}
	}
	if payload.Activity != nil {
		if payload.Activity.Success != nil {
			if *payload.Activity.Success {
				d["success_count"] = 1
			} else {
				d["failure_count"] = 1
			}
		}
		if payload.Activity.DurationMs != nil {
			if v, err := strconv.ParseUint(string(*payload.Activity.DurationMs), 10, 64); err == nil {
				d["duration_ms"] = int64(v)
				d["duration_known_count"] = 1
			}
		}
	}
}

func applyDurationDiff(
	ctx context.Context,
	tx *sql.Tx,
	grain string,
	bucketStart int64,
	ev *telemetryEventRow,
	payload *v2.EventPayload,
	nowMs int64,
	harnessDelta map[string]int64,
) error {
	if len(ev.SessionKey) != 32 {
		return nil
	}
	if v2.EventType(ev.EventType) != v2.EventTypeSessionEnded && v2.EventType(ev.EventType) != v2.EventTypeTurnCompleted {
		return nil
	}
	facts, err := loadDurationFacts(ctx, tx, ev, grain)
	if err != nil {
		return err
	}
	// Include candidate event.
	cand := telemetryagg.DurationFact{
		EventRowID:   int64(ev.ID),
		OccurredAtMs: ev.OccurredAtMs,
		EventType:    v2.EventType(ev.EventType),
		SessionKey:   bytesTo32(ev.SessionKey),
	}
	if payload.Activity != nil && payload.Activity.DurationMs != nil {
		if v, err := strconv.ParseUint(string(*payload.Activity.DurationMs), 10, 64); err == nil {
			cand.DurationMs = &v
		}
	}
	if v2.EventType(ev.EventType) == v2.EventTypeSessionEnded {
		isChild := payload.Context != nil && payload.Context.ParentSessionKey != nil
		cand.IsSessionEnd = !isChild
	}
	if v2.EventType(ev.EventType) == v2.EventTypeTurnCompleted {
		cand.IsTurnEnd = true
		if len(ev.TurnKey) == 32 {
			tk := bytesTo32(ev.TurnKey)
			cand.TurnKey = &tk
		}
	}

	oldFacts := facts
	newFacts := append(append([]telemetryagg.DurationFact{}, facts...), cand)
	sessionKey := bytesTo32(ev.SessionKey)

	switch grain {
	case domain.TelemetryGrainHour:
		dayStart, dayEnd := telemetryagg.BeijingDayBounds(ev.OccurredAtMs)
		oldMap := telemetryagg.SessionHourContributions(oldFacts, sessionKey, dayStart, dayEnd)
		newMap := telemetryagg.SessionHourContributions(newFacts, sessionKey, dayStart, dayEnd)
		deltas := telemetryagg.DiffHourDurationMaps(oldMap, newMap)
		oldSum := telemetryagg.SumDurationMap(oldMap)
		newSum := telemetryagg.SumDurationMap(newMap)
		knownBumpDone := false
		for hourBucket, delta := range deltas {
			d := map[string]int64{"active_duration_ms": delta}
			if !knownBumpDone && oldSum == 0 && newSum > 0 && delta > 0 {
				d["duration_known_count"] = 1
				knownBumpDone = true
			}
			if hourBucket == bucketStart {
				harnessDelta["active_duration_ms"] += delta
				if d["duration_known_count"] != 0 {
					harnessDelta["duration_known_count"] += d["duration_known_count"]
				}
				continue
			}
			if err := mysqlstore.ApplyHarnessMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, hourBucket, ev.HarnessID, ev.MetricSemanticsVersion, nowMs, d); err != nil {
				return err
			}
		}
	case domain.TelemetryGrainDay:
		dayStart, dayEnd := telemetryagg.BeijingDayBounds(ev.OccurredAtMs)
		oldDur := telemetryagg.SelectSessionDayDuration(oldFacts, sessionKey, dayStart, dayEnd)
		newDur := telemetryagg.SelectSessionDayDuration(newFacts, sessionKey, dayStart, dayEnd)
		delta := int64(newDur) - int64(oldDur)
		if delta != 0 {
			harnessDelta["active_duration_ms"] += delta
			if oldDur == 0 && newDur > 0 {
				harnessDelta["duration_known_count"] += 1
			}
		}
	case domain.TelemetryGrainMonth:
		// Per Beijing day selection then sum into month bucket.
		days := map[int64]struct{}{}
		for _, f := range newFacts {
			ds := domain.StartOfDay(time.UnixMilli(f.OccurredAtMs)).UnixMilli()
			days[ds] = struct{}{}
		}
		var oldSum, newSum uint64
		for ds := range days {
			de := time.UnixMilli(ds).In(domain.DayTZ).AddDate(0, 0, 1).UnixMilli()
			oldSum += telemetryagg.SelectSessionDayDuration(oldFacts, bytesTo32(ev.SessionKey), ds, de)
			newSum += telemetryagg.SelectSessionDayDuration(newFacts, bytesTo32(ev.SessionKey), ds, de)
		}
		delta := int64(newSum) - int64(oldSum)
		if delta != 0 {
			harnessDelta["active_duration_ms"] += delta
		}
	}
	return nil
}

func loadDurationFacts(ctx context.Context, tx *sql.Tx, ev *telemetryEventRow, grain string) ([]telemetryagg.DurationFact, error) {
	statusPath := "$." + grain
	rows, err := tx.QueryContext(ctx, `
		SELECT id, occurred_at, event_type, session_key, turn_key,
		       CAST(JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.activity.duration_ms')) AS UNSIGNED),
		       JSON_EXTRACT(payload_json, '$.context.parent_session_key') IS NOT NULL
		FROM telemetry_events
		WHERE installation_id = ?
		  AND harness_id = ?
		  AND session_key = ?
		  AND delete_at IS NULL
		  AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json, ?)) AS UNSIGNED) = ?
		  AND id <> ?`,
		ev.InstallationID, ev.HarnessID, ev.SessionKey, statusPath, domain.TelemetryStatusApplied, ev.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []telemetryagg.DurationFact
	for rows.Next() {
		var f telemetryagg.DurationFact
		var sessionKey, turnKey []byte
		var dur sql.NullInt64
		var hasParent bool
		var eventType string
		if err := rows.Scan(&f.EventRowID, &f.OccurredAtMs, &eventType, &sessionKey, &turnKey, &dur, &hasParent); err != nil {
			return nil, err
		}
		f.EventType = v2.EventType(eventType)
		f.SessionKey = bytesTo32(sessionKey)
		if len(turnKey) == 32 {
			tk := bytesTo32(turnKey)
			f.TurnKey = &tk
		}
		if dur.Valid {
			v := uint64(dur.Int64)
			f.DurationMs = &v
		}
		if f.EventType == v2.EventTypeSessionEnded && !hasParent {
			f.IsSessionEnd = true
		}
		if f.EventType == v2.EventTypeTurnCompleted {
			f.IsTurnEnd = true
		}
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func applyCostDiff(
	ctx context.Context,
	tx *sql.Tx,
	grain string,
	bucketStart int64,
	ev *telemetryEventRow,
	payload *v2.EventPayload,
	nowMs int64,
) error {
	if payload.Cost == nil {
		return nil
	}
	if len(ev.CostScopeKey) != 32 {
		// No scope: treat as independent additive reported/calculated fact.
		units, err := strconv.ParseUint(string(payload.Cost.Units), 10, 64)
		if err != nil {
			return err
		}
		d := map[string]int64{"cost_known_count": 1}
		if payload.Cost.Source == v2.CostSourceProviderReported {
			d["reported_cost_units"] = int64(units)
			d["reported_request_count"] = 1
		} else {
			d["estimated_cost_units"] = int64(units)
			d["estimated_request_count"] = 1
		}
		return mysqlstore.ApplyCostMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, ev.ModelKey, payload.Cost.Currency, ev.MetricSemanticsVersion, nowMs, d)
	}

	oldFacts, err := loadCostFacts(ctx, tx, ev, grain)
	if err != nil {
		return err
	}
	units, err := strconv.ParseUint(string(payload.Cost.Units), 10, 64)
	if err != nil {
		return err
	}
	cand := telemetryagg.CostFact{
		EventRowID: int64(ev.ID),
		OccurredAt: ev.OccurredAtMs,
		ModelKey:   ev.ModelKey,
		Currency:   payload.Cost.Currency,
		Units:      units,
		Source:     payload.Cost.Source,
	}
	oldEff := telemetryagg.SelectEffectiveCosts(oldFacts)
	newEff := telemetryagg.SelectEffectiveCosts(append(append([]telemetryagg.CostFact{}, oldFacts...), cand))
	for _, delta := range telemetryagg.DiffEffectiveCosts(oldEff, newEff) {
		d := map[string]int64{
			"reported_cost_units":     delta.ReportedUnits,
			"estimated_cost_units":    delta.EstimatedUnits,
			"reported_request_count":  delta.ReportedRequestCount,
			"estimated_request_count": delta.EstimatedRequestCount,
			"unpriced_request_count":  delta.UnpricedRequestCount,
			"cost_known_count":        delta.CostKnownCount,
		}
		if err := mysqlstore.ApplyCostMetricDeltaTx(ctx, tx, ev.UserID, ev.InstallationID, grain, bucketStart, ev.HarnessID, delta.ModelKey, delta.Currency, ev.MetricSemanticsVersion, nowMs, d); err != nil {
			return err
		}
	}
	return nil
}

func loadCostFacts(ctx context.Context, tx *sql.Tx, ev *telemetryEventRow, grain string) ([]telemetryagg.CostFact, error) {
	statusPath := "$." + grain
	rows, err := tx.QueryContext(ctx, `
		SELECT id, occurred_at, model_key,
		       JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.cost.currency')),
		       CAST(JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.cost.units')) AS UNSIGNED),
		       JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.cost.source'))
		FROM telemetry_events
		WHERE installation_id = ?
		  AND harness_id = ?
		  AND cost_scope_key = ?
		  AND delete_at IS NULL
		  AND CAST(JSON_UNQUOTE(JSON_EXTRACT(status_json, ?)) AS UNSIGNED) = ?
		  AND id <> ?
		  AND JSON_EXTRACT(payload_json, '$.cost') IS NOT NULL`,
		ev.InstallationID, ev.HarnessID, ev.CostScopeKey, statusPath, domain.TelemetryStatusApplied, ev.ID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var facts []telemetryagg.CostFact
	for rows.Next() {
		var f telemetryagg.CostFact
		var source string
		if err := rows.Scan(&f.EventRowID, &f.OccurredAt, &f.ModelKey, &f.Currency, &f.Units, &source); err != nil {
			return nil, err
		}
		f.Source = v2.CostSource(source)
		facts = append(facts, f)
	}
	return facts, rows.Err()
}

func bytesTo32(b []byte) [32]byte {
	var out [32]byte
	copy(out[:], b)
	return out
}

func decodeB64URL32(s string) ([32]byte, error) {
	var out [32]byte
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil || len(raw) != 32 {
		return out, fmt.Errorf("invalid base64url32")
	}
	copy(out[:], raw)
	return out, nil
}
