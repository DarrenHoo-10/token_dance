#![forbid(unsafe_code)]

pub mod detect;
pub mod runtime;
pub mod upload;

pub use detect::{detect_from_home, detect_local};
pub use runtime::{collect_tick, CollectReport};

use std::collections::BTreeMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use acquisition::{
    DriverBatch, DriverInstance, DriverRegistry, DriverTaskStatus, IngestPipeline, JsonlTailer,
    OtlpReceiverDriver, RemoteApiDriver, RuntimeStreamDriver, SecretResolver, SqliteAdapterPlan,
    SqliteSnapshotDriver, DEFAULT_OTLP_PAYLOAD_LIMIT,
};
use adapter_sdk::{
    AdapterError, AdapterHealth, AgentAdapter, NormalizedEvent, ProbeContext, ProbeReport,
    RawFrame, SetupContext, SetupPlan, SourceContext, SourceSpec,
};
use async_trait::async_trait;
use collector_core::Collector;
use config_executor::{
    ApplyReport, EncryptedBackupStore, ExecutorError, SetupPlanExecutor, SetupVerifier,
};
use wal_spool::WalStore;

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub enum OfficialAgent {
    Codex,
    ClaudeCode,
    GrokBuild,
    Cursor,
    Zcode,
    DeepseekHarness,
    Pi,
}

