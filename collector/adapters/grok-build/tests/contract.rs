use std::sync::Arc;

use adapter_grok_build::{
    load_manifest, GrokBuildAdapter, HISTORY_SOURCE_ID, HOOK_SOURCE_ID, OTLP_SOURCE_ID,
};
use adapter_host::AdapterHost;
use adapter_sdk::{
    validate_manifest, Accuracy, AgentAdapter, Capability, ConfigMutation, EventPayload,
    ProbeContext, RawFrame, SetupContext, SourceContext, SourceKind,
};

const HISTORY: &str = include_str!("../fixtures/contract/history.jsonl");
const SESSION_UPDATES: &str = include_str!("../fixtures/contract/session-updates.jsonl");
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
        serde_json::from_str(adapter_grok_build::COMPATIBILITY_JSON).unwrap();
    assert!(matrix.as_array().is_some_and(|rows| rows.len() >= 4));
    assert!(adapter_grok_build::COMPATIBILITY_MARKDOWN.contains("degraded"));
}

#[tokio::test]
async fn contract_probe_sources_setup_and_decode() {
    let mut host = AdapterHost::new();
    host.register(Arc::new(GrokBuildAdapter::new(HMAC_KEY)))
        .unwrap();
    let report = host
        .probe(
            adapter_grok_build::ADAPTER_ID,
            ProbeContext {
                installation_id: "ins_00000000000000000000000000".into(),
            },
        )
        .await
        .unwrap();
    assert_eq!(report.capability.available(), load_manifest().capabilities);
    let sources = host
        .discover_sources(adapter_grok_build::ADAPTER_ID, SourceContext::default())
        .await
        .unwrap();
    assert_eq!(
        sources
            .iter()
            .map(|source| source.kind())
            .collect::<Vec<_>>(),
        vec![
            SourceKind::Otlp,
            SourceKind::RuntimeStream,
            SourceKind::JsonlTail
        ]
    );
    let plan = host
        .setup_plan(adapter_grok_build::ADAPTER_ID, SetupContext::default())
        .await
        .unwrap();
    assert!(matches!(
        plan.mutations[0],
        ConfigMutation::DirectoryCreate { .. }
    ));
    let ConfigMutation::JsonMergePatch { patch, .. } = &plan.mutations[1] else {
        panic!("expected hook JSON patch")
    };
    let hook = patch.to_string();
    assert!(hook.contains("SessionEnd"));
    assert!(hook.contains("127.0.0.1"));
    assert!(!hook.contains("PostToolUse"));
    let ConfigMutation::JsonMergePatch { patch, .. } = &plan.mutations[2] else {
        panic!("expected settings JSON patch")
    };
    let serialized = patch.to_string();
    assert!(serialized.contains("GROK_OTEL_LOG_PROMPTS\":\"0"));
    assert!(serialized.contains("GROK_OTEL_LOG_TOOL_CONTENT\":\"0"));
    let events = host
        .decode(adapter_grok_build::ADAPTER_ID, otlp_frame(OTLP))
        .await
        .unwrap();
    assert_eq!(events.len(), 3);
    assert!(events
        .iter()
        .all(|event| event.source.kind == SourceKind::Otlp));
    assert!(events
        .iter()
        .all(|event| event.adapter_id == load_manifest().id));
    assert_eq!(events[2].accuracy, Accuracy::Derived);
    let EventPayload::CodeChanged(code) = &events[2].payload else {
        panic!("expected code changed");
    };
    assert_eq!(code.generated_lines.as_deref(), Some("3"));
}

#[tokio::test]
async fn session_end_hook_payload_emits_derived_code_lines() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let payload = serde_json::json!({
        "type": "code_changed",
        "occurredAt": "2026-09-10T00:00:00Z",
        "sessionId": "sess",
        "added": 12,
        "removed": 3,
        "generated": 12,
        "fileCount": 2,
        "semanticEventId": "grok-signals:sess"
    });
    let frame = RawFrame {
        installation_id: "ins_00000000000000000000000000".into(),
        source_kind: SourceKind::RuntimeStream,
        source_id: HOOK_SOURCE_ID.into(),
        cursor: "hook:sess".into(),
        payload: serde_json::to_vec(&payload).unwrap(),
    };
    let events = adapter.decode(frame).await.unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].accuracy, Accuracy::Derived);
    let EventPayload::CodeChanged(code) = &events[0].payload else {
        panic!("expected code changed");
    };
    assert_eq!(code.generated_lines.as_deref(), Some("12"));
}

