use std::fs::{self, OpenOptions};
use std::io::Write;
use std::sync::Arc;
use std::time::Duration;

use adapter_mock::MockAdapter;
use adapter_sdk::{
    AdapterError, AdapterHealth, AdapterManifest, AgentAdapter, ProbeContext, ProbeReport,
    RawFrame, SetupContext, SetupPlan, SourceContext, SourceKind, SourceSpec,
};
use async_trait::async_trait;
use collector_core::Collector;
use protocol::SourceCheckpointStatus;
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tokio::net::TcpListener;
use tokio::sync::Notify;
use tokio::time::sleep;
use uploader::{body_contains_canary, MemoryIngest, RetryPolicy, Uploader};
use wal_spool::{contains_bytes, InjectedKeyProvider, SpoolLimits, WalStore};

use crate::drivers::{
    DriverBatch, RemoteApiDriver, RemotePollError, RuntimeStreamDriver, SecretResolver,
    SqliteAdapterPlan, SqliteSnapshotDriver,
};
use crate::jsonl::JsonlTailer;
use crate::otlp::{OtlpReceiverDriver, OtlpSignal, DEFAULT_OTLP_PAYLOAD_LIMIT};
use crate::pipeline::IngestPipeline;

const INSTALL: &str = "ins_00000000000000000000000001";
const CANARIES: &[&str] = &[
    "TOKSHOW_TEST_PROMPT_SECRET",
    "TOKSHOW_TEST_SOURCE_CODE_SECRET",
    "TOKSHOW_TEST_ABSOLUTE_PATH_SECRET",
    "TOKSHOW_TEST_API_KEY_SECRET",
];

fn write_lines(path: &std::path::Path, lines: &[&str]) {
    let mut file = OpenOptions::new()
        .create(true)
        .write(true)
        .truncate(true)
        .open(path)
        .unwrap();
    for line in lines {
        writeln!(file, "{line}").unwrap();
    }
    file.flush().unwrap();
}

fn open_wal(dir: &std::path::Path) -> WalStore {
    let keys = Arc::new(InjectedKeyProvider::new([0x55; 32]));
    WalStore::open_with_limits(dir.join("state"), keys, SpoolLimits::for_tests()).unwrap()
}

async fn mock_collector() -> Collector {
    let mut collector = Collector::new(INSTALL);
    collector
        .register_adapter(Arc::new(MockAdapter::new()))
        .unwrap();
    collector.probe_all().await;
    collector
        .discover_sources("dev.tokenshow.adapter.mock")
        .await
        .unwrap();
    collector
}

#[tokio::test]
async fn idle_files_sharing_a_source_do_not_rewrite_checkpoints() {
    let dir = tempfile::tempdir().unwrap();
    let collector = mock_collector().await;
    let mut wal = open_wal(dir.path());
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");
    let paths = [dir.path().join("a.jsonl"), dir.path().join("b.jsonl")];
    for path in &paths { write_lines(path, &["{}"]); }
    for path in &paths {
        let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "mock-sessions", path);
        pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();
    }
    let before = wal.spool_bytes();
    for _ in 0..3 {
        for path in &paths {
            let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "mock-sessions", path);
            tailer.restore_matching(&wal);
            pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();
        }
    }
    assert_eq!(wal.spool_bytes(), before);
}

struct SlowAdapter {
    inner: MockAdapter,
    started: Arc<Notify>,
}

#[async_trait]
impl AgentAdapter for SlowAdapter {
    fn manifest(&self) -> &AdapterManifest {
        self.inner.manifest()
    }

    async fn probe(&self, ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        self.inner.probe(ctx).await
    }

    async fn setup_plan(&self, ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        self.inner.setup_plan(ctx).await
    }

    async fn discover_sources(&self, ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        self.inner.discover_sources(ctx).await
    }

    async fn decode(
        &self,
        frame: RawFrame,
    ) -> Result<Vec<adapter_sdk::NormalizedEvent>, AdapterError> {
        self.started.notify_one();
        sleep(Duration::from_millis(30)).await;
        self.inner.decode(frame).await
    }

    async fn health(&self) -> AdapterHealth {
        self.inner.health().await
    }
}

