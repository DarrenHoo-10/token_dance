//! Single-writer command channel for the event pipeline store.

use std::sync::mpsc::{self, Receiver, RecvTimeoutError, Sender, SyncSender, TrySendError};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use super::store::{DrainStats, PipelineStore};
use super::types::{
    PipelineError, SourceCheckpointSnapshot, SourceCommitBatch, SourceCommitResult, TaskComplete,
    TaskRetry, LeasedTask, Consumer, WRITER_QUEUE_BATCHES, WRITER_QUEUE_BYTES,
    COMPENSATION_INTERVAL_MS,
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
    pub fn start(mut store: PipelineStore) -> Self {
        let (tx, rx) = mpsc::sync_channel::<QueuedBatch>(WRITER_QUEUE_BATCHES);
        let join = thread::spawn(move || writer_loop(&mut store, rx));
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

    pub fn reclaim_leases(&self) -> Result<usize, PipelineError> {
        self.request(0, |reply| WriterCommand::ReclaimLeases { reply })
    }

    pub fn expire_events(&self, limit: usize) -> Result<usize, PipelineError> {
        self.request(0, |reply| WriterCommand::ExpireEvents { limit, reply })
    }

    pub fn run_compensation(&self) -> Result<CompensationStats, PipelineError> {
        self.request(0, |reply| WriterCommand::RunCompensation { reply })
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

fn writer_loop(store: &mut PipelineStore, rx: Receiver<QueuedBatch>) {
    let mut pending_bytes: usize = 0;
    let mut last_compensation = Instant::now();
    loop {
        let wait = Duration::from_millis(COMPENSATION_INTERVAL_MS as u64);
        let queued = match rx.recv_timeout(wait) {
            Ok(item) => item,
            Err(RecvTimeoutError::Timeout) => {
                let _ = store.reclaim_expired_leases();
                let _ = store.expire_due_events(64);
                last_compensation = Instant::now();
                continue;
            }
            Err(RecvTimeoutError::Disconnected) => break,
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
                last_compensation = Instant::now();
                false
            }
            WriterCommand::Shutdown { reply } => {
                let _ = reply.send(());
                true
            }
        };
        pending_bytes = pending_bytes.saturating_sub(queued.approx_bytes);
        let _ = pending_bytes;
        let _ = last_compensation;
        if done {
            break;
        }
    }
}
