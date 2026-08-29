use std::collections::HashMap;
use std::sync::Arc;
use std::time::Instant;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use tokio::sync::RwLock;
use uuid::Uuid;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
#[serde(rename_all = "camelCase")]
pub struct AgentConfig {
    pub id: String,
    pub name: String,
    pub adapter_id: String,
    pub adapter_version: String,
    pub status: String, // "ACTIVE", "CONFIGURING", "NEEDS_PERMISSION", "DISABLED", "DEGRADED", "ERROR"
    pub setup_plan_status: String, // "APPLIED", "PROPOSED", "ROLLED_BACK"
    pub enabled: bool,
    pub accuracy: String, // "exact", "derived", "correlated", "estimated"
    pub sources: Vec<String>,
    pub capabilities: Vec<String>,
    pub today_tokens: u64,
    pub total_tokens: u64,
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
    pub status: String, // "ACTIVE", "REVOKED"
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
    pub delivery_status: String, // "QUEUED", "IN_FLIGHT", "ACKED"
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
    pub status: String, // "RUNNING", "PAUSED", "DEGRADED", "STOPPED"
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
    pub success: bool,
    pub requested_at: String,
    pub purged_events: u64,
    pub status: String,
    pub message: String,
}

pub struct StateInner {
    pub start_time: Instant,
    pub global_paused: bool,
    pub autostart_enabled: bool,
    pub events_collected_counter: u64,
    pub events_uploaded_counter: u64,
    pub agents: Vec<AgentConfig>,
    pub devices: Vec<CollectorDevice>,
    pub config_backups: Vec<ConfigBackup>,
    pub outbox: Vec<OutboxEnvelope>,
    pub metric_toggles: HashMap<String, bool>,
    pub is_public_leaderboard: bool,
    pub last_sync_time: Option<DateTime<Utc>>,
}

#[derive(Clone)]
pub struct AppState {
    pub inner: Arc<RwLock<StateInner>>,
}

impl Default for AppState {
    fn default() -> Self {
        Self::new()
    }
}

