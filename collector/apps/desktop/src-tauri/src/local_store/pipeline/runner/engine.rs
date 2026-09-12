//! Public acquisition runner: lease → copy checkpoint → I/O outside writer → CAS.

use std::cell::RefCell;
use std::sync::Arc;
use std::time::Instant;

use serde_json::Value;

use super::admission::{admit_occurred_at, AdmissionDecision};
use super::budget::ReadBudget;
use super::metrics::AcquisitionMetrics;
use super::strategy::{
    CheckpointView, DecodeOutcome, DecoderState, HarnessStrategy, IgnoreCode, RawRecord,
    RunnerError,
};
use crate::local_store::pipeline::types::{
    Consumer, PipelineError, SourceCheckpointSnapshot, SourceCommitBatch, SourceCommitResult,
    DEFAULT_LEASE_MS,
};
use crate::local_store::pipeline::{PipelineStore, PipelineWriter};

/// Sink that commits a source batch (store direct or writer channel).
pub trait SourceCommitSink {
    fn lease_source(
        &self,
        source_id: i64,
        lease_ms: i64,
    ) -> Result<(String, i64, i64), PipelineError>;

    fn load_source_checkpoint(
        &self,
        source_id: i64,
    ) -> Result<SourceCheckpointSnapshot, PipelineError>;

    fn commit_source(&self, batch: SourceCommitBatch) -> Result<SourceCommitResult, PipelineError>;

    fn now_ms(&self) -> i64;
}

impl SourceCommitSink for PipelineWriter {
    fn lease_source(
        &self,
        source_id: i64,
        lease_ms: i64,
    ) -> Result<(String, i64, i64), PipelineError> {
        PipelineWriter::lease_source(self, source_id, lease_ms)
    }

    fn load_source_checkpoint(
        &self,
        source_id: i64,
    ) -> Result<SourceCheckpointSnapshot, PipelineError> {
        PipelineWriter::load_source_checkpoint(self, source_id)
    }

    fn commit_source(&self, batch: SourceCommitBatch) -> Result<SourceCommitResult, PipelineError> {
        PipelineWriter::commit_source(self, batch)
    }

    fn now_ms(&self) -> i64 {
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|d| d.as_millis() as i64)
            .unwrap_or(0)
    }
}

/// Mutable store adapter used by tests and single-threaded callers.
pub struct StoreSinkMut<'a> {
    store: RefCell<&'a mut PipelineStore>,
}

impl<'a> StoreSinkMut<'a> {
    pub fn new(store: &'a mut PipelineStore) -> Self {
        Self {
            store: RefCell::new(store),
        }
    }
}

impl SourceCommitSink for StoreSinkMut<'_> {
    fn lease_source(
        &self,
        source_id: i64,
        lease_ms: i64,
    ) -> Result<(String, i64, i64), PipelineError> {
        self.store.borrow_mut().lease_source(source_id, lease_ms)
    }

    fn load_source_checkpoint(
        &self,
        source_id: i64,
    ) -> Result<SourceCheckpointSnapshot, PipelineError> {
        self.store.borrow().load_source_checkpoint(source_id)
    }

    fn commit_source(&self, batch: SourceCommitBatch) -> Result<SourceCommitResult, PipelineError> {
        self.store.borrow_mut().commit_source(batch)
    }

    fn now_ms(&self) -> i64 {
        self.store.borrow().now_ms()
    }
}

#[derive(Debug, Clone, Default)]
pub struct RunStats {
    pub records_read: usize,
    pub bytes_read: usize,
    pub emitted: usize,
    pub ignored: usize,
    pub context_only: usize,
    pub has_more: bool,
    pub cursor_advanced: bool,
    pub commit_seq: Option<i64>,
    pub last_ignored_code: Option<String>,
    pub checkpoint_delay_ms: u64,
}

#[derive(Debug, Clone)]
pub enum RunOutcome {
    Committed(RunStats),
    /// CAS/lease failed: last committed checkpoint retained.
    CasRejected {
        error: PipelineError,
        stats: RunStats,
    },
    /// Source changed / decode blocked: stream paused without advancing cursor.
    StreamStopped {
        reason: RunnerError,
        stats: RunStats,
    },
    Empty(RunStats),
}

pub struct AcquisitionRunner {
    pub metrics: Arc<AcquisitionMetrics>,
    pub lease_ms: i64,
    pub default_consumers: Vec<Consumer>,
}

impl Default for AcquisitionRunner {
    fn default() -> Self {
        Self {
            metrics: Arc::new(AcquisitionMetrics::default()),
            lease_ms: DEFAULT_LEASE_MS,
            default_consumers: Consumer::ALL.to_vec(),
        }
    }
}

impl AcquisitionRunner {
    pub fn run_once(
        &self,
        sink: &dyn SourceCommitSink,
        strategy: &dyn HarnessStrategy,
        source_id: i64,
        budget: ReadBudget,
    ) -> Result<RunOutcome, RunnerError> {
        run_source_once(
            sink,
            strategy,
            source_id,
            budget,
            self.lease_ms,
            &self.default_consumers,
            Some(self.metrics.as_ref()),
        )
    }
}

