use std::sync::Arc;

use adapter_codex::{
    load_manifest, CodexAdapter, COMPATIBILITY_JSON, OTEL_JSON, SESSION_JSONL, SOURCE_JSONL,
    SOURCE_OTEL,
};
use adapter_host::AdapterHost;
use adapter_sdk::{
    AgentAdapter, Capability, ErrorCode, EventPayload, ProbeContext, RawFrame, SourceKind,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"codex-contract-device-hmac";

fn otel_frame() -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::Otlp,
        source_id: SOURCE_OTEL.into(),
        cursor: "otel:1".into(),
        payload: OTEL_JSON.as_bytes().to_vec(),
    }
}

#[tokio::test]
async fn contract_probe_sources_setup_and_decode() {
    let adapter = Arc::new(CodexAdapter::new("0.130.0", "interactive", KEY));
    let mut host = AdapterHost::new();
    host.register(adapter).unwrap();
    let report = host
        .probe(
            adapter_codex::ADAPTER_ID,
            ProbeContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert!(report.detected);
    assert!(report.capability.available().contains(&Capability::Skills));

    let sources = host
        .discover_sources(
            adapter_codex::ADAPTER_ID,
            adapter_sdk::SourceContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(sources.len(), 3);
    let plan = host
        .setup_plan(
            adapter_codex::ADAPTER_ID,
            adapter_sdk::SetupContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    let plan_json = serde_json::to_string(&plan).unwrap();
    assert!(plan_json.contains("log_user_prompt"));
    assert!(plan_json.contains("false"));

    let session = host
        .decode(
            adapter_codex::ADAPTER_ID,
            RawFrame::jsonl(INSTALL, SOURCE_JSONL, "0", SESSION_JSONL.as_bytes()),
        )
        .await
        .unwrap();
    assert_eq!(
        session.len(),
        5,
        "skill.injected must not count as invocation"
    );
    assert!(matches!(
        session[2].payload,
        EventPayload::ModelUsageRecorded(_)
    ));
    assert!(matches!(
        &session[4].payload,
        EventPayload::SkillInvoked(payload) if !payload.success
    ));
    assert!(session
        .into_iter()
        .all(|event| PrivacyFilter.filter(event).is_ok()));

    let otel = host
        .decode(adapter_codex::ADAPTER_ID, otel_frame())
        .await
        .unwrap();
    assert_eq!(otel.len(), 2);
}

#[tokio::test]
async fn old_version_and_source_mismatch_fail_closed() {
    let adapter = Arc::new(CodexAdapter::new("0.110.0", "exec", KEY));
    let mut host = AdapterHost::new();
    host.register(adapter).unwrap();
    let report = host
        .probe(
            adapter_codex::ADAPTER_ID,
            ProbeContext {
                installation_id: INSTALL.into(),
            },
        )
        .await
        .unwrap();
    assert!(report.capability.missing().contains(&Capability::Skills));
    let error = host
        .decode(
            adapter_codex::ADAPTER_ID,
            RawFrame::jsonl(INSTALL, "wrong-source", "0", SESSION_JSONL.as_bytes()),
        )
        .await
        .unwrap_err();
    assert_eq!(error.code, ErrorCode::DecodeFailed);
}

#[tokio::test]
async fn future_version_is_degraded_and_does_not_enable_unverified_otel() {
    let adapter = CodexAdapter::new("0.200.0", "app-server", KEY);
    let report = adapter.probe(ProbeContext::default()).await.unwrap();
    assert!(report.capability.missing().contains(&Capability::Skills));
    assert!(!report.needs_setup);
    assert_eq!(
        adapter
            .discover_sources(adapter_sdk::SourceContext::default())
            .await
            .unwrap()
            .len(),
        2
    );
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
}

#[tokio::test]
async fn skill_lifecycle_emits_only_terminal_records_with_success() {
    let adapter = CodexAdapter::new("0.130.0", "interactive", KEY);
    let payload = concat!(
        "{\"type\":\"skill.invoked\",\"timestamp\":\"2026-08-30T00:00:00Z\",\"invocationId\":\"inv-1\",\"skill\":\"review\"}\n",
        "{\"type\":\"skill.execution.completed\",\"timestamp\":\"2026-08-30T00:00:01Z\",\"invocationId\":\"inv-1\",\"skill_name\":\"review\"}\n",
        "{\"type\":\"skill.execution.failed\",\"timestamp\":\"2026-08-30T00:00:02Z\",\"invocationId\":\"inv-2\",\"skill_name\":\"review\",\"success\":false}\n"
    );
    let events = adapter
        .decode(RawFrame::jsonl(
            INSTALL,
            SOURCE_JSONL,
            "skill",
            payload.as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
    assert!(matches!(
        &events[0].payload,
        EventPayload::SkillInvoked(payload) if !payload.success
    ));
}

#[tokio::test]
async fn terminal_skill_identity_uses_invocation_id_across_sources() {
    let adapter = CodexAdapter::new("0.130.0", "interactive", KEY);
    let jsonl = r#"{"type":"skill.execution.completed","timestamp":"2026-08-30T00:00:01Z","invocationId":"shared-invocation","skill_name":"review","success":true}"#;
    let otel = r#"{"signal":"logs","name":"codex.skill.execution.completed","timestamp":"2026-08-30T00:00:01Z","attributes":{"invocation.id":"shared-invocation","skill_name":"review","success":true}}"#;

    let jsonl_event = adapter
        .decode(RawFrame::jsonl(
            INSTALL,
            SOURCE_JSONL,
            "jsonl-cursor",
            jsonl.as_bytes(),
        ))
        .await
        .unwrap()
        .remove(0);
    let otel_event = adapter
        .decode(RawFrame {
            installation_id: INSTALL.into(),
            source_kind: SourceKind::Otlp,
            source_id: SOURCE_OTEL.into(),
            cursor: "otel-cursor".into(),
            payload: otel.as_bytes().to_vec(),
        })
        .await
        .unwrap()
        .remove(0);

    assert_eq!(jsonl_event.event_id, otel_event.event_id);
}

#[tokio::test]
async fn injected_device_key_controls_event_identity_and_canaries_do_not_escape() {
    let left = CodexAdapter::new("0.130.0", "interactive", b"left-device-key");
    let right = CodexAdapter::new("0.130.0", "interactive", b"right-device-key");
    let frame = RawFrame::jsonl(INSTALL, SOURCE_JSONL, "0", SESSION_JSONL.as_bytes());
    let left_events = left.decode(frame.clone()).await.unwrap();
    let right_events = right.decode(frame).await.unwrap();
    assert_ne!(left_events[0].event_id, right_events[0].event_id);
    let json = serde_json::to_string(&left_events).unwrap();
    for forbidden in [
        "TOKENDANCE_CODEX_PROMPT_CANARY",
        "TOKENDANCE_CODEX_RESPONSE_CANARY",
        "TOKENDANCE_CODEX_TOOL_CANARY",
        "thread-secret-id",
        "turn-secret-id",
        "call-secret-id",
    ] {
        assert!(!json.contains(forbidden), "leaked {forbidden}");
    }
}

#[tokio::test]
async fn generated_large_sample_keeps_all_tool_calls_distinct() {
    let mut payload = String::new();
    for index in 0..10_000 {
        payload.push_str(&format!(
            "{{\"type\":\"tool.completed\",\"timestamp\":\"2026-08-30T00:00:00Z\",\"thread_id\":\"s\",\"turn_id\":\"t\",\"tool_call_id\":\"call-{index}\",\"tool\":\"shell\",\"success\":true}}\n"
        ));
    }
    let events = CodexAdapter::new("0.130.0", "exec", KEY)
        .decode(RawFrame::jsonl(
            INSTALL,
            SOURCE_JSONL,
            "large",
            payload.as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 10_000);
    let ids = events
        .iter()
        .map(|event| event.tool_call_hash.as_deref().unwrap())
        .collect::<std::collections::HashSet<_>>();
    assert_eq!(ids.len(), 10_000);
}

#[test]
fn manifest_compatibility_and_golden_summary_are_stable() {
    adapter_sdk::validate_manifest(&load_manifest()).unwrap();
    let compatibility: serde_json::Value = serde_json::from_str(COMPATIBILITY_JSON).unwrap();
    assert!(compatibility.is_object() || compatibility.is_array());
    let golden: serde_json::Value =
        serde_json::from_str(include_str!("../fixtures/golden/session.summary.json")).unwrap();
    assert_eq!(golden["count"], 5);
}
