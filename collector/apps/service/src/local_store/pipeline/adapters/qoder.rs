//! Qoder (Alibaba AIDC) CLI / desktop transcript harness.
//!
//! Sources are `~/.qoder/projects/<slug>/<session-id>.jsonl` (CN variant
//! `~/.qoder-cn/`, env override `QODER_CONFIG_DIR`), append-only, one JSON
//! line per content block with a Claude-Code-shaped envelope
//! (`type`/`uuid`/`sessionId`/`timestamp`). Usage lands once per LLM response
//! on the final assistant line (`message.stop_reason == "end_turn"`) inside
//! `message.usage`:
//!
//! - token fields are session-cumulative and written as zeros unless Qoder
//!   runs with `QODER_EXPOSE_TOKEN_USAGE` set. They are high-water-mark
//!   differenced per request; `input_tokens` includes
//!   `cache_read_input_tokens` (verified: cr ⊂ in and
//!   `context_usage_ratio == in/1e6`), so
//!   `input_context = Δin + Δcw` and `total = Δin + Δcw + Δout`;
//! - `credits` is a per-request value (non-monotonic across records) and
//!   becomes a provider-reported `cost_recorded` fact in `QODER_CREDIT`
//!   (units ×1e-8). It is the only consumption signal without the env switch,
//!   so zero-token records still emit cost.
//!
//! Only metric fields are decoded; prompt text, thinking, tool inputs and
//! tool outputs never enter a FactDraft. Subagent transcripts
//! (`<session>/subagents/*.jsonl`) are separate sources and count into the
//! same harness through their own sessionId.

use std::path::PathBuf;

use serde_json::{json, Value};

