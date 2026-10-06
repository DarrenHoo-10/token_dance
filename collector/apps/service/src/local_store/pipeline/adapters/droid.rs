//! Droid (Factory) CLI harness: per-response usage from the local daemon log.
//!
//! droid appends one `[Agent] Streaming result` line per LLM response to
//! `~/.factory/logs/droid-log-single.log` (truncated on every daemon start).
//! The line is `[RFC3339Z] LEVEL: [Agent] Streaming result | Context: {json}`;
//! the JSON carries the exact per-request token components and a `tags` object
//! with `modelId` / `sessionId`. The session JSONL and the per-session
//! `*.settings.json` cumulative `tokenUsage` carry no per-request time or
//! model, so the log is the only source that supports exact per-response
//! facts with native timestamps.
//!
//! droid's `inputTokens` excludes cached tokens (`totalInputTokens` adds them
//! back) and `reasoningTokens` is disjoint from `outputTokens`, so
//! `token_total = totalInputTokens + cachedTokensWritten + output + reasoning`.
//! The log file shrinking below the committed offset is a new log generation:
//! reading restarts from zero and stable native keys
//! (`sessionId/upstreamResponseId`) dedup any overlap.

use std::path::{Path, PathBuf};

use serde_json::{json, Value};

use super::common::{
    emit_usage_fact, parse_timestamp, remember_source_time, str_field, u64_field, UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use crate::local_store::pipeline::runner::{
    read_jsonl_budgeted_with_state, CheckpointView, DecodeOutcome, DecoderState, DiscoveryBudget,
    FactDraft, HarnessStrategy, IgnoreCode, NativeFactKey, RawBatch, RawRecord, ReadBudget,
    RunnerError, SourceSpec, TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "droid";
pub const STREAM_LOG: &str = "droid-log";

/// Fixed text between the RFC3339 timestamp prefix and the response JSON.
const STREAMING_RESULT_MARKER: &str = ": [Agent] Streaming result | Context: ";

pub struct DroidStrategy {
    pub identity_secret: Vec<u8>,
    pub logs_dir: PathBuf,
    model_allocator: Option<crate::local_store::pipeline::runner::ModelAllocator>,
}

impl DroidStrategy {
    pub fn new(identity_secret: impl Into<Vec<u8>>, logs_dir: impl Into<PathBuf>) -> Self {
        Self {
            identity_secret: identity_secret.into(),
            logs_dir: logs_dir.into(),
            model_allocator: None,
        }
    }
}

/// The daemon log lives flat in `logs/`; siblings (console, stderr) stay out.
fn log_files(root: &Path) -> Vec<PathBuf> {
    let entries = match std::fs::read_dir(root) {
        Ok(entries) => entries,
        Err(_) => return Vec::new(),
    };
    let mut out = Vec::new();
    for entry in entries.flatten() {
        let path = entry.path();
        if !path.is_file() {
            continue;
        }
        let name = path.file_name().and_then(|n| n.to_str()).unwrap_or("");
        if name.starts_with("droid-log") && name.ends_with(".log") {
            out.push(path);
        }
    }
    out.sort();
    out
}

/// `[2026-10-06T15:02:28.842Z] LEVEL: …` → the RFC3339 timestamp inside brackets.
fn log_timestamp(text: &str) -> Option<&str> {
    let rest = text.strip_prefix('[')?;
    let end = rest.find(']')?;
    rest.get(..end)
}

impl HarnessStrategy for DroidStrategy {
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
        let mut files = log_files(&self.logs_dir);
        if let Some(after) = budget.resume_after.as_deref() {
            let start = files
                .iter()
                .position(|path| path.to_string_lossy().as_ref() > after)
                .unwrap_or(0);
            files.drain(..start);
        }
        files.truncate(budget.max_sources);
        Ok(files
            .into_iter()
            .map(|path| {
                let scope = path.to_string_lossy().into_owned();
                SourceSpec {
                    harness_id: HARNESS_ID.into(),
                    source_key: source_key(&self.identity_secret, HARNESS_ID, &scope),
                    source_kind: SourceKind::Jsonl,
                    locator_ref: scope,
                    stream_key: STREAM_LOG.into(),
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
        let offset = committed
            .cursor_json
            .get("offset")
            .and_then(Value::as_u64)
            .unwrap_or(0);
        let mut result =
            read_jsonl_budgeted_with_state(Path::new(locator_ref), offset, None, budget, false)?;
        if result.source_change.is_some() {
            // Log restarted between stat and read: begin the new generation.
            result =
                read_jsonl_budgeted_with_state(Path::new(locator_ref), 0, None, budget, false)?;
        }
        let records = result
            .records
            .into_iter()
            .enumerate()
            .map(|(i, rec)| RawRecord {
                ordinal: i as u64,
                byte_start: Some(rec.byte_start),
                byte_end: Some(rec.byte_end),
                native_rowid: None,
                payload: rec.payload,
                file_mtime_ms: result.file_mtime_ms,
            })
            .collect();
        Ok(RawBatch {
            records,
            next_cursor_json: json!({ "offset": result.next_offset }),
            next_observed_boundary_json: json!({ "len": result.file_len }),
            has_more: result.has_more,
            bytes_read: result.bytes_read,
            ignored_incomplete_tail: result.ignored_incomplete_tail,
        })
    }

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        logical_scope: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        let Ok(text) = std::str::from_utf8(&record.payload) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let text = text.trim();
        let Some(marker_at) = text.find(STREAMING_RESULT_MARKER) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::UnsupportedStructure));
        };
        let Some(ts) = log_timestamp(text) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let ctx = &text[marker_at + STREAMING_RESULT_MARKER.len()..];
        let value: Value = match serde_json::from_str(ctx.trim()) {
            Ok(v) => v,
            Err(_) => return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord)),
        };
        let Some(o) = value.as_object() else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };

        let (occurred_at, time_source, is_native) =
            match crate::local_store::pipeline::runner::resolve_event_time(
                parse_timestamp(Some(&Value::from(ts))),
                state.json.get("last_source_time").and_then(Value::as_i64),
                record.file_mtime_ms,
            ) {
                Ok(resolved) => (
                    resolved.occurred_at,
                    resolved.time_source,
                    resolved.is_native_source,
                ),
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);

        let tags = o.get("tags").and_then(Value::as_object);
        let session = tags
            .and_then(|t| str_field(t, "sessionId"))
            .filter(|s| !s.is_empty());
        let model = tags
            .and_then(|t| str_field(t, "modelId"))
            .filter(|s| !s.is_empty());
        let response = str_field(o, "upstreamResponseId").filter(|s| !s.is_empty());

        let input = u64_field(o, "inputTokens").unwrap_or(0);
        let cache_read_field = u64_field(o, "cacheReadInputTokens");
        let cache_read = cache_read_field.unwrap_or(0);
        let cache_write = u64_field(o, "cachedTokensWritten").unwrap_or(0);
        let output = u64_field(o, "outputTokens").unwrap_or(0);
        let reasoning_field = u64_field(o, "reasoningTokens");
        let reasoning = reasoning_field.unwrap_or(0);
        let input_context = u64_field(o, "totalInputTokens")
            .unwrap_or_else(|| input.saturating_add(cache_read))
            .saturating_add(cache_write);
        let total = input_context
            .saturating_add(output)
            .saturating_add(reasoning);
        if total == 0 {
            return Ok(DecodeOutcome::ContextOnly);
        }

        let native = match (&session, &response) {
            (Some(s), Some(r)) => TypedNativeKey::Str(format!("{s}/{r}")),
            _ => TypedNativeKey::ByteOffset(record.byte_start.unwrap_or(record.ordinal)),
        };
        let mut fact = emit_usage_fact(UsageFactArgs {
            secret: &self.identity_secret,
            harness: HARNESS_ID,
            scope: logical_scope,
            native,
            fact_kind: "model_usage_recorded",
            occurred_at,
            time_source,
            token_total: total,
            input_tokens: input_context,
            output_tokens: output,
            accuracy: TokenAccuracy::Exact,
            session_id: session.as_deref(),
            turn_id: response.as_deref(),
            skill_id: None,
            skill_key: None,
            model_key: 0,
            cache_read_tokens: cache_read_field,
            reasoning_tokens: reasoning_field,
        });
        fact.payload_sections["usage"]["cache_write_tokens"] = json!(cache_write);
        if let (Some(allocate), Some(model)) = (&self.model_allocator, &model) {
            fact.model_key = allocate(HARNESS_ID, model)?;
        }
        fact.model_identity = model.map(|m| (HARNESS_ID.to_string(), m));
        Ok(DecodeOutcome::Emit(vec![fact]))
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}
