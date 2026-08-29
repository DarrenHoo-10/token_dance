import { invoke } from "@tauri-apps/api/core";

export interface AgentConfig {
  id: string;
  name: string;
  adapterId: string;
  adapterVersion: string;
  status: "ACTIVE" | "CONFIGURING" | "NEEDS_PERMISSION" | "DISABLED" | "DEGRADED" | "ERROR";
  setupPlanStatus: "APPLIED" | "PROPOSED" | "ROLLED_BACK";
  enabled: boolean;
  accuracy: "exact" | "derived" | "correlated" | "estimated";
  sources: string[];
  capabilities: string[];
  todayTokens: number;
  totalTokens: number;
  lastActive: string;
  version: string;
}

export interface CollectorDevice {
  id: string;
  installationId: string;
  name: string;
  platform: string;
  osVersion: string;
  collectorVersion: string;
  keyFingerprint: string;
  status: "ACTIVE" | "REVOKED";
  lastSyncAt: string;
  pendingEvents: number;
}

export interface ConfigSnapshot {
  agentToggles: Record<string, boolean>;
  metricToggles: Record<string, boolean>;
  globalPaused: boolean;
  autostartEnabled: boolean;
  isPublicLeaderboard: boolean;
}

export interface ConfigBackup {
  id: string;
  createdAt: string;
  versionTag: string;
  description: string;
  snapshot: ConfigSnapshot;
}

export interface OutboxEnvelope {
  id: string;
  eventId: string;
  adapterId: string;
  adapterVersion: string;
  agentId: string;
  occurredAt: string;
  eventType: string;
  deliveryStatus: "QUEUED" | "IN_FLIGHT" | "ACKED";
  accuracy: string;
  payloadSummary: string;
  payload: Record<string, unknown>;
}

export interface UploadBatchPreview {
  batchId: string;
  installationId: string;
  createdAt: string;
  eventCount: number;
  events: Record<string, unknown>[];
  redactionApplied: boolean;
}

export interface DaemonStatus {
  status: "RUNNING" | "PAUSED" | "DEGRADED" | "STOPPED";
  globalPaused: boolean;
  pid: number;
  uptimeSecs: number;
  collectorVersion: string;
  eventsCollected: number;
  eventsPending: number;
  eventsUploaded: number;
  memoryRssBytes: number;
  cpuUsagePct: number;
  walSpoolBytes: number;
  activeAdaptersCount: number;
  totalAdaptersCount: number;
  autostartEnabled: boolean;
  lastHeartbeatAt: string;
}

export interface CollectorMetrics {
  eventsPerSecond: number;
  totalBytesSpooled: number;
  totalBytesUploaded: number;
  lastSyncTimestamp?: string;
  errorCount: number;
  activeAgentIds: string[];
}

export interface AutostartInfo {
  enabled: boolean;
  platform: string;
  method: string;
  targetPath: string;
  details: string;
}

export interface DataDeletionResponse {
  success: boolean;
  requestedAt: string;
  purgedEvents: number;
  status: string;
  message: string;
}

export interface UploadAck {
  batchId: string;
  accepted: number;
  duplicates: number;
  rejected: string[];
  serverTime: string;
}

// Check if running inside real Tauri environment
export function isTauriEnvironment(): boolean {
  return typeof window !== "undefined" && ("__TAURI_INTERNALS__" in window || "__TAURI__" in window);
}