impl OfficialAgent {
    pub const ALL: [Self; 7] = [
        Self::Codex,
        Self::ClaudeCode,
        Self::GrokBuild,
        Self::Cursor,
        Self::Zcode,
        Self::DeepseekHarness,
        Self::Pi,
    ];
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DetectedCursorMode {
    EnterpriseApi,
    TeamAdminApi,
    PersonalLocal,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AgentDetection {
    pub version: String,
    pub mode: Option<String>,
    pub otlp_available: bool,
    pub sqlite_fingerprint: Option<String>,
    pub runtime_verified: bool,
    pub cursor_mode: Option<DetectedCursorMode>,
    pub secret_ref: Option<String>,
}

impl AgentDetection {
    pub fn installed(version: impl Into<String>) -> Self {
        Self {
            version: version.into(),
            mode: None,
            otlp_available: false,
            sqlite_fingerprint: None,
            runtime_verified: false,
            cursor_mode: None,
            secret_ref: None,
        }
    }
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct DetectedSourceConfig {
    pub endpoint: Option<String>,
    pub path: Option<PathBuf>,
    pub secret_ref: Option<String>,
}

#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct DetectionSnapshot {
    agents: BTreeMap<OfficialAgent, AgentDetection>,
    sources: BTreeMap<(OfficialAgent, String), DetectedSourceConfig>,
}

impl DetectionSnapshot {
    pub fn insert(&mut self, agent: OfficialAgent, detection: AgentDetection) {
        self.agents.insert(agent, detection);
    }

    pub fn with(mut self, agent: OfficialAgent, detection: AgentDetection) -> Self {
        self.insert(agent, detection);
        self
    }

    pub fn get(&self, agent: OfficialAgent) -> Option<&AgentDetection> {
        self.agents.get(&agent)
    }

    pub fn configure_source(
        &mut self,
        agent: OfficialAgent,
        source_id: impl Into<String>,
        config: DetectedSourceConfig,
    ) {
        self.sources.insert((agent, source_id.into()), config);
    }

    pub fn with_source(
        mut self,
        agent: OfficialAgent,
        source_id: impl Into<String>,
        config: DetectedSourceConfig,
    ) -> Self {
        self.configure_source(agent, source_id, config);
        self
    }

    pub fn source(&self, agent: OfficialAgent, source_id: &str) -> Option<&DetectedSourceConfig> {
        self.sources.get(&(agent, source_id.to_owned()))
    }

    pub fn is_installed(&self, agent: OfficialAgent) -> bool {
        self.agents.contains_key(&agent)
    }

    pub fn iter_sources(
        &self,
    ) -> impl Iterator<Item = (OfficialAgent, &str, &DetectedSourceConfig)> {
        self.sources
            .iter()
            .map(|((agent, source_id), config)| (*agent, source_id.as_str(), config))
    }
}

pub struct OfficialAdapters {
    pub codex: Arc<dyn AgentAdapter>,
    pub claude_code: Arc<dyn AgentAdapter>,
    pub grok_build: Arc<dyn AgentAdapter>,
    pub cursor: Arc<dyn AgentAdapter>,
    pub zcode: Arc<dyn AgentAdapter>,
    pub deepseek_harness: Arc<dyn AgentAdapter>,
    pub pi: Arc<dyn AgentAdapter>,
}

impl OfficialAdapters {
    pub fn production(snapshot: &DetectionSnapshot, hmac_key: &[u8]) -> Result<Self, AdapterError> {
        Ok(Self {
            codex: codex_adapter(snapshot.get(OfficialAgent::Codex), hmac_key),
            claude_code: claude_adapter(snapshot.get(OfficialAgent::ClaudeCode), hmac_key),
            grok_build: grok_adapter(snapshot.get(OfficialAgent::GrokBuild), hmac_key),
            cursor: cursor_adapter(snapshot.get(OfficialAgent::Cursor), hmac_key)?,
            zcode: zcode_adapter(snapshot.get(OfficialAgent::Zcode), hmac_key),
            deepseek_harness: deepseek_adapter(
                snapshot.get(OfficialAgent::DeepseekHarness),
                hmac_key,
            ),
            pi: pi_adapter(snapshot.get(OfficialAgent::Pi), hmac_key),
        })
    }

    pub fn into_vec(self) -> Vec<Arc<dyn AgentAdapter>> {
        vec![
            self.codex,
            self.claude_code,
            self.grok_build,
            self.cursor,
            self.zcode,
            self.deepseek_harness,
            self.pi,
        ]
    }
}

fn installation_guard(adapter: Arc<dyn AgentAdapter>, installed: bool) -> Arc<dyn AgentAdapter> {
    Arc::new(DetectionGuard { adapter, installed })
}

fn codex_adapter(detection: Option<&AgentDetection>, key: &[u8]) -> Arc<dyn AgentAdapter> {
    let installed = detection.is_some();
    let detection = detection
        .cloned()
        .unwrap_or_else(|| AgentDetection::installed("0"));
    let mode = detection.mode.unwrap_or_else(|| "unknown".into());
    installation_guard(
        Arc::new(
            adapter_codex::CodexAdapter::new(detection.version, mode, key.to_vec())
                .with_otel(detection.otlp_available),
        ),
        installed,
    )
}

fn claude_adapter(detection: Option<&AgentDetection>, key: &[u8]) -> Arc<dyn AgentAdapter> {
    match detection {
        Some(item) => Arc::new(adapter_claude::ClaudeAdapter::for_version(
            item.version.clone(),
            key.to_vec(),
        )),
        None => Arc::new(adapter_claude::ClaudeAdapter::undetected(key.to_vec())),
    }
}

fn grok_adapter(detection: Option<&AgentDetection>, key: &[u8]) -> Arc<dyn AgentAdapter> {
    match detection {
        Some(item) => Arc::new(adapter_grok_build::GrokBuildAdapter::for_version(
            item.version.clone(),
            key.to_vec(),
        )),
        None => Arc::new(adapter_grok_build::GrokBuildAdapter::undetected(
            key.to_vec(),
        )),
    }
}

fn cursor_adapter(
    detection: Option<&AgentDetection>,
    key: &[u8],
) -> Result<Arc<dyn AgentAdapter>, AdapterError> {
    let Some(item) = detection else {
        return Ok(installation_guard(
            Arc::new(
                adapter_cursor::CursorAdapter::personal("0", key.to_vec())
                    .with_local_schema_verified(false),
            ),
            false,
        ));
    };
    let mode = match item
        .cursor_mode
        .unwrap_or(DetectedCursorMode::PersonalLocal)
    {
        DetectedCursorMode::EnterpriseApi => adapter_cursor::CursorMode::EnterpriseApi,
        DetectedCursorMode::TeamAdminApi => adapter_cursor::CursorMode::TeamAdminApi,
        DetectedCursorMode::PersonalLocal => adapter_cursor::CursorMode::PersonalLocal,
    };
    let secret = item
        .secret_ref
        .as_deref()
        .map(adapter_cursor::SecretRef::new)
        .transpose()?;
    let verified = item.sqlite_fingerprint.as_deref() == Some("cursor-local-v1");
    Ok(Arc::new(
        adapter_cursor::CursorAdapter::new(mode, item.version.clone(), secret, key.to_vec())
            .with_local_schema_verified(verified),
    ))
}

fn zcode_adapter(detection: Option<&AgentDetection>, key: &[u8]) -> Arc<dyn AgentAdapter> {
    let installed = detection.is_some();
    let detection = detection
        .cloned()
        .unwrap_or_else(|| AgentDetection::installed("0"));
    installation_guard(
        Arc::new(
            adapter_zcode::ZCodeAdapter::new(
                detection.version,
                detection
                    .sqlite_fingerprint
                    .unwrap_or_else(|| "unverified".into()),
                key.to_vec(),
            )
            .with_runtime_verified(detection.runtime_verified),
        ),
        installed,
    )
}

fn deepseek_adapter(detection: Option<&AgentDetection>, key: &[u8]) -> Arc<dyn AgentAdapter> {
    match detection {
        Some(item) => Arc::new(
            adapter_deepseek_harness::DeepSeekHarnessAdapter::for_version(
                item.version.clone(),
                key.to_vec(),
            ),
        ),
        None => {
            Arc::new(adapter_deepseek_harness::DeepSeekHarnessAdapter::undetected(key.to_vec()))
        }
    }
}

fn pi_adapter(detection: Option<&AgentDetection>, key: &[u8]) -> Arc<dyn AgentAdapter> {
    match detection {
        Some(item) => Arc::new(adapter_pi::PiAdapter::for_version(
            item.version.clone(),
            key.to_vec(),
        )),
        None => Arc::new(adapter_pi::PiAdapter::undetected(key.to_vec())),
    }
}

struct DetectionGuard {
    adapter: Arc<dyn AgentAdapter>,
    installed: bool,
}

#[async_trait]
impl AgentAdapter for DetectionGuard {
    fn manifest(&self) -> &adapter_sdk::AdapterManifest {
        self.adapter.manifest()
    }

    async fn probe(&self, ctx: ProbeContext) -> Result<ProbeReport, AdapterError> {
        let mut report = self.adapter.probe(ctx).await?;
        if !self.installed {
            report.detected = false;
            report.agent_version = None;
            report.needs_permission = false;
            report.needs_setup = false;
            for capability in &mut report.capability.capabilities {
                capability.availability = adapter_sdk::CapabilityAvailability::Unavailable;
                capability.accuracy = None;
                capability.safe_reason_code = Some("AGENT_UNDETECTED".into());
            }
            report.detail = None;
        }
        Ok(report)
    }

    async fn setup_plan(&self, ctx: SetupContext) -> Result<SetupPlan, AdapterError> {
        self.adapter.setup_plan(ctx).await
    }

    async fn discover_sources(&self, ctx: SourceContext) -> Result<Vec<SourceSpec>, AdapterError> {
        if self.installed {
            self.adapter.discover_sources(ctx).await
        } else {
            Ok(Vec::new())
        }
    }

    async fn decode(&self, frame: RawFrame) -> Result<Vec<NormalizedEvent>, AdapterError> {
        self.adapter.decode(frame).await
    }

    async fn health(&self) -> AdapterHealth {
        self.adapter.health().await
    }
}

pub fn assemble_production_collector(
    installation_id: impl Into<String>,
    hmac_key: &[u8],
    snapshot: &DetectionSnapshot,
) -> Result<Collector, AdapterError> {
    if hmac_key.len() < 16 {
        return Err(AdapterError::setup_failed(
            "device HMAC key must contain at least 128 bits",
        ));
    }
    assemble_collector(
        installation_id,
        OfficialAdapters::production(snapshot, hmac_key)?,
    )
}

pub fn assemble_collector(
    installation_id: impl Into<String>,
    official_adapters: OfficialAdapters,
) -> Result<Collector, AdapterError> {
    let mut collector = Collector::new(installation_id);
    for adapter in official_adapters.into_vec() {
        collector.register_adapter(adapter)?;
    }
    Ok(collector)
}

pub struct ProductionService {
    pub collector: Collector,
    pub driver_registry: DriverRegistry,
    pub wal: WalStore,
    pub secret_resolver: Arc<dyn SecretResolver>,
}

impl ProductionService {
    pub async fn assemble(
        installation_id: impl Into<String>,
        hmac_key: &[u8],
        snapshot: &DetectionSnapshot,
        secret_resolver: Arc<dyn SecretResolver>,
        wal: WalStore,
    ) -> Result<Self, AdapterError> {
        let installation_id = installation_id.into();
        let mut collector =
            assemble_production_collector(installation_id.clone(), hmac_key, snapshot)?;
        collector.probe_all().await;
        let mut driver_registry = DriverRegistry::default();
        for agent in OfficialAgent::ALL {
            if !snapshot.is_installed(agent) {
                continue;
            }
            let adapter_id = adapter_id(agent);
            for source in collector.discover_sources(adapter_id).await? {
                let Some(mut driver) = build_driver(
                    &installation_id,
                    agent,
                    &source,
                    snapshot,
                    Arc::clone(&secret_resolver),
                )
                .map_err(|error| AdapterError::source_discovery_failed(error.to_string()))?
                else {
                    continue;
                };
                if let Some(checkpoint) = wal.latest_checkpoint(source.id()) {
                    restore_driver_checkpoint(&mut driver, checkpoint);
                }
                driver_registry
                    .register(adapter_id, source.id(), driver)
                    .map_err(|error| AdapterError::source_discovery_failed(error.to_string()))?;
            }
        }
        Ok(Self {
            collector,
            driver_registry,
            wal,
            secret_resolver,
        })
    }

    pub async fn ingest_jsonl_path(
        &mut self,
        adapter_id: &str,
        source_id: &str,
        path: &Path,
        historical: bool,
    ) -> Result<usize, acquisition::AcquisitionError> {
        let mut tailer =
            JsonlTailer::new(self.collector.installation_id(), source_id, source_id, path);
        tailer.restore_matching(&self.wal);
        // Stable IDs dedupe old tokens while the startup replay fills new metrics.
        let replay_codex = historical && (adapter_id == adapter_codex::ADAPTER_ID
            || (adapter_id == adapter_grok_build::ADAPTER_ID && self.wal.observations_enabled()));
        if replay_codex {
            if !self.wal.backpressure().allow_historical_scan() {
                return Ok(0);
            }
            tailer.reset_for_rescan();
        }
        let collector = &self.collector;
        let wal = &mut self.wal;
        IngestPipeline::new(collector, adapter_id)
            .ingest(&mut tailer, wal, historical && !replay_codex)
            .await
            .map(|poll| poll.accepted_events)
    }

    pub async fn ingest_driver_batch(
        &mut self,
        adapter_id: &str,
        source_id: &str,
        batch: DriverBatch,
        historical: bool,
    ) -> Result<usize, acquisition::AcquisitionError> {
        let source_kind = self
            .driver_registry
            .entry(adapter_id, source_id)
            .map(|entry| match entry.driver.kind() {
                acquisition::DriverKind::OtlpReceiver => adapter_sdk::SourceKind::Otlp,
                acquisition::DriverKind::JsonlTail => adapter_sdk::SourceKind::JsonlTail,
                acquisition::DriverKind::SqliteSnapshot => adapter_sdk::SourceKind::SqliteSnapshot,
                acquisition::DriverKind::RuntimeStream => adapter_sdk::SourceKind::RuntimeStream,
                acquisition::DriverKind::RemoteApi => adapter_sdk::SourceKind::RemoteApi,
            })
            .ok_or_else(|| {
                acquisition::AcquisitionError::Other("source_driver_not_registered".into())
            })?;
        IngestPipeline::new(&self.collector, adapter_id)
            .ingest_bound_batch(source_id, source_kind, batch, &mut self.wal, historical)
            .await
    }

    pub async fn accept_otlp(
        &mut self,
        adapter_id: &str,
        source_id: &str,
        path: &str,
        content_type: &str,
        payload: &[u8],
    ) -> Result<usize, acquisition::AcquisitionError> {
        let batch = {
            let entry = self.driver_entry_mut(adapter_id, source_id)?;
            let DriverInstance::OtlpReceiver(driver) = &mut entry.driver else {
                return Err(acquisition::AcquisitionError::Other(
                    "source_driver_kind_mismatch".into(),
                ));
            };
            entry.status = DriverTaskStatus::Running;
            let frame = match driver.accept_http_path(path, content_type, payload) {
                Ok(frame) => frame,
                Err(error) => {
                    entry.status = DriverTaskStatus::Failed("otlp_accept_failed".into());
                    return Err(error);
                }
            };
            entry.status = DriverTaskStatus::Idle;
            DriverBatch {
                cursor: frame.cursor.clone(),
                frames: vec![frame],
                driver_checkpoint: Some(wal_spool::DriverCheckpoint::OtlpReceiver {
                    sequence: driver.sequence(),
                }),
            }
        };
        self.ingest_driver_batch(adapter_id, source_id, batch, false)
            .await
    }

    pub async fn poll_jsonl(
        &mut self,
        adapter_id: &str,
        source_id: &str,
        historical: bool,
    ) -> Result<usize, acquisition::AcquisitionError> {
        let collector = &self.collector;
        let wal = &mut self.wal;
        let entry = self
            .driver_registry
            .entry_mut(adapter_id, source_id)
            .ok_or_else(|| {
                acquisition::AcquisitionError::Other("source_driver_not_registered".into())
            })?;
        let DriverInstance::JsonlTail(driver) = &mut entry.driver else {
            return Err(acquisition::AcquisitionError::Other(
                "source_driver_kind_mismatch".into(),
            ));
        };
        entry.status = DriverTaskStatus::Running;
        let result = IngestPipeline::new(collector, adapter_id)
            .ingest(driver, wal, historical)
            .await;
        match result {
            Ok(poll) => {
                entry.status = DriverTaskStatus::Idle;
                Ok(poll.accepted_events)
            }
            Err(error) => {
                entry.status = DriverTaskStatus::Failed("jsonl_poll_failed".into());
                Err(error)
            }
        }
    }

    pub async fn poll_jsonl_realtime(
        &mut self,
        adapter_id: &str,
        source_id: &str,
    ) -> Result<usize, acquisition::AcquisitionError> {
        self.poll_jsonl(adapter_id, source_id, false).await
    }

    pub async fn poll_jsonl_historical(
        &mut self,
        adapter_id: &str,
        source_id: &str,
    ) -> Result<usize, acquisition::AcquisitionError> {
        self.poll_jsonl(adapter_id, source_id, true).await
    }

    pub async fn poll_remote(
        &mut self,
        adapter_id: &str,
        source_id: &str,
        now_unix_seconds: i64,
    ) -> Result<usize, acquisition::AcquisitionError> {
        let batch = {
            let entry = self.driver_entry_mut(adapter_id, source_id)?;
            let DriverInstance::RemoteApi(driver) = &mut entry.driver else {
                return Err(acquisition::AcquisitionError::Other(
                    "source_driver_kind_mismatch".into(),
                ));
            };
            entry.status = DriverTaskStatus::Running;
            match driver.poll(now_unix_seconds).await {
                Ok(batch) => {
                    entry.status = DriverTaskStatus::Idle;
                    batch
                }
                Err(acquisition::RemotePollError::RateLimited(delay)) => {
                    entry.status = DriverTaskStatus::Backoff(delay);
                    return Err(acquisition::AcquisitionError::Other(
                        "remote_rate_limited".into(),
                    ));
                }
                Err(_error) => {
                    entry.status = DriverTaskStatus::Failed("remote_poll_failed".into());
                    return Err(acquisition::AcquisitionError::Other(
                        "remote_poll_failed".into(),
                    ));
                }
            }
        };
        self.ingest_driver_batch(adapter_id, source_id, batch, false)
            .await
    }

    pub async fn poll_sqlite(
        &mut self,
        adapter_id: &str,
        source_id: &str,
    ) -> Result<usize, acquisition::AcquisitionError> {
        let batch = {
            let entry = self.driver_entry_mut(adapter_id, source_id)?;
            let DriverInstance::SqliteSnapshot(driver) = &mut entry.driver else {
                return Err(acquisition::AcquisitionError::Other(
                    "source_driver_kind_mismatch".into(),
                ));
            };
            entry.status = DriverTaskStatus::Running;
            let batch = match driver.poll() {
                Ok(batch) => batch,
                Err(error) => {
                    entry.status = DriverTaskStatus::Failed("sqlite_poll_failed".into());
                    return Err(error);
                }
            };
            entry.status = DriverTaskStatus::Idle;
            batch
        };
        self.ingest_driver_batch(adapter_id, source_id, batch, true)
            .await
    }

    pub async fn push_runtime(
        &mut self,
        adapter_id: &str,
        source_id: &str,
        stream_id: &str,
        sequence: u64,
        payload: Vec<u8>,
    ) -> Result<usize, acquisition::AcquisitionError> {
        let batch = {
            let entry = self.driver_entry_mut(adapter_id, source_id)?;
            let DriverInstance::RuntimeStream(driver) = &mut entry.driver else {
                return Err(acquisition::AcquisitionError::Other(
                    "source_driver_kind_mismatch".into(),
                ));
            };
            entry.status = DriverTaskStatus::Running;
            if let Err(error) = driver.push(stream_id, sequence, payload) {
                entry.status = DriverTaskStatus::Failed("runtime_push_failed".into());
                return Err(error);
            }
            let batch = driver.poll(usize::MAX);
            entry.status = DriverTaskStatus::Idle;
            batch
        };
        self.ingest_driver_batch(adapter_id, source_id, batch, false)
            .await
    }

    fn driver_entry_mut(
        &mut self,
        adapter_id: &str,
        source_id: &str,
    ) -> Result<&mut acquisition::DriverEntry, acquisition::AcquisitionError> {
        self.driver_registry
            .entry_mut(adapter_id, source_id)
            .ok_or_else(|| {
                acquisition::AcquisitionError::Other("source_driver_not_registered".into())
            })
    }

    pub fn apply_setup<B, V>(
        &mut self,
        adapter_id: &str,
        plan: &SetupPlan,
        executor: &mut SetupPlanExecutor<B, V>,
    ) -> Result<ApplyReport, ExecutorError>
    where
        B: EncryptedBackupStore,
        V: SetupVerifier,
    {
        if plan.adapter_id != adapter_id {
            return Err(ExecutorError::PlanChanged);
        }
        executor.approve(plan)?;
        executor.apply(plan)
    }
}

fn build_driver(
    installation_id: &str,
    agent: OfficialAgent,
    source: &SourceSpec,
    snapshot: &DetectionSnapshot,
    secret_resolver: Arc<dyn SecretResolver>,
) -> Result<Option<DriverInstance>, acquisition::AcquisitionError> {
    let config = snapshot.source(agent, source.id());
    let driver = match source {
        SourceSpec::OtlpReceiver { id, bind_host, .. } => {
            let configured_host = config
                .and_then(|config| config.endpoint.as_deref())
                .map(endpoint_host)
                .transpose()?
                .unwrap_or_else(|| bind_host.clone());
            DriverInstance::OtlpReceiver(OtlpReceiverDriver::new(
                installation_id,
                id,
                configured_host,
                DEFAULT_OTLP_PAYLOAD_LIMIT,
            )?)
        }
        SourceSpec::JsonlTail { id, path_template } => {
            let Some(path) = config.and_then(|config| config.path.clone()) else {
                return Ok(None);
            };
            if path.is_dir() {
                return Ok(None);
            }
            DriverInstance::JsonlTail(JsonlTailer::new(installation_id, id, path_template, path))
        }
        SourceSpec::SqliteSnapshot { id, .. } => {
            let detection = snapshot
                .get(agent)
                .ok_or_else(|| acquisition::AcquisitionError::Other("agent_not_detected".into()))?;
            let fingerprint = detection.sqlite_fingerprint.as_deref().ok_or_else(|| {
                acquisition::AcquisitionError::Other("sqlite_fingerprint_required".into())
            })?;
            let plan = match fingerprint {
                "cursor-local-v1" => SqliteAdapterPlan::CursorPersonalV1,
                "zcode-sqlite-v1-uv7" => SqliteAdapterPlan::ZcodeV1,
                "zcode-sqlite-v2-uv9" => SqliteAdapterPlan::ZcodeV2,
                "zcode-sqlite-v3-uv0" => SqliteAdapterPlan::ZcodeV3,
                _ => {
                    return Err(acquisition::AcquisitionError::Other(
                        "untrusted_sqlite_schema_fingerprint".into(),
                    ))
                }
            };
            let path = config
                .and_then(|config| config.path.clone())
                .ok_or_else(|| {
                    acquisition::AcquisitionError::Other("sqlite_snapshot_path_required".into())
                })?;
            DriverInstance::SqliteSnapshot(SqliteSnapshotDriver::new(
                installation_id,
                id,
                path,
                plan,
                fingerprint,
            )?)
        }
        SourceSpec::RuntimeStream { id, stream_id } => DriverInstance::RuntimeStream(
            RuntimeStreamDriver::new(installation_id, id, stream_id, DEFAULT_OTLP_PAYLOAD_LIMIT)?,
        ),
        SourceSpec::RemoteApi { id, domain } => {
            let endpoint = config
                .and_then(|config| config.endpoint.as_deref())
                .ok_or_else(|| {
                    acquisition::AcquisitionError::Other("remote_endpoint_required".into())
                })?;
            let secret_ref = config
                .and_then(|config| config.secret_ref.as_deref())
                .or_else(|| {
                    snapshot
                        .get(agent)
                        .and_then(|item| item.secret_ref.as_deref())
                })
                .ok_or_else(|| {
                    acquisition::AcquisitionError::Other("remote_secret_ref_required".into())
                })?;
            DriverInstance::RemoteApi(RemoteApiDriver::new(
                installation_id,
                id,
                endpoint,
                domain,
                secret_ref,
                secret_resolver,
                DEFAULT_OTLP_PAYLOAD_LIMIT,
            )?)
        }
        _ => return Ok(None),
    };
    Ok(Some(driver))
}

fn restore_driver_checkpoint(
    driver: &mut DriverInstance,
    checkpoint: &wal_spool::SourceCheckpoint,
) {
    match driver {
        DriverInstance::JsonlTail(tailer) => tailer.restore(checkpoint),
        DriverInstance::RemoteApi(remote) => {
            if let Some(wal_spool::DriverCheckpoint::RemoteApi {
                cursor,
                window_end_unix_seconds,
            }) = &checkpoint.driver_checkpoint
            {
                remote.restore_cursor(cursor.clone(), *window_end_unix_seconds);
            }
        }
        DriverInstance::SqliteSnapshot(sqlite) => {
            if let Some(wal_spool::DriverCheckpoint::SqliteSnapshot { query_cursors }) =
                &checkpoint.driver_checkpoint
            {
                sqlite.restore_query_cursors(query_cursors);
            }
        }
        DriverInstance::OtlpReceiver(otlp) => {
            if let Some(wal_spool::DriverCheckpoint::OtlpReceiver { sequence }) =
                &checkpoint.driver_checkpoint
            {
                otlp.restore_sequence(*sequence);
            }
        }
        DriverInstance::RuntimeStream(runtime) => {
            if let Some(wal_spool::DriverCheckpoint::RuntimeStream { next_sequence }) =
                &checkpoint.driver_checkpoint
            {
                runtime.restore_next_sequence(*next_sequence);
            }
        }
    }
}

fn endpoint_host(endpoint: &str) -> Result<String, acquisition::AcquisitionError> {
    let authority = endpoint
        .trim()
        .strip_prefix("http://")
        .or_else(|| endpoint.trim().strip_prefix("https://"))
        .unwrap_or(endpoint.trim())
        .split('/')
        .next()
        .unwrap_or_default();
    let host = if authority.starts_with('[') {
        authority
            .strip_prefix('[')
            .and_then(|value| value.split(']').next())
            .unwrap_or_default()
    } else {
        authority.split(':').next().unwrap_or_default()
    };
    if host.is_empty() {
        Err(acquisition::AcquisitionError::Other(
            "invalid_source_endpoint".into(),
        ))
    } else {
        Ok(host.to_owned())
    }
}

pub fn adapter_id(agent: OfficialAgent) -> &'static str {
    match agent {
        OfficialAgent::Codex => adapter_codex::ADAPTER_ID,
        OfficialAgent::ClaudeCode => adapter_claude::ADAPTER_ID,
        OfficialAgent::GrokBuild => adapter_grok_build::ADAPTER_ID,
        OfficialAgent::Cursor => adapter_cursor::ADAPTER_ID,
        OfficialAgent::Zcode => adapter_zcode::ADAPTER_ID,
        OfficialAgent::DeepseekHarness => adapter_deepseek_harness::ADAPTER_ID,
        OfficialAgent::Pi => adapter_pi::ADAPTER_ID,
    }
}

#[cfg(test)]
mod tests {
    use std::fs::{self, OpenOptions};
    use std::io::Write;
    use std::sync::Arc;

