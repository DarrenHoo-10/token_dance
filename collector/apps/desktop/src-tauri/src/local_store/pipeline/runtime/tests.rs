use super::*;
use crate::local_store::pipeline::PipelineStore;
use std::io::Write;

fn fixture() -> (tempfile::TempDir, PipelineRuntime) {
    let dir = tempfile::tempdir().unwrap();
    let writer = Arc::new(PipelineWriter::start(
        PipelineStore::open_in_memory().unwrap(),
    ));
    let runtime = PipelineRuntime::from_roots(
        writer,
        adapter_roots_for_fixture(b"throughput-fixture".to_vec(), dir.path()),
    );
    (dir, runtime)
}

fn reconcile(runtime: &PipelineRuntime) {
    runtime.discovery.lock().unwrap().last_metadata = 0;
    runtime.refresh_sources();
}

#[test]
fn workers_drain_more_than_four_batches_without_a_timer_tick() {
    let (dir, runtime) = fixture();
    for folder in ["codex", "claude", "cursor", "grok", "deepseek", "pi"] {
        let root = dir.path().join(folder);
        std::fs::create_dir_all(&root).unwrap();
        for i in 0..32 {
            std::fs::write(root.join(format!("s{i:03}.jsonl")), "{}\n".repeat(300)).unwrap();
        }
    }
    assert_eq!(runtime.discover_and_register(), 192);
    let start = std::time::Instant::now();
    std::thread::scope(|scope| {
        for _ in 0..4 {
            let runtime = &runtime;
            scope.spawn(move || while runtime.acquire_one() {});
        }
    });
    let queue = runtime.queue.lock().unwrap();
    assert_eq!(queue.sources.len(), 192);
    for source in &queue.sources {
        let checkpoint = runtime.writer.load_source_checkpoint(source.id).unwrap();
        assert_eq!(
            checkpoint.commit_seq, 2,
            "{} must advance without duplicates",
            source.harness
        );
        assert!(source.idle);
    }
    assert_eq!(runtime.acquired.load(Ordering::Relaxed), 384);
    assert_eq!(runtime.failures.load(Ordering::Relaxed), 0);
    eprintln!(
        "384 batches / 192 files drained in {:?}, without another timer tick",
        start.elapsed()
    );
}

#[test]
fn review3_more_harnesses_than_slots_all_make_progress() {
    let (dir, runtime) = fixture();
    for folder in ["codex", "claude", "cursor", "grok", "deepseek", "pi"] {
        let root = dir.path().join(folder);
        std::fs::create_dir_all(&root).unwrap();
        for i in 0..32 {
            std::fs::write(root.join(format!("s{i:03}.jsonl")), "{}\n").unwrap();
        }
    }
    runtime.discover_and_register();
    for _ in 0..6 {
        assert!(runtime.acquire_one());
    }
    let q = runtime.queue.lock().unwrap();
    for harness in [
        "codex",
        "claude-code",
        "cursor",
        "grok-build",
        "deepseek-harness",
        "pi",
    ] {
        assert!(
            q.sources
                .iter()
                .any(|s| s.harness == harness && s.last_served > 0),
            "{harness} starved"
        );
    }
}

#[test]
fn review3_due_page_does_not_starve_another_harness() {
    let (dir, runtime) = fixture();
    for (folder, count) in [("codex", 300), ("claude", 1)] {
        let root = dir.path().join(folder);
        std::fs::create_dir_all(&root).unwrap();
        for i in 0..count {
            std::fs::write(root.join(format!("s{i:03}.jsonl")), "{}\n").unwrap();
        }
    }
    runtime.discover_and_register();
    assert!(runtime.acquire_one());
    assert!(runtime.acquire_one());
    let q = runtime.queue.lock().unwrap();
    assert!(q
        .sources
        .iter()
        .any(|s| s.harness == "claude-code" && s.last_served > 0));
}

#[test]
fn idle_and_incomplete_files_do_not_commit_until_append() {
    let (dir, runtime) = fixture();
    let root = dir.path().join("codex");
    std::fs::create_dir_all(&root).unwrap();
    let path = root.join("s.jsonl");
    std::fs::write(&path, "{}\n{").unwrap();
    runtime.discover_and_register();
    assert!(runtime.acquire_one()); // complete first line
    assert!(runtime.acquire_one()); // incomplete tail, no progress
    let id = runtime.queue.lock().unwrap().sources[0].id;
    let before = runtime.writer.load_source_checkpoint(id).unwrap();
    for _ in 0..10 {
        reconcile(&runtime);
        assert!(!runtime.acquire_one());
    }
    assert_eq!(
        runtime
            .writer
            .load_source_checkpoint(id)
            .unwrap()
            .commit_seq,
        before.commit_seq
    );
    std::fs::OpenOptions::new()
        .append(true)
        .open(&path)
        .unwrap()
        .write_all(b"}\n")
        .unwrap();
    reconcile(&runtime);
    assert!(runtime.acquire_one());
    let after = runtime.writer.load_source_checkpoint(id).unwrap();
    assert_eq!(
        serde_json::from_str::<serde_json::Value>(&after.cursor_json).unwrap()["offset"],
        6
    );
    assert_eq!(after.commit_seq, before.commit_seq + 1);
    assert!(!runtime.acquire_one());
}