// Fallback Mock State for Browser / Headless Testing
const mockState = {
  globalPaused: false,
  autostartEnabled: true,
  eventsCollected: 12480,
  eventsUploaded: 12477,
  agents: [
    {
      id: "codex",
      name: "Codex",
      adapterId: "adapter-codex",
      adapterVersion: "1.4.2",
      status: "ACTIVE" as const,
      setupPlanStatus: "APPLIED" as const,
      enabled: true,
      accuracy: "exact" as const,
      sources: ["otlp", "file_snapshot"],
      capabilities: ["tokens", "code", "turns", "sessions", "skills", "cost"],
      todayTokens: 1420000,
      totalTokens: 101000000,
      lastActive: "2 分钟前",
      version: "2026.8",
    },
    {
      id: "claude-code",
      name: "Claude Code",
      adapterId: "adapter-claude",
      adapterVersion: "1.5.0",
      status: "ACTIVE" as const,
      setupPlanStatus: "APPLIED" as const,
      enabled: true,
      accuracy: "exact" as const,
      sources: ["otlp", "runtime_stream"],
      capabilities: ["tokens", "turns", "sessions", "tools", "skills", "code", "subagents"],
      todayTokens: 2150000,
      totalTokens: 136800000,
      lastActive: "刚刚",
      version: "0.2.19",
    },
    {
      id: "grok-build",
      name: "Grok Build",
      adapterId: "adapter-grok-build",
      adapterVersion: "1.2.0",
      status: "ACTIVE" as const,
      setupPlanStatus: "APPLIED" as const,
      enabled: true,
      accuracy: "derived" as const,
      sources: ["otlp", "local_http_api"],
      capabilities: ["tokens", "code", "turns", "sessions", "tools", "subagents"],
      todayTokens: 890000,
      totalTokens: 32500000,
      lastActive: "12 分钟前",
      version: "1.8.4",
    },
    {
      id: "cursor",
      name: "Cursor",
      adapterId: "adapter-cursor",
      adapterVersion: "1.3.1",
      status: "ACTIVE" as const,
      setupPlanStatus: "APPLIED" as const,
      enabled: true,
      accuracy: "correlated" as const,
      sources: ["sqlite_snapshot", "jsonl"],
      capabilities: ["tokens", "turns", "sessions", "code"],
      todayTokens: 640000,
      totalTokens: 55400000,
      lastActive: "35 分钟前",
      version: "0.45.2",
    },
    {
      id: "zcode",
      name: "ZCode",
      adapterId: "adapter-zcode",
      adapterVersion: "0.9.0",
      status: "CONFIGURING" as const,
      setupPlanStatus: "PROPOSED" as const,
      enabled: false,
      accuracy: "estimated" as const,
      sources: ["file_snapshot", "jsonl"],
      capabilities: ["tokens", "code", "sessions", "turns", "skills"],
      todayTokens: 0,
      totalTokens: 0,
      lastActive: "未连接",
      version: "0.9.1-preview",
    },
    {
      id: "deepseek-harness",
      name: "DeepSeek Harness",
      adapterId: "adapter-deepseek-harness",
      adapterVersion: "1.1.0",
      status: "NEEDS_PERMISSION" as const,
      setupPlanStatus: "PROPOSED" as const,
      enabled: false,
      accuracy: "derived" as const,
      sources: ["otlp", "remote_api"],
      capabilities: ["tokens", "turns", "sessions", "cost"],
      todayTokens: 0,
      totalTokens: 0,
      lastActive: "等待授权",
      version: "2.1.0",
    },
  ] as AgentConfig[],
  devices: [
    {
      id: "dev-win-01",
      installationId: "inst_win_studio_77af",
      name: "Windows Studio (Current)",
      platform: "windows",
      osVersion: "Windows 11 Pro 24H2",
      collectorVersion: "1.2.0",
      keyFingerprint: "ed25519:SHA256:4f8a...99b2",
      status: "ACTIVE" as const,
      lastSyncAt: "刚刚",
      pendingEvents: 0,
    },
    {
      id: "dev-mac-02",
      installationId: "inst_mac_bookpro_e312",
      name: "MacBook Pro",
      platform: "macos",
      osVersion: "macOS Sonoma 14.5",
      collectorVersion: "1.2.0",
      keyFingerprint: "ed25519:SHA256:a71c...04d8",
      status: "ACTIVE" as const,
      lastSyncAt: "8 分钟前",
      pendingEvents: 3,
    },
  ] as CollectorDevice[],
  configBackups: [
    {
      id: "backup_baseline_v1",
      createdAt: new Date().toISOString(),
      versionTag: "v1.0.0-initial",
      description: "首次建档默认基准配置快照",
      snapshot: {
        agentToggles: {
          codex: true,
          "claude-code": true,
          "grok-build": true,
          cursor: true,
          zcode: false,
          "deepseek-harness": false,
        },
        metricToggles: {
          tokens: true,
          sessions: true,
          turns: true,
          tools: true,
          skills: true,
          code: true,
          cost: true,
          subagents: true,
        },
        globalPaused: false,
        autostartEnabled: true,
        isPublicLeaderboard: false,
      },
    },
  ] as ConfigBackup[],
  outbox: [
    {
      id: "out-001",
      eventId: "evt_01J6A1B2C3D4E5F6G7H8J9K0L1",
      adapterId: "adapter-claude",
      adapterVersion: "1.5.0",
      agentId: "claude-code",
      occurredAt: new Date().toISOString(),
      eventType: "model_usage_recorded",
      deliveryStatus: "QUEUED" as const,
      accuracy: "exact",
      payloadSummary: "claude-3-7-sonnet-20250219 | 6,250 tokens (cache: 1,200)",
      payload: {
        type: "model_usage_recorded",
        providerId: "anthropic",
        modelId: "claude-3-7-sonnet-20250219",
        tokens: { inputTokens: 4200, outputTokens: 850, cacheReadTokens: 1200, totalTokens: 6250 },
        durationMs: 3420,
        success: true,
      },
    },
    {
      id: "out-002",
      eventId: "evt_01J6A1B2C3D4E5F6G7H8J9K0L2",
      adapterId: "adapter-codex",
      adapterVersion: "1.4.2",
      agentId: "codex",
      occurredAt: new Date().toISOString(),
      eventType: "code_changed",
      deliveryStatus: "QUEUED" as const,
      accuracy: "exact",
      payloadSummary: "+48 lines / -6 lines across 2 files",
      payload: {
        type: "code_changed",
        codeAddedLines: 48,
        codeDeletedLines: 6,
        codeFileCount: 2,
        success: true,
      },
    },
  ] as OutboxEnvelope[],
};

