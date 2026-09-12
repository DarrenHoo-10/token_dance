//! Single-writer command channel for the event pipeline store.

use std::sync::mpsc::{self, Receiver, RecvTimeoutError, Sender, SyncSender, TrySendError};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use super::store::{DrainStats, PipelineStore};
use super::types::{
    PipelineError, SourceCheckpointSnapshot, SourceCommitBatch, SourceCommitResult, TaskComplete,
    TaskRetry, LeasedTask, Consumer, RenewLease, UploadWireEvent, WRITER_QUEUE_BATCHES,
    WRITER_QUEUE_BYTES, COMPENSATION_INTERVAL_MS,
};

enum WriterCommand {
    LeaseSource {
        source_id: i64,
        lease_ms: i64,
        reply: Sender<Result<(String, i64, i64), PipelineError>>,
    },
    LoadSourceCheckpoint {
        source_id: i64,
        reply: Sender<Result<SourceCheckpointSnapshot, PipelineError>>,
    },
    CommitSource {
        batch: SourceCommitBatch,
        reply: Sender<Result<SourceCommitResult, PipelineError>>,
    },
    ClaimTasks {
        consumer: Consumer,
        limit: usize,
        lease_ms: i64,
        reply: Sender<Result<Vec<LeasedTask>, PipelineError>>,
    },
    CompleteTask {
        complete: TaskComplete,
        reply: Sender<Result<(), PipelineError>>,
    },
    ApplyMetricsTask {
        task: LeasedTask,
        reply: Sender<Result<(), PipelineError>>,
    },
    DrainMetrics {
        consumer: Consumer,
        limit: usize,
        lease_ms: i64,
        reply: Sender<Result<DrainStats, PipelineError>>,
    },
    RetryTask {
        retry: TaskRetry,
        reply: Sender<Result<(), PipelineError>>,
    },
    RenewLease {
        renew: RenewLease,
        reply: Sender<Result<i64, PipelineError>>,
    },
    LoadUploadEvents {
        event_row_ids: Vec<i64>,
        reply: Sender<Result<Vec<UploadWireEvent>, PipelineError>>,
    },
    PendingUploadCount {
        reply: Sender<Result<i64, PipelineError>>,
    },
    ReclaimLeases {
        reply: Sender<Result<usize, PipelineError>>,
    },
    ExpireEvents {
        limit: usize,
        reply: Sender<Result<usize, PipelineError>>,
    },
    RunCompensation {
        reply: Sender<Result<CompensationStats, PipelineError>>,
    },
    RegisterSource {
        source: crate::local_store::pipeline::types::RegisterSource,
        reply: Sender<Result<i64, PipelineError>>,
    },
    RegisterSkill {
        skill_key: [u8; 32],
        public_name: Option<String>,
        reply: Sender<Result<i64, PipelineError>>,
    },
    ListDueSources {
        now_ms: i64,
        limit: usize,
        reply: Sender<Result<Vec<i64>, PipelineError>>,
    },
    ListDueSourcesForHarness {
        harness_id: String,
        now_ms: i64,
        limit: usize,
        reply: Sender<Result<Vec<i64>, PipelineError>>,
    },
    SetHarnessSourcesEnabled {
        harness_id: String,
        enabled: bool,
        reply: Sender<Result<usize, PipelineError>>,
    },
    ListSourceLocators {
        harness_id: String,
        reply: Sender<Result<Vec<String>, PipelineError>>,
    },
    QueryUsageSummary {
        grain: super::buckets::Grain,
        range_start: i64,
        range_end: i64,
        harness_id: Option<String>,
        reply: Sender<Result<super::query::UsageSummary, PipelineError>>,
    },
    QueryHarnessUsageHistory {
        harness_id: String,
        today_start: i64,
        range_end: i64,
        reply: Sender<Result<super::query::HarnessUsageHistory, PipelineError>>,
    },
    QueryHarnessTokenSeries {
        grain: super::buckets::Grain,
        range_start: i64,
        range_end: i64,
        harness_id: String,
        reply: Sender<Result<Vec<(i64, i64)>, PipelineError>>,
    },
    Shutdown {
        reply: Sender<()>,
    },
}