#[test]
fn active_files_precede_history_but_history_gets_a_fair_turn() {
    let (dir, runtime) = fixture();
    let root = dir.path().join("codex");
    std::fs::create_dir_all(&root).unwrap();
    let old = root.join("a-old.jsonl");
    std::fs::write(&old, "{}\n".repeat(3000)).unwrap();
    std::fs::File::options()
        .write(true)
        .open(&old)
        .unwrap()
        .set_modified(UNIX_EPOCH + std::time::Duration::from_secs(1))
        .unwrap();
    let hot = root.join("z-active.jsonl");
    std::fs::write(&hot, "{}\n".repeat(3000)).unwrap();
    runtime.discover_and_register();
    for _ in 0..7 {
        assert!(runtime.acquire_one());
    }
    {
        let q = runtime.queue.lock().unwrap();
        assert_eq!(
            q.sources
                .iter()
                .find(|s| s.locator == old.to_string_lossy())
                .unwrap()
                .last_served,
            0
        );
    }
    assert!(runtime.acquire_one());
    let q = runtime.queue.lock().unwrap();
    assert!(
        q.sources
            .iter()
            .find(|s| s.locator == old.to_string_lossy())
            .unwrap()
            .last_served
            > 0
    );
}

#[test]
fn discovery_is_cached_and_new_files_register_without_resetting_checkpoints() {
    let (dir, runtime) = fixture();
    let root = dir.path().join("codex");
    std::fs::create_dir_all(&root).unwrap();
    for i in 0..130 {
        std::fs::write(root.join(format!("s{i:03}.jsonl")), "{}\n").unwrap();
    }
    assert_eq!(runtime.discover_and_register(), 130);
    assert!(runtime.acquire_one());
    assert_eq!(runtime.discover_and_register(), 0);
    std::fs::write(root.join("new.jsonl"), "{}\n").unwrap();
    runtime.discovery.lock().unwrap().last_scan = 0;
    assert_eq!(runtime.discover_and_register(), 1);
    assert_eq!(runtime.queue.lock().unwrap().sources.len(), 131);
    let q = runtime.queue.lock().unwrap();
    let id = q.sources.iter().find(|s| s.last_served > 0).unwrap().id;
    assert_eq!(
        runtime
            .writer
            .load_source_checkpoint(id)
            .unwrap()
            .commit_seq,
        1
    );
}

#[test]
fn sqlite_wal_changes_wake_idle_stream_without_database_mtime_change() {
    let (dir, runtime) = fixture();
    let db = dir.path().join("source.sqlite");
    let conn = rusqlite::Connection::open(&db).unwrap();
    conn.execute_batch("PRAGMA journal_mode=WAL; CREATE TABLE sample(id INTEGER PRIMARY KEY);")
        .unwrap();
    let before = fingerprint(db.to_str().unwrap(), SourceKind::Sqlite);
    runtime.queue.lock().unwrap().sources.push(QueuedSource {
        id: 42,
        harness: "fixture".into(),
        locator: db.to_string_lossy().into_owned(),
        kind: SourceKind::Sqlite,
        observed: before.clone(),
        ready_at: now_ms() + 30_000,
        idle: true,
        in_flight: false,
        last_served: 0,
    });
    conn.execute("INSERT INTO sample VALUES (1)", []).unwrap();
    assert_eq!(
        before.0[0],
        fingerprint(db.to_str().unwrap(), SourceKind::Sqlite).0[0]
    );
    reconcile(&runtime);
    let q = runtime.queue.lock().unwrap();
    assert!(!q.sources[0].idle);
    assert!(q.sources[0].ready_at <= now_ms());
}

#[test]
fn paused_harness_keeps_cursor_and_resumes_queued_work() {
    let (dir, runtime) = fixture();
    let root = dir.path().join("codex");
    std::fs::create_dir_all(&root).unwrap();
    std::fs::write(root.join("s.jsonl"), "{}\n").unwrap();
    runtime.discover_and_register();
    runtime.set_harness_enabled("codex", false);
    assert!(!runtime.acquire_one());
    runtime.set_harness_enabled("codex", true);
    assert!(runtime.acquire_one());
}