// Tauri IPC Command Implementations

export async function getDaemonStatus(): Promise<DaemonStatus> {
  if (isTauriEnvironment()) {
    return await invoke<DaemonStatus>("get_daemon_status");
  }
  const activeCount = mockState.agents.filter((a) => a.enabled && a.status === "ACTIVE").length;
  return {
    status: mockState.globalPaused ? "PAUSED" : activeCount === 0 ? "DEGRADED" : "RUNNING",
    globalPaused: mockState.globalPaused,
    pid: 14820,
    uptimeSecs: 3600,
    collectorVersion: "1.2.0",
    eventsCollected: mockState.eventsCollected,
    eventsPending: mockState.outbox.filter((e) => e.deliveryStatus !== "ACKED").length,
    eventsUploaded: mockState.eventsUploaded,
    memoryRssBytes: 43280000,
    cpuUsagePct: mockState.globalPaused ? 0.1 : 0.8,
    walSpoolBytes: mockState.outbox.length * 2048,
    activeAdaptersCount: activeCount,
    totalAdaptersCount: mockState.agents.length,
    autostartEnabled: mockState.autostartEnabled,
    lastHeartbeatAt: new Date().toISOString(),
  };
}

export async function toggleGlobalPause(): Promise<boolean> {
  if (isTauriEnvironment()) {
    return await invoke<boolean>("toggle_global_pause");
  }
  mockState.globalPaused = !mockState.globalPaused;
  mockState.agents = mockState.agents.map((a) => ({
    ...a,
    status: a.enabled ? (mockState.globalPaused ? "DEGRADED" : "ACTIVE") : a.status,
  }));
  return mockState.globalPaused;
}

export async function setGlobalPause(paused: boolean): Promise<boolean> {
  if (isTauriEnvironment()) {
    return await invoke<boolean>("set_global_pause", { paused });
  }
  mockState.globalPaused = paused;
  mockState.agents = mockState.agents.map((a) => ({
    ...a,
    status: a.enabled ? (paused ? "DEGRADED" : "ACTIVE") : a.status,
  }));
  return paused;
}

export async function getCollectorMetrics(): Promise<CollectorMetrics> {
  if (isTauriEnvironment()) {
    return await invoke<CollectorMetrics>("get_collector_metrics");
  }
  return {
    eventsPerSecond: mockState.globalPaused ? 0.0 : 4.2,
    totalBytesSpooled: mockState.outbox.length * 2048,
    totalBytesUploaded: mockState.eventsUploaded * 1840,
    lastSyncTimestamp: new Date().toISOString(),
    errorCount: 0,
    activeAgentIds: mockState.agents.filter((a) => a.enabled).map((a) => a.id),
  };
}

