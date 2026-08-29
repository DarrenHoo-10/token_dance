use std::fs;
use std::sync::Arc;

use protocol::{
    Accuracy, EventEnvelope, EventPayload, EventSource, ModelUsageRecordedPayload, SourceKind,
    TokenUsage,
};

use crate::checkpoint::SourceCheckpoint;
use crate::codec::{
    encode_frame, read_frame, FrameRead, FrameType, FLAG_ENCRYPTED, MAGIC, TRAILER,
};
use crate::fault::FaultPoint;
use crate::frame::{decode_cbor, encode_cbor, AckPayload, PersistedTransaction, Transaction};
use crate::keys::{InjectedKeyProvider, UnavailableKeyProvider};
use crate::limits::{AppendClass, SpoolLimits};
use crate::store::{contains_bytes, WalStore};
use crate::{Backpressure, WalError};

fn hmac() -> String {
    format!("hmac-sha256:{}", "A".repeat(43))
}

fn event(marker: &str) -> EventEnvelope {
    let mut event_id = marker.as_bytes().to_vec();
    event_id.resize(43, b'B');
    EventEnvelope {
        schema_version: "1.0".into(),
        event_id: String::from_utf8(event_id).expect("event id"),
        adapter_id: "dev.tokenshow.adapter.mock".into(),
        adapter_version: "1.0.0".into(),
        agent_id: "mock-agent".into(),
        agent_version: None,
        installation_id: format!("ins_{}", "0".repeat(26)),
        occurred_at: "2026-08-30T00:00:00.000Z".into(),
        session_hash: Some(hmac()),
        turn_hash: None,
        tool_call_hash: None,
        source: EventSource {
            kind: SourceKind::JsonlTail,
            cursor_hmac: hmac(),
            raw_fingerprint_hmac: hmac(),
        },
        accuracy: Accuracy::Exact,
        payload: EventPayload::ModelUsageRecorded(ModelUsageRecordedPayload {
            provider_id: "mock-provider".into(),
            model_id: "mock-model".into(),
            tokens: TokenUsage {
                input_tokens: Some("10".into()),
                output_tokens: Some("5".into()),
                cache_read_tokens: None,
                cache_write_tokens: None,
                reasoning_tokens: None,
                tool_tokens: None,
                total_tokens: Some("15".into()),
            },
        }),
    }
}

fn checkpoint(offset: u64, generation: u64) -> SourceCheckpoint {
    SourceCheckpoint {
        source_id: "mock-sessions".into(),
        path_template_id: "AGENT_CONFIG_HOME".into(),
        file_identity: "vol:1".into(),
        generation,
        file_len: offset + 10,
        offset,
        last_record_hash: Some("sha256:deadbeef".into()),
        driver_checkpoint: None,
        status: protocol::SourceCheckpointStatus::Current,
    }
}

fn txn(events: Vec<EventEnvelope>, next: SourceCheckpoint) -> Transaction {
    let checked = events
        .into_iter()
        .map(|event| privacy::PrivacyFilter.filter(event).unwrap())
        .collect();
    Transaction::new(
        "txn_00000000000000000000000001",
        "mock-sessions",
        None,
        next,
        checked,
        "2026-08-30T00:00:00.000Z",
    )
}

fn open_store(dir: &std::path::Path) -> WalStore {
    let keys = Arc::new(InjectedKeyProvider::new([0x11; 32]));
    let mut limits = SpoolLimits::for_tests();
    limits.rotate_frames = 2;
    limits.rotate_bytes = 2 * 1024;
    limits.soft_events = 4;
    limits.hard_events = 6;
    limits.soft_bytes = 64 * 1024;
    limits.hard_bytes = 128 * 1024;
    WalStore::open_with_limits(dir, keys, limits).expect("open wal")
}

#[test]
fn tsw1_round_trip_has_header_crc_and_trailer() {
    let key = [0x22; 32];
    let encoded = encode_frame(&key, 7, FrameType::Txn, b"hello").unwrap();
    assert_eq!(&encoded.bytes[..4], MAGIC);
    assert_eq!(encoded.bytes[6], FrameType::Txn as u8);
    assert_eq!(encoded.bytes[7], FLAG_ENCRYPTED);
    assert_eq!(
        u64::from_le_bytes(encoded.bytes[8..16].try_into().unwrap()),
        7
    );
    let payload_len = u32::from_le_bytes(encoded.bytes[16..20].try_into().unwrap()) as usize;
    assert_eq!(encoded.bytes.len(), 20 + payload_len + 8);
    assert_eq!(&encoded.bytes[encoded.bytes.len() - 4..], TRAILER);
    match read_frame(&encoded.bytes, 0, &key, 1024).unwrap() {
        FrameRead::Frame(frame) => {
            assert_eq!(frame.sequence, 7);
            assert_eq!(frame.plaintext, b"hello");
            assert_eq!(frame.frame_type, FrameType::Txn);
        }
        other => panic!("unexpected {other:?}"),
    }
}