#[tokio::test]
async fn incomplete_line_does_not_advance_checkpoint() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("sessions.jsonl");
    fs::write(&path, "{\"type\":\"session_started\",\"occurredAt\":\"2026-08-29T12:00:00.000Z\",\"sessionId\":\"s\"").unwrap();
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let poll = tailer.poll(wal_spool::Backpressure::Normal, false).unwrap();
    assert!(poll.frames.is_empty());
    assert_eq!(poll.next_checkpoint.offset, 0);

    let mut file = OpenOptions::new().append(true).open(&path).unwrap();
    writeln!(file, "}}").unwrap();
    drop(file);
    let poll = tailer.poll(wal_spool::Backpressure::Normal, false).unwrap();
    assert_eq!(poll.frames.len(), 1);
    assert!(poll.next_checkpoint.offset > 0);
}

#[tokio::test]
async fn truncate_creates_new_generation_and_reads_from_start() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("sessions.jsonl");
    let long_id = format!("sess-{}", "a".repeat(80));
    write_lines(
        &path,
        &[
            &format!(
                r#"{{"type":"session_started","occurredAt":"2026-08-29T12:00:00.000Z","sessionId":"{long_id}"}}"#
            ),
            r#"{"type":"session_started","occurredAt":"2026-08-29T12:00:01.000Z","sessionId":"b"}"#,
        ],
    );
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let first = tailer.poll(wal_spool::Backpressure::Normal, false).unwrap();
    assert_eq!(first.frames.len(), 2);
    let file = OpenOptions::new().write(true).open(&path).unwrap();
    file.set_len(first.frames[0].payload.len() as u64 + 1)
        .unwrap();
    drop(file);
    let second = tailer.poll(wal_spool::Backpressure::Normal, false).unwrap();
    assert_eq!(second.status, SourceCheckpointStatus::Truncated);
    assert_eq!(second.frames.len(), 1);
    assert!(second.next_checkpoint.generation > first.next_checkpoint.generation);
}

#[tokio::test]
async fn rotate_replaced_file_keeps_old_checkpoint_identity() {
    let dir = tempfile::tempdir().unwrap();
    let state = dir.path().join("state");
    let mut wal = open_wal(dir.path());
    let path = dir.path().join("sessions.jsonl");
    write_lines(
        &path,
        &[
            r#"{"type":"session_started","occurredAt":"2026-08-29T12:00:00.000Z","sessionId":"old"}"#,
        ],
    );
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let collector = mock_collector().await;
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");
    let first = pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();
    let old_identity = first.next_checkpoint.file_identity.clone();
    fs::remove_file(&path).unwrap();
    write_lines(
        &path,
        &[
            r#"{"type":"session_started","occurredAt":"2026-08-29T12:00:03.000Z","sessionId":"new"}"#,
        ],
    );
    let second = pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();
    assert_eq!(
        second.status,
        SourceCheckpointStatus::Rotated,
        "replaced file should rotate, got {:?}",
        second.status
    );
    assert!(wal.checkpoint("mock-sessions", &old_identity).is_some());
    assert!(wal
        .checkpoint("mock-sessions", &second.next_checkpoint.file_identity)
        .is_some());
    let _ = state;
}

#[tokio::test]
async fn duplicate_scan_is_idempotent_on_event_id() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_wal(dir.path());
    let path = dir.path().join("sessions.jsonl");
    fs::write(
        dir.path().join("src.jsonl"),
        include_str!("../fixtures/sessions.jsonl"),
    )
    .unwrap();
    fs::copy(dir.path().join("src.jsonl"), &path).unwrap();
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let collector = mock_collector().await;
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");
    pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();
    let first = wal.unacked_count();
    assert!(first >= 3);
    tailer.reset_for_rescan();
    pipeline.ingest(&mut tailer, &mut wal, true).await.unwrap();
    assert_eq!(wal.unacked_count(), first);

    let transport = MemoryIngest::default();
    let mut uploader =
        Uploader::new(INSTALL, transport.clone()).with_retry(RetryPolicy::for_tests());
    uploader.flush(&mut wal).await.unwrap();
    tailer.reset_for_rescan();
    pipeline.ingest(&mut tailer, &mut wal, true).await.unwrap();
    assert_eq!(wal.unacked_count(), 0);
}

