//! Local hour/day/month metrics apply (P4).
//!
//! Claim stays in its own txn; apply + mark applied + delete task must share one txn.

use std::collections::{HashMap, HashSet};

use rusqlite::{params, OptionalExtension, Transaction};
use serde_json::Value;

use super::buckets::{beijing_day_end, beijing_day_start, bucket_start, Grain};
use super::types::{Consumer, ConsumerStatus, PipelineError};

#[derive(Debug, Clone)]
pub struct EventRow {
    pub id: i64,
    pub harness_id: String,
    pub event_type: String,
    pub occurred_at: i64,
    pub model_key: i64,
    pub skill_id: Option<i64>,
    pub session_key: Option<Vec<u8>>,
    pub turn_key: Option<Vec<u8>>,
    pub cost_scope_key: Option<Vec<u8>>,
    pub payload_json: String,
    pub metric_semantics_version: i64,
}

/// Apply metrics for one leased task and mark the lane applied in the same transaction.
pub fn apply_and_complete_in_tx(
    tx: &Transaction<'_>,
    task_id: i64,
    event_row_id: i64,
    consumer: Consumer,
    lease_token: &str,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let grain = Grain::from_consumer(consumer)?;
    verify_lease_inflight(tx, task_id, event_row_id, consumer, lease_token, now_ms)?;
    let event = load_event_row(tx, event_row_id, consumer)?;
    apply_event_metrics(tx, grain, &event, now_ms)?;
    tx.execute(
        "UPDATE events
         SET status_json=json_set(status_json, ?1, ?2), updated_at=?3
         WHERE id=?4 AND delete_at IS NULL AND expire_at>?3",
        params![
            consumer.status_path(),
            ConsumerStatus::Applied as i64,
            now_ms,
            event_row_id
        ],
    )?;
    tx.execute(
        "DELETE FROM processing_tasks WHERE id=?1",
        params![task_id],
    )?;
    Ok(())
}

fn verify_lease_inflight(
    tx: &Transaction<'_>,
    task_id: i64,
    event_row_id: i64,
    consumer: Consumer,
    lease_token: &str,
    now: i64,
) -> Result<(), PipelineError> {
    let row: Option<(i64, String, Option<String>, Option<i64>, Option<i64>)> = tx
        .query_row(
            "SELECT event_row_id, consumer, lease_token, lease_until, delete_at
             FROM processing_tasks WHERE id=?1",
            params![task_id],
            |row| {
                Ok((
                    row.get(0)?,
                    row.get(1)?,
                    row.get(2)?,
                    row.get(3)?,
                    row.get(4)?,
                ))
            },
        )
        .optional()?;
    let Some((row_event_id, row_consumer, token, lease_until, delete_at)) = row else {
        return Err(PipelineError::TaskNotRunnable);
    };
    if delete_at.is_some()
        || row_event_id != event_row_id
        || row_consumer != consumer.as_str()
    {
        return Err(PipelineError::TaskNotRunnable);
    }
    match (token.as_deref(), lease_until) {
        (Some(t), Some(until)) if t == lease_token && until > now => {}
        _ => return Err(PipelineError::TaskLeaseMismatch),
    }
    let event: Option<(Option<i64>, i64, i64)> = tx
        .query_row(
            "SELECT delete_at, expire_at, CAST(json_extract(status_json, ?1) AS INTEGER)
             FROM events WHERE id=?2",
            params![consumer.status_path(), event_row_id],
            |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
        )
        .optional()?;
    let Some((delete_at, expire_at, status)) = event else {
        return Err(PipelineError::EventExpiredOrDeleted);
    };
    if delete_at.is_some() || expire_at <= now {
        return Err(PipelineError::EventExpiredOrDeleted);
    }
    // Already applied → idempotent no-op path for metrics would double-count;
    // treat as not runnable so callers discard the lease.
    if status == ConsumerStatus::Applied as i64 {
        return Err(PipelineError::TaskNotRunnable);
    }
    if status != ConsumerStatus::InFlight as i64 {
        return Err(PipelineError::TaskNotRunnable);
    }
    Ok(())
}

pub fn load_event_row(
    tx: &Transaction<'_>,
    event_row_id: i64,
    _consumer: Consumer,
) -> Result<EventRow, PipelineError> {
    tx.query_row(
        "SELECT id, harness_id, event_type, occurred_at, model_key, skill_id,
                session_key, turn_key, cost_scope_key, payload_json,
                metric_semantics_version
         FROM events WHERE id=?1",
        params![event_row_id],
        |row| {
            Ok(EventRow {
                id: row.get(0)?,
                harness_id: row.get(1)?,
                event_type: row.get(2)?,
                occurred_at: row.get(3)?,
                model_key: row.get(4)?,
                skill_id: row.get(5)?,
                session_key: row.get(6)?,
                turn_key: row.get(7)?,
                cost_scope_key: row.get(8)?,
                payload_json: row.get(9)?,
                metric_semantics_version: row.get(10)?,
            })
        },
    )
    .map_err(PipelineError::from)
}

fn apply_event_metrics(
    tx: &Transaction<'_>,
    grain: Grain,
    event: &EventRow,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let payload: Value = serde_json::from_str(&event.payload_json)
        .map_err(|e| PipelineError::InvalidArgument(format!("payload_json: {e}")))?;
    let bucket = bucket_start(grain, event.occurred_at);
    let semantics = event.metric_semantics_version.max(1);

    match event.event_type.as_str() {
        "model_usage_recorded" => apply_usage(tx, grain, bucket, event, &payload, semantics, now_ms)?,
        "code_changed" => apply_code(tx, grain, bucket, event, &payload, semantics, now_ms)?,
        "tool_invoked" => {
            bump_harness(
                tx,
                grain,
                bucket,
                &event.harness_id,
                semantics,
                now_ms,
                &[("tool_call_count", 1)],
            )?;
        }
        "skill_invoked" => apply_skill(tx, grain, bucket, event, &payload, semantics, now_ms)?,
        "session_started" | "session_ended" | "turn_started" | "turn_completed" => {
            apply_activity(tx, grain, event, &payload, semantics, now_ms)?;
        }
        "cost_recorded" => apply_cost(tx, grain, event, &payload, semantics, now_ms)?,
        _ => {
            // Unknown types: no metrics contribution (still mark applied by caller).
        }
    }
    Ok(())
}

