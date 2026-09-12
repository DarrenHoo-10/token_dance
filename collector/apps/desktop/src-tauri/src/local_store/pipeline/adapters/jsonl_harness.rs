//! Shared JSONL harness strategy for Claude / Grok / DeepSeek / Pi / WorkBuddy / Doubao.
//! Field differences stay in `JsonlProfile`; queue / runner logic is never copied.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::json;

use super::common::{
    emit_skill_fact, emit_usage_fact, json_obj, parse_json_record, remember_source_time,
    resolve_record_time, str_field, u64_field, SkillBook, UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use super::jsonl_io::{discover_jsonl_files, read_jsonl_source};
use crate::local_store::pipeline::runner::{
    CheckpointView, DecodeOutcome, DecoderState, DiscoveryBudget, FactDraft, HarnessStrategy,
    IgnoreCode, NativeFactKey, RawBatch, RawRecord, ReadBudget, RunnerError, SourceSpec,
    TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

#[derive(Debug, Clone, Copy)]
pub struct JsonlProfile {
    pub harness_id: &'static str,
    pub stream_key: &'static str,
    pub usage_types: &'static [&'static str],
    pub skill_types: &'static [&'static str],
    pub context_types: &'static [&'static str],
    pub time_keys: &'static [&'static str],
    pub session_keys: &'static [&'static str],
    pub turn_keys: &'static [&'static str],
    pub input_keys: &'static [&'static str],
    pub output_keys: &'static [&'static str],
    pub total_keys: &'static [&'static str],
    pub skill_name_keys: &'static [&'static str],
}

pub const CLAUDE: JsonlProfile = JsonlProfile {
    harness_id: "claude-code",
    stream_key: "projects-jsonl",
    usage_types: &["model_usage", "assistant", "result"],
    skill_types: &["skill", "skill_invoked"],
    context_types: &["user", "system", "queue-operation"],
    time_keys: &["timestamp", "ts"],
    session_keys: &["sessionId", "session_id"],
    turn_keys: &["uuid", "messageId"],
    input_keys: &["input_tokens", "inputTokens"],
    output_keys: &["output_tokens", "outputTokens"],
    total_keys: &["total_tokens", "totalTokens"],
    skill_name_keys: &["skill", "skillName", "name"],
};

pub const GROK: JsonlProfile = JsonlProfile {
    harness_id: "grok-build",
    stream_key: "updates-jsonl",
    usage_types: &["token.usage", "model_usage", "usage"],
    skill_types: &["skill"],
    context_types: &["session.start", "message"],
    time_keys: &["timestamp", "ts"],
    session_keys: &["sessionId", "thread_id"],
    turn_keys: &["turn_id", "id"],
    input_keys: &["input_tokens", "inputTokens"],
    output_keys: &["output_tokens", "outputTokens"],
    total_keys: &["total_tokens", "totalTokens"],
    skill_name_keys: &["skill", "skillName"],
};

pub const DEEPSEEK: JsonlProfile = JsonlProfile {
    harness_id: "deepseek-harness",
    stream_key: "sessions-jsonl",
    usage_types: &["token.usage", "model_usage"],
    skill_types: &["skill"],
    context_types: &["session.start", "turn.start"],
    time_keys: &["timestamp"],
    session_keys: &["sessionId", "thread_id"],
    turn_keys: &["turn_id"],
    input_keys: &["input_tokens"],
    output_keys: &["output_tokens"],
    total_keys: &["total_tokens"],
    skill_name_keys: &["skill_name", "skill"],
};

pub const PI: JsonlProfile = JsonlProfile {
    harness_id: "pi",
    stream_key: "sessions-jsonl",
    usage_types: &["model_usage", "usage"],
    skill_types: &["skill"],
    context_types: &["session", "message"],
    time_keys: &["timestamp", "ts"],
    session_keys: &["sessionId"],
    turn_keys: &["id"],
    input_keys: &["inputTokens", "input_tokens"],
    output_keys: &["outputTokens", "output_tokens"],
    total_keys: &["totalTokens", "total_tokens"],
    skill_name_keys: &["skillName", "skill"],
};

pub const WORKBUDDY: JsonlProfile = JsonlProfile {
    harness_id: "workbuddy",
    stream_key: "history-jsonl",
    usage_types: &["model_usage", "usage"],
    skill_types: &["skill"],
    context_types: &["session", "message"],
    time_keys: &["timestamp"],
    session_keys: &["sessionId"],
    turn_keys: &["id"],
    input_keys: &["inputTokens"],
    output_keys: &["outputTokens"],
    total_keys: &["totalTokens"],
    skill_name_keys: &["skillName"],
};