#[tokio::test]
async fn canary_never_lands_in_wal_http_or_log() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_wal(dir.path());
    let path = dir.path().join("canary.jsonl");
    fs::write(&path, include_str!("../fixtures/canary.jsonl")).unwrap();
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let collector = mock_collector().await;
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");
    pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();

    let raw = wal.raw_wal_bytes().unwrap();
    let decoded = wal.decoded_payload_bytes().unwrap();
    for canary in CANARIES {
        assert!(!contains_bytes(&raw, canary), "raw wal leaked {canary}");
        assert!(
            !contains_bytes(&decoded, canary),
            "decoded wal leaked {canary}"
        );
    }
    let logged = pipeline.log.lines().join("\n");
    for canary in CANARIES {
        assert!(!logged.contains(canary), "safe log leaked {canary}");
    }

    let transport = MemoryIngest::default();
    let mut uploader =
        Uploader::new(INSTALL, transport.clone()).with_retry(RetryPolicy::for_tests());
    uploader.flush(&mut wal).await.unwrap();
    for canary in CANARIES {
        assert!(!body_contains_canary(&transport.bodies(), canary));
    }
    assert!(
        wal.unacked_count() <= 3,
        "canary records must not inflate the unacked spool"
    );
}

#[tokio::test]
async fn disabled_adapter_stops_source_before_checkpoint_advances() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_wal(dir.path());
    let path = dir.path().join("disabled.jsonl");
    fs::write(&path, include_str!("../fixtures/sessions.jsonl")).unwrap();
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let mut collector = mock_collector().await;
    collector.disable("dev.tokenshow.adapter.mock").unwrap();
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");

    let error = pipeline
        .ingest(&mut tailer, &mut wal, false)
        .await
        .expect_err("disabled Adapter must stop acquisition");
    assert_eq!(error.to_string(), "adapter_disabled");
    assert!(wal.latest_checkpoint("mock-sessions").is_none());
    assert_eq!(wal.unacked_count(), 0);
}

#[tokio::test]
async fn bound_batch_rejects_source_spoof_without_checkpoint() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_wal(dir.path());
    let collector = mock_collector().await;
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");
    let batch = DriverBatch {
        frames: vec![RawFrame::jsonl(
            INSTALL,
            "spoofed-source",
            "0",
            br#"{"type":"session_started","occurredAt":"2026-08-29T12:00:00.000Z","sessionId":"s"}"#,
        )],
        cursor: "1".into(),
        driver_checkpoint: None,
    };

    let error = pipeline
        .ingest_bound_batch(
            "mock-sessions",
            SourceKind::JsonlTail,
            batch,
            &mut wal,
            false,
        )
        .await
        .unwrap_err();

    assert_eq!(error.to_string(), "source_mismatch");
    assert!(wal.latest_checkpoint("mock-sessions").is_none());
    assert_eq!(wal.unacked_count(), 0);
}

#[tokio::test]
async fn disabling_during_decode_prevents_wal_commit() {
    let dir = tempfile::tempdir().unwrap();
    let mut wal = open_wal(dir.path());
    let path = dir.path().join("hot-disable.jsonl");
    fs::write(&path, include_str!("../fixtures/sessions.jsonl")).unwrap();
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let started = Arc::new(Notify::new());
    let mut collector = Collector::new(INSTALL);
    collector
        .register_adapter(Arc::new(SlowAdapter {
            inner: MockAdapter::new(),
            started: Arc::clone(&started),
        }))
        .unwrap();
    collector.probe_all().await;
    collector
        .discover_sources("dev.tokenshow.adapter.mock")
        .await
        .unwrap();
    let control = collector.control("dev.tokenshow.adapter.mock").unwrap();
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");

    let ingest = pipeline.ingest(&mut tailer, &mut wal, false);
    let disable = async {
        started.notified().await;
        control.disable();
    };
    let (result, ()) = tokio::join!(ingest, disable);
    assert_eq!(result.unwrap_err().to_string(), "adapter_disabled");
    assert!(wal.latest_checkpoint("mock-sessions").is_none());
    assert_eq!(wal.unacked_count(), 0);
}