fn accuracy(payload: &Value) -> &str {
    payload
        .pointer("/meta/accuracy")
        .and_then(|v| v.as_str())
        .unwrap_or("unknown")
}

fn json_i64(payload: &Value, path: &str) -> Option<i64> {
    payload.pointer(path).and_then(|v| v.as_i64())
}

fn apply_usage(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    event: &EventRow,
    payload: &Value,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let acc = accuracy(payload);
    if acc != "exact" && acc != "derived" {
        return Ok(());
    }
    ensure_model_metrics(tx, grain, bucket, &event.harness_id, event.model_key, semantics, now_ms)?;

    let mut sets: Vec<(&str, i64)> = vec![("usage_observed_count", 1), ("model_request_count", 1)];
    if let Some(v) = json_i64(payload, "/usage/token_total") {
        if acc == "exact" {
            sets.push(("exact_token_total", v));
        } else {
            sets.push(("derived_token_total", v));
        }
        sets.push(("token_total_known_count", 1));
    }
    for (col, path, known) in [
        ("input_context_tokens", "/usage/input_context_tokens", "input_context_known_count"),
        ("input_uncached_tokens", "/usage/input_uncached_tokens", "input_uncached_known_count"),
        ("output_tokens", "/usage/output_tokens", "output_known_count"),
        ("cache_read_tokens", "/usage/cache_read_tokens", "cache_read_known_count"),
        ("cache_write_tokens", "/usage/cache_write_tokens", "cache_write_known_count"),
        ("reasoning_tokens", "/usage/reasoning_tokens", "reasoning_known_count"),
        ("tool_extra_tokens", "/usage/tool_extra_tokens", "tool_extra_known_count"),
    ] {
        if let Some(v) = json_i64(payload, path) {
            sets.push((col, v));
            sets.push((known, 1));
        }
    }
    let input_ctx = json_i64(payload, "/usage/input_context_tokens");
    let cache_read = json_i64(payload, "/usage/cache_read_tokens");
    if let (Some(inp), Some(read)) = (input_ctx, cache_read) {
        if read <= inp {
            sets.push(("cache_eligible_input_tokens", inp));
            sets.push(("cache_eligible_read_tokens", read));
            sets.push(("cache_pair_known_count", 1));
        }
    }
    bump_model_metrics(tx, grain, bucket, &event.harness_id, event.model_key, now_ms, &sets)?;
    Ok(())
}

fn apply_code(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    event: &EventRow,
    payload: &Value,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let acc = accuracy(payload);
    let mut sets: Vec<(&str, i64)> = Vec::new();
    let mut known = false;
    if acc == "correlated" {
        if let Some(v) = json_i64(payload, "/code/generated_lines") {
            sets.push(("correlated_code_lines", v));
            known = true;
        }
    } else if acc == "exact" || acc == "derived" {
        for (col, path) in [
            ("code_generated_lines", "/code/generated_lines"),
            ("code_accepted_lines", "/code/accepted_lines"),
            ("code_added_lines", "/code/added_lines"),
            ("code_removed_lines", "/code/removed_lines"),
            ("code_file_touch_count", "/code/file_count"),
        ] {
            if let Some(v) = json_i64(payload, path) {
                sets.push((col, v));
                known = true;
            }
        }
    }
    if known {
        sets.push(("code_known_count", 1));
    }
    if !sets.is_empty() {
        bump_harness(tx, grain, bucket, &event.harness_id, semantics, now_ms, &sets)?;
    }
    Ok(())
}

fn apply_skill(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    event: &EventRow,
    payload: &Value,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let Some(skill_id) = event.skill_id else {
        return Err(PipelineError::InvalidArgument(
            "skill_invoked requires skill_id".into(),
        ));
    };
    // Dimension must already exist (registered once).
    let exists: Option<i64> = tx
        .query_row(
            "SELECT id FROM skill_dimensions WHERE id=?1 AND delete_at IS NULL",
            params![skill_id],
            |r| r.get(0),
        )
        .optional()?;
    if exists.is_none() {
        return Err(PipelineError::InvalidArgument(format!(
            "skill_id {skill_id} not registered"
        )));
    }
    ensure_skill_metrics(tx, grain, bucket, &event.harness_id, skill_id, semantics, now_ms)?;
    let acc = accuracy(payload);
    let mut sets: Vec<(&str, i64)> = vec![("use_count", 1)];
    match acc {
        "exact" => sets.push(("exact_use_count", 1)),
        "derived" => sets.push(("derived_use_count", 1)),
        "correlated" => sets.push(("correlated_use_count", 1)),
        _ => {}
    }
    match payload.pointer("/activity/success") {
        Some(Value::Bool(true)) => sets.push(("success_count", 1)),
        Some(Value::Bool(false)) => sets.push(("failure_count", 1)),
        _ => {}
    }
    if let Some(d) = json_i64(payload, "/activity/duration_ms") {
        sets.push(("duration_ms", d));
        sets.push(("duration_known_count", 1));
    }
    bump_skill_metrics(tx, grain, bucket, &event.harness_id, skill_id, now_ms, &sets)?;
    bump_harness(
        tx,
        grain,
        bucket,
        &event.harness_id,
        semantics,
        now_ms,
        &[("skill_use_count", 1)],
    )?;
    Ok(())
}

