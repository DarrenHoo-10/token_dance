//! Codex harness strategy: session JSONL is the sole usage authority.
//! Native `event_msg` / `token_count` uses last_token_usage (request) and
//! total_token_usage (cumulative baseline / delta). OTLP cumulative without
//! differential evidence is Ignore/baseline-only.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::{json, Map, Value};

use super::common::{
    cumulative_delta, emit_skill_fact, emit_usage_fact, json_obj, parse_json_record,
    remember_source_time, resolve_record_time, str_field, u64_field, SkillBook, UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use super::jsonl_io::{discover_jsonl_files, read_jsonl_source};
use crate::local_store::pipeline::runner::{
    CheckpointView, DecodeOutcome, DecoderState, DiscoveryBudget, FactDraft, HarnessStrategy,
    IgnoreCode, NativeFactKey, RawBatch, RawRecord, ReadBudget, RunnerError, SourceSpec,
    TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "codex";
pub const STREAM_SESSIONS: &str = "sessions-jsonl";
pub const STREAM_OTLP: &str = "otlp";

pub struct CodexStrategy {
    pub identity_secret: Vec<u8>,
    pub sessions_root: PathBuf,
    pub skill_book: SkillBook,
    pub skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
}

impl CodexStrategy {
    pub fn new(
        identity_secret: impl Into<Vec<u8>>,
        sessions_root: impl Into<PathBuf>,
        skill_book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            identity_secret: identity_secret.into(),
            sessions_root: sessions_root.into(),
            skill_book,
            skill_allocator,
        }
    }
}

fn read_usage_counts(usage: &Map<String, Value>) -> (u64, u64, u64, Option<u64>, Option<u64>) {
    let input = u64_field(usage, "input_tokens")
        .or_else(|| u64_field(usage, "inputTokens"))
        .unwrap_or(0);
    let output = u64_field(usage, "output_tokens")
        .or_else(|| u64_field(usage, "outputTokens"))
        .unwrap_or(0);
    let total = u64_field(usage, "total_tokens")
        .or_else(|| u64_field(usage, "totalTokens"))
        .unwrap_or(input.saturating_add(output));
    let cache = u64_field(usage, "cached_input_tokens")
        .or_else(|| u64_field(usage, "cache_read_tokens"));
    let reasoning = u64_field(usage, "reasoning_output_tokens")
        .or_else(|| u64_field(usage, "reasoning_tokens"));
    (input, output, total, cache, reasoning)
}

impl HarnessStrategy for CodexStrategy {
    fn harness_id(&self) -> &str {
        HARNESS_ID
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        let files = discover_jsonl_files(&self.sessions_root, ".jsonl", budget.max_sources);
        let mut specs = Vec::new();
        for path in files {
            let scope = path.to_string_lossy().to_string();
            specs.push(SourceSpec {
                harness_id: HARNESS_ID.into(),
                source_key: source_key(&self.identity_secret, HARNESS_ID, &scope),
                source_kind: SourceKind::Jsonl,
                locator_ref: path.to_string_lossy().into_owned(),
                stream_key: STREAM_SESSIONS.into(),
                cursor_kind: CursorKind::ByteOffset,
                initial_cursor_json: json!({ "offset": 0 }),
                initial_decoder_state_json: json!({ "last_source_time": null }),
                observed_boundary_json: json!({ "len": 0 }),
            });
        }
        Ok(specs)
    }

    fn read(
        &self,
        locator_ref: &str,
        stream_key: &str,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        if stream_key == STREAM_OTLP {
            return Ok(RawBatch {
                records: vec![],
                next_cursor_json: committed.cursor_json.clone(),
                next_observed_boundary_json: committed.observed_boundary_json.clone(),
                has_more: false,
                bytes_read: 0,
                ignored_incomplete_tail: false,
            });
        }
        read_jsonl_source(Path::new(locator_ref), committed, budget)
    }

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        logical_scope: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        let value = match parse_json_record(record) {
            Ok(v) => v,
            Err(o) => return Ok(o),
        };
        let Some(o) = json_obj(&value) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };

        // Secondary cumulative channel: baseline only, never a request fact.
        if o.get("otlp_cumulative").and_then(|v| v.as_bool()) == Some(true) {
            let series = str_field(o, "series_id").unwrap_or_else(|| "default".into());
            let total = u64_field(o, "cumulative_tokens").unwrap_or(0);
            return match cumulative_delta(state, &series, total) {
                Ok(_) => Ok(DecodeOutcome::Ignore(IgnoreCode::EstimatedOnly)),
                Err(code) => Ok(DecodeOutcome::Ignore(code)),
            };
        }

        let kind = str_field(o, "type").unwrap_or_default();
        let (occurred_at, time_source, is_native) =
            match resolve_record_time(o, state, record, &["timestamp", "ts", "time"]) {
                Ok(t) => t,
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);

        let session = str_field(o, "thread_id").or_else(|| str_field(o, "session_id"));
        let turn = str_field(o, "turn_id");
        let byte_native = TypedNativeKey::ByteOffset(record.byte_start.unwrap_or(record.ordinal));

        match kind.as_str() {
            "event_msg" => {
                let payload = o.get("payload").and_then(|p| p.as_object());
                let Some(payload) = payload else {
                    return Ok(DecodeOutcome::ContextOnly);
                };
                let payload_type = str_field(payload, "type").unwrap_or_default();
                match payload_type.as_str() {
                    "token_count" => {
                        self.decode_token_count(
                            payload,
                            state,
                            logical_scope,
                            occurred_at,
                            time_source,
                            session.as_deref(),
                            turn.as_deref(),
                            byte_native,
                        )
                    }
                    "task_started" | "task_complete" | "agent_message" => {
                        Ok(DecodeOutcome::ContextOnly)
                    }
                    _ => Ok(DecodeOutcome::ContextOnly),
                }
            }
            "token.usage" | "model_usage" => {
                let usage_obj = o
                    .get("payload")
                    .and_then(|p| p.get("info"))
                    .and_then(|i| i.as_object())
                    .unwrap_or(o);
                let (input, output, total, cache, reasoning) = read_usage_counts(usage_obj);
                if total == 0 && input == 0 && output == 0 {
                    return Ok(DecodeOutcome::ContextOnly);
                }
                let native = turn
                    .as_ref()
                    .map(|t| TypedNativeKey::Str(format!("turn:{t}")))
                    .unwrap_or(byte_native);
                Ok(DecodeOutcome::Emit(vec![emit_usage_fact(UsageFactArgs {
                    secret: &self.identity_secret,
                    harness: HARNESS_ID,
                    scope: logical_scope,
                    native,
                    fact_kind: "model_usage_recorded",
                    occurred_at,
                    time_source,
                    token_total: total,
                    input_tokens: input,
                    output_tokens: output,
                    accuracy: TokenAccuracy::Exact,
                    session_id: session.as_deref(),
                    turn_id: turn.as_deref(),
                    skill_id: None,
                    skill_key: None,
                    model_key: 0,
                    cache_read_tokens: cache,
                    reasoning_tokens: reasoning,
                })]))
            }
            "skill.execution.failed" | "skill.injected" | "skill_invoked" => {
                let name = str_field(o, "skill_name")
                    .or_else(|| str_field(o, "skill"))
                    .unwrap_or_else(|| "unknown-skill".into());
                let native = TypedNativeKey::Str(format!(
                    "skill:{}:{}",
                    session.as_deref().unwrap_or(""),
                    name
                ));
                let alloc = self.skill_allocator.clone();
                Ok(DecodeOutcome::Emit(vec![emit_skill_fact(
                    &self.identity_secret,
                    HARNESS_ID,
                    logical_scope,
                    native,
                    occurred_at,
                    time_source,
                    &name,
                    &self.skill_book,
                    &*alloc,
                    session.as_deref(),
                )]))
            }
            "thread.started" | "turn.started" | "session_meta" | "turn_context" => {
                Ok(DecodeOutcome::ContextOnly)
            }
            "tool.completed" | "response_item" => Ok(DecodeOutcome::ContextOnly),
            _ => {
                if o.contains_key("type") {
                    Ok(DecodeOutcome::Ignore(IgnoreCode::UnsupportedStructure))
                } else {
                    Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord))
                }
            }
        }
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}