#[tokio::test]
async fn hard_backpressure_skips_historical_scan() {
    let dir = tempfile::tempdir().unwrap();
    let keys = Arc::new(InjectedKeyProvider::new([0x66; 32]));
    let mut limits = SpoolLimits::for_tests();
    limits.hard_events = 1;
    limits.soft_events = 1;
    let mut wal = WalStore::open_with_limits(dir.path().join("state"), keys, limits).unwrap();
    let path = dir.path().join("sessions.jsonl");
    fs::write(&path, include_str!("../fixtures/sessions.jsonl")).unwrap();
    let mut tailer = JsonlTailer::new(INSTALL, "mock-sessions", "AGENT_CONFIG_HOME", &path);
    let collector = mock_collector().await;
    let pipeline = IngestPipeline::new(&collector, "dev.tokenshow.adapter.mock");
    pipeline.ingest(&mut tailer, &mut wal, false).await.unwrap();
    tailer.reset_for_rescan();
    let poll = pipeline.ingest(&mut tailer, &mut wal, true).await.unwrap();
    assert!(poll.skipped);
}

struct TestSecrets;

impl SecretResolver for TestSecrets {
    fn resolve(&self, secret_ref: &str) -> Result<Vec<u8>, String> {
        assert_eq!(secret_ref, "secret://cursor/test");
        Ok(b"mock-api-key".to_vec())
    }
}

async fn mock_http(response: &'static str) -> (String, tokio::task::JoinHandle<String>) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let task = tokio::spawn(async move {
        let (mut socket, _) = listener.accept().await.unwrap();
        let mut request = vec![0_u8; 8192];
        let read = socket.read(&mut request).await.unwrap();
        socket.write_all(response.as_bytes()).await.unwrap();
        String::from_utf8_lossy(&request[..read]).into_owned()
    });
    (format!("http://{address}/events"), task)
}

#[tokio::test]
async fn remote_api_uses_secret_cursor_overlap_and_retry_after() {
    let body = r#"{"events":[],"next_cursor":"cursor-2"}"#;
    let response = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: {}\r\nX-Next-Cursor: cursor-2\r\n\r\n{}",
        body.len(), body
    );
    let leaked: &'static str = Box::leak(response.into_boxed_str());
    let (endpoint, request) = mock_http(leaked).await;
    let mut driver = RemoteApiDriver::new(
        INSTALL,
        "cursor-admin-api",
        &endpoint,
        "127.0.0.1",
        "secret://cursor/test",
        Arc::new(TestSecrets),
        1024,
    )
    .unwrap();
    driver.restore_cursor("cursor-1", 1_000);
    let batch = driver.poll(1_600).await.unwrap();
    assert_eq!(batch.cursor, "cursor-2");
    let request = request.await.unwrap();
    assert!(request.contains("from=700"));
    assert!(request.contains("until=1600"));
    assert!(request.contains("cursor=cursor-1"));
    assert!(
        request.contains("authorization: Bearer mock-api-key")
            || request.contains("Authorization: Bearer mock-api-key")
    );

    let (endpoint, _) =
        mock_http("HTTP/1.1 429 Too Many Requests\r\nRetry-After: 17\r\nContent-Length: 0\r\n\r\n")
            .await;
    let mut limited = RemoteApiDriver::new(
        INSTALL,
        "cursor-admin-api",
        &endpoint,
        "127.0.0.1",
        "secret://cursor/test",
        Arc::new(TestSecrets),
        1024,
    )
    .unwrap();
    assert_eq!(
        limited.poll(2_000).await,
        Err(RemotePollError::RateLimited(Duration::from_secs(17)))
    );
}