fn apply_activity(
    tx: &Transaction<'_>,
    grain: Grain,
    event: &EventRow,
    payload: &Value,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    match event.event_type.as_str() {
        "session_started" | "session_ended" => {
            let Some(session_key) = event.session_key.as_deref() else {
                return Ok(());
            };
            let parent = payload
                .pointer("/context/parent_session_key")
                .and_then(|v| v.as_str())
                .and_then(parse_hex32);
            let is_child = parent.is_some();
            let bucket = bucket_start(grain, event.occurred_at);
            let first = upsert_entity(
                tx,
                grain,
                bucket,
                &event.harness_id,
                "session",
                session_key,
                parent.as_ref().map(|p| p.as_slice()),
                now_ms,
            )?;
            if first {
                let col = if is_child {
                    "child_session_count"
                } else {
                    "session_count"
                };
                bump_harness(
                    tx,
                    grain,
                    bucket,
                    &event.harness_id,
                    semantics,
                    now_ms,
                    &[(col, 1)],
                )?;
            }
            if event.event_type == "session_started" {
                set_entity_flag(
                    tx,
                    grain,
                    bucket,
                    &event.harness_id,
                    "session",
                    session_key,
                    "has_started",
                    now_ms,
                )?;
            }
            if event.event_type == "session_ended" {
                set_entity_flag(
                    tx,
                    grain,
                    bucket,
                    &event.harness_id,
                    "session",
                    session_key,
                    "has_completed",
                    now_ms,
                )?;
                if let Some(dur) = json_i64(payload, "/activity/duration_ms") {
                    apply_duration_with_session_end(
                        tx, grain, event, session_key, dur, semantics, now_ms,
                    )?;
                }
            }
        }
        "turn_started" | "turn_completed" => {
            let Some(turn_key) = event.turn_key.as_deref() else {
                return Ok(());
            };
            let bucket = bucket_start(grain, event.occurred_at);
            let parent = event.session_key.as_deref();
            let first = upsert_entity(
                tx,
                grain,
                bucket,
                &event.harness_id,
                "turn",
                turn_key,
                parent,
                now_ms,
            )?;
            if first {
                bump_harness(
                    tx,
                    grain,
                    bucket,
                    &event.harness_id,
                    semantics,
                    now_ms,
                    &[("interaction_turn_count", 1)],
                )?;
            }
            if event.event_type == "turn_started" {
                let became = set_entity_flag(
                    tx,
                    grain,
                    bucket,
                    &event.harness_id,
                    "turn",
                    turn_key,
                    "has_started",
                    now_ms,
                )?;
                if became {
                    bump_harness(
                        tx,
                        grain,
                        bucket,
                        &event.harness_id,
                        semantics,
                        now_ms,
                        &[("turn_started_count", 1), ("message_known_count", 1)],
                    )?;
                }
                let trigger = payload
                    .pointer("/activity/trigger")
                    .and_then(|v| v.as_str())
                    .unwrap_or("");
                if trigger == "user" {
                    let user = set_entity_flag(
                        tx,
                        grain,
                        bucket,
                        &event.harness_id,
                        "turn",
                        turn_key,
                        "has_user_start",
                        now_ms,
                    )?;
                    if user {
                        bump_harness(
                            tx,
                            grain,
                            bucket,
                            &event.harness_id,
                            semantics,
                            now_ms,
                            &[("user_turn_started_count", 1)],
                        )?;
                    }
                }
            }
            if event.event_type == "turn_completed" {
                let became = set_entity_flag(
                    tx,
                    grain,
                    bucket,
                    &event.harness_id,
                    "turn",
                    turn_key,
                    "has_completed",
                    now_ms,
                )?;
                if became {
                    bump_harness(
                        tx,
                        grain,
                        bucket,
                        &event.harness_id,
                        semantics,
                        now_ms,
                        &[("turn_completed_count", 1), ("message_known_count", 1)],
                    )?;
                }
                if let Some(dur) = json_i64(payload, "/activity/duration_ms") {
                    // Store turn duration on entity; adjust harness if no session_end authority.
                    let old = get_turn_duration(tx, grain, bucket, &event.harness_id, turn_key)?;
                    set_turn_duration(tx, grain, bucket, &event.harness_id, turn_key, dur, now_ms)?;
                    if let Some(session_key) = event.session_key.as_deref() {
                        apply_duration_turn_update(
                            tx,
                            grain,
                            event,
                            session_key,
                            turn_key,
                            old,
                            dur,
                            semantics,
                            now_ms,
                        )?;
                    } else {
                        // Orphan turn duration contributes to its bucket directly.
                        let delta = dur - old.unwrap_or(0);
                        if delta != 0 {
                            bump_harness(
                                tx,
                                grain,
                                bucket,
                                &event.harness_id,
                                semantics,
                                now_ms,
                                &[("active_duration_ms", delta)],
                            )?;
                            if old.is_none() {
                                bump_harness(
                                    tx,
                                    grain,
                                    bucket,
                                    &event.harness_id,
                                    semantics,
                                    now_ms,
                                    &[("duration_known_count", 1)],
                                )?;
                            }
                        }
                    }
                }
            }
        }
        _ => {}
    }
    Ok(())
}

fn parse_hex32(s: &str) -> Option<[u8; 32]> {
    // Local payloads may store parent as JSON string hex or we skip if not 64 hex chars.
    if s.len() != 64 {
        return None;
    }
    let mut out = [0u8; 32];
    for i in 0..32 {
        out[i] = u8::from_str_radix(&s[i * 2..i * 2 + 2], 16).ok()?;
    }
    Some(out)
}

