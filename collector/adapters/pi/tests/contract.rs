use std::sync::Arc;

use adapter_host::AdapterHost;
use adapter_pi::{
    decode_jsonl, load_manifest, version_supported, PiAdapter, ADAPTER_ID, COMPATIBILITY_JSON,
    ERRORS_JSON, HISTORY_SOURCE_ID, KNOWN_JSON,
};
use adapter_sdk::{
    AgentAdapter, Capability, ErrorCode, EventPayload, ProbeContext, RawFrame, SourceContext,
    SourceKind,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"pi-contract-device-hmac";

fn frame(cursor: &str, payload: &str) -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::JsonlTail,
        source_id: HISTORY_SOURCE_ID.into(),
        cursor: cursor.into(),
        payload: payload.as_bytes().to_vec(),
    }
}

#[tokio::test]
async fn known_session_decodes_metrics_without_leaking_content() {
    let adapter = Arc::new(PiAdapter::for_version("0.3.0", KEY));
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
    assert_eq!(sources[0].id(), HISTORY_SOURCE_ID);

    let events = decode(frame("unix:111:222:1:0", KNOWN_JSON)).unwrap();
    assert_eq!(events.len(), 6);
    let kinds: Vec<&str> = events
        .iter()
        .map(|event| match event.payload {
            EventPayload::SessionStarted(_) => "session_started",
            EventPayload::ModelUsageRecorded(_) => "model_usage_recorded",
            EventPayload::TurnCompleted(_) => "turn_completed",
            EventPayload::ToolInvoked(_) => "tool_invoked",
            _ => "other",
        })
        .collect();
    assert_eq!(
        kinds,
        [
            "session_started",
            "model_usage_recorded",
            "turn_completed",
            "tool_invoked",
            "model_usage_recorded",
            "turn_completed"
        ]
    );
    let session_hashes: Vec<_> = events
        .iter()
        .map(|event| event.session_hash.clone())
        .collect();
    let first = session_hashes[0].clone().unwrap();
    assert!(session_hashes
        .iter()
        .all(|hash| hash.as_deref() == Some(first.as_str())));

    if let EventPayload::ModelUsageRecorded(usage) = &events[1].payload {
        assert_eq!(usage.provider_id, "anthropic");
        assert_eq!(usage.model_id, "claude-sonnet-4-5");
        assert_eq!(usage.tokens.input_tokens.as_deref(), Some("912"));
        assert_eq!(usage.tokens.output_tokens.as_deref(), Some("254"));
        assert_eq!(usage.tokens.cache_read_tokens.as_deref(), Some("4096"));
        assert_eq!(usage.tokens.cache_write_tokens.as_deref(), Some("128"));
        assert_eq!(usage.tokens.total_tokens.as_deref(), Some("5390"));
    } else {
        panic!("expected model usage payload");
    }
    assert!(matches!(events[2].accuracy, adapter_sdk::Accuracy::Derived));
    if let EventPayload::ToolInvoked(tool) = &events[3].payload {
        assert_eq!(tool.tool_category, "read_file");
        assert!(tool.success);
    } else {
        panic!("expected tool payload");
    }

    for event in events {
        PrivacyFilter.filter(event).unwrap();
    }
}

#[tokio::test]
async fn decoded_events_never_leak_prompt_tool_or_workspace_content() {
    let events = decode(frame("unix:111:222:1:0", KNOWN_JSON)).unwrap();
    let json = serde_json::to_string(&events).unwrap();
    for forbidden in [
        "PI_PROMPT_CANARY",
        "PI_RESPONSE_CANARY",
        "PI_THINKING_CANARY",
        "PI_ARGS_CANARY",
        "PI_TOOL_OUTPUT_CANARY",
        "PI_CUSTOM_CANARY",
        "PI_COMPACTION_CANARY",
        "/Users/dev/finance-app",
        "8f2f1c3a",
    ] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
}

