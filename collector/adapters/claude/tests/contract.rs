use std::sync::Arc;

use adapter_claude::{load_manifest, ClaudeAdapter, HISTORY_SOURCE_ID, OTLP_SOURCE_ID};
use adapter_host::AdapterHost;
use adapter_sdk::{
    validate_manifest, AgentAdapter, Capability, ConfigMutation, EventPayload, ProbeContext,
    RawFrame, SetupContext, SourceContext, SourceKind,
};

const HISTORY: &str = include_str!("../fixtures/contract/history.jsonl");
const HMAC_KEY: &[u8] = b"tokenshow-adapter-fixture-hmac-key-v1";
const OTLP: &str = include_str!("../fixtures/contract/otlp.jsonl");

fn otlp_frame(payload: &str) -> RawFrame {
    RawFrame {
        installation_id: "ins_00000000000000000000000000".into(),
        source_kind: SourceKind::Otlp,
        source_id: OTLP_SOURCE_ID.into(),
        cursor: "otlp:0".into(),
        payload: payload.as_bytes().to_vec(),
    }
}

#[test]
fn manifest_and_compatibility_matrix_are_valid() {
    let manifest = load_manifest();
    validate_manifest(&manifest).unwrap();
    let matrix: serde_json::Value =
        serde_json::from_str(adapter_claude::COMPATIBILITY_JSON).unwrap();
    assert!(matrix.as_array().is_some_and(|rows| rows.len() >= 3));
    assert!(adapter_claude::COMPATIBILITY_MARKDOWN.contains("degraded"));
}

#[tokio::test]
async fn contract_probe_sources_setup_and_decode() {
    let mut host = AdapterHost::new();
    host.register(Arc::new(ClaudeAdapter::new(HMAC_KEY)))
        .unwrap();
    let report = host
        .probe(
            adapter_claude::ADAPTER_ID,
            ProbeContext {
                installation_id: "ins_00000000000000000000000000".into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(report.capability.available(), load_manifest().capabilities);
    let sources = host
        .discover_sources(adapter_claude::ADAPTER_ID, SourceContext::default())
        .await
        .unwrap();
    assert_eq!(
        sources
            .iter()
            .map(|source| source.kind())
            .collect::<Vec<_>>(),
        vec![SourceKind::Otlp, SourceKind::JsonlTail]
    );
    let plan = host
        .setup_plan(adapter_claude::ADAPTER_ID, SetupContext::default())
        .await
        .unwrap();
    let ConfigMutation::JsonMergePatch { patch, .. } = &plan.mutations[0] else {
        panic!("expected JSON patch")
    };
    let serialized = patch.to_string();
    assert!(serialized.contains("OTEL_LOG_USER_PROMPTS\":\"0"));
    assert!(serialized.contains("OTEL_LOG_TOOL_CONTENT\":\"0"));
    let events = host
        .decode(adapter_claude::ADAPTER_ID, otlp_frame(OTLP))
        .await
        .unwrap();
    assert_eq!(events.len(), 3);
    assert!(events
        .iter()
        .all(|event| event.source.kind == SourceKind::Otlp));
    assert!(events
        .iter()
        .all(|event| event.adapter_id == load_manifest().id));
}

#[tokio::test]
async fn strict_decode_rejects_known_malformed_records_and_source_mismatch() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let malformed = r#"{"name":"claude_code.token.usage","timestamp":"2026-08-30T00:00:00Z","attributes":{"provider":"anthropic","model":"m","input_tokens":"not-a-number"}}"#;
    assert!(adapter.decode(otlp_frame(malformed)).await.is_err());
    let wrong = RawFrame::jsonl(
        "ins_00000000000000000000000000",
        OTLP_SOURCE_ID,
        "0",
        OTLP.as_bytes(),
    );
    assert!(adapter.decode(wrong).await.is_err());
}

#[tokio::test]
async fn unknown_version_degrades_without_claiming_otlp_compatibility() {
    let adapter = ClaudeAdapter::for_version("2.0.0", HMAC_KEY);
    let report = adapter.probe(ProbeContext::default()).await.unwrap();
    assert!(report.detected);
    assert_eq!(
        report.capability.available(),
        vec![Capability::Tokens, Capability::Sessions, Capability::Turns]
    );
    assert!(!report.needs_setup);
    let sources = adapter
        .discover_sources(SourceContext::default())
        .await
        .unwrap();
    assert_eq!(sources.len(), 1);
    assert_eq!(sources[0].kind(), SourceKind::JsonlTail);
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
}

#[tokio::test]
async fn skill_load_is_not_invocation_and_failed_execution_is_preserved() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let payload = concat!(
        "{\"name\":\"claude_code.skill.loaded\",\"timestamp\":\"2026-08-30T00:00:00Z\",\"attributes\":{\"session.id\":\"s\",\"skill.name\":\"review\"}}\n",
        "{\"name\":\"claude_code.skill.execution.failed\",\"timestamp\":\"2026-08-30T00:00:01Z\",\"attributes\":{\"session.id\":\"s\",\"skill.name\":\"review\",\"skill.invocation.id\":\"skill-failed-1\",\"success\":false}}\n"
    );
    let events = adapter.decode(otlp_frame(payload)).await.unwrap();
    assert_eq!(events.len(), 1);
    assert!(matches!(
        &events[0].payload,
        EventPayload::SkillInvoked(payload) if !payload.success
    ));
}