#[derive(Debug, Clone, Default)]
pub struct CompensationStats {
    pub reclaimed_leases: usize,
    pub expired_events: usize,
}

struct QueuedBatch {
    cmd: WriterCommand,
    approx_bytes: usize,
}

/// Bounded single-writer actor. Network I/O must stay outside this channel.
pub struct PipelineWriter {
    tx: SyncSender<QueuedBatch>,
    join: Option<JoinHandle<()>>,
}

impl PipelineWriter {
    pub fn start(store: PipelineStore) -> Self {
        Self::start_with_compensation_ms(store, COMPENSATION_INTERVAL_MS as u64)
    }

    pub fn start_with_compensation_ms(mut store: PipelineStore, compensation_ms: u64) -> Self {
        let (tx, rx) = mpsc::sync_channel::<QueuedBatch>(WRITER_QUEUE_BATCHES);
        let join = thread::spawn(move || writer_loop(&mut store, rx, compensation_ms));
        Self {
            tx,
            join: Some(join),
        }
    }

    fn enqueue(&self, cmd: WriterCommand, approx_bytes: usize) -> Result<(), PipelineError> {
        // sync_channel already bounds batch count; also reject oversized pending bytes
        // by refusing when the approx payload alone exceeds the global budget.
        if approx_bytes > WRITER_QUEUE_BYTES {
            return Err(PipelineError::BatchTooLarge {
                events: 0,
                bytes: approx_bytes,
            });
        }
        match self.tx.try_send(QueuedBatch { cmd, approx_bytes }) {
            Ok(()) => Ok(()),
            Err(TrySendError::Full(_)) => Err(PipelineError::WriterBackpressure),
            Err(TrySendError::Disconnected(_)) => Err(PipelineError::Sqlite(
                "pipeline writer disconnected".into(),
            )),
        }
    }

    fn request<T>(
        &self,
        approx_bytes: usize,
        build: impl FnOnce(Sender<Result<T, PipelineError>>) -> WriterCommand,
    ) -> Result<T, PipelineError>
    where
        T: Send + 'static,
    {
        let (reply_tx, reply_rx) = mpsc::channel();
        self.enqueue(build(reply_tx), approx_bytes)?;
        reply_rx
            .recv()
            .map_err(|_| PipelineError::Sqlite("pipeline writer dropped reply".into()))?
    }

    pub fn commit_source(&self, batch: SourceCommitBatch) -> Result<SourceCommitResult, PipelineError> {
        let bytes = estimate_batch_bytes(&batch);
        self.request(bytes, |reply| WriterCommand::CommitSource { batch, reply })
    }

    pub fn lease_source(
        &self,
        source_id: i64,
        lease_ms: i64,
    ) -> Result<(String, i64, i64), PipelineError> {
        self.request(0, |reply| WriterCommand::LeaseSource {
            source_id,
            lease_ms,
            reply,
        })
    }

    pub fn load_source_checkpoint(
        &self,
        source_id: i64,
    ) -> Result<SourceCheckpointSnapshot, PipelineError> {
        self.request(0, |reply| WriterCommand::LoadSourceCheckpoint {
            source_id,
            reply,
        })
    }

    pub fn claim_tasks(
        &self,
        consumer: Consumer,
        limit: usize,
        lease_ms: i64,
    ) -> Result<Vec<LeasedTask>, PipelineError> {
        self.request(0, |reply| WriterCommand::ClaimTasks {
            consumer,
            limit,
            lease_ms,
            reply,
        })
    }

    pub fn complete_task(&self, complete: TaskComplete) -> Result<(), PipelineError> {
        self.request(0, |reply| WriterCommand::CompleteTask { complete, reply })
    }

    pub fn apply_metrics_task(&self, task: LeasedTask) -> Result<(), PipelineError> {
        self.request(0, |reply| WriterCommand::ApplyMetricsTask { task, reply })
    }

