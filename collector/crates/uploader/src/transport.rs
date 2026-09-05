use std::collections::{HashMap, HashSet};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use base64::engine::general_purpose::URL_SAFE_NO_PAD;
use base64::Engine as _;
use flate2::write::GzEncoder;
use flate2::Compression;
use protocol::{
    Architecture, EventEnvelope, InstallationRegisterResponse, OsType, RejectedEvent,
    RejectedEventErrorCode, UploadAck, UploadBatch,
};
use rand::rngs::OsRng;
use rand::RngCore;
use serde_json::{json, Map, Value};
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

#[derive(Clone)]
pub struct HttpTransport {
    base_url: String,
    client: reqwest::Client,
    installation_id: String,
    signer: Arc<dyn DeviceSigner>,
    claimed_protocol: bool,
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
            claimed_protocol: false,
        }
    }

    pub fn new_claimed(
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
            claimed_protocol: true,
        }
    }

    pub fn installation_id(&self) -> &str {
        &self.installation_id
    }
}

#[async_trait]
impl IngestTransport for HttpTransport {
    async fn upload(&self, batch: &UploadBatch) -> Result<UploadAck, TransportError> {
        let json = if self.claimed_protocol {
            encode_claimed_batch(batch)?
        } else {
            serde_json::to_vec(batch).map_err(|err| TransportError::Decode(err.to_string()))?
        };
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
        let body_sha256_hex = body_sha256
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        let canonical = if self.claimed_protocol {
            format!("POST\n{INGEST_PATH}\n{timestamp}\n{nonce}\n{body_sha256_hex}")
        } else {
            canonical_request(
                reqwest::Method::POST.as_str(),
                INGEST_PATH,
                &timestamp,
                &nonce,
                &body_sha256,
            )
        };
        let signature = self
            .signer
            .sign(canonical.as_bytes())
            .map_err(|err| TransportError::Signing(err.to_string()))?;
        let authorization = format!(
            "Device {}:{}",
            self.installation_id,
            URL_SAFE_NO_PAD.encode(signature)
        );

        let mut request = self
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
            .header("idempotency-key", &batch.batch_id);
        if self.claimed_protocol {
            request = request.header("x-body-sha256", body_sha256_hex);
        }
        let response = request
            .body(gzipped)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        if self.claimed_protocol {
            decode_claimed_response(response).await
        } else {
            decode_response(response).await
        }
    }
}

async fn decode_claimed_response(response: reqwest::Response) -> Result<UploadAck, TransportError> {
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
        .map_err(|error| TransportError::Decode(error.to_string()))?;
    match status {
        200..=202 => decode_claimed_ack(&body),
        401 | 403 => Err(TransportError::Auth),
        other => Err(TransportError::Http {
            status: other,
            retry_after,
            body,
        }),
    }
}

fn decode_claimed_ack(body: &str) -> Result<UploadAck, TransportError> {
    let mut value: Value =
        serde_json::from_str(body).map_err(|error| TransportError::Decode(error.to_string()))?;
    if let Some(object) = value.as_object_mut() {
        object.remove("installationId");
        if let Some(Value::Array(rejected)) = object.get_mut("rejected") {
            for rejection in rejected {
                // The claimed-installation API returns {eventId, code}; the
                // protocol transport returns {eventId, errorCode, retryable}.
                // Normalize only the former, retaining strict ACK validation.
                if let Some(item) = rejection.as_object_mut() {
                    if item.contains_key("code")
                        && !item.contains_key("errorCode")
                        && !item.contains_key("retryable")
                    {
                        let code = item
                            .remove("code")
                            .and_then(|code| code.as_str().map(str::to_owned))
                            .filter(|code| !code.is_empty())
                            .ok_or_else(|| {
                                TransportError::Decode("invalid rejection code".into())
                            })?;
                        let mapped = match code.as_str() {
                            "FORBIDDEN_METADATA" | "PRIVACY_REJECTED" => "PRIVACY_REJECTED",
                            "EVENT_TOO_LARGE" => "EVENT_TOO_LARGE",
                            "UNSUPPORTED_VERSION" => "UNSUPPORTED_VERSION",
                            "INTERNAL_RETRYABLE" => "INTERNAL_RETRYABLE",
                            _ => "SCHEMA_INVALID",
                        };
                        item.insert("errorCode".into(), json!(mapped));
                        item.insert("retryable".into(), json!(mapped == "INTERNAL_RETRYABLE"));
                    }
                }
            }
        }
    }
    serde_json::from_value(value).map_err(|error| TransportError::Decode(error.to_string()))
}

