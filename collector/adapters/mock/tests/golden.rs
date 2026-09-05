use std::fs;
use std::path::PathBuf;

use adapter_mock::{
    decode_jsonl, load_manifest, MIN_SESSION_JSONL, SOURCE_ID, UNKNOWN_FIELDS_JSONL,
};
use adapter_sdk::RawFrame;

fn fixture_frame(payload: &str) -> RawFrame {
    RawFrame::jsonl("ins_01test", SOURCE_ID, "0", payload.as_bytes())
}

fn golden_path(name: &str) -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("fixtures")
        .join("golden")
        .join(format!("{name}.events.json"))
}

fn actual_json(payload: &str) -> String {
    let events = decode_jsonl(&load_manifest(), &fixture_frame(payload)).unwrap();
    let mut json = serde_json::to_string_pretty(&events).unwrap();
    json.push('\n');
    json
}

fn assert_golden(name: &str, payload: &str) {
    let actual = actual_json(payload);
    let expected = fs::read_to_string(golden_path(name))
        .unwrap_or_else(|err| panic!("missing golden {name}: {err}"));
    assert_eq!(actual, expected, "golden mismatch for {name}");
}

#[test]
fn golden_min_session() {
    assert_golden("min-session", MIN_SESSION_JSONL);
}

#[test]
fn golden_unknown_fields() {
    assert_golden("unknown-fields", UNKNOWN_FIELDS_JSONL);
}

#[test]
#[ignore]
fn generate_golden_files() {
    let dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("fixtures")
        .join("golden");
    fs::create_dir_all(&dir).unwrap();
    fs::write(
        dir.join("min-session.events.json"),
        actual_json(MIN_SESSION_JSONL),
    )
    .unwrap();
    fs::write(
        dir.join("unknown-fields.events.json"),
        actual_json(UNKNOWN_FIELDS_JSONL),
    )
    .unwrap();
}
