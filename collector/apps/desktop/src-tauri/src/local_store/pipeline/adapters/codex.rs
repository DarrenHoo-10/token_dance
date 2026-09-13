//! Codex harness strategy: session JSONL is the sole usage authority.
//! Native `event_msg` / `token_count` uses last_token_usage (request) and
//! total_token_usage (cumulative baseline / per-field delta). OTLP cumulative without
//! differential evidence is Ignore/baseline-only.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::{json, Map, Value};

use super::common::{
    cumulative_components_delta, cumulative_delta, emit_skill_fact, emit_usage_fact, json_obj,
    parse_json_record, remember_source_time, resolve_record_time, str_field, u64_field,
    CumulativeComponents, SkillBook, UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use super::jsonl_io::{discover_jsonl_files_in_roots, read_jsonl_source};
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
    model_allocator: Option<crate::local_store::pipeline::runner::ModelAllocator>,
    pub identity_secret: Vec<u8>,
    /// Detection source_id → root path (sessions, archived_sessions, …).
    pub roots: Vec<(String, PathBuf)>,
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
        Self::with_roots(
            identity_secret,
            vec![("codex-sessions".into(), sessions_root.into())],
            skill_book,
            skill_allocator,
        )
    }

    pub fn with_roots(
        identity_secret: impl Into<Vec<u8>>,
        roots: Vec<(String, PathBuf)>,
        skill_book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            model_allocator: None,
            identity_secret: identity_secret.into(),
            roots,
            skill_book,
            skill_allocator,
        }
    }

    /// Pick the strategy root whose path is a prefix of `locator_ref`.
    pub fn source_id_for_locator(&self, locator_ref: &str) -> Option<&str> {
        let locator = Path::new(locator_ref);
        self.roots
            .iter()
            .find(|(_, root)| locator.starts_with(root) || locator == root.as_path())
            .map(|(id, _)| id.as_str())
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
    let cache =
        u64_field(usage, "cached_input_tokens").or_else(|| u64_field(usage, "cache_read_tokens"));
    let reasoning = u64_field(usage, "reasoning_output_tokens")
        .or_else(|| u64_field(usage, "reasoning_tokens"));
    (input, output, total, cache, reasoning)
}

impl HarnessStrategy for CodexStrategy {
    fn set_model_allocator(&mut self, allocator:crate::local_store::pipeline::runner::ModelAllocator) {self.model_allocator=Some(allocator);}

