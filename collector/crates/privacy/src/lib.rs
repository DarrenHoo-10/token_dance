//! Final local privacy boundary between Adapter output and durable storage.

use protocol::{EventEnvelope, EventPayload};
use serde_json::Value;

const HMAC_PREFIX: &str = "hmac-sha256:";
const MAX_STRING_BYTES: usize = 2_048;

#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
pub enum PrivacyError {
    #[error("unsupported schema version")]
    UnsupportedSchema,
    #[error("invalid safe identifier in {0}")]
    InvalidIdentifier(&'static str),
    #[error("invalid HMAC in {0}")]
    InvalidHmac(&'static str),
    #[error("sensitive content rejected in {0}")]
    SensitiveContent(String),
    #[error("event serialization failed")]
    Serialization,
}

#[derive(Debug, Clone, PartialEq)]
pub struct PrivacyCheckedEvent(EventEnvelope);

impl PrivacyCheckedEvent {
    pub fn as_envelope(&self) -> &EventEnvelope {
        &self.0
    }

    pub fn into_envelope(self) -> EventEnvelope {
        self.0
    }
}

#[derive(Debug, Clone, Default)]
pub struct PrivacyFilter;

impl PrivacyFilter {
    pub fn filter(&self, event: EventEnvelope) -> Result<PrivacyCheckedEvent, PrivacyError> {
        if event.schema_version != protocol::PROTOCOL_VERSION {
            return Err(PrivacyError::UnsupportedSchema);
        }
        validate_base64url(&event.event_id, 43)
            .then_some(())
            .ok_or(PrivacyError::InvalidIdentifier("eventId"))?;
        validate_prefixed_id(&event.installation_id)
            .then_some(())
            .ok_or(PrivacyError::InvalidIdentifier("installationId"))?;
        validate_identifier(&event.adapter_id, "adapterId")?;
        validate_version(&event.adapter_version, "adapterVersion")?;
        validate_identifier(&event.agent_id, "agentId")?;
        if let Some(agent_version) = event.agent_version.as_deref() {
            validate_version(agent_version, "agentVersion")?;
        }
        validate_hmac(&event.source.cursor_hmac, "source.cursorHmac")?;
        validate_hmac(
            &event.source.raw_fingerprint_hmac,
            "source.rawFingerprintHmac",
        )?;
        validate_optional_hmac(event.session_hash.as_deref(), "sessionHash")?;
        validate_optional_hmac(event.turn_hash.as_deref(), "turnHash")?;
        validate_optional_hmac(event.tool_call_hash.as_deref(), "toolCallHash")?;
        match &event.payload {
            EventPayload::SessionStarted(payload) => {
                validate_optional_identifier(payload.model_id.as_deref(), "modelId")?;
                validate_optional_hmac(payload.workspace_hash.as_deref(), "workspaceHash")?;
            }
            EventPayload::TurnCompleted(payload) => {
                validate_optional_identifier(payload.error_class.as_deref(), "errorClass")?;
            }
            EventPayload::ModelUsageRecorded(payload) => {
                validate_identifier(&payload.provider_id, "providerId")?;
                validate_identifier(&payload.model_id, "modelId")?;
            }
            EventPayload::ToolInvoked(payload) => {
                validate_identifier(&payload.tool_category, "toolCategory")?;
            }
            EventPayload::SkillInvoked(payload) => {
                validate_hmac(&payload.skill_key, "skillKey")?;
                validate_optional_hmac(payload.plugin_key.as_deref(), "pluginKey")?;
            }
            EventPayload::CodeChanged(payload) => {
                validate_optional_identifier(payload.language.as_deref(), "language")?;
            }
            EventPayload::AgentSpawned(payload) => {
                validate_hmac(&payload.child_session_hash, "childSessionHash")?;
                validate_identifier(&payload.spawned_agent_type, "spawnedAgentType")?;
            }
            _ => {}
        }

        let value = serde_json::to_value(&event).map_err(|_| PrivacyError::Serialization)?;
        inspect_value(&value, "$")?;
        Ok(PrivacyCheckedEvent(event))
    }

    pub fn filter_all(
        &self,
        events: Vec<EventEnvelope>,
    ) -> Result<Vec<PrivacyCheckedEvent>, PrivacyError> {
        events.into_iter().map(|event| self.filter(event)).collect()
    }
}

fn validate_version(value: &str, field: &'static str) -> Result<(), PrivacyError> {
    let safe = !value.is_empty()
        && value.len() <= 64
        && value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b':' | b'-' | b'+')
        });
    if safe {
        Ok(())
    } else {
        Err(PrivacyError::InvalidIdentifier(field))
    }
}

fn validate_optional_identifier(
    value: Option<&str>,
    field: &'static str,
) -> Result<(), PrivacyError> {
    match value {
        Some(value) => validate_identifier(value, field),
        None => Ok(()),
    }
}

fn validate_identifier(value: &str, field: &'static str) -> Result<(), PrivacyError> {
    let safe = !value.is_empty()
        && value.len() <= 160
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b':' | b'-'))
        && !looks_like_secret(value);
    if safe {
        Ok(())
    } else {
        Err(PrivacyError::InvalidIdentifier(field))
    }
}

fn looks_like_secret(value: &str) -> bool {
    let lowered = value.to_ascii_lowercase();
    (lowered.starts_with("sk-") && value.len() > 20)
        || lowered.starts_with("ghp_")
        || lowered.starts_with("xoxb-")
        || lowered.starts_with("bearer")
}

fn validate_optional_hmac(value: Option<&str>, field: &'static str) -> Result<(), PrivacyError> {
    match value {
        Some(value) => validate_hmac(value, field),
        None => Ok(()),
    }
}

