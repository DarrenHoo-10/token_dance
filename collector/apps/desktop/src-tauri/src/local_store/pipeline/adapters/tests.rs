//! P3 harness adapter contract tests against real-format fixtures.

#[test]
fn review3_codex_multiple_roots_are_all_discovered() {
    let dir = tempfile::tempdir().unwrap();
    let roots = ["archived_sessions", "sessions"].map(|name| {
        let root = dir.path().join(name);
        std::fs::create_dir_all(&root).unwrap();
        for i in 0..40 {
            std::fs::write(root.join(format!("s{i:03}.jsonl")), "{}\n").unwrap();
        }
        (name.to_string(), root)
    });
    let strategy = CodexStrategy::with_roots(
        secret(),
        roots.to_vec(),
        SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    for limit in [1, 31, 64] {
        let mut resume = None;
        let mut seen = std::collections::HashSet::new();
        for _ in 0..(80 + limit - 1) / limit + 1 {
            let specs = strategy
                .discover(DiscoveryBudget::new(limit, 200).with_resume(resume))
                .unwrap();
            assert!(specs.len() <= limit);
            resume = specs.last().map(|s| s.locator_ref.clone());
            seen.extend(specs.into_iter().map(|s| s.locator_ref));
        }
        assert_eq!(
            seen.len(),
            80,
            "all roots must make progress with page size {limit}"
        );
    }
}

#[test]
fn review3_codex_archive_move_preserves_event_identity() {
    let now = beijing_wall_to_utc_ms(2026, 9, 12, 12, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let sessions = dir.path().join("sessions");
    let archive = dir.path().join("archived_sessions");
    std::fs::create_dir_all(&sessions).unwrap();
    std::fs::create_dir_all(&archive).unwrap();
    let live_file = sessions.join("rollout-session-42.jsonl");
    let archived_file = archive.join("rollout-session-42.jsonl");
    let record = json!({
        "type":"event_msg", "timestamp":now,
        "payload":{"type":"token_count","info":{
            "last_token_usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}
        }}
    });
    let header = json!({"type":"session_meta","timestamp":now,"payload":{"id":"session-42"}});
    std::fs::write(&live_file, format!("{header}\n{record}\n")).unwrap();
    let strategy = CodexStrategy::with_roots(
        secret(),
        vec![
            ("codex-archived-sessions".into(), archive),
            ("codex-sessions".into(), sessions),
        ],
        SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    let mut store = open_store(now);
    for pass in 0..3 {
        if pass == 1 {
            std::fs::rename(&live_file, &archived_file).unwrap();
        }
        if pass == 2 {
            std::fs::rename(&archived_file, &live_file).unwrap();
        }
        for spec in strategy.discover(DiscoveryBudget::default()).unwrap() {
            let id = register_source(
                &mut store,
                "codex",
                &spec.locator_ref,
                &spec.stream_key,
                SourceKind::Jsonl,
                r#"{"offset":0}"#,
                r#"{"last_source_time":null}"#,
                now,
            );
            let sink = StoreSinkMut::new(&mut store);
            if pass == 0 {
                // Commit just the native header, then resume from persisted decoder state.
                run_source_once(
                    &sink,
                    &strategy,
                    id,
                    ReadBudget::new(1, 1024 * 1024, 200),
                    DEFAULT_LEASE_MS,
                    &Consumer::ALL,
                    None,
                )
                .unwrap();
            }
            let result = run_source_once(
                &sink,
                &strategy,
                id,
                DEFAULT_READ_BUDGET,
                DEFAULT_LEASE_MS,
                &Consumer::ALL,
                None,
            )
            .unwrap();
            if pass == 2 {
                assert!(
                    matches!(result, RunOutcome::Empty(_)),
                    "unarchive must resume the original cursor: {result:?}"
                );
            } else {
                assert!(matches!(result, RunOutcome::Committed(_)), "{result:?}");
            }
        }
    }
    assert_eq!(
        store.event_count().unwrap(),
        1,
        "archiving the same already-collected session must not duplicate usage"
    );
}

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
    beijing_wall_to_utc_ms, run_source_once, AcquisitionRunner, CheckpointView, DecodeOutcome,
    DecoderState, DiscoveryBudget, HarnessStrategy, IgnoreCode, RawRecord, ReadBudget, RunOutcome,
    StoreSinkMut, TimeSource, DEFAULT_READ_BUDGET,
};
use crate::local_store::pipeline::types::{
    Consumer, CursorKind, RegisterSource, SourceKind, DEFAULT_LEASE_MS,
};
use crate::local_store::pipeline::PipelineStore;

fn secret() -> Vec<u8> {
    b"p3-test-identity-secret".to_vec()
}

fn skill_allocator(
    book: &SkillBook,
    store: Arc<std::sync::Mutex<PipelineStore>>,
) -> Arc<dyn Fn([u8; 32], &str) -> i64 + Send + Sync> {
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
            source_key: crate::local_store::pipeline::adapters::source_key(
                &secret(),
                harness,
                locator,
            ),
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
        cursor_usage: None,
        identity_secret: secret(),
        codex_roots: vec![("codex-sessions".into(), dir.path().join("codex"))],
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
    let usage_spec = specs.iter().find(|s| s.stream_key == STREAM_USAGE).unwrap();
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
        payload: json!({"api_aggregate":true,"totalTokens":999})
            .to_string()
            .into_bytes(),
        file_mtime_ms: Some(now),
    };
    let out = strategy
        .decode(&agg, &mut state, path.to_str().unwrap())
        .unwrap();
    assert!(matches!(
        out,
        DecodeOutcome::Ignore(IgnoreCode::EstimatedOnly)
    ));
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
    let claude =
        JsonlHarnessStrategy::new(CLAUDE, secret(), dir.path(), book.clone(), alloc.clone());
    let zcode = ZcodeStrategy::new(
        secret(),
        dir.path().join("missing.sqlite"),
        book.clone(),
        alloc,
    );

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
    let a = claude.decode(&rec, &mut state, "claude-fixture").unwrap();
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
    let b = zcode.decode(&rec2, &mut state2, "zcode-fixture").unwrap();
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
    let offset: u64 = serde_json::from_str::<serde_json::Value>(&cursor).unwrap()["offset"]
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

#[test]
fn review_content_hash_is_p0_sha256_not_hmac() {
    use crate::local_store::pipeline::adapters::identity::content_hash as hmac_content_hash;
    use crate::upload_pipeline::encode_wire_event;

    let now = beijing_wall_to_utc_ms(2026, 9, 11, 15, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("session.jsonl");
    std::fs::write(
        &path,
        format!(
            "{}\n",
            json!({"type":"token.usage","timestamp":now-1_000,"thread_id":"t1","turn_id":"u1","input_tokens":10,"output_tokens":5,"total_tokens":15}),
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
    let outcome = run_source_once(
        &sink,
        &strategy,
        source_id,
        DEFAULT_READ_BUDGET,
        DEFAULT_LEASE_MS,
        &Consumer::ALL,
        None,
    )
    .unwrap();
    assert!(matches!(outcome, RunOutcome::Committed(_)));

    let row_id: i64 = store
        .with_connection(|conn| {
            conn.query_row("SELECT id FROM events LIMIT 1", [], |r| r.get(0))
                .map_err(|e| crate::local_store::pipeline::PipelineError::Sqlite(e.to_string()))
        })
        .unwrap();
    let rows = store.load_upload_events(&[row_id]).unwrap();
    assert_eq!(rows.len(), 1);
    let envelope = encode_wire_event(&rows[0]).unwrap();
    let mut wire = serde_json::to_value(&envelope).unwrap();
    wire.as_object_mut().unwrap().remove("contentHash");
    let recomputed = protocol::v2::compute_content_hash(&wire).unwrap();
    assert_eq!(
        recomputed, envelope.content_hash,
        "upload must pass through the same P0 hash frozen at ingest"
    );

    let hmac = hmac_content_hash(&secret(), &[b"not", b"p0"]);
    assert_ne!(
        rows[0].content_hash, hmac,
        "stored hash must not be adapter HMAC"
    );
}

#[test]
fn review_dual_jsonl_same_offset_distinct_event_ids() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 15, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let line = json!({"type":"token.usage","timestamp":now-1_000,"thread_id":"t","turn_id":"same","input_tokens":3,"output_tokens":4,"total_tokens":7}).to_string();
    let a = dir.path().join("a.jsonl");
    let b = dir.path().join("b.jsonl");
    std::fs::write(&a, format!("{line}\n")).unwrap();
    std::fs::write(&b, format!("{line}\n")).unwrap();

    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    let strategy = CodexStrategy::new(secret(), dir.path(), book, alloc);
    let mut store = store_arc.lock().unwrap();
    let id_a = register_source(
        &mut store,
        "codex",
        a.to_str().unwrap(),
        "sessions-jsonl",
        SourceKind::Jsonl,
        r#"{"offset":0}"#,
        r#"{"last_source_time":null}"#,
        now,
    );
    let id_b = register_source(
        &mut store,
        "codex",
        b.to_str().unwrap(),
        "sessions-jsonl",
        SourceKind::Jsonl,
        r#"{"offset":0}"#,
        r#"{"last_source_time":null}"#,
        now,
    );
    for sid in [id_a, id_b] {
        let sink = StoreSinkMut::new(&mut store);
        let out = run_source_once(
            &sink,
            &strategy,
            sid,
            DEFAULT_READ_BUDGET,
            DEFAULT_LEASE_MS,
            &[],
            None,
        )
        .unwrap();
        assert!(matches!(out, RunOutcome::Committed(ref s) if s.emitted == 1));
    }
    let ids = event_ids(&mut store);
    assert_eq!(ids.len(), 2);
    assert_ne!(ids[0], ids[1], "same offset across files must not collide");
}

#[test]
fn review_dual_sqlite_same_rowid_distinct_event_ids() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 16, 0, 0);
    let dir = tempfile::tempdir().unwrap();
    let ts = now - 20_000;
    let mk_db = |name: &str| {
        let path = dir.path().join(name);
        let conn = rusqlite::Connection::open(&path).unwrap();
        conn.execute_batch(&format!(
            "CREATE TABLE session(id TEXT PRIMARY KEY, agent TEXT, model TEXT, time_created INTEGER);
             CREATE TABLE part(rowid INTEGER PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT);
             INSERT INTO session VALUES ('s1','a','m',{ts});
             INSERT INTO part(rowid,session_id,time_created,time_updated,data) VALUES
               (42,'s1',{ts},{ts},'{{\"type\":\"step-finish\",\"tokens\":{{\"input\":3,\"output\":4}}}}');"
        ))
        .unwrap();
        path
    };
    let db_a = mk_db("a.sqlite");
    let db_b = mk_db("b.sqlite");

    let store_arc = Arc::new(std::sync::Mutex::new(open_store(now)));
    let book = SkillBook::new();
    let alloc = skill_allocator(&book, store_arc.clone());
    for path in [db_a, db_b] {
        let strategy = OpenCodeStrategy::new(secret(), &path, book.clone(), alloc.clone());
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
        let out = run_source_once(
            &sink,
            &strategy,
            source_id,
            DEFAULT_READ_BUDGET,
            DEFAULT_LEASE_MS,
            &[],
            None,
        )
        .unwrap();
        assert!(matches!(out, RunOutcome::Committed(ref s) if s.emitted == 1));
    }
    let mut store = store_arc.lock().unwrap();
    let ids = event_ids(&mut store);
    assert_eq!(ids.len(), 2);
    assert_ne!(
        ids[0], ids[1],
        "same rowid across sqlite files must not collide"
    );
}

#[test]
fn review_codex_native_token_count_last_and_total_semantics() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 12, 0, 0);
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

    // First total_token_usage only: baseline, no request.
    let baseline = RawRecord {
        ordinal: 0,
        byte_start: Some(0),
        byte_end: Some(10),
        native_rowid: None,
        payload: json!({
            "type":"event_msg",
            "timestamp": now - 5_000,
            "payload":{"type":"token_count","info":{
                "total_token_usage":{"input_tokens":100,"output_tokens":20,"total_tokens":120}
            }}
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let out = strategy
        .decode(&baseline, &mut state, "/tmp/a.jsonl")
        .unwrap();
    assert!(
        matches!(out, DecodeOutcome::ContextOnly | DecodeOutcome::Ignore(_)),
        "first cumulative total must baseline without inventing a request: {out:?}"
    );

    // last_token_usage emits exact request usage.
    let last = RawRecord {
        ordinal: 1,
        byte_start: Some(100),
        byte_end: Some(200),
        native_rowid: None,
        payload: json!({
            "type":"event_msg",
            "timestamp": now - 4_000,
            "thread_id":"t1",
            "payload":{"type":"token_count","info":{
                "last_token_usage":{"input_tokens":10,"cached_input_tokens":2,"output_tokens":5,"total_tokens":15},
                "total_token_usage":{"input_tokens":110,"output_tokens":25,"total_tokens":135}
            }}
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let out = strategy.decode(&last, &mut state, "/tmp/a.jsonl").unwrap();
    let DecodeOutcome::Emit(facts) = out else {
        panic!("expected emit from last_token_usage, got {out:?}");
    };
    assert_eq!(facts.len(), 1);
    assert_eq!(facts[0].event_type, "model_usage_recorded");
    let usage = &facts[0].payload_sections["usage"];
    assert_eq!(usage["token_total"], 15);
    assert_eq!(usage["input_context_tokens"], 10);
    assert_eq!(usage["output_tokens"], 5);
    assert_eq!(usage["cache_read_tokens"], 2);

    // total-only positive delta after baseline emits derived delta when last is absent.
    let delta_only = RawRecord {
        ordinal: 2,
        byte_start: Some(300),
        byte_end: Some(400),
        native_rowid: None,
        payload: json!({
            "type":"event_msg",
            "timestamp": now - 3_000,
            "thread_id":"t1",
            "payload":{"type":"token_count","info":{
                "total_token_usage":{"input_tokens":150,"output_tokens":30,"total_tokens":180}
            }}
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let out = strategy
        .decode(&delta_only, &mut state, "/tmp/a.jsonl")
        .unwrap();
    let DecodeOutcome::Emit(facts) = out else {
        panic!("expected cumulative delta emit, got {out:?}");
    };
    assert_eq!(facts[0].payload_sections["usage"]["token_total"], 45);
}

#[test]
fn review_pipeline_runtime_raw_to_metrics_to_upload_pending() {
    use crate::local_store::pipeline::adapters::HarnessRegistry;
    use crate::local_store::pipeline::runtime::{adapter_roots_for_fixture, PipelineRuntime};
    use crate::local_store::pipeline::{Consumer, PipelineWriter};

    // Writer sink uses wall-clock admission; keep fixture on "today" (Beijing).
    let now = beijing_wall_to_utc_ms(2026, 9, 12, 15, 30, 0);
    let dir = tempfile::tempdir().unwrap();
    let codex_dir = dir.path().join("codex");
    std::fs::create_dir_all(&codex_dir).unwrap();
    std::fs::write(
        codex_dir.join("s.jsonl"),
        format!(
            "{}\n",
            json!({
                "type":"event_msg",
                "timestamp": now - 2_000,
                "thread_id":"rt1",
                "payload":{"type":"token_count","info":{
                    "last_token_usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}
                }}
            }),
        ),
    )
    .unwrap();

    // Writer timestamps use the wall clock, so task claiming must use it too.
    let store = PipelineStore::open_in_memory().unwrap();
    let writer = Arc::new(PipelineWriter::start(store));
    let roots = adapter_roots_for_fixture(secret(), dir.path());
    let writer_skills = Arc::clone(&writer);
    let book = SkillBook::new();
    let book2 = book.clone();
    let alloc = Arc::new(move |key, name: &str| {
        if let Some(id) = book2.get(&key) {
            return id;
        }
        let id = writer_skills.register_skill(key, Some(name)).unwrap_or(0);
        book2.upsert(key, id);
        id
    });
    let registry = HarnessRegistry::from_roots(roots, alloc);
    let strategy = registry.get("codex").expect("codex");
    let specs = strategy.discover(DiscoveryBudget::new(8, 200)).unwrap();
    assert!(!specs.is_empty());
    let spec = &specs[0];
    let source_id = writer
        .register_source(crate::local_store::pipeline::types::RegisterSource {
            harness_id: spec.harness_id.clone(),
            source_key: spec.source_key,
            source_kind: spec.source_kind,
            locator_ref: spec.locator_ref.clone(),
            stream_key: spec.stream_key.clone(),
            cursor_kind: spec.cursor_kind,
            cursor_json: spec.initial_cursor_json.to_string(),
            decoder_state_version: 1,
            decoder_state_json: spec.initial_decoder_state_json.to_string(),
            observed_boundary_json: spec.observed_boundary_json.to_string(),
            next_poll_at: Some(now),
        })
        .unwrap();

    let runner = AcquisitionRunner::default();
    let outcome = runner
        .run_once(writer.as_ref(), strategy, source_id, DEFAULT_READ_BUDGET)
        .unwrap();
    assert!(
        matches!(outcome, RunOutcome::Committed(ref s) if s.emitted >= 1),
        "unexpected outcome: {outcome:?}"
    );

    for consumer in [Consumer::Hour, Consumer::Day, Consumer::Month] {
        let drained = writer
            .drain_metrics_consumer(consumer, 16, DEFAULT_LEASE_MS)
            .unwrap();
        assert!(drained.applied >= 1, "{consumer:?} should apply");
    }
    let pending = writer.pending_upload_count().unwrap();
    assert!(pending >= 1, "upload lane should have pending work");
    let _ = PipelineRuntime::start;
    drop(writer);
}

#[test]
fn review2_cumulative_token_components_are_differenced() {
    let now = beijing_wall_to_utc_ms(2026, 9, 11, 12, 0, 0);
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

    let baseline = RawRecord {
        ordinal: 0,
        byte_start: Some(0),
        byte_end: Some(10),
        native_rowid: None,
        payload: json!({
            "type":"event_msg",
            "timestamp": now - 5_000,
            "thread_id":"t-cum",
            "payload":{"type":"token_count","info":{
                "total_token_usage":{
                    "input_tokens":100,
                    "output_tokens":20,
                    "total_tokens":120,
                    "cached_input_tokens":10,
                    "reasoning_output_tokens":5
                }
            }}
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let out = strategy
        .decode(&baseline, &mut state, "/tmp/cum.jsonl")
        .unwrap();
    assert!(
        matches!(out, DecodeOutcome::ContextOnly | DecodeOutcome::Ignore(_)),
        "baseline must not invent a request: {out:?}"
    );

    let next = RawRecord {
        ordinal: 1,
        byte_start: Some(100),
        byte_end: Some(200),
        native_rowid: None,
        payload: json!({
            "type":"event_msg",
            "timestamp": now - 4_000,
            "thread_id":"t-cum",
            "payload":{"type":"token_count","info":{
                "total_token_usage":{
                    "input_tokens":150,
                    "output_tokens":30,
                    "total_tokens":180,
                    "cached_input_tokens":14,
                    "reasoning_output_tokens":8
                }
            }}
        })
        .to_string()
        .into_bytes(),
        file_mtime_ms: None,
    };
    let out = strategy
        .decode(&next, &mut state, "/tmp/cum.jsonl")
        .unwrap();
    let DecodeOutcome::Emit(facts) = out else {
        panic!("expected per-field cumulative delta emit, got {out:?}");
    };
    let usage = &facts[0].payload_sections["usage"];
    assert_eq!(usage["input_context_tokens"], 50);
    assert_eq!(usage["output_tokens"], 10);
    assert_eq!(usage["token_total"], 60);
    assert_eq!(usage["cache_read_tokens"], 4);
    assert_eq!(usage["reasoning_tokens"], 3);
}

#[test]
fn review2_codex_keeps_sessions_and_archived_roots() {
    use crate::local_store::pipeline::runtime::adapter_roots_from_detection;
    use collector_service::{DetectedSourceConfig, DetectionSnapshot, OfficialAgent};

    let mut snap = DetectionSnapshot::default();
    // archived sorts before sessions in BTreeMap — must not replace live sessions.
    snap.configure_source(
        OfficialAgent::Codex,
        "codex-archived-sessions",
        DetectedSourceConfig {
            path: Some(PathBuf::from("/tmp/codex/archived_sessions")),
            ..DetectedSourceConfig::default()
        },
    );
    snap.configure_source(
        OfficialAgent::Codex,
        "codex-sessions",
        DetectedSourceConfig {
            path: Some(PathBuf::from("/tmp/codex/sessions")),
            ..DetectedSourceConfig::default()
        },
    );
    let roots = adapter_roots_from_detection(secret(), &snap);
    assert_eq!(roots.codex_roots.len(), 2);
    assert!(roots
        .codex_roots
        .iter()
        .any(|(id, p)| id == "codex-sessions" && p.ends_with("sessions")));
    assert!(roots
        .codex_roots
        .iter()
        .any(|(id, p)| id == "codex-archived-sessions" && p.ends_with("archived_sessions")));

    let strategy = CodexStrategy::with_roots(
        secret(),
        roots.codex_roots.clone(),
        SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    assert_eq!(
        strategy.source_id_for_locator("/tmp/codex/sessions/a.jsonl"),
        Some("codex-sessions")
    );
    assert_eq!(
        strategy.source_id_for_locator("/tmp/codex/archived_sessions/b.jsonl"),
        Some("codex-archived-sessions")
    );
}

#[test]
fn review2_discover_rotates_past_64_cap() {
    use super::jsonl_io::discover_jsonl_files;

    let dir = tempfile::tempdir().unwrap();
    for i in 0..80 {
        std::fs::write(dir.path().join(format!("s{i:03}.jsonl")), "{}\n").unwrap();
    }
    let (page1, cursor1) = discover_jsonl_files(dir.path(), ".jsonl", 64, None);
    assert_eq!(page1.len(), 64);
    let cursor1 = cursor1.expect("cursor");
    let (page2, _) = discover_jsonl_files(dir.path(), ".jsonl", 64, Some(&cursor1));
    assert_eq!(page2.len(), 64);
    // Second page must include files beyond the first 64 lexicographic slice.
    let p1: std::collections::HashSet<_> = page1
        .iter()
        .map(|p| p.to_string_lossy().into_owned())
        .collect();
    let new_on_page2 = page2
        .iter()
        .filter(|p| !p1.contains(p.to_string_lossy().as_ref()))
        .count();
    assert!(
        new_on_page2 >= 16,
        "rotation must surface files past the permanent-64 trap, got {new_on_page2} new"
    );
}

#[test]
fn review2_single_file_history_jsonl_locator() {
    use super::jsonl_io::discover_jsonl_files;

    let dir = tempfile::tempdir().unwrap();
    let file = dir.path().join("history.jsonl");
    std::fs::write(
        &file,
        format!(
            "{}\n",
            json!({"type":"model_usage","timestamp":1,"usage":{"inputTokens":1,"outputTokens":1,"totalTokens":2}})
        ),
    )
    .unwrap();

    let (found, _) = discover_jsonl_files(&file, ".jsonl", 8, None);
    assert_eq!(found, vec![file.clone()]);

    let strategy = JsonlHarnessStrategy::new(
        super::jsonl_harness::WORKBUDDY,
        secret(),
        file.clone(),
        SkillBook::new(),
        Arc::new(|_, _| 1),
    );
    let specs = strategy.discover(DiscoveryBudget::new(8, 200)).unwrap();
    assert_eq!(specs.len(), 1);
    assert_eq!(specs[0].locator_ref, file.to_string_lossy());
}

#[test]
fn review2_harness_disable_stops_discover_and_claim() {
    use crate::local_store::pipeline::runtime::{adapter_roots_for_fixture, PipelineRuntime};
    use crate::local_store::pipeline::PipelineWriter;
    use std::time::{SystemTime, UNIX_EPOCH};

    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_millis() as i64;
    // Keep fixture on "today" for wall-clock admission in writer path.
    let dir = tempfile::tempdir().unwrap();
    let codex_dir = dir.path().join("codex");
    std::fs::create_dir_all(&codex_dir).unwrap();
    std::fs::write(codex_dir.join("a.jsonl"), "{}\n").unwrap();
    let claude_dir = dir.path().join("claude");
    std::fs::create_dir_all(&claude_dir).unwrap();
    std::fs::write(claude_dir.join("c.jsonl"), "{}\n").unwrap();

    let store = open_store(now);
    let writer = Arc::new(PipelineWriter::start(store));
    let roots = adapter_roots_for_fixture(secret(), dir.path());
    let writer_skills = Arc::clone(&writer);
    let book = SkillBook::new();
    let book2 = book.clone();
    let alloc = Arc::new(move |key, name: &str| {
        if let Some(id) = book2.get(&key) {
            return id;
        }
        let id = writer_skills.register_skill(key, Some(name)).unwrap_or(0);
        book2.upsert(key, id);
        id
    });
    let registry = HarnessRegistry::from_roots(roots, alloc);

    // Build runtime via start would need DetectionSnapshot; exercise enable gate through writer+manual.
    writer.set_harness_sources_enabled("codex", false).unwrap();
    let due_before = writer.list_due_sources(now, 32).unwrap();
    // Register both harness sources then disable codex.
    let codex = registry.get("codex").unwrap();
    for spec in codex.discover(DiscoveryBudget::new(8, 200)).unwrap() {
        writer
            .register_source(RegisterSource {
                harness_id: spec.harness_id,
                source_key: spec.source_key,
                source_kind: spec.source_kind,
                locator_ref: spec.locator_ref,
                stream_key: spec.stream_key,
                cursor_kind: spec.cursor_kind,
                cursor_json: spec.initial_cursor_json.to_string(),
                decoder_state_version: 1,
                decoder_state_json: spec.initial_decoder_state_json.to_string(),
                observed_boundary_json: spec.observed_boundary_json.to_string(),
                next_poll_at: Some(now),
            })
            .unwrap();
    }
    let claude = registry.get("claude-code").unwrap();
    for spec in claude.discover(DiscoveryBudget::new(8, 200)).unwrap() {
        writer
            .register_source(RegisterSource {
                harness_id: spec.harness_id,
                source_key: spec.source_key,
                source_kind: spec.source_kind,
                locator_ref: spec.locator_ref,
                stream_key: spec.stream_key,
                cursor_kind: spec.cursor_kind,
                cursor_json: spec.initial_cursor_json.to_string(),
                decoder_state_version: 1,
                decoder_state_json: spec.initial_decoder_state_json.to_string(),
                observed_boundary_json: spec.observed_boundary_json.to_string(),
                next_poll_at: Some(now),
            })
            .unwrap();
    }
    writer.set_harness_sources_enabled("codex", false).unwrap();
    let due = writer.list_due_sources(now, 32).unwrap();
    for id in &due {
        let snap = writer.load_source_checkpoint(*id).unwrap();
        assert_ne!(
            snap.harness_id, "codex",
            "disabled harness must not be claimable"
        );
    }
    assert!(
        due.iter()
            .any(|id| { writer.load_source_checkpoint(*id).unwrap().harness_id == "claude-code" }),
        "other harnesses must remain claimable"
    );
    let _ = (due_before, PipelineRuntime::start);
    drop(writer);
}

#[test]
fn review2_slow_source_does_not_block_other_harness_acquire() {
    use crate::local_store::pipeline::runner::{AcquisitionScheduler, ReadBudget};
    use crate::local_store::pipeline::runtime::{adapter_roots_for_fixture, PipelineRuntime};
    use crate::local_store::pipeline::PipelineWriter;
    use std::sync::atomic::{AtomicBool, AtomicUsize, Ordering};
    use std::time::Duration;

    let now = beijing_wall_to_utc_ms(2026, 9, 12, 15, 30, 0);
    let dir = tempfile::tempdir().unwrap();
    std::fs::create_dir_all(dir.path().join("codex")).unwrap();
    std::fs::create_dir_all(dir.path().join("claude")).unwrap();
    std::fs::write(dir.path().join("codex/slow.jsonl"), "{}\n").unwrap();
    std::fs::write(dir.path().join("claude/fast.jsonl"), "{}\n").unwrap();

    let store = open_store(now);
    let writer = Arc::new(PipelineWriter::start(store));
    let roots = adapter_roots_for_fixture(secret(), dir.path());
    let writer_skills = Arc::clone(&writer);
    let book = SkillBook::new();
    let book2 = book.clone();
    let alloc = Arc::new(move |key, name: &str| {
        if let Some(id) = book2.get(&key) {
            return id;
        }
        let id = writer_skills.register_skill(key, Some(name)).unwrap_or(0);
        book2.upsert(key, id);
        id
    });
    let registry = HarnessRegistry::from_roots(roots, alloc);

    let mut source_ids = Vec::new();
    for harness in ["codex", "claude-code"] {
        let strategy = registry.get(harness).unwrap();
        for spec in strategy.discover(DiscoveryBudget::new(8, 200)).unwrap() {
            let id = writer
                .register_source(RegisterSource {
                    harness_id: spec.harness_id,
                    source_key: spec.source_key,
                    source_kind: spec.source_kind,
                    locator_ref: spec.locator_ref,
                    stream_key: spec.stream_key,
                    cursor_kind: spec.cursor_kind,
                    cursor_json: spec.initial_cursor_json.to_string(),
                    decoder_state_version: 1,
                    decoder_state_json: spec.initial_decoder_state_json.to_string(),
                    observed_boundary_json: spec.observed_boundary_json.to_string(),
                    next_poll_at: Some(now),
                })
                .unwrap();
            source_ids.push((harness.to_string(), id));
        }
    }

    let scheduler = AcquisitionScheduler::with_defaults();
    let fast_done = Arc::new(AtomicBool::new(false));
    let started = Arc::new(AtomicUsize::new(0));
    let t0 = std::time::Instant::now();

    std::thread::scope(|scope| {
        for (harness, source_id) in &source_ids {
            let permit = scheduler.try_acquire(harness, *source_id).expect("permit");
            let fast_done = Arc::clone(&fast_done);
            let started = Arc::clone(&started);
            let harness = harness.clone();
            scope.spawn(move || {
                let _permit = permit;
                started.fetch_add(1, Ordering::SeqCst);
                if harness == "codex" {
                    // Slow source: sleep while other harness should still complete.
                    while !fast_done.load(Ordering::SeqCst) {
                        if t0.elapsed() > Duration::from_millis(800) {
                            break;
                        }
                        std::thread::sleep(Duration::from_millis(10));
                    }
                } else {
                    std::thread::sleep(Duration::from_millis(20));
                    fast_done.store(true, Ordering::SeqCst);
                }
            });
        }
    });

    assert!(
        fast_done.load(Ordering::SeqCst),
        "fast harness must finish while slow source is still running"
    );
    assert!(
        t0.elapsed() < Duration::from_millis(700),
        "concurrent acquire must not serialize behind the slow source"
    );
    let _ = (ReadBudget::new(1, 1, 1), PipelineRuntime::start, writer);
}

#[test]
fn review2_pipeline_query_facade_returns_tokens_when_legacy_empty() {
    use crate::local_store::pipeline::buckets::Grain;
    use crate::local_store::pipeline::query::query_usage_summary;
    use crate::local_store::pipeline::PipelineWriter;

    let now = beijing_wall_to_utc_ms(2026, 9, 12, 12, 0, 0);
    let mut store = open_store(now);
    // Insert a day-grain model_metrics row as the facade would see after drain.
    store
        .with_connection(|conn| {
            conn.execute(
                "INSERT INTO model_metrics (
                    created_at, updated_at, grain, bucket_start, harness_id, model_key,
                    metric_semantics_version, exact_token_total, derived_token_total,
                    token_total_known_count, usage_observed_count, model_request_count,
                    cache_eligible_input_tokens, cache_eligible_read_tokens
                 ) VALUES (?1,?1,'day',?2,'codex',0,1,42,0,1,1,1,0,0)",
                rusqlite::params![now, crate::local_store::pipeline::beijing_day_start(now)],
            )
            .map_err(crate::local_store::pipeline::types::PipelineError::from)?;
            Ok(())
        })
        .unwrap();

    let day_start = crate::local_store::pipeline::beijing_day_start(now);
    let summary = store
        .with_connection(|conn| {
            query_usage_summary(
                conn,
                Grain::Day,
                day_start,
                day_start + 86_400_000,
                Some("codex"),
            )
        })
        .unwrap();
    assert_eq!(summary.total_tokens.value, Some(42));

    let writer = PipelineWriter::start(store);
    let from_writer = writer
        .query_usage_summary(Grain::Day, day_start, day_start + 86_400_000, Some("codex"))
        .unwrap();
    assert_eq!(from_writer.total_tokens.value, Some(42));
    drop(writer);
}