#[test]
fn each_frame_uses_an_independent_nonce() {
    let key = [0x33; 32];
    let left = encode_frame(&key, 1, FrameType::Ack, b"same").unwrap();
    let right = encode_frame(&key, 1, FrameType::Ack, b"same").unwrap();
    assert_ne!(left.bytes, right.bytes);
}

#[test]
fn txn_cbor_contains_events_and_next_checkpoint() {
    let payload = txn(vec![event("evt1")], checkpoint(42, 1));
    let bytes = encode_cbor(&payload).unwrap();
    let decoded: PersistedTransaction = decode_cbor(&bytes).unwrap();
    assert_eq!(decoded.next_checkpoint.offset, 42);
    assert_eq!(decoded.normalized_events.len(), 1);
}

#[test]
fn missing_key_does_not_create_plaintext_wal() {
    let dir = tempfile::tempdir().unwrap();
    let keys = Arc::new(UnavailableKeyProvider);
    let err = match WalStore::open(dir.path(), keys) {
        Ok(_) => panic!("missing key must not open a WAL"),
        Err(err) => err,
    };
    assert!(matches!(err, WalError::KeyUnavailable { .. }));
    let wal_dir = dir.path().join("wal");
    if wal_dir.exists() {
        assert!(fs::read_dir(&wal_dir).unwrap().next().is_none());
    }
}

#[test]
fn rejects_transaction_checkpoint_source_mismatch() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    let mut mismatch = txn(vec![event("src")], checkpoint(10, 1));
    mismatch.source_id = "different-source".into();
    let error = wal
        .append_txn(mismatch, AppendClass::Realtime)
        .expect_err("source mismatch must not advance checkpoint");
    assert!(matches!(error, WalError::Payload(_)));
    assert!(wal.latest_checkpoint("mock-sessions").is_none());
}

#[test]
fn append_is_atomic_with_checkpoint_after_durable_write() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    let next = checkpoint(80, 1);
    wal.append_txn(txn(vec![event("aaa")], next.clone()), AppendClass::Realtime)
        .unwrap();
    assert_eq!(wal.latest_checkpoint("mock-sessions").unwrap().offset, 80);
    assert_eq!(wal.unacked_count(), 1);

    drop(wal);
    let wal = open_store(dir.path());
    assert_eq!(wal.latest_checkpoint("mock-sessions").unwrap().offset, 80);
    assert_eq!(wal.unacked_events()[0].event_id, event("aaa").event_id);
}

