use std::collections::HashMap;
use std::fs;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex as StdMutex};
use std::time::Instant;

use acquisition::SecretResolver;
use adapter_sdk::{ConfigMutation, SetupPlan};
use chrono::{Local, Utc};
use collector_service::{detect_local, DetectionSnapshot, ProductionService};
use config_executor::{EncryptedBackupStore, SemanticVerifier, SetupPlanExecutor};
use protocol::EventEnvelope;
use serde::{Deserialize, Serialize};
use tokio::sync::{Mutex, RwLock};
use uuid::Uuid;
#[cfg(test)]
use wal_spool::InjectedKeyProvider;
use wal_spool::{AckPayload, KeyProvider, OsKeyProvider, WalStore};

use crate::autostart::{AutostartProvider, SystemAutostartManager};
use crate::local_store::{LeasedBatch, LocalStore};
use crate::usage_ledger::DayUsage;

const COLLECTOR_VERSION: &str = env!("CARGO_PKG_VERSION");

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OperationAck<T> {
    pub operation_id: String,
    pub accepted_at: String,
    pub status: String,
    pub state: T,
}

impl<T> OperationAck<T> {
    fn acknowledged(state: T) -> Self {
        Self {
            operation_id: format!("op_{}", Uuid::new_v4().simple()),
            accepted_at: Utc::now().to_rfc3339(),
            status: "ACKNOWLEDGED".into(),
            state,
        }
    }

