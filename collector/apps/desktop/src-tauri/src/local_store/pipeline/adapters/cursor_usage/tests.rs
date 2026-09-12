use super::super::CursorStrategy;
use super::*;
use crate::local_store::pipeline::runner::{
    AcquisitionRunner, DiscoveryBudget, HarnessStrategy, RunOutcome, DEFAULT_READ_BUDGET,
};
use crate::local_store::pipeline::types::{Consumer, RegisterSource, DEFAULT_LEASE_MS};
use crate::local_store::pipeline::{PipelineStore, PipelineWriter};
use std::sync::Arc;

const LOCAL: &str = "00000000-0000-4000-8000-000000000001";
const REMOTE: &str = "00000000-0000-4000-8000-000000000002";
fn cursor() -> Value {
    json!({"start":1,"end":10000,"page":1,"page_size":2})
}
fn row(ts: i64, id: &str) -> Value {
    json!({"timestamp":ts.to_string(),"conversationId":id,"model":"fixture-model",
    "tokenUsage":{"inputTokens":10,"outputTokens":2,"cacheReadTokens":20,"cacheWriteTokens":3},
    "owningUser":"must-not-persist","userEmail":"must-not-persist","chargedCents":12.3})
}
fn page(rows: Vec<Value>, total: usize) -> Value {
    json!({"usageEventsDisplay":rows,"totalUsageEventsCount":total})
}
fn local() -> HashSet<String> {
    HashSet::from([LOCAL.into()])
}
fn paths(root: &Path) -> CursorUsagePaths {
    CursorUsagePaths {
        chats: root.join("chats"),
        database: root.join("state.vscdb"),
        auth: root.join("auth.json"),
    }
}
fn auth(paths: &CursorUsagePaths, subject: &str) {
    let claims = URL_SAFE_NO_PAD
        .encode(json!({"sub":subject,"exp":chrono::Utc::now().timestamp()+3600}).to_string());
    std::fs::write(
        &paths.auth,
        json!({"accessToken":format!("e30.{claims}.fixture")}).to_string(),
    )
    .unwrap();
}
fn cli(paths: &CursorUsagePaths) {
    let root = paths.chats.join("workspace").join(LOCAL);
    std::fs::create_dir_all(&root).unwrap();
    let db = rusqlite::Connection::open(root.join("store.db")).unwrap();
    db.execute_batch("CREATE TABLE meta(key TEXT PRIMARY KEY,value TEXT);")
        .unwrap();
    let meta =
        json!({"agentId":LOCAL,"name":"private fixture","blobEncryptionKey":"must-not-persist"})
            .to_string();
    let hex: String = meta.bytes().map(|b| format!("{b:02x}")).collect();
    db.execute("INSERT INTO meta VALUES ('0',?1)", [hex])
        .unwrap();
}

#[test]
fn exact_usage_is_local_only_and_contains_no_account_fields() {
    let batch = project_page(
        &page(vec![row(99, LOCAL), row(98, REMOTE)], 2),
        &local(),
        &mut cursor(),
    )
    .unwrap();
    assert_eq!(batch.records.len(), 1);
    assert!(!batch.has_more);
    let text = std::str::from_utf8(&batch.records[0].payload).unwrap();
    assert!(!text.contains("must-not-persist"));
    let fact = decode_usage(b"fixture", &serde_json::from_str(text).unwrap()).unwrap();
    assert_eq!(fact.accuracy, TokenAccuracy::Exact);
    assert_eq!(fact.payload_sections["usage"]["token_total"], 35);
    assert_eq!(fact.payload_sections["usage"]["input_context_tokens"], 33);
    assert_eq!(fact.payload_sections["usage"]["output_tokens"], 2);
    assert_eq!(fact.payload_sections["usage"]["cache_read_tokens"], 20);
}

#[test]
fn identical_calls_keep_multiplicity_across_page_boundaries_and_replays() {
    let mut c = cursor();
    let first = project_page(
        &page(vec![row(99, LOCAL), row(99, LOCAL)], 3),
        &local(),
        &mut c,
    )
    .unwrap();
    let second = project_page(
        &page(vec![row(99, LOCAL)], 3),
        &local(),
        &mut first.next_cursor_json.clone(),
    )
    .unwrap();
    let keys: Vec<_> = first
        .records
        .iter()
        .chain(&second.records)
        .map(|r| {
            serde_json::from_slice::<Value>(&r.payload).unwrap()["native_key"]
                .as_str()
                .unwrap()
                .to_string()
        })
        .collect();
    assert_eq!(keys.iter().collect::<HashSet<_>>().len(), 3);
    let replay = project_page(
        &page(vec![row(99, LOCAL), row(99, LOCAL)], 3),
        &local(),
        &mut cursor(),
    )
    .unwrap();
    assert_eq!(first.records[0].payload, replay.records[0].payload);
    // Billing changes are not new usage identities.
    let mut changed = row(99, LOCAL);
    changed["chargedCents"] = json!(99.9);
    let corrected = project_page(&page(vec![changed], 1), &local(), &mut cursor()).unwrap();
    assert_eq!(corrected.records[0].payload, replay.records[0].payload);
}

