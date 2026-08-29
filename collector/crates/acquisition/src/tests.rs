use std::fs::{self, OpenOptions};
use std::io::Write;
use std::sync::Arc;
use std::time::Duration;

use adapter_mock::MockAdapter;
use adapter_sdk::{
    AdapterError, AdapterHealth, AdapterManifest, AgentAdapter, ProbeContext, ProbeReport,
    RawFrame, SetupContext, SetupPlan, SourceContext, SourceSpec,
};
use async_trait::async_trait;
use collector_core::Collector;
use protocol::SourceCheckpointStatus;
use tokio::sync::Notify;
use tokio::time::sleep;
use uploader::{body_contains_canary, MemoryIngest, RetryPolicy, Uploader};
use wal_spool::{contains_bytes, InjectedKeyProvider, SpoolLimits, WalStore};

use crate::jsonl::JsonlTailer;
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
