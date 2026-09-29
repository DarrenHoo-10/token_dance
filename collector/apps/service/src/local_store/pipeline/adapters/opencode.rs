//! OpenCode SQLite: multi-session DB, numeric part ids, composite code cursor.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::{json, Value};

use super::common::{
    emit_code_fact, emit_usage_fact, i64_field, json_obj, remember_source_time,
    resolve_record_time, str_field, u64_field, SkillBook, UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use crate::local_store::pipeline::runner::{
    cursor_from_parts, read_sqlite_change_stream, CheckpointView, DecodeOutcome, DecoderState,
    DiscoveryBudget, FactDraft, HarnessStrategy, IgnoreCode, NativeFactKey, PendingSet, RawBatch,
    RawRecord, ReadBudget, RunnerError, SourceSpec, SqliteChangeMode, TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "opencode";
pub const STREAM_SESSION: &str = "sqlite/session";
pub const STREAM_STEP: &str = "sqlite/step_finish";
pub const STREAM_CODE: &str = "sqlite/code_part";

pub const SQL_SESSION: &str = "SELECT rowid, time_created AS updated_at, 'completed' AS status, \
     json_object('type','session','id',rowid,'sessionId',id,'timestamp',time_created, \
       'model',COALESCE(model,'unknown')) \
     FROM session WHERE rowid > ?1 ORDER BY rowid";

pub const SQL_STEP: &str = "SELECT rowid, time_created AS updated_at, 'completed' AS status, \
     json_object('type','step_finish','id',rowid,'sessionId',session_id, \
       'timestamp',time_created, \
       'model',json_extract((SELECT data FROM message WHERE id=part.message_id),'$.modelID'), \
       'provider',json_extract((SELECT data FROM message WHERE id=part.message_id),'$.providerID'), \
       'inputTokens',json_extract(data,'$.tokens.input'), \
       'outputTokens',json_extract(data,'$.tokens.output'), \
       'cacheReadTokens',json_extract(data,'$.tokens.cache.read'), \
       'cacheWriteTokens',json_extract(data,'$.tokens.cache.write'), \
       'reasoningTokens',json_extract(data,'$.tokens.reasoning'), \
       'totalTokens', \
         COALESCE(json_extract(data,'$.tokens.input'),0) \
         + COALESCE(json_extract(data,'$.tokens.output'),0) \
         + COALESCE(json_extract(data,'$.tokens.cache.read'),0) \
         + COALESCE(json_extract(data,'$.tokens.cache.write'),0)) \
     FROM part WHERE json_extract(data,'$.type') = 'step-finish' AND rowid > ?1 ORDER BY rowid";

pub const SQL_CODE: &str = "SELECT rowid, time_updated AS updated_at, 'completed' AS status, \
     json_object('type','code_changed','id',rowid,'sessionId',session_id, \
       'timestamp',time_updated,'callId',json_extract(data,'$.callID'), \
       'part',json(data)) \
     FROM part WHERE json_extract(data,'$.type') = 'tool' \
       AND ((time_updated > ?1) OR (time_updated = ?1 AND rowid > ?2)) \
     ORDER BY time_updated, rowid";

pub struct OpenCodeStrategy {
    pub identity_secret: Vec<u8>,
    pub db_path: PathBuf,
    model_allocator: Option<crate::local_store::pipeline::runner::ModelAllocator>,
    pub skill_book: SkillBook,
    #[allow(dead_code)]
    pub skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
}

impl OpenCodeStrategy {
    pub fn new(
        identity_secret: impl Into<Vec<u8>>,
        db_path: impl Into<PathBuf>,
        skill_book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            identity_secret: identity_secret.into(),
            db_path: db_path.into(),
            model_allocator: None,
            skill_book,
            skill_allocator,
        }
    }

    fn stream_mode(stream_key: &str) -> Option<(SqliteChangeMode, &'static str)> {
        match stream_key {
            STREAM_SESSION => Some((SqliteChangeMode::AppendRowid, SQL_SESSION)),
            STREAM_STEP => Some((SqliteChangeMode::AppendRowid, SQL_STEP)),
            STREAM_CODE => Some((SqliteChangeMode::UpdatedAtRowid, SQL_CODE)),
            _ => None,
        }
    }
}

impl HarnessStrategy for OpenCodeStrategy {
    fn set_model_allocator(
        &mut self,
        allocator: crate::local_store::pipeline::runner::ModelAllocator,
    ) {
        self.model_allocator = Some(allocator);
    }
    fn harness_id(&self) -> &str {
        HARNESS_ID
    }

    fn discover(&self, _budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        if !self.db_path.exists() {
            return Ok(vec![]);
        }
        let scope = self.db_path.to_string_lossy().to_string();
        let sk = source_key(&self.identity_secret, HARNESS_ID, &scope);
        let pending = PendingSet::new(4096);
        Ok([STREAM_SESSION, STREAM_STEP, STREAM_CODE]
            .into_iter()
            .map(|stream| {
                let (mode, _) = Self::stream_mode(stream).unwrap();
                SourceSpec {
                    harness_id: HARNESS_ID.into(),
                    source_key: sk,
                    source_kind: SourceKind::Sqlite,
                    locator_ref: self.db_path.to_string_lossy().into_owned(),
                    stream_key: stream.into(),
                    cursor_kind: CursorKind::SqliteChange,
                    initial_cursor_json: cursor_from_parts(mode, 0, 0, &pending),
                    initial_decoder_state_json: json!({ "last_source_time": null }),
                    observed_boundary_json: json!({}),
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
        let Some((mode, sql)) = Self::stream_mode(stream_key) else {
            return Err(RunnerError::InvalidArgument(format!(
                "unknown opencode stream {stream_key}"
            )));
        };
        let result = read_sqlite_change_stream(
            Path::new(locator_ref),
            sql,
            &committed.cursor_json,
            mode,
            budget,
        )?;
        let records = result
            .rows
            .into_iter()
            .enumerate()
            .map(|(i, row)| {
                let mut payload = row.payload_json;
                if let Some(obj) = payload.as_object_mut() {
                    obj.insert("_rowid".into(), json!(row.rowid));
                }
                RawRecord {
                    ordinal: i as u64,
                    byte_start: None,
                    byte_end: None,
                    native_rowid: Some(row.rowid),
                    payload: serde_json::to_vec(&payload).unwrap_or_default(),
                    file_mtime_ms: None,
                }
            })
            .collect();
        Ok(RawBatch {
            records,
            next_cursor_json: result.next_cursor_json,
            next_observed_boundary_json: committed.observed_boundary_json.clone(),
            has_more: result.has_more,
            bytes_read: result.bytes_read,
            ignored_incomplete_tail: false,
        })
    }

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        logical_scope: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        let value: Value = match serde_json::from_slice(&record.payload) {
            Ok(v) => v,
            Err(_) => return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord)),
        };
        let Some(o) = json_obj(&value) else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let rowid = record
            .native_rowid
            .or_else(|| i64_field(o, "_rowid"))
            .or_else(|| i64_field(o, "id"));
        let Some(rowid) = rowid else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        // Completed facts emit once; append-rowid stream never re-emits.
        let native = TypedNativeKey::I64(rowid);
        let (occurred_at, time_source, is_native) =
            match resolve_record_time(o, state, record, &["timestamp", "time_created"]) {
                Ok(t) => t,
                Err(code) => return Ok(DecodeOutcome::Ignore(code)),
            };
        remember_source_time(state, occurred_at, is_native);
        let kind = str_field(o, "type").unwrap_or_default();
        let session = str_field(o, "sessionId");
        let turn = rowid.to_string();
        match kind.as_str() {
            "session" => Ok(DecodeOutcome::Emit(vec![
                super::common::emit_activity_fact(
                    &self.identity_secret,
                    HARNESS_ID,
                    logical_scope,
                    native,
                    "session_started",
                    occurred_at,
                    time_source,
                    session.as_deref().unwrap_or(logical_scope),
                    None,
                    json!({}),
                ),
            ])),
            "step_finish" => {
                let input = u64_field(o, "inputTokens").unwrap_or(0);
                let output = u64_field(o, "outputTokens").unwrap_or(0);
                let total = u64_field(o, "totalTokens").unwrap_or(input + output);
                let mut fact = emit_usage_fact(UsageFactArgs {
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
                    turn_id: Some(&turn),
                    skill_id: None,
                    skill_key: None,
                    model_key: 0,
                    cache_read_tokens: u64_field(o, "cacheReadTokens"),
                    reasoning_tokens: u64_field(o, "reasoningTokens"),
                });
                if let Some(write) = u64_field(o, "cacheWriteTokens") {
                    fact.payload_sections["usage"]["cache_write_tokens"] = json!(write);
                }
                let context = input
                    .checked_add(u64_field(o, "cacheReadTokens").unwrap_or(0))
                    .and_then(|v| v.checked_add(u64_field(o, "cacheWriteTokens").unwrap_or(0)))
                    .ok_or_else(|| RunnerError::DecodeBlocked("token count overflow".into()))?;
                fact.payload_sections["usage"]["input_context_tokens"] = json!(context);
                if let Some(model) = str_field(o, "model").filter(|s| !s.is_empty()) {
                    let provider = str_field(o, "provider").unwrap_or_else(|| HARNESS_ID.into());
                    if let Some(allocate) = &self.model_allocator {
                        fact.model_key = allocate(&provider, &model)?;
                    }
                    fact.model_identity = Some((provider, model));
                }
                fact.fact_revision = 2;
                fact.event_id = super::identity::event_id(&self.identity_secret, &fact.fact_key, 2);
                Ok(DecodeOutcome::Emit(vec![fact]))
            }
            "code_changed" => {
                if let Some(part) = o.get("part") {
                    if part
                        .get("tool")
                        .and_then(Value::as_str)
                        .is_some_and(|s| s.eq_ignore_ascii_case("skill"))
                    {
                        let status = part.pointer("/state/status").and_then(Value::as_str);
                        let success = match status {
                            Some("completed") => true,
                            Some("error" | "failed") => false,
                            _ => return Ok(DecodeOutcome::ContextOnly),
                        };
                        let name = part
                            .pointer("/state/input/skill")
                            .or_else(|| part.pointer("/state/input/name"))
                            .and_then(Value::as_str)
                            .filter(|s| !s.is_empty());
                        let Some(name) = name else {
                            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
                        };
                        let alloc = |key, name: &str| (self.skill_allocator)(key, name);
                        let mut fact = super::common::emit_skill_fact(
                            &self.identity_secret,
                            HARNESS_ID,
                            logical_scope,
                            native,
                            occurred_at,
                            time_source,
                            name,
                            &self.skill_book,
                            &alloc,
                            session.as_deref(),
                        );
                        fact.payload_sections = json!({"activity":{"success":success}});
                        return Ok(DecodeOutcome::Emit(vec![fact]));
                    }
                }
                let Some(code) = o
                    .get("part")
                    .and_then(super::common::completed_code_payload)
                else {
                    return Ok(DecodeOutcome::ContextOnly);
                };
                let mut fact = emit_code_fact(
                    &self.identity_secret,
                    HARNESS_ID,
                    logical_scope,
                    native,
                    occurred_at,
                    time_source,
                    session.as_deref(),
                    0,
                    0,
                );
                // Correction revision: old collectors emitted placeholder counts at revision 1.
                fact.fact_revision = 2;
                fact.event_id = super::identity::event_id(&self.identity_secret, &fact.fact_key, 2);
                fact.payload_sections = json!({"code": code});
                Ok(DecodeOutcome::Emit(vec![fact]))
            }
            _ => Ok(DecodeOutcome::Ignore(IgnoreCode::UnsupportedStructure)),
        }
    }

    fn native_identity(&self, _record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        NativeFactKey {
            fact_key: fact.fact_key,
            fact_revision: fact.fact_revision,
        }
    }
}
