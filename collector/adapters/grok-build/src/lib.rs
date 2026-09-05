#![forbid(unsafe_code)]

use std::collections::HashSet;

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    ConfigMutation, EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext,
    ProbeReport, RawFrame, RollbackStep, SetupContext, SetupPlan, SourceContext, SourceKind,
    SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::{
    AgentSpawnedPayload, CodeChangedPayload, ModelUsageRecordedPayload, SessionStartedPayload,
    SkillInvokeType, SkillInvokedPayload, ToolInvokedPayload, TurnCompletedPayload,
};
use serde::Deserialize;
use serde_json::{Map, Value};
use time::format_description::well_known::Rfc3339;
use time::OffsetDateTime;

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.grok-build";
pub const OTLP_SOURCE_ID: &str = "grok-build-otlp";
pub const HISTORY_SOURCE_ID: &str = "grok-build-history";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility-matrix.json");
pub const COMPATIBILITY_MARKDOWN: &str = include_str!("../fixtures/compatibility-matrix.md");
const SUPPORTED_MAJOR: u64 = 1;
const MIN_SUPPORTED_VERSION: (u64, u64, u64) = (1, 0, 0);
const MAX_SUPPORTED_VERSION_EXCLUSIVE: (u64, u64, u64) = (2, 0, 0);
const MAX_COUNTER_INCREMENT: u64 = 10_000;

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Grok Build manifest fixture")
}

pub struct GrokBuildAdapter {
    manifest: AdapterManifest,
    detected: bool,
    agent_version: Option<String>,
    hmac_key: Vec<u8>,
}

impl GrokBuildAdapter {
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

    fn capability_report(&self) -> CapabilityReport {
        let available = self.available_capabilities();
        CapabilityReport {
            adapter_id: self.manifest.id.clone(),
            adapter_version: self.manifest.version.clone(),
            capabilities: self
                .manifest
                .capabilities
                .iter()
                .copied()
                .map(|capability| {
                    let enabled = available.contains(&capability);
                    CapabilityStatus {
                        capability,
                        availability: if enabled {
                            CapabilityAvailability::Available
                        } else {
                            CapabilityAvailability::Unavailable
                        },
                        accuracy: enabled.then_some(
                            if matches!(
                                capability,
                                Capability::Code | Capability::Sessions | Capability::Turns
                            ) {
                                Accuracy::Derived
                            } else {
                                Accuracy::Exact
                            },
                        ),
                        safe_reason_code: (!enabled)
                            .then(|| "GROK_VERSION_OR_SOURCE_UNVERIFIED".into()),
                    }
                })
                .collect(),
        }
    }
}

