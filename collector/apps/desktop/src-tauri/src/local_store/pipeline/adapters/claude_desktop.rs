//! Claude Desktop Cowork / Code IndexedDB source for the v3 pipeline.
//! The store decoder yields only completed usage fields; the shared Claude
//! JSONL strategy normalizes them into the same exact-token facts as CLI Code.

use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::time::{SystemTime, UNIX_EPOCH};

use serde_json::json;
use sha2::{Digest, Sha256};

use super::claude_desktop_store;
use super::common::SkillBook;
use super::identity::source_key;
use super::jsonl_harness::{JsonlHarnessStrategy, CLAUDE};
use crate::local_store::pipeline::runner::{
    CheckpointView, DecodeOutcome, DecoderState, DiscoveryBudget, FactDraft, HarnessStrategy,
    ModelAllocator, NativeFactKey, RawBatch, RawRecord, ReadBudget, RunnerError, SourceSpec,
};
use crate::local_store::pipeline::types::{CursorKind, SourceKind};

const STREAM: &str = "desktop-indexeddb";

pub struct ClaudeDesktopStrategy {
    inner: JsonlHarnessStrategy,
    leveldb: PathBuf,
    blobs: PathBuf,
}

impl ClaudeDesktopStrategy {
    pub fn new(
        secret: impl Into<Vec<u8>>,
        leveldb: PathBuf,
        book: SkillBook,
        skill_allocator: Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync>,
    ) -> Self {
        let name = leveldb
            .file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("");
        let blobs = leveldb.with_file_name(format!(
            "{}blob",
            name.strip_suffix("leveldb").unwrap_or(name)
        ));
        Self {
            inner: JsonlHarnessStrategy::new(CLAUDE, secret, &leveldb, book, skill_allocator),
            leveldb,
            blobs,
        }
    }
}

impl HarnessStrategy for ClaudeDesktopStrategy {
    fn set_model_allocator(&mut self, allocator: ModelAllocator) {
        self.inner.set_model_allocator(allocator);
    }

    fn harness_id(&self) -> &str {
        CLAUDE.harness_id
    }

    fn owns_source(&self, locator_ref: &str) -> bool {
        self.leveldb == Path::new(locator_ref)
    }

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError> {
        if budget.max_sources == 0 || !self.leveldb.is_dir() {
            return Ok(Vec::new());
        }
        let scope = self.leveldb.to_string_lossy().into_owned();
        Ok(vec![SourceSpec {
            harness_id: CLAUDE.harness_id.into(),
            source_key: source_key(&self.inner.identity_secret, CLAUDE.harness_id, &scope),
            source_kind: SourceKind::Other,
            locator_ref: scope,
            stream_key: STREAM.into(),
            cursor_kind: CursorKind::Opaque,
            initial_cursor_json: json!({"hash":"", "index":0}),
            initial_decoder_state_json: json!({"last_source_time":null}),
            observed_boundary_json: json!({"hash":"", "rows":0}),
        }])
    }

    fn read(
        &self,
        locator_ref: &str,
        stream_key: &str,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError> {
        if stream_key != STREAM || !self.owns_source(locator_ref) {
            return Err(RunnerError::InvalidArgument(
                "claude_desktop_source_mismatch".into(),
            ));
        }
        if budget.max_records == 0 || budget.max_bytes == 0 {
            return Err(RunnerError::Budget);
        }
        let now_ms = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|duration| duration.as_millis() as i64)
            .unwrap_or(0);
        let payload = claude_desktop_store::sanitized_snapshot(&self.leveldb, &self.blobs, now_ms)
            .map_err(RunnerError::Io)?;
        let digest = Sha256::digest(&payload);
        let hash = format!("{digest:x}");
        let lines = payload
            .split(|byte| *byte == b'\n')
            .filter(|line| !line.is_empty())
            .collect::<Vec<_>>();
        let start = if committed.cursor_json["hash"].as_str() == Some(hash.as_str()) {
            committed.cursor_json["index"].as_u64().unwrap_or(0) as usize
        } else {
            0
        };
        let mut records = Vec::new();
        let mut bytes_read = 0usize;
        for (index, line) in lines.iter().enumerate().skip(start) {
            if records.len() >= budget.max_records
                || bytes_read.saturating_add(line.len()) > budget.max_bytes
            {
                break;
            }
            bytes_read += line.len();
            records.push(RawRecord {
                ordinal: index as u64 + 1,
                byte_start: None,
                byte_end: None,
                native_rowid: None,
                payload: line.to_vec(),
                file_mtime_ms: None,
            });
        }
        if records.is_empty() && start < lines.len() {
            return Err(RunnerError::Budget);
        }
        let next = start.saturating_add(records.len()).min(lines.len());
        Ok(RawBatch {
            records,
            next_cursor_json: json!({"hash":hash, "index":next}),
            next_observed_boundary_json: json!({"hash":hash, "rows":lines.len()}),
            has_more: next < lines.len(),
            bytes_read,
            ignored_incomplete_tail: false,
        })
    }

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
        logical_scope: &str,
    ) -> Result<DecodeOutcome, RunnerError> {
        self.inner.decode(record, state, logical_scope)
    }

    fn native_identity(&self, record: &RawRecord, fact: &FactDraft) -> NativeFactKey {
        self.inner.native_identity(record, fact)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sanitized_desktop_usage_becomes_exact_claude_token_fact() {
        let strategy = ClaudeDesktopStrategy::new(
            b"desktop-fixture".to_vec(),
            PathBuf::from("/unused/claude.indexeddb.leveldb"),
            SkillBook::new(),
            Arc::new(|_, _| 1),
        );
        let mut state = DecoderState::default();
        let record = RawRecord {
            ordinal: 1,
            byte_start: None,
            byte_end: None,
            native_rowid: None,
            payload: json!({
                "type": "assistant", "uuid": "message-1", "sessionId": "session-1",
                "timestamp": "2026-09-27T12:00:00Z",
                "message": {"id": "message-1", "model": "claude-sonnet-4-5",
                    "usage": {"input_tokens": 2, "output_tokens": 3,
                        "cache_read_input_tokens": 11,
                        "cache_creation_input_tokens": 5}}
            })
            .to_string()
            .into_bytes(),
            file_mtime_ms: None,
        };
        let DecodeOutcome::Emit(facts) = strategy
            .decode(&record, &mut state, "/unused/claude.indexeddb.leveldb")
            .unwrap()
        else {
            panic!("expected exact usage");
        };
        let usage = facts
            .iter()
            .find(|fact| fact.event_type == "model_usage_recorded")
            .unwrap();
        assert_eq!(usage.accuracy, crate::local_store::pipeline::runner::TokenAccuracy::Exact);
        assert_eq!(usage.payload_sections["usage"]["token_total"], 21);
        assert_eq!(usage.payload_sections["usage"]["cache_read_tokens"], 11);
        assert_eq!(usage.model_identity.as_ref().unwrap().1, "claude-sonnet-4-5");
    }
}