#[tokio::test]
async fn multiple_official_tool_events_keep_call_identity() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let payload = (0..8)
        .map(|index| format!(
            "{{\"name\":\"claude_code.tool.completed\",\"timestamp\":\"2026-08-30T00:00:00Z\",\"attributes\":{{\"session.id\":\"s\",\"tool.call.id\":\"call-{index}\",\"tool.name\":\"editor\",\"success\":true}}}}"
        ))
        .collect::<Vec<_>>()
        .join("\n");
    let events = adapter.decode(otlp_frame(&payload)).await.unwrap();
    assert_eq!(events.len(), 8);
    assert!(events.iter().all(|event| event.tool_call_hash.is_some()));
}

#[tokio::test]
async fn history_fallback_ignores_unknown_event_types() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let events = adapter
        .decode(RawFrame::jsonl(
            "ins_00000000000000000000000000",
            HISTORY_SOURCE_ID,
            "0",
            HISTORY.as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 3);
}

#[tokio::test]
async fn lifecycle_starts_are_ignored_and_duplicate_terminals_collapse_by_invocation_id() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let payload = concat!(
        r#"{"name":"claude_code.tool.started","timestamp":"2026-08-30T00:00:00Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell"}}
"#,
        r#"{"name":"claude_code.tool.result","timestamp":"2026-08-30T00:00:01Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell","success":true}}
"#,
        r#"{"name":"claude_code.tool.completed","timestamp":"2026-08-30T00:00:02Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell","success":true}}
"#,
        r#"{"name":"claude_code.skill.invoked","timestamp":"2026-08-30T00:00:03Z","attributes":{"session.id":"s","skill.invocation.id":"skill-1","skill.name":"review"}}
"#,
        r#"{"name":"claude_code.skill.execution.completed","timestamp":"2026-08-30T00:00:04Z","attributes":{"session.id":"s","skill.invocation.id":"skill-1","skill.name":"review","success":true}}
"#,
        r#"{"name":"claude_code.skill.execution.completed","timestamp":"2026-08-30T00:00:05Z","attributes":{"session.id":"s","skill.invocation.id":"skill-1","skill.name":"review","success":true}}"#,
    );
    let events = adapter.decode(otlp_frame(payload)).await.unwrap();
    assert_eq!(events.len(), 2);
    assert!(events.iter().all(|event| event.tool_call_hash.is_some()));
    assert_ne!(events[0].tool_call_hash, events[1].tool_call_hash);
}

#[tokio::test]
async fn terminal_without_success_is_rejected_instead_of_defaulting_true() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let payload = r#"{"name":"claude_code.tool.completed","timestamp":"2026-08-30T00:00:00Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell"}}"#;
    assert!(adapter.decode(otlp_frame(payload)).await.is_err());
}

#[tokio::test]
async fn decoder_rejects_future_schema_and_does_not_accept_internal_standard_names() {
    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let future = r#"{"name":"claude_code.tool.completed","schemaVersion":"2.0","timestamp":"2026-08-30T00:00:00Z","attributes":{"tool.call.id":"tool-1","tool.name":"shell","success":true}}"#;
    assert!(adapter.decode(otlp_frame(future)).await.is_err());
    let internal = r#"{"type":"tool_invoked","timestamp":"2026-08-30T00:00:00Z","toolCallId":"tool-1","toolCategory":"shell","success":true}"#;
    assert!(adapter
        .decode(otlp_frame(internal))
        .await
        .unwrap()
        .is_empty());
}

#[tokio::test]
async fn explicit_version_bounds_and_cross_source_semantic_ids_are_stable() {
    for unsupported in ["0.999.999", "2.0.0", "1.0", "1.0.0.1"] {
        let report = ClaudeAdapter::for_version(unsupported, HMAC_KEY)
            .probe(ProbeContext::default())
            .await
            .unwrap();
        assert!(!report.needs_setup, "unexpected support for {unsupported}");
    }

    let adapter = ClaudeAdapter::new(HMAC_KEY);
    let otlp = concat!(
        r#"{"name":"claude_code.tool.completed","timestamp":"2026-08-30T00:00:01Z","attributes":{"invocation.id":"inv-1","tool.name":"shell","success":true}}"#,
        "\n",
        r#"{"name":"claude_code.token.usage","timestamp":"2026-08-30T00:00:02Z","attributes":{"request.id":"req-1","provider":"anthropic","model":"m","input_tokens":1}}"#,
    );
    let history = concat!(
        r#"{"name":"claude_code.token.usage","timestamp":"2026-08-30T00:00:02Z","requestId":"req-1","provider":"anthropic","model":"m","input_tokens":1}"#,
        "\n",
        r#"{"name":"claude_code.tool.completed","timestamp":"2026-08-30T00:00:01Z","invocation_id":"inv-1","tool":"shell","success":true}"#,
    );
    let otlp_ids = adapter
        .decode(otlp_frame(otlp))
        .await
        .unwrap()
        .into_iter()
        .map(|event| event.event_id)
        .collect::<std::collections::HashSet<_>>();
    let history_ids = adapter
        .decode(RawFrame::jsonl(
            "ins_00000000000000000000000000",
            HISTORY_SOURCE_ID,
            "history:99",
            history.as_bytes(),
        ))
        .await
        .unwrap()
        .into_iter()
        .map(|event| event.event_id)
        .collect::<std::collections::HashSet<_>>();
    assert_eq!(otlp_ids, history_ids);
    assert_eq!(otlp_ids.len(), 2);
}
