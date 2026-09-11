//! P1 store/queue unit tests.

use super::*;
use crate::local_store::pipeline::types::{
    Consumer, ConsumerStatus, CursorKind, EventCandidate, RegisterSource, SourceCommitBatch,
    SourceKind, TaskComplete, TaskRetry, DEFAULT_LEASE_MS, EVENT_TTL_MS,
};

fn blob(seed: u8) -> [u8; 32] {
    [seed; 32]
}

fn payload_exact() -> String {
    r#"{"meta":{"accuracy":"exact","time_source":"source_record"},"usage":{"token_total":10,"input_context_tokens":8,"output_tokens":2}}"#
        .into()
}

fn open_store() -> PipelineStore {
    let mut store = PipelineStore::open_in_memory().expect("open");
    store.set_clock_ms(1_700_000_000_000);
    store
}

fn register_jsonl(store: &mut PipelineStore) -> i64 {
    store
        .register_source(&RegisterSource {
            harness_id: "codex".into(),
            source_key: blob(1),
            source_kind: SourceKind::Jsonl,
            locator_ref: "local:codex/session.jsonl".into(),
            stream_key: "main".into(),
            cursor_kind: CursorKind::ByteOffset,
            cursor_json: r#"{"offset":0}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: r#"{"baseline":0}"#.into(),
            observed_boundary_json: r#"{"len":0}"#.into(),
            next_poll_at: Some(1_700_000_000_000),
        })
        .expect("register source")
}

fn candidate(seed: u8, session: Option<u8>) -> EventCandidate {
    EventCandidate {
        event_id: blob(seed),
        fact_key: blob(seed.wrapping_add(50)),
        fact_revision: 1,
        event_type: "model_usage_recorded".into(),
        schema_version: 2,
        metric_semantics_version: 1,
        content_hash: blob(seed.wrapping_add(100)),
        occurred_at: 1_700_000_000_000,
        model_key: 0,
        skill_id: None,
        session_key: session.map(blob),
        turn_key: None,
        cost_scope_key: None,
        payload_json: payload_exact(),
        applicable_consumers: Consumer::ALL.to_vec(),
    }
}

fn commit_one(
    store: &mut PipelineStore,
    source_id: i64,
    token: &str,
    seq: i64,
    events: Vec<EventCandidate>,
    cursor: &str,
) -> SourceCommitResult {
    store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: seq,
            lease_token: token.into(),
            cursor_json: cursor.into(),
            decoder_state_version: 1,
            decoder_state_json: r#"{"baseline":1}"#.into(),
            observed_boundary_json: r#"{"len":10}"#.into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: Some(1_700_000_100_000),
            events,
            created_at_override: None,
        })
        .expect("commit")
}

#[test]
fn empty_db_creates_ten_tables_and_reinit_preserves_events() {
    let dir = tempfile::TempDir::new().unwrap();
    let mut store = PipelineStore::open(dir.path()).unwrap();
    assert_eq!(store.business_table_count().unwrap(), 10);
    assert!(store.is_ready().unwrap());

    let source_id = register_jsonl(&mut store);
    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    commit_one(
        &mut store,
        source_id,
        &token,
        seq,
        vec![candidate(7, None)],
        r#"{"offset":10}"#,
    );
    assert_eq!(store.event_count().unwrap(), 1);

    // Re-open / re-init must not wipe.
    drop(store);
    let store2 = PipelineStore::open(dir.path()).unwrap();
    assert_eq!(store2.event_count().unwrap(), 1);
    assert_eq!(store2.business_table_count().unwrap(), 10);
}

#[test]
fn skill_registers_once_by_skill_key() {
    let mut store = open_store();
    let id1 = store
        .register_skill(&blob(9), Some("Search"))
        .unwrap();
    let id2 = store
        .register_skill(&blob(9), Some("Renamed"))
        .unwrap();
    assert_eq!(id1, id2);
    let name: String = store
        .with_connection(|conn| {
            Ok(conn.query_row(
                "SELECT COALESCE(public_name,'') FROM skill_dimensions WHERE id=?1",
                rusqlite::params![id1],
                |r| r.get(0),
            )?)
        })
        .unwrap();
    assert_eq!(name, "Search");
}