    use adapter_sdk::{AdapterRuntimeStatus, SourceKind};
    use opentelemetry_proto::tonic::collector::logs::v1::ExportLogsServiceRequest;
    use opentelemetry_proto::tonic::common::v1::{any_value, AnyValue, KeyValue};
    use opentelemetry_proto::tonic::logs::v1::{LogRecord, ResourceLogs, ScopeLogs};
    use prost::Message;
    use protocol::SourceCheckpointStatus;
    use wal_spool::{
        AppendClass, DriverCheckpoint, InjectedKeyProvider, SourceCheckpoint, SpoolLimits,
        Transaction,
    };

    use super::*;

    struct EmptySecrets;
    impl SecretResolver for EmptySecrets {
        fn resolve(&self, _secret_ref: &str) -> Result<Vec<u8>, String> {
            Err("missing".into())
        }
    }

    fn otlp_string(key: &str, value: &str) -> KeyValue {
        KeyValue {
            key: key.into(),
            value: Some(AnyValue {
                value: Some(any_value::Value::StringValue(value.into())),
            }),
            ..KeyValue::default()
        }
    }

    fn otlp_int(key: &str, value: i64) -> KeyValue {
        KeyValue {
            key: key.into(),
            value: Some(AnyValue {
                value: Some(any_value::Value::IntValue(value)),
            }),
            ..KeyValue::default()
        }
    }