#[test]
fn zcode_v3_snapshot_maps_sessions_and_model_usage() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("db.sqlite");
    let connection = rusqlite::Connection::open(&path).unwrap();
    connection
        .execute_batch(
            "PRAGMA user_version = 0;
             CREATE TABLE session(id TEXT PRIMARY KEY, project_id TEXT, workspace_id TEXT, parent_id TEXT, slug TEXT, directory TEXT, path TEXT, title TEXT, version TEXT, share_url TEXT, summary_additions INTEGER, summary_deletions INTEGER, summary_files INTEGER, summary_diffs TEXT, revert TEXT, permission TEXT, time_created INTEGER, time_updated INTEGER, time_compacting INTEGER, time_archived INTEGER, task_type TEXT, title_source TEXT, title_message_id TEXT, time_title_updated INTEGER, trace_id TEXT);
             CREATE TABLE model_usage(id TEXT PRIMARY KEY, logical_request_id TEXT, attempt_index INTEGER, session_id TEXT, turn_id TEXT, trace_id TEXT, span_id TEXT, assistant_message_id TEXT, parent_user_message_id TEXT, query_source TEXT, provider_id TEXT, model_id TEXT, variant TEXT, agent TEXT, mode TEXT, task_type TEXT, status TEXT, started_at INTEGER, first_token_at INTEGER, completed_at INTEGER, duration_ms INTEGER, time_to_first_token_ms INTEGER, finish_reason TEXT, tool_call_count INTEGER, input_tokens INTEGER, output_tokens INTEGER, reasoning_tokens INTEGER, cache_creation_input_tokens INTEGER, cache_read_input_tokens INTEGER, provider_total_tokens INTEGER, computed_total_tokens INTEGER, retry_count INTEGER, retryable INTEGER, cancelled_by_user INTEGER, context_exceeded INTEGER, error_type TEXT, error_code TEXT, error_message TEXT, raw_usage_json TEXT, provider_metadata_json TEXT);
             INSERT INTO session(id, time_created) VALUES('sess_a', 1788594825649);
             INSERT INTO model_usage(id, session_id, provider_id, model_id, status, completed_at, tool_call_count, input_tokens, output_tokens, computed_total_tokens) VALUES('mu_1', 'sess_a', 'builtin:zai', 'glm-5.3', 'completed', 1788606721923, 2, 100, 20, 120);
             INSERT INTO model_usage(id, session_id, provider_id, model_id, status, completed_at) VALUES('mu_failed', 'sess_a', 'builtin:zai', 'glm-5.3', 'error', 1788606721000);",
        )
        .unwrap();
    drop(connection);

    let detected = crate::detect_zcode_sqlite(&path).unwrap();
    assert_eq!(detected.fingerprint, "zcode-sqlite-v3-uv0");

    let mut driver = SqliteSnapshotDriver::new(
        INSTALL,
        "zcode-sqlite",
        &path,
        SqliteAdapterPlan::ZcodeV3,
        "zcode-sqlite-v3-uv0",
    )
    .unwrap();
    let batch = driver.poll().unwrap();
    let payload: serde_json::Value = serde_json::from_slice(&batch.frames[0].payload).unwrap();
    assert_eq!(payload["fingerprint"], "zcode-sqlite-v3-uv0");
    let records = payload["records"].as_array().unwrap();
    assert_eq!(records.len(), 2, "failed usage rows are filtered out");
    let session = records
        .iter()
        .find(|record| record["type"] == "session")
        .unwrap();
    assert_eq!(session["sessionId"], "sess_a");
    assert!(
        session["timestamp"]
            .as_str()
            .unwrap()
            .starts_with("2026-09-05T07:53:45"),
        "time_created ms must become RFC3339: {:?}",
        session["timestamp"]
    );
    let usage = records
        .iter()
        .find(|record| record["type"] == "step_finish")
        .unwrap();
    assert_eq!(usage["sessionId"], "sess_a");
    assert_eq!(usage["provider"], "builtin:zai");
    assert_eq!(usage["model"], "glm-5.3");
    assert_eq!(usage["inputTokens"], 100);
    assert_eq!(usage["outputTokens"], 20);
    assert_eq!(usage["totalTokens"], 120);
    assert_eq!(usage["toolCount"], 2);
    assert!(
        usage["timestamp"]
            .as_str()
            .unwrap()
            .starts_with("2026-09-05T11:12:01"),
        "completed_at ms must become RFC3339: {:?}",
        usage["timestamp"]
    );

    let second = driver.poll().unwrap();
    let second_payload: serde_json::Value =
        serde_json::from_slice(&second.frames[0].payload).unwrap();
    assert_eq!(
        second_payload["records"].as_array().unwrap().len(),
        0,
        "cursors must advance past polled rows"
    );
}

