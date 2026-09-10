#![forbid(unsafe_code)]

use std::collections::HashSet;

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityReport, ConfigMutation, EventEnvelope, EventPayload,
    EventSource, NormalizedEvent, ProbeContext, ProbeReport, RawFrame, RollbackStep, SetupContext,
    SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::{
    AgentSpawnedPayload, CodeChangedPayload, ModelUsageRecordedPayload, SessionStartedPayload,
    SkillInvokeType, SkillInvokedPayload, ToolInvokedPayload, TurnCompletedPayload,
};
use serde::Deserialize;
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.claude";
pub const OTLP_SOURCE_ID: &str = "claude-otlp";
pub const HISTORY_SOURCE_ID: &str = "claude-history";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility-matrix.json");
pub const COMPATIBILITY_MARKDOWN: &str = include_str!("../fixtures/compatibility-matrix.md");
const SUPPORTED_MAJOR: u64 = 1;
const MIN_SUPPORTED_VERSION: (u64, u64, u64) = (1, 0, 0);
const MAX_SUPPORTED_VERSION_EXCLUSIVE: (u64, u64, u64) = (2, 0, 0);

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Claude manifest fixture")
}

pub struct ClaudeAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    hmac_key: Vec<u8>,
}

impl ClaudeAdapter {
    pub fn new(hmac_key: impl Into<Vec<u8>>) -> Self {
        Self::for_version("1.0.0", hmac_key)
    }

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

    fn version_supported(&self) -> bool {
        self.agent_version.as_deref().is_some_and(version_supported)
    }

    fn available_capabilities(&self) -> Vec<Capability> {
        if !self.detected {
            vec![]
        } else if self.version_supported() {
            self.manifest.capabilities.clone()
        } else {
            vec![Capability::Tokens, Capability::Sessions, Capability::Turns]
        }
    }
}