#[async_trait]
impl AgentAdapter for GrokBuildAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }

    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let supported = self.version_supported();
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: self.agent_version.clone(),
            needs_permission: false,
            needs_setup: self.detected && supported,
            capability: self.capability_report(),
            detail: if self.detected && !supported {
                Some(
                    "unknown Grok Build version; OTLP disabled and verified history schema only"
                        .into(),
                )
            } else {
                None
            },
        })
    }

    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "setup-grok-build-otlp-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Enable local Grok Build OTLP without prompt, tool-detail, or content logging"
                .into(),
            mutations: vec![ConfigMutation::JsonMergePatch {
                path_template: "${USER_HOME}/.grok/settings.json".into(),
                patch: serde_json::json!({
                    "env": {
                        "GROK_TELEMETRY_ENABLED": "1",
                        "OTEL_EXPORTER_OTLP_ENDPOINT": "http://127.0.0.1:4318",
                        "GROK_OTEL_LOG_PROMPTS": "0",
                        "GROK_OTEL_LOG_TOOL_DETAILS": "0",
                        "GROK_OTEL_LOG_TOOL_CONTENT": "0"
                    }
                }),
            }],
            required_permissions: vec![],
            verify: vec![VerifyStep {
                id: "grok-build-otlp-safe".into(),
                summary: "OTLP is loopback and content logging remains disabled".into(),
            }],
            rollback: vec![RollbackStep {
                id: "restore-grok-build-settings".into(),
                summary: "Restore the previous Grok Build settings".into(),
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
            path_template: "${USER_HOME}/.grok/sessions/**".into(),
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
                reason: "unknown Grok Build version; history fallback only".into(),
            }
        } else if self.detected {
            AdapterHealth::Degraded {
                reason: "cumulative OTLP counters are safely ignored because no persistent baseline is available; delta increments are capped".into(),
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
            "frame source kind/id does not match Grok Build manifest source",
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
            EventPayload::CodeChanged(CodeChangedPayload {
                added_lines: record.added.to_string(),
                removed_lines: record.removed.to_string(),
                generated_lines: record.generated.map(|v| v.to_string()),
                accepted_lines: record.accepted.map(|v| v.to_string()),
                file_count: record.file_count,
                language: record.language,
            })
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
        .map(|_| "grok-build-semantic-event")
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
        accuracy: if record_type == "code_changed" {
            Accuracy::Derived
        } else {
            Accuracy::Exact
        },
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
    if source_kind == SourceKind::JsonlTail {
        if let Some(records) = session_update_records(&value) {
            return Ok(records);
        }
    }
    if source_kind != SourceKind::Otlp {
        return Ok(normalize_non_counter(value, source_kind)?
            .into_iter()
            .collect());
    }
    let Some(source) = value.as_object() else {
        return Ok(Vec::new());
    };
    ensure_schema_version(source)?;
    let Some(name) = source.get("name").and_then(Value::as_str) else {
        return Ok(Vec::new());
    };
    let record_type = match name {
        "grok_code.session.count" => "session_started",
        "grok_code.turn.count" => "turn_completed",
        _ => {
            return Ok(normalize_non_counter(value, source_kind)?
                .into_iter()
                .collect())
        }
    };
    let Some((count, sample_identity, flattened)) = counter_increment(source)? else {
        return Ok(Vec::new());
    };
    let mut records = Vec::with_capacity(count as usize);
    for sequence in 0..count {
        let mut normalized = build_normalized(&flattened, name, record_type);
        let object = normalized.as_object_mut().expect("normalized object");
        object.insert(
            "_semanticId".into(),
            Value::String(format!("{sample_identity}:{sequence}")),
        );
        records.push(normalized);
    }
    Ok(records)
}

type CounterIncrement = (u64, String, Map<String, Value>);

fn counter_increment(
    source: &Map<String, Value>,
) -> Result<Option<CounterIncrement>, AdapterError> {
    let datapoint = source
        .get("dataPoint")
        .or_else(|| source.get("datapoint"))
        .and_then(Value::as_object);
    let value = datapoint_value(datapoint.unwrap_or(source));
    let temporality = source
        .get("temporality")
        .or_else(|| source.get("aggregationTemporality"))
        .or_else(|| datapoint.and_then(|point| point.get("temporality")));
    let (Some(value), Some(temporality)) = (value, temporality.and_then(parse_temporality)) else {
        return Ok(None);
    };
    let mut flattened = source.clone();
    if !flattened.contains_key("attributes") {
        if let Some(attributes) = datapoint.and_then(|point| point.get("attributes")) {
            flattened.insert("attributes".into(), attributes.clone());
        }
    }
    if !flattened.contains_key("timestamp") {
        if let Some(timestamp) =
            datapoint.and_then(|point| point.get("timestamp").or_else(|| point.get("timeUnixNano")))
        {
            flattened.insert("timestamp".into(), timestamp.clone());
        }
    }
    let increment = match temporality {
        Temporality::Delta if value <= MAX_COUNTER_INCREMENT => value,
        Temporality::Delta | Temporality::Cumulative => return Ok(None),
    };
    if increment == 0 {
        return Ok(None);
    }
    let timestamp = flattened
        .get("timestamp")
        .or_else(|| source.get("timeUnixNano"))
        .map(Value::to_string)
        .unwrap_or_else(|| "missing-time".into());
    let sample_identity = format!(
        "{}:{timestamp}:{value}",
        source
            .get("name")
            .and_then(Value::as_str)
            .unwrap_or_default()
    );
    Ok(Some((increment, sample_identity, flattened)))
}

fn datapoint_value(source: &Map<String, Value>) -> Option<u64> {
    ["value", "asInt", "asDouble"]
        .iter()
        .find_map(|key| source.get(*key))
        .and_then(|value| match value {
            Value::Number(number) => number.as_u64(),
            Value::String(value) => value.parse().ok(),
            _ => None,
        })
}

#[derive(Clone, Copy)]
enum Temporality {
    Delta,
    Cumulative,
}

fn parse_temporality(value: &Value) -> Option<Temporality> {
    match value {
        Value::Number(number) if number.as_u64() == Some(1) => Some(Temporality::Delta),
        Value::Number(number) if number.as_u64() == Some(2) => Some(Temporality::Cumulative),
        Value::String(value)
            if value.eq_ignore_ascii_case("delta") || value.ends_with("_DELTA") =>
        {
            Some(Temporality::Delta)
        }
        Value::String(value)
            if value.eq_ignore_ascii_case("cumulative") || value.ends_with("_CUMULATIVE") =>
        {
            Some(Temporality::Cumulative)
        }
        _ => None,
    }
}

fn session_update_records(value: &Value) -> Option<Vec<Value>> {
    let source = value.as_object()?;
    if !is_session_update(source) {
        return None;
    }
    let params = source.get("params").and_then(Value::as_object);
    let session_id = params
        .and_then(|params| {
            params
                .get("sessionId")
                .or_else(|| params.get("session_id"))
                .and_then(Value::as_str)
        })
        .map(str::to_owned);
    let update = nested_session_update(source, params)?;
    if session_update_name(update) != Some("turn_completed") {
        return Some(Vec::new());
    }
    let Some(usage) = update.get("usage").and_then(Value::as_object) else {
        return Some(Vec::new());
    };
    let Some(occurred_at) = occurred_at_from(source).or_else(|| occurred_at_from(update)) else {
        return Some(Vec::new());
    };
    let turn_id = update
        .get("prompt_id")
        .or_else(|| update.get("promptId"))
        .and_then(Value::as_str)
        .map(str::to_owned);
    let mut records = Vec::new();
    if let Some(models) = usage
        .get("modelUsage")
        .or_else(|| usage.get("model_usage"))
        .and_then(Value::as_object)
    {
        for (model_id, row) in models {
            let Some(row) = row.as_object() else {
                continue;
            };
            if let Some(record) = usage_record(
                &occurred_at,
                session_id.as_deref(),
                turn_id.as_deref(),
                model_id,
                row,
            ) {
                records.push(record);
            }
        }
    }
    if records.is_empty() {
        if let Some(model_id) = usage
            .get("model")
            .or_else(|| update.get("model"))
            .and_then(Value::as_str)
            .or(Some("grok"))
        {
            if let Some(record) = usage_record(
                &occurred_at,
                session_id.as_deref(),
                turn_id.as_deref(),
                model_id,
                usage,
            ) {
                records.push(record);
            }
        }
    }
    Some(records)
}

fn is_session_update(source: &Map<String, Value>) -> bool {
    matches!(
        source.get("method").and_then(Value::as_str),
        Some("session/update" | "_x.ai/session/update")
    ) || nested_session_update(source, source.get("params").and_then(Value::as_object))
        .and_then(session_update_name)
        .is_some()
}

fn nested_session_update<'a>(
    source: &'a Map<String, Value>,
    params: Option<&'a Map<String, Value>>,
) -> Option<&'a Map<String, Value>> {
    let update = params
        .and_then(|params| params.get("update"))
        .or_else(|| source.get("update"))
        .and_then(Value::as_object)?;
    Some(
        update
            .get("update")
            .and_then(Value::as_object)
            .unwrap_or(update),
    )
}

