#![forbid(unsafe_code)]

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    EventEnvelope, EventPayload, EventSource, NormalizedEvent, ProbeContext, ProbeReport, RawFrame,
    SetupContext, SetupPlan, SourceContext, SourceKind, SourceSpec, TokenUsage,
};
use async_trait::async_trait;
use protocol::{
    ModelUsageRecordedPayload, SessionStartedPayload, SkillInvokeType, SkillInvokedPayload,
    ToolInvokedPayload, TurnCompletedPayload,
};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.zcode";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const KNOWN_JSON: &str = include_str!("../fixtures/contract/known.json");
pub const UNKNOWN_JSON: &str = include_str!("../fixtures/contract/unknown.json");
pub const RUNTIME_JSON: &str = include_str!("../fixtures/contract/runtime.json");
pub const FINGERPRINT_V1: &str = "zcode-sqlite-v1-uv7";
pub const FINGERPRINT_V2: &str = "zcode-sqlite-v2-uv9";
pub const FINGERPRINT_V3: &str = "zcode-sqlite-v3-uv0";
const V1_QUERIES: &[&str] = &["SELECT id, created_at, model FROM sessions WHERE id > ? ORDER BY id", "SELECT id, session_id, finished_at, input_tokens, output_tokens, tool_count FROM steps WHERE id > ? ORDER BY id"];
const V2_QUERIES: &[&str] = &["SELECT id, created_at, model FROM sessions WHERE id > ? ORDER BY id", "SELECT id, session_id, finished_at, input_tokens, output_tokens, total_tokens, tool_count, skill_name FROM step_metrics WHERE id > ? ORDER BY id"];
const V3_QUERIES: &[&str] = &["SELECT rowid AS id, id AS session_ref, time_created FROM session WHERE rowid > ? ORDER BY rowid", "SELECT rowid AS id, session_id, provider_id, model_id, input_tokens, output_tokens, computed_total_tokens, tool_call_count, completed_at FROM model_usage WHERE rowid > ? AND status = 'completed' ORDER BY rowid"];

pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("ZCode manifest")
}
pub fn verified_query_plan(fingerprint: &str) -> Option<&'static [&'static str]> {
    match fingerprint {
        FINGERPRINT_V1 => Some(V1_QUERIES),
        FINGERPRINT_V2 => Some(V2_QUERIES),
        FINGERPRINT_V3 => Some(V3_QUERIES),
        _ => None,
    }
}
pub fn fingerprint_supported(fingerprint: &str) -> bool {
    verified_query_plan(fingerprint).is_some()
}

pub fn compatibility_supported(version: &str, fingerprint: &str) -> bool {
    let version = parsed_version(version);
    match fingerprint {
        FINGERPRINT_V1 => ((1, 0, 0)..(1, 2, 0)).contains(&version),
        FINGERPRINT_V2 => ((1, 2, 0)..(2, 0, 0)).contains(&version),
        FINGERPRINT_V3 => ((0, 0, 0)..(1, 0, 0)).contains(&version),
        _ => false,
    }
}