impl CodexStrategy {
    fn decode_token_count(
        &self,
        payload: &Map<String, Value>,
        state: &mut DecoderState,
        logical_scope: &str,
        occurred_at: i64,
        time_source: crate::local_store::pipeline::runner::TimeSource,
        session: Option<&str>,
        turn: Option<&str>,
        byte_native: TypedNativeKey,
    ) -> Result<DecodeOutcome, RunnerError> {
        let info = payload.get("info").and_then(|i| i.as_object());
        let Some(info) = info else {
            return Ok(DecodeOutcome::ContextOnly);
        };

        // Always advance cumulative baseline from total_token_usage when present.
        let mut cumulative_emit: Option<(u64, u64, u64, Option<u64>, Option<u64>)> = None;
        if let Some(total_obj) = info.get("total_token_usage").and_then(|v| v.as_object()) {
            let (input, output, total, cache, reasoning) = read_usage_counts(total_obj);
            let series = format!("total::{}", session.unwrap_or("default"));
            match cumulative_delta(state, &series, total) {
                Ok(delta) => {
                    // Only emit cumulative delta when last_token_usage is absent.
                    cumulative_emit = Some((
                        if input > 0 {
                            // Scale unknown; prefer reporting delta as token_total.
                            0
                        } else {
                            0
                        },
                        0,
                        delta,
                        cache,
                        reasoning,
                    ));
                    let _ = (input, output);
                }
                Err(IgnoreCode::Other(code))
                    if code == "cumulative_baseline_only" || code == "cumulative_unchanged" => {}
                Err(IgnoreCode::EstimatedOnly) => {}
                Err(other) => return Ok(DecodeOutcome::Ignore(other)),
            }
        }

        if let Some(last_obj) = info.get("last_token_usage").and_then(|v| v.as_object()) {
            let (input, output, total, cache, reasoning) = read_usage_counts(last_obj);
            if total == 0 && input == 0 && output == 0 {
                return Ok(DecodeOutcome::ContextOnly);
            }
            let native = turn
                .map(|t| TypedNativeKey::Str(format!("turn:{t}")))
                .unwrap_or(byte_native);
            return Ok(DecodeOutcome::Emit(vec![emit_usage_fact(UsageFactArgs {
                secret: &self.identity_secret,
                harness: HARNESS_ID,
                scope: logical_scope,
                native,
                fact_kind: "model_usage_recorded",
                occurred_at,
                time_source,
                token_total: total,
                input_tokens: input,
                output_tokens: output,
                accuracy: TokenAccuracy::Exact,
                session_id: session,
                turn_id: turn,
                skill_id: None,
                skill_key: None,
                model_key: 0,
                cache_read_tokens: cache,
                reasoning_tokens: reasoning,
            })]));
        }

        if let Some((_input, _output, delta, cache, reasoning)) = cumulative_emit {
            if delta == 0 {
                return Ok(DecodeOutcome::ContextOnly);
            }
            let native = turn
                .map(|t| TypedNativeKey::Str(format!("cum-turn:{}", t)))
                .unwrap_or(byte_native);
            return Ok(DecodeOutcome::Emit(vec![emit_usage_fact(UsageFactArgs {
                secret: &self.identity_secret,
                harness: HARNESS_ID,
                scope: logical_scope,
                native,
                fact_kind: "model_usage_recorded",
                occurred_at,
                time_source,
                token_total: delta,
                input_tokens: delta,
                output_tokens: 0,
                accuracy: TokenAccuracy::Derived,
                session_id: session,
                turn_id: turn,
                skill_id: None,
                skill_key: None,
                model_key: 0,
                cache_read_tokens: cache,
                reasoning_tokens: reasoning,
            })]));
        }

        Ok(DecodeOutcome::ContextOnly)
    }
}

/// Decode a synthetic OTLP cumulative record for contract tests.
pub fn decode_otlp_cumulative_for_test(
    strategy: &CodexStrategy,
    cumulative: u64,
    series: &str,
    state: &mut DecoderState,
) -> DecodeOutcome {
    let payload = json!({
        "otlp_cumulative": true,
        "series_id": series,
        "cumulative_tokens": cumulative,
    });
    let record = RawRecord {
        ordinal: 0,
        byte_start: Some(0),
        byte_end: Some(payload.to_string().len() as u64),
        native_rowid: None,
        payload: payload.to_string().into_bytes(),
        file_mtime_ms: None,
    };
    strategy
        .decode(&record, state, "otlp-test")
        .unwrap_or(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord))
}

#[allow(dead_code)]
fn _assert_value_used(v: &Value) {
    let _ = v;
}