impl AppState {
    pub fn new() -> Self {
        let initial_agents = vec![
            AgentConfig {
                id: "codex".into(),
                name: "Codex".into(),
                adapter_id: "adapter-codex".into(),
                adapter_version: "1.4.2".into(),
                status: "ACTIVE".into(),
                setup_plan_status: "APPLIED".into(),
                enabled: true,
                accuracy: "exact".into(),
                sources: vec!["otlp".into(), "file_snapshot".into()],
                capabilities: vec!["tokens".into(), "code".into(), "turns".into(), "sessions".into(), "skills".into(), "cost".into()],
                today_tokens: 1_420_000,
                total_tokens: 101_000_000,
                last_active: "2 分钟前".into(),
                version: "2026.8".into(),
            },
            AgentConfig {
                id: "claude-code".into(),
                name: "Claude Code".into(),
                adapter_id: "adapter-claude".into(),
                adapter_version: "1.5.0".into(),
                status: "ACTIVE".into(),
                setup_plan_status: "APPLIED".into(),
                enabled: true,
                accuracy: "exact".into(),
                sources: vec!["otlp".into(), "runtime_stream".into()],
                capabilities: vec!["tokens".into(), "turns".into(), "sessions".into(), "tools".into(), "skills".into(), "code".into(), "subagents".into()],
                today_tokens: 2_150_000,
                total_tokens: 136_800_000,
                last_active: "刚刚".into(),
                version: "0.2.19".into(),
            },
            AgentConfig {
                id: "grok-build".into(),
                name: "Grok Build".into(),
                adapter_id: "adapter-grok-build".into(),
                adapter_version: "1.2.0".into(),
                status: "ACTIVE".into(),
                setup_plan_status: "APPLIED".into(),
                enabled: true,
                accuracy: "derived".into(),
                sources: vec!["otlp".into(), "local_http_api".into()],
                capabilities: vec!["tokens".into(), "code".into(), "turns".into(), "sessions".into(), "tools".into(), "subagents".into()],
                today_tokens: 890_000,
                total_tokens: 32_500_000,
                last_active: "12 分钟前".into(),
                version: "1.8.4".into(),
            },
            AgentConfig {
                id: "cursor".into(),
                name: "Cursor".into(),
                adapter_id: "adapter-cursor".into(),
                adapter_version: "1.3.1".into(),
                status: "ACTIVE".into(),
                setup_plan_status: "APPLIED".into(),
                enabled: true,
                accuracy: "correlated".into(),
                sources: vec!["sqlite_snapshot".into(), "jsonl".into()],
                capabilities: vec!["tokens".into(), "turns".into(), "sessions".into(), "code".into()],
                today_tokens: 640_000,
                total_tokens: 55_400_000,
                last_active: "35 分钟前".into(),
                version: "0.45.2".into(),
            },
            AgentConfig {
                id: "zcode".into(),
                name: "ZCode".into(),
                adapter_id: "adapter-zcode".into(),
                adapter_version: "0.9.0".into(),
                status: "CONFIGURING".into(),
                setup_plan_status: "PROPOSED".into(),
                enabled: false,
                accuracy: "estimated".into(),
                sources: vec!["file_snapshot".into(), "jsonl".into()],
                capabilities: vec!["tokens".into(), "code".into(), "sessions".into(), "turns".into(), "skills".into()],
                today_tokens: 0,
                total_tokens: 0,
                last_active: "未连接".into(),
                version: "0.9.1-preview".into(),
            },
            AgentConfig {
                id: "deepseek-harness".into(),
                name: "DeepSeek Harness".into(),
                adapter_id: "adapter-deepseek-harness".into(),
                adapter_version: "1.1.0".into(),
                status: "NEEDS_PERMISSION".into(),
                setup_plan_status: "PROPOSED".into(),
                enabled: false,
                accuracy: "derived".into(),
                sources: vec!["otlp".into(), "remote_api".into()],
                capabilities: vec!["tokens".into(), "turns".into(), "sessions".into(), "cost".into()],
                today_tokens: 0,
                total_tokens: 0,
                last_active: "等待授权".into(),
                version: "2.1.0".into(),
            },
        ];

        let initial_devices = vec![
            CollectorDevice {
                id: "dev-win-01".into(),
                installation_id: "inst_win_studio_77af".into(),
                name: "Windows Studio (Current)".into(),
                platform: "windows".into(),
                os_version: "Windows 11 Pro 24H2".into(),
                collector_version: "1.2.0".into(),
                key_fingerprint: "ed25519:SHA256:4f8a...99b2".into(),
                status: "ACTIVE".into(),
                last_sync_at: "刚刚".into(),
                pending_events: 0,
            },
            CollectorDevice {
                id: "dev-mac-02".into(),
                installation_id: "inst_mac_bookpro_e312".into(),
                name: "MacBook Pro".into(),
                platform: "macos".into(),
                os_version: "macOS Sonoma 14.5".into(),
                collector_version: "1.2.0".into(),
                key_fingerprint: "ed25519:SHA256:a71c...04d8".into(),
                status: "ACTIVE".into(),
                last_sync_at: "8 分钟前".into(),
                pending_events: 3,
            },
        ];

        let mut metric_toggles = HashMap::new();
        metric_toggles.insert("tokens".into(), true);
        metric_toggles.insert("sessions".into(), true);
        metric_toggles.insert("turns".into(), true);
        metric_toggles.insert("tools".into(), true);
        metric_toggles.insert("skills".into(), true);
        metric_toggles.insert("code".into(), true);
        metric_toggles.insert("cost".into(), true);
        metric_toggles.insert("subagents".into(), true);

        let initial_backup = ConfigBackup {
            id: "backup_baseline_v1".into(),
            created_at: Utc::now().to_rfc3339(),
            version_tag: "v1.0.0-initial".into(),
            description: "首次建档默认基准配置快照".into(),
            snapshot: ConfigSnapshot {
                agent_toggles: initial_agents.iter().map(|a| (a.id.clone(), a.enabled)).collect(),
                metric_toggles: metric_toggles.clone(),
                global_paused: false,
                autostart_enabled: true,
                is_public_leaderboard: false,
            },
        };

        let initial_outbox = vec![
            OutboxEnvelope {
                id: "out-001".into(),
                event_id: "evt_01J6A1B2C3D4E5F6G7H8J9K0L1".into(),
                adapter_id: "adapter-claude".into(),
                adapter_version: "1.5.0".into(),
                agent_id: "claude-code".into(),
                occurred_at: Utc::now().to_rfc3339(),
                event_type: "model_usage_recorded".into(),
                delivery_status: "QUEUED".into(),
                accuracy: "exact".into(),
                payload_summary: "claude-3-7-sonnet-20250219 | 6,250 tokens (cache: 1,200)".into(),
                payload: serde_json::json!({
                    "type": "model_usage_recorded",
                    "providerId": "anthropic",
                    "modelId": "claude-3-7-sonnet-20250219",
                    "tokens": { "inputTokens": 4200, "outputTokens": 850, "cacheReadTokens": 1200, "totalTokens": 6250 },
                    "durationMs": 3420,
                    "success": true
                }),
            },
            OutboxEnvelope {
                id: "out-002".into(),
                event_id: "evt_01J6A1B2C3D4E5F6G7H8J9K0L2".into(),
                adapter_id: "adapter-codex".into(),
                adapter_version: "1.4.2".into(),
                agent_id: "codex".into(),
                occurred_at: Utc::now().to_rfc3339(),
                event_type: "code_changed".into(),
                delivery_status: "QUEUED".into(),
                accuracy: "exact".into(),
                payload_summary: "+48 lines / -6 lines across 2 files".into(),
                payload: serde_json::json!({
                    "type": "code_changed",
                    "codeAddedLines": 48,
                    "codeDeletedLines": 6,
                    "codeFileCount": 2,
                    "success": true
                }),
            },
            OutboxEnvelope {
                id: "out-003".into(),
                event_id: "evt_01J6A1B2C3D4E5F6G7H8J9K0L3".into(),
                adapter_id: "adapter-claude".into(),
                adapter_version: "1.5.0".into(),
                agent_id: "claude-code".into(),
                occurred_at: Utc::now().to_rfc3339(),
                event_type: "skill_invoked".into(),
                delivery_status: "QUEUED".into(),
                accuracy: "exact".into(),
                payload_summary: "skill: codex-review (slash_command)".into(),
                payload: serde_json::json!({
                    "type": "skill_invoked",
                    "skillKey": "codex-review",
                    "skillPublicName": "codex-review",
                    "skillInvokeType": "slash_command",
                    "durationMs": 1820,
                    "success": true
                }),
            },
        ];

        Self {
            inner: Arc::new(RwLock::new(StateInner {
                start_time: Instant::now(),
                global_paused: false,
                autostart_enabled: true,
                events_collected_counter: 12480,
                events_uploaded_counter: 12477,
                agents: initial_agents,
                devices: initial_devices,
                config_backups: vec![initial_backup],
                outbox: initial_outbox,
                metric_toggles,
                is_public_leaderboard: false,
                last_sync_time: Some(Utc::now()),
            })),
        }
    }

