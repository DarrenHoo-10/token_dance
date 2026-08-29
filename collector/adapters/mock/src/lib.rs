//! Fixture-backed Adapter used to prove the host/Core contracts.

#![forbid(unsafe_code)]

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityReport, ConfigMutation, EventEnvelope, EventPayload,
    EventSource, EventType, NormalizedEvent, ProbeContext, ProbeReport, RawFrame, RollbackStep,
    SetupContext, SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::{
    ModelUsageRecordedPayload, SessionEndReason, SessionEndedPayload, SessionStartedPayload,
    SkillInvokeType, SkillInvokedPayload, TurnCompletedPayload, TurnStartedPayload, TurnTrigger,
};
use serde_json::Value;

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.mock";
pub const ADAPTER_VERSION: &str = "1.0.0";
pub const AGENT_ID: &str = "mock";
pub const SOURCE_ID: &str = "mock-sessions";
pub const EVENT_ID_KEY: &[u8] = b"tokshow-mock-event-id-key";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const MIN_SESSION_JSONL: &str = include_str!("../fixtures/contract/min-session.jsonl");
pub const UNKNOWN_FIELDS_JSONL: &str = include_str!("../fixtures/contract/unknown-fields.jsonl");

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("mock adapter manifest fixture")
}

pub struct MockAdapter {
    manifest: AdapterManifest,
    pub detected: bool,
    pub needs_permission: bool,
    pub needs_setup: bool,
    pub panic_on_probe: bool,
    pub panic_on_decode: bool,
    pub available: Vec<Capability>,
}

impl Default for MockAdapter {
    fn default() -> Self {
        Self::new()
    }
}

impl MockAdapter {
    pub fn new() -> Self {
        let manifest = load_manifest();
        let available = manifest.capabilities.clone();
        Self {
            manifest,
            detected: true,
            needs_permission: false,
            needs_setup: false,
            panic_on_probe: false,
            panic_on_decode: false,
            available,
        }
    }

    pub fn undetected() -> Self {
        let mut adapter = Self::new();
        adapter.detected = false;
        adapter.available.clear();
        adapter
    }

    pub fn panicking() -> Self {
        let mut adapter = Self::new();
        adapter.panic_on_probe = true;
        adapter
    }

    pub fn with_id(mut self, id: impl Into<String>) -> Self {
        self.manifest.id = id.into();
        self
    }

    pub fn with_available(mut self, available: Vec<Capability>) -> Self {
        self.available = available;
        self
    }
}