/// Duration authority helpers.
fn apply_duration_with_session_end(
    tx: &Transaction<'_>,
    grain: Grain,
    event: &EventRow,
    session_key: &[u8],
    session_dur: i64,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let day_start = beijing_day_start(event.occurred_at);
    let day_end = beijing_day_end(event.occurred_at);

    match grain {
        Grain::Day | Grain::Month => {
            // Month: authority is per business day, credited into the month bucket.
            let metric_bucket = bucket_start(grain, event.occurred_at);
            let old = session_day_authority(tx, grain, &event.harness_id, session_key, day_start, day_end)?;
            // Record session duration on the day-scoped entity row inside this grain's bucket.
            // For month grain, entity rows live under month bucket_start but we still key by day
            // via storing duration on the session entity in this month bucket; day scoping for
            // authority uses applied events query below. Store duration on entity for inspection.
            let entity_bucket = metric_bucket;
            set_session_duration(
                tx,
                grain,
                entity_bucket,
                &event.harness_id,
                session_key,
                session_dur,
                now_ms,
            )?;
            let new = Some(session_dur);
            let old_v = old.unwrap_or(0);
            let new_v = new.unwrap_or(0);
            let delta = new_v - old_v;
            if delta != 0 || old.is_none() {
                let mut sets = vec![("active_duration_ms", delta)];
                if old.is_none() {
                    sets.push(("duration_known_count", 1));
                }
                bump_harness(
                    tx,
                    grain,
                    metric_bucket,
                    &event.harness_id,
                    semantics,
                    now_ms,
                    &sets,
                )?;
            }
        }
        Grain::Hour => {
            // Withdraw turn fallbacks from their hour buckets for this biz day; credit session_end hour.
            let turns = list_turn_durations_for_session_day(
                tx,
                &event.harness_id,
                session_key,
                day_start,
                day_end,
                Consumer::Hour,
                event.id,
            )?;
            let already_had_session = session_end_duration_excluding(
                tx,
                &event.harness_id,
                session_key,
                day_start,
                day_end,
                Consumer::Hour,
                event.id,
            )?;
            if already_had_session.is_none() {
                // Withdraw each turn from its hour bucket once.
                for (turn_occurred, turn_dur) in &turns {
                    let b = bucket_start(Grain::Hour, *turn_occurred);
                    bump_harness(
                        tx,
                        Grain::Hour,
                        b,
                        &event.harness_id,
                        semantics,
                        now_ms,
                        &[("active_duration_ms", -turn_dur)],
                    )?;
                }
            } else if let Some(prev) = already_had_session {
                // Replace previous session_end contribution in its hour (same day).
                // Find previous session_end occurred_at among applied (excluding candidate).
                if let Some(prev_at) = session_end_occurred_excluding(
                    tx,
                    &event.harness_id,
                    session_key,
                    day_start,
                    day_end,
                    Consumer::Hour,
                    event.id,
                )? {
                    let b = bucket_start(Grain::Hour, prev_at);
                    bump_harness(
                        tx,
                        Grain::Hour,
                        b,
                        &event.harness_id,
                        semantics,
                        now_ms,
                        &[("active_duration_ms", -prev)],
                    )?;
                }
            }
            let end_bucket = bucket_start(Grain::Hour, event.occurred_at);
            set_session_duration(
                tx,
                Grain::Hour,
                end_bucket,
                &event.harness_id,
                session_key,
                session_dur,
                now_ms,
            )?;
            let mut sets = vec![("active_duration_ms", session_dur)];
            if already_had_session.is_none() && turns.is_empty() {
                sets.push(("duration_known_count", 1));
            } else if already_had_session.is_none() && !turns.is_empty() {
                // duration_known_count already bumped by turns; keep as-is
            }
            bump_harness(
                tx,
                Grain::Hour,
                end_bucket,
                &event.harness_id,
                semantics,
                now_ms,
                &sets,
            )?;
        }
    }
    Ok(())
}

fn apply_duration_turn_update(
    tx: &Transaction<'_>,
    grain: Grain,
    event: &EventRow,
    session_key: &[u8],
    _turn_key: &[u8],
    old_turn: Option<i64>,
    new_turn: i64,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let day_start = beijing_day_start(event.occurred_at);
    let day_end = beijing_day_end(event.occurred_at);
    let consumer = match grain {
        Grain::Hour => Consumer::Hour,
        Grain::Day => Consumer::Day,
        Grain::Month => Consumer::Month,
    };
    // If session_end already authoritative for this biz day, turns do not contribute.
    if session_end_duration_excluding(
        tx,
        &event.harness_id,
        session_key,
        day_start,
        day_end,
        consumer,
        event.id,
    )?
    .is_some()
    {
        return Ok(());
    }

    let metric_bucket = bucket_start(grain, event.occurred_at);
    let delta = new_turn - old_turn.unwrap_or(0);
    if delta == 0 && old_turn.is_some() {
        return Ok(());
    }
    let mut sets = vec![("active_duration_ms", delta)];
    if old_turn.is_none() {
        sets.push(("duration_known_count", 1));
    }
    bump_harness(
        tx,
        grain,
        metric_bucket,
        &event.harness_id,
        semantics,
        now_ms,
        &sets,
    )?;
    Ok(())
}