    pub fn drain_metrics_consumer(
        &self,
        consumer: Consumer,
        limit: usize,
        lease_ms: i64,
    ) -> Result<DrainStats, PipelineError> {
        self.request(0, |reply| WriterCommand::DrainMetrics {
            consumer,
            limit,
            lease_ms,
            reply,
        })
    }

    pub fn retry_task(&self, retry: TaskRetry) -> Result<(), PipelineError> {
        self.request(0, |reply| WriterCommand::RetryTask { reply, retry })
    }

    pub fn renew_task_lease(&self, renew: RenewLease) -> Result<i64, PipelineError> {
        self.request(0, |reply| WriterCommand::RenewLease { renew, reply })
    }

    pub fn load_upload_events(
        &self,
        event_row_ids: Vec<i64>,
    ) -> Result<Vec<UploadWireEvent>, PipelineError> {
        self.request(0, |reply| WriterCommand::LoadUploadEvents {
            event_row_ids,
            reply,
        })
    }

    pub fn pending_upload_count(&self) -> Result<i64, PipelineError> {
        self.request(0, |reply| WriterCommand::PendingUploadCount { reply })
    }

    pub fn reclaim_leases(&self) -> Result<usize, PipelineError> {
        self.request(0, |reply| WriterCommand::ReclaimLeases { reply })
    }

    pub fn expire_events(&self, limit: usize) -> Result<usize, PipelineError> {
        self.request(0, |reply| WriterCommand::ExpireEvents { limit, reply })
    }

    pub fn run_compensation(&self) -> Result<CompensationStats, PipelineError> {
        self.request(0, |reply| WriterCommand::RunCompensation { reply })
    }

    pub fn register_source(
        &self,
        source: crate::local_store::pipeline::types::RegisterSource,
    ) -> Result<i64, PipelineError> {
        self.request(0, |reply| WriterCommand::RegisterSource { source, reply })
    }

    pub fn register_skill(
        &self,
        skill_key: [u8; 32],
        public_name: Option<&str>,
    ) -> Result<i64, PipelineError> {
        self.request(0, |reply| WriterCommand::RegisterSkill {
            skill_key,
            public_name: public_name.map(|s| s.to_string()),
            reply,
        })
    }

    pub fn list_due_sources(&self, now_ms: i64, limit: usize) -> Result<Vec<i64>, PipelineError> {
        self.request(0, |reply| WriterCommand::ListDueSources {
            now_ms,
            limit,
            reply,
        })
    }

    pub fn list_due_sources_for_harness(
        &self,
        harness_id: &str,
        now_ms: i64,
        limit: usize,
    ) -> Result<Vec<i64>, PipelineError> {
        self.request(0, |reply| WriterCommand::ListDueSourcesForHarness {
            harness_id: harness_id.to_string(),
            now_ms,
            limit,
            reply,
        })
    }

    pub fn set_harness_sources_enabled(
        &self,
        harness_id: &str,
        enabled: bool,
    ) -> Result<usize, PipelineError> {
        self.request(0, |reply| WriterCommand::SetHarnessSourcesEnabled {
            harness_id: harness_id.to_string(),
            enabled,
            reply,
        })
    }

    pub fn list_source_locators(&self, harness_id: &str) -> Result<Vec<String>, PipelineError> {
        self.request(0, |reply| WriterCommand::ListSourceLocators {
            harness_id: harness_id.to_string(),
            reply,
        })
    }

    pub fn query_usage_summary(
        &self,
        grain: super::buckets::Grain,
        range_start: i64,
        range_end: i64,
        harness_id: Option<&str>,
    ) -> Result<super::query::UsageSummary, PipelineError> {
        self.request(0, |reply| WriterCommand::QueryUsageSummary {
            grain,
            range_start,
            range_end,
            harness_id: harness_id.map(|s| s.to_string()),
            reply,
        })
    }