export async function getAgentConfigs(): Promise<AgentConfig[]> {
  if (isTauriEnvironment()) {
    return await invoke<AgentConfig[]>("get_agent_configs");
  }
  return [...mockState.agents];
}

export async function toggleAgent(agentId: string): Promise<AgentConfig> {
  if (isTauriEnvironment()) {
    return await invoke<AgentConfig>("toggle_agent", { agentId });
  }
  const ag = mockState.agents.find((a) => a.id === agentId);
  if (!ag) throw new Error(`Agent '${agentId}' not found`);
  ag.enabled = !ag.enabled;
  ag.status = ag.enabled ? (mockState.globalPaused ? "DEGRADED" : "ACTIVE") : "DISABLED";
  ag.setupPlanStatus = ag.enabled ? "APPLIED" : "ROLLED_BACK";
  return { ...ag };
}

export async function setAgentStatus(agentId: string, enabled: boolean): Promise<AgentConfig> {
  if (isTauriEnvironment()) {
    return await invoke<AgentConfig>("set_agent_status", { agentId, enabled });
  }
  const ag = mockState.agents.find((a) => a.id === agentId);
  if (!ag) throw new Error(`Agent '${agentId}' not found`);
  ag.enabled = enabled;
  ag.status = enabled ? (mockState.globalPaused ? "DEGRADED" : "ACTIVE") : "DISABLED";
  ag.setupPlanStatus = enabled ? "APPLIED" : "ROLLED_BACK";
  return { ...ag };
}

export async function previewUploadBatch(): Promise<UploadBatchPreview> {
  if (isTauriEnvironment()) {
    return await invoke<UploadBatchPreview>("preview_upload_batch");
  }
  const instId = mockState.devices[0]?.installationId || "inst_win_studio_77af";
  return {
    batchId: `batch_preview_${Date.now()}`,
    installationId: instId,
    createdAt: new Date().toISOString(),
    eventCount: mockState.outbox.length,
    events: mockState.outbox.map((e) => ({
      schemaVersion: "1.0",
      eventId: e.eventId,
      adapterId: e.adapterId,
      adapterVersion: e.adapterVersion,
      agentId: e.agentId,
      installationId: instId,
      occurredAt: e.occurredAt,
      sessionHash: "sess_sha256_masked",
      turnHash: "turn_sha256_masked",
      accuracy: e.accuracy,
      source: { kind: "otlp", cursor: "wal_pos_auto", rawFingerprint: "fp_redacted" },
      payload: e.payload,
    })),
    redactionApplied: true,
  };
}

export async function triggerSyncNow(): Promise<UploadAck> {
  if (isTauriEnvironment()) {
    return (await invoke<UploadAck>("trigger_sync_now")) as UploadAck;
  }
  if (mockState.globalPaused) {
    throw new Error("全局采集已暂停，无法上报");
  }
  const count = mockState.outbox.filter((e) => e.deliveryStatus !== "ACKED").length;
  mockState.outbox = mockState.outbox.map((e) => ({ ...e, deliveryStatus: "ACKED" }));
  mockState.eventsUploaded += count;
  if (mockState.devices[0]) {
    mockState.devices[0].lastSyncAt = "刚刚";
    mockState.devices[0].pendingEvents = 0;
  }
  return {
    batchId: `batch_${Date.now()}_ack`,
    accepted: count,
    duplicates: 0,
    rejected: [],
    serverTime: new Date().toISOString(),
  };
}

export async function getPendingEnvelopes(): Promise<OutboxEnvelope[]> {
  if (isTauriEnvironment()) {
    return await invoke<OutboxEnvelope[]>("get_pending_envelopes");
  }
  return [...mockState.outbox];
}

export async function createConfigBackup(description?: string): Promise<ConfigBackup> {
  if (isTauriEnvironment()) {
    return await invoke<ConfigBackup>("create_config_backup", { description });
  }
  const count = mockState.configBackups.length + 1;
  const backup: ConfigBackup = {
    id: `backup_${Date.now()}`,
    createdAt: new Date().toISOString(),
    versionTag: `v1.0.${count}`,
    description: description || "用户手动创建的配置快照",
    snapshot: {
      agentToggles: mockState.agents.reduce((acc, a) => ({ ...acc, [a.id]: a.enabled }), {}),
      metricToggles: { tokens: true, sessions: true, turns: true, tools: true, skills: true, code: true, cost: true, subagents: true },
      globalPaused: mockState.globalPaused,
      autostartEnabled: mockState.autostartEnabled,
      isPublicLeaderboard: false,
    },
  };
  mockState.configBackups.unshift(backup);
  return backup;
}