#[async_trait]
impl AgentAdapter for MockAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        if self.panic_on_probe {
            panic!("mock adapter probe panic");
        }
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: Some("1.0.0".into()),
            needs_permission: self.needs_permission,
            needs_setup: self.needs_setup,
            capability: CapabilityReport::from_declared(
                &self.manifest.id,
                &self.manifest.version,
                &self.manifest.capabilities,
                &self.available,
            ),
            detail: None,
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "plan-mock-1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Enable mock session collection".into(),
            mutations: vec![ConfigMutation::JsonMergePatch {
                path_template: "${AGENT_CONFIG_HOME}/config.json".into(),
                patch: serde_json::json!({"telemetry":{"enabled":true}}),
            }],
            required_permissions: vec![],
            verify: vec![VerifyStep {
                id: "syntax".into(),
                summary: "config is JSON".into(),
            }],
            rollback: vec![RollbackStep {
                id: "restore".into(),
                summary: "restore previous config".into(),
            }],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        Ok(vec![SourceSpec::JsonlTail {
            id: SOURCE_ID.into(),
            path_template: "${AGENT_CONFIG_HOME}/sessions/**".into(),
        }])
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if self.panic_on_decode {
            panic!("mock adapter decode panic");
        }
        decode_jsonl(&self.manifest, &frame)
    }

    async fn health(&self) -> AdapterHealth {
        if !self.detected {
            AdapterHealth::Healthy
        } else if self.available.len() < self.manifest.capabilities.len() {
            AdapterHealth::Degraded {
                reason: "declared capabilities are only partially available".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

pub fn decode_jsonl(
    manifest: &AdapterManifest,
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let text = std::str::from_utf8(&frame.payload)
        .map_err(|err| AdapterError::decode_failed(err.to_string()))?;
    let mut events = Vec::new();
    for (index, line) in text.lines().enumerate() {
        let line = line.trim();
        if line.is_empty() {
            continue;
        }
        let line_no = (index + 1) as u64;
        if let Some(event) = decode_line(manifest, frame, line, line_no)? {
            events.push(event);
        }
    }
    Ok(events)
}

fn decode_line(
    manifest: &AdapterManifest,
    frame: &RawFrame,
    line: &str,
    line_no: u64,
) -> Result<Option<NormalizedEvent>, AdapterError> {
    let value: Value = match serde_json::from_str(line) {
        Ok(value) => value,
        Err(_) => return Ok(None),
    };
    let Some(object) = value.as_object() else {
        return Ok(None);
    };
    let Some(type_name) = object.get("type").and_then(Value::as_str) else {
        return Ok(None);
    };
    let event_type = match parse_event_type(type_name) {
        Some(event_type) => event_type,
        None => return Ok(None),
    };
    let occurred_at = object
        .get("occurredAt")
        .and_then(Value::as_str)
        .ok_or_else(|| AdapterError::decode_failed("record missing occurredAt"))?
        .to_string();
    let session_id = object.get("sessionId").and_then(Value::as_str);
    let turn_id = object.get("turnId").and_then(Value::as_str);
    let cursor = format!("{}:{line_no}", frame.cursor);
    let semantic_type = event_type_name(event_type);
    let event_id = event_id(
        EVENT_ID_KEY,
        &frame.installation_id,
        &manifest.id,
        &frame.source_id,
        &cursor,
        semantic_type,
        &line_no.to_string(),
    );
    let payload = match event_type {
        EventType::SessionStarted => EventPayload::SessionStarted(SessionStartedPayload {
            model_id: opt_string(object.get("modelId")),
            workspace_hash: None,
        }),
        EventType::SessionEnded => EventPayload::SessionEnded(SessionEndedPayload {
            reason: parse_end_reason(object.get("reason")),
            duration_ms: json_u64_string(object.get("durationMs")),
        }),
        EventType::TurnStarted => EventPayload::TurnStarted(TurnStartedPayload {
            trigger: parse_trigger(object.get("trigger")),
        }),
        EventType::TurnCompleted => EventPayload::TurnCompleted(TurnCompletedPayload {
            success: object
                .get("success")
                .and_then(Value::as_bool)
                .unwrap_or(false),
            duration_ms: json_u64_string(object.get("durationMs")),
            error_class: opt_string(object.get("errorClass")),
        }),
        EventType::ModelUsageRecorded => {
            let Some(provider_id) = opt_string(object.get("providerId")) else {
                return Ok(None);
            };
            let Some(model_id) = opt_string(object.get("modelId")) else {
                return Ok(None);
            };
            EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                provider_id,
                model_id,
                tokens: object
                    .get("tokens")
                    .and_then(parse_tokens)
                    .unwrap_or_else(empty_tokens),
            })
        }
        EventType::SkillInvoked => {
            let Some(skill) = opt_string(object.get("skillKey")) else {
                return Ok(None);
            };
            EventPayload::SkillInvoked(SkillInvokedPayload {
                skill_key: format!("hmac-sha256:{}", keyed_hmac(EVENT_ID_KEY, &[&skill])),
                invoke_type: parse_invoke_type(object.get("skillInvokeType")),
                success: object
                    .get("success")
                    .and_then(Value::as_bool)
                    .unwrap_or(true),
                plugin_key: None,
                duration_ms: json_u64_string(object.get("durationMs")),
            })
        }
        EventType::ToolInvoked
        | EventType::CodeChanged
        | EventType::CostRecorded
        | EventType::AgentSpawned => return Ok(None),
    };
    Ok(Some(EventEnvelope {
        schema_version: "1.0".into(),
        event_id,
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: Some("1.0.0".into()),
        installation_id: frame.installation_id.clone(),
        occurred_at,
        session_hash: session_id
            .map(|session| format!("hmac-sha256:{}", keyed_hmac(EVENT_ID_KEY, &[session]))),
        turn_hash: turn_id.map(|turn| {
            format!(
                "hmac-sha256:{}",
                keyed_hmac(EVENT_ID_KEY, &[session_id.unwrap_or_default(), turn])
            )
        }),
        tool_call_hash: None,
        source: EventSource {
            kind: SourceKind::JsonlTail,
            cursor_hmac: format!("hmac-sha256:{}", keyed_hmac(EVENT_ID_KEY, &[&cursor])),
            raw_fingerprint_hmac: format!(
                "hmac-sha256:{}",
                keyed_hmac(EVENT_ID_KEY, &[&raw_fingerprint(line.as_bytes())])
            ),
        },
        accuracy: Accuracy::Exact,
        payload,
    }))
}

fn parse_event_type(name: &str) -> Option<EventType> {
    match name {
        "session_started" => Some(EventType::SessionStarted),
        "session_ended" => Some(EventType::SessionEnded),
        "turn_started" => Some(EventType::TurnStarted),
        "turn_completed" => Some(EventType::TurnCompleted),
        "model_usage_recorded" => Some(EventType::ModelUsageRecorded),
        "tool_invoked" => Some(EventType::ToolInvoked),
        "skill_invoked" => Some(EventType::SkillInvoked),
        "code_changed" => Some(EventType::CodeChanged),
        "cost_recorded" => Some(EventType::CostRecorded),
        "agent_spawned" => Some(EventType::AgentSpawned),
        _ => None,
    }
}

fn event_type_name(event_type: EventType) -> &'static str {
    match event_type {
        EventType::SessionStarted => "session_started",
        EventType::SessionEnded => "session_ended",
        EventType::TurnStarted => "turn_started",
        EventType::TurnCompleted => "turn_completed",
        EventType::ModelUsageRecorded => "model_usage_recorded",
        EventType::ToolInvoked => "tool_invoked",
        EventType::SkillInvoked => "skill_invoked",
        EventType::CodeChanged => "code_changed",
        EventType::CostRecorded => "cost_recorded",
        EventType::AgentSpawned => "agent_spawned",
    }
}