    fn harness_id(&self) -> &str {
        HARNESS_ID
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        let (files, _) = discover_jsonl_files_in_roots(
            self.roots.iter().map(|(_, root)| root.as_path()),
            ".jsonl",
            budget.max_sources,
            budget.resume_after.as_deref(),
        );
        Ok(files
            .into_iter()
            .map(|path| {
                let locator = path.to_string_lossy().into_owned();
                SourceSpec {
                    harness_id: HARNESS_ID.into(),
                    source_key: source_key(&self.identity_secret, HARNESS_ID, &locator),
                    source_kind: SourceKind::Jsonl,
                    locator_ref: locator,
                    stream_key: STREAM_SESSIONS.into(),
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

    fn decode(&self, record:&RawRecord, state:&mut DecoderState, logical_scope:&str)->Result<DecodeOutcome,RunnerError> {
        let mut result=self.decode_with_context(record,state,logical_scope)?;
        if let Some(model)=state.json.get("codex_model").and_then(Value::as_str).filter(|s|!s.is_empty()) {
            let provider=state.json.get("codex_provider").and_then(Value::as_str).unwrap_or("openai");
            if let DecodeOutcome::Emit(facts)=&mut result {
                for fact in facts.iter_mut().filter(|f|f.event_type=="model_usage_recorded") {
                    fact.model_identity=Some((provider.into(),model.into()));
                    if let Some(allocate)=&self.model_allocator {fact.model_key=allocate(provider,model)?;}
                    fact.fact_revision=2;
                    fact.event_id=super::identity::event_id(&self.identity_secret,&fact.fact_key,2);
                }
            }
        }
        Ok(result)
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}

impl CodexStrategy {
    fn decode_with_context(
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
        // Fork headers describe how many inherited records precede this agent's
        // own history. Persist the remaining count because reads are batched.
        if let Some(remaining) = state.json.get("codex_inherited_remaining").and_then(Value::as_u64).filter(|n| *n > 0) {
            state.json["codex_inherited_remaining"] = json!(remaining - 1);
            return Ok(DecodeOutcome::ContextOnly);
        }
        if kind == "session_meta" {
            if state.json.get("codex_header_seen").and_then(Value::as_bool) == Some(true) {
                return Ok(DecodeOutcome::ContextOnly);
            }
            state.json["codex_header_seen"] = json!(true);
            if let Some(start) = value.pointer("/payload/subagent_history_start_ordinal").and_then(Value::as_u64) {
                state.json["codex_inherited_remaining"] = json!(start.saturating_sub(1));
            }
        }

        if let Some(model)=value.pointer("/payload/model").or_else(||value.get("model")).and_then(Value::as_str).filter(|s|!s.is_empty()) {
            state.json["codex_model"]=json!(model);
        }
        if let Some(provider)=value.pointer("/payload/model_provider").or_else(||value.get("model_provider")).and_then(Value::as_str).filter(|s|!s.is_empty()) {
            state.json["codex_provider"]=json!(provider);
        }
        // Only call/session records can change this state. Token records must not
        // deserialize the skill history on the hot collection path.
        let tracks_skills = kind == "session_meta"
            || kind == "turn_context"
            || (kind == "event_msg"
                && value.pointer("/payload/type").and_then(Value::as_str) == Some("task_started"))
            || (kind == "response_item" && value.pointer("/payload/call_id").is_some());
        let skill_read = if tracks_skills {
            let mut reads: adapter_codex::SkillReads = serde_json::from_value(
                state
                    .json
                    .get("skill_reads")
                    .cloned()
                    .unwrap_or(Value::Null),
            )
            .unwrap_or_default();
            let result = reads.observe(&value);
            state.json["skill_reads"] =
                serde_json::to_value(reads).expect("serializable skill reader");
            result
        } else {
            None
        };
        // The native session header survives archive/unarchive. Persist its ID
        // with the cursor so later batches keep the same logical fact scope.
        if kind == "session_meta" {
            if let Some(id) = o
                .get("payload")
                .and_then(|v| v.as_object())
                .and_then(|p| str_field(p, "id").or_else(|| str_field(p, "session_id")))
                .filter(|id| !id.is_empty())
            {
                state.json["codex_session_id"] = json!(id);
            }
        }
        let session_scope = state
            .json
            .get("codex_session_id")
            .and_then(|v| v.as_str())
            .map(|id| {
                let segment = Path::new(logical_scope).file_name().and_then(|n| n.to_str()).unwrap_or(logical_scope);
                format!("codex-session:{id}:segment:{segment}")
            });
        // Headerless secondary formats still use the file scope: equal byte
        // offsets or turn labels in independent files must not collide.
        let logical_scope = session_scope.as_deref().unwrap_or(logical_scope);
        let (occurred_at, time_source, is_native) =
            match resolve_record_time(o, state, record, &["timestamp", "ts", "time"]) {
                Ok(t) => t,
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);

        let session = str_field(o, "thread_id")
            .or_else(|| str_field(o, "session_id"))
            .or_else(|| {
                state
                    .json
                    .get("codex_session_id")
                    .and_then(|v| v.as_str())
                    .map(str::to_owned)
            });

        if let Some(skill) = skill_read {
            let name = skill["skill_name"].as_str().expect("skill identity");
            let fallback = super::common::skill_display_name(name);
            let public = skill["skill_public_name"]
                .as_str()
                .filter(|s| !s.is_empty())
                .unwrap_or(&fallback);
            let alloc = |key, _: &str| (self.skill_allocator)(key, public);
            let mut fact = emit_skill_fact(
                &self.identity_secret,
                HARNESS_ID,
                logical_scope,
                TypedNativeKey::Str(
                    skill["invocation_id"]
                        .as_str()
                        .expect("invocation identity")
                        .into(),
                ),
                occurred_at,
                time_source,
                name,
                &self.skill_book,
                &alloc,
                session.as_deref(),
            );
            fact.accuracy = TokenAccuracy::Correlated;
            fact.payload_sections = json!({"activity":{"success":true}});
            return Ok(DecodeOutcome::Emit(vec![fact]));
        }
        let nested = o.get("payload").and_then(Value::as_object);
        let turn = str_field(o, "turn_id")
            .or_else(|| nested.and_then(|p| str_field(p, "turn_id")))
            .or_else(|| {
                state
                    .json
                    .get("active_turn_id")
                    .and_then(Value::as_str)
                    .map(str::to_owned)
            });
        if let Some(session) = session.as_deref() {
            let payload_type = nested
                .and_then(|p| str_field(p, "type"))
                .unwrap_or_default();
            let mut activity = json!({});
            let lifecycle = if kind == "session_meta" {
                Some(("session_started", session.to_string(), None))
            } else if kind == "event_msg" && payload_type == "task_started" {
                if let Some(t) = turn.as_deref() {
                    state.json["active_turn_id"] = json!(t);
                    state.json["active_turn_started_at"] = json!(occurred_at);
                    Some(("turn_started", t.to_string(), Some(t)))
                } else {
                    None
                }
            } else if kind == "event_msg" && payload_type == "task_complete" {
                if let Some(t) = turn.as_deref() {
                    if state.json.get("active_turn_id").and_then(Value::as_str) == Some(t) {
                        if let Some(start) = state
                            .json
                            .get("active_turn_started_at")
                            .and_then(Value::as_i64)
                        {
                            if occurred_at >= start {
                                activity["duration_ms"] = json!(occurred_at - start);
                            }
                        }
                    }
                    state.json["active_turn_id"] = Value::Null;
                    state.json["active_turn_started_at"] = Value::Null;
                    activity["success"] = json!(true);
                    Some(("turn_completed", t.to_string(), Some(t)))
                } else {
                    None
                }
            } else if kind == "response_item"
                && payload_type == "message"
                && nested.and_then(|p| str_field(p, "role")).as_deref() == Some("user")
            {
                turn.as_deref().map(|t| {
                    activity["trigger"] = json!("user");
                    ("turn_started", format!("{t}:user:{}", record.byte_start.unwrap_or(record.ordinal)), Some(t))
                })
            } else {
                None
            };
            if let Some((event_type, native, turn)) = lifecycle {
                return Ok(DecodeOutcome::Emit(vec![
                    super::common::emit_activity_fact(
                        &self.identity_secret,
                        HARNESS_ID,
                        logical_scope,
                        TypedNativeKey::Str(native),
                        event_type,
                        occurred_at,
                        time_source,
                        session,
                        turn,
                        activity,
                    ),
                ]));
            }
        }
        if kind == "response_item" {
            if let Some(p) = nested {
                let ty = str_field(p, "type").unwrap_or_default();
                if ty == "custom_tool_call"
                    && str_field(p, "name").as_deref() == Some("apply_patch")
                {
                    if let (Some(call), Some(patch)) = (
                        str_field(p, "call_id"),
                        p.get("input").and_then(Value::as_str),
                    ) {
                        if let Some(code) = super::common::patch_code_payload(patch) {
                            if !state.json["pending_code"].is_object() {
                                state.json["pending_code"] = json!({});
                            }
                            // Persist only counts, never raw code or paths.
                            if state.json["pending_code"]
                                .as_object()
                                .map_or(0, |m| m.len())
                                < 128
                            {
                                state.json["pending_code"][call] = code;
                            }
                        }
                    }
                } else if ty == "custom_tool_call_output" {
                    if let Some(call) = str_field(p, "call_id") {
                        let code = state.json["pending_code"]
                            .as_object_mut()
                            .and_then(|m| m.remove(&call));
                        let output = p.get("output").and_then(Value::as_str).unwrap_or("");
                        let parsed = serde_json::from_str::<Value>(output).ok();
                        let success = parsed.as_ref().is_some_and(|v| {
                            v.pointer("/metadata/exit_code").and_then(Value::as_i64) == Some(0)
                        }) || output
                            .starts_with("Success. Updated the following files:");
                        if success {
                            if let Some(code) = code {
                                let mut fact = super::common::emit_code_fact(
                                    &self.identity_secret,
                                    HARNESS_ID,
                                    logical_scope,
                                    TypedNativeKey::Str(call),
                                    occurred_at,
                                    time_source,
                                    session.as_deref(),
                                    0,
                                    0,
                                );
                                fact.payload_sections = json!({"code":code});
                                return Ok(DecodeOutcome::Emit(vec![fact]));
                            }
                        }
                    }
                }
            }
        }
        let byte_native = TypedNativeKey::ByteOffset(record.byte_start.unwrap_or(record.ordinal));

        match kind.as_str() {
            "event_msg" => {
                let payload = o.get("payload").and_then(|p| p.as_object());
                let Some(payload) = payload else {
                    return Ok(DecodeOutcome::ContextOnly);
                };
                let payload_type = str_field(payload, "type").unwrap_or_default();
                match payload_type.as_str() {
                    "token_count" => self.decode_token_count(
                        payload,
                        state,
                        logical_scope,
                        occurred_at,
                        time_source,
                        session.as_deref(),
                        turn.as_deref(),
                        byte_native,
                    ),
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
            "skill.injected" | "skill.loaded" | "skill.execution.started" => {
                Ok(DecodeOutcome::ContextOnly)
            }
            "skill.execution.failed" | "skill.execution.completed" | "skill_invoked" => {
                let Some(name) = str_field(o, "skill_name").or_else(|| str_field(o, "skill"))
                else {
                    return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
                };
                let native = str_field(o, "invocation_id")
                    .or_else(|| str_field(o, "call_id"))
                    .or_else(|| str_field(o, "id"))
                    .map(TypedNativeKey::Str)
                    .unwrap_or(TypedNativeKey::ByteOffset(
                        record.byte_start.unwrap_or(record.ordinal),
                    ));
                let mut fact = emit_skill_fact(
                    &self.identity_secret,
                    HARNESS_ID,
                    logical_scope,
                    native,
                    occurred_at,
                    time_source,
                    &name,
                    &self.skill_book,
                    &*self.skill_allocator,
                    session.as_deref(),
                );
                let success = match kind.as_str() {
                    "skill.execution.failed" => Some(false),
                    "skill.execution.completed" => Some(true),
                    _ => o.get("success").and_then(|v| v.as_bool()),
                };
                if let Some(success) = success {
                    fact.payload_sections["activity"]["success"] = json!(success);
                }
                Ok(DecodeOutcome::Emit(vec![fact]))
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

        // Always advance cumulative baselines from total_token_usage when present.
        let mut cumulative_emit = None;
        if let Some(total_obj) = info.get("total_token_usage").and_then(|v| v.as_object()) {
            let (input, output, total, cache, reasoning) = read_usage_counts(total_obj);
            let series = format!("total::{}", session.unwrap_or("default"));
            match cumulative_components_delta(
                state,
                &series,
                CumulativeComponents {
                    input,
                    output,
                    total,
                    cache,
                    reasoning,
                },
            ) {
                Ok(delta) => {
                    cumulative_emit = Some(delta);
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
            // A turn can contain many model requests; its ID is context, not request identity.
            let native = byte_native;
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

        if let Some(delta) = cumulative_emit {
            if delta.total == 0 && delta.input == 0 && delta.output == 0 {
                return Ok(DecodeOutcome::ContextOnly);
            }
            let native = byte_native;
            return Ok(DecodeOutcome::Emit(vec![emit_usage_fact(UsageFactArgs {
                secret: &self.identity_secret,
                harness: HARNESS_ID,
                scope: logical_scope,
                native,
                fact_kind: "model_usage_recorded",
                occurred_at,
                time_source,
                token_total: delta.total,
                input_tokens: delta.input,
                output_tokens: delta.output,
                accuracy: TokenAccuracy::Derived,
                session_id: session,
                turn_id: turn,
                skill_id: None,
                skill_key: None,
                model_key: 0,
                cache_read_tokens: delta.cache,
                reasoning_tokens: delta.reasoning,
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
