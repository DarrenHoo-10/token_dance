//! P3 harness adapter contract tests against real-format fixtures.

use std::path::PathBuf;
use std::sync::atomic::{AtomicI64, Ordering};
use std::sync::Arc;

use serde_json::json;

use super::codex::{decode_otlp_cumulative_for_test, CodexStrategy};
use super::common::SkillBook;
use super::cursor::CursorStrategy;
use super::identity::{fact_key, skill_key, TypedNativeKey};
use super::jsonl_harness::{JsonlHarnessStrategy, CLAUDE};
use super::opencode::OpenCodeStrategy;
use super::registry::{AdapterRoots, HarnessRegistry};
use super::zcode::{ZcodeStrategy, STREAM_USAGE};
use super::{capability, for_harness, should_throttle};
use crate::local_store::pipeline::runner::{
    beijing_wall_to_utc_ms, run_source_once, CheckpointView, DecodeOutcome, DecoderState,
    DiscoveryBudget, HarnessStrategy, IgnoreCode, RawRecord, ReadBudget, RunOutcome, StoreSinkMut,
    TimeSource, DEFAULT_READ_BUDGET,
};
use crate::local_store::pipeline::types::{
    CursorKind, RegisterSource, SourceKind, DEFAULT_LEASE_MS,
};
use crate::local_store::pipeline::PipelineStore;

fn secret() -> Vec<u8> {
    b"p3-test-identity-secret".to_vec()
}

fn skill_allocator(book: &SkillBook, store: Arc<std::sync::Mutex<PipelineStore>>) -> Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync> {
    let book = book.clone();
    Arc::new(move |key, name| {
        if let Some(id) = book.get(&key) {
            return id;
        }
        let mut guard = store.lock().expect("store");
        let id = guard
            .register_skill(&key, Some(name))
            .expect("register skill");
        book.upsert(key, id);
        id
    })
}

fn open_store(now: i64) -> PipelineStore {
    let mut store = PipelineStore::open_in_memory().expect("open");
    store.set_clock_ms(now);
    store
}

fn register_source(
    store: &mut PipelineStore,
    harness: &str,
    locator: &str,
    stream: &str,
    kind: SourceKind,
    cursor: &str,
    decoder: &str,
    now: i64,
) -> i64 {
    store
        .register_source(&RegisterSource {
            harness_id: harness.into(),
            source_key: {
                let mut b = [0u8; 32];
                b[..harness.len().min(32)].copy_from_slice(&harness.as_bytes()[..harness.len().min(32)]);
                b
            },
            source_kind: kind,
            locator_ref: locator.into(),
            stream_key: stream.into(),
            cursor_kind: if kind == SourceKind::Sqlite {
                CursorKind::SqliteChange
            } else {
                CursorKind::ByteOffset
            },
            cursor_json: cursor.into(),
            decoder_state_version: 1,
            decoder_state_json: decoder.into(),
            observed_boundary_json: if kind == SourceKind::Jsonl {
                r#"{"len":0}"#.into()
            } else {
                "{}".into()
            },
            next_poll_at: Some(now),
        })
        .expect("register")
}

fn count_events(store: &PipelineStore) -> i64 {
    store.event_count().unwrap_or(0)
}

fn event_ids(store: &mut PipelineStore) -> Vec<Vec<u8>> {
    store
        .with_connection(|conn| {
            let mut stmt = conn
                .prepare("SELECT event_id FROM events ORDER BY id")
                .map_err(|e| crate::local_store::pipeline::PipelineError::Sqlite(e.to_string()))?;
            let rows = stmt
                .query_map([], |r| r.get::<_, Vec<u8>>(0))
                .map_err(|e| crate::local_store::pipeline::PipelineError::Sqlite(e.to_string()))?;
            Ok(rows.map(|r| r.unwrap()).collect())
        })
        .unwrap()
}

fn exec_sql(store: &mut PipelineStore, sql: &str, params: &[&dyn rusqlite::types::ToSql]) {
    store
        .with_connection(|conn| {
            conn.execute(sql, params)
                .map_err(|e| crate::local_store::pipeline::PipelineError::Sqlite(e.to_string()))?;
            Ok(())
        })
        .unwrap();
}

#[test]
fn capability_matrix_throttles_unavailable_streams() {
    assert!(should_throttle("cursor", "remote-api-aggregate"));
    assert!(should_throttle("grok-build", "chat-history"));
    assert!(!should_throttle("codex", "sessions-jsonl"));
    assert_eq!(for_harness("zcode").unwrap().streams.len(), 4);
    assert_eq!(capability::ALL.len(), 10);
}