fn validate_hmac(value: &str, field: &'static str) -> Result<(), PrivacyError> {
    let encoded = value
        .strip_prefix(HMAC_PREFIX)
        .ok_or(PrivacyError::InvalidHmac(field))?;
    if validate_base64url(encoded, 43) {
        Ok(())
    } else {
        Err(PrivacyError::InvalidHmac(field))
    }
}

fn validate_base64url(value: &str, length: usize) -> bool {
    value.len() == length
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_' || byte == b'-')
}

fn validate_prefixed_id(value: &str) -> bool {
    value.len() == 30
        && value.as_bytes().get(3) == Some(&b'_')
        && value[..3].bytes().all(|byte| byte.is_ascii_lowercase())
        && value[4..].bytes().all(|byte| byte.is_ascii_alphanumeric())
}

fn inspect_value(value: &Value, path: &str) -> Result<(), PrivacyError> {
    match value {
        Value::String(text) => inspect_string(text, path),
        Value::Array(items) => {
            for (index, item) in items.iter().enumerate() {
                inspect_value(item, &format!("{path}[{index}]"))?;
            }
            Ok(())
        }
        Value::Object(fields) => {
            for (key, item) in fields {
                let lowered = key.to_ascii_lowercase();
                if [
                    "prompt",
                    "response",
                    "reasoningcontent",
                    "sourcecode",
                    "diffcontent",
                    "toolarguments",
                    "tooloutput",
                    "authorization",
                    "credential",
                ]
                .contains(&lowered.as_str())
                {
                    return Err(PrivacyError::SensitiveContent(format!("{path}.{key}")));
                }
                inspect_value(item, &format!("{path}.{key}"))?;
            }
            Ok(())
        }
        _ => Ok(()),
    }
}

fn inspect_string(text: &str, path: &str) -> Result<(), PrivacyError> {
    let lowered = text.to_ascii_lowercase();
    let has_windows_path = text.as_bytes().get(1) == Some(&b':')
        && text
            .as_bytes()
            .get(2)
            .is_some_and(|byte| *byte == b'\\' || *byte == b'/');
    let has_absolute_path = text.starts_with('/') || text.starts_with("\\\\");
    let has_secret_marker = lowered.contains("authorization: bearer")
        || lowered.contains("api_key=")
        || lowered.contains("apikey=")
        || looks_like_secret(text);
    let has_unsafe_text = text.chars().any(char::is_control) || text.contains("://");
    if text.len() > MAX_STRING_BYTES
        || has_windows_path
        || has_absolute_path
        || has_secret_marker
        || has_unsafe_text
    {
        return Err(PrivacyError::SensitiveContent(path.to_string()));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use protocol::{Accuracy, EventSource, ModelUsageRecordedPayload, SourceKind, TokenUsage};

    fn hmac() -> String {
        format!("hmac-sha256:{}", "A".repeat(43))
    }

    fn event() -> EventEnvelope {
        EventEnvelope {
            schema_version: "1.0".into(),
            event_id: "B".repeat(43),
            adapter_id: "dev.tokenshow.adapter.mock".into(),
            adapter_version: "1.0.0".into(),
            agent_id: "mock-agent".into(),
            agent_version: None,
            installation_id: format!("ins_{}", "0".repeat(26)),
            occurred_at: "2026-08-30T00:00:00.000Z".into(),
            session_hash: Some(hmac()),
            turn_hash: None,
            tool_call_hash: None,
            source: EventSource {
                kind: SourceKind::JsonlTail,
                cursor_hmac: hmac(),
                raw_fingerprint_hmac: hmac(),
            },
            accuracy: Accuracy::Exact,
            payload: EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
                provider_id: "mock-provider".into(),
                model_id: "mock-model".into(),
                tokens: TokenUsage {
                    input_tokens: Some("10".into()),
                    output_tokens: Some("5".into()),
                    cache_read_tokens: None,
                    cache_write_tokens: None,
                    reasoning_tokens: None,
                    tool_tokens: None,
                    total_tokens: Some("15".into()),
                },
            }),
        }
    }

    #[test]
    fn accepts_schema_valid_content_free_event() {
        assert_eq!(
            PrivacyFilter.filter(event()).unwrap().as_envelope(),
            &event()
        );
    }

    #[test]
    fn rejects_prose_secret_and_absolute_path_in_identifier_slots() {
        let mut prose = event();
        prose.agent_id = "Please refactor the private payment code".into();
        assert_eq!(
            PrivacyFilter.filter(prose),
            Err(PrivacyError::InvalidIdentifier("agentId"))
        );

        let mut path = event();
        path.agent_id = r"C:\Users\private\session.jsonl".into();
        assert_eq!(
            PrivacyFilter.filter(path),
            Err(PrivacyError::InvalidIdentifier("agentId"))
        );

        let mut version = event();
        version.agent_version = Some("private prompt content".into());
        assert_eq!(
            PrivacyFilter.filter(version),
            Err(PrivacyError::InvalidIdentifier("agentVersion"))
        );
    }

    #[test]
    fn rejects_raw_source_cursor_and_wrong_schema() {
        let mut raw_cursor = event();
        raw_cursor.source.cursor_hmac = "offset:4096".into();
        assert_eq!(
            PrivacyFilter.filter(raw_cursor),
            Err(PrivacyError::InvalidHmac("source.cursorHmac"))
        );

        let mut wrong_schema = event();
        wrong_schema.schema_version = "2.0".into();
        assert_eq!(
            PrivacyFilter.filter(wrong_schema),
            Err(PrivacyError::UnsupportedSchema)
        );
    }
}
