//! Strategy surface for harness-specific discover / read / decode.

use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::budget::{DiscoveryBudget, ReadBudget};
use crate::local_store::pipeline::types::{CursorKind, EventCandidate, SourceKind};

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum RunnerError {
    Io(String),
    SourceChanged(String),
    DecodeBlocked(String),
    InvalidArgument(String),
    Pipeline(String),
    Budget,
}

impl std::fmt::Display for RunnerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Io(msg) => write!(f, "io: {msg}"),
            Self::SourceChanged(msg) => write!(f, "source_changed: {msg}"),
            Self::DecodeBlocked(msg) => write!(f, "decode_blocked: {msg}"),
            Self::InvalidArgument(msg) => write!(f, "invalid: {msg}"),
            Self::Pipeline(msg) => write!(f, "pipeline: {msg}"),
            Self::Budget => write!(f, "budget_exhausted"),
        }
    }
}

impl std::error::Error for RunnerError {}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TokenAccuracy {
    Exact,
    Derived,
}

impl TokenAccuracy {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Exact => "exact",
            Self::Derived => "derived",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum IgnoreCode {
    OutsideAdmissionDay,
    MissingEventTime,
    InvalidEventTime,
    MalformedRecord,
    UnsupportedStructure,
    EstimatedOnly,
    Other(String),
}

impl IgnoreCode {
    pub fn as_str(&self) -> &str {
        match self {
            Self::OutsideAdmissionDay => "outside_admission_day",
            Self::MissingEventTime => "missing_event_time",
            Self::InvalidEventTime => "invalid_event_time",
            Self::MalformedRecord => "malformed_record",
            Self::UnsupportedStructure => "unsupported_structure",
            Self::EstimatedOnly => "estimated_only",
            Self::Other(code) => code.as_str(),
        }
    }
}

#[derive(Debug, Clone)]
pub struct SourceSpec {
    pub harness_id: String,
    pub source_key: [u8; 32],
    pub source_kind: SourceKind,
    pub locator_ref: String,
    pub stream_key: String,
    pub cursor_kind: CursorKind,
    pub initial_cursor_json: Value,
    pub initial_decoder_state_json: Value,
    pub observed_boundary_json: Value,
}

#[derive(Debug, Clone)]
pub struct CheckpointView {
    pub cursor_json: Value,
    pub decoder_state_version: i64,
    pub decoder_state_json: Value,
    pub observed_boundary_json: Value,
    pub commit_seq: i64,
}

#[derive(Debug, Clone)]
pub struct RawRecord {
    pub ordinal: u64,
    pub byte_start: Option<u64>,
    pub byte_end: Option<u64>,
    pub native_rowid: Option<i64>,
    pub payload: Vec<u8>,
    pub file_mtime_ms: Option<i64>,
}

#[derive(Debug, Clone)]
pub struct RawBatch {
    pub records: Vec<RawRecord>,
    pub next_cursor_json: Value,
    pub next_observed_boundary_json: Value,
    /// Raw EOF / has_more decided by source boundary, never by decoded emit count.
    pub has_more: bool,
    pub bytes_read: usize,
    pub ignored_incomplete_tail: bool,
}

#[derive(Debug, Clone, Default)]
pub struct DecoderState {
    pub version: i64,
    pub json: Value,
}

#[derive(Debug, Clone)]
pub struct FactDraft {
    pub event_id: [u8; 32],
    pub fact_key: [u8; 32],
    pub fact_revision: i64,
    pub event_type: String,
    pub schema_version: i64,
    pub metric_semantics_version: i64,
    pub content_hash: [u8; 32],
    pub occurred_at: i64,
    pub time_source: super::admission::TimeSource,
    pub model_key: i64,
    pub skill_id: Option<i64>,
    pub session_key: Option<[u8; 32]>,
    pub turn_key: Option<[u8; 32]>,
    pub cost_scope_key: Option<[u8; 32]>,
    pub accuracy: TokenAccuracy,
    pub usage_json: Value,
}

impl FactDraft {
    pub fn into_event_candidate(
        self,
        applicable_consumers: Vec<crate::local_store::pipeline::types::Consumer>,
    ) -> EventCandidate {
        let payload = serde_json::json!({
            "meta": {
                "accuracy": self.accuracy.as_str(),
                "time_source": self.time_source.as_str(),
            },
            "usage": self.usage_json,
        });
        EventCandidate {
            event_id: self.event_id,
            fact_key: self.fact_key,
            fact_revision: self.fact_revision,
            event_type: self.event_type,
            schema_version: self.schema_version,
            metric_semantics_version: self.metric_semantics_version,
            content_hash: self.content_hash,
            occurred_at: self.occurred_at,
            model_key: self.model_key,
            skill_id: self.skill_id,
            session_key: self.session_key,
            turn_key: self.turn_key,
            cost_scope_key: self.cost_scope_key,
            payload_json: payload.to_string(),
            applicable_consumers,
        }
    }
}

#[derive(Debug, Clone)]
pub enum DecodeOutcome {
    Emit(Vec<FactDraft>),
    ContextOnly,
    Ignore(IgnoreCode),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct NativeFactKey {
    pub fact_key: [u8; 32],
    pub fact_revision: i64,
}

/// Harness-owned strategy. Public runner owns lease, budget, admission, CAS.
pub trait HarnessStrategy: Send + Sync {
    fn harness_id(&self) -> &str;

    fn discover(&self, budget: DiscoveryBudget) -> Result<Vec<SourceSpec>, RunnerError>;

    fn read(
        &self,
        locator_ref: &str,
        stream_key: &str,
        committed: &CheckpointView,
        budget: ReadBudget,
    ) -> Result<RawBatch, RunnerError>;

    fn decode(
        &self,
        record: &RawRecord,
        state: &mut DecoderState,
    ) -> Result<DecodeOutcome, RunnerError>;

    fn native_identity(&self, record: &RawRecord, fact: &FactDraft) -> NativeFactKey;
}