#[test]
fn partial_tail_frame_is_truncated_and_not_applied() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    wal.append_txn(
        txn(vec![event("aaa")], checkpoint(10, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    wal.inject_fault(FaultPoint::PartialWrite { bytes: 12 });
    let err = wal
        .append_txn(
            txn(vec![event("bbb")], checkpoint(20, 1)),
            AppendClass::Realtime,
        )
        .unwrap_err();
    assert!(matches!(err, WalError::InjectedCrash));
    drop(wal);

    let wal = open_store(dir.path());
    assert_eq!(wal.unacked_count(), 1);
    assert_eq!(wal.latest_checkpoint("mock-sessions").unwrap().offset, 10);
    let raw = wal.raw_wal_bytes().unwrap();
    assert!(
        raw.len() >= 20,
        "truncated file should keep the first complete frame"
    );
}

#[test]
fn durable_write_abort_replays_events_and_checkpoint() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    wal.inject_fault(FaultPoint::DurableWriteAbort);
    let err = wal
        .append_txn(
            txn(vec![event("ccc")], checkpoint(30, 1)),
            AppendClass::Realtime,
        )
        .unwrap_err();
    assert!(matches!(err, WalError::InjectedCrash));
    assert!(wal.latest_checkpoint("mock-sessions").is_none());
    drop(wal);

    let wal = open_store(dir.path());
    assert_eq!(wal.unacked_count(), 1);
    assert_eq!(wal.latest_checkpoint("mock-sessions").unwrap().offset, 30);
}

#[test]
fn mid_segment_corruption_is_isolated_and_not_skipped() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    wal.append_txn(
        txn(vec![event("aaa")], checkpoint(10, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    wal.append_txn(
        txn(vec![event("bbb")], checkpoint(20, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    drop(wal);

    let path = dir.path().join("wal").join("0000000000000001.wal");
    let mut bytes = fs::read(&path).unwrap();
    let mut hits = 0;
    let mut corrupt_at = None;
    for index in 0..bytes.len().saturating_sub(3) {
        if &bytes[index..index + 4] == MAGIC {
            hits += 1;
            if hits == 2 {
                corrupt_at = Some(index);
                break;
            }
        }
    }
    bytes[corrupt_at.expect("second frame")] = b'X';
    fs::write(&path, &bytes).unwrap();

    let extra = encode_frame(&[0x11; 32], 99, FrameType::Settings, b"after").unwrap();
    let mut combined = fs::read(&path).unwrap();
    combined.extend_from_slice(&extra.bytes);
    fs::write(&path, combined).unwrap();

    let wal = open_store(dir.path());
    assert!(!wal.isolated_segments().is_empty());
    assert_eq!(wal.unacked_count(), 1);
    assert_eq!(wal.latest_checkpoint("mock-sessions").unwrap().offset, 10);
}

#[test]
fn ack_frames_and_compact_only_remove_fully_acked_segments() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    wal.append_txn(
        txn(vec![event("aaa")], checkpoint(10, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    wal.append_txn(
        txn(vec![event("bbb")], checkpoint(20, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    wal.append_txn(
        txn(vec![event("ccc")], checkpoint(30, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    let ids_before = wal.segment_ids().unwrap();
    assert!(ids_before.len() >= 2);

    wal.append_ack(AckPayload {
        batch_id: "bat_00000000000000000000000001".into(),
        acked_event_ids: vec![event("aaa").event_id, event("bbb").event_id],
        server_acked_at: "2026-08-30T00:00:01.000Z".into(),
    })
    .unwrap();
    let report = wal.compact().unwrap();
    assert!(!report.deleted_segments.is_empty() || !report.rewritten_segments.is_empty());
    assert_eq!(wal.unacked_count(), 1);
    assert_eq!(
        wal.event_status(&event("aaa").event_id),
        Some(protocol::EventDeliveryStatus::Acked)
    );
}

#[test]
fn plaintext_frame_is_rejected_and_events_are_encrypted_on_disk() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    let ev = event("zzz");
    wal.append_txn(
        txn(vec![ev.clone()], checkpoint(1, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    let raw = wal.raw_wal_bytes().unwrap();
    assert!(!contains_bytes(&raw, &ev.event_id));
    assert!(!contains_bytes(&raw, "mock-model"));
    assert!(contains_bytes(&raw, "TSW1"));
    let decoded = wal.decoded_payload_bytes().unwrap();
    assert!(contains_bytes(&decoded, &ev.event_id));
}

#[test]
fn production_limits_match_architecture_baseline() {
    let limits = SpoolLimits::default();
    assert_eq!(limits.soft_bytes, 128 * 1024 * 1024);
    assert_eq!(limits.hard_bytes, 256 * 1024 * 1024);
    assert_eq!(limits.soft_events, 100_000);
    assert_eq!(limits.hard_events, 250_000);
    assert_eq!(limits.rotate_bytes, 16 * 1024 * 1024);
    assert_eq!(limits.rotate_frames, 10_000);
}

#[test]
fn soft_and_hard_backpressure_thresholds() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    for i in 0..4 {
        let marker = format!("{i}aa");
        wal.append_txn(
            txn(vec![event(&marker)], checkpoint(10 * (i as u64 + 1), 1)),
            AppendClass::Realtime,
        )
        .unwrap();
    }
    assert_eq!(wal.backpressure(), Backpressure::Soft);
    wal.append_txn(
        txn(vec![event("eaa")], checkpoint(50, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    wal.append_txn(
        txn(vec![event("faa")], checkpoint(60, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    assert_eq!(wal.backpressure(), Backpressure::Hard);
    let err = wal
        .append_txn(
            txn(vec![event("gaa")], checkpoint(70, 1)),
            AppendClass::Historical,
        )
        .unwrap_err();
    assert!(matches!(err, WalError::HardBackpressure));
    wal.append_txn(
        txn(vec![event("haa")], checkpoint(80, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
}

#[test]
fn duplicate_event_id_is_not_requeued() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_store(dir.path());
    let ev = event("dup");
    wal.append_txn(
        txn(vec![ev.clone()], checkpoint(1, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    wal.append_txn(
        txn(vec![ev.clone()], checkpoint(1, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    assert_eq!(wal.unacked_count(), 1);
    wal.append_ack(AckPayload {
        batch_id: "bat_00000000000000000000000002".into(),
        acked_event_ids: vec![ev.event_id.clone()],
        server_acked_at: "2026-08-30T00:00:01.000Z".into(),
    })
    .unwrap();
    wal.append_txn(
        txn(vec![ev.clone()], checkpoint(2, 1)),
        AppendClass::Realtime,
    )
    .unwrap();
    assert_eq!(wal.unacked_count(), 0);
    assert_eq!(
        wal.event_status(&ev.event_id),
        Some(protocol::EventDeliveryStatus::Acked)
    );
}