#[test]
fn pages_without_local_rows_still_advance_and_malformed_pages_do_not() {
    let mut c = cursor();
    let first = project_page(
        &page(vec![row(99, REMOTE), row(98, REMOTE)], 3),
        &local(),
        &mut c,
    )
    .unwrap();
    assert!(first.records.is_empty());
    assert!(first.has_more);
    assert_eq!(first.next_cursor_json["page"], 2);
    let second = project_page(
        &page(vec![row(97, LOCAL)], 3),
        &local(),
        &mut first.next_cursor_json.clone(),
    )
    .unwrap();
    assert_eq!(second.records.len(), 1);
    assert!(project_page(
        &page(vec![row(99, LOCAL), row(100, LOCAL)], 2),
        &local(),
        &mut cursor()
    )
    .is_err());
    assert!(project_page(&page(vec![], 10), &local(), &mut cursor()).is_err());
    assert!(project_page(&json!({"unexpected":true}), &local(), &mut cursor()).is_err());
    assert!(project_page(&page(vec![row(0, LOCAL)], 1), &local(), &mut cursor()).is_err());
}

#[test]
fn absent_zero_scalars_are_valid_but_negative_and_overflow_are_not() {
    let mut r = row(99, LOCAL);
    r["tokenUsage"] = json!({});
    let b = project_page(&page(vec![r.clone()], 1), &local(), &mut cursor()).unwrap();
    assert_eq!(
        decode_usage(
            b"s",
            &serde_json::from_slice(&b.records[0].payload).unwrap()
        )
        .unwrap()
        .payload_sections["usage"]["token_total"],
        0
    );
    r["tokenUsage"] = json!({"inputTokens":-1});
    assert!(project_page(&page(vec![r.clone()], 1), &local(), &mut cursor()).is_err());
    r["tokenUsage"] = json!({"inputTokens":u64::MAX,"outputTokens":1});
    let b = project_page(&page(vec![r], 1), &local(), &mut cursor()).unwrap();
    assert!(decode_usage(
        b"s",
        &serde_json::from_slice(&b.records[0].payload).unwrap()
    )
    .is_err());
}

#[test]
fn discovers_cli_metadata_ide_keys_and_transcripts_without_opening_message_blobs() {
    let dir = tempfile::tempdir().unwrap();
    let p = paths(dir.path());
    cli(&p);
    let db = rusqlite::Connection::open(&p.database).unwrap();
    db.execute_batch("CREATE TABLE cursorDiskKV(key TEXT,value BLOB);")
        .unwrap();
    db.execute(
        "INSERT INTO cursorDiskKV VALUES (?1,'private data')",
        [format!("composerData:{REMOTE}")],
    )
    .unwrap();
    drop(db);
    let ids = local_conversations(&p, &dir.path().join("transcripts")).unwrap();
    assert_eq!(ids, HashSet::from([LOCAL.into(), REMOTE.into()]));
    let remote_prefix = format!("sand-subagent-{LOCAL}");
    assert!(!ids.contains(&remote_prefix));
}

fn server(
    body: Value,
    status: &str,
    after_request: impl FnOnce() + Send + 'static,
) -> (String, std::thread::JoinHandle<()>) {
    use std::io::{Read, Write};
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let url = format!("http://{}/usage", listener.local_addr().unwrap());
    let status = status.to_owned();
    let handle = std::thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let mut request = Vec::new();
        let mut b = [0u8; 4096];
        loop {
            let n = stream.read(&mut b).unwrap();
            assert!(n > 0);
            request.extend_from_slice(&b[..n]);
            if let Some(pos) = request.windows(4).position(|w| w == b"\r\n\r\n") {
                let headers = String::from_utf8_lossy(&request[..pos]).to_lowercase();
                assert!(headers.contains("authorization: bearer "));
                let len: usize = headers
                    .lines()
                    .find_map(|l| l.strip_prefix("content-length:"))
                    .unwrap()
                    .trim()
                    .parse()
                    .unwrap();
                if request.len() >= pos + 4 + len {
                    break;
                }
            }
        }
        after_request();
        let bytes = body.to_string();
        write!(stream,"HTTP/1.1 {status}\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{bytes}",bytes.len()).unwrap();
    });
    (url, handle)
}

