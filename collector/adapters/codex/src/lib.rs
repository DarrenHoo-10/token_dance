#![forbid(unsafe_code)]
mod session_context;
use session_context::SessionContext;
use std::collections::BTreeMap;
use std::sync::Mutex;

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    ConfigMutation, EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext,
    ProbeReport, RawFrame, RollbackStep, SetupContext, SetupPlan, SourceContext, SourceKind,
    SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::{
    CodeChangedPayload, ModelUsageRecordedPayload, SessionStartedPayload, SkillInvokeType,
    SkillInvokedPayload, ToolInvokedPayload, TurnCompletedPayload, TurnStartedPayload,
};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.codex";
pub const SOURCE_OTEL: &str = "codex-otel";
pub const SOURCE_JSONL: &str = "codex-sessions";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const SESSION_JSONL: &str = include_str!("../fixtures/contract/session.jsonl");
pub const OTEL_JSON: &str = include_str!("../fixtures/contract/otel.json");
const MIN_SUPPORTED_VERSION: (u64, u64, u64) = (0, 100, 0);
const MAX_SUPPORTED_VERSION_EXCLUSIVE: (u64, u64, u64) = (0, 200, 0);

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Codex manifest")
}

pub struct CodexAdapter {
    manifest: AdapterManifest,
    version: String,
    mode: String,
    detected: bool,
    otel_available: bool,
    hmac_key: Vec<u8>,
    sessions: Mutex<BTreeMap<String, SessionContext>>,
}