#[tokio::test]
async fn search_replace_completion_counts_lines_without_keeping_patch_text() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let line = serde_json::json!({
        "method": "session/update",
        "timestamp": "2026-09-10T05:32:20Z",
        "params": {
            "sessionId": "sess-probe",
            "update": {
                "sessionUpdate": "tool_call_update",
                "toolCallId": "call-probe-1",
                "status": "completed",
                "kind": "edit",
                "_meta": {"x.ai/tool": {"name": "search_replace"}},
                "rawInput": {
                    "file_path": "C:/private/secret.rs",
                    "old_string": "GROK_OLD_CANARY\n",
                    "new_string": "GROK_OLD_CANARY\nGROK_NEW_CANARY\n"
                },
                "rawOutput": {
                    "type": "SearchReplace",
                    "EditsApplied": {
                        "old_string": "GROK_OLD_CANARY\n",
                        "new_string": "GROK_OLD_CANARY\nGROK_NEW_CANARY\n",
                        "absolute_path": "C:/private/secret.rs"
                    }
                }
            }
        }
    });
    let events = adapter
        .decode(RawFrame::jsonl(
            "ins_00000000000000000000000000",
            HISTORY_SOURCE_ID,
            "0",
            format!("{line}\n").as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].accuracy, Accuracy::Derived);
    let EventPayload::CodeChanged(code) = &events[0].payload else {
        panic!("expected code changed");
    };
    assert_eq!(code.generated_lines.as_deref(), Some("2"));
    assert_eq!(code.removed_lines, "1");
    let encoded = serde_json::to_string(&events).unwrap();
    assert!(!encoded.contains("GROK_OLD_CANARY"));
    assert!(!encoded.contains("GROK_NEW_CANARY"));
    assert!(!encoded.contains("secret.rs"));
}

#[tokio::test]
async fn completed_search_replace_without_tool_meta_still_counts() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let line = serde_json::json!({
        "method": "session/update",
        "timestamp": "2026-09-10T05:37:10Z",
        "params": {
            "sessionId": "sess-live",
            "update": {
                "sessionUpdate": "tool_call_update",
                "toolCallId": "call-live-1",
                "status": "completed",
                "rawOutput": {
                    "type": "SearchReplace",
                    "EditsApplied": {
                        "old_string": "base-line\n",
                        "new_string": "base-line\nGROK_LINE_PROBE_0910\n"
                    }
                }
            }
        }
    });
    let events = adapter
        .decode(RawFrame::jsonl(
            "ins_00000000000000000000000000",
            HISTORY_SOURCE_ID,
            "0",
            format!("{line}\n").as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 1);
    let EventPayload::CodeChanged(code) = &events[0].payload else {
        panic!("expected code changed");
    };
    assert_eq!(code.generated_lines.as_deref(), Some("2"));
}

#[tokio::test]
async fn loc_count_without_generated_field_still_counts_added_lines() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let payload = r#"{"name":"grok_code.lines_of_code.count","timestamp":"2026-08-30T10:01:02.000Z","attributes":{"session.id":"otlp-session","value":9}}"#;
    let events = adapter.decode(otlp_frame(payload)).await.unwrap();
    assert_eq!(events.len(), 1);
    let EventPayload::CodeChanged(code) = &events[0].payload else {
        panic!("expected code changed");
    };
    assert_eq!(code.generated_lines.as_deref(), Some("9"));
    assert_eq!(code.added_lines, "9");
}

#[tokio::test]
async fn strict_decode_rejects_known_malformed_records_and_source_mismatch() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let malformed = r#"{"name":"grok_code.token.usage","timestamp":"2026-08-30T00:00:00Z","attributes":{"provider":"xai","model":"m","input_tokens":"not-a-number"}}"#;
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
    let adapter = GrokBuildAdapter::for_version("2.0.0", HMAC_KEY);
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
async fn skill_load_is_ignored_failure_is_preserved_and_multiple_tools_survive() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let payload = concat!(
        "{\"name\":\"grok_code.skill.injected\",\"timestamp\":\"2026-08-30T00:00:00Z\",\"attributes\":{\"session.id\":\"s\",\"skill.name\":\"review\"}}\n",
        "{\"name\":\"grok_code.skill.execution.failed\",\"timestamp\":\"2026-08-30T00:00:01Z\",\"attributes\":{\"session.id\":\"s\",\"skill.name\":\"review\",\"skill.invocation.id\":\"skill-failed-1\",\"success\":false}}\n",
        "{\"name\":\"grok_code.tool.completed\",\"timestamp\":\"2026-08-30T00:00:02Z\",\"attributes\":{\"session.id\":\"s\",\"tool.call.id\":\"one\",\"tool.name\":\"shell\",\"success\":true}}\n",
        "{\"name\":\"grok_code.tool.completed\",\"timestamp\":\"2026-08-30T00:00:03Z\",\"attributes\":{\"session.id\":\"s\",\"tool.call.id\":\"two\",\"tool.name\":\"editor\",\"success\":false}}\n"
    );
    let events = adapter.decode(otlp_frame(payload)).await.unwrap();
    assert_eq!(events.len(), 3);
    assert!(matches!(
        &events[0].payload,
        EventPayload::SkillInvoked(payload) if !payload.success
    ));
    assert_ne!(events[1].tool_call_hash, events[2].tool_call_hash);
}