fn opt_string(value: Option<&Value>) -> Option<String> {
    value.and_then(Value::as_str).map(ToOwned::to_owned)
}

fn json_u64_string(value: Option<&Value>) -> Option<String> {
    match value? {
        Value::Null => None,
        Value::Number(number) => number.as_u64().map(|n| n.to_string()),
        Value::String(text) => Some(text.clone()),
        _ => None,
    }
}

fn parse_tokens(value: &Value) -> Option<TokenUsage> {
    let object = value.as_object()?;
    Some(TokenUsage {
        input_tokens: json_u64_string(object.get("inputTokens")),
        output_tokens: json_u64_string(object.get("outputTokens")),
        cache_read_tokens: json_u64_string(object.get("cacheReadTokens")),
        cache_write_tokens: json_u64_string(object.get("cacheWriteTokens")),
        reasoning_tokens: json_u64_string(object.get("reasoningTokens")),
        tool_tokens: json_u64_string(object.get("toolTokens")),
        total_tokens: json_u64_string(object.get("totalTokens")),
    })
}

fn empty_tokens() -> TokenUsage {
    TokenUsage {
        input_tokens: None,
        output_tokens: None,
        cache_read_tokens: None,
        cache_write_tokens: None,
        reasoning_tokens: None,
        tool_tokens: None,
        total_tokens: None,
    }
}

fn parse_end_reason(value: Option<&Value>) -> SessionEndReason {
    match value.and_then(Value::as_str) {
        Some("completed") => SessionEndReason::Completed,
        Some("cancelled") => SessionEndReason::Cancelled,
        Some("error") => SessionEndReason::Error,
        Some("timeout") => SessionEndReason::Timeout,
        _ => SessionEndReason::Unknown,
    }
}

fn parse_trigger(value: Option<&Value>) -> Option<TurnTrigger> {
    match value.and_then(Value::as_str) {
        Some("user") => Some(TurnTrigger::User),
        Some("system") => Some(TurnTrigger::System),
        Some("scheduled") => Some(TurnTrigger::Scheduled),
        Some("subagent") => Some(TurnTrigger::Subagent),
        _ => None,
    }
}

fn parse_invoke_type(value: Option<&Value>) -> SkillInvokeType {
    match value.and_then(Value::as_str) {
        Some("hook") => SkillInvokeType::Hook,
        Some("tool_correlated") => SkillInvokeType::ToolCorrelated,
        Some("runtime_correlated") => SkillInvokeType::RuntimeCorrelated,
        _ => SkillInvokeType::Native,
    }
}

#[cfg(test)]
mod tests {
    use adapter_sdk::{validate_manifest, AgentAdapter, ProbeContext, RawFrame};

    use super::*;

    #[test]
    fn fixture_manifest_is_valid() {
        validate_manifest(&load_manifest()).unwrap();
    }

    #[tokio::test]
    async fn decode_skips_unknown_event_types() {
        let adapter = MockAdapter::new();
        let frame = RawFrame::jsonl(
            "ins_01test",
            SOURCE_ID,
            "0",
            UNKNOWN_FIELDS_JSONL.as_bytes(),
        );
        let events = adapter.decode(frame).await.unwrap();
        assert_eq!(events.len(), 2);
        assert!(matches!(events[0].payload, EventPayload::SessionStarted(_)));
        assert!(matches!(events[1].payload, EventPayload::SkillInvoked(_)));
        let encoded = serde_json::to_string(&events).unwrap();
        assert!(!encoded.contains("TOKSHOW_TEST_PROMPT_SECRET"));
        assert!(!encoded.contains("unknownField"));
        assert!(!encoded.contains("future_event_v2"));
    }

    #[tokio::test]
    async fn probe_reports_declared_capabilities() {
        let adapter = MockAdapter::new();
        let report = adapter
            .probe(ProbeContext {
                installation_id: "ins_01test".into(),
            })
            .await
            .unwrap();
        assert!(report.detected);
        assert_eq!(report.capability.available().len(), 6);
    }
}
