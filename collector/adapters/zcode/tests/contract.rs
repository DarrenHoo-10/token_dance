use std::sync::Arc;

use adapter_host::AdapterHost;
use adapter_sdk::{
    AgentAdapter, Capability, ErrorCode, EventPayload, ProbeContext, RawFrame, SourceKind,
};
use adapter_zcode::{
    compatibility_supported, fingerprint_supported, load_manifest, verified_query_plan,
    ZCodeAdapter, COMPATIBILITY_JSON, FINGERPRINT_V1, FINGERPRINT_V2, FINGERPRINT_V3, KNOWN_JSON,
    RUNTIME_JSON, UNKNOWN_JSON,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"zcode-contract-device-hmac";

fn frame(kind: SourceKind, source_id: &str, payload: &str) -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: kind,
        source_id: source_id.into(),
        cursor: "zcode:1".into(),
        payload: payload.as_bytes().to_vec(),
    }
}

#[tokio::test]
async fn known_fingerprint_uses_only_verified_sources_and_decodes() {
    let adapter = Arc::new(ZCodeAdapter::new("1.2.0", FINGERPRINT_V2, KEY));
    let mut host = AdapterHost::new();
    host.register(adapter).unwrap();
    let report = host
        .probe(
            adapter_zcode::ADAPTER_ID,
            ProbeContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert!(report.capability.available().contains(&Capability::Tokens));
    let sources = host
        .discover_sources(
            adapter_zcode::ADAPTER_ID,
            adapter_sdk::SourceContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(sources.len(), 2);
    let events = host
        .decode(
            adapter_zcode::ADAPTER_ID,
            frame(SourceKind::SqliteSnapshot, "zcode-sqlite", KNOWN_JSON),
        )
        .await
        .unwrap();
    assert_eq!(events.len(), 3);
    assert!(matches!(events[0].payload, EventPayload::SessionStarted(_)));
    assert!(matches!(
        events[1].payload,
        EventPayload::ModelUsageRecorded(_)
    ));
    assert!(matches!(events[2].payload, EventPayload::SkillInvoked(_)));
    assert!(events
        .into_iter()
        .all(|event| PrivacyFilter.filter(event).is_ok()));
}

#[tokio::test]
async fn unknown_fingerprint_never_guesses_sql_and_degrades() {
    let adapter = ZCodeAdapter::new("9.9.9", "zcode-sqlite-v99-unknown", KEY);
    assert!(!fingerprint_supported("zcode-sqlite-v99-unknown"));
    assert!(verified_query_plan("zcode-sqlite-v99-unknown").is_none());
    let report = adapter
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report.capability.missing().contains(&Capability::Tokens));
    assert_eq!(
        adapter
            .discover_sources(adapter_sdk::SourceContext {
                installation_id: INSTALL.into(),
            })
            .await
            .unwrap()
            .len(),
        1,
        "only the verified runtime source may remain"
    );
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
    let events = adapter
        .decode(frame(
            SourceKind::SqliteSnapshot,
            "zcode-sqlite",
            UNKNOWN_JSON,
        ))
        .await
        .unwrap();
    assert!(events.is_empty());
}

#[tokio::test]
async fn version_and_fingerprint_must_match_the_verified_matrix() {
    assert!(compatibility_supported("1.0.0", FINGERPRINT_V1));
    assert!(compatibility_supported("1.1.9", FINGERPRINT_V1));
    assert!(compatibility_supported("1.2.0", FINGERPRINT_V2));
    assert!(!compatibility_supported("1.2.0", FINGERPRINT_V1));
    assert!(!compatibility_supported("1.1.9", FINGERPRINT_V2));
    assert!(!compatibility_supported("2.0.0", FINGERPRINT_V2));

    let mismatch = ZCodeAdapter::new("1.1.9", FINGERPRINT_V2, KEY);
    let sources = mismatch
        .discover_sources(adapter_sdk::SourceContext::default())
        .await
        .unwrap();
    assert!(sources.iter().all(|source| source.id() != "zcode-sqlite"));
}

#[tokio::test]
async fn v3_session_summaries_emit_derived_generated_lines() {
    let adapter = ZCodeAdapter::new("0.8.0", FINGERPRINT_V3, KEY);
    let payload = r#"{"fingerprint":"zcode-sqlite-v3-uv0","records":[{"type":"code_changed","timestamp":"2026-09-10T01:00:00Z","sessionId":"zcode-session-secret","addedLines":12,"removedLines":3,"fileCount":2,"summary_diffs":"ZCODE_DIFF_CANARY","path":"C:/secret.rs"}]}"#;
    let events = adapter
        .decode(frame(
            SourceKind::SqliteSnapshot,
            "zcode-sqlite",
            payload,
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].accuracy, adapter_sdk::Accuracy::Derived);
    let EventPayload::CodeChanged(code) = &events[0].payload else {
        panic!("expected code changed");
    };
    assert_eq!(code.generated_lines.as_deref(), Some("12"));
    assert_eq!(code.added_lines, "12");
    assert_eq!(code.removed_lines, "3");
    let json = serde_json::to_string(&events).unwrap();
    assert!(!json.contains("ZCODE_DIFF_CANARY"));
    assert!(!json.contains("secret.rs"));
}

#[tokio::test]
async fn turn_and_skill_without_success_are_ignored() {
    let adapter = ZCodeAdapter::new("1.2.0", FINGERPRINT_V2, KEY);
    let payload = r#"{"records":[{"type":"turn_finish","timestamp":"2026-08-30T13:00:00Z","sessionId":"s","stepId":"t"},{"type":"skill","timestamp":"2026-08-30T13:00:01Z","sessionId":"s","stepId":"t","skillName":"review"}]}"#;
    let events = adapter
        .decode(frame(
            SourceKind::RuntimeStream,
            "zcode-runtime-events",
            payload,
        ))
        .await
        .unwrap();
    assert!(events.is_empty());
}

#[tokio::test]
async fn runtime_fallback_and_source_mismatch_are_explicit() {
    let adapter = ZCodeAdapter::new("1.2.0", FINGERPRINT_V2, KEY);
    let events = adapter
        .decode(frame(
            SourceKind::RuntimeStream,
            "zcode-runtime-events",
            RUNTIME_JSON,
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
    assert!(matches!(events[0].payload, EventPayload::ToolInvoked(_)));
    let error = adapter
        .decode(frame(
            SourceKind::RuntimeStream,
            "wrong-runtime",
            RUNTIME_JSON,
        ))
        .await
        .unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
}

#[tokio::test]
async fn injected_key_changes_identity_and_sensitive_fields_do_not_escape() {
    let left = ZCodeAdapter::new("1.2.0", FINGERPRINT_V2, b"left-key");
    let right = ZCodeAdapter::new("1.2.0", FINGERPRINT_V2, b"right-key");
    let source = frame(SourceKind::SqliteSnapshot, "zcode-sqlite", KNOWN_JSON);
    let left_events = left.decode(source.clone()).await.unwrap();
    let right_events = right.decode(source).await.unwrap();
    assert_ne!(left_events[0].event_id, right_events[0].event_id);
    let json = serde_json::to_string(&left_events).unwrap();
    for forbidden in [
        "ZCODE_PROMPT_CANARY",
        "ZCODE_TOOL_CANARY",
        "ZCODE_SKILL_CANARY",
        "zcode-session-secret",
        "zcode-step-secret",
    ] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
}

#[tokio::test]
async fn sqlite_usage_identity_does_not_depend_on_poll_cursor() {
    let adapter = ZCodeAdapter::new("0.8.0", FINGERPRINT_V3, KEY);
    // Real model_usage rows carry a NUMERIC rowid as stepId; identity must
    // accept numbers or it falls back to the batch sequence and every
    // incremental poll after the first rescan collides with old fingerprints.
    let payload = r#"{"fingerprint":"zcode-sqlite-v3-uv0","records":[{"type":"step_finish","timestamp":"2026-09-10T01:00:00Z","sessionId":"zcode-session-secret","stepId":42,"provider":"zai","model":"glm","inputTokens":10,"outputTokens":2,"totalTokens":12}]}"#;
    let mut first = frame(SourceKind::SqliteSnapshot, "zcode-sqlite", payload);
    first.cursor = "0:10:1:1".into();
    let mut second = frame(SourceKind::SqliteSnapshot, "zcode-sqlite", payload);
    second.cursor = "0:999:9:9".into();
    let left = adapter.decode(first).await.unwrap();
    let right = adapter.decode(second).await.unwrap();
    assert_eq!(left.len(), 1);
    assert_eq!(left[0].event_id, right[0].event_id);
}

#[tokio::test]
async fn numeric_usage_rows_in_separate_polls_keep_distinct_identities() {
    let adapter = ZCodeAdapter::new("0.8.0", FINGERPRINT_V3, KEY);
    let record = |step_id: i64, tokens: i64| {
        format!(
            r#"{{"type":"step_finish","timestamp":"2026-09-11T01:00:0{tokens}.000Z","sessionId":"s","stepId":{step_id},"provider":"zai","model":"glm","inputTokens":{tokens},"outputTokens":0,"totalTokens":{tokens}}}"#
        )
    };
    // A big rescan places a row at batch position 1; a later incremental poll
    // places a DIFFERENT row at position 1 too. Their identities must differ.
    let rescan = format!(
        r#"{{"fingerprint":"zcode-sqlite-v3-uv0","records":[{},{}]}}"#,
        record(100, 10),
        record(101, 20),
    );
    let incremental = format!(
        r#"{{"fingerprint":"zcode-sqlite-v3-uv0","records":[{}]}}"#,
        record(102, 30),
    );
    let mut big = frame(SourceKind::SqliteSnapshot, "zcode-sqlite", &rescan);
    big.cursor = "0:1:1:1".into();
    let mut small = frame(SourceKind::SqliteSnapshot, "zcode-sqlite", &incremental);
    small.cursor = "0:1:99:99".into();
    let big_events = adapter.decode(big).await.unwrap();
    let small_events = adapter.decode(small).await.unwrap();
    assert_eq!(big_events.len(), 2);
    assert_eq!(small_events.len(), 1);
    let big_ids: Vec<_> = big_events.iter().map(|e| e.event_id.clone()).collect();
    assert!(!big_ids.contains(&small_events[0].event_id));
}

#[test]
fn manifest_compatibility_and_golden_summary_are_stable() {
    adapter_sdk::validate_manifest(&load_manifest()).unwrap();
    let compatibility: serde_json::Value = serde_json::from_str(COMPATIBILITY_JSON).unwrap();
    assert!(compatibility.is_object() || compatibility.is_array());
    let golden: serde_json::Value =
        serde_json::from_str(include_str!("../fixtures/golden/known.summary.json")).unwrap();
    assert_eq!(golden["count"], 3);
}