#[test]
fn registry_covers_all_ten_harnesses() {
    let dir = tempfile::tempdir().unwrap();
    let store = Arc::new(std::sync::Mutex::new(open_store(1_700_000_000_000)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store);
    let roots = AdapterRoots {
        identity_secret: secret(),
        codex_sessions: dir.path().join("codex"),
        claude_projects: dir.path().join("claude"),
        cursor_transcripts: dir.path().join("cursor"),
        zcode_db: dir.path().join("zcode.sqlite"),
        opencode_db: dir.path().join("opencode.sqlite"),
        grok_updates: dir.path().join("grok"),
        deepseek_sessions: dir.path().join("deepseek"),
        pi_sessions: dir.path().join("pi"),
        workbuddy_history: dir.path().join("workbuddy"),
        doubao_history: dir.path().join("doubao"),
    };
    let reg = HarnessRegistry::from_roots(roots, alloc);
    assert_eq!(reg.harness_ids().len(), 10);
}

#[test]
fn numeric_rowid_identity_differs_from_string() {
    let a = fact_key(
        &secret(),
        "zcode",
        STREAM_USAGE,
        &TypedNativeKey::I64(42),
        "model_usage_recorded",
    );
    let b = fact_key(
        &secret(),
        "zcode",
        STREAM_USAGE,
        &TypedNativeKey::Str("42".into()),
        "model_usage_recorded",
    );
    assert_ne!(a, b);
}

#[test]
fn codex_jsonl_double_scan_stable_event_ids() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 15, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("session.jsonl");
    let ts = now - 60_000;
    std::fs::write(
        &path,
        format!(
            "{}\n{}\n",
            json!({"type":"token.usage","timestamp":ts,"thread_id":"t1","turn_id":"u1","input_tokens":10,"output_tokens":5,"total_tokens":15}),
            json!({"type":"token.usage","timestamp":ts+1000,"thread_id":"t1","turn_id":"u2","input_tokens":20,"output_tokens":8,"total_tokens":28}),
        ),
    )
    .unwrap();

    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = CodexStrategy::new(secret(), dir.path(), book, alloc);

    let mut store = store_arc.lock().unwrap();
    let source_id = register_source(
        &mut store,
        "codex",
        path.to_str().unwrap(),
        "sessions-jsonl",
        SourceKind::Jsonl,
        r#"{"offset":0}"#,
        r#"{"last_source_time":null}"#,
        now,
    );

    let sink = StoreSinkMut::new(&mut store);
    let budget = DEFAULT_READ_BUDGET;
    let first = run_source_once(
        &sink,
        &strategy,
        source_id,
        budget,
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(first, RunOutcome::Committed(_)));
    let ids1 = event_ids(&mut store);
    assert_eq!(ids1.len(), 2);

    // Reset cursor to rescan same bytes.
    exec_sql(
        &mut store,
        "UPDATE collection_sources SET cursor_json=?1, observed_boundary_json=?2, decoder_state_json=?3",
        &[
            &r#"{"offset":0}"#,
            &r#"{"len":0}"#,
            &r#"{"last_source_time":null}"#,
        ],
    );
    let sink = StoreSinkMut::new(&mut store);
    let second = run_source_once(
        &sink,
        &strategy,
        source_id,
        budget,
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(second, RunOutcome::Committed(_)));
    let ids2 = event_ids(&mut store);
    assert_eq!(ids2.len(), 2, "rescan must not double-count");
    assert_eq!(ids1, ids2);
}

