//! ZCode SQLite harness: numeric native IDs, per-query streams, running→completed.

use std::path::{Path, PathBuf};
use std::sync::Arc;

use serde_json::{json, Value};

use super::common::{
    emit_code_fact, emit_skill_fact, emit_usage_fact, i64_field, json_obj, remember_source_time, resolve_record_time,
    str_field, u64_field, SkillBook, UsageFactArgs,
};
use super::identity::{source_key, TypedNativeKey};
use crate::local_store::pipeline::runner::{
    cursor_from_parts, read_sqlite_change_stream, CheckpointView, DecodeOutcome, DecoderState,
    DiscoveryBudget, FactDraft, HarnessStrategy, IgnoreCode, NativeFactKey, PendingSet, RawBatch,
    RawRecord, ReadBudget, RunnerError, SourceSpec, SqliteChangeMode, TokenAccuracy,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

pub const HARNESS_ID: &str = "zcode";
pub const STREAM_SESSION: &str = "sqlite/session";
pub const STREAM_USAGE: &str = "sqlite/model_usage";
pub const STREAM_CODE: &str = "sqlite/code_part";

/// Fixture / v3-compatible projection: columns rowid, updated_at, status, payload_json text.
pub const SQL_SESSION: &str =
    "SELECT rowid, time_created AS updated_at, 'completed' AS status, \
     json_object('type','session','id',rowid,'sessionId',id,'timestamp',time_created) \
     FROM session WHERE rowid > ?1 ORDER BY rowid";

pub const SQL_USAGE: &str =
    "SELECT rowid, COALESCE(completed_at, 0) AS updated_at, status, \
     json_object('type','step_finish','id',rowid,'sessionId',session_id, \
       'provider',provider_id,'model',model_id,'inputTokens',input_tokens, \
       'outputTokens',output_tokens,'totalTokens',computed_total_tokens, \
       'timestamp',COALESCE(completed_at, 0),'status',status) \
     FROM model_usage WHERE rowid > ?1 ORDER BY rowid";

pub const SQL_CODE: &str =
    "SELECT rowid, time_updated AS updated_at, 'completed' AS status, \
     json_object('type','code_changed','id',rowid,'sessionId',session_id, \
       'timestamp',time_updated,'callId',json_extract(data,'$.callID'), \
       'addedLines',1,'removedLines',0,'fileCount',1) \
     FROM part WHERE (time_updated > ?1) OR (time_updated = ?1 AND rowid > ?2) \
     ORDER BY time_updated, rowid";

pub struct ZcodeStrategy {
    pub identity_secret: Vec<u8>,
    pub db_path: PathBuf,
    pub skill_book: SkillBook,
    pub skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
}

impl ZcodeStrategy {
    pub fn new(
        identity_secret: impl Into<Vec<u8>>,
        db_path: impl Into<PathBuf>,
        skill_book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        Self {
            identity_secret: identity_secret.into(),
            db_path: db_path.into(),
            skill_book,
            skill_allocator,
        }
    }

    fn stream_mode(stream_key: &str) -> Option<(SqliteChangeMode, &'static str)> {
        match stream_key {
            STREAM_SESSION => Some((SqliteChangeMode::AppendRowid, SQL_SESSION)),
            STREAM_USAGE => Some((SqliteChangeMode::RunningToCompleted, SQL_USAGE)),
            STREAM_CODE => Some((SqliteChangeMode::UpdatedAtRowid, SQL_CODE)),
            _ => None,
        }
    }
}

impl HarnessStrategy for ZcodeStrategy {
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
        Ok([STREAM_SESSION, STREAM_USAGE, STREAM_CODE]
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
                "unknown zcode stream {stream_key}"
            )));
        };
        let result =
            read_sqlite_change_stream(Path::new(locator_ref), sql, &committed.cursor_json, mode, budget)?;

        let records = result
            .rows
            .into_iter()
            .enumerate()
            .map(|(i, row)| {
                let mut payload = row.payload_json;
                if let Some(obj) = payload.as_object_mut() {
                    obj.insert("_rowid".into(), json!(row.rowid));
                    if let Some(st) = row.status.clone() {
                        obj.insert("_status".into(), json!(st));
                    }
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
            has_more: result.has_more || result.discovery_paused,
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

        let status = str_field(o, "_status").or_else(|| str_field(o, "status"));
        if matches!(status.as_deref(), Some("running") | Some("pending")) {
            return Ok(DecodeOutcome::ContextOnly);
        }

        let rowid = record
            .native_rowid
            .or_else(|| i64_field(o, "_rowid"))
            .or_else(|| i64_field(o, "id"));
        let Some(rowid) = rowid else {
            return Ok(DecodeOutcome::Ignore(IgnoreCode::MalformedRecord));
        };
        let native = TypedNativeKey::I64(rowid);

        let (occurred_at, time_source, is_native) = match resolve_record_time(
            o,
            state,
            record,
            &["timestamp", "completed_at", "time_created"],
        ) {
            Ok(t) => t,
            Err(code) => return Ok(DecodeOutcome::Ignore(code)),
        };
        remember_source_time(state, occurred_at, is_native);

        let kind = str_field(o, "type").unwrap_or_default();
        let session = str_field(o, "sessionId").or_else(|| str_field(o, "session_id"));
        let turn = rowid.to_string();

        match kind.as_str() {
            "session" => Ok(DecodeOutcome::ContextOnly),
            "step_finish" | "model_usage" => {
                let input = u64_field(o, "inputTokens").unwrap_or(0);
                let output = u64_field(o, "outputTokens").unwrap_or(0);
                let total = u64_field(o, "totalTokens").unwrap_or(input + output);
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
                    turn_id: Some(&turn),
                    skill_id: None,
                    skill_key: None,
                    model_key: 0,
                    cache_read_tokens: None,
                    reasoning_tokens: None,
                })]))
            }
            "skill" => {
                let name = str_field(o, "skillName").unwrap_or_else(|| "zcode-skill".into());
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
            "code_changed" => Ok(DecodeOutcome::Emit(vec![emit_code_fact(
                &self.identity_secret,
                HARNESS_ID,
                logical_scope,
                native,
                occurred_at,
                time_source,
                session.as_deref(),
                u64_field(o, "addedLines").unwrap_or(0),
                u64_field(o, "removedLines").unwrap_or(0),
            )])),
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