fn session_update_name(update: &Map<String, Value>) -> Option<&str> {
    update
        .get("sessionUpdate")
        .or_else(|| update.get("session_update"))
        .and_then(Value::as_str)
}

fn usage_record(
    occurred_at: &str,
    session_id: Option<&str>,
    turn_id: Option<&str>,
    model_id: &str,
    usage: &Map<String, Value>,
) -> Option<Value> {
    let model_id = model_id.trim();
    if model_id.is_empty() {
        return None;
    }
    let input = token_u64(
        usage,
        &["inputTokens", "input_tokens", "uncachedInputTokens"],
    );
    let output = token_u64(usage, &["outputTokens", "output_tokens"]);
    let cache_read = token_u64(
        usage,
        &[
            "cachedReadTokens",
            "cacheReadTokens",
            "cache_read_tokens",
            "cacheReadInputTokens",
        ],
    );
    let cache_write = token_u64(
        usage,
        &[
            "cacheCreationTokens",
            "cacheWriteTokens",
            "cache_write_tokens",
            "cacheCreationInputTokens",
        ],
    );
    let reasoning = token_u64(usage, &["reasoningTokens", "reasoning_tokens"]);
    let total = token_u64(usage, &["totalTokens", "total_tokens"]).or_else(|| {
        let parts = [input, output, cache_read, cache_write];
        parts
            .iter()
            .copied()
            .flatten()
            .reduce(|a, b| a.saturating_add(b))
    });
    if input.or(output).or(total).is_none() {
        return None;
    }
    let semantic = match turn_id {
        Some(turn_id) => format!("{turn_id}:{model_id}"),
        None => format!("{occurred_at}:{model_id}"),
    };
    let mut record = Map::new();
    record.insert("type".into(), Value::String("model_usage_recorded".into()));
    record.insert("occurredAt".into(), Value::String(occurred_at.to_owned()));
    if let Some(session_id) = session_id {
        record.insert("sessionId".into(), Value::String(session_id.to_owned()));
    }
    if let Some(turn_id) = turn_id {
        record.insert("turnId".into(), Value::String(turn_id.to_owned()));
    }
    record.insert("semanticEventId".into(), Value::String(semantic));
    record.insert(
        "providerId".into(),
        Value::String(provider_for_model(model_id).into()),
    );
    record.insert("modelId".into(), Value::String(model_id.to_owned()));
    let mut tokens = Map::new();
    insert_token(&mut tokens, "input", input);
    insert_token(&mut tokens, "output", output);
    insert_token(&mut tokens, "cacheRead", cache_read);
    insert_token(&mut tokens, "cacheWrite", cache_write);
    insert_token(&mut tokens, "reasoning", reasoning);
    insert_token(&mut tokens, "total", total);
    record.insert("tokens".into(), Value::Object(tokens));
    Some(Value::Object(record))
}