    pub async fn get_daemon_status(&self) -> DaemonStatus {
        let state = self.inner.read().await;
        let uptime_secs = state.start_time.elapsed().as_secs();
        let active_count = state.agents.iter().filter(|a| a.enabled && a.status == "ACTIVE").count();
        let total_count = state.agents.len();
        let status_str = if state.global_paused {
            "PAUSED"
        } else if active_count == 0 {
            "DEGRADED"
        } else {
            "RUNNING"
        };

        DaemonStatus {
            status: status_str.into(),
            global_paused: state.global_paused,
            pid: std::process::id(),
            uptime_secs,
            collector_version: "1.2.0".into(),
            events_collected: state.events_collected_counter,
            events_pending: state.outbox.iter().filter(|e| e.delivery_status != "ACKED").count(),
            events_uploaded: state.events_uploaded_counter,
            memory_rss_bytes: 42_580_000 + (uptime_secs % 100) * 1024,
            cpu_usage_pct: if state.global_paused { 0.1 } else { 0.8 },
            wal_spool_bytes: (state.outbox.len() as u64) * 2048,
            active_adapters_count: active_count,
            total_adapters_count: total_count,
            autostart_enabled: state.autostart_enabled,
            last_heartbeat_at: Utc::now().to_rfc3339(),
        }
    }

