#![forbid(unsafe_code)]

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext, ProbeReport, RawFrame,
    SetupContext, SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage,
};
use async_trait::async_trait;
use protocol::{
    CodeChangedPayload, ModelUsageRecordedPayload, SessionStartedPayload, TurnCompletedPayload,
};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.opencode";
pub const SQLITE_SOURCE_ID: &str = "opencode-sqlite";
pub const FINGERPRINT_V1: &str = "opencode-sqlite-v1-uv0";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const KNOWN_JSON: &str = include_str!("../fixtures/contract/known.json");

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("OpenCode manifest")
}

pub fn fingerprint_supported(fingerprint: &str) -> bool {
    fingerprint == FINGERPRINT_V1
}

pub struct OpenCodeAdapter {
    manifest: AdapterManifest,
    version: String,
    fingerprint: String,
    detected: bool,
    hmac_key: Vec<u8>,
}

impl OpenCodeAdapter {
    pub fn new(
        version: impl Into<String>,
        fingerprint: impl Into<String>,
        hmac_key: impl Into<Vec<u8>>,
    ) -> Self {
        Self {
            manifest: load_manifest(),
            version: version.into(),
            fingerprint: fingerprint.into(),
            detected: true,
            hmac_key: hmac_key.into(),
        }
    }

    pub fn undetected(hmac_key: impl Into<Vec<u8>>) -> Self {
        Self {
            manifest: load_manifest(),
            version: "0".into(),
            fingerprint: "unverified".into(),
            detected: false,
            hmac_key: hmac_key.into(),
        }
    }

    fn schema_known(&self) -> bool {
        fingerprint_supported(&self.fingerprint)
    }

    fn capability_report(&self) -> CapabilityReport {
        let schema = self.schema_known();
        let capabilities = self
            .manifest
            .capabilities
            .iter()
            .copied()
            .map(|capability| CapabilityStatus {
                capability,
                availability: if schema {
                    CapabilityAvailability::Available
                } else {
                    CapabilityAvailability::Unavailable
                },
                accuracy: schema.then_some(Accuracy::Exact),
                safe_reason_code: (!schema)
                    .then(|| "OPENCODE_SCHEMA_FINGERPRINT_UNSUPPORTED".into()),
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
impl AgentAdapter for OpenCodeAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: Some(self.version.clone()),
            needs_permission: false,
            needs_setup: false,
            capability: self.capability_report(),
            detail: Some(if self.schema_known() {
                format!("schema_fingerprint={}", self.fingerprint)
            } else {
                "当前 OpenCode 版本尚未适配".into()
            }),
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "opencode-readonly-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Read OpenCode session metrics from a fingerprint-verified SQLite snapshot"
                .into(),
            mutations: vec![],
            required_permissions: vec![],
            verify: vec![],
            rollback: vec![],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        if !self.schema_known() {
            return Ok(vec![]);
        }
        Ok(vec![SourceSpec::SqliteSnapshot {
            id: SQLITE_SOURCE_ID.into(),
            path_template: "${AGENT_CONFIG_HOME}/opencode.db".into(),
        }])
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if frame.source_kind != SourceKind::SqliteSnapshot || frame.source_id != SQLITE_SOURCE_ID {
            return Err(AdapterError::decode_failed(
                "unsupported OpenCode source",
            ));
        }
        if !self.schema_known() {
            return Ok(vec![]);
        }
        let root: Value = serde_json::from_slice(&frame.payload)
            .map_err(|error| AdapterError::decode_failed(error.to_string()))?;
        if root.get("fingerprint").and_then(Value::as_str) != Some(self.fingerprint.as_str()) {
            return Ok(vec![]);
        }
        decode_records(&self.manifest, &self.version, &self.hmac_key, &frame)
    }

    async fn health(&self) -> AdapterHealth {
        if self.schema_known() {
            AdapterHealth::Healthy
        } else {
            AdapterHealth::Degraded {
                reason: "当前 OpenCode 版本尚未适配；未知 schema 不执行 SQL".into(),
            }
        }
    }
}

fn decode_records(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let root: Value = serde_json::from_slice(&frame.payload)
        .map_err(|error| AdapterError::decode_failed(error.to_string()))?;
    let records = root
        .get("records")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let mut events = Vec::new();
    for (index, record) in records.iter().enumerate() {
        events.extend(decode_record(
            manifest,
            version,
            hmac_key,
            frame,
            record,
            index + 1,
        )?);
    }
    Ok(events)
}

fn decode_record(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
    value: &Value,
    sequence: usize,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let Some(object) = value.as_object() else {
        return Ok(vec![]);
    };
    let kind = object.get("type").and_then(Value::as_str).unwrap_or("");
    let session = string(object, "sessionId");
    // Row ids from the sqlite snapshot arrive as JSON numbers; identity must
    // accept them or it degrades to the batch sequence and dedupe eats every
    // incremental poll after the initial rescan.
    let turn = id_string(object, "stepId").or_else(|| id_string(object, "id"));
    let (provider, model) = model_parts(
        string(object, "provider").unwrap_or_else(|| "opencode".into()),
        string(object, "model").unwrap_or_else(|| "unknown".into()),
    );
    let payloads = match kind {
        "session" => vec![(
            EventPayload::SessionStarted(SessionStartedPayload {
                model_id: Some(model),
                workspace_hash: None,
            }),
            Accuracy::Exact,
            kind,
        )],
        "step_finish" => vec![
            (
                EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                    provider_id: provider,
                    model_id: model,
                    tokens: TokenUsage {
                        input_tokens: number(object, "inputTokens"),
                        output_tokens: number(object, "outputTokens"),
                        cache_read_tokens: number(object, "cacheReadTokens"),
                        cache_write_tokens: number(object, "cacheWriteTokens"),
                        reasoning_tokens: number(object, "reasoningTokens"),
                        tool_tokens: None,
                        total_tokens: None,
                    },
                }),
                Accuracy::Exact,
                kind,
            ),
            (
                EventPayload::TurnCompleted(TurnCompletedPayload {
                    success: true,
                    duration_ms: None,
                    error_class: None,
                }),
                Accuracy::Derived,
                "turn_completed",
            ),
        ],
        "code_changed" => {
            let added = number(object, "addedLines").unwrap_or_else(|| "0".into());
            let removed = number(object, "removedLines").unwrap_or_else(|| "0".into());
            if added == "0" && removed == "0" {
                return Ok(vec![]);
            }
            let files = object
                .get("fileCount")
                .and_then(Value::as_u64)
                .or_else(|| number(object, "fileCount")?.parse().ok())
                .unwrap_or(1)
                .max(1) as u32;
            vec![(
                EventPayload::CodeChanged(CodeChangedPayload {
                    added_lines: added.clone(),
                    removed_lines: removed,
                    generated_lines: Some(added),
                    accepted_lines: None,
                    file_count: files,
                    language: None,
                }),
                Accuracy::Derived,
                kind,
            )]
        }
        _ => return Ok(vec![]),
    };
    let raw = serde_json::to_vec(value).map_err(|error| AdapterError::decode_failed(error.to_string()))?;
    let mut events = Vec::with_capacity(payloads.len());
    for (offset, (payload, accuracy, event_kind)) in payloads.into_iter().enumerate() {
        let cursor = format!("{}:{sequence}:{offset}", frame.cursor);
        // Identity must survive sqlite rescans: poll cursors move whenever the
        // snapshot is rebuilt, so they must never feed the event id. Every
        // source row carries a stable id (`part.id`, session id, callId); the
        // batch sequence is only a last-resort fallback for runtime frames.
        let identity = match event_kind {
            "session" => session
                .clone()
                .unwrap_or_else(|| sequence.to_string()),
            "step_finish" | "turn_finish" | "skill" | "tool" => turn
                .clone()
                .or_else(|| id_string(object, "toolCallId"))
                .unwrap_or_else(|| sequence.to_string()),
            "code_changed" => id_string(object, "callId")
                .or_else(|| id_string(object, "call_id"))
                .or_else(|| {
                    session.as_deref().map(|id| {
                        format!(
                            "code:{id}:{}",
                            string(object, "timestamp").unwrap_or_default()
                        )
                    })
                })
                .unwrap_or_else(|| sequence.to_string()),
            _ => sequence.to_string(),
        };
        events.push(EventEnvelope {
            schema_version: "1.0".into(),
            event_id: event_id(
                hmac_key,
                &frame.installation_id,
                &manifest.id,
                "opencode-row",
                &identity,
                event_kind,
                "1",
            ),
            adapter_id: manifest.id.clone(),
            adapter_version: manifest.version.clone(),
            agent_id: manifest.agent.id.clone(),
            agent_version: Some(version.into()),
            installation_id: frame.installation_id.clone(),
            occurred_at: string(object, "timestamp").unwrap_or_else(|| "1970-01-01T00:00:00Z".into()),
            session_hash: session.as_deref().map(|value| hash(hmac_key, value)),
            turn_hash: turn.as_deref().map(|id| {
                hash(
                    hmac_key,
                    &format!("{}\x1f{id}", session.as_deref().unwrap_or("")),
                )
            }),
            tool_call_hash: None,
            source: EventSource {
                kind: frame.source_kind,
                cursor_hmac: format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[&cursor])),
                raw_fingerprint_hmac: format!(
                    "hmac-sha256:{}",
                    keyed_hmac(hmac_key, &[&raw_fingerprint(&raw)])
                ),
            },
            accuracy,
            payload,
        });
    }
    Ok(events)
}