    pub fn query_harness_usage_history(
        &self,
        harness_id: &str,
        today_start: i64,
        range_end: i64,
    ) -> Result<super::query::HarnessUsageHistory, PipelineError> {
        self.request(0, |reply| WriterCommand::QueryHarnessUsageHistory {
            harness_id: harness_id.to_string(),
            today_start,
            range_end,
            reply,
        })
    }

    /// `(bucket_start, token_total)` ascending for one harness/grain.
    pub fn query_harness_token_series(
        &self,
        grain: super::buckets::Grain,
        range_start: i64,
        range_end: i64,
        harness_id: &str,
    ) -> Result<Vec<(i64, i64)>, PipelineError> {
        self.request(0, |reply| WriterCommand::QueryHarnessTokenSeries {
            grain,
            range_start,
            range_end,
            harness_id: harness_id.to_string(),
            reply,
        })
    }

    pub fn shutdown(mut self) {
        let (reply_tx, reply_rx) = mpsc::channel();
        let _ = self.enqueue(
            WriterCommand::Shutdown { reply: reply_tx },
            0,
        );
        let _ = reply_rx.recv_timeout(Duration::from_secs(5));
        if let Some(join) = self.join.take() {
            let _ = join.join();
        }
    }
}

impl Drop for PipelineWriter {
    fn drop(&mut self) {
        if self.join.is_some() {
            let (reply_tx, reply_rx) = mpsc::channel();
            let _ = self.tx.try_send(QueuedBatch {
                cmd: WriterCommand::Shutdown { reply: reply_tx },
                approx_bytes: 0,
            });
            let _ = reply_rx.recv_timeout(Duration::from_secs(2));
            if let Some(join) = self.join.take() {
                let _ = join.join();
            }
        }
    }
}

fn estimate_batch_bytes(batch: &SourceCommitBatch) -> usize {
    batch
        .events
        .iter()
        .map(|e| e.payload_json.len() + 128)
        .sum::<usize>()
        + batch.cursor_json.len()
        + batch.decoder_state_json.len()
        + batch.observed_boundary_json.len()
}

