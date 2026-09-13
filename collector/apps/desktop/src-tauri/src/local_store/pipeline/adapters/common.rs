//! Shared decode helpers for harness strategies.

use std::collections::HashMap;
use std::sync::{Arc, Mutex};

use chrono::{DateTime, Utc};
use serde_json::{json, Map, Value};

use super::identity::{
    event_id, fact_key, session_key as sess_hmac, skill_key, turn_key, TypedNativeKey,
};
use crate::local_store::pipeline::runner::{
    resolve_event_time, DecodeOutcome, DecoderState, FactDraft, IgnoreCode, RawRecord, TimeSource,
    TokenAccuracy,
};

/// In-memory skill registry shared across harness strategies for a device.
/// Maps skill_key → local skill_dimensions.id (allocated by PipelineStore).
#[derive(Clone, Default)]
pub struct SkillBook {
    inner: Arc<Mutex<HashMap<[u8; 32], i64>>>,
}

impl SkillBook {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn upsert(&self, key: [u8; 32], id: i64) {
        self.inner.lock().expect("skill book").insert(key, id);
    }

    pub fn get(&self, key: &[u8; 32]) -> Option<i64> {
        self.inner.lock().expect("skill book").get(key).copied()
    }

    pub fn ensure(&self, key: [u8; 32], allocate: impl FnOnce() -> i64) -> i64 {
        let mut guard = self.inner.lock().expect("skill book");
        if let Some(id) = guard.get(&key).copied() {
            return id;
        }
        let id = allocate();
        guard.insert(key, id);
        id
    }
}

pub fn parse_json_record(record: &RawRecord) -> Result<Value, DecodeOutcome> {
    let text = match std::str::from_utf8(&record.payload) {
        Ok(t) => t.trim(),
        Err(_) => return Err(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord)),
    };
    if text.is_empty() {
        return Err(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
    }
    match serde_json::from_str::<Value>(text) {
        Ok(v) => Ok(v),
        Err(_) => Err(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord)),
    }
}

pub fn json_obj(value: &Value) -> Option<&Map<String, Value>> {
    value.as_object()
}

pub fn str_field(o: &Map<String, Value>, key: &str) -> Option<String> {
    match o.get(key)? {
        Value::String(s) => Some(s.clone()),
        Value::Number(n) => Some(n.to_string()),
        _ => None,
    }
}

pub fn i64_field(o: &Map<String, Value>, key: &str) -> Option<i64> {
    match o.get(key)? {
        Value::Number(n) => n.as_i64(),
        Value::String(s) => s.parse().ok(),
        _ => None,
    }
}

pub fn u64_field(o: &Map<String, Value>, key: &str) -> Option<u64> {
    match o.get(key)? {
        Value::Number(n) => n.as_u64(),
        Value::String(s) => s.parse().ok(),
        _ => None,
    }
}

/// Parse RFC3339 / unix-ms / unix-sec into UTC ms.
pub fn parse_timestamp(value: Option<&Value>) -> Option<Result<i64, ()>> {
    let Some(v) = value else {
        return None;
    };
    if v.is_null() {
        return None;
    }
    if let Some(ms) = v.as_i64() {
        if ms > 1_000_000_000_000 {
            return Some(Ok(ms));
        }
        if ms > 1_000_000_000 {
            return Some(Ok(ms.saturating_mul(1000)));
        }
        if ms == 0 {
            return Some(Err(()));
        }
        return Some(Ok(ms));
    }
    if let Some(s) = v.as_str() {
        if let Ok(ms) = s.parse::<i64>() {
            return parse_timestamp(Some(&Value::from(ms)));
        }
        if let Ok(dt) = DateTime::parse_from_rfc3339(s) {
            return Some(Ok(dt.with_timezone(&Utc).timestamp_millis()));
        }
        return Some(Err(()));
    }
    Some(Err(()))
}

pub fn resolve_record_time(
    o: &Map<String, Value>,
    state: &DecoderState,
    record: &RawRecord,
    time_keys: &[&str],
) -> Result<(i64, TimeSource, bool), IgnoreCode> {
    let mut source = None;
    for key in time_keys {
        if let Some(v) = o.get(*key) {
            source = parse_timestamp(Some(v));
            if source.is_some() {
                break;
            }
        }
    }
    let last = state.json.get("last_source_time").and_then(|v| v.as_i64());
    let resolved = resolve_event_time(source, last, record.file_mtime_ms)?;
    Ok((
        resolved.occurred_at,
        resolved.time_source,
        resolved.is_native_source,
    ))
}

