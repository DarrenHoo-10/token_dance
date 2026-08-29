use protocol::EventEnvelope;
use serde::{Deserialize, Serialize};

use crate::checkpoint::SourceCheckpoint;

/// Public write request. It is intentionally not deserializable and its event
/// field is private, so callers can only create it from privacy-checked events.
///
/// ```compile_fail
/// use wal_spool::Transaction;
/// let _: Transaction = serde_json::from_str("{}").unwrap();
/// ```
///
/// ```compile_fail
/// use protocol::EventEnvelope;
/// use wal_spool::{SourceCheckpoint, Transaction};
/// let raw: Vec<EventEnvelope> = Vec::new();
/// let checkpoint: SourceCheckpoint = todo!();
/// let _ = Transaction::new("txn", "source", None, checkpoint, raw, "now");
/// ```
#[derive(Debug, Clone, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Transaction {
    pub transaction_id: String,
    pub source_id: String,
    pub previous_checkpoint: Option<SourceCheckpoint>,
    pub next_checkpoint: SourceCheckpoint,
    pub(crate) normalized_events: Vec<EventEnvelope>,
    pub created_at: String,
}

impl Transaction {
    pub fn new(
        transaction_id: impl Into<String>,
        source_id: impl Into<String>,
        previous_checkpoint: Option<SourceCheckpoint>,
        next_checkpoint: SourceCheckpoint,
        events: Vec<privacy::PrivacyCheckedEvent>,
        created_at: impl Into<String>,
    ) -> Self {
        Self {
            transaction_id: transaction_id.into(),
            source_id: source_id.into(),
            previous_checkpoint,
            next_checkpoint,
            normalized_events: events
                .into_iter()
                .map(privacy::PrivacyCheckedEvent::into_envelope)
                .collect(),
            created_at: created_at.into(),
        }
    }

    pub fn event_count(&self) -> usize {
        self.normalized_events.len()
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct PersistedTransaction {
    pub transaction_id: String,
    pub source_id: String,
    pub previous_checkpoint: Option<SourceCheckpoint>,
    pub next_checkpoint: SourceCheckpoint,
    pub normalized_events: Vec<EventEnvelope>,
    pub created_at: String,
}

impl From<&Transaction> for PersistedTransaction {
    fn from(value: &Transaction) -> Self {
        Self {
            transaction_id: value.transaction_id.clone(),
            source_id: value.source_id.clone(),
            previous_checkpoint: value.previous_checkpoint.clone(),
            next_checkpoint: value.next_checkpoint.clone(),
            normalized_events: value.normalized_events.clone(),
            created_at: value.created_at.clone(),
        }
    }
}

impl From<PersistedTransaction> for Transaction {
    fn from(value: PersistedTransaction) -> Self {
        Self {
            transaction_id: value.transaction_id,
            source_id: value.source_id,
            previous_checkpoint: value.previous_checkpoint,
            next_checkpoint: value.next_checkpoint,
            normalized_events: value.normalized_events,
            created_at: value.created_at,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct AckPayload {
    pub batch_id: String,
    pub acked_event_ids: Vec<String>,
    pub server_acked_at: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DeadLetterPayload {
    pub event_id: Option<String>,
    pub error_code: String,
    pub retryable: bool,
    pub created_at: String,
    pub safe_reason: String,
}

#[derive(Debug, Clone, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SettingsPayload {
    pub note: String,
}

pub fn encode_cbor<T: Serialize>(value: &T) -> Result<Vec<u8>, String> {
    let mut buf = Vec::new();
    ciborium::ser::into_writer(value, &mut buf).map_err(|err| err.to_string())?;
    Ok(buf)
}

pub fn decode_cbor<T: for<'de> Deserialize<'de>>(bytes: &[u8]) -> Result<T, String> {
    ciborium::de::from_reader(bytes).map_err(|err| err.to_string())
}
