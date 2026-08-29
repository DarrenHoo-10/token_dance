use adapter_deepseek_harness::{DeepSeekHarnessAdapter, HISTORY_SOURCE_ID};
use adapter_sdk::{AgentAdapter, RawFrame};
use privacy::PrivacyFilter;

const HISTORY: &str = include_str!("../fixtures/contract/history.jsonl");
const HMAC_KEY: &[u8] = b"tokenshow-adapter-fixture-hmac-key-v1";

#[tokio::test]
async fn prompt_content_paths_credentials_and_raw_ids_never_escape_decode() {
    let events = DeepSeekHarnessAdapter::new(HMAC_KEY)
        .decode(RawFrame::jsonl(
            "ins_00000000000000000000000000",
            HISTORY_SOURCE_ID,
            "0",
            HISTORY.as_bytes(),
        ))
        .await
        .unwrap();
    for event in &events {
        PrivacyFilter.filter(event.clone()).unwrap();
    }
    let json = serde_json::to_string(&events).unwrap();
    for secret in [
        "TOKSHOW_TEST_PROMPT_SECRET",
        "TOKSHOW_TEST_SOURCE_CODE_SECRET",
        "TOKSHOW_TEST_ABSOLUTE_PATH_SECRET",
        "TOKSHOW_TEST_API_KEY_SECRET",
        "session-secret",
        "turn-secret",
        "child-session-secret",
    ] {
        assert!(!json.contains(secret), "privacy canary escaped: {secret}");
    }
    assert!(json.contains("hmac-sha256:"));
}