    fn codex_token_usage_protobuf() -> Vec<u8> {
        ExportLogsServiceRequest {
            resource_logs: vec![ResourceLogs {
                resource: None,
                scope_logs: vec![ScopeLogs {
                    scope: None,
                    log_records: vec![LogRecord {
                        time_unix_nano: 1_788_084_061_000_000_000,
                        event_name: "codex.token.usage".into(),
                        attributes: vec![
                            otlp_string("thread.id", "protobuf-thread"),
                            otlp_string("turn.id", "protobuf-turn"),
                            otlp_string("provider", "openai"),
                            otlp_string("model", "gpt-5-codex"),
                            otlp_int("input_tokens", 21),
                            otlp_int("output_tokens", 13),
                            otlp_int("total_tokens", 34),
                        ],
                        ..LogRecord::default()
                    }],
                    schema_url: String::new(),
                }],
                schema_url: String::new(),
            }],
        }
        .encode_to_vec()
    }

    #[test]
    fn production_assembly_rejects_short_device_hmac_key() {
        assert!(assemble_production_collector(
            "ins_00000000000000000000000000",
            b"short",
            &DetectionSnapshot::default(),
        )
        .is_err());
    }

    #[tokio::test]
    async fn uninstalled_snapshot_keeps_all_official_adapters_undetected() {
        let mut collector = assemble_production_collector(
            "ins_00000000000000000000000000",
            b"test-device-hmac-key",
            &DetectionSnapshot::default(),
        )
        .unwrap();
        collector.probe_all().await;
        assert_eq!(collector.runtimes().len(), 7);
        assert!(collector.runtimes().into_iter().all(|runtime| {
            !runtime.detected
                && runtime.agent_version.is_none()
                && runtime.status == AdapterRuntimeStatus::Undetected
        }));
    }

