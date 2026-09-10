#![forbid(unsafe_code)]

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, CapabilityAvailability, CapabilityReport, CapabilityStatus, EventEnvelope,
    EventPayload, EventSource, NormalizedEvent, ProbeContext, ProbeReport, RawFrame, SetupContext,
    SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage,
};
use async_trait::async_trait;
use protocol::{
    CodeChangedPayload, ModelUsageRecordedPayload, SessionStartedPayload, TurnCompletedPayload,
};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.workbuddy";
pub const HISTORY_SOURCE_ID: &str = "workbuddy-sessions";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const KNOWN_JSON: &str = include_str!("../fixtures/contract/known.json");

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("WorkBuddy manifest")
}

pub struct WorkBuddyAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    hmac_key: Vec<u8>,
}

impl WorkBuddyAdapter {
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

    fn capability_report(&self) -> CapabilityReport {
        let available = self.detected;
        CapabilityReport {
            adapter_id: self.manifest.id.clone(),
            adapter_version: self.manifest.version.clone(),
            capabilities: self
                .manifest
                .capabilities
                .iter()
                .copied()
                .map(|capability| CapabilityStatus {
                    capability,
                    availability: if available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy: available.then_some(Accuracy::Exact),
                    safe_reason_code: (!available).then(|| "WORKBUDDY_NOT_DETECTED".into()),
                })
                .collect(),
        }
    }
}

#[async_trait]
impl AgentAdapter for WorkBuddyAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: self.agent_version.clone(),
            needs_permission: false,
            needs_setup: false,
            capability: self.capability_report(),
            detail: None,
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "workbuddy-readonly-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Read WorkBuddy local session JSONL without uploading prompts".into(),
            mutations: vec![],
            required_permissions: vec![],
            verify: vec![],
            rollback: vec![],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        if !self.detected {
            return Ok(vec![]);
        }
        Ok(vec![SourceSpec::JsonlTail {
            id: HISTORY_SOURCE_ID.into(),
            path_template: "${AGENT_CONFIG_HOME}/**/*.jsonl".into(),
        }])
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if frame.source_kind != SourceKind::JsonlTail || frame.source_id != HISTORY_SOURCE_ID {
            return Err(AdapterError::decode_failed("unsupported WorkBuddy source"));
        }
        decode_jsonl(&self.manifest, self.agent_version.as_deref(), &self.hmac_key, &frame)
    }

    async fn health(&self) -> AdapterHealth {
        if self.detected {
            AdapterHealth::Healthy
        } else {
            AdapterHealth::Degraded {
                reason: "WorkBuddy is not installed on this device".into(),
            }
        }
    }
}

pub fn decode_jsonl(
    manifest: &AdapterManifest,
    version: Option<&str>,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let value: Value = serde_json::from_slice(&frame.payload)
        .map_err(|error| AdapterError::decode_failed(error.to_string()))?;
    decode_record(manifest, version, hmac_key, frame, &value, 1)
}

fn decode_record(
    manifest: &AdapterManifest,
    version: Option<&str>,
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
    let turn = string(object, "stepId");
    let model = string(object, "model").unwrap_or_else(|| "unknown".into());
    let provider = string(object, "provider").unwrap_or_else(|| "workbuddy".into());
    let payloads = match kind {
        "session" => vec![(
            EventPayload::SessionStarted(SessionStartedPayload {
                model_id: Some(model),
                workspace_hash: None,
            }),
            Accuracy::Exact,
            kind,
        )],
        "step_finish" | "model_usage" => vec![
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
                        total_tokens: number(object, "totalTokens"),
                    },
                }),
                Accuracy::Exact,
                "model_usage",
            ),
            (
                EventPayload::TurnCompleted(TurnCompletedPayload {
                    success: true,
                    duration_ms: number(object, "durationMs"),
                    error_class: None,
                }),
                Accuracy::Derived,
                "turn_completed",
            ),
        ],
        "turn_completed" => vec![(
            EventPayload::TurnCompleted(TurnCompletedPayload {
                success: object
                    .get("success")
                    .and_then(Value::as_bool)
                    .unwrap_or(true),
                duration_ms: number(object, "durationMs"),
                error_class: string(object, "errorClass"),
            }),
            Accuracy::Exact,
            kind,
        )],
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
                    language: string(object, "language"),
                }),
                Accuracy::Derived,
                kind,
            )]
        }
        _ => return Ok(vec![]),
    };
    let mut sanitized = object.clone();
    sanitized.remove("prompt");
    sanitized.remove("text");
    sanitized.remove("content");
    let raw = serde_json::to_vec(&Value::Object(sanitized))
        .map_err(|error| AdapterError::decode_failed(error.to_string()))?;
    let mut events = Vec::with_capacity(payloads.len());
    for (offset, (payload, accuracy, event_kind)) in payloads.into_iter().enumerate() {
        let cursor = format!("{}:{sequence}:{offset}", frame.cursor);
        events.push(EventEnvelope {
            schema_version: "1.0".into(),
            event_id: event_id(
                hmac_key,
                &frame.installation_id,
                &manifest.id,
                &frame.source_id,
                &cursor,
                event_kind,
                &format!("{sequence}:{offset}"),
            ),
            adapter_id: manifest.id.clone(),
            adapter_version: manifest.version.clone(),
            agent_id: manifest.agent.id.clone(),
            agent_version: version.map(str::to_owned),
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

fn hash(hmac_key: &[u8], value: &str) -> String {
    format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[value]))
}

fn string(object: &Map<String, Value>, key: &str) -> Option<String> {
    object.get(key).and_then(Value::as_str).map(str::to_owned)
}

fn number(object: &Map<String, Value>, key: &str) -> Option<String> {
    match object.get(key)? {
        Value::String(value) => Some(value.clone()),
        Value::Number(value) => Some(value.to_string()),
        _ => None,
    }
}
