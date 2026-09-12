use std::sync::Arc;

use adapter_host::AdapterHost;
use adapter_opencode::{
    fingerprint_supported, load_manifest, OpenCodeAdapter, ADAPTER_ID, FINGERPRINT_V1, KNOWN_JSON,
    SQLITE_SOURCE_ID,
};
use adapter_sdk::{
    AgentAdapter, Capability, EventPayload, ProbeContext, RawFrame, SourceContext, SourceKind,
};
use privacy::PrivacyFilter;

const INSTALL: &str = "ins_00000000000000000000000000";
const KEY: &[u8] = b"opencode-contract-device-hmac";

fn frame(payload: &str) -> RawFrame {
    frame_with_cursor("opencode:1", payload)
}

fn frame_with_cursor(cursor: &str, payload: &str) -> RawFrame {
    RawFrame {
        installation_id: INSTALL.into(),
        source_kind: SourceKind::SqliteSnapshot,
        source_id: SQLITE_SOURCE_ID.into(),
        cursor: cursor.into(),
        payload: payload.as_bytes().to_vec(),
    }
}

#[tokio::test]
async fn known_fingerprint_decodes_session_metrics_without_prompt() {
    let adapter = Arc::new(OpenCodeAdapter::new("1.18.18", FINGERPRINT_V1, KEY));
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
    let events = host.decode(ADAPTER_ID, frame(KNOWN_JSON)).await.unwrap();
    assert_eq!(events.len(), 4);
    assert!(matches!(events[0].payload, EventPayload::SessionStarted(_)));
    assert!(matches!(
        events[1].payload,
        EventPayload::ModelUsageRecorded(_)
    ));
    assert!(matches!(events[2].payload, EventPayload::TurnCompleted(_)));
    assert!(matches!(events[3].payload, EventPayload::CodeChanged(_)));
    let encoded = serde_json::to_string(&events).unwrap();
    assert!(!encoded.contains("OPENCODE_PROMPT_CANARY"));
    assert!(events
        .into_iter()
        .all(|event| PrivacyFilter.filter(event).is_ok()));
    let _ = load_manifest();
}

#[tokio::test]
async fn unknown_fingerprint_never_guesses_sql() {
    let adapter = OpenCodeAdapter::new("9.9.9", "opencode-sqlite-v99-unknown", KEY);
    assert!(!fingerprint_supported("opencode-sqlite-v99-unknown"));
    let sources = adapter
        .discover_sources(SourceContext {
            installation_id: INSTALL.into(),
        })
        .await
        .unwrap();
    assert!(sources.is_empty());
    let events = adapter.decode(frame(KNOWN_JSON)).await.unwrap();
    assert!(events.is_empty());
}

#[tokio::test]
async fn usage_identity_does_not_depend_on_poll_cursor() {
    let adapter = OpenCodeAdapter::new("1.18.18", FINGERPRINT_V1, KEY);
    // Real part rows carry a NUMERIC rowid as stepId; identity must accept
    // numbers or it falls back to the batch sequence and incremental polls
    // collide with fingerprints from the initial rescan.
    let left = adapter
        .decode(frame_with_cursor("3:3892:0:1786885842651", KNOWN_JSON))
        .await
        .unwrap();
    let right = adapter
        .decode(frame_with_cursor("3:1:0:0", KNOWN_JSON))
        .await
        .unwrap();
    assert_eq!(left.len(), right.len());
    for (l, r) in left.iter().zip(right.iter()) {
        assert_eq!(l.event_id, r.event_id, "kind must share one identity");
    }
    let incremental = r#"{"fingerprint":"opencode-sqlite-v1-uv0","records":[{"type":"step_finish","timestamp":"2026-09-11T01:00:05Z","sessionId":"opencode-session-secret","stepId":777,"inputTokens":5,"outputTokens":1,"reasoningTokens":0,"cacheReadTokens":0,"cacheWriteTokens":0}]}"#;
    let small = adapter
        .decode(frame_with_cursor("3:3892:500:999", incremental))
        .await
        .unwrap();
    // step_finish emits both a usage and a turn event for the same row id.
    assert_eq!(small.len(), 2);
    let left_ids: Vec<_> = left.iter().map(|e| e.event_id.clone()).collect();
    assert!(!left_ids.contains(&small[0].event_id));
    assert!(!left_ids.contains(&small[1].event_id));
}