    #[tokio::test]
    async fn seven_official_adapters_map_discovered_sources_to_drivers() {
        let mut snapshot = DetectionSnapshot::default()
            .with(
                OfficialAgent::Codex,
                AgentDetection {
                    otlp_available: true,
                    mode: Some("interactive".into()),
                    ..AgentDetection::installed("0.130.0")
                },
            )
            .with(
                OfficialAgent::ClaudeCode,
                AgentDetection::installed("1.0.0"),
            )
            .with(OfficialAgent::GrokBuild, AgentDetection::installed("1.0.0"))
            .with(
                OfficialAgent::Cursor,
                AgentDetection {
                    sqlite_fingerprint: Some("cursor-local-v1".into()),
                    cursor_mode: Some(DetectedCursorMode::PersonalLocal),
                    ..AgentDetection::installed("0.45.2")
                },
            )
            .with(
                OfficialAgent::Zcode,
                AgentDetection {
                    sqlite_fingerprint: Some("zcode-sqlite-v2-uv9".into()),
                    runtime_verified: true,
                    ..AgentDetection::installed("1.2.0")
                },
            )
            .with(
                OfficialAgent::DeepseekHarness,
                AgentDetection::installed("1.0.0"),
            )
            .with(OfficialAgent::Pi, AgentDetection::installed("0.3.0"));
        snapshot.configure_source(
            OfficialAgent::Cursor,
            "cursor-personal-local",
            DetectedSourceConfig {
                path: Some(PathBuf::from("cursor.sqlite")),
                ..DetectedSourceConfig::default()
            },
        );
        snapshot.configure_source(
            OfficialAgent::Zcode,
            "zcode-sqlite",
            DetectedSourceConfig {
                path: Some(PathBuf::from("zcode.sqlite")),
                ..DetectedSourceConfig::default()
            },
        );
        snapshot.configure_source(
            OfficialAgent::Pi,
            adapter_pi::HISTORY_SOURCE_ID,
            DetectedSourceConfig {
                path: Some(PathBuf::from("pi-session.jsonl")),
                ..DetectedSourceConfig::default()
            },
        );
        let mut collector =
            assemble_production_collector("ins_test", b"test-device-hmac-key", &snapshot).unwrap();
        collector.probe_all().await;
        let mut registry = DriverRegistry::default();
        for agent in OfficialAgent::ALL {
            for source in collector.discover_sources(adapter_id(agent)).await.unwrap() {
                if let Some(driver) = build_driver(
                    "ins_test",
                    agent,
                    &source,
                    &snapshot,
                    Arc::new(EmptySecrets),
                )
                .unwrap()
                {
                    registry
                        .register(adapter_id(agent), source.id(), driver)
                        .unwrap();
                }
            }
        }
        for agent in OfficialAgent::ALL {
            assert!(registry_snapshot_for_adapter(&registry, adapter_id(agent)) > 0);
        }
        let _ = Arc::new(EmptySecrets);
    }