impl CodexAdapter {
    pub fn new(
        version: impl Into<String>,
        mode: impl Into<String>,
        hmac_key: impl Into<Vec<u8>>,
    ) -> Self {
        Self {
            manifest: load_manifest(),
            version: version.into(),
            mode: mode.into(),
            detected: true,
            otel_available: true,
            hmac_key: hmac_key.into(),
            sessions: Mutex::new(BTreeMap::new()),
        }
    }
    pub fn with_otel(mut self, available: bool) -> Self {
        self.otel_available = available;
        self
    }
    fn version_supported(&self) -> bool {
        version_in_range(
            &self.version,
            MIN_SUPPORTED_VERSION,
            MAX_SUPPORTED_VERSION_EXCLUSIVE,
        )
    }
    fn skills_supported(&self) -> bool {
        self.version_supported() && version_at_least(&self.version, 0, 120, 0)
    }
    fn capability_report(&self) -> CapabilityReport {
        let supported = self.version_supported();
        let skills = self.skills_supported();
        let capabilities = self
            .manifest
            .capabilities
            .iter()
            .copied()
            .map(|capability| {
                let available = if supported {
                    capability != Capability::Skills || skills
                } else {
                    matches!(
                        capability,
                        Capability::Tokens | Capability::Sessions | Capability::Turns
                    )
                };
                let accuracy = match capability {
                    Capability::Code if available => Some(Accuracy::Derived),
                    _ if available => Some(Accuracy::Exact),
                    _ => None,
                };
                CapabilityStatus {
                    capability,
                    availability: if available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy,
                    safe_reason_code: (!available).then(|| {
                        if supported {
                            "CODEX_SKILL_SCHEMA_UNAVAILABLE"
                        } else {
                            "CODEX_VERSION_UNVERIFIED"
                        }
                        .into()
                    }),
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
impl AgentAdapter for CodexAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }
    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: Some(self.version.clone()),
            needs_permission: false,
            needs_setup: self.version_supported() && !self.otel_available,
            capability: self.capability_report(),
            detail: Some(format!(
                "mode={}; otel={}; compatibility={}",
                self.mode,
                self.otel_available,
                if self.version_supported() {
                    "verified"
                } else {
                    "degraded"
                }
            )),
        })
    }
    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "codex-otel-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Merge a privacy-safe local Codex OTLP exporter".into(),
            mutations: vec![ConfigMutation::TomlMergePatch {
                path_template: "${CODEX_HOME}/config.toml".into(),
                patch: serde_json::json!({"otel":{"exporter":{"otlp-http":{"endpoint":"http://127.0.0.1:${TOKENSHOW_OTLP_PORT}"}},"log_user_prompt":false,"log_tool_details":false,"log_tool_content":false}}),
            }],
            required_permissions: vec![],
            verify: vec![
                VerifyStep {
                    id: "codex-toml".into(),
                    summary: "validate merged TOML and preserve existing OTLP settings".into(),
                },
                VerifyStep {
                    id: "privacy-flags".into(),
                    summary: "verify prompt and tool content logging remain disabled".into(),
                },
            ],
            rollback: vec![RollbackStep {
                id: "restore-config".into(),
                summary: "restore the pre-merge Codex config".into(),
            }],
        })
    }
    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        let mut sources = Vec::new();
        if self.version_supported() && self.otel_available {
            sources.push(SourceSpec::OtlpReceiver {
                id: SOURCE_OTEL.into(),
                bind_host: "127.0.0.1".into(),
                bind_port: None,
            });
        }
        sources.push(SourceSpec::JsonlTail {
            id: SOURCE_JSONL.into(),
            path_template: "${CODEX_HOME}/sessions/**".into(),
        });
        sources.push(SourceSpec::JsonlTail {
            id: "codex-archived-sessions".into(),
            path_template: "${CODEX_HOME}/archived_sessions/**".into(),
        });
        Ok(sources)
    }
    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if frame.source_kind == SourceKind::JsonlTail
            && matches!(
                frame.source_id.as_str(),
                SOURCE_JSONL | "codex-archived-sessions"
            )
        {
            let key = format!(
                "{}:{}",
                frame.source_id,
                frame
                    .cursor
                    .rsplit_once(':')
                    .map(|(p, _)| p)
                    .unwrap_or(&frame.cursor)
            );
            let mut sessions = self
                .sessions
                .lock()
                .map_err(|_| AdapterError::decode_failed("session context lock"))?;
            if sessions.len() > 2048 {
                sessions.clear();
            }
            return decode_jsonl_context(
                &self.manifest,
                &self.version,
                &self.hmac_key,
                &frame,
                sessions.entry(key).or_default(),
            );
        }
        decode_frame(&self.manifest, &self.version, &self.hmac_key, frame)
    }
    async fn health(&self) -> AdapterHealth {
        if !self.detected {
            AdapterHealth::Healthy
        } else if !self.version_supported() {
            AdapterHealth::Degraded {
                reason: format!(
                    "Codex version {} is outside the verified range >=0.100.0 <0.200.0",
                    self.version
                ),
            }
        } else if !self.otel_available || !self.skills_supported() {
            AdapterHealth::Degraded {
                reason:
                    "Codex is using JSONL fallback or this version has no verified Skill invocation schema"
                        .into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

pub fn decode_frame(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    match (frame.source_kind, frame.source_id.as_str()) {
        (SourceKind::JsonlTail, SOURCE_JSONL | "codex-archived-sessions") => {
            decode_jsonl(manifest, version, hmac_key, &frame)
        }
        (SourceKind::Otlp, SOURCE_OTEL) => decode_otel(manifest, version, hmac_key, &frame),
        _ => Err(AdapterError::decode_failed(
            "unsupported or mismatched Codex source",
        )),
    }
}

fn decode_jsonl(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    decode_jsonl_context(
        manifest,
        version,
        hmac_key,
        frame,
        &mut SessionContext::default(),
    )
}
fn decode_jsonl_context(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
    context: &mut SessionContext,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let text = std::str::from_utf8(&frame.payload)
        .map_err(|e| AdapterError::decode_failed(e.to_string()))?;
    let mut events = Vec::new();
    for (index, line) in text.lines().enumerate() {
        let Ok(value) = serde_json::from_str::<Value>(line) else {
            continue;
        };
        let patch = context.observe(&value);
        let Some(mut value) = patch.or_else(|| flatten_codex_record(value)) else {
            continue;
        };
        if value.get("type").and_then(Value::as_str) == Some("token.usage") {
            if let Some(model) = &context.model {
                value
                    .as_object_mut()
                    .unwrap()
                    .entry("model")
                    .or_insert(Value::String(model.clone()));
            }
        }
        if let Some(event) = decode_record(
            manifest,
            version,
            hmac_key,
            frame,
            &value,
            (index + 1).to_string(),
        )? {
            events.push(event);
        }
    }
    Ok(events)
}

fn flatten_codex_record(value: Value) -> Option<Value> {
    let object = value.as_object()?;
    let outer = object.get("type").and_then(Value::as_str).unwrap_or("");
    if matches!(
        outer,
        "thread.started"
            | "turn.started"
            | "token.usage"
            | "tool.completed"
            | "skill.execution.completed"
            | "skill.execution.failed"
            | "patch.applied"
            | "skill.injected"
            | "future.record"
    ) {
        return Some(value);
    }
    let payload = object.get("payload").and_then(Value::as_object)?;
    let mut out = Map::new();
    if let Some(timestamp) = object.get("timestamp") {
        out.insert("timestamp".into(), timestamp.clone());
    }
    match outer {
        "session_meta" => {
            out.insert("type".into(), Value::String("thread.started".into()));
            if let Some(id) = payload
                .get("session_id")
                .or_else(|| payload.get("id"))
                .cloned()
            {
                out.insert("thread_id".into(), id);
            }
            Some(Value::Object(out))
        }
        "event_msg" => match payload.get("type").and_then(Value::as_str) {
            Some("task_started") => {
                out.insert("type".into(), Value::String("turn.started".into()));
                if let Some(turn_id) = payload.get("turn_id").cloned() {
                    out.insert("turn_id".into(), turn_id);
                }
                Some(Value::Object(out))
            }
            Some("task_complete") => {
                out.insert("type".into(), Value::String("turn.completed".into()));
                for key in ["turn_id", "duration_ms"] {
                    if let Some(v) = payload.get(key) {
                        out.insert(key.into(), v.clone());
                    }
                }
                out.insert("success".into(), Value::Bool(true));
                Some(Value::Object(out))
            }
            Some("token_count") => {
                let usage = payload
                    .get("info")
                    .and_then(Value::as_object)
                    .and_then(|info| info.get("last_token_usage"))
                    .and_then(Value::as_object)?;
                out.insert("type".into(), Value::String("token.usage".into()));
                out.insert("provider".into(), Value::String("openai".into()));
                for key in [
                    "input_tokens",
                    "output_tokens",
                    "cached_input_tokens",
                    "reasoning_output_tokens",
                    "total_tokens",
                ] {
                    if let Some(value) = usage.get(key) {
                        let dest = if key == "reasoning_output_tokens" {
                            "reasoning_tokens"
                        } else {
                            key
                        };
                        out.insert(dest.into(), value.clone());
                    }
                }
                Some(Value::Object(out))
            }
            _ => None,
        },
        "response_item" => match payload.get("type").and_then(Value::as_str) {
            Some("custom_tool_call") => {
                out.insert("type".into(), Value::String("tool.completed".into()));
                if let Some(name) = payload.get("name").cloned() {
                    out.insert("tool".into(), name);
                }
                let success = payload.get("status").and_then(Value::as_str) == Some("completed");
                out.insert("success".into(), Value::Bool(success));
                Some(Value::Object(out))
            }
            _ => None,
        },
        _ => None,
    }
}

fn decode_otel(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let text = std::str::from_utf8(&frame.payload)
        .map_err(|e| AdapterError::decode_failed(e.to_string()))?;
    let mut events = Vec::new();
    for (index, line) in text.lines().enumerate() {
        let record: Value =
            serde_json::from_str(line).map_err(|e| AdapterError::decode_failed(e.to_string()))?;
        let mut normalized = Map::new();
        let name = record
            .get("name")
            .and_then(Value::as_str)
            .unwrap_or("")
            .strip_prefix("codex.")
            .unwrap_or("");
        normalized.insert(
            "type".into(),
            Value::String(
                match name {
                    "thread.started" => "thread.started",
                    "token.usage" => "token.usage",
                    "turn.started" => "turn.started",
                    "tool.completed" => "tool.completed",
                    "skill.execution.completed" => "skill.execution.completed",
                    "skill.execution.failed" => "skill.execution.failed",
                    "patch.applied" => "patch.applied",
                    _ => "unknown",
                }
                .into(),
            ),
        );
        normalized.insert(
            "timestamp".into(),
            record.get("timestamp").cloned().unwrap_or(Value::Null),
        );
        if let Some(attrs) = record.get("attributes").and_then(Value::as_object) {
            for (key, value) in attrs {
                normalized.insert(key.replace('.', "_"), value.clone());
            }
        }
        if let Some(event) = decode_record(
            manifest,
            version,
            hmac_key,
            frame,
            &Value::Object(normalized),
            (index + 1).to_string(),
        )? {
            events.push(event);
        }
    }
    Ok(events)
}

fn decode_record(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
    value: &Value,
    sequence: String,
) -> Result<Option<NormalizedEvent>, AdapterError> {
    let Some(o) = value.as_object() else {
        return Ok(None);
    };
    let kind = o.get("type").and_then(Value::as_str).unwrap_or("");
    let occurred_at = o
        .get("timestamp")
        .and_then(Value::as_str)
        .unwrap_or("1970-01-01T00:00:00Z")
        .to_string();
    let session = string_any(o, &["thread_id", "threadId"]);
    let turn = string_any(o, &["turn_id", "turnId"]);
    let payload = match kind {
        "thread.started" => EventPayload::SessionStarted(SessionStartedPayload {
            model_id: string_any(o, &["model"]),
            workspace_hash: None,
        }),
        "turn.started" => EventPayload::TurnStarted(TurnStartedPayload {
            trigger: Some(protocol::TurnTrigger::User),
        }),
        "turn.completed" => EventPayload::TurnCompleted(TurnCompletedPayload {
            success: o.get("success").and_then(Value::as_bool).unwrap_or(false),
            duration_ms: num(o, "duration_ms"),
            error_class: None,
        }),
        "token.usage" => EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
            provider_id: string_any(o, &["provider"]).unwrap_or_else(|| "openai".into()),
            model_id: string_any(o, &["model"]).unwrap_or_else(|| "unknown".into()),
            tokens: TokenUsage {
                input_tokens: num(o, "input_tokens"),
                output_tokens: num(o, "output_tokens"),
                cache_read_tokens: num(o, "cached_input_tokens"),
                cache_write_tokens: None,
                reasoning_tokens: num(o, "reasoning_tokens"),
                tool_tokens: None,
                total_tokens: num(o, "total_tokens"),
            },
        }),
        "tool.completed" => EventPayload::ToolInvoked(ToolInvokedPayload {
            tool_category: string_any(o, &["tool"]).unwrap_or_else(|| "other".into()),
            success: o.get("success").and_then(Value::as_bool).unwrap_or(false),
            duration_ms: num(o, "duration_ms"),
        }),
        "skill.execution.completed" | "skill.execution.failed"
            if version_in_range(version, (0, 120, 0), MAX_SUPPORTED_VERSION_EXCLUSIVE) =>
        {
            let Some(skill) = string_any(o, &["skill", "skill_name"]) else {
                return Ok(None);
            };
            let Some(success) = o.get("success").and_then(Value::as_bool) else {
                return Ok(None);
            };
            let Some(_) = string_any(o, &["invocationId", "invocation_id"]) else {
                return Ok(None);
            };
            EventPayload::SkillInvoked(SkillInvokedPayload {
                skill_key: format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[&skill])),
                invoke_type: SkillInvokeType::Native,
                success,
                plugin_key: string_any(o, &["plugin", "plugin_name"])
                    .as_deref()
                    .map(|plugin| hash(hmac_key, plugin)),
                duration_ms: num(o, "duration_ms"),
            })
        }
        "patch.applied" => EventPayload::CodeChanged(CodeChangedPayload {
            added_lines: num(o, "added_lines").unwrap_or_else(|| "0".into()),
            removed_lines: num(o, "removed_lines").unwrap_or_else(|| "0".into()),
            generated_lines: num(o, "generated_lines"),
            accepted_lines: None,
            file_count: o.get("file_count").and_then(Value::as_u64).unwrap_or(0) as u32,
            language: None,
        }),
        _ => return Ok(None),
    };
    let semantic = kind;
    let cursor = format!("{}:{}", frame.cursor, sequence);
    let invocation_id = string_any(o, &["invocationId", "invocation_id"]);
    let (identity_source, identity_cursor, identity_semantic, identity_sequence) =
        if matches!(kind, "skill.execution.completed" | "skill.execution.failed") {
            (
                "codex-skill-invocation",
                invocation_id.as_deref().unwrap_or_default(),
                "skill.terminal",
                "terminal",
            )
        } else {
            (
                frame.source_id.as_str(),
                cursor.as_str(),
                semantic,
                sequence.as_str(),
            )
        };
    let raw = serde_json::to_vec(value).map_err(|e| AdapterError::decode_failed(e.to_string()))?;
    Ok(Some(EventEnvelope {
        schema_version: "1.0".into(),
        event_id: event_id(
            hmac_key,
            &frame.installation_id,
            &manifest.id,
            identity_source,
            identity_cursor,
            identity_semantic,
            identity_sequence,
        ),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: Some(version.into()),
        installation_id: frame.installation_id.clone(),
        occurred_at,
        session_hash: session.as_deref().map(|value| hash(hmac_key, value)),
        turn_hash: turn.as_deref().map(|id| {
            hash(
                hmac_key,
                &format!("{}\x1f{id}", session.as_deref().unwrap_or("")),
            )
        }),
        tool_call_hash: string_any(o, &["tool_call_id"])
            .as_deref()
            .map(|value| hash(hmac_key, value)),
        source: EventSource {
            kind: frame.source_kind,
            cursor_hmac: format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[&cursor])),
            raw_fingerprint_hmac: format!(
                "hmac-sha256:{}",
                keyed_hmac(hmac_key, &[&raw_fingerprint(&raw)])
            ),
        },
        accuracy: if kind == "patch.applied" {
            Accuracy::Derived
        } else {
            Accuracy::Exact
        },
        payload,
    }))
}