fn writer_loop(store: &mut PipelineStore, rx: Receiver<QueuedBatch>, compensation_ms: u64) {
    let mut pending_bytes: usize = 0;
    let interval = Duration::from_millis(compensation_ms.max(1));
    let mut next_compensation = Instant::now() + interval;
    loop {
        let now = Instant::now();
        let wait = next_compensation.saturating_duration_since(now);
        let queued = match rx.recv_timeout(wait) {
            Ok(item) => Some(item),
            Err(RecvTimeoutError::Timeout) => None,
            Err(RecvTimeoutError::Disconnected) => break,
        };

        // Absolute monotonic deadline: run even when the channel stays busy.
        if Instant::now() >= next_compensation {
            let _ = store.reclaim_expired_leases();
            let _ = store.expire_due_events(64);
            next_compensation = Instant::now() + interval;
        }

        let Some(queued) = queued else {
            continue;
        };

        pending_bytes = pending_bytes.saturating_add(queued.approx_bytes);
        let done = match queued.cmd {
            WriterCommand::LeaseSource {
                source_id,
                lease_ms,
                reply,
            } => {
                let _ = reply.send(store.lease_source(source_id, lease_ms));
                false
            }
            WriterCommand::LoadSourceCheckpoint { source_id, reply } => {
                let _ = reply.send(store.load_source_checkpoint(source_id));
                false
            }
            WriterCommand::CommitSource { batch, reply } => {
                let _ = reply.send(store.commit_source(batch));
                false
            }
            WriterCommand::ClaimTasks {
                consumer,
                limit,
                lease_ms,
                reply,
            } => {
                let _ = reply.send(store.claim_tasks(consumer, limit, lease_ms));
                false
            }
            WriterCommand::CompleteTask { complete, reply } => {
                let _ = reply.send(store.complete_task(complete));
                false
            }
            WriterCommand::ApplyMetricsTask { task, reply } => {
                let _ = reply.send(store.apply_and_complete_metrics(&task));
                false
            }
            WriterCommand::DrainMetrics {
                consumer,
                limit,
                lease_ms,
                reply,
            } => {
                let _ = reply.send(store.drain_metrics_consumer(consumer, limit, lease_ms));
                false
            }
            WriterCommand::RetryTask { retry, reply } => {
                let _ = reply.send(store.retry_task(retry));
                false
            }
            WriterCommand::RenewLease { renew, reply } => {
                let _ = reply.send(store.renew_task_lease(renew));
                false
            }
            WriterCommand::LoadUploadEvents {
                event_row_ids,
                reply,
            } => {
                let _ = reply.send(store.load_upload_events(&event_row_ids));
                false
            }
            WriterCommand::PendingUploadCount { reply } => {
                let _ = reply.send(store.pending_upload_count());
                false
            }
            WriterCommand::ReclaimLeases { reply } => {
                let _ = reply.send(store.reclaim_expired_leases());
                false
            }
            WriterCommand::ExpireEvents { limit, reply } => {
                let _ = reply.send(store.expire_due_events(limit));
                false
            }
            WriterCommand::RunCompensation { reply } => {
                let reclaimed = store.reclaim_expired_leases().unwrap_or(0);
                let expired = store.expire_due_events(64).unwrap_or(0);
                let _ = reply.send(Ok(CompensationStats {
                    reclaimed_leases: reclaimed,
                    expired_events: expired,
                }));
                next_compensation = Instant::now() + interval;
                false
            }
            WriterCommand::RegisterSource { source, reply } => {
                let _ = reply.send(store.register_source(&source));
                false
            }
            WriterCommand::RegisterSkill {
                skill_key,
                public_name,
                reply,
            } => {
                let _ = reply.send(store.register_skill(&skill_key, public_name.as_deref()));
                false
            }
            WriterCommand::ListDueSources {
                now_ms,
                limit,
                reply,
            } => {
                let _ = reply.send(store.list_due_sources(now_ms, limit));
                false
            }
            WriterCommand::ListDueSourcesForHarness {
                harness_id,
                now_ms,
                limit,
                reply,
            } => {
                let _ = reply.send(store.list_due_sources_for_harness(&harness_id, now_ms, limit));
                false
            }
            WriterCommand::SetHarnessSourcesEnabled {
                harness_id,
                enabled,
                reply,
            } => {
                let _ = reply.send(store.set_harness_sources_enabled(&harness_id, enabled));
                false
            }
            WriterCommand::ListSourceLocators { harness_id, reply } => {
                let _ = reply.send(store.list_source_locators(&harness_id));
                false
            }
            WriterCommand::QueryUsageSummary {
                grain,
                range_start,
                range_end,
                harness_id,
                reply,
            } => {
                let result = store.with_connection(|conn| {
                    super::query::query_usage_summary(
                        conn,
                        grain,
                        range_start,
                        range_end,
                        harness_id.as_deref(),
                    )
                });
                let _ = reply.send(result);
                false
            }
            WriterCommand::QueryHarnessUsageHistory {
                harness_id,
                today_start,
                range_end,
                reply,
            } => {
                let result = store.with_connection(|conn| {
                    super::query::query_harness_usage_history(
                        conn,
                        &harness_id,
                        today_start,
                        range_end,
                    )
                });
                let _ = reply.send(result);
                false
            }
            WriterCommand::QueryHarnessTokenSeries {
                grain,
                range_start,
                range_end,
                harness_id,
                reply,
            } => {
                let result = store.with_connection(|conn| {
                    super::query::query_harness_token_series(
                        conn,
                        grain,
                        range_start,
                        range_end,
                        &harness_id,
                    )
                });
                let _ = reply.send(result);
                false
            }
            WriterCommand::Shutdown { reply } => {
                let _ = reply.send(());
                true
            }
        };
        pending_bytes = pending_bytes.saturating_sub(queued.approx_bytes);
        let _ = pending_bytes;
        if done {
            break;
        }
    }
}