#[tokio::test]
async fn history_fallback_ignores_unknown_event_types() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
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
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let payload = concat!(
        r#"{"name":"grok_code.tool.started","timestamp":"2026-08-30T00:00:00Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell"}}
"#,
        r#"{"name":"grok_code.tool.result","timestamp":"2026-08-30T00:00:01Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell","success":true}}
"#,
        r#"{"name":"grok_code.tool.completed","timestamp":"2026-08-30T00:00:02Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell","success":true}}
"#,
        r#"{"name":"grok_code.skill.invoked","timestamp":"2026-08-30T00:00:03Z","attributes":{"session.id":"s","skill.invocation.id":"skill-1","skill.name":"review"}}
"#,
        r#"{"name":"grok_code.skill.execution.completed","timestamp":"2026-08-30T00:00:04Z","attributes":{"session.id":"s","skill.invocation.id":"skill-1","skill.name":"review","success":true}}
"#,
        r#"{"name":"grok_code.skill.execution.completed","timestamp":"2026-08-30T00:00:05Z","attributes":{"session.id":"s","skill.invocation.id":"skill-1","skill.name":"review","success":true}}"#,
    );
    let events = adapter.decode(otlp_frame(payload)).await.unwrap();
    assert_eq!(events.len(), 2);
    assert!(events.iter().all(|event| event.tool_call_hash.is_some()));
    assert_ne!(events[0].tool_call_hash, events[1].tool_call_hash);
}

#[tokio::test]
async fn terminal_without_success_is_rejected_instead_of_defaulting_true() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let payload = r#"{"name":"grok_code.tool.completed","timestamp":"2026-08-30T00:00:00Z","attributes":{"session.id":"s","tool.call.id":"tool-1","tool.name":"shell"}}"#;
    assert!(adapter.decode(otlp_frame(payload)).await.is_err());
}

#[tokio::test]
async fn decoder_rejects_future_schema_and_does_not_accept_internal_standard_names() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let future = r#"{"name":"grok_code.tool.completed","schemaVersion":"2.0","timestamp":"2026-08-30T00:00:00Z","attributes":{"tool.call.id":"tool-1","tool.name":"shell","success":true}}"#;
    assert!(adapter.decode(otlp_frame(future)).await.is_err());
    let internal = r#"{"type":"tool_invoked","timestamp":"2026-08-30T00:00:00Z","toolCallId":"tool-1","toolCategory":"shell","success":true}"#;
    assert!(adapter
        .decode(otlp_frame(internal))
        .await
        .unwrap()
        .is_empty());
}

#[tokio::test]
async fn otlp_counter_delta_is_bounded_and_cumulative_is_safely_ignored() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let delta = concat!(
        r#"{"name":"grok_code.session.count","timestamp":"2026-08-30T00:00:00Z","temporality":"delta","value":2,"attributes":{"model":"grok-code-fast"}}
"#,
        r#"{"name":"grok_code.turn.count","timestamp":"2026-08-30T00:00:01Z","aggregationTemporality":"AGGREGATION_TEMPORALITY_DELTA","value":3,"attributes":{"session.id":"s","success":true}}"#,
    );
    let events = adapter.decode(otlp_frame(delta)).await.unwrap();
    assert_eq!(events.len(), 5);
    assert_eq!(
        events
            .iter()
            .filter(|event| matches!(event.payload, EventPayload::SessionStarted(_)))
            .count(),
        2
    );
    assert_eq!(
        events
            .iter()
            .filter(|event| matches!(event.payload, EventPayload::TurnCompleted(_)))
            .count(),
        3
    );
    let first = r#"{"name":"grok_code.session.count","timestamp":"2026-08-30T00:00:02Z","temporality":"cumulative","value":3,"attributes":{"model":"cumulative"}}"#;
    assert!(adapter.decode(otlp_frame(first)).await.unwrap().is_empty());
    let second = r#"{"name":"grok_code.session.count","timestamp":"2026-08-30T00:00:03Z","temporality":"cumulative","value":5,"attributes":{"model":"cumulative"}}"#;
    assert!(adapter.decode(otlp_frame(second)).await.unwrap().is_empty());
    let huge = r#"{"name":"grok_code.turn.count","timestamp":"2026-08-30T00:00:04Z","temporality":"delta","value":10001,"attributes":{"success":true}}"#;
    assert!(adapter.decode(otlp_frame(huge)).await.unwrap().is_empty());
    assert!(matches!(
        adapter.health().await,
        adapter_sdk::AdapterHealth::Degraded { .. }
    ));
    let report = adapter.probe(ProbeContext::default()).await.unwrap();
    for capability in [Capability::Sessions, Capability::Turns] {
        let status = report
            .capability
            .capabilities
            .iter()
            .find(|status| status.capability == capability)
            .unwrap();
        assert_eq!(status.accuracy, Some(Accuracy::Derived));
    }
}