    pub async fn set_global_pause(&self, paused: bool) -> bool {
        let mut state = self.inner.write().await;
        state.global_paused = paused;
        for agent in state.agents.iter_mut() {
            if agent.enabled {
                agent.status = if paused { "DEGRADED".into() } else { "ACTIVE".into() };
            }
        }
        paused
    }

    pub async fn toggle_global_pause(&self) -> bool {
        let paused = {
            let state = self.inner.read().await;
            !state.global_paused
        };
        self.set_global_pause(paused).await
    }

    pub async fn get_agents(&self) -> Vec<AgentConfig> {
        let state = self.inner.read().await;
        state.agents.clone()
    }

    pub async fn set_agent_status(&self, agent_id: &str, enabled: bool) -> Result<AgentConfig, String> {
        let mut state = self.inner.write().await;
        let paused = state.global_paused;
        if let Some(agent) = state.agents.iter_mut().find(|a| a.id == agent_id) {
            agent.enabled = enabled;
            if enabled {
                agent.status = if paused { "DEGRADED".into() } else { "ACTIVE".into() };
                agent.setup_plan_status = "APPLIED".into();
            } else {
                agent.status = "DISABLED".into();
                agent.setup_plan_status = "ROLLED_BACK".into();
            }
            Ok(agent.clone())
        } else {
            Err(format!("Agent '{}' not found", agent_id))
        }
    }

    pub async fn toggle_agent(&self, agent_id: &str) -> Result<AgentConfig, String> {
        let current_enabled = {
            let state = self.inner.read().await;
            state.agents.iter().find(|a| a.id == agent_id).map(|a| a.enabled)
        };

        match current_enabled {
            Some(enabled) => self.set_agent_status(agent_id, !enabled).await,
            None => Err(format!("Agent '{}' not found", agent_id)),
        }
    }

    pub async fn get_outbox(&self) -> Vec<OutboxEnvelope> {
        let state = self.inner.read().await;
        state.outbox.clone()
    }

    pub async fn preview_upload_batch(&self) -> UploadBatchPreview {
        let state = self.inner.read().await;
        let inst_id = state.devices.first().map(|d| d.installation_id.clone()).unwrap_or_else(|| "inst_win_studio_77af".into());
        let events = state.outbox.iter().map(|env| {
            serde_json::json!({
                "schemaVersion": "1.0",
                "eventId": env.event_id,
                "adapterId": env.adapter_id,
                "adapterVersion": env.adapter_version,
                "agentId": env.agent_id,
                "installationId": inst_id,
                "occurredAt": env.occurred_at,
                "sessionHash": "sess_sha256_masked",
                "turnHash": "turn_sha256_masked",
                "accuracy": env.accuracy,
                "source": { "kind": "otlp", "cursor": "wal_pos_auto", "rawFingerprint": "fp_redacted" },
                "payload": env.payload
            })
        }).collect::<Vec<_>>();

        UploadBatchPreview {
            batch_id: format!("batch_preview_{}", Uuid::new_v4().simple()),
            installation_id: inst_id,
            created_at: Utc::now().to_rfc3339(),
            event_count: events.len(),
            events,
            redaction_applied: true,
        }
    }

    pub async fn trigger_sync_now(&self) -> Result<serde_json::Value, String> {
        let mut state = self.inner.write().await;
        if state.global_paused {
            return Err("全局采集已暂停，无法上报".into());
        }

        let pending_count = state.outbox.iter().filter(|e| e.delivery_status != "ACKED").count();
        let batch_id = format!("batch_{}_{}", Utc::now().timestamp(), &Uuid::new_v4().to_string()[0..6]);
        let now = Utc::now();

        for env in state.outbox.iter_mut() {
            env.delivery_status = "ACKED".into();
        }

        state.events_uploaded_counter += pending_count as u64;
        state.last_sync_time = Some(now);

        if let Some(dev) = state.devices.first_mut() {
            dev.last_sync_at = "刚刚".into();
            dev.pending_events = 0;
        }

        Ok(serde_json::json!({
            "batchId": batch_id,
            "accepted": pending_count,
            "duplicates": 0,
            "rejected": [],
            "serverTime": now.to_rfc3339()
        }))
    }