#[test]
fn history_fairness_is_per_harness_not_global_rotation() {
    let (dir, runtime) = fixture();
    for folder in ["codex", "claude"] {
        let root = dir.path().join(folder);
        std::fs::create_dir_all(&root).unwrap();
        for (name, old) in [("old.jsonl", true), ("hot.jsonl", false)] {
            let path = root.join(name);
            std::fs::write(&path, "{}\n".repeat(4000)).unwrap();
            if old {
                std::fs::File::options()
                    .write(true)
                    .open(path)
                    .unwrap()
                    .set_modified(UNIX_EPOCH + std::time::Duration::from_secs(1))
                    .unwrap();
            }
        }
    }
    runtime.discover_and_register();
    for _ in 0..16 {
        assert!(runtime.acquire_one());
    }
    let q = runtime.queue.lock().unwrap();
    for harness in ["codex", "claude-code"] {
        assert!(q.sources.iter().any(|s| s.harness == harness
            && s.locator.ends_with("old.jsonl")
            && s.last_served > 0));
    }
}

#[test]
fn restart_registration_preserves_committed_cursor_and_decoder_state() {
    let (dir, runtime) = fixture();
    let root = dir.path().join("codex");
    std::fs::create_dir_all(&root).unwrap();
    std::fs::write(root.join("s.jsonl"), "{}\n".repeat(300)).unwrap();
    runtime.discover_and_register();
    runtime.acquire_one();
    let id = runtime.queue.lock().unwrap().sources[0].id;
    let before = runtime.writer.load_source_checkpoint(id).unwrap();
    let restarted = PipelineRuntime::from_roots(
        runtime.writer(),
        adapter_roots_for_fixture(b"throughput-fixture".to_vec(), dir.path()),
    );
    assert_eq!(restarted.discover_and_register(), 1);
    let after = restarted.writer.load_source_checkpoint(id).unwrap();
    assert_eq!(after.commit_seq, before.commit_seq);
    assert_eq!(after.cursor_json, before.cursor_json);
    assert_eq!(after.decoder_state_json, before.decoder_state_json);
    assert!(restarted.acquire_one());
    assert_eq!(
        restarted
            .writer
            .load_source_checkpoint(id)
            .unwrap()
            .commit_seq,
        before.commit_seq + 1
    );
}

#[test]
fn source_registration_page_rolls_back_as_one_transaction() {
    let writer = PipelineWriter::start(PipelineStore::open_in_memory().unwrap());
    let first = RegisterSource {
        harness_id: "fixture".into(),
        source_key: [1; 32],
        source_kind: SourceKind::Jsonl,
        locator_ref: "fixture.jsonl".into(),
        stream_key: "jsonl".into(),
        cursor_kind: CursorKind::ByteOffset,
        cursor_json: "{}".into(),
        decoder_state_version: 1,
        decoder_state_json: "{}".into(),
        observed_boundary_json: "{}".into(),
        next_poll_at: Some(now_ms()),
    };
    let mut second = first.clone();
    second.harness_id.clear(); // fails the SQLite CHECK after the first row's INSERT
    second.source_key = [2; 32];
    assert!(writer.register_sources(vec![first, second]).is_err());
    assert!(writer.list_source_locators("fixture").unwrap().is_empty());
}

#[test]
fn filesystem_create_notification_invalidates_discovery_cache() {
    let dir = tempfile::tempdir().unwrap();
    let root = dir.path().join("codex");
    std::fs::create_dir_all(&root).unwrap();
    let writer = Arc::new(PipelineWriter::start(
        PipelineStore::open_in_memory().unwrap(),
    ));
    let runtime = PipelineRuntime::from_roots(
        writer,
        adapter_roots_for_fixture(b"watch-fixture".to_vec(), dir.path()),
    );
    assert!(runtime._watcher.is_some());
    assert_eq!(runtime.discover_and_register(), 0);
    std::fs::write(root.join("new.jsonl"), "{}\n").unwrap();
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(3);
    while !runtime.scan_dirty.load(Ordering::Acquire) && std::time::Instant::now() < deadline {
        std::thread::sleep(std::time::Duration::from_millis(10));
    }
    assert!(
        runtime.scan_dirty.load(Ordering::Acquire),
        "create notification must invalidate cached discovery"
    );
    runtime.discovery.lock().unwrap().last_scan = now_ms() - DISCOVER_DEBOUNCE_MS;
    assert_eq!(runtime.discover_and_register(), 1);
}
