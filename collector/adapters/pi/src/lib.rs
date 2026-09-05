#![forbid(unsafe_code)]

//! Pi coding agent adapter (https://github.com/earendil-works/pi).
//!
//! Pi stores one JSONL file per session under `~/.pi/agent/sessions/`. The
//! first line is a `type: "session"` header (id, timestamp, cwd, version);
//! subsequent lines are tree entries (`id`/`parentId`) whose `type` is
//! `message`, `model_change`, `compaction`, and so on. Only metrics-relevant
//! fields are decoded; message content, tool arguments, and tool output never
//! leave this module.

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext, ProbeReport, RawFrame,
    SetupContext, SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage,
};
use async_trait::async_trait;
use protocol::{
    ModelUsageRecordedPayload, SessionStartedPayload, ToolInvokedPayload, TurnCompletedPayload,
};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.pi";
pub const HISTORY_SOURCE_ID: &str = "pi-sessions";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const KNOWN_JSON: &str = include_str!("../fixtures/contract/known.json");
pub const ERRORS_JSON: &str = include_str!("../fixtures/contract/errors.json");

const MIN_SUPPORTED_VERSION: (u64, u64, u64) = (0, 2, 0);
const MAX_SUPPORTED_VERSION_EXCLUSIVE: (u64, u64, u64) = (2, 0, 0);

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Pi manifest fixture")
}

pub fn version_supported(version: &str) -> bool {
    match version_triplet(version) {
        Some(parsed) => parsed >= MIN_SUPPORTED_VERSION && parsed < MAX_SUPPORTED_VERSION_EXCLUSIVE,
        None => false,
    }
}

/// Pi never tags per-line records with the session id, but every session lives
/// in exactly one file and the JSONL tailer folds a stable file identity into
/// the frame cursor as `<identity>:<generation>:<offset>`. The identity prefix
/// is therefore the only privacy-safe session key available per line.
fn session_identity(cursor: &str) -> &str {
    let mut parts = cursor.rsplitn(3, ':');
    parts.next();
    parts.next();
    parts.next().unwrap_or(cursor)
}

pub struct PiAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    hmac_key: Vec<u8>,
}

