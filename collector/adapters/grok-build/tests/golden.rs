use std::{fs, path::PathBuf};

use adapter_grok_build::{decode_jsonl, load_manifest, HISTORY_SOURCE_ID};
use adapter_sdk::RawFrame;

const HISTORY: &str = include_str!("../fixtures/contract/history.jsonl");

fn actual() -> String {
    let frame = RawFrame::jsonl(
        "ins_00000000000000000000000000",
        HISTORY_SOURCE_ID,
        "0",
        HISTORY.as_bytes(),
    );
    let events = decode_jsonl(
        &load_manifest(),
        Some("1.0.0"),
        b"tokenshow-adapter-fixture-hmac-key-v1",
        &frame,
    )
    .unwrap();
    let mut json = serde_json::to_string_pretty(&events).unwrap();
    json.push('\n');
    json
}

fn path() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("fixtures/golden/history.events.json")
}

#[test]
fn history_matches_golden() {
    assert_eq!(actual(), fs::read_to_string(path()).unwrap());
}

#[test]
#[ignore]
fn generate_golden() {
    fs::write(path(), actual()).unwrap();
}