#[test]
fn raw_to_event_metrics_replay_is_idempotent_and_status_uses_real_feed() {
    let dir = tempfile::tempdir().unwrap();
    let paths = paths(dir.path());
    auth(&paths, "fixture-user");
    cli(&paths);
    let now = chrono::Utc::now().timestamp_millis() - 1000;
    let make = || {
        CursorStrategy::new(
            b"test".to_vec(),
            dir.path().join("transcripts"),
            super::super::SkillBook::new(),
            Arc::new(|_, _| 1),
        )
        .with_usage(paths.clone())
    };
    let writer = Arc::new(PipelineWriter::start(
        PipelineStore::open_in_memory().unwrap(),
    ));
    let mut source_id = None;
    for _ in 0..2 {
        let (url, server) = server(page(vec![row(now, LOCAL)], 1), "200 OK", || {});
        let mut strategy = make();
        let model_writer = Arc::clone(&writer);
        strategy.set_model_allocator(Arc::new(move |provider, model| {
            model_writer
                .upsert_model(provider, model)
                .map_err(|_| failure("model allocation failed"))
        }));
        strategy.usage_source.as_mut().unwrap().endpoint = Some(url);
        let spec = strategy
            .discover(DiscoveryBudget::default())
            .unwrap()
            .into_iter()
            .find(|s| s.stream_key == STREAM_USAGE)
            .unwrap();
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
                decoder_state_json: "{}".into(),
                observed_boundary_json: "{}".into(),
                next_poll_at: Some(now),
            })
            .unwrap();
        assert!(source_id.is_none() || source_id == Some(id));
        source_id = Some(id);
        let outcome = AcquisitionRunner::default()
            .run_once(writer.as_ref(), &strategy, id, DEFAULT_READ_BUDGET)
            .unwrap();
        assert!(matches!(outcome, RunOutcome::Committed(_)));
        assert_eq!(strategy.collection_status(), Some("ACTIVE"));
        server.join().unwrap();
    }
    assert_eq!(writer.pending_upload_count().unwrap(), 1);
    let tasks = writer
        .claim_tasks(Consumer::Upload, 10, DEFAULT_LEASE_MS)
        .unwrap();
    let ids: Vec<_> = tasks.iter().map(|t| t.event_row_id).collect();
    let events = writer.load_upload_events(ids).unwrap();
    assert_eq!(
        events[0].model,
        Some(("cursor".into(), "fixture-model".into()))
    );
    let wire = crate::upload_pipeline::encode_wire_event(&events[0]).unwrap();
    let expected =
        protocol::v2::compute_content_hash(&serde_json::to_value(&wire).unwrap()).unwrap();
    assert_eq!(wire.content_hash, expected);
    assert_eq!(
        writer
            .drain_metrics_consumer(Consumer::Day, 64, DEFAULT_LEASE_MS)
            .unwrap()
            .applied,
        1
    );
}

#[test]
fn authentication_errors_and_midflight_account_change_never_commit_a_page() {
    let dir = tempfile::tempdir().unwrap();
    let p = paths(dir.path());
    auth(&p, "first");
    cli(&p);
    let checkpoint = CheckpointView {
        cursor_json: json!({}),
        decoder_state_version: 1,
        decoder_state_json: json!({}),
        observed_boundary_json: json!({}),
        commit_seq: 0,
    };
    let (url, handle) = server(json!({"code":"unauthenticated"}), "401 Unauthorized", || {});
    let mut source = CursorUsageSource::new(p.clone());
    source.endpoint = Some(url);
    assert!(source
        .read(
            &dir.path().join("transcripts"),
            &checkpoint,
            DEFAULT_READ_BUDGET
        )
        .is_err());
    assert_eq!(source.status(), "AUTH_REQUIRED");
    handle.join().unwrap();
    let changed = p.clone();
    let (url, handle) = server(page(vec![], 0), "200 OK", move || auth(&changed, "second"));
    source.endpoint = Some(url);
    assert!(
        matches!(source.read(&dir.path().join("transcripts"),&checkpoint,DEFAULT_READ_BUDGET),Err(RunnerError::Io(code)) if code=="CURSOR_ACCOUNT_CHANGED")
    );
    handle.join().unwrap();
    std::fs::write(&p.auth, "invalid").unwrap();
    assert!(load_token(&p).is_err());
}

