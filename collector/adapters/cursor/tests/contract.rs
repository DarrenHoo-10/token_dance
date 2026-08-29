use std::sync::Arc;

use adapter_cursor::{
    load_manifest, CursorAdapter, CursorMode, SecretRef, COMPATIBILITY_JSON, ENTERPRISE_JSON,
    PERSONAL_JSON,
};
use adapter_host::AdapterHost;
use adapter_sdk::{
    AgentAdapter, Capability, ErrorCode, EventPayload, ProbeContext, RawFrame, SourceKind,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"cursor-contract-device-hmac";

fn frame(kind: SourceKind, source_id: &str, payload: &str) -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: kind,
        source_id: source_id.into(),
        cursor: "cursor:1".into(),
        payload: payload.as_bytes().to_vec(),
    }
}

#[tokio::test]
async fn enterprise_mode_uses_secret_ref_and_emits_token_cost_and_code() {
    let secret = SecretRef::new("secret://cursor/admin-api").unwrap();
    assert_eq!(format!("{secret:?}"), "SecretRef([REDACTED])");
    let adapter = Arc::new(CursorAdapter::new(
        CursorMode::EnterpriseApi,
        "0.45.2",
        Some(secret),
        KEY,
    ));
    let mut host = AdapterHost::new();
    host.register(adapter).unwrap();
    let report = host
        .probe(
            adapter_cursor::ADAPTER_ID,
            ProbeContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert!(!report.needs_permission);
    assert!(report.capability.available().contains(&Capability::Code));
    let sources = host
        .discover_sources(
            adapter_cursor::ADAPTER_ID,
            adapter_sdk::SourceContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(sources.len(), 2);
    let plan = host
        .setup_plan(
            adapter_cursor::ADAPTER_ID,
            adapter_sdk::SetupContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert!(plan.mutations.is_empty());
    assert_eq!(plan.required_permissions[0].code, "CURSOR_API_SECRET_REF");

    let events = host
        .decode(
            adapter_cursor::ADAPTER_ID,
            frame(
                SourceKind::RemoteApi,
                "cursor-analytics-api",
                ENTERPRISE_JSON,
            ),
        )
        .await
        .unwrap();
    assert_eq!(events.len(), 3);
    assert!(matches!(
        events[0].payload,
        EventPayload::ModelUsageRecorded(_)
    ));
    assert!(matches!(events[1].payload, EventPayload::CostRecorded(_)));
    assert!(matches!(events[2].payload, EventPayload::CodeChanged(_)));
    assert!(events
        .into_iter()
        .all(|event| PrivacyFilter.filter(event).is_ok()));
}

#[tokio::test]
async fn personal_and_team_modes_report_real_capability_limits() {
    let personal = CursorAdapter::personal("0.45.2", KEY);
    let report = personal
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report
        .capability
        .available()
        .contains(&Capability::Sessions));
    assert!(report.capability.missing().contains(&Capability::Tokens));
    let events = personal
        .decode(frame(
            SourceKind::SqliteSnapshot,
            "cursor-personal-local",
            PERSONAL_JSON,
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].accuracy, adapter_sdk::Accuracy::Derived);

    let team = CursorAdapter::new(
        CursorMode::TeamAdminApi,
        "0.45.2",
        Some(SecretRef::new("secret://cursor/team").unwrap()),
        KEY,
    );
    let report = team
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report.capability.missing().contains(&Capability::Code));
}

#[tokio::test]
async fn personal_conversation_identity_is_fingerprinted_and_stable() {
    let adapter = CursorAdapter::personal("0.45.2", KEY);
    let first = r#"{"events":[{"type":"conversation","timestamp":"2026-08-30T12:30:00Z","conversationId":"same-private-conversation","model":"m1"}]}"#;
    let second = r#"{"events":[{"eventId":"changed-platform-event","type":"conversation","timestamp":"2026-08-31T12:30:00Z","conversationId":"same-private-conversation","model":"m2"}]}"#;
    let mut first_frame = frame(SourceKind::SqliteSnapshot, "cursor-personal-local", first);
    first_frame.cursor = "sqlite:1".into();
    let mut second_frame = frame(SourceKind::SqliteSnapshot, "cursor-personal-local", second);
    second_frame.cursor = "sqlite:99".into();

    let first_event = adapter.decode(first_frame).await.unwrap().remove(0);
    let second_event = adapter.decode(second_frame).await.unwrap().remove(0);

    assert_eq!(first_event.event_id, second_event.event_id);
    assert!(!first_event.event_id.contains("same-private-conversation"));
}

#[tokio::test]
async fn missing_secret_unverified_schema_and_source_mismatch_fail_closed() {
    let enterprise = CursorAdapter::new(CursorMode::EnterpriseApi, "0.45.2", None, KEY);
    let report = enterprise
        .probe(ProbeContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(report.needs_permission);
    assert!(enterprise
        .decode(frame(
            SourceKind::RemoteApi,
            "cursor-admin-api",
            ENTERPRISE_JSON
        ))
        .await
        .is_err());

    let personal = CursorAdapter::personal("0.45.2", KEY).with_local_schema_verified(false);
    assert!(personal
        .discover_sources(adapter_sdk::SourceContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap()
        .is_empty());
    let error = CursorAdapter::personal("0.45.2", KEY)
        .decode(frame(SourceKind::RemoteApi, "wrong-source", PERSONAL_JSON))
        .await
        .unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
}

#[tokio::test]
async fn device_key_changes_ids_and_private_fields_never_escape() {
    let left = CursorAdapter::new(
        CursorMode::EnterpriseApi,
        "0.45.2",
        Some(SecretRef::new("secret://cursor/left").unwrap()),
        b"left-key",
    );
    let right = CursorAdapter::new(
        CursorMode::EnterpriseApi,
        "0.45.2",
        Some(SecretRef::new("secret://cursor/right").unwrap()),
        b"right-key",
    );
    let source = frame(
        SourceKind::RemoteApi,
        "cursor-analytics-api",
        ENTERPRISE_JSON,
    );
    let left_events = left.decode(source.clone()).await.unwrap();
    let right_events = right.decode(source).await.unwrap();
    assert_ne!(left_events[0].event_id, right_events[0].event_id);
    let json = serde_json::to_string(&left_events).unwrap();
    for forbidden in [
        "CURSOR_API_KEY_CANARY",
        "CURSOR_PROMPT_CANARY",
        "CURSOR_CODE_CANARY",
        "cursor-conversation-secret",
        "cursor-request-secret",
        "C:/private/project/secret.rs",
    ] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
}

#[tokio::test]
async fn overlapping_out_of_order_pages_deduplicate_by_platform_event_identity() {
    let adapter = CursorAdapter::new(
        CursorMode::EnterpriseApi,
        "0.45.2",
        Some(SecretRef::new("secret://cursor/dedup").unwrap()),
        KEY,
    );
    let first = r#"{"events":[{"eventId":"event-a","type":"accepted_code","timestamp":"2026-08-30T12:00:01Z","addedLines":1,"removedLines":0,"fileCount":1},{"requestId":"request-b","type":"usage","timestamp":"2026-08-30T12:00:02Z","provider":"anthropic","model":"m","inputTokens":1}]}"#;
    let second = r#"{"events":[{"requestId":"request-b","type":"usage","timestamp":"2026-08-30T12:00:02Z","provider":"anthropic","model":"m","inputTokens":1},{"eventId":"event-a","type":"accepted_code","timestamp":"2026-08-30T12:00:01Z","addedLines":1,"removedLines":0,"fileCount":1}]}"#;
    let mut first_frame = frame(SourceKind::RemoteApi, "cursor-analytics-api", first);
    first_frame.cursor = "page:10".into();
    let mut second_frame = frame(SourceKind::RemoteApi, "cursor-analytics-api", second);
    second_frame.cursor = "page:9".into();

    let first_ids = adapter
        .decode(first_frame)
        .await
        .unwrap()
        .into_iter()
        .map(|event| event.event_id)
        .collect::<std::collections::HashSet<_>>();
    let second_ids = adapter
        .decode(second_frame)
        .await
        .unwrap()
        .into_iter()
        .map(|event| event.event_id)
        .collect::<std::collections::HashSet<_>>();

    assert_eq!(first_ids, second_ids);
    assert_eq!(first_ids.len(), 2);
}

#[test]
fn manifest_compatibility_and_golden_summary_are_stable() {
    adapter_sdk::validate_manifest(&load_manifest()).unwrap();
    let compatibility: serde_json::Value = serde_json::from_str(COMPATIBILITY_JSON).unwrap();
    assert!(compatibility.is_object() || compatibility.is_array());
    let golden: serde_json::Value =
        serde_json::from_str(include_str!("../fixtures/golden/enterprise.summary.json")).unwrap();
    assert_eq!(golden["count"], 3);
}