pub fn remember_source_time(state: &mut DecoderState, occurred_at: i64, is_native: bool) {
    if is_native {
        state.json["last_source_time"] = json!(occurred_at);
    }
}

/// Cumulative usage baseline: emit only positive deltas after the first observation.
pub fn cumulative_delta(
    state: &mut DecoderState,
    series_id: &str,
    cumulative: u64,
) -> Result<u64, IgnoreCode> {
    let key = format!("cum::{series_id}");
    let had_prev = state.json.get(&key).and_then(|v| v.as_u64()).is_some();
    let prev = state.json.get(&key).and_then(|v| v.as_u64()).unwrap_or(0);
    state.json[key] = json!(cumulative);
    if !had_prev {
        // First observation of a cumulative series: baseline only.
        return Err(IgnoreCode::Other("cumulative_baseline_only".into()));
    }
    if cumulative < prev {
        // Reset / new series without proof — do not invent a request.
        return Err(IgnoreCode::EstimatedOnly);
    }
    let delta = cumulative - prev;
    if delta == 0 {
        return Err(IgnoreCode::Other("cumulative_unchanged".into()));
    }
    Ok(delta)
}

/// Per-component cumulative observation (input / output / total / cache / reasoning).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CumulativeComponents {
    pub input: u64,
    pub output: u64,
    pub total: u64,
    pub cache: Option<u64>,
    pub reasoning: Option<u64>,
}

/// Differenced request-sized components after a cumulative baseline.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct CumulativeComponentDelta {
    pub input: u64,
    pub output: u64,
    pub total: u64,
    pub cache: Option<u64>,
    pub reasoning: Option<u64>,
}

/// First sight baselines all fields; later sights return per-field positive deltas.
/// A reset on any tracked field yields `EstimatedOnly` (no invented request).
pub fn cumulative_components_delta(
    state: &mut DecoderState,
    series_id: &str,
    observed: CumulativeComponents,
) -> Result<CumulativeComponentDelta, IgnoreCode> {
    let prefix = format!("cum::{series_id}");
    let keys = [
        ("input", Some(observed.input)),
        ("output", Some(observed.output)),
        ("total", Some(observed.total)),
        ("cache", observed.cache),
        ("reasoning", observed.reasoning),
    ];
    let mut had_any_prev = false;
    let mut reset = false;
    let mut deltas = [0u64; 5];
    let mut present = [false; 5];

    for (i, (field, value)) in keys.iter().enumerate() {
        let Some(curr) = value else {
            continue;
        };
        present[i] = true;
        let key = format!("{prefix}::{field}");
        let prev = state.json.get(&key).and_then(|v| v.as_u64());
        if let Some(prev) = prev {
            had_any_prev = true;
            if *curr < prev {
                reset = true;
            } else {
                deltas[i] = *curr - prev;
            }
        }
        state.json[key] = json!(curr);
    }

    // Keep scalar total baseline in sync for OTLP / legacy callers.
    state.json[format!("cum::{series_id}")] = json!(observed.total);

    if !had_any_prev {
        return Err(IgnoreCode::Other("cumulative_baseline_only".into()));
    }
    if reset {
        return Err(IgnoreCode::EstimatedOnly);
    }
    let total_delta = deltas[2];
    if total_delta == 0 && deltas[0] == 0 && deltas[1] == 0 {
        return Err(IgnoreCode::Other("cumulative_unchanged".into()));
    }
    Ok(CumulativeComponentDelta {
        input: deltas[0],
        output: deltas[1],
        total: if total_delta > 0 {
            total_delta
        } else {
            deltas[0].saturating_add(deltas[1])
        },
        cache: if present[3] { Some(deltas[3]) } else { None },
        reasoning: if present[4] { Some(deltas[4]) } else { None },
    })
}

