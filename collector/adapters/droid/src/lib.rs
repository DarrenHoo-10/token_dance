#![forbid(unsafe_code)]

//! Droid (Factory) adapter.
//!
//! droid appends one `[Agent] Streaming result` line per LLM response to
//! `~/.factory/logs/droid-log-single.log`, truncated on every daemon start.
//! Only metrics-relevant fields (timestamp, token components, hashed
//! model/session identities) are decoded; prompt content, tool arguments, and
//! tool output never leave this module.

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext, ProbeReport, RawFrame,
    SetupContext, SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage,
};
use async_trait::async_trait;
use protocol::ModelUsageRecordedPayload;
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.droid";
pub const LOG_SOURCE_ID: &str = "droid-logs";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const KNOWN_LOG: &str = include_str!("../fixtures/contract/droid-log-single.log");

const MIN_SUPPORTED_VERSION: (u64, u64, u64) = (0, 1, 0);
const MAX_SUPPORTED_VERSION_EXCLUSIVE: (u64, u64, u64) = (1, 0, 0);

/// Fixed text between the RFC3339 timestamp prefix and the response JSON.
const STREAMING_RESULT_MARKER: &str = ": [Agent] Streaming result | Context: ";

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Droid manifest fixture")
}

pub fn version_supported(version: &str) -> bool {
    match version_triplet(version) {
        Some(parsed) => parsed >= MIN_SUPPORTED_VERSION && parsed < MAX_SUPPORTED_VERSION_EXCLUSIVE,
        None => false,
    }
}

pub struct DroidAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    hmac_key: Vec<u8>,
}

impl DroidAdapter {
    pub fn for_version(version: impl Into<String>, hmac_key: impl Into<Vec<u8>>) -> Self {
        Self {
            manifest: load_manifest(),
            detected: true,
            agent_version: Some(version.into()),
            hmac_key: hmac_key.into(),
        }
    }

    pub fn undetected(hmac_key: impl Into<Vec<u8>>) -> Self {
        Self {
            manifest: load_manifest(),
            detected: false,
            agent_version: None,
            hmac_key: hmac_key.into(),
        }
    }

    fn version_gate_open(&self) -> bool {
        self.agent_version.as_deref().is_some_and(version_supported)
    }

    fn available_capabilities(&self) -> Vec<Capability> {
        if !self.detected {
            vec![]
        } else if self.version_gate_open() {
            self.manifest.capabilities.clone()
        } else {
            vec![Capability::Tokens]
        }
    }

    fn capability_report(&self) -> CapabilityReport {
        let available = self.available_capabilities();
        let capabilities = self
            .manifest
            .capabilities
            .iter()
            .copied()
            .map(|capability| {
                let is_available = available.contains(&capability);
                CapabilityStatus {
                    capability,
                    availability: if is_available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy: is_available.then_some(Accuracy::Exact),
                    safe_reason_code: (!is_available)
                        .then(|| "DROID_SCHEMA_FINGERPRINT_UNSUPPORTED".into()),
                }
            })
            .collect();
        CapabilityReport {
            adapter_id: self.manifest.id.clone(),
            adapter_version: self.manifest.version.clone(),
            capabilities,
        }
    }
}

