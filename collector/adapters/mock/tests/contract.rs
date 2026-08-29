use std::sync::Arc;

use adapter_host::AdapterHost;
use adapter_mock::{
    load_manifest, MockAdapter, MIN_SESSION_JSONL, SOURCE_ID, UNKNOWN_FIELDS_JSONL,
};
use adapter_sdk::{
    validate_manifest, AgentAdapter, ErrorCode, ProbeContext, RawFrame, SourceContext, SourceKind,
};

#[test]
fn manifest_fixture_validates_against_sdk_rules() {
    let manifest = load_manifest();
    validate_manifest(&manifest).unwrap();
    assert_eq!(manifest.id, "dev.tokenshow.adapter.mock");
    assert_eq!(manifest.sources, vec![SourceKind::JsonlTail]);
}

#[tokio::test]
async fn host_contract_probe_sources_and_decode() {
    let mut host = AdapterHost::new();
    let adapter = Arc::new(MockAdapter::new());
    host.register(adapter).unwrap();

    let report = host
        .probe(
            "dev.tokenshow.adapter.mock",
            ProbeContext {
                installation_id: "ins_01test".into(),
            },
        )
        .await
        .unwrap();
    assert!(report.detected);
    assert!(!report.capability.available().is_empty());

    let sources = host
        .discover_sources(
            "dev.tokenshow.adapter.mock",
            SourceContext {
                installation_id: "ins_01test".into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(sources.len(), 1);
    assert_eq!(sources[0].kind(), SourceKind::JsonlTail);

    let events = host
        .decode(
            "dev.tokenshow.adapter.mock",
            RawFrame::jsonl("ins_01test", SOURCE_ID, "0", MIN_SESSION_JSONL.as_bytes()),
        )
        .await
        .unwrap();
    assert_eq!(events.len(), 5);
    assert!(matches!(
        events[0].payload,
        adapter_sdk::EventPayload::SessionStarted(_)
    ));
    match &events[2].payload {
        adapter_sdk::EventPayload::ModelUsageRecorded(payload) => {
            assert_eq!(payload.tokens.input_tokens.as_deref(), Some("10"));
            assert_eq!(payload.tokens.cache_read_tokens, None);
            assert_eq!(payload.tokens.total_tokens.as_deref(), Some("30"));
        }
        other => panic!("expected model usage, got {other:?}"),
    }
}

#[tokio::test]
async fn unknown_fields_and_types_are_ignored() {
    let adapter = MockAdapter::new();
    let events = adapter
        .decode(RawFrame::jsonl(
            "ins_01test",
            SOURCE_ID,
            "0",
            UNKNOWN_FIELDS_JSONL.as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 2);
    let json = serde_json::to_string(&events).unwrap();
    assert!(!json.contains("TOKSHOW_TEST_PROMPT_SECRET"));
    assert!(!json.contains("future_event_v2"));
    assert!(!json.contains("unknownField"));
}

#[test]
fn incompatible_protocol_is_rejected_before_probe() {
    let mut manifest = load_manifest();
    manifest.protocol_version = "9.0".into();
    let err = adapter_sdk::validate_manifest(&manifest).unwrap_err();
    assert_eq!(err.code, ErrorCode::ProtocolIncompatible);
}

#[tokio::test]
async fn decode_is_deterministic_for_the_same_frame() {
    let adapter = MockAdapter::new();
    let frame = RawFrame::jsonl("ins_01test", SOURCE_ID, "0", MIN_SESSION_JSONL.as_bytes());
    let left = adapter.decode(frame.clone()).await.unwrap();
    let right = adapter.decode(frame).await.unwrap();
    assert_eq!(left, right);
}
