//! Protocol v2 telemetry transport: capabilities + signed event ingest (P5/P7).

use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use protocol::v2::{
    EventAck, TelemetryCapabilities, TelemetryEventsRequest, TelemetryEventsResponse,
    DEFAULT_MAX_BATCH_BYTES, DEFAULT_MAX_BATCH_EVENTS, PROTOCOL_VERSION_NUMBER,
};
use rand::rngs::OsRng;
use rand::RngCore;
use sha2::{Digest, Sha256};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

use crate::error::TransportError;
use crate::signer::DeviceSigner;

pub const TELEMETRY_EVENTS_PATH: &str = "/v2/telemetry/events";
pub const TELEMETRY_CAPABILITIES_PATH: &str = "/v2/telemetry/capabilities";

/// Client-side upload batch caps (stricter than server defaults).
pub const CLIENT_MAX_BATCH_EVENTS: usize = 200;
pub const CLIENT_MAX_BATCH_BYTES: usize = 512 * 1024;
pub const CLIENT_MAX_IN_FLIGHT: usize = 4;
pub const CLIENT_HTTP_TIMEOUT: Duration = Duration::from_secs(15);
pub const CLIENT_LEASE_MS: i64 = 30_000;
pub const CLIENT_LEASE_RENEW_MS: i64 = 10_000;

pub fn canonical_request_v2(
    method: &str,
    path: &str,
    timestamp: &str,
    nonce: &str,
    body_sha256_hex: &str,
    installation_id: &str,
    binding_status_version: &str,
) -> String {
    format!(
        "{}\n{}\n{}\n{}\n{}\n{}\n{}",
        method.to_ascii_uppercase(),
        if path.is_empty() { "/" } else { path },
        timestamp,
        nonce,
        body_sha256_hex.to_ascii_lowercase(),
        installation_id,
        binding_status_version
    )
}

pub fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|b| format!("{b:02x}"))
        .collect()
}

#[derive(Debug, Clone)]
pub struct V2UploadAuth {
    pub session_bearer: String,
    pub binding_status_version: u64,
}

#[async_trait]
pub trait TelemetryV2Transport: Send + Sync {
    async fn capabilities(&self) -> Result<TelemetryCapabilities, TransportError>;
    async fn upload_events(
        &self,
        auth: &V2UploadAuth,
        body: &[u8],
    ) -> Result<TelemetryEventsResponse, TransportError>;
}

#[derive(Clone)]
pub struct HttpTelemetryV2 {
    base_url: String,
    client: reqwest::Client,
    installation_id: String,
    signer: Arc<dyn DeviceSigner>,
}

impl HttpTelemetryV2 {
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

    pub fn installation_id(&self) -> &str {
        &self.installation_id
    }

    pub fn encode_request(request: &TelemetryEventsRequest) -> Result<Vec<u8>, TransportError> {
        serde_json::to_vec(request).map_err(|e| TransportError::Decode(e.to_string()))
    }
}

#[async_trait]
impl TelemetryV2Transport for HttpTelemetryV2 {
    async fn capabilities(&self) -> Result<TelemetryCapabilities, TransportError> {
        let url = format!(
            "{}{}",
            self.base_url.trim_end_matches('/'),
            TELEMETRY_CAPABILITIES_PATH
        );
        let response = self
            .client
            .get(url)
            .timeout(CLIENT_HTTP_TIMEOUT)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        decode_json(response).await
    }

    async fn upload_events(
        &self,
        auth: &V2UploadAuth,
        body: &[u8],
    ) -> Result<TelemetryEventsResponse, TransportError> {
        if body.len() > DEFAULT_MAX_BATCH_BYTES as usize {
            return Err(TransportError::Decode("INGEST_BODY_TOO_LARGE".into()));
        }
        let timestamp = OffsetDateTime::now_utc()
            .format(&Rfc3339)
            .map_err(|e| TransportError::Signing(e.to_string()))?;
        let mut nonce_bytes = [0u8; 24];
        OsRng.fill_bytes(&mut nonce_bytes);
        let nonce = URL_SAFE_NO_PAD.encode(nonce_bytes);
        let body_hash_hex = sha256_hex(body);
        let binding = auth.binding_status_version.to_string();
        let canonical = canonical_request_v2(
            "POST",
            TELEMETRY_EVENTS_PATH,
            &timestamp,
            &nonce,
            &body_hash_hex,
            &self.installation_id,
            &binding,
        );
        let signature = self
            .signer
            .sign(canonical.as_bytes())
            .map_err(|e| TransportError::Signing(e.to_string()))?;
        let device_auth = format!(
            "Device {}:{}",
            self.installation_id,
            URL_SAFE_NO_PAD.encode(signature)
        );
        let url = format!(
            "{}{}",
            self.base_url.trim_end_matches('/'),
            TELEMETRY_EVENTS_PATH
        );
        let response = self
            .client
            .post(url)
            .timeout(CLIENT_HTTP_TIMEOUT)
            .header("content-type", "application/json")
            .header("authorization", format!("Bearer {}", auth.session_bearer))
            .header("x-device-authorization", device_auth)
            .header("x-timestamp", timestamp)
            .header("x-nonce", nonce)
            .header("x-body-sha256", body_hash_hex)
            .header("x-binding-status-version", binding)
            .body(body.to_vec())
            .send()
            .await
            .map_err(map_reqwest_error)?;
        decode_json(response).await
    }
}

