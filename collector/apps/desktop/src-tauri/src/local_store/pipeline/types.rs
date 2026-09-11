//! Types for the v3 event-pipeline local store (P1).

use serde::{Deserialize, Serialize};

/// Consumer lanes that own independent status bits and processing tasks.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Consumer {
    Hour,
    Day,
    Month,
    Upload,
}

impl Consumer {
    pub const ALL: [Consumer; 4] = [
        Consumer::Hour,
        Consumer::Day,
        Consumer::Month,
        Consumer::Upload,
    ];

    pub fn as_str(self) -> &'static str {
        match self {
            Consumer::Hour => "hour",
            Consumer::Day => "day",
            Consumer::Month => "month",
            Consumer::Upload => "upload",
        }
    }

    pub fn parse(raw: &str) -> Result<Self, String> {
        match raw {
            "hour" => Ok(Self::Hour),
            "day" => Ok(Self::Day),
            "month" => Ok(Self::Month),
            "upload" => Ok(Self::Upload),
            other => Err(format!("unknown consumer {other}")),
        }
    }

    pub fn status_path(self) -> &'static str {
        match self {
            Consumer::Hour => "$.hour",
            Consumer::Day => "$.day",
            Consumer::Month => "$.month",
            Consumer::Upload => "$.upload",
        }
    }
}

/// status_json lane values (0–6).
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum ConsumerStatus {
    Pending = 0,
    Retry = 1,
    InFlight = 2,
    Applied = 3,
    NotApplicable = 4,
    Blocked = 5,
    Quarantined = 6,
}

impl ConsumerStatus {
    pub fn from_i64(value: i64) -> Result<Self, String> {
        match value {
            0 => Ok(Self::Pending),
            1 => Ok(Self::Retry),
            2 => Ok(Self::InFlight),
            3 => Ok(Self::Applied),
            4 => Ok(Self::NotApplicable),
            5 => Ok(Self::Blocked),
            6 => Ok(Self::Quarantined),
            other => Err(format!("invalid consumer status {other}")),
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SourceKind {
    Jsonl,
    Sqlite,
    Other,
}

impl SourceKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Jsonl => "jsonl",
            Self::Sqlite => "sqlite",
            Self::Other => "other",
        }
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CursorKind {
    ByteOffset,
    SqliteChange,
    Opaque,
}

impl CursorKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::ByteOffset => "byte_offset",
            Self::SqliteChange => "sqlite_change",
            Self::Opaque => "opaque",
        }
    }
}

#[derive(Debug, Clone)]
pub struct RegisterSource {
    pub harness_id: String,
    pub source_key: [u8; 32],
    pub source_kind: SourceKind,
    pub locator_ref: String,
    pub stream_key: String,
    pub cursor_kind: CursorKind,
    pub cursor_json: String,
    pub decoder_state_version: i64,
    pub decoder_state_json: String,
    pub observed_boundary_json: String,
    pub next_poll_at: Option<i64>,
}

/// Committed source checkpoint copied under lease for out-of-transaction I/O.
#[derive(Debug, Clone)]
pub struct SourceCheckpointSnapshot {
    pub source_id: i64,
    pub harness_id: String,
    pub source_kind: SourceKind,
    pub locator_ref: String,
    pub stream_key: String,
    pub cursor_kind: CursorKind,
    pub cursor_json: String,
    pub decoder_state_version: i64,
    pub decoder_state_json: String,
    pub observed_boundary_json: String,
    pub commit_seq: i64,
    pub lease_token: Option<String>,
    pub lease_until: Option<i64>,
    pub ignored_record_count: i64,
    pub last_ignored_code: Option<String>,
    pub next_poll_at: Option<i64>,
    pub enabled: bool,
}

#[derive(Debug, Clone)]
pub struct EventCandidate {
    pub event_id: [u8; 32],
    pub fact_key: [u8; 32],
    pub fact_revision: i64,
    pub event_type: String,
    pub schema_version: i64,
    pub metric_semantics_version: i64,
    pub content_hash: [u8; 32],
    pub occurred_at: i64,
    /// Local model_dimensions.id; 0 = unknown.
    pub model_key: i64,
    pub skill_id: Option<i64>,
    pub session_key: Option<[u8; 32]>,
    pub turn_key: Option<[u8; 32]>,
    pub cost_scope_key: Option<[u8; 32]>,
    /// Must satisfy events.payload_json CHECK constraints.
    pub payload_json: String,
    /// Consumers that receive a processing task; others are marked not_applicable.
    pub applicable_consumers: Vec<Consumer>,
}