#[test]
fn sqlite_snapshot_executes_only_fixed_plan_for_trusted_fingerprint() {
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("zcode.sqlite");
    let connection = rusqlite::Connection::open(&path).unwrap();
    connection
        .execute_batch(
            "PRAGMA user_version = 9;\
             CREATE TABLE sessions(id INTEGER PRIMARY KEY, created_at TEXT, model TEXT);\
             CREATE TABLE step_metrics(id INTEGER PRIMARY KEY, session_id INTEGER, finished_at TEXT, input_tokens INTEGER, output_tokens INTEGER, total_tokens INTEGER, tool_count INTEGER, skill_name TEXT);\
             INSERT INTO sessions VALUES(1, '2026-08-30T13:00:00Z', 'glm-4.5');\
             INSERT INTO step_metrics VALUES(2, 1, '2026-08-30T13:00:01Z', 90, 30, 120, 2, 'review');",
        )
        .unwrap();
    drop(connection);
    assert!(SqliteSnapshotDriver::new(
        INSTALL,
        "zcode-sqlite",
        &path,
        SqliteAdapterPlan::ZcodeV2,
        "attacker-selected-schema",
    )
    .is_err());
    let mut driver = SqliteSnapshotDriver::new(
        INSTALL,
        "zcode-sqlite",
        &path,
        SqliteAdapterPlan::ZcodeV2,
        "zcode-sqlite-v2-uv9",
    )
    .unwrap();
    let batch = driver.poll().unwrap();
    let payload: serde_json::Value = serde_json::from_slice(&batch.frames[0].payload).unwrap();
    assert_eq!(payload["fingerprint"], "zcode-sqlite-v2-uv9");
    assert_eq!(payload["records"].as_array().unwrap().len(), 2);
    assert_eq!(batch.cursor, "1:2");
    let second = driver.poll().unwrap();
    let payload: serde_json::Value = serde_json::from_slice(&second.frames[0].payload).unwrap();
    assert!(payload["records"].as_array().unwrap().is_empty());

    let connection = rusqlite::Connection::open(&path).unwrap();
    connection.pragma_update(None, "user_version", 7).unwrap();
    drop(connection);
    assert!(driver.poll().is_err());
}

#[test]
fn otlp_json_envelopes_normalize_metrics_and_logs() {
    let mut otlp =
        OtlpReceiverDriver::new(INSTALL, "otlp", "127.0.0.1", DEFAULT_OTLP_PAYLOAD_LIMIT).unwrap();
    let metrics = otlp
        .accept_http_path(
            "/v1/metrics",
            "application/json; charset=utf-8",
            include_bytes!("../fixtures/otlp-metrics.json"),
        )
        .unwrap();
    let metric: serde_json::Value = serde_json::from_slice(&metrics.payload).unwrap();
    assert_eq!(metric["signal"], "metrics");
    assert_eq!(metric["name"], "grok_code.token.usage");
    assert_eq!(metric["metricType"], "sum");
    assert_eq!(metric["value"], 15);
    assert_eq!(metric["temporality"], "cumulative");
    assert_eq!(metric["aggregationTemporality"], 2);
    assert_eq!(metric["startTimeUnixNano"], "1788084000000000000");
    assert_eq!(metric["timeUnixNano"], "1788084060000000000");
    assert_eq!(metric["timestamp"], "2026-08-30T10:01:00Z");
    assert_eq!(metric["endTimeUnixNano"], "1788084060000000000");
    assert_eq!(metric["attributes"]["input_tokens"], 7);
    assert_eq!(
        metric["resource"]["attributes"]["service.name"],
        "grok-build"
    );
    assert_eq!(metric["scope"]["name"], "grok.telemetry");

    let logs = otlp
        .accept_json(
            OtlpSignal::Logs,
            include_bytes!("../fixtures/otlp-logs.json"),
        )
        .unwrap();
    let log: serde_json::Value = serde_json::from_slice(&logs.payload).unwrap();
    assert_eq!(log["signal"], "logs");
    assert_eq!(log["name"], "grok_code.tool.usage");
    assert_eq!(log["attributes"]["tool.name"], "editor");
    assert_eq!(log["resource"]["attributes"]["service.name"], "grok-build");
    assert_eq!(log["scope"]["version"], "1.2.3");
}