pub fn run_source_once(
    sink: &dyn SourceCommitSink,
    strategy: &dyn HarnessStrategy,
    source_id: i64,
    budget: ReadBudget,
    lease_ms: i64,
    applicable_consumers: &[Consumer],
    metrics: Option<&AcquisitionMetrics>,
) -> Result<RunOutcome, RunnerError> {
    let lease_started = Instant::now();
    let (lease_token, _lease_until, commit_seq) = sink
        .lease_source(source_id, lease_ms)
        .map_err(|e| RunnerError::Pipeline(e.to_string()))?;

    let snapshot = sink
        .load_source_checkpoint(source_id)
        .map_err(|e| RunnerError::Pipeline(e.to_string()))?;

    if snapshot.harness_id != strategy.harness_id() {
        return Err(RunnerError::InvalidArgument(format!(
            "harness mismatch: source={} strategy={}",
            snapshot.harness_id,
            strategy.harness_id()
        )));
    }

    let committed = CheckpointView {
        cursor_json: parse_obj(&snapshot.cursor_json)?,
        decoder_state_version: snapshot.decoder_state_version,
        decoder_state_json: parse_obj(&snapshot.decoder_state_json)?,
        observed_boundary_json: parse_obj(&snapshot.observed_boundary_json)?,
        commit_seq,
    };

    let rebuilding = committed.observed_boundary_json["_rebuild_pending"].as_bool() == Some(true);

    // Source I/O and decode: no writer / store write transaction held.
    let batch = match strategy.read(
        &snapshot.locator_ref,
        &snapshot.stream_key,
        &committed,
        budget,
    ) {
        Ok(batch) => batch,
        Err(RunnerError::SourceChanged(msg)) => {
            if let Some(m) = metrics {
                m.record_source_changed();
            }
            let stats = RunStats::default();
            let _ = try_release_lease_unchanged(
                sink,
                &snapshot,
                &lease_token,
                commit_seq,
                sink.now_ms(),
            );
            return Ok(RunOutcome::StreamStopped {
                reason: RunnerError::SourceChanged(msg),
                stats,
            });
        }
        Err(err) => {
            let _ = try_release_lease_unchanged(
                sink,
                &snapshot,
                &lease_token,
                commit_seq,
                sink.now_ms(),
            );
            return Err(err);
        }
    };

    if let Some(m) = metrics {
        m.record_read(batch.bytes_read, batch.records.len());
    }

    let mut decoder = DecoderState {
        version: committed.decoder_state_version,
        json: committed.decoder_state_json.clone(),
    };

    let mut events = Vec::new();
    let mut ignored = 0usize;
    let mut context_only = 0usize;
    let mut last_ignored: Option<String> = None;
    let admission_now = sink.now_ms();

    for record in &batch.records {
        let outcome = match strategy.decode(record, &mut decoder, &snapshot.locator_ref) {
            Ok(o) => o,
            Err(RunnerError::DecodeBlocked(msg)) => {
                let _ = try_release_lease_unchanged(
                    sink,
                    &snapshot,
                    &lease_token,
                    commit_seq,
                    admission_now,
                );
                return Ok(RunOutcome::StreamStopped {
                    reason: RunnerError::DecodeBlocked(msg),
                    stats: RunStats {
                        records_read: batch.records.len(),
                        bytes_read: batch.bytes_read,
                        emitted: events.len(),
                        ignored,
                        context_only,
                        has_more: batch.has_more,
                        cursor_advanced: batch.next_cursor_json != committed.cursor_json,
                        commit_seq: None,
                        last_ignored_code: last_ignored,
                        checkpoint_delay_ms: 0,
                    },
                });
            }
            Err(err) => {
                let _ = try_release_lease_unchanged(
                    sink,
                    &snapshot,
                    &lease_token,
                    commit_seq,
                    admission_now,
                );
                return Err(err);
            }
        };

        match outcome {
            DecodeOutcome::ContextOnly => context_only += 1,
            DecodeOutcome::Ignore(code) => {
                ignored += 1;
                last_ignored = Some(code.as_str().to_string());
            }
            DecodeOutcome::Emit(facts) => {
                for fact in facts {
                    let _native = strategy.native_identity(record, &fact);
                    match if rebuilding {
                        AdmissionDecision::Admit
                    } else {
                        admit_occurred_at(fact.occurred_at, admission_now)
                    } {
                        AdmissionDecision::Admit => {
                            if fact.occurred_at <= 0 {
                                ignored += 1;
                                last_ignored =
                                    Some(IgnoreCode::InvalidEventTime.as_str().to_string());
                                continue;
                            }
                            match fact.into_event_candidate(
                                strategy.harness_id(),
                                applicable_consumers.to_vec(),
                            ) {
                                Ok(candidate) => events.push(candidate),
                                Err(msg) => {
                                    let _ = try_release_lease_unchanged(
                                        sink,
                                        &snapshot,
                                        &lease_token,
                                        commit_seq,
                                        admission_now,
                                    );
                                    return Ok(RunOutcome::StreamStopped {
                                        reason: RunnerError::DecodeBlocked(msg),
                                        stats: RunStats {
                                            records_read: batch.records.len(),
                                            bytes_read: batch.bytes_read,
                                            emitted: events.len(),
                                            ignored,
                                            context_only,
                                            has_more: batch.has_more,
                                            cursor_advanced: batch.next_cursor_json
                                                != committed.cursor_json,
                                            commit_seq: None,
                                            last_ignored_code: last_ignored,
                                            checkpoint_delay_ms: 0,
                                        },
                                    });
                                }
                            }
                        }
                        AdmissionDecision::IgnoreOutsideDay => {
                            ignored += 1;
                            last_ignored =
                                Some(IgnoreCode::OutsideAdmissionDay.as_str().to_string());
                        }
                    }
                }
            }
        }
    }

    if let Some(m) = metrics {
        m.record_emit(events.len());
        m.record_ignore(ignored);
    }

    let mut stats = RunStats {
        records_read: batch.records.len(),
        bytes_read: batch.bytes_read,
        emitted: events.len(),
        ignored,
        context_only,
        has_more: batch.has_more,
        cursor_advanced: batch.next_cursor_json != committed.cursor_json,
        commit_seq: None,
        last_ignored_code: last_ignored.clone(),
        checkpoint_delay_ms: lease_started.elapsed().as_millis() as u64,
    };

    let mut next_boundary = batch.next_observed_boundary_json.clone();
    if rebuilding {
        next_boundary["_rebuild_pending"] =
            serde_json::json!(batch.has_more && !batch.ignored_incomplete_tail);
    }
    let commit = SourceCommitBatch {
        source_id,
        expected_commit_seq: commit_seq,
        lease_token,
        cursor_json: batch.next_cursor_json.to_string(),
        decoder_state_version: decoder.version.max(1),
        decoder_state_json: decoder.json.to_string(),
        observed_boundary_json: next_boundary.to_string(),
        ignored_record_count_delta: ignored as i64,
        last_ignored_code: last_ignored,
        next_poll_at: if batch.has_more && batch.next_cursor_json != committed.cursor_json {
            Some(admission_now)
        } else {
            Some(admission_now + 5_000)
        },
        events,
        created_at_override: Some(admission_now),
    };

    match sink.commit_source(commit) {
        Ok(result) => {
            stats.commit_seq = Some(result.commit_seq);
            if let Some(m) = metrics {
                m.record_checkpoint_delay_ms(stats.checkpoint_delay_ms);
            }
            if stats.emitted == 0 && stats.records_read == 0 && !stats.has_more {
                Ok(RunOutcome::Empty(stats))
            } else {
                Ok(RunOutcome::Committed(stats))
            }
        }
        Err(error) => {
            if matches!(
                error,
                PipelineError::SourceCommitSeqMismatch { .. }
                    | PipelineError::SourceLeaseMismatch
                    | PipelineError::IdentityContentConflict { .. }
            ) {
                if let Some(m) = metrics {
                    m.record_conflict();
                }
                Ok(RunOutcome::CasRejected { error, stats })
            } else {
                Err(RunnerError::Pipeline(error.to_string()))
            }
        }
    }
}