    #[tokio::test]
    async fn production_otlp_path_ingests_directly_into_wal() {
        let dir = tempfile::tempdir().unwrap();
        let wal = WalStore::open_with_limits(
            dir.path().join("state"),
            Arc::new(InjectedKeyProvider::new([0x44; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        let snapshot = DetectionSnapshot::default()
            .with(OfficialAgent::GrokBuild, AgentDetection::installed("1.0.0"));
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000001",
            b"production-e2e-device-key",
            &snapshot,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .unwrap();

        let accepted = service
            .accept_otlp(
                adapter_grok_build::ADAPTER_ID,
                adapter_grok_build::OTLP_SOURCE_ID,
                "/v1/metrics",
                "application/json",
                include_bytes!("../../../crates/acquisition/fixtures/otlp-metrics.json"),
            )
            .await
            .unwrap();

        assert_eq!(accepted, 1);
        assert_eq!(service.wal.unacked_count(), 1);
        assert!(service
            .wal
            .latest_checkpoint(adapter_grok_build::OTLP_SOURCE_ID)
            .is_some());
        assert_eq!(
            service.wal.unacked_events()[0].source.kind,
            SourceKind::Otlp
        );
        assert_eq!(
            service
                .driver_registry
                .entry(
                    adapter_grok_build::ADAPTER_ID,
                    adapter_grok_build::OTLP_SOURCE_ID,
                )
                .unwrap()
                .status,
            DriverTaskStatus::Idle
        );
    }

    #[tokio::test]
    async fn codex_real_otlp_json_and_protobuf_flow_through_service_into_wal() {
        let dir = tempfile::tempdir().unwrap();
        let wal = WalStore::open_with_limits(
            dir.path().join("state"),
            Arc::new(InjectedKeyProvider::new([0x47; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        let snapshot = DetectionSnapshot::default().with(
            OfficialAgent::Codex,
            AgentDetection {
                otlp_available: true,
                mode: Some("interactive".into()),
                ..AgentDetection::installed("0.130.0")
            },
        );
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000003",
            b"codex-real-otlp-device-key",
            &snapshot,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .unwrap();
        let json = br#"{
            "resourceLogs": [{
                "scopeLogs": [{
                    "logRecords": [{
                        "timeUnixNano": "1788084060000000000",
                        "eventName": "codex.thread.started",
                        "attributes": [
                            {"key":"thread.id","value":{"stringValue":"json-thread"}},
                            {"key":"model","value":{"stringValue":"gpt-5-codex"}}
                        ]
                    }]
                }]
            }]
        }"#;

        assert_eq!(
            service
                .accept_otlp(
                    adapter_codex::ADAPTER_ID,
                    adapter_codex::SOURCE_OTEL,
                    "/v1/logs",
                    "application/json",
                    json,
                )
                .await
                .unwrap(),
            1
        );
        assert_eq!(
            service
                .accept_otlp(
                    adapter_codex::ADAPTER_ID,
                    adapter_codex::SOURCE_OTEL,
                    "/v1/logs",
                    "application/x-protobuf",
                    &codex_token_usage_protobuf(),
                )
                .await
                .unwrap(),
            1
        );

        assert_eq!(service.wal.unacked_count(), 2);
        let checkpoint = service
            .wal
            .latest_checkpoint(adapter_codex::SOURCE_OTEL)
            .unwrap();
        assert_eq!(checkpoint.offset, 2);
        assert_eq!(
            checkpoint.driver_checkpoint,
            Some(DriverCheckpoint::OtlpReceiver { sequence: 2 })
        );
        assert!(service
            .wal
            .unacked_events()
            .iter()
            .all(|event| event.source.kind == SourceKind::Otlp));
    }

    #[tokio::test]
    async fn otlp_sequence_restores_after_restart_without_cursor_collision() {
        let dir = tempfile::tempdir().unwrap();
        let state = dir.path().join("state");
        let snapshot = DetectionSnapshot::default().with(
            OfficialAgent::Codex,
            AgentDetection {
                otlp_available: true,
                mode: Some("interactive".into()),
                ..AgentDetection::installed("0.130.0")
            },
        );
        let json = br#"{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"1788084060000000000","eventName":"codex.thread.started","attributes":[{"key":"thread.id","value":{"stringValue":"restart-thread"}}]}]}]}]}"#;
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000004",
            b"codex-restart-sequence-key",
            &snapshot,
            Arc::new(EmptySecrets),
            WalStore::open_with_limits(
                &state,
                Arc::new(InjectedKeyProvider::new([0x48; 32])),
                SpoolLimits::for_tests(),
            )
            .unwrap(),
        )
        .await
        .unwrap();
        service
            .accept_otlp(
                adapter_codex::ADAPTER_ID,
                adapter_codex::SOURCE_OTEL,
                "/v1/logs",
                "application/json",
                json,
            )
            .await
            .unwrap();
        let first_event_id = service.wal.unacked_events()[0].event_id.clone();
        drop(service);

        let mut restarted = ProductionService::assemble(
            "ins_00000000000000000000000004",
            b"codex-restart-sequence-key",
            &snapshot,
            Arc::new(EmptySecrets),
            WalStore::open_with_limits(
                &state,
                Arc::new(InjectedKeyProvider::new([0x48; 32])),
                SpoolLimits::for_tests(),
            )
            .unwrap(),
        )
        .await
        .unwrap();
        let DriverInstance::OtlpReceiver(driver) = &restarted
            .driver_registry
            .entry(adapter_codex::ADAPTER_ID, adapter_codex::SOURCE_OTEL)
            .unwrap()
            .driver
        else {
            unreachable!()
        };
        assert_eq!(driver.sequence(), 1);
        restarted
            .accept_otlp(
                adapter_codex::ADAPTER_ID,
                adapter_codex::SOURCE_OTEL,
                "/v1/logs",
                "application/json",
                json,
            )
            .await
            .unwrap();

        assert_eq!(restarted.wal.unacked_count(), 2);
        assert_ne!(restarted.wal.unacked_events()[1].event_id, first_event_id);
        assert_eq!(
            restarted
                .wal
                .latest_checkpoint(adapter_codex::SOURCE_OTEL)
                .unwrap()
                .driver_checkpoint,
            Some(DriverCheckpoint::OtlpReceiver { sequence: 2 })
        );
    }

    #[tokio::test]
    async fn runtime_next_sequence_restores_and_rejects_low_sequence() {
        let dir = tempfile::tempdir().unwrap();
        let state = dir.path().join("state");
        let snapshot = DetectionSnapshot::default().with(
            OfficialAgent::Zcode,
            AgentDetection {
                runtime_verified: true,
                ..AgentDetection::installed("1.2.0")
            },
        );
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000005",
            b"zcode-runtime-restart-key",
            &snapshot,
            Arc::new(EmptySecrets),
            WalStore::open_with_limits(
                &state,
                Arc::new(InjectedKeyProvider::new([0x49; 32])),
                SpoolLimits::for_tests(),
            )
            .unwrap(),
        )
        .await
        .unwrap();
        assert_eq!(
            service
                .push_runtime(
                    adapter_zcode::ADAPTER_ID,
                    "zcode-runtime-events",
                    "zcode.session.events.v1",
                    7,
                    adapter_zcode::RUNTIME_JSON.as_bytes().to_vec(),
                )
                .await
                .unwrap(),
            1
        );
        assert_eq!(
            service
                .wal
                .latest_checkpoint("zcode-runtime-events")
                .unwrap()
                .driver_checkpoint,
            Some(DriverCheckpoint::RuntimeStream { next_sequence: 8 })
        );
        drop(service);

        let mut restarted = ProductionService::assemble(
            "ins_00000000000000000000000005",
            b"zcode-runtime-restart-key",
            &snapshot,
            Arc::new(EmptySecrets),
            WalStore::open_with_limits(
                &state,
                Arc::new(InjectedKeyProvider::new([0x49; 32])),
                SpoolLimits::for_tests(),
            )
            .unwrap(),
        )
        .await
        .unwrap();
        let DriverInstance::RuntimeStream(driver) = &restarted
            .driver_registry
            .entry(adapter_zcode::ADAPTER_ID, "zcode-runtime-events")
            .unwrap()
            .driver
        else {
            unreachable!()
        };
        assert_eq!(driver.next_sequence(), 8);
        let error = restarted
            .push_runtime(
                adapter_zcode::ADAPTER_ID,
                "zcode-runtime-events",
                "zcode.session.events.v1",
                7,
                adapter_zcode::RUNTIME_JSON.as_bytes().to_vec(),
            )
            .await
            .unwrap_err();
        assert_eq!(error.to_string(), "runtime_sequence_not_monotonic");
        assert_eq!(restarted.wal.unacked_count(), 1);
        assert_eq!(
            restarted
                .push_runtime(
                    adapter_zcode::ADAPTER_ID,
                    "zcode-runtime-events",
                    "zcode.session.events.v1",
                    8,
                    adapter_zcode::RUNTIME_JSON.as_bytes().to_vec(),
                )
                .await
                .unwrap(),
            1
        );
        assert_eq!(
            restarted
                .wal
                .latest_checkpoint("zcode-runtime-events")
                .unwrap()
                .driver_checkpoint,
            Some(DriverCheckpoint::RuntimeStream { next_sequence: 9 })
        );
    }

    #[tokio::test]
    async fn jsonl_poll_restores_checkpoint_after_service_restart() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("codex.jsonl");
        fs::write(&path, adapter_codex::SESSION_JSONL).unwrap();
        let snapshot = DetectionSnapshot::default()
            .with(
                OfficialAgent::Codex,
                AgentDetection {
                    mode: Some("interactive".into()),
                    ..AgentDetection::installed("0.130.0")
                },
            )
            .with_source(
                OfficialAgent::Codex,
                adapter_codex::SOURCE_JSONL,
                DetectedSourceConfig {
                    path: Some(path.clone()),
                    ..DetectedSourceConfig::default()
                },
            );
        let state = dir.path().join("state");
        let keys = Arc::new(InjectedKeyProvider::new([0x45; 32]));
        let wal = WalStore::open_with_limits(&state, keys, SpoolLimits::for_tests()).unwrap();
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000002",
            b"restart-jsonl-device-key",
            &snapshot,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .unwrap();

        assert_eq!(
            service
                .poll_jsonl_realtime(adapter_codex::ADAPTER_ID, adapter_codex::SOURCE_JSONL)
                .await
                .unwrap(),
            5
        );
        drop(service);

        let mut file = OpenOptions::new().append(true).open(&path).unwrap();
        writeln!(file, "{{\"type\":\"thread.started\",\"timestamp\":\"2026-08-30T10:01:00Z\",\"thread_id\":\"after-restart\"}}").unwrap();
        drop(file);
        let wal = WalStore::open_with_limits(
            &state,
            Arc::new(InjectedKeyProvider::new([0x45; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        let mut restarted = ProductionService::assemble(
            "ins_00000000000000000000000002",
            b"restart-jsonl-device-key",
            &snapshot,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .unwrap();

        assert_eq!(
            restarted
                .poll_jsonl_historical(adapter_codex::ADAPTER_ID, adapter_codex::SOURCE_JSONL)
                .await
                .unwrap(),
            1
        );
    }

    #[tokio::test]
    async fn collect_tick_ingests_detected_codex_jsonl() {
        let home = tempfile::tempdir().unwrap();
        let sessions = home.path().join(".codex").join("sessions");
        fs::create_dir_all(&sessions).unwrap();
        fs::write(sessions.join("one.jsonl"), adapter_codex::SESSION_JSONL).unwrap();
        let snapshot = crate::detect_from_home(home.path());
        assert!(snapshot.is_installed(OfficialAgent::Codex));
        let state = home.path().join("state");
        let wal = WalStore::open_with_limits(
            &state,
            Arc::new(InjectedKeyProvider::new([0x47; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000003",
            b"collect-tick-device-key",
            &snapshot,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .unwrap();
        let report = crate::collect_tick(&mut service, &snapshot, true).await;
        assert!(report.accepted_events > 0, "{report:?}");
        assert!(service.wal.unacked_count() > 0);
    }

    #[tokio::test]
    async fn collect_tick_ingests_grok_session_turn_usage() {
        let home = tempfile::tempdir().unwrap();
        let session = home
            .path()
            .join(".grok")
            .join("sessions")
            .join("proj")
            .join("primary");
        fs::create_dir_all(&session).unwrap();
        fs::write(
            home.path().join(".grok").join("version.json"),
            "{\"version\":\"1.0.13\"}\n",
        )
        .unwrap();
        fs::write(
            session.join("updates.jsonl"),
            r#"{"timestamp":"2026-09-05T12:16:09Z","method":"_x.ai/session/update","params":{"sessionId":"s1","update":{"sessionUpdate":"turn_completed","prompt_id":"p1","usage":{"inputTokens":11,"outputTokens":7,"totalTokens":18,"modelUsage":{"grok-4.6-build":{"inputTokens":11,"outputTokens":7,"totalTokens":18}}}}}}
"#,
        )
        .unwrap();
        let snapshot = crate::detect_from_home(home.path());
        assert!(snapshot.is_installed(OfficialAgent::GrokBuild));
        let state = home.path().join("state");
        let wal = WalStore::open_with_limits(
            &state,
            Arc::new(InjectedKeyProvider::new([0x48; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        let mut service = ProductionService::assemble(
            "ins_00000000000000000000000004",
            b"collect-tick-grok-device-key",
            &snapshot,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .unwrap();
        let report = crate::collect_tick(&mut service, &snapshot, true).await;
        assert!(
            report.accepted_events > 0,
            "grok session usage was not ingested: {report:?}"
        );
        assert!(service.wal.unacked_count() > 0);
    }

    #[test]
    fn remote_window_and_sqlite_query_cursors_restore_from_wal() {
        let dir = tempfile::tempdir().unwrap();
        let state = dir.path().join("state");
        let mut wal = WalStore::open_with_limits(
            &state,
            Arc::new(InjectedKeyProvider::new([0x46; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        for (source_id, driver_checkpoint) in [
            (
                "remote-source",
                DriverCheckpoint::RemoteApi {
                    cursor: "remote-cursor-7".into(),
                    window_end_unix_seconds: 7_000,
                },
            ),
            (
                "sqlite-source",
                DriverCheckpoint::SqliteSnapshot {
                    query_cursors: vec![11, 29],
                },
            ),
        ] {
            let checkpoint = SourceCheckpoint {
                source_id: source_id.into(),
                path_template_id: source_id.into(),
                file_identity: source_id.into(),
                generation: 1,
                file_len: 1,
                offset: 1,
                last_record_hash: None,
                driver_checkpoint: Some(driver_checkpoint),
                status: SourceCheckpointStatus::Current,
            };
            wal.append_txn(
                Transaction::new(
                    String::new(),
                    source_id,
                    None,
                    checkpoint,
                    vec![],
                    String::new(),
                ),
                AppendClass::Realtime,
            )
            .unwrap();
        }
        drop(wal);

        let wal = WalStore::open_with_limits(
            &state,
            Arc::new(InjectedKeyProvider::new([0x46; 32])),
            SpoolLimits::for_tests(),
        )
        .unwrap();
        let mut remote = DriverInstance::RemoteApi(
            RemoteApiDriver::new(
                "ins_test",
                "remote-source",
                "http://127.0.0.1/events",
                "127.0.0.1",
                "secret://remote/test",
                Arc::new(EmptySecrets),
                DEFAULT_OTLP_PAYLOAD_LIMIT,
            )
            .unwrap(),
        );
        restore_driver_checkpoint(&mut remote, wal.latest_checkpoint("remote-source").unwrap());
        let DriverInstance::RemoteApi(remote) = remote else {
            unreachable!()
        };
        assert_eq!(
            remote.cursor_state(),
            (Some("remote-cursor-7"), Some(7_000))
        );

        let mut sqlite = DriverInstance::SqliteSnapshot(
            SqliteSnapshotDriver::new(
                "ins_test",
                "sqlite-source",
                "unused.sqlite",
                SqliteAdapterPlan::ZcodeV2,
                "zcode-sqlite-v2-uv9",
            )
            .unwrap(),
        );
        restore_driver_checkpoint(&mut sqlite, wal.latest_checkpoint("sqlite-source").unwrap());
        let DriverInstance::SqliteSnapshot(sqlite) = sqlite else {
            unreachable!()
        };
        assert_eq!(sqlite.query_cursors(), &[11, 29]);
    }

    fn registry_snapshot_for_adapter(registry: &DriverRegistry, adapter_id: &str) -> usize {
        acquisition::registry_snapshot(registry)
            .keys()
            .filter(|key| key.starts_with(adapter_id))
            .count()
    }
}