#[async_trait]
impl AgentAdapter for ClaudeAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let available = self.available_capabilities();
        let supported = self.version_supported();
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: self.agent_version.clone(),
            needs_permission: false,
            needs_setup: self.detected && supported,
            capability: CapabilityReport::from_declared(
                &self.manifest.id,
                &self.manifest.version,
                &self.manifest.capabilities,
                &available,
            ),
            detail: if self.detected && !supported {
                Some(
                    "unknown Claude Code version; OTLP disabled and verified history schema only"
                        .into(),
                )
            } else {
                None
            },
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "setup-claude-otlp-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary:
                "Enable local Claude Code OTLP without prompt, tool-detail, or content logging"
                    .into(),
            mutations: vec![ConfigMutation::JsonMergePatch {
                path_template: "${USER_HOME}/.claude/settings.json".into(),
                patch: serde_json::json!({
                    "env": {
                        "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
                        "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
                        "OTEL_LOG_USER_PROMPTS": "0",
                        "OTEL_LOG_TOOL_DETAILS": "0",
                        "OTEL_LOG_TOOL_CONTENT": "0"
                    }
                }),
            }],
            required_permissions: vec![],
            verify: vec![VerifyStep {
                id: "claude-otlp-safe".into(),
                summary: "OTLP is loopback and content logging remains disabled".into(),
            }],
            rollback: vec![RollbackStep {
                id: "restore-claude-settings".into(),
                summary: "Restore the previous Claude Code settings".into(),
            }],
        })
    }

    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        let mut sources = Vec::new();
        if self.version_supported() {
            sources.push(SourceSpec::OtlpReceiver {
                id: OTLP_SOURCE_ID.into(),
                bind_host: "127.0.0.1".into(),
                bind_port: Some(4318),
            });
        }
        sources.push(SourceSpec::JsonlTail {
            id: HISTORY_SOURCE_ID.into(),
            path_template: "${USER_HOME}/.claude/projects/**".into(),
        });
        Ok(sources)
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        validate_frame(&frame)?;
        if frame.source_kind == SourceKind::Otlp && !self.version_supported() {
            return Err(AdapterError::decode_failed(
                "OTLP agent version exceeds adapter maximum",
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
        if self.detected && !self.version_supported() {
            AdapterHealth::Degraded {
                reason: "unknown Claude Code version; history fallback only".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

fn validate_frame(frame: &RawFrame) -> Result<(), AdapterError> {
    let valid = matches!(
        (frame.source_kind, frame.source_id.as_str()),
        (SourceKind::Otlp, OTLP_SOURCE_ID) | (SourceKind::JsonlTail, HISTORY_SOURCE_ID)
    );
    if valid {
        Ok(())
    } else {
        Err(AdapterError::decode_failed(
            "frame source kind/id does not match Claude manifest source",
        ))
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
    let mut events = Vec::new();
    let mut terminal_ids = HashSet::new();
    for (index, raw) in text.lines().enumerate() {
        let line = raw.trim();
        if line.is_empty() {
            continue;
        }
        let line_no = index + 1;
        let value: Value = serde_json::from_str(line).map_err(|err| {
            AdapterError::decode_failed(format!("invalid JSON at line {line_no}: {err}"))
        })?;
        for value in normalize_records(value, frame.source_kind)? {
            let object = value.as_object().ok_or_else(|| {
                AdapterError::decode_failed(format!("record at line {line_no} is not an object"))
            })?;
            let record_type = object
                .get("type")
                .and_then(Value::as_str)
                .ok_or_else(|| {
                    AdapterError::decode_failed(format!("record at line {line_no} is missing type"))
                })?
                .to_owned();
            if !known_type(&record_type) {
                continue;
            }
            if matches!(record_type.as_str(), "tool_invoked" | "skill_invoked") {
                let Some(invocation_id) = object.get("toolCallId").and_then(Value::as_str) else {
                    continue;
                };
                if !terminal_ids.insert(format!("{record_type}:{invocation_id}")) {
                    continue;
                }
            }
            if matches!(record_type.as_str(), "model_usage_recorded" | "code_changed") {
                if let Some(identity) = object
                    .get("semanticEventId")
                    .and_then(Value::as_str)
                    .or_else(|| object.get("toolCallId").and_then(Value::as_str))
                {
                    if !terminal_ids.insert(format!("{record_type}:{identity}")) {
                        continue;
                    }
                }
            }
            events.push(decode_record(
                manifest,
                agent_version,
                hmac_key,
                frame,
                RecordInput {
                    value,
                    record_type: &record_type,
                    raw_line: line,
                    line_no,
                },
            )?);
        }
    }
    Ok(events)
}

struct RecordInput<'a> {
    value: Value,
    record_type: &'a str,
    raw_line: &'a str,
    line_no: usize,
}

fn decode_record(
    manifest: &AdapterManifest,
    agent_version: Option<&str>,
    hmac_key: &[u8],
    frame: &RawFrame,
    input: RecordInput<'_>,
) -> Result<NormalizedEvent, AdapterError> {
    let RecordInput {
        value,
        record_type,
        raw_line,
        line_no,
    } = input;
    let explicit_semantic_id = value
        .get("_semanticId")
        .and_then(Value::as_str)
        .map(ToOwned::to_owned);
    let common: Common = serde_json::from_value(value.clone()).map_err(decode_error(line_no))?;
    let cursor = format!("{}:{line_no}", frame.cursor);
    let payload = match record_type {
        "session_started" => {
            let record: SessionRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::SessionStarted(SessionStartedPayload {
                model_id: record.model_id,
                workspace_hash: None,
            })
        }
        "turn_completed" => {
            let record: TurnRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::TurnCompleted(TurnCompletedPayload {
                success: record.success,
                duration_ms: record.duration_ms.map(|v| v.to_string()),
                error_class: record.error_class,
            })
        }
        "model_usage_recorded" => {
            let record: UsageRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                provider_id: record.provider_id,
                model_id: record.model_id,
                tokens: TokenUsage {
                    input_tokens: record.tokens.input.map(|v| v.to_string()),
                    output_tokens: record.tokens.output.map(|v| v.to_string()),
                    cache_read_tokens: record.tokens.cache_read.map(|v| v.to_string()),
                    cache_write_tokens: record.tokens.cache_write.map(|v| v.to_string()),
                    reasoning_tokens: record.tokens.reasoning.map(|v| v.to_string()),
                    tool_tokens: record.tokens.tool.map(|v| v.to_string()),
                    total_tokens: record.tokens.total.map(|v| v.to_string()),
                },
            })
        }
        "tool_invoked" => {
            let record: ToolRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::ToolInvoked(ToolInvokedPayload {
                tool_category: record.tool_category,
                success: record.success,
                duration_ms: record.duration_ms.map(|v| v.to_string()),
            })
        }
        "skill_invoked" => {
            let record: SkillRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::SkillInvoked(SkillInvokedPayload {
                skill_public_name: None,
                skill_key: hmac(hmac_key, &[&record.skill]),
                invoke_type: SkillInvokeType::Native,
                success: record.success,
                plugin_key: record
                    .plugin
                    .as_deref()
                    .map(|plugin| hmac(hmac_key, &[plugin])),
                duration_ms: record.duration_ms.map(|v| v.to_string()),
            })
        }
        "code_changed" => {
            let record: CodeRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::CodeChanged(code_changed_payload(record))
        }
        "agent_spawned" => {
            let record: AgentRecord =
                serde_json::from_value(value).map_err(decode_error(line_no))?;
            EventPayload::AgentSpawned(AgentSpawnedPayload {
                child_session_hash: hmac(hmac_key, &[&record.child_session_id]),
                spawned_agent_type: record.agent_type,
            })
        }
        _ => unreachable!(),
    };
    let semantic_id = explicit_semantic_id
        .as_deref()
        .or(common.semantic_event_id.as_deref())
        .or_else(|| {
            common
                .tool_call_id
                .as_deref()
                .filter(|_| matches!(record_type, "tool_invoked" | "skill_invoked"))
        });
    let source_identity = semantic_id
        .map(|_| "claude-semantic-event")
        .unwrap_or(frame.source_id.as_str());
    Ok(EventEnvelope {
        schema_version: "1.0".into(),
        event_id: event_id(
            hmac_key,
            &frame.installation_id,
            &manifest.id,
            source_identity,
            semantic_id.unwrap_or(&cursor),
            record_type,
            semantic_id.unwrap_or(&line_no.to_string()),
        ),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: agent_version.map(ToOwned::to_owned),
        installation_id: frame.installation_id.clone(),
        occurred_at: common.occurred_at,
        session_hash: common.session_id.as_deref().map(|id| hmac(hmac_key, &[id])),
        turn_hash: common
            .turn_id
            .as_deref()
            .map(|id| hmac(hmac_key, &[common.session_id.as_deref().unwrap_or(""), id])),
        tool_call_hash: common
            .tool_call_id
            .as_deref()
            .map(|id| hmac(hmac_key, &[id])),
        source: EventSource {
            kind: frame.source_kind,
            cursor_hmac: hmac(hmac_key, &[&cursor]),
            raw_fingerprint_hmac: hmac(hmac_key, &[&raw_fingerprint(raw_line.as_bytes())]),
        },
        accuracy: Accuracy::Exact,
        payload,
    })
}

fn hmac(key: &[u8], parts: &[&str]) -> String {
    format!("hmac-sha256:{}", keyed_hmac(key, parts))
}

fn version_supported(version: &str) -> bool {
    let Some(parsed) = version_triplet(version) else {
        return false;
    };
    parsed >= MIN_SUPPORTED_VERSION && parsed < MAX_SUPPORTED_VERSION_EXCLUSIVE
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

fn version_major(version: &str) -> Option<u64> {
    version
        .trim_start_matches('v')
        .split('.')
        .next()?
        .parse()
        .ok()
}

fn normalize_records(value: Value, source_kind: SourceKind) -> Result<Vec<Value>, AdapterError> {
    let source = match value.as_object() {
        Some(source) => source,
        None => return Ok(vec![]),
    };
    ensure_schema_version(source)?;
    if source_kind == SourceKind::JsonlTail {
        if let Some(native) = native_history_records(source) {
            return Ok(native);
        }
    }
    let Some(name) = source
        .get("name")
        .or_else(|| source.get("event"))
        .or_else(|| source.get("type"))
        .and_then(Value::as_str)
    else {
        return Ok(vec![]);
    };
    let normalized_type = match source_kind {
        SourceKind::Otlp => match name {
            "claude_code.session.count" | "claude_code.session.started" => "session_started",
            "claude_code.turn.count" | "claude_code.turn.completed" => "turn_completed",
            "claude_code.token.usage" => "model_usage_recorded",
            "claude_code.tool.result"
            | "claude_code.tool.completed"
            | "claude_code.tool.failed" => "tool_invoked",
            "claude_code.skill.execution.completed" | "claude_code.skill.execution.failed" => {
                "skill_invoked"
            }
            "claude_code.lines_of_code.count" | "claude_code.code.changed" => "code_changed",
            "claude_code.agent.spawned" => "agent_spawned",
            "claude_code.tool.started"
            | "claude_code.tool.invoked"
            | "claude_code.tool.usage"
            | "claude_code.skill.invoked"
            | "claude_code.skill.injected"
            | "claude_code.skill.loaded"
            | "claude_code.skill.execution.started" => return Ok(vec![]),
            _ => return Ok(vec![]),
        },
        SourceKind::JsonlTail => match name {
            "claude_code.session.started" => "session_started",
            "claude_code.turn.completed" => "turn_completed",
            "claude_code.token.usage" => "model_usage_recorded",
            "claude_code.tool.result"
            | "claude_code.tool.completed"
            | "claude_code.tool.failed" => "tool_invoked",
            "claude_code.skill.execution.completed" | "claude_code.skill.execution.failed" => {
                "skill_invoked"
            }
            "claude_code.code.changed" => "code_changed",
            "claude_code.agent.spawned" => "agent_spawned",
            "claude_code.tool.started"
            | "claude_code.tool.invoked"
            | "claude_code.tool.usage"
            | "claude_code.skill.invoked"
            | "claude_code.skill.injected"
            | "claude_code.skill.loaded"
            | "claude_code.skill.execution.started" => return Ok(vec![]),
            _ => return Ok(vec![]),
        },
        _ => return Ok(vec![]),
    };
    Ok(vec![build_normalized(source, name, normalized_type)])
}

fn native_history_records(source: &Map<String, Value>) -> Option<Vec<Value>> {
    let record_type = source.get("type").and_then(Value::as_str)?;
    if source.get("name").is_some() || record_type.starts_with("claude_code.") {
        return None;
    }
    if record_type != "assistant" {
        return Some(vec![]);
    }
    let message = source.get("message").and_then(Value::as_object)?;
    let occurred_at = source
        .get("timestamp")
        .and_then(Value::as_str)
        .unwrap_or("1970-01-01T00:00:00Z");
    let session_id = source
        .get("sessionId")
        .or_else(|| source.get("session_id"))
        .and_then(Value::as_str)
        .unwrap_or("");
    let mut records = Vec::new();
    if let Some(usage) = usage_from_message(message, session_id, occurred_at) {
        records.push(usage);
    }
    if let Some(content) = message.get("content").and_then(Value::as_array) {
        for item in content {
            let Some(tool) = item.as_object() else {
                continue;
            };
            if let Some(changed) = code_changed_from_tool_use(tool, session_id, occurred_at) {
                records.push(changed);
            }
        }
    }
    Some(records)
}

fn usage_from_message(
    message: &Map<String, Value>,
    session_id: &str,
    occurred_at: &str,
) -> Option<Value> {
    let usage = message.get("usage").and_then(Value::as_object)?;
    let model_id = message.get("model").and_then(Value::as_str).unwrap_or("");
    if model_id.is_empty() {
        return None;
    }
    let input = json_u64(usage, &["input_tokens", "inputTokens"]);
    let output = json_u64(usage, &["output_tokens", "outputTokens"]);
    let cache_read = json_u64(usage, &["cache_read_input_tokens", "cacheReadTokens"]);
    let cache_write = json_u64(
        usage,
        &["cache_creation_input_tokens", "cacheWriteTokens"],
    );
    let reasoning = usage
        .get("output_tokens_details")
        .and_then(Value::as_object)
        .and_then(|details| json_u64(details, &["thinking_tokens", "reasoning_tokens"]))
        .or_else(|| json_u64(usage, &["reasoning_tokens"]));
    let total = json_u64(usage, &["total_tokens", "totalTokens"]).or_else(|| {
        Some(
            input.unwrap_or(0)
                + output.unwrap_or(0)
                + cache_read.unwrap_or(0)
                + cache_write.unwrap_or(0),
        )
    });
    if input.unwrap_or(0) == 0 && output.unwrap_or(0) == 0 && total.unwrap_or(0) == 0 {
        return None;
    }
    let message_id = message.get("id").and_then(Value::as_str).unwrap_or("");
    Some(serde_json::json!({
        "type": "model_usage_recorded",
        "occurredAt": occurred_at,
        "sessionId": session_id,
        "semanticEventId": format!("claude-usage:{message_id}"),
        "providerId": "anthropic",
        "modelId": model_id,
        "tokens": {
            "input": input,
            "output": output,
            "cacheRead": cache_read,
            "cacheWrite": cache_write,
            "reasoning": reasoning,
            "total": total
        }
    }))
}

fn code_changed_from_tool_use(
    tool: &Map<String, Value>,
    session_id: &str,
    occurred_at: &str,
) -> Option<Value> {
    if tool.get("type").and_then(Value::as_str) != Some("tool_use") {
        return None;
    }
    let name = tool.get("name").and_then(Value::as_str).unwrap_or("");
    if !matches!(name, "Write" | "Edit" | "NotebookEdit") {
        return None;
    }
    let input = tool.get("input").and_then(Value::as_object);
    let old_text = input
        .and_then(|input| json_string(input, &["old_string", "oldString"]))
        .unwrap_or("");
    let new_text = input
        .and_then(|input| json_string(input, &["new_string", "newString", "content", "contents"]))
        .unwrap_or("");
    if old_text.is_empty() && new_text.is_empty() {
        return None;
    }
    let added = line_count(new_text);
    let removed = line_count(old_text);
    if added == 0 && removed == 0 {
        return None;
    }
    let call_id = tool.get("id").and_then(Value::as_str)?;
    Some(serde_json::json!({
        "type": "code_changed",
        "occurredAt": occurred_at,
        "sessionId": session_id,
        "toolCallId": call_id,
        "semanticEventId": format!("claude-edit:{call_id}"),
        "added": added,
        "removed": removed,
        "generated": added,
        "fileCount": 1
    }))
}

fn line_count(text: &str) -> u64 {
    if text.is_empty() {
        0
    } else {
        u64::try_from(text.lines().count()).unwrap_or(0)
    }
}

fn json_string<'a>(value: &'a Map<String, Value>, keys: &[&str]) -> Option<&'a str> {
    keys.iter()
        .find_map(|key| value.get(*key).and_then(Value::as_str))
}

fn json_u64(value: &Map<String, Value>, keys: &[&str]) -> Option<u64> {
    keys.iter().find_map(|key| {
        value.get(*key).and_then(|item| match item {
            Value::Number(number) => number.as_u64(),
            Value::String(text) => text.parse().ok(),
            _ => None,
        })
    })
}

fn build_normalized(source: &Map<String, Value>, name: &str, normalized_type: &str) -> Value {
    let attrs = source
        .get("attributes")
        .or_else(|| source.get("data"))
        .and_then(Value::as_object);
    let mut out = Map::new();
    out.insert("type".into(), Value::String(normalized_type.into()));
    copy_common_fields(&mut out, source, attrs);
    if normalized_type == "skill_invoked" && name.ends_with(".failed") {
        out.insert("success".into(), Value::Bool(false));
    }
    add_tokens(&mut out, source, attrs, normalized_type);
    Value::Object(out)
}

fn ensure_schema_version(source: &Map<String, Value>) -> Result<(), AdapterError> {
    let version = source
        .get("schemaVersion")
        .or_else(|| source.get("schema_version"));
    let Some(version) = version else {
        return Ok(());
    };
    let major = match version {
        Value::Number(number) => number.as_u64(),
        Value::String(value) => version_major(value),
        _ => None,
    };
    if major.is_some_and(|major| major <= SUPPORTED_MAJOR) {
        Ok(())
    } else {
        Err(AdapterError::decode_failed(
            "record schema version exceeds adapter maximum",
        ))
    }
}

fn copy_common_fields(
    out: &mut Map<String, Value>,
    source: &Map<String, Value>,
    attrs: Option<&Map<String, Value>>,
) {
    copy_alias(
        out,
        "occurredAt",
        source,
        attrs,
        &["timestamp", "time", "occurredAt"],
    );
    copy_alias(
        out,
        "sessionId",
        source,
        attrs,
        &["session.id", "session_id", "sessionId"],
    );
    copy_alias(
        out,
        "turnId",
        source,
        attrs,
        &["turn.id", "turn_id", "turnId"],
    );
    copy_alias(
        out,
        "toolCallId",
        source,
        attrs,
        &[
            "tool.call.id",
            "tool_call_id",
            "toolCallId",
            "invocation.id",
            "invocation_id",
            "skill.invocation.id",
        ],
    );
    copy_alias(
        out,
        "semanticEventId",
        source,
        attrs,
        &[
            "event.id",
            "event_id",
            "eventId",
            "request.id",
            "request_id",
            "requestId",
            "invocation.id",
            "invocation_id",
            "skill.invocation.id",
            "tool.call.id",
            "tool_call_id",
            "toolCallId",
        ],
    );
    for (target, aliases) in [
        ("modelId", &["model", "model.id", "model_id"][..]),
        (
            "providerId",
            &["provider", "provider.id", "provider_id"][..],
        ),
        ("toolCategory", &["tool.category", "tool.name", "tool"][..]),
        ("skill", &["skill.name", "skill"][..]),
        ("plugin", &["plugin.name", "plugin"][..]),
        ("errorClass", &["error.type", "error_class"][..]),
        ("language", &["language"][..]),
        (
            "childSessionId",
            &["child.session.id", "child_session_id"][..],
        ),
        ("agentType", &["agent.type", "agent_type"][..]),
    ] {
        copy_alias(out, target, source, attrs, aliases);
    }
    for (target, aliases) in [
        ("success", &["success"][..]),
        ("durationMs", &["duration_ms", "duration.ms"][..]),
        ("added", &["added_lines", "lines.added", "value"][..]),
        ("removed", &["removed_lines", "lines.removed"][..]),
        ("generated", &["generated_lines", "lines.generated"][..]),
        ("accepted", &["accepted_lines", "lines.accepted"][..]),
        ("fileCount", &["file_count", "files.count"][..]),
    ] {
        copy_alias(out, target, source, attrs, aliases);
    }
}

fn add_tokens(
    out: &mut Map<String, Value>,
    source: &Map<String, Value>,
    attrs: Option<&Map<String, Value>>,
    normalized_type: &str,
) {
    if normalized_type != "model_usage_recorded" {
        return;
    }
    let mut tokens = Map::new();
    for (target, aliases) in [
        ("input", &["input_tokens", "input.tokens"][..]),
        ("output", &["output_tokens", "output.tokens"][..]),
        ("cacheRead", &["cache_read_tokens", "cache.read.tokens"][..]),
        (
            "cacheWrite",
            &["cache_write_tokens", "cache.write.tokens"][..],
        ),
        ("reasoning", &["reasoning_tokens", "reasoning.tokens"][..]),
        ("tool", &["tool_tokens", "tool.tokens"][..]),
        ("total", &["total_tokens", "total.tokens", "value"][..]),
    ] {
        copy_alias(&mut tokens, target, source, attrs, aliases);
    }
    out.insert("tokens".into(), Value::Object(tokens));
}

fn copy_alias(
    out: &mut Map<String, Value>,
    target: &str,
    source: &Map<String, Value>,
    attrs: Option<&Map<String, Value>>,
    aliases: &[&str],
) {
    if let Some(value) = aliases.iter().find_map(|key| {
        attrs
            .and_then(|attrs| attrs.get(*key))
            .or_else(|| source.get(*key))
    }) {
        out.insert(target.into(), value.clone());
    }
}

fn known_type(value: &str) -> bool {
    matches!(
        value,
        "session_started"
            | "turn_completed"
            | "model_usage_recorded"
            | "tool_invoked"
            | "skill_invoked"
            | "code_changed"
            | "agent_spawned"
    )
}

fn decode_error(line: usize) -> impl FnOnce(serde_json::Error) -> AdapterError {
    move |err| AdapterError::decode_failed(format!("invalid known record at line {line}: {err}"))
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct Common {
    occurred_at: String,
    #[serde(default)]
    session_id: Option<String>,
    #[serde(default)]
    turn_id: Option<String>,
    #[serde(default)]
    tool_call_id: Option<String>,
    #[serde(default)]
    semantic_event_id: Option<String>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SessionRecord {
    #[serde(default)]
    model_id: Option<String>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct TurnRecord {
    success: bool,
    #[serde(default)]
    duration_ms: Option<u64>,
    #[serde(default)]
    error_class: Option<String>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct UsageRecord {
    provider_id: String,
    model_id: String,
    tokens: RawTokens,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct RawTokens {
    #[serde(default)]
    input: Option<u64>,
    #[serde(default)]
    output: Option<u64>,
    #[serde(default)]
    cache_read: Option<u64>,
    #[serde(default)]
    cache_write: Option<u64>,
    #[serde(default)]
    reasoning: Option<u64>,
    #[serde(default)]
    tool: Option<u64>,
    #[serde(default)]
    total: Option<u64>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct ToolRecord {
    tool_category: String,
    success: bool,
    #[serde(default)]
    duration_ms: Option<u64>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct SkillRecord {
    skill: String,
    success: bool,
    #[serde(default)]
    plugin: Option<String>,
    #[serde(default)]
    duration_ms: Option<u64>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct CodeRecord {
    #[serde(default)]
    added: u64,
    #[serde(default)]
    removed: u64,
    #[serde(default)]
    generated: Option<u64>,
    #[serde(default)]
    accepted: Option<u64>,
    #[serde(default)]
    file_count: u32,
    #[serde(default)]
    language: Option<String>,
}

fn code_changed_payload(record: CodeRecord) -> CodeChangedPayload {
    let generated = record.generated.unwrap_or(record.added);
    CodeChangedPayload {
        added_lines: record.added.to_string(),
        removed_lines: record.removed.to_string(),
        generated_lines: Some(generated.to_string()),
        accepted_lines: record.accepted.map(|v| v.to_string()),
        file_count: record.file_count.max(1),
        language: record.language,
    }
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AgentRecord {
    child_session_id: String,
    agent_type: String,
}