fn session_day_authority(
    tx: &Transaction<'_>,
    grain: Grain,
    harness_id: &str,
    session_key: &[u8],
    day_start: i64,
    day_end: i64,
) -> Result<Option<i64>, PipelineError> {
    let consumer = match grain {
        Grain::Hour => Consumer::Hour,
        Grain::Day => Consumer::Day,
        Grain::Month => Consumer::Month,
    };
    if let Some(d) = session_end_duration_excluding(
        tx, harness_id, session_key, day_start, day_end, consumer, -1,
    )? {
        return Ok(Some(d));
    }
    let turns = list_turn_durations_for_session_day(
        tx, harness_id, session_key, day_start, day_end, consumer, -1,
    )?;
    if turns.is_empty() {
        return Ok(None);
    }
    Ok(Some(turns.iter().map(|(_, d)| *d).sum()))
}

fn session_end_duration_excluding(
    tx: &Transaction<'_>,
    harness_id: &str,
    session_key: &[u8],
    day_start: i64,
    day_end: i64,
    consumer: Consumer,
    exclude_id: i64,
) -> Result<Option<i64>, PipelineError> {
    // Prefer latest session_ended with duration among applied facts for this consumer.
    let mut stmt = tx.prepare(
        "SELECT id, payload_json FROM events
         WHERE harness_id=?1 AND session_key=?2
           AND event_type='session_ended'
           AND delete_at IS NULL
           AND occurred_at >= ?3 AND occurred_at < ?4
           AND id != ?5
           AND CAST(json_extract(status_json, ?6) AS INTEGER) = 3
         ORDER BY occurred_at DESC, id DESC",
    )?;
    let mut rows = stmt.query(params![
        harness_id,
        session_key,
        day_start,
        day_end,
        exclude_id,
        consumer.status_path()
    ])?;
    while let Some(row) = rows.next()? {
        let payload: String = row.get(1)?;
        let v: Value = serde_json::from_str(&payload)
            .map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
        if let Some(d) = json_i64(&v, "/activity/duration_ms") {
            return Ok(Some(d));
        }
    }
    Ok(None)
}

fn session_end_occurred_excluding(
    tx: &Transaction<'_>,
    harness_id: &str,
    session_key: &[u8],
    day_start: i64,
    day_end: i64,
    consumer: Consumer,
    exclude_id: i64,
) -> Result<Option<i64>, PipelineError> {
    let mut stmt = tx.prepare(
        "SELECT occurred_at, payload_json FROM events
         WHERE harness_id=?1 AND session_key=?2
           AND event_type='session_ended'
           AND delete_at IS NULL
           AND occurred_at >= ?3 AND occurred_at < ?4
           AND id != ?5
           AND CAST(json_extract(status_json, ?6) AS INTEGER) = 3
         ORDER BY occurred_at DESC, id DESC",
    )?;
    let mut rows = stmt.query(params![
        harness_id,
        session_key,
        day_start,
        day_end,
        exclude_id,
        consumer.status_path()
    ])?;
    while let Some(row) = rows.next()? {
        let occurred: i64 = row.get(0)?;
        let payload: String = row.get(1)?;
        let v: Value = serde_json::from_str(&payload)
            .map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
        if json_i64(&v, "/activity/duration_ms").is_some() {
            return Ok(Some(occurred));
        }
    }
    Ok(None)
}

fn list_turn_durations_for_session_day(
    tx: &Transaction<'_>,
    harness_id: &str,
    session_key: &[u8],
    day_start: i64,
    day_end: i64,
    consumer: Consumer,
    exclude_id: i64,
) -> Result<Vec<(i64, i64)>, PipelineError> {
    let mut stmt = tx.prepare(
        "SELECT id, turn_key, occurred_at, payload_json FROM events
         WHERE harness_id=?1 AND session_key=?2
           AND event_type='turn_completed'
           AND delete_at IS NULL
           AND occurred_at >= ?3 AND occurred_at < ?4
           AND id != ?5
           AND CAST(json_extract(status_json, ?6) AS INTEGER) = 3",
    )?;
    let rows = stmt.query_map(
        params![
            harness_id,
            session_key,
            day_start,
            day_end,
            exclude_id,
            consumer.status_path()
        ],
        |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, Option<Vec<u8>>>(1)?,
                row.get::<_, i64>(2)?,
                row.get::<_, String>(3)?,
            ))
        },
    )?;
    // Dedupe by turn_key: keep max duration / last.
    let mut by_turn: HashMap<Vec<u8>, (i64, i64)> = HashMap::new();
    for row in rows {
        let (_id, turn_key, occurred, payload) = row?;
        let Some(tk) = turn_key else { continue };
        let v: Value = serde_json::from_str(&payload)
            .map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
        let Some(d) = json_i64(&v, "/activity/duration_ms") else {
            continue;
        };
        by_turn
            .entry(tk)
            .and_modify(|(o, dur)| {
                if occurred >= *o {
                    *o = occurred;
                    *dur = d;
                }
            })
            .or_insert((occurred, d));
    }
    Ok(by_turn.into_values().collect())
}

