use std::sync::Arc;

use adapter_qoder::{
    decode_transcript, load_manifest, version_supported, QoderAdapter, ADAPTER_ID,
    COMPATIBILITY_JSON, CREDITS_CURRENCY, EXPOSE_TOKEN_USAGE_ENV, HISTORY_SOURCE_ID, KNOWN_LOG,
};
use adapter_host::AdapterHost;
use adapter_sdk::{
    AgentAdapter, Capability, ErrorCode, EventPayload, ProbeContext, RawFrame, SourceContext,
    SourceKind,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"qoder-contract-device-hmac";

fn frame(cursor: &str, payload: &str) -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::JsonlTail,
        source_id: HISTORY_SOURCE_ID.into(),
        cursor: cursor.into(),
        payload: payload.as_bytes().to_vec(),
    }
}

fn decode(frame: RawFrame) -> Result<Vec<adapter_sdk::NormalizedEvent>, adapter_sdk::AdapterError> {
    let adapter = QoderAdapter::for_version("1.1.75-hotfix.1", KEY);
    decode_transcript(adapter.manifest(), Some("1.1.75-hotfix.1"), KEY, &frame)
}

#[tokio::test]
async fn known_transcript_decodes_credits_and_diffed_tokens() {
    let adapter = Arc::new(QoderAdapter::for_version("1.1.75-hotfix.1", KEY));
    let mut host = AdapterHost::new();
    host.register(adapter).unwrap();
    let report = host
        .probe(
            ADAPTER_ID,
            ProbeContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert!(report.detected);
    for capability in [Capability::Tokens, Capability::Cost, Capability::Sessions] {
        assert!(report.capability.available().contains(&capability));
    }

    let sources = host
        .discover_sources(
            ADAPTER_ID,
            SourceContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(sources.len(), 1);
    assert_eq!(sources[0].id(), HISTORY_SOURCE_ID);

    let events = decode(frame("offset:0", KNOWN_LOG)).unwrap();
    // req-a1 credits only (tokens zeroed), req-a2 baseline only + credits,
    // req-a3 token delta + credits, req-a4 unchanged + zero credits.
    assert_eq!(events.len(), 4);

    let costs: Vec<_> = events
        .iter()
        .filter(|e| matches!(e.payload, EventPayload::CostRecorded(_)))
        .collect();
    assert_eq!(costs.len(), 3);
    if let EventPayload::CostRecorded(cost) = &costs[0].payload {
        assert_eq!(cost.amount, "0.5");
        assert_eq!(cost.currency, CREDITS_CURRENCY);
        assert_eq!(cost.source, protocol::CostSource::ProviderReported);
        assert_eq!(cost.discount_amount.as_deref(), Some("0.1"));
    } else {
        panic!("expected cost payload");
    }
    if let EventPayload::CostRecorded(cost) = &costs[1].payload {
        assert_eq!(cost.amount, "0.0226");
        assert!(cost.discount_amount.is_none());
    } else {
        panic!("expected cost payload");
    }

    let usage: Vec<_> = events
        .iter()
        .filter(|e| matches!(e.payload, EventPayload::ModelUsageRecorded(_)))
        .collect();
    assert_eq!(usage.len(), 1, "first exposed observation is baseline only");
    if let EventPayload::ModelUsageRecorded(recorded) = &usage[0].payload {
        assert_eq!(recorded.provider_id, "qoder");
        // Routing alias is reported as-is; no guessing the concrete model.
        assert_eq!(recorded.model_id, "dfmodel");
        // Delta: in 2785 (includes cr 2272), read 2272, write 20, out 57.
        assert_eq!(recorded.tokens.input_tokens.as_deref(), Some("513"));
        assert_eq!(recorded.tokens.cache_read_tokens.as_deref(), Some("2272"));
        assert_eq!(recorded.tokens.cache_write_tokens.as_deref(), Some("20"));
        assert_eq!(recorded.tokens.output_tokens.as_deref(), Some("57"));
        assert_eq!(recorded.tokens.total_tokens.as_deref(), Some("2862"));
    } else {
        panic!("expected usage payload");
    }

    for event in events {
        PrivacyFilter.filter(event).unwrap();
    }
}

#[tokio::test]
async fn transcript_noise_never_leaks_content() {
    let events = decode(frame("offset:0", KNOWN_LOG)).unwrap();
    let json = serde_json::to_string(&events).unwrap();
    for forbidden in [
        "QODER_PROMPT_CANARY",
        "QODER_TEXT_CANARY",
        "QODER_THINKING_CANARY",
        "QODER_CODE_CANARY",
        "QODER_TOOL_OUTPUT_CANARY",
        "QODER_MALFORMED_CANARY",
        // Raw session ids and machine paths stay inside the adapter.
        "qoder-sess-alpha-0001",
        "C:\\\\Users\\\\fixture",
    ] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
}

#[tokio::test]
async fn truncated_and_malformed_lines_are_dropped() {
    let events = decode(frame("offset:0", KNOWN_LOG)).unwrap();
    assert_eq!(events.len(), 4);
}

#[tokio::test]
async fn turn_identity_is_stable_per_request() {
    let adapter = QoderAdapter::for_version("1.1.75-hotfix.1", KEY);
    let line = KNOWN_LOG
        .lines()
        .find(|line| line.contains("req-a3") && line.contains("end_turn"))
        .unwrap();
    let first = adapter
        .decode(frame("offset:0", line))
        .await
        .unwrap()
        .remove(0);
    let replayed = adapter
        .decode(frame("offset:4096", line))
        .await
        .unwrap()
        .remove(0);
    assert_eq!(first.turn_hash, replayed.turn_hash);
    let other = adapter
        .decode(frame(
            "offset:0",
            KNOWN_LOG
                .lines()
                .find(|line| line.contains("req-a2") && line.contains("end_turn"))
                .unwrap(),
        ))
        .await
        .unwrap()
        .remove(0);
    assert_ne!(first.turn_hash, other.turn_hash);
}

#[tokio::test]
async fn setup_plan_sets_env_without_permission_gate() {
    let adapter = QoderAdapter::for_version("1.1.75-hotfix.1", KEY);
    let plan = adapter
        .setup_plan(adapter_sdk::SetupContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(plan.required_permissions.is_empty(), "not a dangerous permission");
    assert_eq!(plan.mutations.len(), 1);
    match &plan.mutations[0] {
        adapter_sdk::ConfigMutation::EnvironmentSet { scope, key, value_ref } => {
            assert_eq!(scope, "User");
            assert_eq!(key, EXPOSE_TOKEN_USAGE_ENV);
            assert_eq!(value_ref, "value://true");
        }
        other => panic!("expected EnvironmentSet, got {other:?}"),
    }
    assert!(!plan.verify.is_empty());
    assert!(!plan.rollback.is_empty());
}

#[tokio::test]
async fn undetected_adapter_reports_no_capabilities() {
    let adapter = QoderAdapter::undetected(KEY);
    let report = adapter
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(!report.detected);
    assert!(report.agent_version.is_none());
    assert!(report.capability.available().is_empty());
}

#[tokio::test]
async fn unknown_version_degrades_but_keeps_credits() {
    let adapter = QoderAdapter::for_version("9.9.9", KEY);
    let report = adapter
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report.capability.missing().contains(&Capability::Tokens));
    assert!(report.capability.available().contains(&Capability::Cost));
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
    let events = decode(frame("offset:0", KNOWN_LOG)).unwrap();
    assert_eq!(events.len(), 4, "verified transcript still decodes");
}

#[tokio::test]
async fn source_mismatch_is_rejected_explicitly() {
    let adapter = QoderAdapter::for_version("1.1.75-hotfix.1", KEY);
    let wrong_source = RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::JsonlTail,
        source_id: "wrong-source".into(),
        cursor: "offset:0".into(),
        payload: KNOWN_LOG.as_bytes().to_vec(),
    };
    let error = adapter.decode(wrong_source).await.unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
}

#[test]
fn manifest_compatibility_are_stable() {
    adapter_sdk::validate_manifest(&load_manifest()).unwrap();
    let compatibility: serde_json::Value = serde_json::from_str(COMPATIBILITY_JSON).unwrap();
    assert!(compatibility.is_array());
    assert!(version_supported("0.1.0"));
    assert!(version_supported("1.1.75-hotfix.1"));
    assert!(!version_supported("2.0.0"));
    assert!(!version_supported("beta"));
}