#[test]
fn codex_otlp_cumulative_baselines_without_request_delta() {
    let strategy = CodexStrategy::new(
        secret(),
        PathBuf::from("/tmp"),
        SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    let mut state = DecoderState {
        version: 1,
        json: json!({}),
    };
    let first = decode_otlp_cumulative_for_test(&strategy, 1000, "series-a", &mut state);
    match &first {
        DecodeOutcome::Ignore(IgnoreCode::Other(c)) => {
            assert_eq!(c, "cumulative_baseline_only");
        }
        DecodeOutcome::Ignore(IgnoreCode::EstimatedOnly) => {}
        other => panic!("expected ignore baseline, got {other:?}"),
    }
    let second = decode_otlp_cumulative_for_test(&strategy, 1000, "series-a", &mut state);
    assert!(matches!(second, DecodeOutcome::Ignore(_)));
}

#[test]
fn zcode_late_small_rowid_not_lost_when_large_completes_first() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 16, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("zcode.sqlite");
    {
        let conn = rusqlite::Connection::open(&path).unwrap();
        conn.execute_batch(
            "CREATE TABLE model_usage(
                rowid INTEGER PRIMARY KEY,
                session_id TEXT,
                provider_id TEXT,
                model_id TEXT,
                input_tokens INTEGER,
                output_tokens INTEGER,
                computed_total_tokens INTEGER,
                status TEXT,
                completed_at INTEGER
             );
             INSERT INTO model_usage VALUES
               (1,'s1','zai','m',10,5,15,'running',NULL),
               (2,'s1','zai','m',20,8,28,'completed',1000);",
        )
        .unwrap();
    }

    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = ZcodeStrategy::new(secret(), &path, book, alloc);

    // Patch timestamps to today so admission accepts.
    let today = now - 30_000;
    {
        let conn = rusqlite::Connection::open(&path).unwrap();
        conn.execute(
            "UPDATE model_usage SET completed_at=?1 WHERE rowid=2",
            rusqlite::params![today],
        )
        .unwrap();
    }

    let mut store = store_arc.lock().unwrap();
    let specs = strategy.discover(DiscoveryBudget::new(8, 200)).unwrap();
    let usage_spec = specs
        .iter()
        .find(|s| s.stream_key == STREAM_USAGE)
        .unwrap();
    let source_id = register_source(
        &mut store,
        "zcode",
        path.to_str().unwrap(),
        STREAM_USAGE,
        SourceKind::Sqlite,
        &usage_spec.initial_cursor_json.to_string(),
        r#"{"last_source_time":null}"#,
        now,
    );

    let sink = StoreSinkMut::new(&mut store);
    let first = run_source_once(
        &sink,
        &strategy,
        source_id,
        ReadBudget::new(32, 1024 * 1024, 5_000),
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(first, RunOutcome::Committed(_)));
    assert_eq!(count_events(&store), 1, "only completed rowid=2");

    // Complete the late small rowid.
    {
        let conn = rusqlite::Connection::open(&path).unwrap();
        conn.execute(
            "UPDATE model_usage SET status='completed', completed_at=?1 WHERE rowid=1",
            rusqlite::params![today + 1000],
        )
        .unwrap();
    }
    let sink = StoreSinkMut::new(&mut store);
    let second = run_source_once(
        &sink,
        &strategy,
        source_id,
        ReadBudget::new(32, 1024 * 1024, 5_000),
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(second, RunOutcome::Committed(_)));
    assert_eq!(count_events(&store), 2, "rowid=1 must still be collected");
}

#[test]
fn zcode_same_time_updated_multi_rows_not_skipped() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 16, 30, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("zcode.sqlite");
    let ts = now - 10_000;
    {
        let conn = rusqlite::Connection::open(&path).unwrap();
        conn.execute_batch(&format!(
            "CREATE TABLE session(id TEXT PRIMARY KEY, time_created INTEGER);
             CREATE TABLE model_usage(
                rowid INTEGER PRIMARY KEY, session_id TEXT, provider_id TEXT, model_id TEXT,
                input_tokens INTEGER, output_tokens INTEGER, computed_total_tokens INTEGER,
                status TEXT, completed_at INTEGER);
             CREATE TABLE part(rowid INTEGER PRIMARY KEY, session_id TEXT, time_updated INTEGER, data TEXT);
             INSERT INTO part VALUES
               (1,'s',{ts},'{{\"callID\":\"c1\",\"type\":\"tool\"}}'),
               (2,'s',{ts},'{{\"callID\":\"c2\",\"type\":\"tool\"}}'),
               (3,'s',{ts},'{{\"callID\":\"c3\",\"type\":\"tool\"}}');"
        ))
        .unwrap();
    }

    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = ZcodeStrategy::new(secret(), &path, book, alloc);
    let mut store = store_arc.lock().unwrap();
    let specs = strategy.discover(DiscoveryBudget::new(8, 200)).unwrap();
    let code_spec = specs
        .iter()
        .find(|s| s.stream_key == "sqlite/code_part")
        .unwrap();
    let source_id = register_source(
        &mut store,
        "zcode",
        path.to_str().unwrap(),
        "sqlite/code_part",
        SourceKind::Sqlite,
        &code_spec.initial_cursor_json.to_string(),
        r#"{"last_source_time":null}"#,
        now,
    );
    let sink = StoreSinkMut::new(&mut store);
    let outcome = run_source_once(
        &sink,
        &strategy,
        source_id,
        ReadBudget::new(32, 1024 * 1024, 5_000),
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(outcome, RunOutcome::Committed(_)));
    assert_eq!(count_events(&store), 3);
}