#[test]
fn source_commit_is_atomic_with_tasks_and_cursor() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    assert_eq!(seq, 0);

    let result = commit_one(
        &mut store,
        source_id,
        &token,
        seq,
        vec![candidate(1, None), candidate(2, None)],
        r#"{"offset":42}"#,
    );
    assert_eq!(result.commit_seq, 1);
    assert_eq!(result.inserted_events, 2);
    assert_eq!(store.event_count().unwrap(), 2);
    // 2 events × 4 consumers
    assert_eq!(store.task_count().unwrap(), 8);
    assert_eq!(
        store.source_cursor_json(source_id).unwrap(),
        r#"{"offset":42}"#
    );
    assert_eq!(store.source_commit_seq(source_id).unwrap(), 1);

    // Lease cleared after commit.
    let err = store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: 1,
            lease_token: token,
            cursor_json: r#"{"offset":99}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: None,
            events: vec![],
            created_at_override: None,
        })
        .unwrap_err();
    assert!(matches!(err, PipelineError::SourceLeaseMismatch));
}

#[test]
fn commit_seq_cas_rejects_stale_writer() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let (token_a, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    commit_one(
        &mut store,
        source_id,
        &token_a,
        seq,
        vec![candidate(3, None)],
        r#"{"offset":1}"#,
    );

    // Simulate a crashed writer retrying with the old commit_seq after a successful commit.
    let (token_b, _, seq_b) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    assert_eq!(seq_b, 1);
    let err = store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: 0, // stale
            lease_token: token_b,
            cursor_json: r#"{"offset":2}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: None,
            events: vec![candidate(4, None)],
            created_at_override: None,
        })
        .unwrap_err();
    assert!(matches!(
        err,
        PipelineError::SourceCommitSeqMismatch {
            expected: 0,
            actual: 1
        }
    ));
    assert_eq!(store.event_count().unwrap(), 1);
    assert_eq!(
        store.source_cursor_json(source_id).unwrap(),
        r#"{"offset":1}"#
    );
}

#[test]
fn same_key_same_hash_is_idempotent() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let event = candidate(11, None);

    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    let first = commit_one(
        &mut store,
        source_id,
        &token,
        seq,
        vec![event.clone()],
        r#"{"offset":1}"#,
    );
    assert_eq!(first.inserted_events, 1);

    let (token2, _, seq2) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    let second = commit_one(
        &mut store,
        source_id,
        &token2,
        seq2,
        vec![event],
        r#"{"offset":2}"#,
    );
    assert_eq!(second.duplicate_events, 1);
    assert_eq!(second.inserted_events, 0);
    assert_eq!(store.event_count().unwrap(), 1);
    // Still only one set of tasks.
    assert_eq!(store.task_count().unwrap(), 4);
}

#[test]
fn same_key_different_hash_rolls_back_entire_batch() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let mut event = candidate(12, None);

    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    commit_one(
        &mut store,
        source_id,
        &token,
        seq,
        vec![event.clone()],
        r#"{"offset":1}"#,
    );

    event.content_hash = blob(200);
    let (token2, _, seq2) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    let err = store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: seq2,
            lease_token: token2.clone(),
            cursor_json: r#"{"offset":99}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: Some(1),
            events: vec![event, candidate(13, None)],
            created_at_override: None,
        })
        .unwrap_err();
    assert!(matches!(err, PipelineError::IdentityContentConflict { .. }));
    // Cursor and new events must not advance / insert.
    assert_eq!(
        store.source_cursor_json(source_id).unwrap(),
        r#"{"offset":1}"#
    );
    assert_eq!(store.event_count().unwrap(), 1);
    assert_eq!(store.source_commit_seq(source_id).unwrap(), 1);
    // Lease should still be held (rollback does not clear it) so caller can recover.
    let err2 = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap_err();
    assert!(matches!(err2, PipelineError::SourceLeaseMismatch));
}

