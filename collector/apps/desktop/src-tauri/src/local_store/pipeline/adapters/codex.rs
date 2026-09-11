//! Codex harness strategy: session JSONL is the sole usage authority.
//! OTLP cumulative series without differential evidence is Ignore/baseline-only.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::{json, Value};

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
            // OTLP is secondary; strategy surfaces empty until a durable push
            // adapter feeds records. Never invent request rows from aggregates.
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
            "token.usage" | "model_usage" | "event_msg" => {
                // Codex may embed usage under payload; support both flat and nested.
                let usage_obj = o
                    .get("payload")
                    .and_then(|p| p.get("info"))
                    .and_then(|i| i.as_object())
                    .unwrap_or(o);
                let input = u64_field(usage_obj, "input_tokens")
                    .or_else(|| u64_field(usage_obj, "inputTokens"))
                    .unwrap_or(0);
                let output = u64_field(usage_obj, "output_tokens")
                    .or_else(|| u64_field(usage_obj, "outputTokens"))
                    .unwrap_or(0);
                let total = u64_field(usage_obj, "total_tokens")
                    .or_else(|| u64_field(usage_obj, "totalTokens"))
                    .unwrap_or(input.saturating_add(output));
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
                    scope: STREAM_SESSIONS,
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
                    model_key: 0,
                    extra_usage: json!({}),
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
                    STREAM_SESSIONS,
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
                // Unknown record shapes: context-only when they look structural,
                // otherwise ignore as unsupported (do not block the stream).
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
    strategy.decode(&record, state).unwrap_or(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord))
}

#[allow(dead_code)]
fn _assert_value_used(v: &Value) {
    let _ = v;
}
