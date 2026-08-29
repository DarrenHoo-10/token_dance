use protocol::{EventEnvelope, RejectedEvent, UploadBatch};
use wal_spool::{new_prefixed_id, DeadLetterPayload};

pub const DEFAULT_MAX_BATCH_EVENTS: usize = 500;
pub const DEFAULT_MAX_BATCH_BYTES: usize = 512 * 1024;

#[derive(Debug, Clone)]
pub struct BatchLimits {
    pub max_events: usize,
    pub max_bytes: usize,
}

impl Default for BatchLimits {
    fn default() -> Self {
        Self {
            max_events: DEFAULT_MAX_BATCH_EVENTS,
            max_bytes: DEFAULT_MAX_BATCH_BYTES,
        }
    }
}

#[derive(Debug, Clone)]
pub struct PreparedBatch {
    pub batch: UploadBatch,
    pub encoded_len: usize,
}

pub fn build_batches(
    installation_id: &str,
    events: Vec<EventEnvelope>,
    limits: &BatchLimits,
    created_at: &str,
) -> Result<(Vec<PreparedBatch>, Vec<DeadLetterPayload>), String> {
    let mut batches = Vec::new();
    let mut dead = Vec::new();
    let mut current: Vec<EventEnvelope> = Vec::new();
    let mut current_len = overhead_len(installation_id, created_at);

    for event in events {
        let encoded = serde_json::to_vec(&event).map_err(|err| err.to_string())?;
        if encoded.len() + 2 > limits.max_bytes {
            dead.push(DeadLetterPayload {
                event_id: Some(event.event_id),
                error_code: "EVENT_TOO_LARGE".into(),
                retryable: false,
                created_at: created_at.to_string(),
                safe_reason: "event_too_large".into(),
            });
            continue;
        }
        let extra = encoded.len() + if current.is_empty() { 0 } else { 1 };
        if !current.is_empty()
            && (current.len() >= limits.max_events || current_len + extra > limits.max_bytes)
        {
            batches.push(seal(
                installation_id,
                created_at,
                std::mem::take(&mut current),
            )?);
            current_len = overhead_len(installation_id, created_at);
        }
        current_len += extra;
        current.push(event);
    }
    if !current.is_empty() {
        batches.push(seal(installation_id, created_at, current)?);
    }
    Ok((batches, dead))
}

fn seal(
    installation_id: &str,
    created_at: &str,
    events: Vec<EventEnvelope>,
) -> Result<PreparedBatch, String> {
    let batch = UploadBatch {
        batch_id: new_prefixed_id("bat"),
        installation_id: installation_id.to_string(),
        created_at: created_at.to_string(),
        events,
    };
    let encoded = serde_json::to_vec(&batch).map_err(|err| err.to_string())?;
    Ok(PreparedBatch {
        encoded_len: encoded.len(),
        batch,
    })
}

fn overhead_len(installation_id: &str, created_at: &str) -> usize {
    64 + installation_id.len() + created_at.len()
}

pub fn accepted_ids(batch: &UploadBatch, rejected: &[RejectedEvent]) -> Vec<String> {
    let rejected: std::collections::HashSet<&str> =
        rejected.iter().map(|item| item.event_id.as_str()).collect();
    batch
        .events
        .iter()
        .filter(|event| !rejected.contains(event.event_id.as_str()))
        .map(|event| event.event_id.clone())
        .collect()
}
