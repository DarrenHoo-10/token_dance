use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use flate2::write::GzEncoder;
use flate2::Compression;
use protocol::{RejectedEvent, RejectedEventErrorCode, UploadAck, UploadBatch};
use serde_json::Value;

use crate::error::TransportError;

#[async_trait]
pub trait IngestTransport: Send + Sync {
    async fn upload(&self, batch: &UploadBatch) -> Result<UploadAck, TransportError>;
}

#[derive(Clone, Default)]
pub struct MemoryIngest {
    inner: Arc<Mutex<MemoryState>>,
}

#[derive(Default)]
struct MemoryState {
    batches: HashMap<String, UploadAck>,
    events: HashSet<String>,
    bodies: Vec<Vec<u8>>,
}

impl MemoryIngest {
    pub fn bodies(&self) -> Vec<Vec<u8>> {
        self.inner.lock().expect("memory ingest").bodies.clone()
    }

    pub fn seen_event(&self, event_id: &str) -> bool {
        self.inner
            .lock()
            .expect("memory ingest")
            .events
            .contains(event_id)
    }
}

#[async_trait]
impl IngestTransport for MemoryIngest {
    async fn upload(&self, batch: &UploadBatch) -> Result<UploadAck, TransportError> {
        let encoded =
            serde_json::to_vec(batch).map_err(|err| TransportError::Decode(err.to_string()))?;
        let mut inner = self.inner.lock().expect("memory ingest");
        inner.bodies.push(encoded);
        if let Some(existing) = inner.batches.get(&batch.batch_id).cloned() {
            return Ok(existing);
        }
        let mut accepted = 0u32;
        let mut duplicates = 0u32;
        let mut rejected = Vec::new();
        for event in &batch.events {
            if inner.events.contains(&event.event_id) {
                duplicates += 1;
                continue;
            }
            match validate_event(event) {
                Ok(()) => {
                    inner.events.insert(event.event_id.clone());
                    accepted += 1;
                }
                Err(item) => rejected.push(item),
            }
        }
        let ack = UploadAck {
            batch_id: batch.batch_id.clone(),
            accepted,
            duplicates,
            rejected,
            server_time: "2026-08-30T00:00:00.000Z".into(),
        };
        inner.batches.insert(batch.batch_id.clone(), ack.clone());
        Ok(ack)
    }
}

fn validate_event(event: &protocol::EventEnvelope) -> Result<(), RejectedEvent> {
    if event.event_id.len() != 43 {
        return Err(RejectedEvent {
            event_id: event.event_id.clone(),
            error_code: RejectedEventErrorCode::SchemaInvalid,
            retryable: false,
        });
    }
    Ok(())
}

pub struct ScriptedTransport {
    pub script: Mutex<Vec<ScriptStep>>,
    pub bodies: Mutex<Vec<Vec<u8>>>,
}

pub enum ScriptStep {
    NetworkFail,
    Http {
        status: u16,
        retry_after: Option<Duration>,
    },
    Ack(UploadAck),
    Partial {
        accepted_ids: Vec<String>,
        rejected: Vec<RejectedEvent>,
    },
}

impl ScriptedTransport {
    pub fn new(script: Vec<ScriptStep>) -> Self {
        Self {
            script: Mutex::new(script),
            bodies: Mutex::new(Vec::new()),
        }
    }

    pub fn bodies(&self) -> Vec<Vec<u8>> {
        self.bodies.lock().expect("bodies").clone()
    }
}

#[async_trait]
impl IngestTransport for ScriptedTransport {
    async fn upload(&self, batch: &UploadBatch) -> Result<UploadAck, TransportError> {
        let encoded =
            serde_json::to_vec(batch).map_err(|err| TransportError::Decode(err.to_string()))?;
        self.bodies.lock().expect("bodies").push(encoded);
        let step = {
            let mut script = self.script.lock().expect("script");
            if script.is_empty() {
                None
            } else {
                Some(script.remove(0))
            }
        };
        match step {
            Some(ScriptStep::NetworkFail) => Err(TransportError::Network("disconnected".into())),
            Some(ScriptStep::Http {
                status,
                retry_after,
            }) => {
                if status == 401 || status == 403 {
                    Err(TransportError::Auth)
                } else {
                    Err(TransportError::Http {
                        status,
                        retry_after,
                        body: String::new(),
                    })
                }
            }
            Some(ScriptStep::Ack(ack)) => Ok(ack),
            Some(ScriptStep::Partial {
                accepted_ids,
                rejected,
            }) => Ok(UploadAck {
                batch_id: batch.batch_id.clone(),
                accepted: accepted_ids.len() as u32,
                duplicates: 0,
                rejected,
                server_time: "2026-08-30T00:00:00.000Z".into(),
            }),
            None => Ok(UploadAck {
                batch_id: batch.batch_id.clone(),
                accepted: batch.events.len() as u32,
                duplicates: 0,
                rejected: Vec::new(),
                server_time: "2026-08-30T00:00:00.000Z".into(),
            }),
        }
    }
}

pub struct HttpTransport {
    pub base_url: String,
    pub client: reqwest::Client,
    pub authorization: String,
}

#[async_trait]
impl IngestTransport for HttpTransport {
    async fn upload(&self, batch: &UploadBatch) -> Result<UploadAck, TransportError> {
        let json =
            serde_json::to_vec(batch).map_err(|err| TransportError::Decode(err.to_string()))?;
        let mut encoder = GzEncoder::new(Vec::new(), Compression::default());
        use std::io::Write;
        let gzipped = encoder
            .write_all(&json)
            .and_then(|_| encoder.finish())
            .map_err(|err| TransportError::Network(err.to_string()))?;
        let response = self
            .client
            .post(format!(
                "{}/v1/telemetry/batches",
                self.base_url.trim_end_matches('/')
            ))
            .header("content-type", "application/json")
            .header("content-encoding", "gzip")
            .header("authorization", &self.authorization)
            .header("idempotency-key", &batch.batch_id)
            .body(gzipped)
            .send()
            .await
            .map_err(|err| {
                if err.is_timeout() {
                    TransportError::Timeout
                } else {
                    TransportError::Network(err.to_string())
                }
            })?;
        let status = response.status().as_u16();
        let retry_after = response
            .headers()
            .get("retry-after")
            .and_then(|value| value.to_str().ok())
            .and_then(|text| text.parse::<u64>().ok())
            .map(Duration::from_secs);
        let body = response
            .text()
            .await
            .map_err(|err| TransportError::Decode(err.to_string()))?;
        match status {
            200..=202 => {
                serde_json::from_str(&body).map_err(|err| TransportError::Decode(err.to_string()))
            }
            401 | 403 => Err(TransportError::Auth),
            other => Err(TransportError::Http {
                status: other,
                retry_after,
                body,
            }),
        }
    }
}

pub fn body_contains_canary(bodies: &[Vec<u8>], canary: &str) -> bool {
    bodies.iter().any(|body| {
        String::from_utf8_lossy(body).contains(canary)
            || serde_json::from_slice::<Value>(body)
                .ok()
                .is_some_and(|value| value.to_string().contains(canary))
    })
}