use super::common::{
    emit_activity_fact, emit_usage_fact, json_obj, parse_json_record, remember_source_time,
    resolve_record_time, str_field, UsageFactArgs,
};
use super::identity::{event_id, fact_key, session_key, source_key, turn_key, TypedNativeKey};
use super::jsonl_io::{discover_jsonl_files_in_roots, read_jsonl_source};
use crate::local_store::pipeline::runner::{
    CheckpointView, DecodeOutcome, DecoderState, DiscoveryBudget, FactDraft, HarnessStrategy,
    IgnoreCode, NativeFactKey, RawBatch, RawRecord, ReadBudget, RunnerError, SourceSpec,
    TimeSource, TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "qoder";
pub const STREAM_KEY: &str = "projects-jsonl";
/// Credits currency: provider-reported Qoder subscription credits.
pub const CREDITS_CURRENCY: &str = "QODER_CREDIT";
/// Cost units scale: 1 credit = 100_000_000 units (matches the 1e-8 USD scale).
const CREDIT_UNIT_SCALE: f64 = 100_000_000.0;
/// Bound tracked cumulative baselines; beyond it token detail is skipped.
const HWM_SESSION_CAP: usize = 16;

pub struct QoderStrategy {
    pub identity_secret: Vec<u8>,
    /// Intl + CN project roots (`~/.qoder/projects`, `~/.qoder-cn/projects`).
    pub roots: Vec<PathBuf>,
    model_allocator: Option<crate::local_store::pipeline::runner::ModelAllocator>,
}

impl QoderStrategy {
    pub fn new(identity_secret: impl Into<Vec<u8>>, roots: Vec<PathBuf>) -> Self {
        Self {
            identity_secret: identity_secret.into(),
            roots,
            model_allocator: None,
        }
    }

    fn allocate_model(&self, model: &str) -> Result<i64, RunnerError> {
        match &self.model_allocator {
            Some(allocate) => allocate(HARNESS_ID, model),
            None => Ok(0),
        }
    }

    /// Per-session high-water mark over [input(includes cr), cr, cw, output].
    ///
    /// All-zero observations (env switch off) carry no information and do not
    /// touch the baseline. The first non-zero observation only establishes a
    /// baseline — prior consumption is unknowable, no request is invented. A
    /// counter decrease resets the baseline (session generation change).
    fn cumulative_delta(
        state: &mut DecoderState,
        session: &str,
        current: [u64; 4],
    ) -> Option<[u64; 4]> {
        if current == [0, 0, 0, 0] {
            return None;
        }
        let baselines = match state
            .json
            .get_mut("qoder_hwm")
            .and_then(Value::as_object_mut)
        {
            Some(map) => map,
            None => {
                state.json["qoder_hwm"] = json!({});
                state.json["qoder_hwm"]
                    .as_object_mut()
                    .expect("just inserted")
            }
        };
        let previous: Option<[u64; 4]> =
            baselines.get(session).and_then(|v| v.as_array()).map(|a| {
                [
                    a.first().and_then(Value::as_u64).unwrap_or(0),
                    a.get(1).and_then(Value::as_u64).unwrap_or(0),
                    a.get(2).and_then(Value::as_u64).unwrap_or(0),
                    a.get(3).and_then(Value::as_u64).unwrap_or(0),
                ]
            });
        match previous {
            None => {
                if baselines.len() >= HWM_SESSION_CAP {
                    return None;
                }
                baselines.insert(session.into(), json!(current));
                None
            }
            Some(prev) => {
                if current.iter().zip(prev.iter()).any(|(c, p)| c < p) {
                    baselines.insert(session.into(), json!(current));
                    return None;
                }
                let delta = [
                    current[0] - prev[0],
                    current[1] - prev[1],
                    current[2] - prev[2],
                    current[3] - prev[3],
                ];
                baselines.insert(session.into(), json!(current));
                (delta != [0, 0, 0, 0]).then_some(delta)
            }
        }
    }

    fn cost_fact(
        &self,
        logical_scope: &str,
        native: TypedNativeKey,
        session: &str,
        turn: Option<&str>,
        occurred_at: i64,
        time_source: TimeSource,
        model_key: i64,
        model_identity: &Option<(String, String)>,
        credits: f64,
    ) -> Option<FactDraft> {
        if !credits.is_finite() || credits <= 0.0 || credits >= 1.0e6 {
            return None;
        }
        // Bounded by the guard above (≤1e14), so the float cast cannot overflow.
        let units = (credits * CREDIT_UNIT_SCALE).round() as i64;
        let fk = fact_key(
            &self.identity_secret,
            HARNESS_ID,
            logical_scope,
            &native,
            "cost_recorded",
        );
        Some(FactDraft {
            usage_identity: None,
            event_id: event_id(&self.identity_secret, &fk, 1),
            fact_key: fk,
            fact_revision: 1,
            event_type: "cost_recorded".into(),
            schema_version: 2,
            metric_semantics_version: 1,
            occurred_at,
            time_source,
            model_key,
            model_identity: model_identity.clone(),
            skill_id: None,
            skill_key: None,
            session_key: Some(session_key(&self.identity_secret, HARNESS_ID, session)),
            turn_key: turn.map(|t| turn_key(&self.identity_secret, HARNESS_ID, session, t)),
            // Own fact key scopes the delta: one cost fact per request applies once.
            cost_scope_key: Some(fk),
            accuracy: TokenAccuracy::Exact,
            payload_sections: json!({
                "cost": {
                    "units": units,
                    "currency": CREDITS_CURRENCY,
                    "source": "provider_reported"
                }
            }),
        })
    }
}

impl HarnessStrategy for QoderStrategy {
    fn set_model_allocator(
        &mut self,
        allocator: crate::local_store::pipeline::runner::ModelAllocator,
    ) {
        self.model_allocator = Some(allocator);
    }
    fn harness_id(&self) -> &str {
        HARNESS_ID
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        if self.roots.is_empty() {
            return Ok(vec![]);
        }
        let (files, _) = discover_jsonl_files_in_roots(
            self.roots.iter().map(|p| p.as_path()),
            ".jsonl",
            budget.max_sources,
            budget.resume_after.as_deref(),
        );
        Ok(files
            .into_iter()
            .map(|path| {
                let scope = path.to_string_lossy().into_owned();
                SourceSpec {
                    harness_id: HARNESS_ID.into(),
                    source_key: source_key(&self.identity_secret, HARNESS_ID, &scope),
                    source_kind: SourceKind::Jsonl,
                    locator_ref: scope,
                    stream_key: STREAM_KEY.into(),
                    cursor_kind: CursorKind::ByteOffset,
                    initial_cursor_json: json!({ "offset": 0 }),
                    initial_decoder_state_json: json!({ "last_source_time": null }),
                    observed_boundary_json: json!({ "len": 0 }),
                }
            })
            .collect())
    }

    fn read(
        &self,
        locator_ref: &str,
        _stream_key: &str,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        read_jsonl_source(std::path::Path::new(locator_ref), committed, budget)
    }

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        logical_scope: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        let value = match parse_json_record(record) {
            Ok(v) => v,
            Err(outcome) => return Ok(outcome),
        };
        let Some(o) = json_obj(&value) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let kind = str_field(o, "type").unwrap_or_default();
        if kind != "user" && kind != "assistant" {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::UnsupportedStructure));
        }
        let (occurred_at, time_source, is_native) =
            match resolve_record_time(o, state, record, &["timestamp"]) {
                Ok(t) => t,
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);
        let session = str_field(o, "sessionId")
            .filter(|s| !s.is_empty())
            .or_else(|| {
                state
                    .json
                    .get("qoder_session")
                    .and_then(Value::as_str)
                    .map(str::to_owned)
            })
            .unwrap_or_else(|| logical_scope.to_string());
        state.json["qoder_session"] = json!(session);

        let mut facts: Vec<FactDraft> = Vec::new();
        if state.json["qoder_session_emitted"] != true {
            facts.push(emit_activity_fact(
                &self.identity_secret,
                HARNESS_ID,
                logical_scope,
                TypedNativeKey::Str(format!("session:{session}")),
                "session_started",
                occurred_at,
                time_source,
                &session,
                None,
                json!({}),
            ));
            state.json["qoder_session_emitted"] = json!(true);
        }

        let uuid = str_field(o, "uuid").filter(|s| !s.is_empty());
        if kind == "user" {
            let human = value.pointer("/origin/kind").and_then(Value::as_str) == Some("human");
            let has_tool_result = value
                .pointer("/message/content")
                .and_then(Value::as_array)
                .is_some_and(|parts| parts.iter().any(|p| p["type"] == "tool_result"));
            if human && !has_tool_result {
                let turn = str_field(o, "promptId")
                    .filter(|s| !s.is_empty())
                    .or_else(|| uuid.clone())
                    .unwrap_or_else(|| {
                        format!("offset:{}", record.byte_start.unwrap_or(record.ordinal))
                    });
                state.json["qoder_turn"] = json!(turn);
                facts.push(emit_activity_fact(
                    &self.identity_secret,
                    HARNESS_ID,
                    logical_scope,
                    TypedNativeKey::Str(turn.clone()),
                    "turn_started",
                    occurred_at,
                    time_source,
                    &session,
                    Some(&turn),
                    json!({"trigger":"user"}),
                ));
            }
            return Ok(if facts.is_empty() {
                DecodeOutcome::ContextOnly
            } else {
                DecodeOutcome::Emit(facts)
            });
        }

        // Assistant: usage (once per response) on the stop_reason == end_turn line.
        let message = o.get("message").and_then(Value::as_object);
        let usage = message
            .and_then(|m| m.get("usage"))
            .and_then(Value::as_object);
        let stop_reason = message
            .and_then(|m| str_field(m, "stop_reason"))
            .unwrap_or_default();
        if let Some(usage) = usage {
            let request = str_field(usage, "request_id")
                .or_else(|| {
                    value
                        .pointer("/requestTokenAnchor/requestId")
                        .and_then(Value::as_str)
                        .map(str::to_owned)
                })
                .filter(|s| !s.is_empty());
            let native = match (&request, &uuid) {
                (Some(r), _) => TypedNativeKey::Str(format!("{session}/{r}")),
                (None, Some(u)) => TypedNativeKey::Str(format!("{session}/{u}")),
                (None, None) => {
                    TypedNativeKey::ByteOffset(record.byte_start.unwrap_or(record.ordinal))
                }
            };
            let turn = request.clone().or_else(|| uuid.clone());
            let model = message
                .and_then(|m| str_field(m, "model"))
                .filter(|s| !s.is_empty());
            let model_identity = model
                .as_deref()
                .map(|m| (HARNESS_ID.to_string(), m.to_string()));
            let model_key = match &model {
                Some(model) => self.allocate_model(model)?,
                None => 0,
            };

            let counters = [
                u64_field(usage, "input_tokens"),
                u64_field(usage, "cache_read_input_tokens"),
                u64_field(usage, "cache_creation_input_tokens"),
                u64_field(usage, "output_tokens"),
            ];
            if let Some(delta) = Self::cumulative_delta(state, &session, counters) {
                let (d_in, d_read, d_write, d_out) = (delta[0], delta[1], delta[2], delta[3]);
                let input_context = d_in.saturating_add(d_write);
                let total = input_context.saturating_add(d_out);
                let mut fact = emit_usage_fact(UsageFactArgs {
                    secret: &self.identity_secret,
                    harness: HARNESS_ID,
                    scope: logical_scope,
                    native: native.clone(),
                    fact_kind: "model_usage_recorded",
                    occurred_at,
                    time_source,
                    token_total: total,
                    input_tokens: input_context,
                    output_tokens: d_out,
                    accuracy: TokenAccuracy::Exact,
                    session_id: Some(&session),
                    turn_id: turn.as_deref(),
                    skill_id: None,
                    skill_key: None,
                    model_key,
                    cache_read_tokens: Some(d_read),
                    reasoning_tokens: None,
                });
                fact.payload_sections["usage"]["cache_write_tokens"] = json!(d_write);
                fact.model_identity = model_identity.clone();
                facts.push(fact);
            }

            if let Some(credits) = usage.get("credits").and_then(Value::as_f64) {
                if let Some(fact) = self.cost_fact(
                    logical_scope,
                    native,
                    &session,
                    turn.as_deref(),
                    occurred_at,
                    time_source,
                    model_key,
                    &model_identity,
                    credits,
                ) {
                    facts.push(fact);
                }
            }
        }

        if stop_reason == "end_turn" {
            let turn = state
                .json
                .get("qoder_turn")
                .and_then(Value::as_str)
                .map(str::to_owned)
                .or_else(|| uuid.clone())
                .unwrap_or_else(|| {
                    format!("offset:{}", record.byte_start.unwrap_or(record.ordinal))
                });
            facts.push(emit_activity_fact(
                &self.identity_secret,
                HARNESS_ID,
                logical_scope,
                TypedNativeKey::Str(turn.clone()),
                "turn_completed",
                occurred_at,
                time_source,
                &session,
                Some(&turn),
                json!({"success":true}),
            ));
            state.json["qoder_turn"] = Value::Null;
        }

        Ok(if facts.is_empty() {
            DecodeOutcome::ContextOnly
        } else {
            DecodeOutcome::Emit(facts)
        })
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}