#[test]
fn cursor_missing_timestamp_uses_previous_or_mtime() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 12, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("t.jsonl");
    std::fs::write(
        &path,
        format!(
            "{}\n{}\n",
            json!({"type":"assistant","timestamp":now-5000,"sessionId":"s","tokenCount":11}),
            json!({"type":"assistant","sessionId":"s","tokenCount":22}),
        ),
    )
    .unwrap();
    // Ensure mtime is usable as last resort for files with no prior context —
    // second line inherits previous_record from first.
    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = CursorStrategy::new(secret(), dir.path(), book, alloc);
    let mut store = store_arc.lock().unwrap();
    let source_id = register_source(
        &mut store,
        "cursor",
        path.to_str().unwrap(),
        "transcript-jsonl",
        SourceKind::Jsonl,
        r#"{"offset":0}"#,
        r#"{"last_source_time":null}"#,
        now,
    );
    let sink = StoreSinkMut::new(&mut store);
    let outcome = run_source_once(
        &sink,
        &strategy,
        source_id,
        DEFAULT_READ_BUDGET,
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(outcome, RunOutcome::Committed(ref s) if s.emitted == 2));

    // Aggregate must not become request detail.
    let mut state = DecoderState {
        version: 1,
        json: json!({}),
    };
    let agg = RawRecord {
        ordinal: 0,
        byte_start: Some(0),
        byte_end: Some(10),
        native_rowid: None,
        payload: json!({"api_aggregate":true,"totalTokens":999}).to_string().into_bytes(),
        file_mtime_ms: Some(now),
    };
    let out = strategy.decode(&agg, &mut state).unwrap();
    assert!(matches!(out, DecodeOutcome::Ignore(IgnoreCode::EstimatedOnly)));
}