async fn decode_json<T: serde::de::DeserializeOwned>(
    response: reqwest::Response,
) -> Result<T, TransportError> {
    let status = response.status().as_u16();
    let retry_after = response
        .headers()
        .get("retry-after")
        .and_then(|h| h.to_str().ok())
        .and_then(|s| s.parse::<u64>().ok())
        .map(Duration::from_secs);
    let body = response
        .text()
        .await
        .map_err(|e| TransportError::Decode(e.to_string()))?;
    match status {
        200..=202 => serde_json::from_str(&body).map_err(|e| TransportError::Decode(e.to_string())),
        401 | 403 => Err(TransportError::Auth),
        other => Err(TransportError::Http {
            status: other,
            retry_after,
            body,
        }),
    }
}

fn map_reqwest_error(error: reqwest::Error) -> TransportError {
    if error.is_timeout() {
        TransportError::Timeout
    } else {
        TransportError::Network(error.to_string())
    }
}

/// Scripted v2 transport for unit tests (partial ACK / auth / retry).
pub struct ScriptedTelemetryV2 {
    pub script: Mutex<Vec<V2ScriptStep>>,
    pub bodies: Mutex<Vec<Vec<u8>>>,
    pub last_auth: Mutex<Option<V2UploadAuth>>,
}

pub enum V2ScriptStep {
    NetworkFail,
    Timeout,
    Auth,
    Http {
        status: u16,
        retry_after: Option<Duration>,
    },
    Response(TelemetryEventsResponse),
}

impl ScriptedTelemetryV2 {
    pub fn new(script: Vec<V2ScriptStep>) -> Self {
        Self {
            script: Mutex::new(script),
            bodies: Mutex::new(Vec::new()),
            last_auth: Mutex::new(None),
        }
    }

    pub fn bodies(&self) -> Vec<Vec<u8>> {
        self.bodies.lock().expect("bodies").clone()
    }
}

#[async_trait]
impl TelemetryV2Transport for ScriptedTelemetryV2 {
    async fn capabilities(&self) -> Result<TelemetryCapabilities, TransportError> {
        Ok(TelemetryCapabilities {
            protocol_version: PROTOCOL_VERSION_NUMBER,
            supported_schema_versions: vec![2],
            supported_metric_semantics_versions: vec![1],
            max_batch_events: DEFAULT_MAX_BATCH_EVENTS,
            max_batch_bytes: DEFAULT_MAX_BATCH_BYTES,
            server_time_ms: "1735689600000".into(),
            event_receive_lower_bound_ms: "1734393600000".into(),
        })
    }

    async fn upload_events(
        &self,
        auth: &V2UploadAuth,
        body: &[u8],
    ) -> Result<TelemetryEventsResponse, TransportError> {
        self.bodies.lock().expect("bodies").push(body.to_vec());
        *self.last_auth.lock().expect("auth") = Some(auth.clone());
        let step = {
            let mut script = self.script.lock().expect("script");
            if script.is_empty() {
                None
            } else {
                Some(script.remove(0))
            }
        };
        match step {
            Some(V2ScriptStep::NetworkFail) => Err(TransportError::Network("disconnected".into())),
            Some(V2ScriptStep::Timeout) => Err(TransportError::Timeout),
            Some(V2ScriptStep::Auth) => Err(TransportError::Auth),
            Some(V2ScriptStep::Http {
                status,
                retry_after,
            }) => Err(TransportError::Http {
                status,
                retry_after,
                body: String::new(),
            }),
            Some(V2ScriptStep::Response(resp)) => Ok(resp),
            None => {
                let req: TelemetryEventsRequest = serde_json::from_slice(body)
                    .map_err(|e| TransportError::Decode(e.to_string()))?;
                Ok(TelemetryEventsResponse {
                    request_id: req.request_id,
                    server_time_ms: "1735689600000".into(),
                    acks: req
                        .events
                        .iter()
                        .map(|e| EventAck {
                            event_id: e.event_id.clone(),
                            content_hash: e.content_hash.clone(),
                            result: protocol::v2::AckResult::Accepted,
                            code: None,
                        })
                        .collect(),
                })
            }
        }
    }
}

