use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use flate2::write::GzEncoder;
use flate2::Compression;
use protocol::{
    Architecture, InstallationRegisterRequest, InstallationRegisterResponse, OsType, RejectedEvent,
    RejectedEventErrorCode, UploadAck, UploadBatch,
};
use rand::rngs::OsRng;
use rand::RngCore;
use serde_json::Value;
use sha2::{Digest, Sha256};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

use crate::error::TransportError;
use crate::signer::DeviceSigner;

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

const INGEST_PATH: &str = "/v1/telemetry/batches";
const REGISTER_PATH: &str = "/v1/installations/register";

pub fn canonical_request(
    method: &str,
    path: &str,
    timestamp: &str,
    nonce: &str,
    body_sha256: &[u8; 32],
) -> String {
    format!(
        "{}\n{}\n{}\n{}\n{}",
        method.to_ascii_uppercase(),
        path,
        timestamp,
        nonce,
        URL_SAFE_NO_PAD.encode(body_sha256)
    )
}

pub struct HttpTransport {
    base_url: String,
    client: reqwest::Client,
    installation_id: String,
    signer: Arc<dyn DeviceSigner>,
}

impl HttpTransport {
    pub fn new(
        base_url: impl Into<String>,
        client: reqwest::Client,
        installation_id: impl Into<String>,
        signer: Arc<dyn DeviceSigner>,
    ) -> Self {
        Self {
            base_url: base_url.into(),
            client,
            installation_id: installation_id.into(),
            signer,
        }
    }
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

        let timestamp = OffsetDateTime::now_utc()
            .format(&Rfc3339)
            .map_err(|err| TransportError::Signing(err.to_string()))?;
        let mut nonce_bytes = [0u8; 24];
        OsRng.fill_bytes(&mut nonce_bytes);
        let nonce = URL_SAFE_NO_PAD.encode(nonce_bytes);
        let body_sha256: [u8; 32] = Sha256::digest(&gzipped).into();
        let canonical = canonical_request(
            reqwest::Method::POST.as_str(),
            INGEST_PATH,
            &timestamp,
            &nonce,
            &body_sha256,
        );
        let signature = self
            .signer
            .sign(canonical.as_bytes())
            .map_err(|err| TransportError::Signing(err.to_string()))?;
        let authorization = format!(
            "Device {}:{}",
            self.installation_id,
            URL_SAFE_NO_PAD.encode(signature)
        );

        let response = self
            .client
            .post(format!(
                "{}{}",
                self.base_url.trim_end_matches('/'),
                INGEST_PATH
            ))
            .header("content-type", "application/json")
            .header("content-encoding", "gzip")
            .header("authorization", authorization)
            .header("x-timestamp", timestamp)
            .header("x-nonce", nonce)
            .header("idempotency-key", &batch.batch_id)
            .body(gzipped)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        decode_response(response).await
    }
}

pub struct RegistrationClient {
    base_url: String,
    client: reqwest::Client,
    user_session_token: String,
}

pub struct RegisteredCollector {
    pub registration: InstallationRegisterResponse,
    pub transport: HttpTransport,
}

impl RegistrationClient {
    pub fn new(
        base_url: impl Into<String>,
        client: reqwest::Client,
        user_session_token: impl Into<String>,
    ) -> Self {
        Self {
            base_url: base_url.into(),
            client,
            user_session_token: user_session_token.into(),
        }
    }

    pub async fn register(
        self,
        signer: Arc<dyn DeviceSigner>,
        os_type: OsType,
        architecture: Architecture,
        collector_version: impl Into<String>,
    ) -> Result<RegisteredCollector, TransportError> {
        let public_key = signer
            .public_key()
            .map_err(|err| TransportError::Signing(err.to_string()))?;
        let request = InstallationRegisterRequest {
            device_public_key: URL_SAFE_NO_PAD.encode(public_key),
            os_type,
            architecture,
            collector_version: collector_version.into(),
        };
        let response = self
            .client
            .post(format!(
                "{}{}",
                self.base_url.trim_end_matches('/'),
                REGISTER_PATH
            ))
            .bearer_auth(&self.user_session_token)
            .json(&request)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        let registration: InstallationRegisterResponse = decode_response(response).await?;
        let transport = HttpTransport::new(
            self.base_url,
            self.client,
            registration.installation_id.clone(),
            signer,
        );
        Ok(RegisteredCollector {
            registration,
            transport,
        })
    }
}

async fn decode_response<T: serde::de::DeserializeOwned>(
    response: reqwest::Response,
) -> Result<T, TransportError> {
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

fn map_reqwest_error(err: reqwest::Error) -> TransportError {
    if err.is_timeout() {
        TransportError::Timeout
    } else {
        TransportError::Network(err.to_string())
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
