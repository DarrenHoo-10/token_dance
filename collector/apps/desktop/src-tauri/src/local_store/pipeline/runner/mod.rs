//! P2 public acquisition runner: lease → bounded read/decode outside writer → CAS.
//!
//! Harness adapters only supply strategy differences; I/O never runs under the
//! single-writer lock.

mod admission;
mod budget;
mod engine;
mod jsonl;
mod metrics;
mod scheduler;
mod sqlite_stream;
mod strategy;

#[cfg(test)]
mod tests;

pub use admission::{
    admit_occurred_at, beijing_day_key, beijing_wall_to_utc_ms, resolve_event_time,
    AdmissionDecision, ResolvedTime, TimeSource,
};
pub use budget::{
    DiscoveryBudget, ReadBudget, DEFAULT_GLOBAL_ACQUISITION_CONCURRENCY,
    DEFAULT_PER_HARNESS_CONCURRENCY, DEFAULT_PENDING_SET_LIMIT, DEFAULT_READ_BUDGET,
};
pub use engine::{
    raw_record_from_bytes, run_source_once, AcquisitionRunner, RunOutcome, RunStats,
    SourceCommitSink, StoreSinkMut,
};
pub use jsonl::{
    boundary_with_len, cursor_offset, cursor_with_offset, read_jsonl_budgeted, JsonlReadResult,
    JsonlRecord, SourceChange,
};
pub use metrics::AcquisitionMetrics;
pub use scheduler::AcquisitionScheduler;
pub use sqlite_stream::{
    cursor_from_parts, read_sqlite_change_stream, CompositeCursor, PendingSet, SqliteChangeMode,
    SqliteReadResult, SqliteRow, DEFAULT_SQLITE_PENDING_LIMIT,
};
pub use strategy::{
    CheckpointView, DecodeOutcome, DecoderState, FactDraft, HarnessStrategy, IgnoreCode,
    NativeFactKey, RawBatch, RawRecord, RunnerError, SourceSpec, TokenAccuracy,
};