#[tokio::test]
async fn failed_turns_and_tool_errors_stay_explicit() {
    let events = decode(frame("unix:333:444:1:0", ERRORS_JSON)).unwrap();
    assert_eq!(events.len(), 7);
    let turns: Vec<(bool, Option<&str>)> = events
        .iter()
        .filter_map(|event| match &event.payload {
            EventPayload::TurnCompleted(turn) => Some((turn.success, turn.error_class.as_deref())),
            _ => None,
        })
        .collect();
    assert_eq!(
        turns,
        [
            (true, None),
            (false, Some("error")),
            (false, Some("aborted"))
        ]
    );
    let failed_tool = events
        .iter()
        .find(|event| matches!(event.payload, EventPayload::ToolInvoked(_)))
        .unwrap();
    if let EventPayload::ToolInvoked(tool) = &failed_tool.payload {
        assert!(!tool.success);
        assert_eq!(tool.tool_category, "bash");
    }
    let json = serde_json::to_string(&events).unwrap();
    for forbidden in [
        "PI_NO_USAGE_CANARY",
        "PI_ERROR_DETAIL_CANARY",
        "PI_FAILED_TOOL_OUTPUT_CANARY",
    ] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
    for event in events {
        PrivacyFilter.filter(event).unwrap();
    }
}

#[tokio::test]
async fn session_identity_is_stable_per_file_and_distinct_across_files() {
    let adapter = PiAdapter::for_version("0.3.0", KEY);
    let first_line = KNOWN_JSON.lines().next().unwrap();
    let early = adapter
        .decode(frame("unix:111:222:1:0", first_line))
        .await
        .unwrap()
        .remove(0);
    let late = adapter
        .decode(frame("unix:111:222:1:4096", first_line))
        .await
        .unwrap()
        .remove(0);
    assert_eq!(early.session_hash, late.session_hash);

    let other_file = adapter
        .decode(frame("unix:555:666:1:0", first_line))
        .await
        .unwrap()
        .remove(0);
    assert_ne!(early.session_hash, other_file.session_hash);

    let legacy = adapter
        .decode(frame("pi:1", first_line))
        .await
        .unwrap()
        .remove(0);
    assert!(legacy.session_hash.is_some());
}

#[tokio::test]
async fn undetected_adapter_reports_no_capabilities() {
    let adapter = PiAdapter::undetected(KEY);
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
async fn unknown_version_degrades_but_verified_history_still_decodes() {
    let adapter = PiAdapter::for_version("9.9.9", KEY);
    let report = adapter
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report.capability.missing().contains(&Capability::Tools));
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
    let events = decode(frame("unix:111:222:1:0", KNOWN_JSON)).unwrap();
    assert_eq!(events.len(), 6);
}

#[tokio::test]
async fn source_mismatch_is_rejected_explicitly() {
    let adapter = PiAdapter::for_version("0.3.0", KEY);
    let wrong_source = RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::JsonlTail,
        source_id: "wrong-source".into(),
        cursor: "unix:111:222:1:0".into(),
        payload: KNOWN_JSON.as_bytes().to_vec(),
    };
    let error = adapter.decode(wrong_source).await.unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
    let error = decode_jsonl(
        adapter.manifest(),
        Some("0.3.0"),
        KEY,
        &frame("unix:111:222:1:0", "not json"),
    )
    .unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
}

#[test]
fn manifest_compatibility_and_golden_summaries_are_stable() {
    adapter_sdk::validate_manifest(&load_manifest()).unwrap();
    let compatibility: serde_json::Value = serde_json::from_str(COMPATIBILITY_JSON).unwrap();
    assert!(compatibility.is_array());
    assert!(version_supported("0.2.0"));
    assert!(version_supported("0.3.0"));
    assert!(version_supported("1.9.9"));
    assert!(!version_supported("2.0.0"));
    assert!(!version_supported("0.1.9"));
    assert!(!version_supported("beta"));

    let known: serde_json::Value =
        serde_json::from_str(include_str!("../fixtures/golden/known.summary.json")).unwrap();
    assert_eq!(known["count"], 6);
    let errors: serde_json::Value =
        serde_json::from_str(include_str!("../fixtures/golden/errors.summary.json")).unwrap();
    assert_eq!(errors["count"], 7);
}

fn decode(frame: RawFrame) -> Result<Vec<adapter_sdk::NormalizedEvent>, adapter_sdk::AdapterError> {
    let adapter = PiAdapter::for_version("0.3.0", KEY);
    decode_jsonl(adapter.manifest(), Some("0.3.0"), KEY, &frame)
}