#[derive(Debug, Clone)]
pub struct SourceCommitBatch {
    pub source_id: i64,
    pub expected_commit_seq: i64,
    pub lease_token: String,
    pub cursor_json: String,
    pub decoder_state_version: i64,
    pub decoder_state_json: String,
    pub observed_boundary_json: String,
    pub ignored_record_count_delta: i64,
    pub last_ignored_code: Option<String>,
    pub next_poll_at: Option<i64>,
    pub events: Vec<EventCandidate>,
    /// Optional override for created_at of newly inserted events (tests / clock).
    pub created_at_override: Option<i64>,
}

#[derive(Debug, Clone, Default)]
pub struct SourceCommitResult {
    pub commit_seq: i64,
    pub inserted_events: usize,
    pub duplicate_events: usize,
}

#[derive(Debug, Clone)]
pub struct LeasedTask {
    pub task_id: i64,
    pub event_row_id: i64,
    pub consumer: Consumer,
    pub lease_token: String,
    pub lease_until: i64,
    pub attempt_count: i64,
}

#[derive(Debug, Clone)]
pub struct TaskComplete {
    pub task_id: i64,
    pub event_row_id: i64,
    pub consumer: Consumer,
    pub lease_token: String,
    /// Final status: Applied (3), NotApplicable (4), Blocked (5), Quarantined (6).
    pub status: ConsumerStatus,
    pub error_code: Option<String>,
}

#[derive(Debug, Clone)]
pub struct TaskRetry {
    pub task_id: i64,
    pub event_row_id: i64,
    pub consumer: Consumer,
    pub lease_token: String,
    pub runnable_at: i64,
    pub error_code: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum PipelineError {
    NotInitialized,
    AlreadyInitialized,
    BatchTooLarge { events: usize, bytes: usize },
    WriterBackpressure,
    SourceNotFound(i64),
    SourceLeaseMismatch,
    SourceCommitSeqMismatch { expected: i64, actual: i64 },
    SourceDisabledOrDeleted,
    IdentityContentConflict {
        fact_key: [u8; 32],
        fact_revision: i64,
    },
    TaskLeaseMismatch,
    TaskNotRunnable,
    EventExpiredOrDeleted,
    InvalidArgument(String),
    Sqlite(String),
}

impl std::fmt::Display for PipelineError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotInitialized => write!(f, "event pipeline store is not initialized"),
            Self::AlreadyInitialized => write!(f, "event pipeline store already initialized"),
            Self::BatchTooLarge { events, bytes } => {
                write!(f, "batch too large: {events} events / {bytes} bytes")
            }
            Self::WriterBackpressure => write!(f, "writer channel backpressure"),
            Self::SourceNotFound(id) => write!(f, "collection source {id} not found"),
            Self::SourceLeaseMismatch => write!(f, "source lease_token mismatch"),
            Self::SourceCommitSeqMismatch { expected, actual } => {
                write!(f, "source commit_seq mismatch: expected {expected}, actual {actual}")
            }
            Self::SourceDisabledOrDeleted => write!(f, "source disabled or deleted"),
            Self::IdentityContentConflict {
                fact_key,
                fact_revision,
            } => write!(
                f,
                "identity_content_conflict fact_revision={fact_revision} fact_key={}",
                hex::encode(fact_key)
            ),
            Self::TaskLeaseMismatch => write!(f, "task lease_token mismatch"),
            Self::TaskNotRunnable => write!(f, "task not runnable under current lease/status"),
            Self::EventExpiredOrDeleted => write!(f, "event expired or soft-deleted"),
            Self::InvalidArgument(msg) => write!(f, "invalid argument: {msg}"),
            Self::Sqlite(msg) => write!(f, "sqlite: {msg}"),
        }
    }
}

impl std::error::Error for PipelineError {}

impl From<rusqlite::Error> for PipelineError {
    fn from(value: rusqlite::Error) -> Self {
        Self::Sqlite(value.to_string())
    }
}

/// Tiny hex helper so we do not pull an extra crate for error formatting.
mod hex {
    pub fn encode(bytes: &[u8]) -> String {
        const HEX: &[u8; 16] = b"0123456789abcdef";
        let mut out = String::with_capacity(bytes.len() * 2);
        for b in bytes {
            out.push(HEX[(b >> 4) as usize] as char);
            out.push(HEX[(b & 0xf) as usize] as char);
        }
        out
    }
}

pub const MAX_BATCH_EVENTS: usize = 256;
pub const MAX_BATCH_BYTES: usize = 2 * 1024 * 1024;
pub const WRITER_QUEUE_BATCHES: usize = 16;
pub const WRITER_QUEUE_BYTES: usize = 32 * 1024 * 1024;
pub const DEFAULT_LEASE_MS: i64 = 30_000;
pub const COMPENSATION_INTERVAL_MS: i64 = 5_000;
pub const EVENT_TTL_MS: i64 = 1_209_600_000; // 14 days
pub const DB_FILE: &str = "tokendance-events.sqlite3";
pub const PIPELINE_SCHEMA_VERSION: i64 = 3;
