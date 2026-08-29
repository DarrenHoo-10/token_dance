use std::sync::Arc;

use protocol::{
    Accuracy, EventDeliveryStatus, EventEnvelope, EventPayload, EventSource,
    ModelUsageRecordedPayload, RejectedEvent, RejectedEventErrorCode, SourceKind, TokenUsage,
    UploadAck,
};
use wal_spool::{
    AppendClass, InjectedKeyProvider, SourceCheckpoint, SpoolLimits, Transaction, WalStore,
};

use crate::batch::BatchLimits;
use crate::client::Uploader;
use crate::retry::RetryPolicy;
use crate::transport::{body_contains_canary, MemoryIngest, ScriptStep, ScriptedTransport};
use crate::IngestTransport;

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

fn checkpoint(offset: u64) -> SourceCheckpoint {
    SourceCheckpoint {
        source_id: "mock-sessions".into(),
        path_template_id: "AGENT_CONFIG_HOME".into(),
        file_identity: "vol:1".into(),
        generation: 1,
        file_len: offset + 8,
        offset,
        last_record_hash: None,
        status: protocol::SourceCheckpointStatus::Current,
    }
}

fn open_wal() -> (tempfile::TempDir, WalStore) {
    let dir = tempfile::tempdir().unwrap();
    let keys = Arc::new(InjectedKeyProvider::new([0x44; 32]));
    let wal = WalStore::open_with_limits(dir.path(), keys, SpoolLimits::for_tests()).unwrap();
    (dir, wal)
}

fn append(wal: &mut WalStore, events: Vec<EventEnvelope>) {
    let count = events.len();
    let checked = events
        .into_iter()
        .map(|event| privacy::PrivacyFilter.filter(event).unwrap())
        .collect();
    wal.append_txn(
        Transaction::new(
            String::new(),
            "mock-sessions",
            None,
            checkpoint(count as u64),
            checked,
            String::new(),
        ),
        AppendClass::Realtime,
    )
    .unwrap();
}

#[tokio::test]
async fn batches_respect_event_and_byte_caps() {
    let (_dir, mut wal) = open_wal();
    let events: Vec<_> = (0..6).map(|i| event(&format!("{i}xx"))).collect();
    append(&mut wal, events);
    let transport = MemoryIngest::default();
    let mut uploader = Uploader::new(format!("ins_{}", "0".repeat(26)), transport)
        .with_retry(RetryPolicy::for_tests())
        .with_limits(BatchLimits {
            max_events: 2,
            max_bytes: 512 * 1024,
        });
    let report = uploader.flush(&mut wal).await.unwrap();
    assert_eq!(report.batches, 3);
    assert_eq!(wal.unacked_count(), 0);
}

#[tokio::test]
async fn disconnect_retries_then_acks() {
    let (_dir, mut wal) = open_wal();
    append(&mut wal, vec![event("net")]);
    let transport = ScriptedTransport::new(vec![
        ScriptStep::NetworkFail,
        ScriptStep::NetworkFail,
        ScriptStep::Ack(UploadAck {
            batch_id: "ignored".into(),
            accepted: 1,
            duplicates: 0,
            rejected: Vec::new(),
            server_time: "2026-08-30T00:00:00.000Z".into(),
        }),
    ]);
    let mut uploader = Uploader::new(format!("ins_{}", "0".repeat(26)), transport)
        .with_retry(RetryPolicy::for_tests());
    let report = uploader.flush(&mut wal).await.unwrap();
    assert_eq!(report.retries, 2);
    assert_eq!(wal.unacked_count(), 0);
}

#[tokio::test]
async fn partial_ack_dead_letters_non_retryable_and_keeps_retryable() {
    let (_dir, mut wal) = open_wal();
    let a = event("aaa");
    let b = event("bbb");
    let c = event("ccc");
    append(&mut wal, vec![a.clone(), b.clone(), c.clone()]);
    let transport = ScriptedTransport::new(vec![ScriptStep::Partial {
        accepted_ids: vec![a.event_id.clone()],
        rejected: vec![
            RejectedEvent {
                event_id: b.event_id.clone(),
                error_code: RejectedEventErrorCode::SchemaInvalid,
                retryable: false,
            },
            RejectedEvent {
                event_id: c.event_id.clone(),
                error_code: RejectedEventErrorCode::InternalRetryable,
                retryable: true,
            },
        ],
    }]);
    let mut uploader = Uploader::new(format!("ins_{}", "0".repeat(26)), transport)
        .with_retry(RetryPolicy::for_tests());
    let report = uploader.flush(&mut wal).await.unwrap();
    assert_eq!(report.dead_letters, 1);
    assert_eq!(
        wal.event_status(&a.event_id),
        Some(EventDeliveryStatus::Acked)
    );
    assert_eq!(
        wal.event_status(&b.event_id),
        Some(EventDeliveryStatus::DeadLetter)
    );
    assert_eq!(
        wal.event_status(&c.event_id),
        Some(EventDeliveryStatus::Queued)
    );
}

#[tokio::test]
async fn event_id_idempotency_on_replayed_batch() {
    let (_dir, mut wal) = open_wal();
    let ev = event("idp");
    append(&mut wal, vec![ev.clone()]);
    let transport = MemoryIngest::default();
    let mut uploader = Uploader::new(format!("ins_{}", "0".repeat(26)), transport.clone())
        .with_retry(RetryPolicy::for_tests());
    uploader.flush(&mut wal).await.unwrap();
    append(&mut wal, vec![ev.clone()]);
    assert_eq!(wal.unacked_count(), 0);
    let report = uploader.flush(&mut wal).await.unwrap();
    assert_eq!(report.acked_events, 0);
    assert!(transport.seen_event(&ev.event_id));
    let dup = transport
        .upload(&protocol::UploadBatch {
            batch_id: "bat_00000000000000000000000009".into(),
            installation_id: format!("ins_{}", "0".repeat(26)),
            created_at: "2026-08-30T00:00:00.000Z".into(),
            events: vec![ev.clone()],
        })
        .await
        .unwrap();
    assert_eq!(dup.duplicates, 1);
    assert!(!body_contains_canary(&transport.bodies(), "TOKSHOW_TEST_"));
}

#[tokio::test]
async fn http_400_splits_then_dead_letters_singleton() {
    let (_dir, mut wal) = open_wal();
    append(&mut wal, vec![event("s1a"), event("s2a")]);
    let transport = ScriptedTransport::new(vec![
        ScriptStep::Http {
            status: 400,
            retry_after: None,
        },
        ScriptStep::Http {
            status: 400,
            retry_after: None,
        },
        ScriptStep::Http {
            status: 400,
            retry_after: None,
        },
    ]);
    let mut uploader = Uploader::new(format!("ins_{}", "0".repeat(26)), transport)
        .with_retry(RetryPolicy::for_tests());
    let report = uploader.flush(&mut wal).await.unwrap();
    assert_eq!(report.dead_letters, 2);
    assert_eq!(wal.unacked_count(), 0);
}
