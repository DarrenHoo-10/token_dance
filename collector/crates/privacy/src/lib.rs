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

#[derive(Debug, Clone, Default)]
pub struct PrivacyFilter;

impl PrivacyFilter {
    pub fn filter(&self, event: EventEnvelope) -> Result<EventEnvelope, PrivacyError> {
        if event.schema_version != protocol::PROTOCOL_VERSION {
            return Err(PrivacyError::UnsupportedSchema);
        }
        validate_base64url(&event.event_id, 43)
            .then_some(())
            .ok_or(PrivacyError::InvalidIdentifier("eventId"))?;
        validate_prefixed_id(&event.installation_id)
            .then_some(())
            .ok_or(PrivacyError::InvalidIdentifier("installationId"))?;
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
                validate_optional_hmac(payload.workspace_hash.as_deref(), "workspaceHash")?;
            }
            EventPayload::SkillInvoked(payload) => {
                validate_hmac(&payload.skill_key, "skillKey")?;
                validate_optional_hmac(payload.plugin_key.as_deref(), "pluginKey")?;
            }
            EventPayload::AgentSpawned(payload) => {
                validate_hmac(&payload.child_session_hash, "childSessionHash")?;
            }
            _ => {}
        }

        let value = serde_json::to_value(&event).map_err(|_| PrivacyError::Serialization)?;
        inspect_value(&value, "$")?;
        Ok(event)
    }

    pub fn filter_all(
        &self,
        events: Vec<EventEnvelope>,
    ) -> Result<Vec<EventEnvelope>, PrivacyError> {
        events.into_iter().map(|event| self.filter(event)).collect()
    }
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
    let has_unix_home =
        text.starts_with("/Users/") || text.starts_with("/home/") || text.starts_with("/root/");
    let has_secret_marker = text.contains("TOKSHOW_TEST_")
        || lowered.contains("authorization: bearer")
        || lowered.contains("api_key=")
        || lowered.contains("apikey=");
    if text.len() > MAX_STRING_BYTES || has_windows_path || has_unix_home || has_secret_marker {
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
        assert_eq!(PrivacyFilter.filter(event()).unwrap(), event());
    }

    #[test]
    fn rejects_canary_and_absolute_path_in_allowed_string_slots() {
        let mut canary = event();
        canary.agent_id = "TOKSHOW_TEST_PROMPT_SECRET".into();
        assert!(matches!(
            PrivacyFilter.filter(canary),
            Err(PrivacyError::SensitiveContent(_))
        ));

        let mut path = event();
        path.agent_id = r"C:\Users\private\session.jsonl".into();
        assert!(matches!(
            PrivacyFilter.filter(path),
            Err(PrivacyError::SensitiveContent(_))
        ));
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