pub struct UsageFactArgs<'a> {
    pub secret: &'a [u8],
    pub harness: &'a str,
    /// Stable logical source identity (file path / DB path), not a stream-kind constant.
    pub scope: &'a str,
    pub native: TypedNativeKey,
    pub fact_kind: &'a str,
    pub occurred_at: i64,
    pub time_source: TimeSource,
    pub token_total: u64,
    pub input_tokens: u64,
    pub output_tokens: u64,
    pub accuracy: TokenAccuracy,
    pub session_id: Option<&'a str>,
    pub turn_id: Option<&'a str>,
    pub skill_id: Option<i64>,
    pub skill_key: Option<[u8; 32]>,
    pub model_key: i64,
    pub cache_read_tokens: Option<u64>,
    pub reasoning_tokens: Option<u64>,
}

pub fn emit_usage_fact(args: UsageFactArgs<'_>) -> FactDraft {
    let fk = fact_key(
        args.secret,
        args.harness,
        args.scope,
        &args.native,
        args.fact_kind,
    );
    let eid = event_id(args.secret, &fk, 1);
    let mut usage = serde_json::Map::new();
    usage.insert("token_total".into(), json!(args.token_total));
    usage.insert("input_context_tokens".into(), json!(args.input_tokens));
    usage.insert("output_tokens".into(), json!(args.output_tokens));
    if let Some(v) = args.cache_read_tokens {
        usage.insert("cache_read_tokens".into(), json!(v));
    }
    if let Some(v) = args.reasoning_tokens {
        usage.insert("reasoning_tokens".into(), json!(v));
    }

    FactDraft {
        event_id: eid,
        fact_key: fk,
        fact_revision: 1,
        event_type: args.fact_kind.into(),
        schema_version: 2,
        metric_semantics_version: 1,
        occurred_at: args.occurred_at,
        time_source: args.time_source,
        model_key: args.model_key,
        model_identity: None,
        skill_id: args.skill_id,
        skill_key: args.skill_key,
        session_key: args
            .session_id
            .map(|s| sess_hmac(args.secret, args.harness, s)),
        turn_key: match (args.session_id, args.turn_id) {
            (Some(s), Some(t)) => Some(turn_key(args.secret, args.harness, s, t)),
            _ => None,
        },
        cost_scope_key: None,
        accuracy: args.accuracy,
        payload_sections: json!({ "usage": usage }),
    }
}

/// Preserve the actual skill label without displaying a machine-specific absolute path.
pub fn skill_display_name(name: &str) -> String {
    let normalized = name.replace('\\', "/");
    if normalized
        .rsplit('/')
        .next()
        .is_some_and(|n| n.eq_ignore_ascii_case("SKILL.md"))
    {
        normalized.rsplit('/').nth(1).unwrap_or(name).to_owned()
    } else {
        name.to_owned()
    }
}

pub fn emit_skill_fact(
    secret: &[u8],
    harness: &str,
    scope: &str,
    native: TypedNativeKey,
    occurred_at: i64,
    time_source: TimeSource,
    skill_name: &str,
    skill_book: &SkillBook,
    allocate_skill: &dyn Fn([u8; 32], &str) -> i64,
    session_id: Option<&str>,
) -> FactDraft {
    let sk = skill_key(secret, skill_name);
    let display_name = skill_display_name(skill_name);
    let skill_id = skill_book.ensure(sk, || allocate_skill(sk, &display_name));
    let fk = fact_key(secret, harness, scope, &native, "skill_invoked");
    let eid = event_id(secret, &fk, 1);
    FactDraft {
        event_id: eid,
        fact_key: fk,
        fact_revision: 1,
        event_type: "skill_invoked".into(),
        schema_version: 2,
        metric_semantics_version: 1,
        occurred_at,
        time_source,
        model_key: 0,
        model_identity: None,
        skill_id: Some(skill_id),
        skill_key: Some(sk),
        session_key: session_id.map(|s| sess_hmac(secret, harness, s)),
        turn_key: None,
        cost_scope_key: None,
        accuracy: TokenAccuracy::Exact,
        payload_sections: json!({
            "activity": {}
        }),
    }
}

