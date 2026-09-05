#![forbid(unsafe_code)]

use adapter_sdk::{
    event_id, keyed_hmac, raw_fingerprint, Accuracy, AdapterError, AdapterHealth, AdapterManifest,
    AgentAdapter, Capability, CapabilityAvailability, CapabilityReport, CapabilityStatus,
    EventEnvelope, EventPayload, EventSource, NormalizedEvent, PermissionRequest, ProbeContext,
    ProbeReport, RawFrame, RollbackStep, SetupContext, SetupPlan, SourceContext, SourceKind,
    SourceSpec, TokenUsage, VerifyStep,
};
use async_trait::async_trait;
use protocol::{
    CodeChangedPayload, CostRecordedPayload, CostSource, ModelUsageRecordedPayload,
    SessionStartedPayload,
};
use serde_json::{Map, Value};

pub const ADAPTER_ID: &str = "dev.tokenshow.adapter.cursor";
pub const MANIFEST_JSON: &str = include_str!("../fixtures/manifest.json");
pub const COMPATIBILITY_JSON: &str = include_str!("../fixtures/compatibility.json");
pub const ENTERPRISE_JSON: &str = include_str!("../fixtures/contract/enterprise.json");
pub const PERSONAL_JSON: &str = include_str!("../fixtures/contract/personal.json");
pub fn load_manifest() -> AdapterManifest {
    serde_json::from_str(MANIFEST_JSON).expect("Cursor manifest")
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CursorMode {
    EnterpriseApi,
    TeamAdminApi,
    PersonalLocal,
}
impl CursorMode {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::EnterpriseApi => "enterprise_api",
            Self::TeamAdminApi => "team_admin_api",
            Self::PersonalLocal => "personal_local",
        }
    }
}

#[derive(Clone, PartialEq, Eq)]
pub struct SecretRef(String);
impl SecretRef {
    pub fn new(reference: impl Into<String>) -> Result<Self, AdapterError> {
        let reference = reference.into();
        if !reference.starts_with("secret://") {
            return Err(AdapterError::setup_failed(
                "Cursor API key must be supplied as SecretRef",
            ));
        }
        Ok(Self(reference))
    }
    pub fn handle(&self) -> &str {
        &self.0
    }
}
impl std::fmt::Debug for SecretRef {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("SecretRef([REDACTED])")
    }
}

