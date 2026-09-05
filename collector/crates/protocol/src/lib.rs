//! TokenShow collector protocol types generated from `schemas/protocol/v1`.

#[rustfmt::skip]
mod generated;

pub use generated::*;

#[cfg(test)]
mod tests {
    use super::*;

    fn hmac() -> String {
        format!("hmac-sha256:{}", "A".repeat(43))
    }

    #[test]
    fn model_usage_round_trip_omits_absent_fields() {
        let event = EventEnvelope {
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
        };

        let json = serde_json::to_value(&event).expect("serialize protocol event");
        assert_eq!(json["payload"]["type"], "model_usage_recorded");
        assert_eq!(json["payload"]["tokens"]["inputTokens"], "10");
        assert!(json.get("agentVersion").is_none());
        assert!(json["payload"]["tokens"].get("cacheReadTokens").is_none());

        let decoded: EventEnvelope =
            serde_json::from_value(json).expect("deserialize protocol event");
        assert_eq!(decoded, event);
    }

    #[test]
    fn unknown_payload_fields_are_rejected() {
        let json = format!(
            r#"{{"schemaVersion":"1.0","eventId":"{}","adapterId":"dev.tokenshow.adapter.mock","adapterVersion":"1.0.0","agentId":"mock-agent","installationId":"ins_{}","occurredAt":"2026-08-30T00:00:00.000Z","sessionHash":"{}","source":{{"kind":"jsonl_tail","cursorHmac":"{}","rawFingerprintHmac":"{}"}},"accuracy":"exact","payload":{{"type":"model_usage_recorded","providerId":"mock","modelId":"mock","tokens":{{"totalTokens":"1"}},"prompt":"secret"}}}}"#,
            "B".repeat(43),
            "0".repeat(26),
            hmac(),
            hmac(),
            hmac()
        );
        assert!(serde_json::from_str::<EventEnvelope>(&json).is_err());
    }
}