#[test]
fn historical_pagination_resumes_after_process_restart() {
    let dir=tempfile::tempdir().unwrap();
    let p=paths(dir.path());
    auth(&p,"fixture-user"); cli(&p);
    let now=chrono::Utc::now().timestamp_millis();
    let checkpoint=CheckpointView {
        cursor_json:json!({"start":0,"end":now,"page":2,"page_size":1}),
        decoder_state_version:1,decoder_state_json:json!({}),
        observed_boundary_json:json!({"_rebuild_pending":true}),commit_seq:1,
    };
    let (url,handle)=server(page(vec![row(now-86400000,LOCAL)],2),"200 OK",||{});
    let mut source=CursorUsageSource::new(p); source.endpoint=Some(url);
    let batch=source.read(&dir.path().join("transcripts"),&checkpoint,DEFAULT_READ_BUDGET).unwrap();
    handle.join().unwrap();
    assert!(!batch.has_more,"must resume page 2, not restart at page 1");
    assert_eq!(batch.records.len(),1);
}

#[test]
#[ignore = "explicit read-only local account diagnostic; needs TOKENDANCE_CURSOR_LIVE_HOME and TOKENDANCE_CURSOR_LIVE_CONFIG"]
fn live_cursor_feed_to_isolated_store() {
    let historical = std::env::var_os("TOKENDANCE_CURSOR_LIVE_REBUILD").is_some();
    let home = PathBuf::from(std::env::var_os("TOKENDANCE_CURSOR_LIVE_HOME").unwrap());
    let config = PathBuf::from(std::env::var_os("TOKENDANCE_CURSOR_LIVE_CONFIG").unwrap());
    let paths = CursorUsagePaths {
        chats: home.join(".cursor/chats"),
        auth: config.join("auth.json"),
        database: config.join("User/globalStorage/state.vscdb"),
    };
    let strategy = CursorStrategy::new(
        b"local-diagnostic-only".to_vec(),
        home.join(".cursor/projects"),
        super::super::SkillBook::new(),
        Arc::new(|_, _| 1),
    )
    .with_usage(paths);
    let spec = strategy
        .discover(DiscoveryBudget::new(usize::MAX, 200))
        .unwrap()
        .into_iter()
        .find(|s| s.stream_key == STREAM_USAGE)
        .unwrap();
    let writer = PipelineWriter::start(PipelineStore::open_in_memory().unwrap());
    if historical {
        writer.rebuild(crate::local_store::pipeline::reconstruction::RebuildAction::Begin(env!("CARGO_PKG_VERSION").into())).unwrap();
    }
    let id = writer
        .register_source(RegisterSource {
            harness_id: spec.harness_id,
            source_key: spec.source_key,
            source_kind: spec.source_kind,
            locator_ref: spec.locator_ref,
            stream_key: spec.stream_key,
            cursor_kind: spec.cursor_kind,
            cursor_json: "{}".into(),
            decoder_state_version: 1,
            decoder_state_json: "{}".into(),
            observed_boundary_json: "{}".into(),
            next_poll_at: None,
        })
        .unwrap();
    let mut completed = false;
    for page in 0..1000 {
        match AcquisitionRunner::default()
            .run_once(&writer, &strategy, id, DEFAULT_READ_BUDGET)
            .unwrap()
        {
            RunOutcome::Committed(stats) | RunOutcome::Empty(stats) if !stats.has_more => {
                completed = true;
                break;
            }
            RunOutcome::Committed(_) => {}
            RunOutcome::CasRejected { error, .. } => panic!("live CAS rejected: {error}"),
            RunOutcome::StreamStopped { reason, .. } => panic!("live stream stopped: {reason}"),
            _ => panic!("live read failed without committing"),
        }
        if page % 25 == 0 {
            let checkpoint = writer.load_source_checkpoint(id).unwrap();
            let cursor: Value = serde_json::from_str(&checkpoint.cursor_json).unwrap();
            eprintln!("Cursor probe: {} batches; next page {}; {} local events", page+1, cursor["page"],writer.pending_upload_count().unwrap());
        }
    }
    assert!(completed, "scan must finish within probe limit");
    let count = writer.pending_upload_count().unwrap();
    assert!(count > 0, "no confirmed local usage observed");
    let rollup = writer
        .drain_metrics_consumer(Consumer::Day, 256, DEFAULT_LEASE_MS)
        .unwrap();
    assert!(rollup.applied > 0);
    let now = chrono::Utc::now().timestamp_millis();
    let start = (now + 28_800_000).div_euclid(86_400_000) * 86_400_000 - 28_800_000;
    while writer.drain_metrics_consumer(Consumer::Day, 256, DEFAULT_LEASE_MS).unwrap().applied > 0 {}
    let summary = writer.query_usage_summary(crate::local_store::pipeline::buckets::Grain::Day, if historical {0} else {start}, start + 86_400_000, Some("cursor")).unwrap();
    assert!(summary.total_tokens.value.is_some_and(|n| n > 0));
    eprintln!("confirmed local Cursor token total: {}", summary.total_tokens.value.unwrap());
    eprintln!("isolated Cursor ingestion: {count} local events pending, {} daily tasks applied; no upload or live database writes",rollup.applied);
}