fn insert_token(tokens: &mut Map<String, Value>, key: &str, value: Option<u64>) {
    if let Some(value) = value {
        tokens.insert(key.into(), Value::Number(value.into()));
    }
}

fn token_u64(source: &Map<String, Value>, keys: &[&str]) -> Option<u64> {
    keys.iter().find_map(|key| match source.get(*key)? {
        Value::Number(number) => number
            .as_u64()
            .or_else(|| number.as_i64().and_then(|value| u64::try_from(value).ok()))
            .or_else(|| {
                number
                    .as_f64()
                    .and_then(|value| (value >= 0.0).then_some(value as u64))
            }),
        Value::String(value) => value.parse().ok(),
        _ => None,
    })
}

fn provider_for_model(model: &str) -> &'static str {
    let model = model.to_ascii_lowercase();
    if model.contains("claude") || model.contains("anthropic") {
        "anthropic"
    } else if model.contains("gemini") {
        "google"
    } else if model.contains("deepseek") {
        "deepseek"
    } else if model.starts_with("gpt")
        || model.contains("codex")
        || model.starts_with("o1")
        || model.starts_with("o3")
        || model.starts_with("o4")
    {
        "openai"
    } else {
        "xai"
    }
}

fn occurred_at_from(source: &Map<String, Value>) -> Option<String> {
    let value = source
        .get("timestamp")
        .or_else(|| source.get("ts"))
        .or_else(|| source.get("occurredAt"))?;
    match value {
        Value::String(value) if value.contains('T') => OffsetDateTime::parse(value, &Rfc3339)
            .ok()
            .and_then(|time| time.format(&Rfc3339).ok())
            .or_else(|| Some(value.clone())),
        Value::String(value) => value.parse::<i64>().ok().and_then(unix_to_rfc3339),
        Value::Number(number) => number
            .as_i64()
            .or_else(|| number.as_u64().and_then(|value| i64::try_from(value).ok()))
            .and_then(unix_to_rfc3339),
        _ => None,
    }
}