/// Build a frozen request body from events; returns (request_id, body_bytes, body_sha256_hex).
pub fn freeze_events_request(
    events: Vec<protocol::v2::EventEnvelope>,
) -> Result<(String, Vec<u8>, String), TransportError> {
    freeze_events_request_with_reconstruction(events, false)
}

pub fn freeze_events_request_with_reconstruction(
    events: Vec<protocol::v2::EventEnvelope>,
    reconstruction: bool,
) -> Result<(String, Vec<u8>, String), TransportError> {
    if events.is_empty() {
        return Err(TransportError::Decode("empty batch".into()));
    }
    if events.len() > CLIENT_MAX_BATCH_EVENTS {
        return Err(TransportError::Decode("batch event limit".into()));
    }
    let request_id = format!(
        "req_{}",
        URL_SAFE_NO_PAD.encode({
            let mut b = [0u8; 16];
            OsRng.fill_bytes(&mut b);
            b
        })
    );
    let request = TelemetryEventsRequest {
        protocol_version: PROTOCOL_VERSION_NUMBER,
        reconstruction: reconstruction.then_some(true),
        request_id: request_id.clone(),
        events,
    };
    let body = HttpTelemetryV2::encode_request(&request)?;
    if body.len() > CLIENT_MAX_BATCH_BYTES {
        return Err(TransportError::Decode("batch byte limit".into()));
    }
    let hash = sha256_hex(&body);
    Ok((request_id, body, hash))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::InMemoryDeviceSigner;
    use ed25519_dalek::{Signature, Verifier, VerifyingKey};

    #[test]
    fn v2_canonical_includes_installation_and_binding() {
        let canonical = canonical_request_v2(
            "post",
            "/v2/telemetry/events",
            "2026-09-11T00:00:00.000000000Z",
            "nonce1",
            "AABB",
            "ins_test",
            "3",
        );
        assert_eq!(
            canonical,
            "POST\n/v2/telemetry/events\n2026-09-11T00:00:00.000000000Z\nnonce1\naabb\nins_test\n3"
        );
    }

    #[test]
    fn device_key_signs_v2_canonical() {
        let signer = InMemoryDeviceSigner::from_seed([9; 32]);
        let canonical = canonical_request_v2(
            "POST",
            TELEMETRY_EVENTS_PATH,
            "2026-09-11T00:00:00Z",
            "AQEBAQEBAQEBAQEBAQEBAQ",
            &sha256_hex(b"{}"),
            "ins_aaaaaaaaaaaaaaaaaaaaaaaaaa",
            "1",
        );
        let key = VerifyingKey::from_bytes(&signer.public_key().unwrap()).unwrap();
        let signature = Signature::from_bytes(&signer.sign(canonical.as_bytes()).unwrap());
        key.verify(canonical.as_bytes(), &signature).unwrap();
    }

    #[test]
    fn freeze_rejects_oversize_event_count() {
        let events = (0..CLIENT_MAX_BATCH_EVENTS + 1)
            .map(|i| protocol::v2::EventEnvelope {
                event_id: "A".repeat(43),
                fact_key: "B".repeat(43),
                fact_revision: "1".into(),
                schema_version: 2,
                metric_semantics_version: 1,
                harness_id: "codex".into(),
                event_type: protocol::v2::EventType::ModelUsageRecorded,
                occurred_at: "1700000000000".into(),
                model: None,
                skill: None,
                session_key: None,
                turn_key: None,
                cost_scope_key: None,
                payload: protocol::v2::EventPayload {
                    usage: None,
                    cost: None,
                    code: None,
                    activity: None,
                    context: None,
                    meta: Some(protocol::v2::MetaPayload {
                        accuracy: protocol::v2::Accuracy::Exact,
                        time_source: protocol::v2::TimeSource::SourceRecord,
                        safe_tags: None,
                    }),
                },
                content_hash: format!("{:043}", i % 10),
            })
            .collect::<Vec<_>>();
        let err = freeze_events_request(events).unwrap_err();
        assert!(matches!(err, TransportError::Decode(_)));
    }
}