fn hash(hmac_key: &[u8], value: &str) -> String {
    format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[value]))
}
fn string_any(o: &Map<String, Value>, keys: &[&str]) -> Option<String> {
    keys.iter()
        .find_map(|k| o.get(*k).and_then(Value::as_str).map(str::to_owned))
}
fn num(o: &Map<String, Value>, key: &str) -> Option<String> {
    match o.get(key)? {
        Value::Number(n) => Some(n.to_string()),
        Value::String(s) => Some(s.clone()),
        _ => None,
    }
}
fn parsed_version(version: &str) -> (u64, u64, u64) {
    let mut parts = version
        .trim_start_matches('v')
        .split(['.', '-', '+'])
        .map(|value| value.parse::<u64>().unwrap_or(0));
    (
        parts.next().unwrap_or(0),
        parts.next().unwrap_or(0),
        parts.next().unwrap_or(0),
    )
}

fn version_at_least(version: &str, major: u64, minor: u64, patch: u64) -> bool {
    parsed_version(version) >= (major, minor, patch)
}

fn version_in_range(
    version: &str,
    minimum: (u64, u64, u64),
    maximum_exclusive: (u64, u64, u64),
) -> bool {
    let version = parsed_version(version);
    version >= minimum && version < maximum_exclusive
}