    pub async fn create_config_backup(&self, description: Option<String>) -> ConfigBackup {
        let mut state = self.inner.write().await;
        let count = state.config_backups.len() + 1;
        let backup = ConfigBackup {
            id: format!("backup_{}_{}", Utc::now().timestamp(), &Uuid::new_v4().to_string()[0..4]),
            created_at: Utc::now().to_rfc3339(),
            version_tag: format!("v1.0.{}", count),
            description: description.unwrap_or_else(|| "用户手动创建的配置快照".into()),
            snapshot: ConfigSnapshot {
                agent_toggles: state.agents.iter().map(|a| (a.id.clone(), a.enabled)).collect(),
                metric_toggles: state.metric_toggles.clone(),
                global_paused: state.global_paused,
                autostart_enabled: state.autostart_enabled,
                is_public_leaderboard: state.is_public_leaderboard,
            },
        };
        state.config_backups.insert(0, backup.clone());
        backup
    }

    pub async fn restore_config_backup(&self, backup_id: &str) -> Result<bool, String> {
        let mut state = self.inner.write().await;
        let backup = match state.config_backups.iter().find(|b| b.id == backup_id) {
            Some(b) => b.clone(),
            None => return Err(format!("Backup snapshot '{}' not found", backup_id)),
        };

        let snap = backup.snapshot;
        state.global_paused = snap.global_paused;
        state.autostart_enabled = snap.autostart_enabled;
        state.metric_toggles = snap.metric_toggles;
        state.is_public_leaderboard = snap.is_public_leaderboard;

        for agent in state.agents.iter_mut() {
            let enabled = snap.agent_toggles.get(&agent.id).copied().unwrap_or(agent.enabled);
            agent.enabled = enabled;
            if enabled {
                agent.status = if snap.global_paused { "DEGRADED".into() } else { "ACTIVE".into() };
                agent.setup_plan_status = "APPLIED".into();
            } else {
                agent.status = "DISABLED".into();
                agent.setup_plan_status = "ROLLED_BACK".into();
            }
        }

        Ok(true)
    }

    pub async fn list_config_backups(&self) -> Vec<ConfigBackup> {
        let state = self.inner.read().await;
        state.config_backups.clone()
    }

    pub async fn list_devices(&self) -> Vec<CollectorDevice> {
        let state = self.inner.read().await;
        state.devices.clone()
    }

    pub async fn revoke_device(&self, device_id: &str) -> Result<bool, String> {
        let mut state = self.inner.write().await;
        if let Some(dev) = state.devices.iter_mut().find(|d| d.id == device_id) {
            dev.status = "REVOKED".into();
            dev.last_sync_at = "已撤销".into();
            Ok(true)
        } else {
            Err(format!("Device '{}' not found", device_id))
        }
    }

    pub async fn request_data_deletion(&self) -> DataDeletionResponse {
        let mut state = self.inner.write().await;
        let purged_count = state.outbox.len() as u64;
        state.outbox.clear();
        state.events_collected_counter = 0;
        state.events_uploaded_counter = 0;
        state.is_public_leaderboard = false;

        DataDeletionResponse {
            success: true,
            requested_at: Utc::now().to_rfc3339(),
            purged_events: purged_count,
            status: "DELETION_PENDING".into(),
            message: "数据擦除指令已下发：本地队列已彻底清空，服务端数据删除流程已登记并在保护期后永久清除。".into(),
        }
    }

    pub async fn purge_local_cache(&self) -> u64 {
        let mut state = self.inner.write().await;
        let count = state.outbox.len() as u64;
        state.outbox.clear();
        count
    }

    pub async fn get_collector_metrics(&self) -> CollectorMetrics {
        let state = self.inner.read().await;
        let active_ids = state.agents.iter().filter(|a| a.enabled).map(|a| a.id.clone()).collect();
        let last_sync = state.last_sync_time.map(|t| t.to_rfc3339());

        CollectorMetrics {
            events_per_second: if state.global_paused { 0.0 } else { 4.2 },
            total_bytes_spooled: (state.outbox.len() as u64) * 2048,
            total_bytes_uploaded: state.events_uploaded_counter * 1840,
            last_sync_timestamp: last_sync,
            error_count: 0,
            active_agent_ids: active_ids,
        }
    }

    pub async fn set_autostart_state(&self, enabled: bool) {
        let mut state = self.inner.write().await;
        state.autostart_enabled = enabled;
    }
}