#[test]
fn skill_same_key_shared_across_harnesses() {
    let now = 1_700_000_000_000i64;
    let mut store = open_store(now);
    let book = SkillBook::new();
    let key = skill_key(&secret(), "shared-skill");
    let id1 = store.register_skill(&key, Some("Shared")).unwrap();
    book.upsert(key, id1);
    let id2 = store.register_skill(&key, Some("Other Label")).unwrap();
    assert_eq!(id1, id2);
    let count: i64 = store
        .with_connection(|conn| {
            conn.query_row("SELECT COUNT(*) FROM skill_dimensions", [], |r| r.get(0))
                .map_err(|e| crate::local_store::pipeline::PipelineError::Sqlite(e.to_string()))
        })
        .unwrap();
    assert_eq!(count, 1);

    // Emit skill facts from two harness strategies into the same book.
    let store_arc = Arc::new(std::sync::Mutex::new(store));
    let alloc = skill_allocator(&book, store_arc.clone());
    let dir = tempfile::tempdir().unwrap();
    let claude = JsonlHarnessStrategy::new(CLAUDE, secret(), dir.path(), book.clone(), alloc.clone());
    let zcode = ZcodeStrategy::new(secret(), dir.path().join("missing.sqlite"), book.clone(), alloc);

    let mut state = DecoderState {
        version: 1,
        json: json!({}),
    };
    let rec = RawRecord {
        ordinal: 0,
        byte_start: Some(0),
        byte_end: Some(50),
        native_rowid: None,
        payload: json!({
            "type":"skill",
            "timestamp": now,
            "sessionId":"s",
            "skillName":"shared-skill",
            "success": true
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let a = claude.decode(&rec, &mut state).unwrap();
    let DecodeOutcome::Emit(facts_a) = a else {
        panic!("expected emit");
    };
    assert_eq!(facts_a[0].skill_id, Some(id1));

    // zcode path uses same book — skill id stays shared.
    let mut state2 = DecoderState {
        version: 1,
        json: json!({}),
    };
    let rec2 = RawRecord {
        ordinal: 0,
        byte_start: None,
        byte_end: None,
        native_rowid: Some(9),
        payload: json!({
            "type":"skill",
            "timestamp": now,
            "sessionId":"s",
            "skillName":"shared-skill",
            "success": true,
            "_rowid": 9,
            "_status":"completed"
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let b = zcode.decode(&rec2, &mut state2).unwrap();
    let DecodeOutcome::Emit(facts_b) = b else {
        panic!("expected emit");
    };
    assert_eq!(facts_b[0].skill_id, Some(id1));
    let _ = zcode; // silence
}

#[test]
fn opencode_numeric_step_ids_stable_across_polls() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 14, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("opencode.sqlite");
    let ts = now - 20_000;
    {
        let conn = rusqlite::Connection::open(&path).unwrap();
        conn.execute_batch(&format!(
            "CREATE TABLE session(id TEXT PRIMARY KEY, agent TEXT, model TEXT, time_created INTEGER);
             CREATE TABLE part(rowid INTEGER PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
             INSERT INTO session VALUES ('s1','a','m',{ts});
             INSERT INTO part(rowid,session_id,time_created,time_updated,data) VALUES
               (42,'s1',{ts},{ts},'{{\"type\":\"step-finish\",\"tokens\":{{\"input\":3,\"output\":4}}}}'),
               (43,'s1',{ts},{ts},'{{\"type\":\"step-finish\",\"tokens\":{{\"input\":5,\"output\":6}}}}');"
        ))
        .unwrap();
    }
    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = OpenCodeStrategy::new(secret(), &path, book, alloc);
    let mut store = store_arc.lock().unwrap();
    let specs = strategy.discover(DiscoveryBudget::new(8, 200)).unwrap();
    let step = specs
        .iter()
        .find(|s| s.stream_key == "sqlite/step_finish")
        .unwrap();
    let source_id = register_source(
        &mut store,
        "opencode",
        path.to_str().unwrap(),
        "sqlite/step_finish",
        SourceKind::Sqlite,
        &step.initial_cursor_json.to_string(),
        r#"{"last_source_time":null}"#,
        now,
    );
    let sink = StoreSinkMut::new(&mut store);
    let first = run_source_once(
        &sink,
        &strategy,
        source_id,
        DEFAULT_READ_BUDGET,
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(first, RunOutcome::Committed(_)));
    let ids = event_ids(&mut store);
    assert_eq!(ids.len(), 2);

    // Rescan from zero — idempotent.
    let cursor_reset = step.initial_cursor_json.to_string();
    exec_sql(
        &mut store,
        "UPDATE collection_sources SET cursor_json=?1, decoder_state_json=?2",
        &[&cursor_reset.as_str(), &r#"{"last_source_time":null}"#],
    );
    let sink = StoreSinkMut::new(&mut store);
    let second = run_source_once(
        &sink,
        &strategy,
        source_id,
        DEFAULT_READ_BUDGET,
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    assert!(matches!(second, RunOutcome::Committed(_)));
    assert_eq!(event_ids(&mut store), ids);
}

#[test]
fn cross_midnight_first_seen_ignored_but_baseline_advances() {
    // Admission day = Sep 12; record on Sep 11 → IgnoreOutsideDay at runner.
    let admission = beijing_wall_to_utc_ms(2026, 9, 12, 0, 30, 0);
    let yesterday = beijing_wall_to_utc_ms(2026, 9, 11, 23, 50, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("s.jsonl");
    std::fs::write(
        &path,
        format!(
            "{}\n",
            json!({"type":"token.usage","timestamp":yesterday,"thread_id":"t","turn_id":"old","input_tokens":9,"output_tokens":1,"total_tokens":10}),
        ),
    )
    .unwrap();
    let store_arc = Arc::new(std::sync::Mutex::new(open_store(admission)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = CodexStrategy::new(secret(), dir.path(), book, alloc);
    let mut store = store_arc.lock().unwrap();
    let source_id = register_source(
        &mut store,
        "codex",
        path.to_str().unwrap(),
        "sessions-jsonl",
        SourceKind::Jsonl,
        r#"{"offset":0}"#,
        r#"{"last_source_time":null}"#,
        admission,
    );
    let sink = StoreSinkMut::new(&mut store);
    let outcome = run_source_once(
        &sink,
        &strategy,
        source_id,
        DEFAULT_READ_BUDGET,
        DEFAULT_LEASE_MS,
        &[],
        None,
    )
    .unwrap();
    match outcome {
        RunOutcome::Committed(stats) => {
            assert_eq!(stats.emitted, 0);
            assert!(stats.ignored >= 1);
            assert_eq!(
                stats.last_ignored_code.as_deref(),
                Some("outside_admission_day")
            );
        }
        other => panic!("unexpected {other:?}"),
    }
    assert_eq!(count_events(&store), 0);
    let cursor = store.source_cursor_json(source_id).unwrap();
    let offset: u64 = serde_json::from_str::<serde_json::Value>(&cursor)
        .unwrap()["offset"]
        .as_u64()
        .unwrap();
    assert!(offset > 0);
}

// Silence unused import warnings in some cfg combinations.
#[allow(dead_code)]
fn _touch() {
    let _ = AtomicI64::new(0).load(Ordering::Relaxed);
    let _ = TimeSource::SourceRecord;
    let _ = CheckpointView {
        cursor_json: json!({}),
        decoder_state_version: 1,
        decoder_state_json: json!({}),
        observed_boundary_json: json!({}),
        commit_seq: 0,
    };
}