impl PiAdapter {
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
            vec![Capability::Tokens, Capability::Sessions]
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
                    accuracy: is_available.then(|| match capability {
                        Capability::Turns => Accuracy::Derived,
                        _ => Accuracy::Exact,
                    }),
                    safe_reason_code: (!is_available)
                        .then(|| "PI_SCHEMA_FINGERPRINT_UNSUPPORTED".into()),
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
impl AgentAdapter for PiAdapter {
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
                Some("unknown Pi version; verified session schema only".into())
            } else {
                None
            },
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "pi-readonly-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Use only read-only Pi session history sources".into(),
            mutations: vec![],
            required_permissions: vec![],
            verify: vec![],
            rollback: vec![],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        Ok(vec![SourceSpec::JsonlTail {
            id: HISTORY_SOURCE_ID.into(),
            path_template: "${AGENT_CONFIG_HOME}/agent/sessions/**".into(),
        }])
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if !matches!(
            (frame.source_kind, frame.source_id.as_str()),
            (SourceKind::JsonlTail, HISTORY_SOURCE_ID)
        ) {
            return Err(AdapterError::decode_failed(
                "frame source kind/id does not match Pi manifest source",
            ));
        }
        decode_jsonl(
            &self.manifest,
            self.agent_version.as_deref(),
            &self.hmac_key,
            &frame,
        )
    }

    async fn health(&self) -> AdapterHealth {
        if self.detected && !self.version_gate_open() {
            AdapterHealth::Degraded {
                reason: "unknown Pi version; session history fallback only".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

pub fn decode_jsonl(
    manifest: &AdapterManifest,
    agent_version: Option<&str>,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let text = std::str::from_utf8(&frame.payload)
        .map_err(|err| AdapterError::decode_failed(err.to_string()))?;
    let identity = session_identity(&frame.cursor);
    let session_hash = hash(hmac_key, &[identity]);
    let mut events = Vec::new();
    for (index, raw) in text.lines().enumerate() {
        let line = raw.trim();
        if line.is_empty() {
            continue;
        }
        let line_no = index + 1;
        let value: Value = serde_json::from_str(line).map_err(|err| {
            AdapterError::decode_failed(format!("invalid JSON at line {line_no}: {err}"))
        })?;
        let Some(object) = value.as_object() else {
            continue;
        };
        let entry_type = object.get("type").and_then(Value::as_str).unwrap_or("");
        let entry_id = object.get("id").and_then(Value::as_str);
        let cursor = format!("{}:{line_no}", frame.cursor);
        let occurred_at =
            string(object, "timestamp").unwrap_or_else(|| "1970-01-01T00:00:00Z".into());
        match entry_type {
            "session" => events.push(envelope(
                EnvelopeInput {
                    manifest,
                    agent_version,
                    hmac_key,
                    frame,
                    cursor: &cursor,
                    raw_line: line.as_bytes(),
                    occurred_at,
                    session_hash: Some(session_hash.clone()),
                    turn_hash: None,
                    tool_call_hash: None,
                    kind: "session_started",
                    sequence: "0",
                    accuracy: Accuracy::Exact,
                },
                EventPayload::SessionStarted(SessionStartedPayload {
                    model_id: None,
                    workspace_hash: string(object, "cwd").map(|cwd| hash(hmac_key, &[&cwd])),
                }),
            )),
            "message" => {
                let Some(message) = object.get("message").and_then(Value::as_object) else {
                    continue;
                };
                let role = message.get("role").and_then(Value::as_str).unwrap_or("");
                let turn_hash = entry_id.map(|id| hash(hmac_key, &[identity, id]));
                match role {
                    "assistant" => {
                        if let Some(usage) = message.get("usage").and_then(Value::as_object) {
                            events.push(envelope(
                                EnvelopeInput {
                                    manifest,
                                    agent_version,
                                    hmac_key,
                                    frame,
                                    cursor: &cursor,
                                    raw_line: line.as_bytes(),
                                    occurred_at: occurred_at.clone(),
                                    session_hash: Some(session_hash.clone()),
                                    turn_hash: turn_hash.clone(),
                                    tool_call_hash: None,
                                    kind: "model_usage_recorded",
                                    sequence: "0",
                                    accuracy: Accuracy::Exact,
                                },
                                EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                                    provider_id: string(message, "provider")
                                        .unwrap_or_else(|| "unknown".into()),
                                    model_id: string(message, "model")
                                        .unwrap_or_else(|| "unknown".into()),
                                    tokens: TokenUsage {
                                        input_tokens: number(usage, "input"),
                                        output_tokens: number(usage, "output"),
                                        cache_read_tokens: number(usage, "cacheRead"),
                                        cache_write_tokens: number(usage, "cacheWrite"),
                                        reasoning_tokens: None,
                                        tool_tokens: None,
                                        total_tokens: number(usage, "totalTokens"),
                                    },
                                }),
                            ));
                        }
                        let Some(stop_reason) = message.get("stopReason").and_then(Value::as_str)
                        else {
                            continue;
                        };
                        let (success, error_class) = match stop_reason {
                            "stop" | "toolUse" => (true, None),
                            "error" => (false, Some("error".into())),
                            "aborted" => (false, Some("aborted".into())),
                            "length" => (false, Some("length".into())),
                            _ => (false, None),
                        };
                        events.push(envelope(
                            EnvelopeInput {
                                manifest,
                                agent_version,
                                hmac_key,
                                frame,
                                cursor: &cursor,
                                raw_line: line.as_bytes(),
                                occurred_at,
                                session_hash: Some(session_hash.clone()),
                                turn_hash,
                                tool_call_hash: None,
                                kind: "turn_completed",
                                sequence: "1",
                                accuracy: Accuracy::Derived,
                            },
                            EventPayload::TurnCompleted(TurnCompletedPayload {
                                success,
                                duration_ms: None,
                                error_class,
                            }),
                        ));
                    }
                    "toolResult" => {
                        let Some(tool_call_id) = message.get("toolCallId").and_then(Value::as_str)
                        else {
                            continue;
                        };
                        events.push(envelope(
                            EnvelopeInput {
                                manifest,
                                agent_version,
                                hmac_key,
                                frame,
                                cursor: &cursor,
                                raw_line: line.as_bytes(),
                                occurred_at,
                                session_hash: Some(session_hash.clone()),
                                turn_hash: None,
                                tool_call_hash: Some(hash(hmac_key, &[tool_call_id])),
                                kind: "tool_invoked",
                                sequence: "0",
                                accuracy: Accuracy::Exact,
                            },
                            EventPayload::ToolInvoked(ToolInvokedPayload {
                                tool_category: string(message, "toolName")
                                    .unwrap_or_else(|| "other".into()),
                                success: !message
                                    .get("isError")
                                    .and_then(Value::as_bool)
                                    .unwrap_or(false),
                                duration_ms: None,
                            }),
                        ));
                    }
                    _ => {}
                }
            }
            _ => {}
        }
    }
    Ok(events)
}

struct EnvelopeInput<'a> {
    manifest: &'a AdapterManifest,
    agent_version: Option<&'a str>,
    hmac_key: &'a [u8],
    frame: &'a RawFrame,
    cursor: &'a str,
    raw_line: &'a [u8],
    occurred_at: String,
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
        occurred_at,
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
