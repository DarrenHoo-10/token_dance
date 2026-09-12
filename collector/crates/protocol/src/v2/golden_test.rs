use crate::v2::{
    classify_ack, compute_content_hash, content_hash_canonical_json, reject_duplicate_keys,
};
use serde_json::Value;
use std::fs;
use std::path::PathBuf;

fn repo_root() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("..")
        .join("..")
        .join("..")
}

#[test]
fn golden_content_hash_and_ack_match_fixtures() {
    let raw = fs::read_to_string(
        repo_root()
            .join("schemas/protocol/v2/fixtures/golden/events.json"),
    )
    .expect("read golden fixtures");
    let fixtures: Vec<Value> = serde_json::from_str(&raw).expect("parse fixtures");
    for fixture in fixtures {
        let name = fixture["name"].as_str().unwrap();
        if name == "ack_duplicate_vs_conflict" {
            let base = &fixture["base_event"];
            let base_hash = compute_content_hash(base).expect("base hash");
            assert_eq!(base_hash, fixture["base_hash"].as_str().unwrap());
            assert_eq!(classify_ack(None, &base_hash), "accepted");
            assert_eq!(classify_ack(Some(&base_hash), &base_hash), "duplicate");
            let conflict_hash =
                compute_content_hash(&fixture["conflict_event"]).expect("conflict hash");
            assert_eq!(conflict_hash, fixture["conflict_hash"].as_str().unwrap());
            assert_eq!(classify_ack(Some(&base_hash), &conflict_hash), "conflict");
            continue;
        }
        let event = &fixture["event"];
        let canonical = content_hash_canonical_json(event).expect("canonical");
        assert_eq!(canonical, fixture["canonical_json"].as_str().unwrap());
        let hash = compute_content_hash(event).expect("hash");
        assert_eq!(hash, fixture["content_hash"].as_str().unwrap());
    }
}

#[test]
fn negative_fixtures_are_rejected() {
    let root = repo_root().join("schemas/protocol/v2/fixtures/negative");
    let dup = fs::read_to_string(root.join("duplicate_keys.json.txt")).unwrap();
    assert!(reject_duplicate_keys(dup.trim()).is_err());

    let unknown: Value =
        serde_json::from_str(&fs::read_to_string(root.join("unknown_field.json")).unwrap())
            .unwrap();
    assert!(compute_content_hash(&unknown).is_err());

    let negative: Value =
        serde_json::from_str(&fs::read_to_string(root.join("negative_count.json")).unwrap())
            .unwrap();
    assert!(compute_content_hash(&negative).is_err());
}