pub struct CursorAdapter {
    manifest: AdapterManifest,
    mode: CursorMode,
    version: String,
    secret_ref: Option<SecretRef>,
    detected: bool,
    local_schema_verified: bool,
    hmac_key: Vec<u8>,
}
impl CursorAdapter {
    pub fn new(
        mode: CursorMode,
        version: impl Into<String>,
        secret_ref: Option<SecretRef>,
        hmac_key: impl Into<Vec<u8>>,
    ) -> Self {
        Self {
            manifest: load_manifest(),
            mode,
            version: version.into(),
            secret_ref,
            detected: true,
            local_schema_verified: true,
            hmac_key: hmac_key.into(),
        }
    }
    pub fn personal(version: impl Into<String>, hmac_key: impl Into<Vec<u8>>) -> Self {
        Self::new(CursorMode::PersonalLocal, version, None, hmac_key)
    }
    pub fn with_local_schema_verified(mut self, verified: bool) -> Self {
        self.local_schema_verified = verified;
        self
    }
    fn has_api_secret(&self) -> bool {
        self.secret_ref.is_some()
    }
    fn capability_report(&self) -> CapabilityReport {
        let capabilities = self
            .manifest
            .capabilities
            .iter()
            .copied()
            .map(|capability| {
                let (available, accuracy, reason) = match self.mode {
                    CursorMode::EnterpriseApi => (
                        self.has_api_secret(),
                        Some(Accuracy::Exact),
                        "CURSOR_SECRET_REF_REQUIRED",
                    ),
                    CursorMode::TeamAdminApi => match capability {
                        Capability::Code => (false, None, "CURSOR_ANALYTICS_PLAN_REQUIRED"),
                        Capability::Sessions | Capability::Turns => (
                            self.has_api_secret(),
                            Some(Accuracy::Derived),
                            "CURSOR_SECRET_REF_REQUIRED",
                        ),
                        _ => (
                            self.has_api_secret(),
                            Some(Accuracy::Exact),
                            "CURSOR_SECRET_REF_REQUIRED",
                        ),
                    },
                    CursorMode::PersonalLocal => match capability {
                        Capability::Sessions | Capability::Turns if self.local_schema_verified => {
                            (true, Some(Accuracy::Derived), "")
                        }
                        _ => (false, None, "CURSOR_PERSONAL_CAPABILITY_UNAVAILABLE"),
                    },
                };
                CapabilityStatus {
                    capability,
                    availability: if available {
                        CapabilityAvailability::Available
                    } else {
                        CapabilityAvailability::Unavailable
                    },
                    accuracy: if available { accuracy } else { None },
                    safe_reason_code: (!available).then(|| reason.into()),
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
impl AgentAdapter for CursorAdapter {
    fn manifest(&self) -> &AdapterManifest {
        &self.manifest
    }
    async fn probe(&self, _ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let api_mode = self.mode != CursorMode::PersonalLocal;
        Ok(ProbeReport {
            detected: self.detected,
            agent_version: Some(self.version.clone()),
            needs_permission: api_mode && !self.has_api_secret(),
            needs_setup: api_mode && !self.has_api_secret(),
            capability: self.capability_report(),
            detail: Some(format!("mode={}", self.mode.as_str())),
        })
    }
    async fn setup_plan(&self, _ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        let mutations = vec![];
        let mut required_permissions = vec![];
        if self.mode != CursorMode::PersonalLocal {
            required_permissions.push(PermissionRequest { code: "CURSOR_API_SECRET_REF".into(), summary: "Store the Cursor API key in the OS credential store and pass only its SecretRef".into() });
        }
        Ok(SetupPlan {
            plan_id: format!("cursor-{}-v1", self.mode.as_str()),
            adapter_id: self.manifest.id.clone(),
            summary: format!("Configure Cursor {} collection", self.mode.as_str()),
            mutations,
            required_permissions,
            verify: vec![VerifyStep {
                id: "secret-ref-only".into(),
                summary: "verify no API key value is persisted outside the OS secret store".into(),
            }],
            rollback: vec![RollbackStep {
                id: "remove-secret-ref".into(),
                summary: "remove the collector SecretRef binding".into(),
            }],
        })
    }
    async fn discover_sources(&self, _ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        Ok(match self.mode {
            CursorMode::EnterpriseApi if self.has_api_secret() => vec![
                SourceSpec::RemoteApi {
                    id: "cursor-analytics-api".into(),
                    domain: "cursor.com".into(),
                },
                SourceSpec::RemoteApi {
                    id: "cursor-admin-api".into(),
                    domain: "api.cursor.com".into(),
                },
            ],
            CursorMode::TeamAdminApi if self.has_api_secret() => vec![SourceSpec::RemoteApi {
                id: "cursor-admin-api".into(),
                domain: "api.cursor.com".into(),
            }],
            CursorMode::PersonalLocal if self.local_schema_verified => {
                vec![SourceSpec::SqliteSnapshot {
                    id: "cursor-personal-local".into(),
                    path_template: "${AGENT_CONFIG_HOME}/User/globalStorage/state.vscdb".into(),
                }]
            }
            _ => vec![],
        })
    }
    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        if self.mode == CursorMode::PersonalLocal && !self.local_schema_verified {
            return Ok(vec![]);
        }
        if self.mode != CursorMode::PersonalLocal && !self.has_api_secret() {
            return Err(AdapterError::decode_failed(
                "Cursor API mode requires a SecretRef",
            ));
        }
        decode_frame(
            &self.manifest,
            &self.version,
            self.mode,
            &self.hmac_key,
            frame,
        )
    }
    async fn health(&self) -> AdapterHealth {
        if self.mode != CursorMode::PersonalLocal && !self.has_api_secret() {
            AdapterHealth::Permission {
                reason: "Cursor API SecretRef is required".into(),
            }
        } else if self.mode == CursorMode::PersonalLocal {
            AdapterHealth::Degraded {
                reason: if self.local_schema_verified {
                    "personal_local exposes only verified session metadata".into()
                } else {
                    "personal_local schema is unverified".into()
                },
            }
        } else if self.mode == CursorMode::TeamAdminApi {
            AdapterHealth::Degraded {
                reason: "team_admin_api does not include Enterprise Analytics code metrics".into(),
            }
        } else {
            AdapterHealth::Healthy
        }
    }
}

pub fn decode_frame(
    manifest: &AdapterManifest,
    version: &str,
    mode: CursorMode,
    hmac_key: &[u8],
    frame: RawFrame,
) -> Result<Vec<NormalizedEvent>, AdapterError> {
    let expected_source = if mode == CursorMode::PersonalLocal {
        SourceKind::SqliteSnapshot
    } else {
        SourceKind::RemoteApi
    };
    let source_id_allowed = match mode {
        CursorMode::EnterpriseApi => {
            matches!(
                frame.source_id.as_str(),
                "cursor-analytics-api" | "cursor-admin-api"
            )
        }
        CursorMode::TeamAdminApi => frame.source_id == "cursor-admin-api",
        CursorMode::PersonalLocal => frame.source_id == "cursor-personal-local",
    };
    if frame.source_kind != expected_source || !source_id_allowed {
        return Err(AdapterError::decode_failed(
            "Cursor source does not match runtime mode",
        ));
    }
    let root: Value = serde_json::from_slice(&frame.payload)
        .map_err(|e| AdapterError::decode_failed(e.to_string()))?;
    let records = root
        .get("events")
        .and_then(Value::as_array)
        .cloned()
        .unwrap_or_default();
    let mut events = Vec::new();
    for (index, value) in records.iter().enumerate() {
        if let Some(event) =
            decode_record(manifest, version, mode, hmac_key, &frame, value, index + 1)?
        {
            events.push(event);
        }
        if mode != CursorMode::PersonalLocal
            && value.get("type").and_then(Value::as_str) == Some("usage")
            && value.get("cost").is_some()
        {
            let mut cost = value.clone();
            if let Some(object) = cost.as_object_mut() {
                object.insert("type".into(), Value::String("usage_cost".into()));
            }
            if let Some(event) =
                decode_record(manifest, version, mode, hmac_key, &frame, &cost, index + 1)?
            {
                events.push(event);
            }
        }
    }
    Ok(events)
}

fn decode_record(
    manifest: &AdapterManifest,
    version: &str,
    mode: CursorMode,
    hmac_key: &[u8],
    frame: &RawFrame,
    value: &Value,
    sequence: usize,
) -> Result<Option<NormalizedEvent>, AdapterError> {
    let Some(o) = value.as_object() else {
        return Ok(None);
    };
    let kind = o.get("type").and_then(Value::as_str).unwrap_or("");
    let session = string(o, "conversationId");
    let (payload, accuracy) = match kind {
        "usage" if mode != CursorMode::PersonalLocal => (
            EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                provider_id: string(o, "provider").unwrap_or_else(|| "unknown".into()),
                model_id: string(o, "model").unwrap_or_else(|| "unknown".into()),
                tokens: TokenUsage {
                    input_tokens: number(o, "inputTokens"),
                    output_tokens: number(o, "outputTokens"),
                    cache_read_tokens: number(o, "cacheReadTokens"),
                    cache_write_tokens: number(o, "cacheWriteTokens"),
                    reasoning_tokens: number(o, "reasoningTokens"),
                    tool_tokens: None,
                    total_tokens: number(o, "totalTokens"),
                },
            }),
            Accuracy::Exact,
        ),
        "usage_cost" | "usage" if mode != CursorMode::PersonalLocal && o.contains_key("cost") => (
            EventPayload::CostRecorded(CostRecordedPayload {
                amount: number(o, "cost").unwrap_or_else(|| "0".into()),
                currency: string(o, "currency").unwrap_or_else(|| "USD".into()),
                source: CostSource::ProviderReported,
                discount_amount: None,
            }),
            Accuracy::Exact,
        ),
        "accepted_code" if mode == CursorMode::EnterpriseApi => (
            EventPayload::CodeChanged(CodeChangedPayload {
                added_lines: number(o, "addedLines").unwrap_or_else(|| "0".into()),
                removed_lines: number(o, "removedLines").unwrap_or_else(|| "0".into()),
                generated_lines: None,
                accepted_lines: number(o, "acceptedLines"),
                file_count: o.get("fileCount").and_then(Value::as_u64).unwrap_or(0) as u32,
                language: string(o, "language"),
            }),
            Accuracy::Exact,
        ),
        "conversation" => (
            EventPayload::SessionStarted(SessionStartedPayload {
                model_id: string(o, "model"),
                workspace_hash: None,
            }),
            if mode == CursorMode::PersonalLocal {
                Accuracy::Derived
            } else {
                Accuracy::Exact
            },
        ),
        _ => return Ok(None),
    };
    let cursor = format!("{}:{sequence}", frame.cursor);
    let occurred_at = string(o, "timestamp").unwrap_or_else(|| "1970-01-01T00:00:00Z".into());
    let (identity_source, platform_event_id, identity_sequence) =
        if mode == CursorMode::PersonalLocal {
            let Some(conversation_id) = session.as_deref() else {
                return Ok(None);
            };
            (
                "cursor-personal-conversation",
                raw_fingerprint(conversation_id.as_bytes()),
                "stable",
            )
        } else {
            let Some(platform_event_id) = string(o, "eventId").or_else(|| string(o, "requestId"))
            else {
                return Ok(None);
            };
            (
                "cursor-platform-event",
                platform_event_id,
                occurred_at.as_str(),
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
            &platform_event_id,
            kind,
            identity_sequence,
        ),
        adapter_id: manifest.id.clone(),
        adapter_version: manifest.version.clone(),
        agent_id: manifest.agent.id.clone(),
        agent_version: Some(version.into()),
        installation_id: frame.installation_id.clone(),
        occurred_at,
        session_hash: session.as_deref().map(|value| hash(hmac_key, value)),
        turn_hash: string(o, "requestId").as_deref().map(|id| {
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