pub const DOUBAO: JsonlProfile = JsonlProfile {
    harness_id: "doubao-work",
    stream_key: "history-jsonl",
    usage_types: &["model_usage", "usage"],
    skill_types: &["skill"],
    context_types: &["session", "message"],
    time_keys: &["timestamp"],
    session_keys: &["sessionId"],
    turn_keys: &["id"],
    input_keys: &["inputTokens"],
    output_keys: &["outputTokens"],
    total_keys: &["totalTokens"],
    skill_name_keys: &["skillName"],
};

pub struct JsonlHarnessStrategy {
    pub profile: JsonlProfile,
    pub identity_secret: Vec<u8>,
    pub root: PathBuf,
    pub skill_book: SkillBook,
    pub skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
}

impl JsonlHarnessStrategy {
    pub fn new(
        profile: JsonlProfile,
        identity_secret: impl Into<Vec<u8>>,
        root: impl Into<PathBuf>,
        skill_book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            profile,
            identity_secret: identity_secret.into(),
            root: root.into(),
            skill_book,
            skill_allocator,
        }
    }

    fn first_str(
        o: &serde_json::Map<String, serde_json::Value>,
        keys: &[&str],
    ) -> Option<String> {
        for k in keys {
            if let Some(v) = str_field(o, k) {
                return Some(v);
            }
        }
        None
    }

    fn first_u64(o: &serde_json::Map<String, serde_json::Value>, keys: &[&str]) -> u64 {
        for k in keys {
            if let Some(v) = u64_field(o, k) {
                return v;
            }
        }
        0
    }
}

impl HarnessStrategy for JsonlHarnessStrategy {
    fn harness_id(&self) -> &str {
        self.profile.harness_id
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        let files = discover_jsonl_files(&self.root, ".jsonl", budget.max_sources);
        Ok(files
            .into_iter()
            .map(|path| {
                let scope = path.to_string_lossy().to_string();
                SourceSpec {
                    harness_id: self.profile.harness_id.into(),
                    source_key: source_key(&self.identity_secret, self.profile.harness_id, &scope),
                    source_kind: SourceKind::Jsonl,
                    locator_ref: path.to_string_lossy().into_owned(),
                    stream_key: self.profile.stream_key.into(),
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
        let kind = str_field(o, "type").unwrap_or_default();
        let (occurred_at, time_source, is_native) =
            match resolve_record_time(o, state, record, self.profile.time_keys) {
                Ok(t) => t,
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);

        let session = Self::first_str(o, self.profile.session_keys);
        let turn = Self::first_str(o, self.profile.turn_keys);
        let byte_native = TypedNativeKey::ByteOffset(record.byte_start.unwrap_or(record.ordinal));

        if self.profile.context_types.iter().any(|t| *t == kind) {
            return Ok(DecodeOutcome::ContextOnly);
        }
        if self.profile.skill_types.iter().any(|t| *t == kind) {
            let name = Self::first_str(o, self.profile.skill_name_keys)
                .unwrap_or_else(|| "unnamed-skill".into());
            let native = TypedNativeKey::Str(format!("skill:{name}:{}", turn.as_deref().unwrap_or("")));
            let alloc = self.skill_allocator.clone();
            return Ok(DecodeOutcome::Emit(vec![emit_skill_fact(
                &self.identity_secret,
                self.profile.harness_id,
                logical_scope,
                native,
                occurred_at,
                time_source,
                &name,
                &self.skill_book,
                &*alloc,
                session.as_deref(),
            )]));
        }
        if self.profile.usage_types.iter().any(|t| *t == kind) {
            let usage_obj = o
                .get("message")
                .and_then(|m| m.get("usage"))
                .and_then(|u| u.as_object())
                .or_else(|| o.get("usage").and_then(|u| u.as_object()))
                .unwrap_or(o);
            let input = Self::first_u64(usage_obj, self.profile.input_keys);
            let output = Self::first_u64(usage_obj, self.profile.output_keys);
            let total = Self::first_u64(usage_obj, self.profile.total_keys)
                .max(input.saturating_add(output));
            if total == 0 {
                return Ok(DecodeOutcome::ContextOnly);
            }
            let native = turn
                .as_ref()
                .map(|t| TypedNativeKey::Str(t.clone()))
                .unwrap_or(byte_native);
            return Ok(DecodeOutcome::Emit(vec![emit_usage_fact(UsageFactArgs {
                secret: &self.identity_secret,
                harness: self.profile.harness_id,
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
                cache_read_tokens: None,
                reasoning_tokens: None,
            })]));
        }
        Ok(DecodeOutcome::Ignore(IgnoreCode::UnsupportedStructure))
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}