#[tokio::test]
async fn otlp_counter_without_value_or_temporality_is_safely_ignored() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let missing_temporality =
        r#"{"name":"grok_code.session.count","timestamp":"2026-08-30T00:00:00Z","value":9}"#;
    let missing_value = r#"{"name":"grok_code.turn.count","timestamp":"2026-08-30T00:00:01Z","temporality":"delta","attributes":{"success":true}}"#;
    assert!(adapter
        .decode(otlp_frame(missing_temporality))
        .await
        .unwrap()
        .is_empty());
    assert!(adapter
        .decode(otlp_frame(missing_value))
        .await
        .unwrap()
        .is_empty());
}

#[tokio::test]
async fn explicit_version_bounds_and_cross_source_semantic_ids_are_stable() {
    for unsupported in ["0.999.999", "2.0.0", "1.0", "1.0.0.1"] {
        let report = GrokBuildAdapter::for_version(unsupported, HMAC_KEY)
            .probe(ProbeContext::default())
            .await
            .unwrap();
        assert!(!report.needs_setup, "unexpected support for {unsupported}");
    }

    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let otlp = concat!(
        r#"{"name":"grok_code.tool.completed","timestamp":"2026-08-30T00:00:01Z","attributes":{"invocation.id":"inv-1","tool.name":"shell","success":true}}"#,
        "\n",
        r#"{"name":"grok_code.token.usage","timestamp":"2026-08-30T00:00:02Z","attributes":{"request.id":"req-1","provider":"xai","model":"m","input_tokens":1}}"#,
    );
    let history = concat!(
        r#"{"name":"grok_code.token.usage","timestamp":"2026-08-30T00:00:02Z","requestId":"req-1","provider":"xai","model":"m","input_tokens":1}"#,
        "\n",
        r#"{"name":"grok_code.tool.completed","timestamp":"2026-08-30T00:00:01Z","invocation_id":"inv-1","tool":"shell","success":true}"#,
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

#[tokio::test]
async fn session_updates_emit_exact_tokens_and_ignore_message_chunks() {
    let adapter = GrokBuildAdapter::new(HMAC_KEY);
    let events = adapter
        .decode(RawFrame::jsonl(
            "ins_00000000000000000000000000",
            HISTORY_SOURCE_ID,
            "0",
            SESSION_UPDATES.as_bytes(),
        ))
        .await
        .unwrap();
    assert_eq!(events.len(), 3);
    let usage = events
        .iter()
        .map(|event| match &event.payload {
            EventPayload::ModelUsageRecorded(payload) => (
                payload.provider_id.as_str(),
                payload.model_id.as_str(),
                payload.tokens.input_tokens.as_deref(),
                payload.tokens.output_tokens.as_deref(),
                payload.tokens.total_tokens.as_deref(),
            ),
            other => panic!("unexpected payload {other:?}"),
        })
        .collect::<Vec<_>>();
    assert_eq!(
        usage,
        vec![
            (
                "xai",
                "grok-4.6-build",
                Some("100"),
                Some("20"),
                Some("125")
            ),
            (
                "anthropic",
                "claude-sonnet-4-6",
                Some("10"),
                Some("20"),
                Some("30")
            ),
            ("xai", "grok", Some("7"), Some("8"), Some("15")),
        ]
    );
    assert!(events.iter().all(|event| event.accuracy == Accuracy::Exact));
    assert!(events.iter().all(|event| event.session_hash.is_some()));
    let encoded = serde_json::to_string(&events).unwrap();
    assert!(!encoded.contains("TOKSHOW_TEST_PROMPT_SECRET"));
    assert!(!encoded.contains("session-secret"));
    assert!(!encoded.contains("turn-secret"));
}