fn apply_cost(
    tx: &Transaction<'_>,
    grain: Grain,
    event: &EventRow,
    payload: &Value,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let Some(scope) = event.cost_scope_key.as_deref() else {
        return Ok(());
    };
    let consumer = match grain {
        Grain::Hour => Consumer::Hour,
        Grain::Day => Consumer::Day,
        Grain::Month => Consumer::Month,
    };
    let old = effective_cost_for_scope(tx, &event.harness_id, scope, consumer, event.id)?;
    let new = effective_cost_for_scope_with_candidate(
        tx, &event.harness_id, scope, consumer, event, payload,
    )?;

    // Apply deltas per (model_key, currency, source-bucket).
    let mut keys: HashSet<(i64, String, i64)> = HashSet::new();
    if let Some(o) = &old {
        keys.insert((o.model_key, o.currency.clone(), o.bucket));
    }
    if let Some(n) = &new {
        keys.insert((n.model_key, n.currency.clone(), n.bucket));
    }
    for (model_key, currency, bucket) in keys {
        let old_units = old
            .as_ref()
            .filter(|c| c.model_key == model_key && c.currency == currency && c.bucket == bucket)
            .map(|c| (c.units, c.source.as_str(), c.request_count))
            .unwrap_or((0, "", 0));
        let new_units = new
            .as_ref()
            .filter(|c| c.model_key == model_key && c.currency == currency && c.bucket == bucket)
            .map(|c| (c.units, c.source.as_str(), c.request_count))
            .unwrap_or((0, "", 0));

        ensure_cost_metrics(
            tx, grain, bucket, &event.harness_id, model_key, &currency, semantics, now_ms,
        )?;

        // Withdraw old contribution.
        if old_units.1 == "provider_reported" && old_units.0 > 0 {
            bump_cost(
                tx,
                grain,
                bucket,
                &event.harness_id,
                model_key,
                &currency,
                now_ms,
                &[
                    ("reported_cost_units", -old_units.0),
                    ("reported_request_count", -old_units.2),
                    ("cost_known_count", -1),
                ],
            )?;
        } else if old_units.1 == "estimated_price_table" && old_units.0 > 0 {
            bump_cost(
                tx,
                grain,
                bucket,
                &event.harness_id,
                model_key,
                &currency,
                now_ms,
                &[
                    ("estimated_cost_units", -old_units.0),
                    ("estimated_request_count", -old_units.2),
                    ("cost_known_count", -1),
                ],
            )?;
        } else if old_units.1 == "unpriced" {
            bump_cost(
                tx,
                grain,
                bucket,
                &event.harness_id,
                model_key,
                &currency,
                now_ms,
                &[("unpriced_request_count", -old_units.2)],
            )?;
        }

        if new_units.1 == "provider_reported" {
            bump_cost(
                tx,
                grain,
                bucket,
                &event.harness_id,
                model_key,
                &currency,
                now_ms,
                &[
                    ("reported_cost_units", new_units.0),
                    ("reported_request_count", new_units.2),
                    ("cost_known_count", 1),
                ],
            )?;
        } else if new_units.1 == "estimated_price_table" {
            bump_cost(
                tx,
                grain,
                bucket,
                &event.harness_id,
                model_key,
                &currency,
                now_ms,
                &[
                    ("estimated_cost_units", new_units.0),
                    ("estimated_request_count", new_units.2),
                    ("cost_known_count", 1),
                ],
            )?;
        } else if new_units.1 == "unpriced" {
            bump_cost(
                tx,
                grain,
                bucket,
                &event.harness_id,
                model_key,
                &currency,
                now_ms,
                &[("unpriced_request_count", new_units.2)],
            )?;
        }
    }
    Ok(())
}

#[derive(Debug, Clone)]
struct CostEffect {
    model_key: i64,
    currency: String,
    units: i64,
    source: String, // provider_reported | estimated_price_table | unpriced
    request_count: i64,
    bucket: i64,
}

fn effective_cost_for_scope(
    tx: &Transaction<'_>,
    harness_id: &str,
    scope: &[u8],
    consumer: Consumer,
    exclude_id: i64,
) -> Result<Option<CostEffect>, PipelineError> {
    let facts = load_cost_facts(tx, harness_id, scope, consumer, exclude_id)?;
    Ok(select_effective_cost(facts, consumer))
}

fn effective_cost_for_scope_with_candidate(
    tx: &Transaction<'_>,
    harness_id: &str,
    scope: &[u8],
    consumer: Consumer,
    event: &EventRow,
    payload: &Value,
) -> Result<Option<CostEffect>, PipelineError> {
    let mut facts = load_cost_facts(tx, harness_id, scope, consumer, event.id)?;
    facts.push(cost_fact_from_event(event, payload)?);
    Ok(select_effective_cost(facts, consumer))
}

#[derive(Debug, Clone)]
struct CostFact {
    model_key: i64,
    currency: Option<String>,
    units: Option<i64>,
    source: Option<String>,
    occurred_at: i64,
}

fn load_cost_facts(
    tx: &Transaction<'_>,
    harness_id: &str,
    scope: &[u8],
    consumer: Consumer,
    exclude_id: i64,
) -> Result<Vec<CostFact>, PipelineError> {
    let mut stmt = tx.prepare(
        "SELECT model_key, occurred_at, payload_json FROM events
         WHERE harness_id=?1 AND cost_scope_key=?2
           AND event_type='cost_recorded'
           AND delete_at IS NULL
           AND id != ?3
           AND CAST(json_extract(status_json, ?4) AS INTEGER) = 3",
    )?;
    let rows = stmt.query_map(
        params![harness_id, scope, exclude_id, consumer.status_path()],
        |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, i64>(1)?,
                row.get::<_, String>(2)?,
            ))
        },
    )?;
    let mut out = Vec::new();
    for row in rows {
        let (model_key, occurred_at, payload) = row?;
        let v: Value = serde_json::from_str(&payload)
            .map_err(|e| PipelineError::InvalidArgument(e.to_string()))?;
        out.push(CostFact {
            model_key,
            currency: v
                .pointer("/cost/currency")
                .and_then(|x| x.as_str())
                .map(|s| s.to_string()),
            units: json_i64(&v, "/cost/units"),
            source: v
                .pointer("/cost/source")
                .and_then(|x| x.as_str())
                .map(|s| s.to_string()),
            occurred_at,
        });
    }
    Ok(out)
}