fn unix_to_rfc3339(timestamp: i64) -> Option<String> {
    let seconds = if timestamp.abs() > 1_000_000_000_000 {
        timestamp / 1000
    } else {
        timestamp
    };
    OffsetDateTime::from_unix_timestamp(seconds)
        .ok()?
        .format(&Rfc3339)
        .ok()
}

fn normalize_non_counter(
    value: Value,
    source_kind: SourceKind,
) -> Result<Option<Value>, AdapterError> {
    let source = match value.as_object() {
        Some(source) => source,
        None => return Ok(None),
    };
    ensure_schema_version(source)?;
    let Some(name) = source
        .get("name")
        .or_else(|| source.get("event"))
        .or_else(|| source.get("type"))
        .and_then(Value::as_str)
    else {
        return Ok(None);
    };
    let normalized_type = match source_kind {
        SourceKind::Otlp => match name {
            "grok_code.session.count" | "grok_code.session.started" => "session_started",
            "grok_code.turn.count" | "grok_code.turn.completed" => "turn_completed",
            "grok_code.token.usage" => "model_usage_recorded",
            "grok_code.tool.result" | "grok_code.tool.completed" | "grok_code.tool.failed" => {
                "tool_invoked"
            }
            "grok_code.skill.execution.completed" | "grok_code.skill.execution.failed" => {
                "skill_invoked"
            }
            "grok_code.lines_of_code.count" | "grok_code.code.changed" => "code_changed",
            "grok_code.agent.spawned" => "agent_spawned",
            "grok_code.tool.started"
            | "grok_code.tool.invoked"
            | "grok_code.tool.usage"
            | "grok_code.skill.invoked"
            | "grok_code.skill.injected"
            | "grok_code.skill.loaded"
            | "grok_code.skill.execution.started" => return Ok(None),
            _ => return Ok(None),
        },
        SourceKind::JsonlTail => match name {
            "grok_code.session.started" => "session_started",
            "grok_code.turn.completed" => "turn_completed",
            "grok_code.token.usage" => "model_usage_recorded",
            "grok_code.tool.result" | "grok_code.tool.completed" | "grok_code.tool.failed" => {
                "tool_invoked"
            }
            "grok_code.skill.execution.completed" | "grok_code.skill.execution.failed" => {
                "skill_invoked"
            }
            "grok_code.code.changed" => "code_changed",
            "grok_code.agent.spawned" => "agent_spawned",
            "grok_code.tool.started"
            | "grok_code.tool.invoked"
            | "grok_code.tool.usage"
            | "grok_code.skill.invoked"
            | "grok_code.skill.injected"
            | "grok_code.skill.loaded"
            | "grok_code.skill.execution.started" => return Ok(None),
            _ => return Ok(None),
        },
        _ => return Ok(None),
    };
    Ok(Some(build_normalized(source, name, normalized_type)))
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
    added: u64,
    removed: u64,
    #[serde(default)]
    generated: Option<u64>,
    #[serde(default)]
    accepted: Option<u64>,
    file_count: u32,
    #[serde(default)]
    language: Option<String>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
struct AgentRecord {
    child_session_id: String,
    agent_type: String,
}
