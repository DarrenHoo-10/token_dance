//! Cursor harness: transcript JSONL with time fallback; remote aggregates never
//! masquerade as per-request usage facts.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::json;

use super::capability::{self, CapabilityLevel};
use super::common::{
    emit_usage_fact, json_obj, parse_json_record, remember_source_time, resolve_record_time,
    str_field, u64_field, SkillBook, UsageFactArgs,
};
use super::cursor_usage::{decode_usage, CursorUsagePaths, CursorUsageSource, STREAM_USAGE};
use super::identity::{source_key, TypedNativeKey};
use super::jsonl_io::{discover_jsonl_files, read_jsonl_source};
use crate::local_store::pipeline::runner::{
    CheckpointView, DecodeOutcome, DecoderState, DiscoveryBudget, FactDraft, HarnessStrategy,
    IgnoreCode, NativeFactKey, RawBatch, RawRecord, ReadBudget, RunnerError, SourceSpec,
    TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "cursor";
pub const STREAM_TRANSCRIPT: &str = "transcript-jsonl";
pub const STREAM_REMOTE: &str = "remote-api-aggregate";

pub struct CursorStrategy {
    pub identity_secret: Vec<u8>,
    pub transcripts_root: PathBuf,
    pub usage_source: Option<CursorUsageSource>,
    model_allocator: Option<crate::local_store::pipeline::runner::ModelAllocator>,
    pub skill_book: SkillBook,
    #[allow(dead_code)]
    pub skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
}

impl CursorStrategy {
    pub fn new(
        identity_secret: impl Into<Vec<u8>>,
        transcripts_root: impl Into<PathBuf>,
        skill_book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            identity_secret: identity_secret.into(),
            transcripts_root: transcripts_root.into(),
            usage_source: None,
            model_allocator: None,
            skill_book,
            skill_allocator,
        }
    }
}

impl CursorStrategy {
    pub fn with_usage(mut self, paths: CursorUsagePaths) -> Self {
        self.usage_source = Some(CursorUsageSource::new(paths));
        self
    }
}

impl HarnessStrategy for CursorStrategy {
    fn set_model_allocator(
        &mut self,
        allocator: crate::local_store::pipeline::runner::ModelAllocator,
    ) {
        self.model_allocator = Some(allocator);
    }
    fn collection_status(&self) -> Option<&'static str> {
        self.usage_source
            .as_ref()
            .filter(|s| s.configured())
            .map(|s| s.status())
    }

    fn harness_id(&self) -> &str {
        HARNESS_ID
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        let mut specs = Vec::new();
        if let Some(source) = self.usage_source.as_ref().filter(|s| s.configured()) {
            let locator = source.locator();
            specs.push(SourceSpec {
                harness_id: HARNESS_ID.into(),
                source_key: source_key(&self.identity_secret, HARNESS_ID, &locator),
                source_kind: SourceKind::Other,
                locator_ref: locator,
                stream_key: STREAM_USAGE.into(),
                cursor_kind: CursorKind::Opaque,
                initial_cursor_json: json!({}),
                initial_decoder_state_json: json!({}),
                observed_boundary_json: json!({}),
            });
        }
        let (files, _cursor) = discover_jsonl_files(
            &self.transcripts_root,
            ".jsonl",
            budget.max_sources,
            budget.resume_after.as_deref(),
        );
        for path in files {
            let scope = path.to_string_lossy().to_string();
            specs.push(SourceSpec {
                harness_id: HARNESS_ID.into(),
                source_key: source_key(&self.identity_secret, HARNESS_ID, &scope),
                source_kind: SourceKind::Jsonl,
                locator_ref: path.to_string_lossy().into_owned(),
                stream_key: STREAM_TRANSCRIPT.into(),
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
        if stream_key == STREAM_USAGE {
            return self
                .usage_source
                .as_ref()
                .ok_or_else(|| RunnerError::InvalidArgument("CURSOR_SOURCE_MISSING".into()))?
                .read(&self.transcripts_root, committed, budget);
        }
        if stream_key == STREAM_REMOTE
            || capability::stream_level(HARNESS_ID, stream_key)
                == Some(CapabilityLevel::Unavailable)
        {
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
        if value["cursor_usage"].as_bool() == Some(true) {
            let mut fact = decode_usage(&self.identity_secret, &value)?;
            if let (Some(allocate), Some(model)) = (
                &self.model_allocator,
                value["model"].as_str().filter(|s| !s.is_empty()),
            ) {
                fact.model_key = allocate("cursor", model)?;
                fact.model_identity = Some(("cursor".into(), model.into()));
            }
            return Ok(DecodeOutcome::Emit(vec![fact]));
        }
        let Some(o) = json_obj(&value) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };

        if o.get("api_aggregate").and_then(|v| v.as_bool()) == Some(true)
            || o.get("kind").and_then(|v| v.as_str()) == Some("daily_summary")
        {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::EstimatedOnly));
        }

        let (occurred_at, time_source, is_native) =
            match resolve_record_time(o, state, record, &["timestamp", "createdAt", "ts"]) {
                Ok(t) => t,
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);
        let context = super::native_jsonl::Context {
            harness: HARNESS_ID,
            secret: &self.identity_secret,
            scope: logical_scope,
            now: occurred_at,
            time_source,
            record,
            book: &self.skill_book,
            allocator: &*self.skill_allocator,
        };
        if let Some(mut facts) = if value.get("message").is_some() || value["type"] == "turn_ended"
        {
            super::native_jsonl::decode(&context, &value, state)
        } else {
            None
        } {
            // API owns tokens; local transcripts contribute only non-token facts.
            facts.retain(|f| f.event_type != "model_usage_recorded");
            return Ok(if facts.is_empty() {
                DecodeOutcome::ContextOnly
            } else {
                DecodeOutcome::Emit(facts)
            });
        }
        if self.usage_source.is_some() {
            return Ok(DecodeOutcome::ContextOnly);
        }

        let kind = str_field(o, "type")
            .or_else(|| str_field(o, "role"))
            .unwrap_or_default();
        let session = str_field(o, "sessionId").or_else(|| str_field(o, "composerId"));
        let native = TypedNativeKey::ByteOffset(record.byte_start.unwrap_or(record.ordinal));

        let tokens = u64_field(o, "tokenCount")
            .or_else(|| u64_field(o, "totalTokens"))
            .or_else(|| {
                o.get("usage")
                    .and_then(|u| u64_field(u.as_object()?, "totalTokens"))
            })
            .unwrap_or(0);

        if tokens == 0 {
            let _ = kind;
            return Ok(DecodeOutcome::ContextOnly);
        }

        Ok(DecodeOutcome::Emit(vec![emit_usage_fact(UsageFactArgs {
            secret: &self.identity_secret,
            harness: HARNESS_ID,
            scope: logical_scope,
            native,
            fact_kind: "model_usage_recorded",
            occurred_at,
            time_source,
            token_total: tokens,
            input_tokens: tokens,
            output_tokens: 0,
            accuracy: TokenAccuracy::Derived,
            session_id: session.as_deref(),
            turn_id: None,
            skill_id: None,
            skill_key: None,
            model_key: 0,
            cache_read_tokens: None,
            reasoning_tokens: None,
        })]))
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}