fn cost_fact_from_event(event: &EventRow, payload: &Value) -> Result<CostFact, PipelineError> {
    Ok(CostFact {
        model_key: event.model_key,
        currency: payload
            .pointer("/cost/currency")
            .and_then(|x| x.as_str())
            .map(|s| s.to_string()),
        units: json_i64(payload, "/cost/units"),
        source: payload
            .pointer("/cost/source")
            .and_then(|x| x.as_str())
            .map(|s| s.to_string()),
        occurred_at: event.occurred_at,
    })
}

fn select_effective_cost(facts: Vec<CostFact>, consumer: Consumer) -> Option<CostEffect> {
    if facts.is_empty() {
        return None;
    }
    let grain = Grain::from_consumer(consumer).ok()?;
    // Prefer any provider_reported; else sum/select estimated_price_table; else unpriced.
    let reported: Vec<_> = facts
        .iter()
        .filter(|f| f.source.as_deref() == Some("provider_reported") && f.units.is_some() && f.currency.is_some())
        .collect();
    if !reported.is_empty() {
        // One effective amount: take the latest reported (bill replacement).
        let best = reported
            .into_iter()
            .max_by_key(|f| (f.occurred_at, f.units.unwrap_or(0)))?;
        let currency = best.currency.clone()?;
        let units = best.units?;
        return Some(CostEffect {
            model_key: best.model_key,
            currency,
            units,
            source: "provider_reported".into(),
            request_count: 1,
            bucket: bucket_start(grain, best.occurred_at),
        });
    }
    let estimated: Vec<_> = facts
        .iter()
        .filter(|f| {
            f.source.as_deref() == Some("estimated_price_table")
                && f.units.is_some()
                && f.currency.is_some()
        })
        .collect();
    if !estimated.is_empty() {
        // Same currency assumed for scope; sum units, use first currency/model of latest.
        let currency = estimated[0].currency.clone()?;
        let units: i64 = estimated.iter().filter_map(|f| f.units).sum();
        let latest = estimated.iter().max_by_key(|f| f.occurred_at)?;
        return Some(CostEffect {
            model_key: latest.model_key,
            currency,
            units,
            source: "estimated_price_table".into(),
            request_count: estimated.len() as i64,
            bucket: bucket_start(grain, latest.occurred_at),
        });
    }
    // Unpriced requests in scope.
    let latest = facts.iter().max_by_key(|f| f.occurred_at)?;
    Some(CostEffect {
        model_key: latest.model_key,
        currency: latest
            .currency
            .clone()
            .unwrap_or_else(|| "USD".into()),
        units: 0,
        source: "unpriced".into(),
        request_count: facts.len() as i64,
        bucket: bucket_start(grain, latest.occurred_at),
    })
}

// ── upsert / bump helpers ───────────────────────────────────────────────────

fn ensure_harness(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    tx.execute(
        "INSERT INTO harness_metrics (
            created_at, updated_at, grain, bucket_start, harness_id, metric_semantics_version
         ) VALUES (?1, ?1, ?2, ?3, ?4, ?5)
         ON CONFLICT(grain, bucket_start, harness_id) DO NOTHING",
        params![now_ms, grain.as_str(), bucket, harness_id, semantics],
    )?;
    Ok(())
}

fn bump_harness(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    semantics: i64,
    now_ms: i64,
    deltas: &[(&str, i64)],
) -> Result<(), PipelineError> {
    ensure_harness(tx, grain, bucket, harness_id, semantics, now_ms)?;
    for (col, delta) in deltas {
        if *delta == 0 {
            continue;
        }
        // active_duration_ms has CHECK >=0; allow temporary via max(0,...) only for safety on bugs.
        let sql = format!(
            "UPDATE harness_metrics SET {col} = {col} + ?1, updated_at=?2
             WHERE grain=?3 AND bucket_start=?4 AND harness_id=?5 AND delete_at IS NULL"
        );
        tx.execute(
            &sql,
            params![delta, now_ms, grain.as_str(), bucket, harness_id],
        )?;
    }
    Ok(())
}

fn ensure_model_metrics(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    model_key: i64,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    tx.execute(
        "INSERT INTO model_metrics (
            created_at, updated_at, grain, bucket_start, harness_id, model_key, metric_semantics_version
         ) VALUES (?1, ?1, ?2, ?3, ?4, ?5, ?6)
         ON CONFLICT(grain, bucket_start, harness_id, model_key) DO NOTHING",
        params![now_ms, grain.as_str(), bucket, harness_id, model_key, semantics],
    )?;
    Ok(())
}

fn bump_model_metrics(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    model_key: i64,
    now_ms: i64,
    deltas: &[(&str, i64)],
) -> Result<(), PipelineError> {
    for (col, delta) in deltas {
        if *delta == 0 {
            continue;
        }
        let sql = format!(
            "UPDATE model_metrics SET {col} = {col} + ?1, updated_at=?2
             WHERE grain=?3 AND bucket_start=?4 AND harness_id=?5 AND model_key=?6 AND delete_at IS NULL"
        );
        tx.execute(
            &sql,
            params![delta, now_ms, grain.as_str(), bucket, harness_id, model_key],
        )?;
    }
    Ok(())
}

fn ensure_skill_metrics(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    skill_id: i64,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    tx.execute(
        "INSERT INTO skill_metrics (
            created_at, updated_at, grain, bucket_start, harness_id, skill_id, metric_semantics_version
         ) VALUES (?1, ?1, ?2, ?3, ?4, ?5, ?6)
         ON CONFLICT(grain, bucket_start, harness_id, skill_id) DO NOTHING",
        params![now_ms, grain.as_str(), bucket, harness_id, skill_id, semantics],
    )?;
    Ok(())
}