#[cfg(test)]
mod claimed_ack_tests {
    use super::*;

    #[test]
    fn partial_server_ack_preserves_counts_and_rejected_ids() {
        let ack = decode_claimed_ack(r#"{"batchId":"batch-1","installationId":"ins_1","accepted":22,"duplicates":0,"rejected":[{"eventId":"event-1","code":"INVALID_EVENT_SOURCE"}],"serverTime":"2026-09-06T00:00:00Z"}"#).unwrap();
        assert_eq!(ack.accepted, 22);
        assert_eq!(ack.duplicates, 0);
        assert_eq!(ack.rejected[0].event_id, "event-1");
        assert_eq!(
            ack.rejected[0].error_code,
            RejectedEventErrorCode::SchemaInvalid
        );
        assert!(!ack.rejected[0].retryable);
    }

    #[test]
    fn protocol_ack_and_retryable_rejections_still_work() {
        let ack = decode_claimed_ack(r#"{"batchId":"batch-1","accepted":0,"duplicates":1,"rejected":[{"eventId":"event-1","errorCode":"INTERNAL_RETRYABLE","retryable":true}],"serverTime":"2026-09-06T00:00:00Z"}"#).unwrap();
        assert_eq!(ack.duplicates, 1);
        assert!(ack.rejected[0].retryable);
    }

    #[test]
    fn malformed_rejections_cannot_be_acknowledged() {
        for rejection in [
            json!({"code": "INVALID_EVENT_SOURCE"}),
            json!({"eventId":"e", "code":42}),
            json!({"eventId":"e", "code":"INVALID_EVENT_SOURCE", "unexpected":true}),
        ] {
            let body = json!({"batchId":"b", "accepted":0,"duplicates":0,"rejected":[rejection],"serverTime":"2026-09-06T00:00:00Z"});
            assert!(decode_claimed_ack(&body.to_string()).is_err());
        }
    }
}

fn encode_claimed_batch(batch: &UploadBatch) -> Result<Vec<u8>, TransportError> {
    let events = batch
        .events
        .iter()
        .map(flatten_claimed_event)
        .collect::<Result<Vec<_>, _>>()?;
    serde_json::to_vec(&json!({
        "batchId": batch.batch_id,
        "events": events,
    }))
    .map_err(|error| TransportError::Decode(error.to_string()))
}

fn flatten_claimed_event(event: &EventEnvelope) -> Result<Value, TransportError> {
    let encoded =
        serde_json::to_value(event).map_err(|error| TransportError::Decode(error.to_string()))?;
    let mut flat = encoded
        .as_object()
        .cloned()
        .ok_or_else(|| TransportError::Decode("event envelope must be an object".into()))?;
    flat.remove("installationId");

    let schema_version = flat
        .remove("schemaVersion")
        .and_then(|value| {
            value
                .as_str()
                .and_then(|text| text.split('.').next())
                .and_then(|text| text.parse::<u64>().ok())
        })
        .unwrap_or(1);
    flat.insert("schemaVersion".into(), Value::from(schema_version));
    flat.insert("privacyPolicyVersion".into(), Value::from(1));

    let source_kind = flat
        .remove("source")
        .and_then(|value| {
            value
                .as_object()
                .and_then(|source| source.get("kind"))
                .cloned()
        })
        .and_then(|value| value.as_str().map(str::to_owned))
        .unwrap_or_else(|| "runtime_stream".into());
    flat.insert(
        "sourceKind".into(),
        Value::String(if source_kind == "jsonl_tail" {
            "jsonl".into()
        } else {
            source_kind
        }),
    );

    let mut payload = flat
        .remove("payload")
        .and_then(|value| value.as_object().cloned())
        .ok_or_else(|| TransportError::Decode("event payload must be an object".into()))?;
    let event_type = payload
        .remove("type")
        .and_then(|value| value.as_str().map(str::to_owned))
        .ok_or_else(|| TransportError::Decode("event payload type is required".into()))?;
    flat.insert("eventType".into(), Value::String(event_type.clone()));

    match event_type.as_str() {
        "session_started" => move_value(&mut payload, &mut flat, "modelId", "modelId"),
        "session_ended" => move_u64(&mut payload, &mut flat, "durationMs", "durationMs"),
        "turn_started" => {
            if let Some(Value::String(trigger)) = payload.remove("trigger") {
                let trigger = match trigger.as_str() {
                    "scheduled" => "automation",
                    "subagent" => "system",
                    other => other,
                };
                flat.insert("turnTrigger".into(), Value::String(trigger.into()));
            }
        }
        "turn_completed" => {
            move_value(&mut payload, &mut flat, "success", "success");
            move_u64(&mut payload, &mut flat, "durationMs", "durationMs");
        }
        "model_usage_recorded" => {
            move_value(&mut payload, &mut flat, "providerId", "providerId");
            move_value(&mut payload, &mut flat, "modelId", "modelId");
            if let Some(Value::Object(mut tokens)) = payload.remove("tokens") {
                for (from, to) in [
                    ("inputTokens", "tokenInput"),
                    ("outputTokens", "tokenOutput"),
                    ("cacheReadTokens", "tokenCacheRead"),
                    ("cacheWriteTokens", "tokenCacheWrite"),
                    ("reasoningTokens", "tokenReasoning"),
                    ("totalTokens", "tokenTotal"),
                ] {
                    move_u64(&mut tokens, &mut flat, from, to);
                }
            }
        }
        "tool_invoked" => {
            move_value(&mut payload, &mut flat, "toolCategory", "toolCategory");
            move_value(&mut payload, &mut flat, "success", "success");
            move_u64(&mut payload, &mut flat, "durationMs", "durationMs");
        }
        "skill_invoked" => {
            move_value(&mut payload, &mut flat, "skillKey", "skillKey");
            move_value(&mut payload, &mut flat, "invokeType", "skillInvokeType");
            move_value(&mut payload, &mut flat, "pluginKey", "pluginKey");
            move_value(&mut payload, &mut flat, "success", "success");
            move_u64(&mut payload, &mut flat, "durationMs", "durationMs");
        }
        "code_changed" => {
            for (from, to) in [
                ("addedLines", "codeAddedLines"),
                ("removedLines", "codeDeletedLines"),
                ("generatedLines", "codeGeneratedLines"),
                ("acceptedLines", "codeAcceptedLines"),
                ("fileCount", "codeFileCount"),
            ] {
                move_u64(&mut payload, &mut flat, from, to);
            }
        }
        "cost_recorded" => {
            move_value(&mut payload, &mut flat, "amount", "costAmount");
            move_value(&mut payload, &mut flat, "currency", "costCurrency");
            move_value(&mut payload, &mut flat, "source", "costSource");
        }
        "agent_spawned" => {}
        _ => {}
    }

    Ok(Value::Object(flat))
}

fn move_value(
    source: &mut Map<String, Value>,
    target: &mut Map<String, Value>,
    from: &str,
    to: &str,
) {
    if let Some(value) = source.remove(from) {
        target.insert(to.into(), value);
    }
}

fn move_u64(
    source: &mut Map<String, Value>,
    target: &mut Map<String, Value>,
    from: &str,
    to: &str,
) {
    let Some(value) = source.remove(from) else {
        return;
    };
    let number = match value {
        Value::Number(number) => Some(number),
        Value::String(text) => text.parse::<u64>().ok().map(serde_json::Number::from),
        _ => None,
    };
    if let Some(number) = number {
        target.insert(to.into(), Value::Number(number));
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
        self.register_with_installation(signer, os_type, architecture, collector_version, None)
            .await
    }

    pub async fn register_with_installation(
        self,
        signer: Arc<dyn DeviceSigner>,
        os_type: OsType,
        architecture: Architecture,
        collector_version: impl Into<String>,
        installation_id: Option<String>,
    ) -> Result<RegisteredCollector, TransportError> {
        let public_key = signer
            .public_key()
            .map_err(|err| TransportError::Signing(err.to_string()))?;
        let mut request = serde_json::Map::new();
        request.insert(
            "devicePublicKey".into(),
            Value::String(URL_SAFE_NO_PAD.encode(public_key)),
        );
        request.insert(
            "osType".into(),
            serde_json::to_value(os_type).map_err(|err| TransportError::Decode(err.to_string()))?,
        );
        request.insert(
            "architecture".into(),
            serde_json::to_value(architecture)
                .map_err(|err| TransportError::Decode(err.to_string()))?,
        );
        request.insert(
            "collectorVersion".into(),
            Value::String(collector_version.into()),
        );
        if let Some(installation_id) = installation_id {
            request.insert("installationId".into(), Value::String(installation_id));
        }
        let response = self
            .client
            .post(format!(
                "{}{}",
                self.base_url.trim_end_matches('/'),
                REGISTER_PATH
            ))
            .bearer_auth(&self.user_session_token)
            .json(&Value::Object(request))
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
