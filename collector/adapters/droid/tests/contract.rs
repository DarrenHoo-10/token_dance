use std::sync::Arc;

use adapter_droid::{
    decode_log, load_manifest, version_supported, DroidAdapter, ADAPTER_ID, COMPATIBILITY_JSON,
    KNOWN_LOG, LOG_SOURCE_ID,
};
use adapter_host::AdapterHost;
use adapter_sdk::{
    AgentAdapter, Capability, ErrorCode, EventPayload, ProbeContext, RawFrame, SourceContext,
    SourceKind,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"droid-contract-device-hmac";

fn frame(cursor: &str, payload: &str) -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::JsonlTail,
        source_id: LOG_SOURCE_ID.into(),
        cursor: cursor.into(),
        payload: payload.as_bytes().to_vec(),
    }
}

fn decode(frame: RawFrame) -> Result<Vec<adapter_sdk::NormalizedEvent>, adapter_sdk::AdapterError> {
    let adapter = DroidAdapter::for_version("0.234.0", KEY);
    decode_log(adapter.manifest(), Some("0.234.0"), KEY, &frame)
}

#[tokio::test]
async fn known_log_decodes_usage_without_leaking_content() {
    let adapter = Arc::new(DroidAdapter::for_version("0.234.0", KEY));
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
    assert!(report.capability.available().contains(&Capability::Tokens));

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
    assert_eq!(sources[0].id(), LOG_SOURCE_ID);

    let events = decode(frame("unix:111:222:1:0", KNOWN_LOG)).unwrap();
    assert_eq!(events.len(), 4);
    let session_hashes: Vec<_> = events.iter().map(|e| e.session_hash.clone()).collect();
    assert!(session_hashes.iter().all(Option::is_some));

    if let EventPayload::ModelUsageRecorded(usage) = &events[0].payload {
        assert_eq!(usage.provider_id, "droid");
        assert_eq!(usage.model_id, "gemini-3.8-flash");
        // input 12686 + output 555 + reasoning 109; no cached tokens.
        assert_eq!(usage.tokens.input_tokens.as_deref(), Some("12686"));
        assert_eq!(usage.tokens.output_tokens.as_deref(), Some("555"));
        assert_eq!(usage.tokens.reasoning_tokens.as_deref(), Some("109"));
        assert_eq!(usage.tokens.cache_read_tokens.as_deref(), Some("0"));
        assert_eq!(usage.tokens.total_tokens.as_deref(), Some("13350"));
    } else {
        panic!("expected model usage payload");
    }
    // input 13695 + cache read 11445 + output 263 + reasoning 57.
    if let EventPayload::ModelUsageRecorded(usage) = &events[1].payload {
        assert_eq!(usage.tokens.cache_read_tokens.as_deref(), Some("11445"));
        assert_eq!(usage.tokens.total_tokens.as_deref(), Some("25460"));
    } else {
        panic!("expected model usage payload");
    }
    for event in events {
        PrivacyFilter.filter(event).unwrap();
    }
}

#[tokio::test]
async fn zero_usage_and_malformed_lines_are_dropped() {
    let events = decode(frame("unix:111:222:1:0", KNOWN_LOG)).unwrap();
    assert_eq!(events.len(), 4);
    let json = serde_json::to_string(&events).unwrap();
    assert!(!json.contains("not-json"));
    assert!(!json.contains("DR_RESP_CANARY_ABORTED"));
}

#[tokio::test]
async fn raw_session_ids_never_leave_the_adapter() {
    let events = decode(frame("unix:111:222:1:0", KNOWN_LOG)).unwrap();
    let json = serde_json::to_string(&events).unwrap();
    for forbidden in ["sess-a", "sess-b"] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
    for event in events {
        PrivacyFilter.filter(event).unwrap();
    }
}

#[tokio::test]
async fn turn_identity_is_stable_per_response_and_distinct_across_responses() {
    let adapter = DroidAdapter::for_version("0.234.0", KEY);
    let line = KNOWN_LOG
        .lines()
        .find(|line| line.contains("DR_RESP_CANARY_1"))
        .unwrap();
    let first = adapter
        .decode(frame("unix:111:222:1:0", line))
        .await
        .unwrap()
        .remove(0);
    let replayed = adapter
        .decode(frame("unix:111:222:1:4096", line))
        .await
        .unwrap()
        .remove(0);
    assert_eq!(first.turn_hash, replayed.turn_hash);
    let other = adapter
        .decode(frame(
            "unix:111:222:1:0",
            KNOWN_LOG
                .lines()
                .find(|line| line.contains("DR_RESP_CANARY_3"))
                .unwrap(),
        ))
        .await
        .unwrap()
        .remove(0);
    assert_ne!(first.turn_hash, other.turn_hash);
}

#[tokio::test]
async fn undetected_adapter_reports_no_capabilities() {
    let adapter = DroidAdapter::undetected(KEY);
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
async fn unknown_version_degrades_but_verified_log_still_decodes() {
    let adapter = DroidAdapter::for_version("9.9.9", KEY);
    let report = adapter
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report.capability.missing().contains(&Capability::Sessions));
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
    let events = decode(frame("unix:111:222:1:0", KNOWN_LOG)).unwrap();
    assert_eq!(events.len(), 4);
}

#[tokio::test]
async fn source_mismatch_is_rejected_explicitly() {
    let adapter = DroidAdapter::for_version("0.234.0", KEY);
    let wrong_source = RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::JsonlTail,
        source_id: "wrong-source".into(),
        cursor: "unix:111:222:1:0".into(),
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
    assert!(version_supported("0.234.0"));
    assert!(!version_supported("1.0.0"));
    assert!(!version_supported("beta"));
}