fn try_release_lease_unchanged(
    sink: &dyn SourceCommitSink,
    snapshot: &SourceCheckpointSnapshot,
    lease_token: &str,
    commit_seq: i64,
    now_ms: i64,
) -> Result<SourceCommitResult, PipelineError> {
    sink.commit_source(SourceCommitBatch {
        source_id: snapshot.source_id,
        expected_commit_seq: commit_seq,
        lease_token: lease_token.to_string(),
        cursor_json: snapshot.cursor_json.clone(),
        decoder_state_version: snapshot.decoder_state_version,
        decoder_state_json: snapshot.decoder_state_json.clone(),
        observed_boundary_json: snapshot.observed_boundary_json.clone(),
        ignored_record_count_delta: 0,
        last_ignored_code: snapshot.last_ignored_code.clone(),
        next_poll_at: Some(now_ms + 5_000),
        events: Vec::new(),
        created_at_override: Some(now_ms),
    })
}

fn parse_obj(raw: &str) -> Result<Value, RunnerError> {
    let value: Value = serde_json::from_str(raw)
        .map_err(|e| RunnerError::InvalidArgument(format!("json: {e}")))?;
    if !value.is_object() {
        return Err(RunnerError::InvalidArgument(
            "checkpoint field must be object".into(),
        ));
    }
    Ok(value)
}

/// Convenience for building a RawRecord in tests / thin adapters.
pub fn raw_record_from_bytes(
    ordinal: u64,
    payload: Vec<u8>,
    byte_start: Option<u64>,
    byte_end: Option<u64>,
    file_mtime_ms: Option<i64>,
) -> RawRecord {
    RawRecord {
        ordinal,
        byte_start,
        byte_end,
        native_rowid: None,
        payload,
        file_mtime_ms,
    }
}