pub fn emit_code_fact(
    secret: &[u8],
    harness: &str,
    scope: &str,
    native: TypedNativeKey,
    occurred_at: i64,
    time_source: TimeSource,
    session_id: Option<&str>,
    added: u64,
    removed: u64,
) -> FactDraft {
    let fk = fact_key(secret, harness, scope, &native, "code_changed");
    let eid = event_id(secret, &fk, 1);
    FactDraft {
        event_id: eid,
        fact_key: fk,
        fact_revision: 1,
        event_type: "code_changed".into(),
        schema_version: 2,
        metric_semantics_version: 1,
        occurred_at,
        time_source,
        model_key: 0,
        model_identity: None,
        skill_id: None,
        skill_key: None,
        session_key: session_id.map(|s| sess_hmac(secret, harness, s)),
        turn_key: None,
        cost_scope_key: None,
        accuracy: TokenAccuracy::Derived,
        payload_sections: json!({
            "code": {
                "added": added,
                "removed": removed
            }
        }),
    }
}

/// Lifecycle evidence has stable native identity, separate from token facts.
pub fn emit_activity_fact(
    secret: &[u8],
    harness: &str,
    scope: &str,
    native: TypedNativeKey,
    kind: &str,
    occurred_at: i64,
    time_source: TimeSource,
    session: &str,
    turn: Option<&str>,
    activity: Value,
) -> FactDraft {
    let fk = fact_key(secret, harness, scope, &native, kind);
    FactDraft {
        event_id: event_id(secret, &fk, 1),
        fact_key: fk,
        fact_revision: 1,
        event_type: kind.into(),
        schema_version: 2,
        metric_semantics_version: 1,
        occurred_at,
        time_source,
        model_key: 0,
        model_identity: None,
        skill_id: None,
        skill_key: None,
        session_key: Some(sess_hmac(secret, harness, session)),
        turn_key: turn.map(|t| turn_key(secret, harness, session, t)),
        cost_scope_key: None,
        accuracy: TokenAccuracy::Derived,
        payload_sections: json!({"activity": activity}),
    }
}

/// Only successful edit/write inputs are evidence of generated code, never arbitrary tools.
pub fn completed_code_payload(part: &Value) -> Option<Value> {
    let state = part.get("state")?;
    if state.get("status")?.as_str()? != "completed" {
        return None;
    }
    let input = state.get("input")?;
    let tool = part.get("tool")?.as_str()?.to_ascii_lowercase();
    let lines = |s: &str| s.lines().count() as u64;
    match tool.as_str() {
        "edit" => {
            // Without a match count, replace-all has no reliable line delta.
            if input
                .get("replace_all")
                .or_else(|| input.get("replaceAll"))
                .and_then(Value::as_bool)
                == Some(true)
            {
                return None;
            }
            let old = input
                .get("old_string")
                .or_else(|| input.get("oldString"))
                .or_else(|| input.get("oldText"))?
                .as_str()?;
            let new = input
                .get("new_string")
                .or_else(|| input.get("newString"))
                .or_else(|| input.get("newText"))?
                .as_str()?;
            if old == new {
                return None;
            }
            Some(
                json!({"generated": lines(new), "added": lines(new), "removed": lines(old), "file_touch_count": 1}),
            )
        }
        "write" => {
            let text = input.get("content")?.as_str()?;
            // Generated content is known; overwritten old contents may not be available.
            Some(json!({"generated": lines(text), "file_touch_count": 1}))
        }
        _ => None,
    }
}

/// Count only explicit patch additions/deletions; context and headers are not generated lines.
pub fn patch_code_payload(patch: &str) -> Option<Value> {
    if !patch.starts_with("*** Begin Patch") || !patch.contains("*** End Patch") {
        return None;
    }
    let (mut added, mut removed, mut files) = (0u64, 0u64, 0u64);
    for line in patch.lines() {
        if line.starts_with("*** Add File:") || line.starts_with("*** Update File:") {
            files += 1;
        } else if line.starts_with("*** Delete File:") {
            return None;
        }
        // No deleted contents in this format.
        else if line.starts_with('+') {
            added += 1;
        } else if line.starts_with('-') {
            removed += 1;
        }
    }
    (files > 0).then(
        || json!({"generated":added,"added":added,"removed":removed,"file_touch_count":files}),
    )
}

#[cfg(test)]
mod name_tests {
    use super::*;
    #[test]
    fn display_name_keeps_identity_and_omits_machine_path() {
        assert_eq!(skill_display_name("review"), "review");
        assert_eq!(
            skill_display_name("C:/example/skills/code-review/SKILL.md"),
            "code-review"
        );
        assert_eq!(
            skill_display_name(r"C:\example\skills\code-review\SKILL.md"),
            "code-review"
        );
    }
}