#[test]
fn task_lease_claim_complete_and_expired_reclaim() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    commit_one(
        &mut store,
        source_id,
        &token,
        seq,
        vec![candidate(21, None)],
        r#"{"offset":1}"#,
    );

    let claimed = store
        .claim_tasks(Consumer::Day, 10, DEFAULT_LEASE_MS)
        .unwrap();
    assert_eq!(claimed.len(), 1);
    let task = &claimed[0];
    let status = store.event_status_json(task.event_row_id).unwrap();
    assert!(status.contains(r#""day":2"#));

    // Stale lease token cannot complete.
    let bad = store.complete_task(TaskComplete {
        task_id: task.task_id,
        event_row_id: task.event_row_id,
        consumer: Consumer::Day,
        lease_token: "stale".into(),
        status: ConsumerStatus::Applied,
        error_code: None,
    });
    assert!(matches!(bad, Err(PipelineError::TaskLeaseMismatch)));

    store
        .complete_task(TaskComplete {
            task_id: task.task_id,
            event_row_id: task.event_row_id,
            consumer: Consumer::Day,
            lease_token: task.lease_token.clone(),
            status: ConsumerStatus::Applied,
            error_code: None,
        })
        .unwrap();
    let status = store.event_status_json(task.event_row_id).unwrap();
    assert!(status.contains(r#""day":3"#));
    // Task row removed on success.
    assert_eq!(store.task_count().unwrap(), 3);

    // Lease reclaim: claim hour, advance clock past lease_until.
    let hour = store
        .claim_tasks(Consumer::Hour, 10, 1_000)
        .unwrap();
    assert_eq!(hour.len(), 1);
    store.set_clock_ms(store.now_ms() + 5_000);
    let reclaimed = store.reclaim_expired_leases().unwrap();
    assert_eq!(reclaimed, 1);
    let status = store.event_status_json(hour[0].event_row_id).unwrap();
    assert!(status.contains(r#""hour":1"#)); // retry

    // Old lease result is discarded.
    let late = store.complete_task(TaskComplete {
        task_id: hour[0].task_id,
        event_row_id: hour[0].event_row_id,
        consumer: Consumer::Hour,
        lease_token: hour[0].lease_token.clone(),
        status: ConsumerStatus::Applied,
        error_code: None,
    });
    assert!(matches!(late, Err(PipelineError::TaskLeaseMismatch)));

    // Can claim again after reclaim.
    let again = store
        .claim_tasks(Consumer::Hour, 10, DEFAULT_LEASE_MS)
        .unwrap();
    assert_eq!(again.len(), 1);
}

#[test]
fn ttl_hard_deletes_without_extending_on_update_and_blocks_dependents() {
    let mut store = open_store();
    let base = 1_700_000_000_000i64;
    store.set_clock_ms(base);
    let source_id = register_jsonl(&mut store);

    // Event A created now (will expire at base+14d).
    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    let a = candidate(31, Some(77));
    let b = candidate(32, Some(77));
    store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: seq,
            lease_token: token,
            cursor_json: r#"{"offset":1}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: Some(base + 1),
            events: vec![a.clone()],
            created_at_override: Some(base),
        })
        .unwrap();

    // Event B created later so it is still within TTL when A expires.
    store.set_clock_ms(base + 60_000);
    let (token2, _, seq2) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    store
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: seq2,
            lease_token: token2,
            cursor_json: r#"{"offset":2}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: Some(base + 2),
            events: vec![b.clone()],
            created_at_override: Some(base + 60_000),
        })
        .unwrap();

    let a_id = store.event_row_id_by_event_id(&a.event_id).unwrap().unwrap();
    let b_id = store.event_row_id_by_event_id(&b.event_id).unwrap().unwrap();

    // Touch updated_at / soft-delete simulation on A — must not extend expire_at.
    store
        .with_connection(|conn| {
            conn.execute(
                "UPDATE events SET updated_at=?1, delete_at=?1 WHERE id=?2",
                rusqlite::params![base + 100_000, a_id],
            )?;
            Ok(())
        })
        .unwrap();

    // Jump past A's expire_at but before B's.
    store.set_clock_ms(base + EVENT_TTL_MS + 1);
    let deleted = store.expire_due_events(10).unwrap();
    assert_eq!(deleted, 1);
    assert!(store.event_row_id_by_event_id(&a.event_id).unwrap().is_none());
    assert!(store.event_row_id_by_event_id(&b.event_id).unwrap().is_some());
    assert_eq!(store.expired_incomplete_count(source_id).unwrap(), 1);

    let b_status = store.event_status_json(b_id).unwrap();
    assert!(b_status.contains(r#""hour":5"#));
    assert!(b_status.contains(r#""day":5"#));
    assert!(b_status.contains(r#""month":5"#));
    // Upload is independent of local metric dependency blocking.
    assert!(b_status.contains(r#""upload":0"#));

    let blocked_code: Option<String> = store
        .with_connection(|conn| {
            Ok(conn
                .query_row(
                    "SELECT last_error_code FROM processing_tasks
                     WHERE event_row_id=?1 AND consumer='day'",
                    rusqlite::params![b_id],
                    |r| r.get(0),
                )
                .ok())
        })
        .unwrap();
    assert_eq!(blocked_code.as_deref(), Some("dependency_expired"));

    // Cursor/metrics untouched: cursor still at last commit; no metric rows required for P1.
    assert_eq!(
        store.source_cursor_json(source_id).unwrap(),
        r#"{"offset":2}"#
    );

    let _ = (a, b);
}

#[test]
fn retry_schedules_runnable_at_without_losing_other_lanes() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    commit_one(
        &mut store,
        source_id,
        &token,
        seq,
        vec![candidate(41, None)],
        r#"{"offset":1}"#,
    );
    let day = store.claim_tasks(Consumer::Day, 1, DEFAULT_LEASE_MS).unwrap();
    let hour = store.claim_tasks(Consumer::Hour, 1, DEFAULT_LEASE_MS).unwrap();
    store
        .complete_task(TaskComplete {
            task_id: hour[0].task_id,
            event_row_id: hour[0].event_row_id,
            consumer: Consumer::Hour,
            lease_token: hour[0].lease_token.clone(),
            status: ConsumerStatus::Applied,
            error_code: None,
        })
        .unwrap();
    store
        .retry_task(TaskRetry {
            task_id: day[0].task_id,
            event_row_id: day[0].event_row_id,
            consumer: Consumer::Day,
            lease_token: day[0].lease_token.clone(),
            runnable_at: store.now_ms() + 10_000,
            error_code: Some("transient".into()),
        })
        .unwrap();
    let status = store.event_status_json(day[0].event_row_id).unwrap();
    assert!(status.contains(r#""hour":3"#));
    assert!(status.contains(r#""day":1"#));
}

#[test]
fn writer_channel_commits_and_runs_compensation() {
    let mut store = open_store();
    let source_id = register_jsonl(&mut store);
    let (token, _, seq) = store.lease_source(source_id, DEFAULT_LEASE_MS).unwrap();
    let writer = PipelineWriter::start(store);
    let result = writer
        .commit_source(SourceCommitBatch {
            source_id,
            expected_commit_seq: seq,
            lease_token: token,
            cursor_json: r#"{"offset":7}"#.into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            ignored_record_count_delta: 0,
            last_ignored_code: None,
            next_poll_at: Some(1),
            events: vec![candidate(51, None)],
            created_at_override: Some(1_700_000_000_000),
        })
        .unwrap();
    assert_eq!(result.inserted_events, 1);
    let leased = writer
        .claim_tasks(Consumer::Upload, 5, DEFAULT_LEASE_MS)
        .unwrap();
    assert_eq!(leased.len(), 1);
    let stats = writer.run_compensation().unwrap();
    assert_eq!(stats.reclaimed_leases, 0);
    writer.shutdown();
}