#[tokio::test]
async fn normalized_otlp_frame_drives_real_grok_adapter_with_rfc3339_time() {
    let mut otlp = OtlpReceiverDriver::new(
        INSTALL,
        adapter_grok_build::OTLP_SOURCE_ID,
        "127.0.0.1",
        DEFAULT_OTLP_PAYLOAD_LIMIT,
    )
    .unwrap();
    let frame = otlp
        .accept_json(
            OtlpSignal::Metrics,
            include_bytes!("../fixtures/otlp-metrics.json"),
        )
        .unwrap();
    let adapter = adapter_grok_build::GrokBuildAdapter::for_version(
        "1.0.0",
        b"otlp-end-to-end-device-key".to_vec(),
    );
    let events = adapter.decode(frame).await.unwrap();
    assert_eq!(events.len(), 1);
    assert_eq!(events[0].occurred_at, "2026-08-30T10:01:00Z");
    assert!(matches!(
        events[0].payload,
        adapter_sdk::EventPayload::ModelUsageRecorded(_)
    ));
}

#[test]
fn otlp_protobuf_fixtures_match_json_normalization() {
    let mut otlp =
        OtlpReceiverDriver::new(INSTALL, "otlp", "localhost", DEFAULT_OTLP_PAYLOAD_LIMIT).unwrap();
    let json_metric = otlp
        .accept_json(
            OtlpSignal::Metrics,
            include_bytes!("../fixtures/otlp-metrics.json"),
        )
        .unwrap();
    let protobuf_metric = otlp
        .accept_protobuf(
            OtlpSignal::Metrics,
            include_bytes!("../fixtures/otlp-metrics.pb"),
        )
        .unwrap();
    let json_value: serde_json::Value = serde_json::from_slice(&json_metric.payload).unwrap();
    let protobuf_value: serde_json::Value =
        serde_json::from_slice(&protobuf_metric.payload).unwrap();
    assert_eq!(protobuf_value, json_value);

    let json_log = otlp
        .accept_json(
            OtlpSignal::Logs,
            include_bytes!("../fixtures/otlp-logs.json"),
        )
        .unwrap();
    let protobuf_log = otlp
        .accept_http(
            OtlpSignal::Logs,
            "application/x-protobuf",
            include_bytes!("../fixtures/otlp-logs.pb"),
        )
        .unwrap();
    let json_value: serde_json::Value = serde_json::from_slice(&json_log.payload).unwrap();
    let protobuf_value: serde_json::Value = serde_json::from_slice(&protobuf_log.payload).unwrap();
    assert_eq!(protobuf_value, json_value);
}

#[test]
fn otlp_rejects_flat_json_wrong_signal_and_limits() {
    assert!(OtlpReceiverDriver::new(INSTALL, "otlp", "0.0.0.0", 1024).is_err());
    assert!(
        OtlpReceiverDriver::new(INSTALL, "otlp", "127.0.0.1", DEFAULT_OTLP_PAYLOAD_LIMIT + 1)
            .is_err()
    );

    let mut small = OtlpReceiverDriver::new(INSTALL, "otlp", "127.0.0.1", 8).unwrap();
    assert!(small
        .accept_json(OtlpSignal::Metrics, b"123456789")
        .is_err());

    let mut otlp =
        OtlpReceiverDriver::new(INSTALL, "otlp", "127.0.0.1", DEFAULT_OTLP_PAYLOAD_LIMIT).unwrap();
    assert!(otlp
        .accept_json(
            OtlpSignal::Metrics,
            br#"{"name":"grok_code.token.usage","value":15}"#,
        )
        .is_err());
    assert!(otlp
        .accept_json(
            OtlpSignal::Logs,
            include_bytes!("../fixtures/otlp-metrics.json"),
        )
        .is_err());
    assert!(otlp
        .accept_http(OtlpSignal::Metrics, "text/plain", b"not otlp")
        .is_err());

    let mut limited = OtlpReceiverDriver::new_with_limits(INSTALL, "otlp", "::1", 4096, 1).unwrap();
    assert!(limited
        .accept_json(
            OtlpSignal::Metrics,
            include_bytes!("../fixtures/otlp-metrics.json"),
        )
        .is_err());
}

#[test]
fn runtime_stream_is_monotonic() {
    let mut runtime = RuntimeStreamDriver::new(INSTALL, "runtime", "stream.v1", 1024).unwrap();
    runtime.push("stream.v1", 1, b"{}".to_vec()).unwrap();
    assert!(runtime.push("stream.v1", 1, b"{}".to_vec()).is_err());
    assert!(runtime.push("other", 2, b"{}".to_vec()).is_err());
    assert_eq!(runtime.poll(10).cursor, "1");
}