export async function restoreConfigBackup(backupId: string): Promise<boolean> {
  if (isTauriEnvironment()) {
    return await invoke<boolean>("restore_config_backup", { backupId });
  }
  const backup = mockState.configBackups.find((b) => b.id === backupId);
  if (!backup) throw new Error(`Backup snapshot '${backupId}' not found`);
  const snap = backup.snapshot;
  mockState.globalPaused = snap.globalPaused;
  mockState.autostartEnabled = snap.autostartEnabled;
  mockState.agents = mockState.agents.map((a) => {
    const enabled = snap.agentToggles[a.id] ?? a.enabled;
    return {
      ...a,
      enabled,
      status: enabled ? (snap.globalPaused ? "DEGRADED" : "ACTIVE") : "DISABLED",
      setupPlanStatus: enabled ? "APPLIED" : "ROLLED_BACK",
    };
  });
  return true;
}

export async function listConfigBackups(): Promise<ConfigBackup[]> {
  if (isTauriEnvironment()) {
    return await invoke<ConfigBackup[]>("list_config_backups");
  }
  return [...mockState.configBackups];
}

export async function listDevices(): Promise<CollectorDevice[]> {
  if (isTauriEnvironment()) {
    return await invoke<CollectorDevice[]>("list_devices");
  }
  return [...mockState.devices];
}

export async function revokeDevice(deviceId: string): Promise<boolean> {
  if (isTauriEnvironment()) {
    return await invoke<boolean>("revoke_device", { deviceId });
  }
  const dev = mockState.devices.find((d) => d.id === deviceId);
  if (!dev) throw new Error(`Device '${deviceId}' not found`);
  dev.status = "REVOKED";
  dev.lastSyncAt = "已撤销";
  return true;
}

export async function requestDataDeletion(): Promise<DataDeletionResponse> {
  if (isTauriEnvironment()) {
    return await invoke<DataDeletionResponse>("request_data_deletion");
  }
  const count = mockState.outbox.length;
  mockState.outbox = [];
  mockState.eventsCollected = 0;
  mockState.eventsUploaded = 0;
  return {
    success: true,
    requestedAt: new Date().toISOString(),
    purgedEvents: count,
    status: "DELETION_PENDING",
    message: "数据擦除指令已下发：本地队列已彻底清空，服务端数据删除流程已登记并在保护期后永久清除。",
  };
}

export async function purgeLocalCache(): Promise<number> {
  if (isTauriEnvironment()) {
    return await invoke<number>("purge_local_cache");
  }
  const count = mockState.outbox.length;
  mockState.outbox = [];
  return count;
}

export async function getAutostartStatus(): Promise<AutostartInfo> {
  if (isTauriEnvironment()) {
    return await invoke<AutostartInfo>("get_autostart_status");
  }
  const isWin = typeof navigator !== "undefined" && navigator.userAgent.includes("Windows");
  return {
    enabled: mockState.autostartEnabled,
    platform: isWin ? "windows" : "macos",
    method: isWin ? "HKCU_Registry_Run" : "LaunchAgents_Plist",
    targetPath: isWin
      ? "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run\\TokenDanceCollector"
      : "~/Library/LaunchAgents/io.tokendance.collector.plist",
    details: isWin
      ? 'Command: "tokendance-collector.exe" --minimized'
      : "User-level LaunchAgents plist",
  };
}

export async function setAutostart(enabled: boolean): Promise<AutostartInfo> {
  if (isTauriEnvironment()) {
    return await invoke<AutostartInfo>("set_autostart", { enabled });
  }
  mockState.autostartEnabled = enabled;
  return await getAutostartStatus();
}

export async function hideWindow(): Promise<void> {
  if (isTauriEnvironment()) {
    await invoke("hide_window");
  }
}

export async function showWindow(): Promise<void> {
  if (isTauriEnvironment()) {
    await invoke("show_window");
  }
}

export async function quitApp(): Promise<void> {
  if (isTauriEnvironment()) {
    await invoke("quit_app");
  }
}