fn u64_field(object: &serde_json::Map<String, Value>, key: &str) -> u64 {
    object
        .get(key)
        .and_then(Value::as_u64)
        .or_else(|| str_field(object, key).and_then(|s| s.parse().ok()))
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn strategy() -> QoderStrategy {
        QoderStrategy::new(vec![7; 32], Vec::new())
    }

    fn decode_line(
        strategy: &QoderStrategy,
        line: &str,
        state: &mut DecoderState,
    ) -> Vec<FactDraft> {
        let record = RawRecord {
            ordinal: 0,
            byte_start: Some(0),
            byte_end: Some(line.len() as u64),
            native_rowid: None,
            payload: line.as_bytes().to_vec(),
            file_mtime_ms: Some(1_782_000_000_000),
        };
        match strategy
            .decode(&record, state, "fixture/qoder-session")
            .unwrap()
        {
            DecodeOutcome::Emit(facts) => facts,
            _ => Vec::new(),
        }
    }

    fn transcript_facts() -> (Vec<FactDraft>, DecoderState) {
        let strategy = strategy();
        let mut state = DecoderState {
            version: 1,
            json: json!({}),
        };
        let mut facts = Vec::new();
        for line in adapter_qoder::KNOWN_LOG.lines() {
            facts.extend(decode_line(&strategy, line, &mut state));
        }
        (facts, state)
    }

    #[test]
    fn transcript_yields_usage_cost_session_and_turns() {
        let (facts, state) = transcript_facts();
        let kinds: Vec<&str> = facts.iter().map(|f| f.event_type.as_str()).collect();
        assert!(kinds.contains(&"session_started"));
        assert_eq!(
            kinds.iter().filter(|k| **k == "turn_started").count(),
            2,
            "prompt-a1 and prompt-a2 are human turns"
        );
        assert_eq!(
            kinds.iter().filter(|k| **k == "turn_completed").count(),
            4,
            "end_turn lines complete turns (req-a1..a4)"
        );
        let usage: Vec<_> = facts
            .iter()
            .filter(|f| f.event_type == "model_usage_recorded")
            .collect();
        assert_eq!(usage.len(), 1, "first exposed observation is baseline only");
        assert_eq!(
            usage[0].payload_sections["usage"]["token_total"], 2862,
            "Δin 2785 + Δcw 20 + Δout 57"
        );
        assert_eq!(
            usage[0].payload_sections["usage"]["input_context_tokens"],
            2805
        );
        assert_eq!(
            usage[0].payload_sections["usage"]["cache_read_tokens"],
            2272
        );
        assert_eq!(usage[0].payload_sections["usage"]["cache_write_tokens"], 20);
        assert_eq!(usage[0].payload_sections["usage"]["output_tokens"], 57);
        assert_eq!(
            usage[0].model_identity,
            Some(("qoder".into(), "dfmodel".into()))
        );

        let costs: Vec<_> = facts
            .iter()
            .filter(|f| f.event_type == "cost_recorded")
            .collect();
        assert_eq!(costs.len(), 3, "credits for req-a1/a2/a3; req-a4 is zero");
        assert_eq!(costs[0].payload_sections["cost"]["units"], 50_000_000);
        assert_eq!(
            costs[0].payload_sections["cost"]["currency"],
            CREDITS_CURRENCY
        );
        assert_eq!(
            costs[0].payload_sections["cost"]["source"],
            "provider_reported"
        );
        assert_eq!(costs[0].cost_scope_key.is_some(), true);
        assert_eq!(costs[1].payload_sections["cost"]["units"], 2_260_000);
        assert!(costs.iter().all(|f| f.accuracy == TokenAccuracy::Exact));
        // Session id never appears in payloads.
        let payloads: Vec<&Value> = facts.iter().map(|f| &f.payload_sections).collect();
        let serialized = serde_json::to_string(&payloads).unwrap();
        assert!(!serialized.contains("qoder-sess-alpha-0001"));
        assert!(!serialized.contains("QODER_PROMPT_CANARY"));
        assert!(!state.json["qoder_session_emitted"].is_null());
    }

    #[test]
    fn decoder_state_survives_roundtrip_across_batches() {
        let strategy = strategy();
        let mut state = DecoderState {
            version: 1,
            json: json!({}),
        };
        // Batch 1: baseline record.
        let baseline = adapter_qoder::KNOWN_LOG
            .lines()
            .find(|l| l.contains("req-a2") && l.contains("end_turn"))
            .unwrap();
        let facts = decode_line(&strategy, baseline, &mut state);
        assert_eq!(
            facts
                .iter()
                .filter(|f| f.event_type == "model_usage_recorded")
                .count(),
            0
        );
        // Persist + restore decoder state the way the runner does.
        let mut restored = DecoderState {
            version: state.version,
            json: serde_json::from_str(&state.json.to_string()).unwrap(),
        };
        let delta_line = adapter_qoder::KNOWN_LOG
            .lines()
            .find(|l| l.contains("req-a3") && l.contains("end_turn"))
            .unwrap();
        let facts = decode_line(&strategy, delta_line, &mut restored);
        assert_eq!(
            facts
                .iter()
                .filter(|f| f.event_type == "model_usage_recorded")
                .count(),
            1,
            "HWM baseline persists across batches"
        );
    }

    #[test]
    fn counter_decrease_resets_baseline_without_inventing_usage() {
        let strategy = strategy();
        let mut state = DecoderState {
            version: 1,
            json: json!({}),
        };
        let line = |request: &str, in_tok: u64| {
            format!(
                r#"{{"type":"assistant","uuid":"u-{request}","timestamp":"2026-10-24T10:00:00Z","sessionId":"s1","message":{{"id":"m","role":"assistant","model":"kmodel_latest","stop_reason":"end_turn","content":[],"usage":{{"input_tokens":{in_tok},"cache_read_input_tokens":0,"cache_creation_input_tokens":0,"output_tokens":1,"credits":0.1,"request_id":"{request}"}}}}}}"#
            )
        };
        decode_line(&strategy, &line("r1", 5000), &mut state);
        // New session generation: counters dropped below the baseline.
        let facts = decode_line(&strategy, &line("r2", 100), &mut state);
        assert_eq!(
            facts
                .iter()
                .filter(|f| f.event_type == "model_usage_recorded")
                .count(),
            0
        );
        // Post-reset diff is trusted against the new baseline.
        let facts = decode_line(&strategy, &line("r3", 300), &mut state);
        assert_eq!(
            facts
                .iter()
                .filter(|f| f.event_type == "model_usage_recorded")
                .count(),
            1
        );
    }

    #[test]
    fn unknown_record_types_are_ignored() {
        let strategy = strategy();
        let mut state = DecoderState {
            version: 1,
            json: json!({}),
        };
        let facts = decode_line(
            &strategy,
            r#"{"type":"file-history-snapshot","uuid":"x","timestamp":"2026-10-24T10:00:00Z","sessionId":"s1"}"#,
            &mut state,
        );
        assert!(facts.is_empty());
        let malformed = decode_line(&strategy, "{not-json", &mut state);
        assert!(malformed.is_empty());
    }
}