    fn pending(state: T) -> Self {
        Self {
            operation_id: format!("op_{}", Uuid::new_v4().simple()),
            accepted_at: Utc::now().to_rfc3339(),
            status: "PENDING".into(),
            state,
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AgentConfig {
    pub id: String,
    pub name: String,
    pub adapter_id: String,
    pub adapter_version: String,
    pub status: String,
    pub setup_plan_status: String,
    pub enabled: bool,
    pub accuracy: String,
    pub sources: Vec<String>,
    pub capabilities: Vec<String>,
    pub today_tokens: u64,
    pub total_tokens: u64,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub daily_usage: Vec<DayUsage>,
    pub total_costs: std::collections::BTreeMap<String, u64>,
    pub pricing: crate::pricing::CostCoverage,
    pub history_start: Option<String>,
    pub last_active: String,
    pub version: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CollectorDevice {
    pub id: String,
    pub installation_id: String,
    pub name: String,
    pub platform: String,
    pub os_version: String,
    pub collector_version: String,
    pub key_fingerprint: String,
    pub status: String,
    pub last_sync_at: String,
    pub pending_events: u32,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ConfigSnapshot {
    pub agent_toggles: HashMap<String, bool>,
    pub metric_toggles: HashMap<String, bool>,
    pub global_paused: bool,
    pub autostart_enabled: bool,
    pub is_public_leaderboard: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct ConfigBackup {
    pub id: String,
    pub created_at: String,
    pub version_tag: String,
    pub description: String,
    pub snapshot: ConfigSnapshot,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct OutboxEnvelope {
    pub id: String,
    pub event_id: String,
    pub adapter_id: String,
    pub adapter_version: String,
    pub agent_id: String,
    pub occurred_at: String,
    pub event_type: String,
    pub delivery_status: String,
    pub accuracy: String,
    pub payload_summary: String,
    pub payload: serde_json::Value,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct UploadBatchPreview {
    pub batch_id: String,
    pub installation_id: String,
    pub created_at: String,
    pub event_count: usize,
    pub events: Vec<serde_json::Value>,
    pub redaction_applied: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DaemonStatus {
    pub status: String,
    pub global_paused: bool,
    pub pid: u32,
    pub uptime_secs: u64,
    pub collector_version: String,
    pub events_collected: u64,
    pub events_pending: usize,
    pub events_uploaded: u64,
    pub memory_rss_bytes: u64,
    pub cpu_usage_pct: f32,
    pub wal_spool_bytes: u64,
    pub active_adapters_count: usize,
    pub total_adapters_count: usize,
    pub autostart_enabled: bool,
    pub last_heartbeat_at: String,
    pub sync_status: String,
    pub last_sync_at: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct CollectorMetrics {
    pub events_per_second: f32,
    pub total_bytes_spooled: u64,
    pub total_bytes_uploaded: u64,
    pub last_sync_timestamp: Option<String>,
    pub error_count: u32,
    pub active_agent_ids: Vec<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AutostartInfo {
    pub enabled: bool,
    pub platform: String,
    pub method: String,
    pub target_path: String,
    pub details: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct DataDeletionResponse {
    pub requested_at: String,
    pub purged_events: u64,
    pub status: String,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct SyncRequestState {
    pub queued_events: usize,
    pub message: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct PersistedControl {
    global_paused: bool,
    agent_toggles: HashMap<String, bool>,
    metric_toggles: HashMap<String, bool>,
    is_public_leaderboard: bool,
    devices: Vec<CollectorDevice>,
    config_backups: Vec<ConfigBackup>,
    events_uploaded: u64,
    last_sync_time: Option<String>,
}

impl PersistedControl {
    fn initial(installation_id: &str, _autostart_enabled: bool) -> Self {
        let agent_toggles = agent_metadata()
            .into_iter()
            .map(|(id, _, _)| (id.to_string(), true))
            .collect();
        let metric_toggles = [
            "tokens",
            "sessions",
            "turns",
            "tools",
            "skills",
            "code",
            "cost",
            "subagents",
        ]
        .into_iter()
        .map(|metric| (metric.to_string(), true))
        .collect();
        let devices = vec![CollectorDevice {
            id: "current-device".into(),
            installation_id: installation_id.into(),
            name: "Current Collector".into(),
            platform: std::env::consts::OS.into(),
            os_version: std::env::consts::OS.into(),
            collector_version: COLLECTOR_VERSION.into(),
            key_fingerprint: "OS_KEYSTORE_MANAGED".into(),
            status: "ACTIVE".into(),
            last_sync_at: "NEVER".into(),
            pending_events: 0,
        }];
        Self {
            global_paused: false,
            agent_toggles,
            metric_toggles,
            is_public_leaderboard: false,
            devices,
            config_backups: Vec::new(),
            events_uploaded: 0,
            last_sync_time: None,
        }
    }

    fn snapshot(&self, autostart_enabled: bool) -> ConfigSnapshot {
        ConfigSnapshot {
            agent_toggles: self.agent_toggles.clone(),
            metric_toggles: self.metric_toggles.clone(),
            global_paused: self.global_paused,
            autostart_enabled,
            is_public_leaderboard: self.is_public_leaderboard,
        }
    }
}

struct EmptySecrets;
impl SecretResolver for EmptySecrets {
    fn resolve(&self, _secret_ref: &str) -> Result<Vec<u8>, String> {
        Err("secret_not_configured".into())
    }
}

#[derive(Default)]
struct MemoryBackupStore {
    values: HashMap<String, Vec<u8>>,
}

impl EncryptedBackupStore for MemoryBackupStore {
    type Handle = String;

    fn save_encrypted(
        &mut self,
        plan_id: &str,
        path: &Path,
        before_hash: &str,
        plaintext: &[u8],
    ) -> Result<Self::Handle, String> {
        let handle = format!("{plan_id}:{}:{before_hash}", path.display());
        self.values.insert(handle.clone(), plaintext.to_vec());
        Ok(handle)
    }

    fn restore_decrypted(&mut self, handle: &Self::Handle) -> Result<Vec<u8>, String> {
        self.values
            .get(handle)
            .cloned()
            .ok_or_else(|| "configuration rollback backup missing".into())
    }
}

#[derive(Clone)]
pub struct AppState {
    pub service: Arc<Mutex<ProductionService>>,
    pub detection: Arc<DetectionSnapshot>,
    pub sync_status: Arc<RwLock<String>>,
    control: Arc<RwLock<PersistedControl>>,
    control_dir: Arc<PathBuf>,
    start_time: Instant,
    autostart: Arc<dyn AutostartProvider>,
    shutting_down: Arc<StdMutex<bool>>,
    local_store: Arc<StdMutex<LocalStore>>,
    storage_error: Arc<StdMutex<Option<String>>>,
    rebuilding: Arc<StdMutex<bool>>,
}

impl AppState {
    pub async fn production() -> Result<Self, String> {
        let root = app_data_root();
        let key_provider: Arc<dyn KeyProvider> = Arc::new(OsKeyProvider::new(
            "io.tokendance.desktop",
            "collector-wal-key",
        ));
        Self::build(
            root,
            key_provider,
            Arc::new(SystemAutostartManager::new("TokenDanceCollector")),
        )
        .await
    }

    #[cfg(test)]
    pub async fn test(
        root: PathBuf,
        autostart: Arc<dyn AutostartProvider>,
    ) -> Result<Self, String> {
        Self::build(
            root,
            Arc::new(InjectedKeyProvider::new([0x61; 32])),
            autostart,
        )
        .await
    }

    async fn build(
        root: PathBuf,
        key_provider: Arc<dyn KeyProvider>,
        autostart: Arc<dyn AutostartProvider>,
    ) -> Result<Self, String> {
        fs::create_dir_all(&root).map_err(|error| error.to_string())?;
        let key = key_provider.data_key().map_err(|error| error.to_string())?;
        let wal =
            WalStore::open(root.join("spool"), key_provider).map_err(|error| error.to_string())?;
        let installation_id = load_or_create_installation_id(&root)?;
        let detection = if cfg!(test) {
            DetectionSnapshot::default()
        } else {
            detect_local()
        };
        let service = ProductionService::assemble(
            installation_id.clone(),
            &key,
            &detection,
            Arc::new(EmptySecrets),
            wal,
        )
        .await
        .map_err(|error| error.to_string())?;
        let autostart_enabled = autostart.is_enabled()?;
        let control = load_control(&root)?
            .unwrap_or_else(|| PersistedControl::initial(&installation_id, autostart_enabled));
        let state = Self {
            service: Arc::new(Mutex::new(service)),
            detection: Arc::new(detection),
            sync_status: Arc::new(RwLock::new("LOGIN_REQUIRED".into())),
            control: Arc::new(RwLock::new(control)),
            control_dir: Arc::new(root.clone()),
            start_time: Instant::now(),
            autostart,
            shutting_down: Arc::new(StdMutex::new(false)),
            local_store: Arc::new(StdMutex::new(LocalStore::open(&root)?)),
            storage_error: Arc::new(StdMutex::new(None)),
            rebuilding: Arc::new(StdMutex::new(false)),
        };
        state.apply_saved_agent_controls().await?;
        state.persist_control().await?;
        Ok(state)
    }

    pub fn control_dir_path(&self) -> PathBuf {
        (*self.control_dir).clone()
    }

    pub(crate) fn lock_store(&self) -> std::sync::MutexGuard<'_, LocalStore> {
        self.local_store.lock().expect("local store poisoned")
    }

    pub fn set_storage_error(&self, error: &str) {
        *self.storage_error.lock().expect("storage error poisoned") = Some(error.to_string());
    }

    pub fn clear_storage_error(&self) {
        *self.storage_error.lock().expect("storage error poisoned") = None;
    }

    pub fn set_rebuilding(&self, rebuilding: bool) {
        *self.rebuilding.lock().expect("rebuild flag poisoned") = rebuilding;
    }

    pub fn pending_sync_count(&self) -> usize {
        self.lock_store().aggregate_pending_count()
    }

    pub async fn activate_sync_account(&self, account_id: &str) -> Result<String, String> {
        let installation = self
            .service
            .lock()
            .await
            .collector
            .installation_id()
            .to_string();
        self.lock_store()
            .activate_account(account_id, &installation)
    }

    pub fn deactivate_sync_account(&self) -> Result<(), String> {
        self.lock_store().deactivate_account()
    }

    pub fn lease_sync_batch(&self, lease_id: &str, limit: usize) -> Result<LeasedBatch, String> {
        self.lock_store().lease_batch(lease_id, limit)
    }

    pub fn ack_sync_batch(
        &self,
        target_id: &str,
        lease_id: &str,
        event_ids: &[String],
    ) -> Result<usize, String> {
        self.lock_store().ack_leased(target_id, lease_id, event_ids)
    }

    pub fn isolate_sync_events(
        &self,
        target_id: &str,
        event_ids: &[String],
        error: &str,
    ) -> Result<(), String> {
        self.lock_store()
            .isolate_events(target_id, event_ids, error)
    }

    pub fn retry_sync_events(
        &self,
        target_id: &str,
        lease_id: &str,
        event_ids: &[String],
        error: &str,
        delay_secs: i64,
    ) -> Result<(), String> {
        self.lock_store()
            .retry_events(target_id, lease_id, event_ids, error, delay_secs)
    }

    pub fn release_sync_lease(&self, target_id: &str, lease_id: &str) -> Result<(), String> {
        self.lock_store().release_lease(target_id, lease_id)
    }

    pub fn sync_enabled(&self) -> bool {
        self.lock_store().sync_enabled()
    }

    pub async fn backfill_local_prices(&self) {
        let mut store = self.local_store.lock().expect("local store poisoned");
        if let Err(error) = store.reprice() {
            collector_service::runtime::append_log(
                &self.control_dir_path(),
                &format!("pricing reprice failed: {error}"),
            );
        }
    }
    pub async fn refresh_local_prices(&self) {
        if self
            .local_store
            .lock()
            .expect("local store poisoned")
            .catalog()
            .fresh()
        {
            return;
        }
        match crate::pricing::Catalog::fetch().await {
            Ok(catalog) => {
                let mut store = self.local_store.lock().expect("local store poisoned");
                let status = if store.apply_prices(catalog).is_ok() {
                    "OpenRouter pricing updated"
                } else {
                    "OpenRouter pricing save failed"
                };
                collector_service::runtime::append_log(&self.control_dir_path(), status);
            }
            Err(code) => collector_service::runtime::append_log(
                &self.control_dir_path(),
                &format!("OpenRouter prices unavailable; keeping cache: {code}"),
            ),
        }
    }
    pub fn commit_local(
        &self,
        events: &[EventEnvelope],
        checkpoints: &[wal_spool::SourceCheckpoint],
    ) -> Result<bool, String> {
        self.local_store
            .lock()
            .expect("local store poisoned")
            .commit_batch(events, checkpoints)
    }
    pub fn record_usage(&self, events: &[EventEnvelope]) -> bool {
        match self.commit_local(events, &[]) {
            Ok(changed) => changed,
            Err(error) => {
                collector_service::runtime::append_log(
                    &self.control_dir_path(),
                    &format!("存储异常: {error}"),
                );
                false
            }
        }
    }

    async fn apply_saved_agent_controls(&self) -> Result<(), String> {
        let toggles = self.control.read().await.agent_toggles.clone();
        let mut service = self.service.lock().await;
        for (id, _, adapter_id) in agent_metadata() {
            let enabled = toggles.get(id).copied().unwrap_or(true);
            if enabled {
                service.collector.enable(adapter_id)
            } else {
                service.collector.disable(adapter_id)
            }
            .map_err(|error| error.to_string())?;
        }
        Ok(())
    }

    pub async fn get_daemon_status(&self) -> DaemonStatus {
        let control = self.control.read().await;
        let sync_status = self.sync_status.read().await.clone();
        let service = self.service.lock().await;
        let runtimes = service.collector.runtimes();
        let active = runtimes
            .iter()
            .filter(|runtime| runtime.enabled && runtime.detected)
            .count();
        let wal_spool_bytes = service.wal.spool_bytes();
        let total_adapters_count = runtimes.len();
        drop(service);
        let store = self.lock_store();
        let pending = store.aggregate_pending_count();
        let collected = store.event_count();
        let rebuilding =
            *self.rebuilding.lock().expect("rebuild flag poisoned") || store.rebuild_in_progress();
        drop(store);
        let storage_error = self
            .storage_error
            .lock()
            .expect("storage error poisoned")
            .is_some();
        let collection_status = if control.global_paused {
            "PAUSED"
        } else if storage_error {
            "STORAGE_ERROR"
        } else if rebuilding {
            "REBUILDING"
        } else {
            "RUNNING"
        };
        DaemonStatus {
            status: collection_status.into(),
            global_paused: control.global_paused,
            pid: std::process::id(),
            uptime_secs: self.start_time.elapsed().as_secs(),
            collector_version: COLLECTOR_VERSION.into(),
            events_collected: collected,
            events_pending: pending,
            events_uploaded: control.events_uploaded,
            memory_rss_bytes: 0,
            cpu_usage_pct: 0.0,
            wal_spool_bytes,
            active_adapters_count: active,
            total_adapters_count,
            autostart_enabled: self.autostart.is_enabled().unwrap_or(false),
            last_heartbeat_at: Utc::now().to_rfc3339(),
            sync_status,
            last_sync_at: control.last_sync_time.clone(),
        }
    }

    pub async fn set_global_pause(
        &self,
        paused: bool,
    ) -> Result<OperationAck<DaemonStatus>, String> {
        self.control.write().await.global_paused = paused;
        self.persist_control().await?;
        Ok(OperationAck::acknowledged(self.get_daemon_status().await))
    }

    pub async fn toggle_global_pause(&self) -> Result<OperationAck<DaemonStatus>, String> {
        let paused = !self.control.read().await.global_paused;
        self.set_global_pause(paused).await
    }

    pub async fn get_agents(&self) -> Vec<AgentConfig> {
        let control = self.control.read().await;
        let service = self.service.lock().await;
        let store = self.local_store.lock().expect("local store poisoned");
        let today = Local::now().date_naive();
        agent_metadata()
            .into_iter()
            .filter_map(|(id, name, adapter_id)| {
                let runtime = service.collector.runtime(adapter_id)?;
                let enabled = control
                    .agent_toggles
                    .get(id)
                    .copied()
                    .unwrap_or(runtime.enabled);
                let usage = store.agent_usage(id, today);
                let (accuracy, today_tokens, total_tokens, daily_usage) = match &usage {
                    Some(usage) => (
                        usage.accuracy.clone(),
                        usage.today_tokens,
                        usage.total_tokens,
                        usage.daily_usage.clone(),
                    ),
                    None => ("unknown".to_string(), 0, 0, Vec::new()),
                };
                Some(AgentConfig {
                    id: id.into(),
                    name: name.into(),
                    adapter_id: runtime.adapter_id.clone(),
                    adapter_version: runtime.adapter_version.clone(),
                    status: runtime_status(runtime, control.global_paused, enabled),
                    setup_plan_status: runtime
                        .setup_plan_status
                        .map(|status| format!("{status:?}").to_uppercase())
                        .unwrap_or_else(|| "UNCONFIGURED".into()),
                    enabled,
                    accuracy,
                    sources: runtime
                        .sources
                        .iter()
                        .map(|source| source.id().to_string())
                        .collect(),
                    capabilities: Vec::new(),
                    today_tokens,
                    total_tokens,
                    daily_usage,
                    total_costs: usage
                        .as_ref()
                        .map(|item| item.total_costs.clone())
                        .unwrap_or_default(),
                    pricing: usage
                        .as_ref()
                        .map(|u| u.pricing.clone())
                        .unwrap_or_default(),
                    history_start: usage.as_ref().map(|item| item.history_start.clone()),
                    last_active: if runtime.detected {
                        "DETECTED"
                    } else {
                        "UNDETECTED"
                    }
                    .into(),
                    version: runtime
                        .agent_version
                        .clone()
                        .unwrap_or_else(|| "unknown".into()),
                })
            })
            .collect()
    }

    pub async fn set_agent_status(
        &self,
        agent_id: &str,
        enabled: bool,
    ) -> Result<OperationAck<AgentConfig>, String> {
        let (_, _, adapter_id) = agent_metadata()
            .into_iter()
            .find(|(id, _, _)| *id == agent_id)
            .ok_or_else(|| format!("Agent '{agent_id}' not found"))?;
        {
            let mut service = self.service.lock().await;
            if enabled {
                service.collector.enable(adapter_id)
            } else {
                service.collector.disable(adapter_id)
            }
            .map_err(|error| error.to_string())?;
        }
        self.control
            .write()
            .await
            .agent_toggles
            .insert(agent_id.into(), enabled);
        self.persist_control().await?;
        let agent = self
            .get_agents()
            .await
            .into_iter()
            .find(|agent| agent.id == agent_id)
            .ok_or_else(|| "agent state readback failed".to_string())?;
        Ok(OperationAck::acknowledged(agent))
    }

    pub async fn toggle_agent(&self, agent_id: &str) -> Result<OperationAck<AgentConfig>, String> {
        let enabled = self
            .control
            .read()
            .await
            .agent_toggles
            .get(agent_id)
            .copied()
            .ok_or_else(|| format!("Agent '{agent_id}' not found"))?;
        self.set_agent_status(agent_id, !enabled).await
    }

    pub async fn preview_upload_batch(&self) -> OperationAck<UploadBatchPreview> {
        let installation_id = self
            .service
            .lock()
            .await
            .collector
            .installation_id()
            .to_string();
        let events = self
            .lock_store()
            .peek_pending_events(100)
            .into_iter()
            .filter_map(|event| serde_json::to_value(event).ok())
            .collect::<Vec<_>>();
        OperationAck::acknowledged(UploadBatchPreview {
            batch_id: format!("preview_{}", Uuid::new_v4().simple()),
            installation_id,
            created_at: Utc::now().to_rfc3339(),
            event_count: events.len(),
            events,
            redaction_applied: true,
        })
    }

    pub async fn get_outbox(&self) -> Vec<OutboxEnvelope> {
        self.lock_store()
            .pending_outbox(100)
            .into_iter()
            .filter_map(|(event, status)| {
                let value = serde_json::to_value(&event).ok()?;
                let event_id = value.get("eventId")?.as_str()?.to_string();
                let payload = value.get("payload").cloned().unwrap_or_default();
                let event_type = payload
                    .get("type")
                    .and_then(|item| item.as_str())
                    .unwrap_or("event")
                    .to_string();
                Some(OutboxEnvelope {
                    id: event_id.clone(),
                    event_id,
                    adapter_id: event.adapter_id,
                    adapter_version: event.adapter_version,
                    agent_id: event.agent_id,
                    occurred_at: event.occurred_at,
                    event_type,
                    delivery_status: status.to_uppercase(),
                    accuracy: format!("{:?}", event.accuracy).to_lowercase(),
                    payload_summary: "privacy-filtered event".into(),
                    payload,
                })
            })
            .collect()
    }

    pub async fn trigger_sync_now(&self) -> Result<OperationAck<SyncRequestState>, String> {
        Err("Manual sync was replaced by automatic sync after sign-in.".into())
    }

    pub async fn acknowledge_auto_sync(&self, ack: AckPayload) -> Result<(), String> {
        let count = ack.acked_event_ids.len() as u64;
        let server_time = ack.server_acked_at.clone();
        {
            let mut control = self.control.write().await;
            control.events_uploaded = control.events_uploaded.saturating_add(count);
            control.last_sync_time = Some(server_time);
        }
        self.persist_control().await
    }

    pub async fn create_config_backup(
        &self,
        description: Option<String>,
    ) -> Result<ConfigBackup, String> {
        let autostart = self.autostart.is_enabled()?;
        let mut control = self.control.write().await;
        let backup = ConfigBackup {
            id: format!("backup_{}", Uuid::new_v4().simple()),
            created_at: Utc::now().to_rfc3339(),
            version_tag: format!("v{}", control.config_backups.len() + 1),
            description: description.unwrap_or_else(|| "Collector control snapshot".into()),
            snapshot: control.snapshot(autostart),
        };
        control.config_backups.insert(0, backup.clone());
        drop(control);
        self.persist_control().await?;
        Ok(backup)
    }

    pub async fn restore_config_backup(
        &self,
        backup_id: &str,
    ) -> Result<OperationAck<ConfigSnapshot>, String> {
        let snapshot = self
            .control
            .read()
            .await
            .config_backups
            .iter()
            .find(|backup| backup.id == backup_id)
            .map(|backup| backup.snapshot.clone())
            .ok_or_else(|| format!("Backup snapshot '{backup_id}' not found"))?;
        if snapshot.autostart_enabled {
            self.autostart.enable()?;
        } else {
            self.autostart.disable()?;
        }
        {
            let mut service = self.service.lock().await;
            for (id, enabled) in &snapshot.agent_toggles {
                if let Some((_, _, adapter_id)) = agent_metadata()
                    .into_iter()
                    .find(|(agent_id, _, _)| *agent_id == id)
                {
                    if *enabled {
                        service.collector.enable(adapter_id)
                    } else {
                        service.collector.disable(adapter_id)
                    }
                    .map_err(|error| error.to_string())?;
                }
            }
        }
        {
            let mut control = self.control.write().await;
            control.global_paused = snapshot.global_paused;
            control.agent_toggles = snapshot.agent_toggles.clone();
            control.metric_toggles = snapshot.metric_toggles.clone();
            control.is_public_leaderboard = snapshot.is_public_leaderboard;
        }
        self.persist_control().await?;
        let readback = self
            .control
            .read()
            .await
            .snapshot(self.autostart.is_enabled()?);
        Ok(OperationAck::acknowledged(readback))
    }

    pub async fn list_config_backups(&self) -> Vec<ConfigBackup> {
        self.control.read().await.config_backups.clone()
    }

    pub async fn list_devices(&self) -> Vec<CollectorDevice> {
        let pending = self.pending_sync_count() as u32;
        let mut devices = self.control.read().await.devices.clone();
        if let Some(current) = devices.first_mut() {
            current.pending_events = pending;
        }
        devices
    }

    pub async fn revoke_device(
        &self,
        device_id: &str,
    ) -> Result<OperationAck<CollectorDevice>, String> {
        let device = {
            let mut control = self.control.write().await;
            let device = control
                .devices
                .iter_mut()
                .find(|device| device.id == device_id)
                .ok_or_else(|| format!("Device '{device_id}' not found"))?;
            device.status = "REVOCATION_PENDING".into();
            device.clone()
        };
        self.persist_control().await?;
        Ok(OperationAck::pending(device))
    }

    pub async fn request_data_deletion(
        &self,
    ) -> Result<OperationAck<DataDeletionResponse>, String> {
        let state = DataDeletionResponse {
            requested_at: Utc::now().to_rfc3339(),
            purged_events: 0,
            status: "DELETION_PENDING".into(),
            message:
                "Remote deletion confirmation is pending; local WAL remains intact until confirmed."
                    .into(),
        };
        Ok(OperationAck::pending(state))
    }

    pub async fn purge_local_queue(&self) -> Result<OperationAck<u64>, String> {
        let count = self.ack_all_local("local-purge").await?;
        Ok(OperationAck::acknowledged(count))
    }

    async fn ack_all_local(&self, reason: &str) -> Result<u64, String> {
        let mut service = self.service.lock().await;
        let ids = service
            .wal
            .unacked_events()
            .into_iter()
            .map(|event| event.event_id.to_string())
            .collect::<Vec<_>>();
        if !ids.is_empty() {
            service
                .wal
                .append_ack(AckPayload {
                    batch_id: format!("{reason}_{}", Uuid::new_v4().simple()),
                    acked_event_ids: ids.clone(),
                    server_acked_at: Utc::now().to_rfc3339(),
                })
                .map_err(|error| error.to_string())?;
            service.wal.compact().map_err(|error| error.to_string())?;
        }
        Ok(ids.len() as u64)
    }

    pub async fn get_collector_metrics(&self) -> CollectorMetrics {
        let control = self.control.read().await;
        let service = self.service.lock().await;
        CollectorMetrics {
            events_per_second: 0.0,
            total_bytes_spooled: service.wal.spool_bytes(),
            total_bytes_uploaded: 0,
            last_sync_timestamp: control.last_sync_time.clone(),
            error_count: service.wal.isolated_segments().len() as u32,
            active_agent_ids: service
                .collector
                .runtimes()
                .into_iter()
                .filter(|runtime| runtime.enabled && runtime.detected)
                .map(|runtime| runtime.adapter_id.clone())
                .collect(),
        }
    }

    pub fn get_autostart_status(&self) -> Result<AutostartInfo, String> {
        self.autostart.get_info()
    }

    pub fn set_autostart(&self, enabled: bool) -> Result<AutostartInfo, String> {
        if enabled {
            self.autostart.enable()
        } else {
            self.autostart.disable()
        }
    }

    pub async fn shutdown(&self) -> Result<(), String> {
        {
            let mut shutting_down = self
                .shutting_down
                .lock()
                .map_err(|_| "shutdown lock poisoned")?;
            if *shutting_down {
                return Ok(());
            }
            *shutting_down = true;
        }
        self.persist_control().await?;
        let mut service = self.service.lock().await;
        service.wal.snapshot().map_err(|error| error.to_string())?;
        service.wal.compact().map_err(|error| error.to_string())?;
        Ok(())
    }

    async fn persist_control(&self) -> Result<(), String> {
        let control = self.control.read().await.clone();
        let patch = serde_json::to_value(control).map_err(|error| error.to_string())?;
        let plan = SetupPlan {
            plan_id: format!("desktop-control-{}", Uuid::new_v4().simple()),
            adapter_id: "desktop-control".into(),
            summary: "Persist desktop collector control state".into(),
            mutations: vec![ConfigMutation::JsonMergePatch {
                path_template: "${ControlRoot}/control.json".into(),
                patch,
            }],
            required_permissions: Vec::new(),
            verify: Vec::new(),
            rollback: Vec::new(),
        };
        let mut roots = HashMap::new();
        roots.insert("ControlRoot".into(), self.control_dir.as_ref().clone());
        let mut executor = SetupPlanExecutor::new(
            roots,
            MemoryBackupStore::default(),
            SemanticVerifier::default(),
        );
        executor.approve(&plan).map_err(|error| error.to_string())?;
        executor.apply(&plan).map_err(|error| error.to_string())?;
        Ok(())
    }
}

pub(crate) fn app_data_root() -> PathBuf {
    if let Some(path) = std::env::var_os("TOKENDANCE_DATA_DIR").filter(|p| !p.is_empty()) {
        return PathBuf::from(path);
    }
    let base = std::env::var_os("LOCALAPPDATA")
        .or_else(|| std::env::var_os("XDG_DATA_HOME"))
        .or_else(|| std::env::var_os("HOME"))
        .map(PathBuf::from)
        .unwrap_or_else(std::env::temp_dir);
    base.join("TokenDance").join("collector")
}

fn load_or_create_installation_id(root: &Path) -> Result<String, String> {
    let path = root.join("installation-id");
    if path.exists() {
        return fs::read_to_string(path)
            .map(|value| value.trim().to_string())
            .map_err(|error| error.to_string());
    }
    let id = format!("ins_{}", ulid::Ulid::new());
    fs::write(path, &id).map_err(|error| error.to_string())?;
    Ok(id)
}

fn load_control(root: &Path) -> Result<Option<PersistedControl>, String> {
    let path = root.join("control.json");
    if !path.exists() {
        return Ok(None);
    }
    let bytes = fs::read(path).map_err(|error| error.to_string())?;
    serde_json::from_slice(&bytes)
        .map(Some)
        .map_err(|error| error.to_string())
}

fn agent_metadata() -> [(&'static str, &'static str, &'static str); 7] {
    [
        ("codex", "Codex", "dev.tokenshow.adapter.codex"),
        ("claude-code", "Claude Code", "dev.tokenshow.adapter.claude"),
        (
            "grok-build",
            "Grok Build",
            "dev.tokenshow.adapter.grok-build",
        ),
        ("cursor", "Cursor", "dev.tokenshow.adapter.cursor"),
        ("zcode", "ZCode", "dev.tokenshow.adapter.zcode"),
        ("pi", "Pi", "dev.tokenshow.adapter.pi"),
        (
            "deepseek-harness",
            "DeepSeek Harness",
            "dev.tokenshow.adapter.deepseek-harness",
        ),
    ]
}

fn runtime_status(runtime: &collector_core::AdapterRuntime, paused: bool, enabled: bool) -> String {
    if !enabled {
        "DISABLED".into()
    } else if paused {
        "PAUSED".into()
    } else {
        format!("{:?}", runtime.status).to_uppercase()
    }
}
