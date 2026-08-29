use adapter_sdk::ErrorCode;
use collector_core::{CollectionControl, Collector};
use privacy::PrivacyCheckedEvent;
use wal_spool::{AppendClass, Transaction, WalStore};

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
        let poll = tailer.poll(wal.backpressure(), historical)?;
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
        for diagnostic in &poll.diagnostics {
            self.log.record("source_diagnostic", diagnostic);
        }
        let moved = wal
            .latest_checkpoint(&tailer.logical_source_id)
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