fn bump_skill_metrics(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    skill_id: i64,
    now_ms: i64,
    deltas: &[(&str, i64)],
) -> Result<(), PipelineError> {
    for (col, delta) in deltas {
        if *delta == 0 {
            continue;
        }
        let sql = format!(
            "UPDATE skill_metrics SET {col} = {col} + ?1, updated_at=?2
             WHERE grain=?3 AND bucket_start=?4 AND harness_id=?5 AND skill_id=?6 AND delete_at IS NULL"
        );
        tx.execute(
            &sql,
            params![delta, now_ms, grain.as_str(), bucket, harness_id, skill_id],
        )?;
    }
    Ok(())
}

fn ensure_cost_metrics(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    model_key: i64,
    currency: &str,
    semantics: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    tx.execute(
        "INSERT INTO cost_metrics (
            created_at, updated_at, grain, bucket_start, harness_id, model_key, currency, metric_semantics_version
         ) VALUES (?1, ?1, ?2, ?3, ?4, ?5, ?6, ?7)
         ON CONFLICT(grain, bucket_start, harness_id, model_key, currency) DO NOTHING",
        params![
            now_ms,
            grain.as_str(),
            bucket,
            harness_id,
            model_key,
            currency,
            semantics
        ],
    )?;
    Ok(())
}

fn bump_cost(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    model_key: i64,
    currency: &str,
    now_ms: i64,
    deltas: &[(&str, i64)],
) -> Result<(), PipelineError> {
    for (col, delta) in deltas {
        if *delta == 0 {
            continue;
        }
        let sql = format!(
            "UPDATE cost_metrics SET {col} = {col} + ?1, updated_at=?2
             WHERE grain=?3 AND bucket_start=?4 AND harness_id=?5 AND model_key=?6
               AND currency=?7 AND delete_at IS NULL"
        );
        tx.execute(
            &sql,
            params![
                delta,
                now_ms,
                grain.as_str(),
                bucket,
                harness_id,
                model_key,
                currency
            ],
        )?;
    }
    Ok(())
}

/// Returns true if this is the first insert of the entity in the bucket.
fn upsert_entity(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    kind: &str,
    entity_key: &[u8],
    parent_key: Option<&[u8]>,
    now_ms: i64,
) -> Result<bool, PipelineError> {
    let existing: Option<i64> = tx
        .query_row(
            "SELECT id FROM bucket_entity_state
             WHERE grain=?1 AND bucket_start=?2 AND harness_id=?3
               AND entity_kind=?4 AND entity_key=?5 AND delete_at IS NULL",
            params![grain.as_str(), bucket, harness_id, kind, entity_key],
            |r| r.get(0),
        )
        .optional()?;
    if existing.is_some() {
        return Ok(false);
    }
    tx.execute(
        "INSERT INTO bucket_entity_state (
            created_at, updated_at, grain, bucket_start, harness_id,
            entity_kind, entity_key, parent_key
         ) VALUES (?1, ?1, ?2, ?3, ?4, ?5, ?6, ?7)",
        params![
            now_ms,
            grain.as_str(),
            bucket,
            harness_id,
            kind,
            entity_key,
            parent_key
        ],
    )?;
    Ok(true)
}

/// Returns true if the flag transitioned 0→1.
fn set_entity_flag(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    kind: &str,
    entity_key: &[u8],
    flag: &str,
    now_ms: i64,
) -> Result<bool, PipelineError> {
    // Ensure row exists.
    let _ = upsert_entity(tx, grain, bucket, harness_id, kind, entity_key, None, now_ms)?;
    let sql = format!(
        "UPDATE bucket_entity_state
         SET {flag} = 1, updated_at=?1
         WHERE grain=?2 AND bucket_start=?3 AND harness_id=?4
           AND entity_kind=?5 AND entity_key=?6 AND delete_at IS NULL AND {flag}=0"
    );
    let n = tx.execute(
        &sql,
        params![now_ms, grain.as_str(), bucket, harness_id, kind, entity_key],
    )?;
    Ok(n > 0)
}

fn get_turn_duration(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    turn_key: &[u8],
) -> Result<Option<i64>, PipelineError> {
    Ok(tx
        .query_row(
            "SELECT turn_duration_ms FROM bucket_entity_state
             WHERE grain=?1 AND bucket_start=?2 AND harness_id=?3
               AND entity_kind='turn' AND entity_key=?4 AND delete_at IS NULL",
            params![grain.as_str(), bucket, harness_id, turn_key],
            |r| r.get(0),
        )
        .optional()?
        .flatten())
}

fn set_turn_duration(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    turn_key: &[u8],
    dur: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let _ = upsert_entity(tx, grain, bucket, harness_id, "turn", turn_key, None, now_ms)?;
    tx.execute(
        "UPDATE bucket_entity_state
         SET turn_duration_ms=?1, updated_at=?2
         WHERE grain=?3 AND bucket_start=?4 AND harness_id=?5
           AND entity_kind='turn' AND entity_key=?6 AND delete_at IS NULL",
        params![dur, now_ms, grain.as_str(), bucket, harness_id, turn_key],
    )?;
    Ok(())
}

fn set_session_duration(
    tx: &Transaction<'_>,
    grain: Grain,
    bucket: i64,
    harness_id: &str,
    session_key: &[u8],
    dur: i64,
    now_ms: i64,
) -> Result<(), PipelineError> {
    let _ = upsert_entity(
        tx,
        grain,
        bucket,
        harness_id,
        "session",
        session_key,
        None,
        now_ms,
    )?;
    tx.execute(
        "UPDATE bucket_entity_state
         SET session_duration_ms=?1, updated_at=?2
         WHERE grain=?3 AND bucket_start=?4 AND harness_id=?5
           AND entity_kind='session' AND entity_key=?6 AND delete_at IS NULL",
        params![dur, now_ms, grain.as_str(), bucket, harness_id, session_key],
    )?;
    Ok(())
}