pub struct ZCodeAdapter {
    manifest: AdapterManifest,
    version: String,
    fingerprint: String,
    runtime_verified: bool,
    detected: bool,
    hmac_key: Vec<u8>,
}
impl ZCodeAdapter {
    pub fn new(
        version: impl Into<String>,
        fingerprint: impl Into<String>,
        hmac_key: impl Into<Vec<u8>>,
    ) -> Self {
        Self {
            manifest: load_manifest(),
            version: version.into(),
            fingerprint: fingerprint.into(),
            runtime_verified: true,
            detected: true,
            hmac_key: hmac_key.into(),
        }
    }
    pub fn with_runtime_verified(mut self, verified: bool) -> Self {
        self.runtime_verified = verified;
        self
    }
    fn schema_known(&self) -> bool {
        compatibility_supported(&self.version, &self.fingerprint)
    }
    fn capability_report(&self) -> CapabilityReport {
        let schema = self.schema_known();
        let capabilities = self
            .manifest
            .capabilities
            .iter()
            .copied()
            .map(|capability| {
                let available =
                    schema || (self.runtime_verified && capability == Capability::Tools);
                let accuracy = if available {
                    Some(if capability == Capability::Turns {
                        Accuracy::Derived
                    } else {
                        Accuracy::Exact
                    })
                } else {
                    None
                };
                CapabilityStatus {
                    capability,
                    availability: if available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy,
                    safe_reason_code: (!available)
                        .then(|| "ZCODE_SCHEMA_FINGERPRINT_UNSUPPORTED".into()),
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
impl AgentAdapter for ZCodeAdapter {
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
                "当前 ZCode 版本尚未适配".into()
            }),
        })
    }
    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        Ok(SetupPlan {
            plan_id: "zcode-readonly-v1".into(),
            adapter_id: self.manifest.id.clone(),
            summary: "Use only fingerprint-verified read-only ZCode sources".into(),
            mutations: vec![],
            required_permissions: vec![],
            verify: vec![],
            rollback: vec![],
        })
    }
    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        let mut sources = vec![];
        if self.runtime_verified {
            sources.push(SourceSpec::RuntimeStream {
                id: "zcode-runtime-events".into(),
                stream_id: "zcode.session.events.v1".into(),
            });
        }
        if self.schema_known() {
            sources.push(SourceSpec::SqliteSnapshot {
                id: "zcode-sqlite".into(),
                path_template: "${AGENT_CONFIG_HOME}/cli/db/db.sqlite".into(),
            });
        }
        Ok(sources)
    }
    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        match (frame.source_kind, frame.source_id.as_str()) {
            (SourceKind::RuntimeStream, "zcode-runtime-events") if self.runtime_verified => {
                decode_records(&self.manifest, &self.version, &self.hmac_key, &frame)
            }
            (SourceKind::SqliteSnapshot, "zcode-sqlite") if self.schema_known() => {
                let root: Value = serde_json::from_slice(&frame.payload)
                    .map_err(|e| AdapterError::decode_failed(e.to_string()))?;
                if root.get("fingerprint").and_then(Value::as_str)
                    != Some(self.fingerprint.as_str())
                {
                    return Ok(vec![]);
                }
                decode_records(&self.manifest, &self.version, &self.hmac_key, &frame)
            }
            (SourceKind::SqliteSnapshot, "zcode-sqlite") => Ok(vec![]),
            _ => Err(AdapterError::decode_failed(
                "unsupported or unverified ZCode source",
            )),
        }
    }
    async fn health(&self) -> AdapterHealth {
        if !self.schema_known() {
            AdapterHealth::Degraded {
                reason: "当前 ZCode 版本尚未适配；未知 schema 不执行 SQL".into(),
            }
        } else if !self.runtime_verified {
            AdapterHealth::Degraded {
                reason: "ZCode runtime event schema is unavailable; using verified SQLite only"
                    .into(),
            }
        } else {
            AdapterHealth::Healthy
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
        .map_err(|e| AdapterError::decode_failed(e.to_string()))?;
    let records = root
        .get("records")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    records
        .iter()
        .enumerate()
        .filter_map(
            |(i, r)| match decode_record(manifest, version, hmac_key, frame, r, i + 1) {
                Ok(Some(e)) => Some(Ok(e)),
                Ok(None) => None,
                Err(e) => Some(Err(e)),
            },
        )
        .collect()
}

fn decode_record(
    manifest: &AdapterManifest,
    version: &str,
    hmac_key: &[u8],
    frame: &RawFrame,
    value: &Value,
    sequence: usize,
) -> Result<Option<NormalizedEvent>, AdapterError> {
    let Some(o) = value.as_object() else {
        return Ok(None);
    };
    let kind = o.get("type").and_then(Value::as_str).unwrap_or("");
    let session = string(o, "sessionId");
    let turn = string(o, "stepId");
    let (payload, accuracy) = match kind {
        "session" => (
            EventPayload::SessionStarted(SessionStartedPayload {
                model_id: string(o, "model"),
                workspace_hash: None,
            }),
            Accuracy::Exact,
        ),
        "step_finish" => (
            EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                provider_id: string(o, "provider").unwrap_or_else(|| "zai".into()),
                model_id: string(o, "model").unwrap_or_else(|| "unknown".into()),
                tokens: TokenUsage {
                    input_tokens: number(o, "inputTokens"),
                    output_tokens: number(o, "outputTokens"),
                    cache_read_tokens: None,
                    cache_write_tokens: None,
                    reasoning_tokens: None,
                    tool_tokens: None,
                    total_tokens: number(o, "totalTokens"),
                },
            }),
            Accuracy::Exact,
        ),
        "turn_finish" => {
            let Some(success) = o.get("success").and_then(Value::as_bool) else {
                return Ok(None);
            };
            (
                EventPayload::TurnCompleted(TurnCompletedPayload {
                    success,
                    duration_ms: number(o, "durationMs"),
                    error_class: string(o, "errorClass"),
                }),
                Accuracy::Derived,
            )
        }
        "tool" => (
            EventPayload::ToolInvoked(ToolInvokedPayload {
                tool_category: string(o, "tool").unwrap_or_else(|| "other".into()),
                success: o.get("success").and_then(Value::as_bool).unwrap_or(false),
                duration_ms: number(o, "durationMs"),
            }),
            Accuracy::Exact,
        ),
        "skill" => {
            let Some(skill) = string(o, "skillName") else {
                return Ok(None);
            };
            let Some(success) = o.get("success").and_then(Value::as_bool) else {
                return Ok(None);
            };
            (
                EventPayload::SkillInvoked(SkillInvokedPayload {
                    skill_key: hash(hmac_key, &skill),
                    invoke_type: SkillInvokeType::RuntimeCorrelated,
                    success,
                    plugin_key: None,
                    duration_ms: number(o, "durationMs"),
                }),
                Accuracy::Exact,
            )
        }
        _ => return Ok(None),
    };
    let cursor = format!("{}:{sequence}", frame.cursor);
    let raw = serde_json::to_vec(value).map_err(|e| AdapterError::decode_failed(e.to_string()))?;
    Ok(Some(EventEnvelope {
        schema_version: "1.0".into(),
        event_id: event_id(
            hmac_key,
            &frame.installation_id,
            &manifest.id,
            &frame.source_id,
            &cursor,
            kind,
            &sequence.to_string(),
        ),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: Some(version.into()),
        installation_id: frame.installation_id.clone(),
        occurred_at: string(o, "timestamp").unwrap_or_else(|| "1970-01-01T00:00:00Z".into()),
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
    }))
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

fn hash(hmac_key: &[u8], value: &str) -> String {
    format!("hmac-sha256:{}", keyed_hmac(hmac_key, &[value]))
}
fn string(o: &Map<String, Value>, key: &str) -> Option<String> {
    o.get(key).and_then(Value::as_str).map(str::to_owned)
}
fn number(o: &Map<String, Value>, key: &str) -> Option<String> {
    match o.get(key)? {
        Value::String(s) => Some(s.clone()),
        Value::Number(n) => Some(n.to_string()),
        _ => None,
    }
}
