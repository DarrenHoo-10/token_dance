use protocol::{RejectedEventErrorCode, UploadAck, UploadBatch};
use wal_spool::{AckPayload, DeadLetterPayload, WalStore};

use crate::batch::{accepted_ids, build_batches, BatchLimits};
use crate::error::{TransportError, UploadError};
use crate::retry::RetryPolicy;
use crate::transport::IngestTransport;

pub struct Uploader<T> {
    installation_id: String,
    transport: T,
    retry: RetryPolicy,
    limits: BatchLimits,
    paused: bool,
}

#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct FlushReport {
    pub batches: usize,
    pub acked_events: usize,
    pub dead_letters: usize,
    pub retries: u32,
    pub paused: bool,
    pub bytes: usize,
}

impl<T: IngestTransport> Uploader<T> {
    pub fn new(installation_id: impl Into<String>, transport: T) -> Self {
        Self {
            installation_id: installation_id.into(),
            transport,
            retry: RetryPolicy::default(),
            limits: BatchLimits::default(),
            paused: false,
        }
    }

    pub fn with_retry(mut self, retry: RetryPolicy) -> Self {
        self.retry = retry;
        self
    }

    pub fn with_limits(mut self, limits: BatchLimits) -> Self {
        self.limits = limits;
        self
    }

    pub fn is_paused(&self) -> bool {
        self.paused
    }

    pub fn transport(&self) -> &T {
        &self.transport
    }

    pub async fn flush(&mut self, wal: &mut WalStore) -> Result<FlushReport, UploadError> {
        if self.paused {
            return Err(UploadError::AuthPaused);
        }
        let events = wal.unacked_events();
        if events.is_empty() {
            return Ok(FlushReport::default());
        }
        let created_at = "2026-08-30T00:00:00.000Z";
        let (batches, oversized) =
            build_batches(&self.installation_id, events, &self.limits, created_at)
                .map_err(UploadError::Encode)?;
        let mut report = FlushReport::default();
        for payload in oversized {
            wal.append_dead_letter(payload)?;
            report.dead_letters += 1;
        }
        for prepared in batches {
            report.batches += 1;
            report.bytes += prepared.encoded_len;
            match self
                .upload_with_retry(&prepared.batch, wal, &mut report)
                .await
            {
                Ok(()) => {}
                Err(UploadError::AuthPaused) => {
                    report.paused = true;
                    return Ok(report);
                }
                Err(err) => return Err(err),
            }
        }
        let _ = wal.compact();
        Ok(report)
    }

    async fn upload_with_retry(
        &mut self,
        batch: &UploadBatch,
        wal: &mut WalStore,
        report: &mut FlushReport,
    ) -> Result<(), UploadError> {
        let mut attempt = 0u32;
        loop {
            attempt += 1;
            match self.transport.upload(batch).await {
                Ok(ack) => {
                    self.apply_ack(wal, batch, ack, report)?;
                    return Ok(());
                }
                Err(TransportError::Auth) => {
                    self.paused = true;
                    report.paused = true;
                    return Err(UploadError::AuthPaused);
                }
                Err(TransportError::Http { status: 400, .. }) => {
                    self.split_or_dead_letter(wal, batch, report).await?;
                    return Ok(());
                }
                Err(err) if err.is_retryable() && attempt < self.retry.max_attempts => {
                    report.retries += 1;
                    let delay = self.retry.delay_for(attempt, Some(&err));
                    if !delay.is_zero() {
                        tokio::time::sleep(delay).await;
                    }
                }
                Err(err) => return Err(err.into()),
            }
        }
    }

    fn apply_ack(
        &self,
        wal: &mut WalStore,
        batch: &UploadBatch,
        ack: UploadAck,
        report: &mut FlushReport,
    ) -> Result<(), UploadError> {
        let mut acked = accepted_ids(batch, &ack.rejected);
        let mut dead = Vec::new();
        let mut retryable = Vec::new();
        for rejected in &ack.rejected {
            if rejected.retryable
                || rejected.error_code == RejectedEventErrorCode::InternalRetryable
            {
                retryable.push(rejected.event_id.clone());
            } else {
                dead.push(rejected);
            }
        }
        acked.retain(|id| !retryable.iter().any(|item| item == id));
        if !acked.is_empty() {
            wal.append_ack(AckPayload {
                batch_id: ack.batch_id,
                acked_event_ids: acked.clone(),
                server_acked_at: ack.server_time,
            })?;
            report.acked_events += acked.len();
        }
        for rejected in dead {
            wal.append_dead_letter(DeadLetterPayload {
                event_id: Some(rejected.event_id.clone()),
                error_code: rejection_error_code(rejected.error_code).into(),
                retryable: false,
                created_at: "2026-08-30T00:00:00.000Z".into(),
                safe_reason: "server_rejected_event".into(),
            })?;
            report.dead_letters += 1;
        }
        Ok(())
    }

    async fn split_or_dead_letter(
        &mut self,
        wal: &mut WalStore,
        batch: &UploadBatch,
        report: &mut FlushReport,
    ) -> Result<(), UploadError> {
        if batch.events.len() <= 1 {
            if let Some(event) = batch.events.first() {
                wal.append_dead_letter(DeadLetterPayload {
                    event_id: Some(event.event_id.clone()),
                    error_code: "SCHEMA_INVALID".into(),
                    retryable: false,
                    created_at: "2026-08-30T00:00:00.000Z".into(),
                    safe_reason: "http_400_schema_error".into(),
                })?;
                report.dead_letters += 1;
            }
            return Ok(());
        }
        let mid = batch.events.len() / 2;
        let left = UploadBatch {
            batch_id: wal_spool::new_prefixed_id("bat"),
            installation_id: batch.installation_id.clone(),
            created_at: batch.created_at.clone(),
            events: batch.events[..mid].to_vec(),
        };
        let right = UploadBatch {
            batch_id: wal_spool::new_prefixed_id("bat"),
            installation_id: batch.installation_id.clone(),
            created_at: batch.created_at.clone(),
            events: batch.events[mid..].to_vec(),
        };
        Box::pin(self.upload_with_retry(&left, wal, report)).await?;
        Box::pin(self.upload_with_retry(&right, wal, report)).await?;
        Ok(())
    }
}

fn rejection_error_code(code: RejectedEventErrorCode) -> &'static str {
    match code {
        RejectedEventErrorCode::SchemaInvalid => "SCHEMA_INVALID",
        RejectedEventErrorCode::PrivacyRejected => "PRIVACY_REJECTED",
        RejectedEventErrorCode::EventTooLarge => "EVENT_TOO_LARGE",
        RejectedEventErrorCode::UnsupportedVersion => "UNSUPPORTED_VERSION",
        RejectedEventErrorCode::InternalRetryable => "INTERNAL_RETRYABLE",
    }
}
