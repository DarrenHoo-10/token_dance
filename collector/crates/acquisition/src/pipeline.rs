use adapter_sdk::{ErrorCode, SourceKind};
use collector_core::{CollectionControl, Collector};
use privacy::PrivacyCheckedEvent;
use protocol::SourceCheckpointStatus;
use wal_spool::{AppendClass, SourceCheckpoint, Transaction, WalStore};

use crate::drivers::DriverBatch;
use crate::error::AcquisitionError;
use crate::jsonl::{JsonlTailer, PollResult};
use crate::log::SafeLog;

/// Acquisition can only decode through Collector, preserving host isolation,
/// disabled policy, metadata checks, and the final privacy boundary.
pub struct IngestPipeline<'a> {
    collector: &'a Collector,
    adapter_id: String,
    control: CollectionControl,
    pub log: SafeLog,
}

impl<'a> IngestPipeline<'a> {
    pub fn new(collector: &'a Collector, adapter_id: impl Into<String>) -> Self {
        let adapter_id = adapter_id.into();
        let control = collector
            .control(&adapter_id)
            .expect("pipeline Adapter must be registered");
        Self {
            collector,
            adapter_id,
            control,
            log: SafeLog::default(),
        }
    }

    fn ensure_enabled(&self) -> Result<(), AcquisitionError> {
        if self.control.is_enabled() {
            Ok(())
        } else {
            self.log.record("scan_skipped", "adapter_disabled");
            Err(AcquisitionError::Other("adapter_disabled".into()))
        }
    }

    pub async fn ingest_batch(
        &self,
        source_id: &str,
        batch: DriverBatch,
        wal: &mut WalStore,
        historical: bool,
    ) -> Result<usize, AcquisitionError> {
        let source_kind = batch
            .frames
            .first()
            .map(|frame| frame.source_kind)
            .ok_or_else(|| AcquisitionError::Other("empty_driver_batch".into()))?;
        self.ingest_bound_batch(source_id, source_kind, batch, wal, historical)
            .await
    }

    pub async fn ingest_bound_batch(
        &self,
        source_id: &str,
        source_kind: SourceKind,
        batch: DriverBatch,
        wal: &mut WalStore,
        historical: bool,
    ) -> Result<usize, AcquisitionError> {
        self.ensure_enabled()?;
        if batch
            .frames
            .iter()
            .any(|frame| frame.source_id != source_id || frame.source_kind != source_kind)
        {
            return Err(AcquisitionError::Other("source_mismatch".into()));
        }
        if historical && !wal.backpressure().allow_historical_scan() {
            self.log.record("scan_skipped", "hard_backpressure");
            return Ok(0);
        }
        let class = if historical {
            AppendClass::Historical
        } else {
            AppendClass::Realtime
        };
        let mut accepted = Vec::new();
        for frame in batch.frames {
            self.ensure_enabled()?;
            match self.collector.decode(&self.adapter_id, frame).await {
                Ok(events) => accepted.extend(events),
                Err(err) if err.code == ErrorCode::PrivacyRejected => {
                    self.log.record("privacy_rejected", "privacy_policy");
                    wal.append_dead_letter(wal_spool::DeadLetterPayload {
                        event_id: None,
                        error_code: "PRIVACY_REJECTED".into(),
                        retryable: false,
                        created_at: String::new(),
                        safe_reason: "privacy_policy".into(),
                    })?;
                }
                Err(err) => self
                    .log
                    .record("decode_failed", &err.code.as_str().to_ascii_lowercase()),
            }
        }
        self.ensure_enabled()?;
        let offset = batch.cursor.parse::<u64>().unwrap_or_else(|_| {
            batch.cursor.bytes().fold(0_u64, |value, byte| {
                value.wrapping_mul(109).wrapping_add(byte as u64)
            })
        });
        let previous = wal.latest_checkpoint(source_id).cloned();
        let checkpoint = SourceCheckpoint {
            source_id: source_id.to_owned(),
            path_template_id: source_id.to_owned(),
            file_identity: source_id.to_owned(),
            generation: 1,
            file_len: offset,
            offset,
            last_record_hash: Some(batch.cursor),
            driver_checkpoint: batch.driver_checkpoint,
            status: SourceCheckpointStatus::Current,
        };
        let count = accepted.len();
        let transaction = Transaction::new(
            String::new(),
            source_id,
            previous,
            checkpoint,
            accepted,
            String::new(),
        );
        self.control
            .with_commit_lease(|| wal.append_txn(transaction, class))
            .ok_or_else(|| AcquisitionError::Other("adapter_disabled".into()))??;
        Ok(count)
    }

    pub async fn ingest(
        &self,
        tailer: &mut JsonlTailer,
        wal: &mut WalStore,
        historical: bool,
    ) -> Result<PollResult, AcquisitionError> {
        self.ensure_enabled()?;
        let class = if historical {
            AppendClass::Historical
        } else {
            AppendClass::Realtime
        };
        let mut poll = tailer.poll(wal.backpressure(), historical)?;
        self.ensure_enabled()?;
        if poll.skipped {
            self.log.record("scan_skipped", "hard_backpressure");
            return Ok(poll);
        }
        let mut accepted: Vec<PrivacyCheckedEvent> = Vec::new();
        for frame in &poll.frames {
            self.ensure_enabled()?;
            let decoded = self.collector.decode(&self.adapter_id, frame.clone()).await;
            self.ensure_enabled()?;
            match decoded {
                Ok(events) => accepted.extend(events),
                Err(err) if err.code == ErrorCode::PrivacyRejected => {
                    self.log.record("privacy_rejected", "privacy_policy");
                    wal.append_dead_letter(wal_spool::DeadLetterPayload {
                        event_id: None,
                        error_code: "PRIVACY_REJECTED".into(),
                        retryable: false,
                        created_at: String::new(),
                        safe_reason: "privacy_policy".into(),
                    })?;
                }
                Err(err) => self
                    .log
                    .record("decode_failed", &err.code.as_str().to_ascii_lowercase()),
            }
        }
        poll.accepted_events = accepted.len();
        for diagnostic in &poll.diagnostics {
            self.log.record("source_diagnostic", diagnostic);
        }
        let moved = wal
            .checkpoint(&tailer.logical_source_id, &poll.next_checkpoint.file_identity)
            .map(|item| {
                item.offset != poll.next_checkpoint.offset
                    || item.generation != poll.next_checkpoint.generation
                    || item.file_identity != poll.next_checkpoint.file_identity
            })
            .unwrap_or(true);
        if accepted.is_empty() && poll.frames.is_empty() && poll.diagnostics.is_empty() && !moved {
            return Ok(poll);
        }
        let transaction = Transaction::new(
            String::new(),
            tailer.logical_source_id.clone(),
            wal.checkpoint(
                &tailer.logical_source_id,
                &poll.next_checkpoint.file_identity,
            )
            .cloned(),
            poll.next_checkpoint.clone(),
            accepted,
            String::new(),
        );
        let append = self
            .control
            .with_commit_lease(|| wal.append_txn(transaction, class))
            .ok_or_else(|| AcquisitionError::Other("adapter_disabled".into()))?;
        append?;
        Ok(poll)
    }
}