fn model_parts(provider: String, model: String) -> (String, String) {
    let Ok(value) = serde_json::from_str::<Value>(&model) else {
        return (sanitize_id(&provider), sanitize_id(&model));
    };
    let id = value
        .get("id")
        .and_then(Value::as_str)
        .unwrap_or("unknown");
    let provider = value
        .get("providerID")
        .or_else(|| value.get("providerId"))
        .and_then(Value::as_str)
        .unwrap_or(&provider);
    (sanitize_id(provider), sanitize_id(id))
}

fn sanitize_id(value: &str) -> String {
    let cleaned: String = value
        .chars()
        .filter(|ch| ch.is_ascii_alphanumeric() || matches!(ch, '.' | '_' | ':' | '-' | '+'))
        .collect();
    if cleaned.is_empty() {
        "unknown".into()
    } else {
        cleaned
    }
}

fn hash(hmac_key: &[u8], value: &str) -> String {
    format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[value]))
}

fn string(object: &Map<String, Value>, key: &str) -> Option<String> {
    object.get(key).and_then(Value::as_str).map(str::to_owned)
}

/// Reads an id that may be stored as a JSON string or number; sqlite row ids
/// arrive as numbers and must still yield a stable identity string.
fn id_string(object: &Map<String, Value>, key: &str) -> Option<String> {
    match object.get(key) {
        Some(Value::String(value)) => Some(value.clone()),
        Some(Value::Number(number)) => Some(number.to_string()),
        _ => None,
    }
}

fn number(object: &Map<String, Value>, key: &str) -> Option<String> {
    match object.get(key)? {
        Value::String(value) => Some(value.clone()),
        Value::Number(value) => Some(value.to_string()),
        _ => None,
    }
}