#[async_trait]
impl AgentAdapter for DroidAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let supported = self.version_gate_open();
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: self.agent_version.clone(),
            needs_permission: false,
            needs_setup: self.detected && supported,
            capability: self.capability_report(),
            detail: if self.detected && !supported {
                Some("unknown droid version; verified log schema only".into())
            } else {
                None
            },
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "droid-readonly-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Use only read-only droid log sources".into(),
            mutations: vec![],
            required_permissions: vec![],
            verify: vec![],
            rollback: vec![],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        Ok(vec![SourceSpec::JsonlTail {
            id: LOG_SOURCE_ID.into(),
            path_template: "${USER_HOME}/.factory/logs/**".into(),
        }])
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if !matches!(
            (frame.source_kind, frame.source_id.as_str()),
            (SourceKind::JsonlTail, LOG_SOURCE_ID)
        ) {
            return Err(AdapterError::decode_failed(
                "frame source kind/id does not match droid manifest source",
            ));
        }
        decode_log(
            &self.manifest,
            self.agent_version.as_deref(),
            &self.hmac_key,
            &frame,
        )
    }

    async fn health(&self) -> AdapterHealth {
        if self.detected && !self.version_gate_open() {
            AdapterHealth::Degraded {
                reason: "unknown droid version; log fallback only".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

pub fn decode_log(
    manifest: &AdapterManifest,
    agent_version: Option<&str>,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let text = std::str::from_utf8(&frame.payload)
        .map_err(|err| AdapterError::decode_failed(err.to_string()))?;
    let mut events = Vec::new();
    for (index, raw) in text.lines().enumerate() {
        let line = raw.trim();
        let Some(marker_at) = line.find(STREAMING_RESULT_MARKER) else {
            continue;
        };
        let line_no = index + 1;
        let Some(occurred_at) = log_timestamp(line) else {
            continue;
        };
        let context = &line[marker_at + STREAMING_RESULT_MARKER.len()..];
        let Ok(value) = serde_json::from_str::<Value>(context.trim()) else {
            continue;
        };
        let Some(object) = value.as_object() else {
            continue;
        };
        let Some(usage) = streaming_usage(object) else {
            continue;
        };
        let tags = object.get("tags").and_then(Value::as_object);
        let session_id = tags
            .and_then(|tags| string(tags, "sessionId"))
            .filter(|id| !id.is_empty());
        let model_id = tags
            .and_then(|tags| string(tags, "modelId"))
            .filter(|id| !id.is_empty())
            .unwrap_or_else(|| "unknown".into());
        let response_id = string(object, "upstreamResponseId").filter(|id| !id.is_empty());
        let session_hash = session_id.as_deref().map(|id| hash(hmac_key, &[id]));
        let turn_hash = match (&session_id, &response_id) {
            (Some(session), Some(response)) => Some(hash(hmac_key, &[session, response])),
            _ => None,
        };
        events.push(envelope(
            EnvelopeInput {
                manifest,
                agent_version,
                hmac_key,
                frame,
                cursor: &format!("{}:{line_no}", frame.cursor),
                raw_line: line.as_bytes(),
                occurred_at,
                session_hash,
                turn_hash,
                tool_call_hash: None,
                kind: "model_usage_recorded",
                sequence: response_id.as_deref().unwrap_or("0"),
                accuracy: Accuracy::Exact,
            },
            EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                provider_id: "droid".into(),
                model_id,
                tokens: usage,
            }),
        ));
    }
    Ok(events)
}

/// droid's `inputTokens` excludes cached tokens (`totalInputTokens` adds them
/// back) and `reasoningTokens` is disjoint from `outputTokens`.
fn streaming_usage(object: &Map<String, Value>) -> Option<TokenUsage> {
    let input = number(object, "inputTokens")?;
    let output = number(object, "outputTokens")?;
    let cache_read = number(object, "cacheReadInputTokens");
    let cache_write =
        number(object, "cacheWriteTokens").or_else(|| number(object, "cachedTokensWritten"));
    let reasoning = number(object, "reasoningTokens");
    let (input_u, output_u) = (parse_u64(Some(&input)), parse_u64(Some(&output)));
    let (cache_read_u, cache_write_u) = (
        parse_u64(cache_read.as_deref()),
        parse_u64(cache_write.as_deref()),
    );
    let reasoning_u = parse_u64(reasoning.as_deref());
    let total_input_u = number(object, "totalInputTokens")
        .as_deref()
        .and_then(|value| value.parse().ok())
        .unwrap_or_else(|| input_u.saturating_add(cache_read_u));
    let total = total_input_u
        .saturating_add(cache_write_u)
        .saturating_add(output_u)
        .saturating_add(reasoning_u);
    if total == 0 {
        return None;
    }
    Some(TokenUsage {
        input_tokens: Some(input),
        output_tokens: Some(output),
        cache_read_tokens: cache_read,
        cache_write_tokens: cache_write,
        reasoning_tokens: reasoning,
        tool_tokens: None,
        total_tokens: Some(total.to_string()),
    })
}

/// `[2026-10-06T15:02:28.842Z] LEVEL: …` → the RFC3339 timestamp inside brackets.
fn log_timestamp(text: &str) -> Option<&str> {
    let rest = text.strip_prefix('[')?;
    let end = rest.find(']')?;
    rest.get(..end)
}

struct EnvelopeInput<'a> {
    manifest: &'a AdapterManifest,
    agent_version: Option<&'a str>,
    hmac_key: &'a [u8],
    frame: &'a RawFrame,
    cursor: &'a str,
    raw_line: &'a [u8],
    occurred_at: &'a str,
    session_hash: Option<String>,
    turn_hash: Option<String>,
    tool_call_hash: Option<String>,
    kind: &'a str,
    sequence: &'a str,
    accuracy: Accuracy,
}

fn envelope(input: EnvelopeInput<'_>, payload: EventPayload) -> NormalizedEvent {
    let EnvelopeInput {
        manifest,
        agent_version,
        hmac_key,
        frame,
        cursor,
        raw_line,
        occurred_at,
        session_hash,
        turn_hash,
        tool_call_hash,
        kind,
        sequence,
        accuracy,
    } = input;
    EventEnvelope {
        schema_version: "1.0".into(),
        event_id: event_id(
            hmac_key,
            &frame.installation_id,
            &manifest.id,
            &frame.source_id,
            cursor,
            kind,
            sequence,
        ),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: agent_version.map(ToOwned::to_owned),
        installation_id: frame.installation_id.clone(),
        occurred_at: occurred_at.to_string(),
        session_hash,
        turn_hash,
        tool_call_hash,
        source: EventSource {
            kind: frame.source_kind,
            cursor_hmac: format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[cursor])),
            raw_fingerprint_hmac: format!(
                "hmac-sha256:{}",
                keyed_hmac(hmac_key, &[&raw_fingerprint(raw_line)])
            ),
        },
        accuracy,
        payload,
    }
}

fn version_triplet(version: &str) -> Option<(u64, u64, u64)> {
    let mut parts = version.trim_start_matches('v').split('.');
    let parsed = (
        parts.next()?.parse().ok()?,
        parts.next()?.parse().ok()?,
        parts.next()?.parse().ok()?,
    );
    parts.next().is_none().then_some(parsed)
}

fn hash(hmac_key: &[u8], parts: &[&str]) -> String {
    format!("hmac-sha256:{}", keyed_hmac(hmac_key, parts))
}

fn string(object: &Map<String, Value>, key: &str) -> Option<String> {
    object.get(key).and_then(Value::as_str).map(str::to_owned)
}

fn number(object: &Map<String, Value>, key: &str) -> Option<String> {
    object.get(key)?.as_number().map(|value| value.to_string())
}

fn parse_u64(value: Option<&str>) -> u64 {
    value.and_then(|v| v.parse().ok()).unwrap_or(0)
}
