import React, { createContext, useContext, useState, useMemo, useCallback } from "react";
import type {
  AdapterRuntimeStatus,
  Capability,
  EventEnvelope,
  EventPayload,
  EventType,
  UploadBatch,
  UploadAck,
} from "../protocol/generated.ts";
import { PROTOCOL_VERSION } from "../protocol/generated.ts";
import type {
  AccountStatus,
  UserProfile,
  PrivacyScopeSettings,
  AgentInfo,
  CollectorDevice,
  ConfigBackup,
  OutboxItem,
  SyncLogEntry,
  PersonalMetrics,
  SkillUsage,
  LeaderboardEntry,
} from "../types.ts";

const WIRE_HMAC = `hmac-sha256:${"A".repeat(43)}`;
const DEFAULT_INSTALLATION_ID = `ins_${"0".repeat(26)}`;
const SECONDARY_INSTALLATION_ID = `ins_${"1".repeat(26)}`;
const wireEventId = (suffix: string) => `${suffix}${"B".repeat(43)}`.slice(0, 43);
const wireBatchId = (suffix: string) => `bat_${`${suffix}${"0".repeat(26)}`.slice(0, 26).toUpperCase()}`;

const INITIAL_PROFILE: UserProfile = {
  id: "usr_tokendance_01",
  email: "developer@tokendance.io",
  nickname: "Hoo Darren",
  handle: "darrenhoo",
  bio: "Building intelligent agents with full telemetry & token dance.",
  avatarText: "HD",
  timezone: "Asia/Shanghai",
  language: "zh",
  createdAt: "2026-08-01T08:00:00Z",
};

const INITIAL_PRIVACY: PrivacyScopeSettings = {
  isPublicLeaderboard: false, // Default is STRICTLY PRIVATE
  showTokenTotals: true,
  showTokenTrends: true,
  showAgentBreakdown: true,
  showActivityHeatmap: false,
  showTopSkills: true,
  showAICodeLines: true,
};

const INITIAL_AGENTS: AgentInfo[] = [
  {
    id: "codex",
    name: "Codex",
    adapterId: "adapter-codex",
    adapterVersion: "1.4.2",
    status: "ACTIVE",
    setupPlanStatus: "APPLIED",
    checkpointStatus: "CURRENT",
    capabilities: ["tokens", "code", "turns", "sessions", "skills", "cost"],
    sources: ["otlp", "file_snapshot"],
    accuracy: "exact",
    enabled: true,
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
    status: "ACTIVE",
    setupPlanStatus: "APPLIED",
    checkpointStatus: "CURRENT",
    capabilities: ["tokens", "turns", "sessions", "tools", "skills", "code", "subagents"],
    sources: ["otlp", "runtime_stream"],
    accuracy: "exact",
    enabled: true,
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
    status: "ACTIVE",
    setupPlanStatus: "APPLIED",
    checkpointStatus: "CURRENT",
    capabilities: ["tokens", "code", "turns", "sessions", "tools", "subagents"],
    sources: ["otlp", "local_http_api"],
    accuracy: "derived",
    enabled: true,
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
    status: "ACTIVE",
    setupPlanStatus: "APPLIED",
    checkpointStatus: "CURRENT",
    capabilities: ["tokens", "turns", "sessions", "code"],
    sources: ["sqlite_snapshot", "jsonl_tail"],
    accuracy: "correlated",
    enabled: true,
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
    status: "CONFIGURING",
    setupPlanStatus: "PROPOSED",
    checkpointStatus: "SCANNING",
    capabilities: ["tokens", "code", "sessions", "turns", "skills"],
    sources: ["file_snapshot", "jsonl_tail"],
    accuracy: "estimated",
    enabled: false,
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
    status: "NEEDS_PERMISSION",
    setupPlanStatus: "PROPOSED",
    checkpointStatus: "DISCOVERED",
    capabilities: ["tokens", "turns", "sessions", "cost"],
    sources: ["otlp", "remote_api"],
    accuracy: "derived",
    enabled: false,
    todayTokens: 0,
    totalTokens: 0,
    lastActive: "等待授权",
    version: "2.1.0",
  },
];

const INITIAL_METRICS: PersonalMetrics = {
  estimatedCost: 1428.6,
  totalTokens: 325700000,
  inputContextTokens: 184600000,
  outputTokens: 78300000,
  cacheTokens: 62800000,
  cacheHitRate: 38.6,
  codeLinesAdded: 864200,
  codeLinesDeleted: 142000,
  tokensPerLine: 286.4,
  totalHours: 482.6,
  totalSessions: 1840,
  totalTurns: 42800,
  userMessages: 18600,
  globalRank: 37,
  streakDays: 23,
};

const INITIAL_SKILLS: SkillUsage[] = [
  { id: "sk-1", name: "codex-review", invokeCount: 1284, daysUsed: 18, trend: "+24%", accuracy: "exact" },
  { id: "sk-2", name: "commit-context", invokeCount: 936, daysUsed: 14, trend: "+12%", accuracy: "derived" },
  { id: "sk-3", name: "imagegen", invokeCount: 622, daysUsed: 9, trend: "+8%", accuracy: "exact" },
  { id: "sk-4", name: "test-scaffolder", invokeCount: 451, daysUsed: 7, trend: "+19%", accuracy: "exact" },
  { id: "sk-5", name: "refactor-ast", invokeCount: 318, daysUsed: 5, trend: "+5%", accuracy: "correlated" },
];

const INITIAL_DEVICES: CollectorDevice[] = [
  {
    id: "dev-win-01",
    installationId: DEFAULT_INSTALLATION_ID,
    name: "Windows Studio",
    platform: "windows",
    osVersion: "Windows 11 Pro 24H2",
    collectorVersion: "1.2.0",
    keyFingerprint: "ed25519:SHA256:4f8a...99b2",
    status: "ACTIVE",
    lastSyncAt: "刚刚",
    pendingEvents: 0,
  },
  {
    id: "dev-mac-02",
    installationId: SECONDARY_INSTALLATION_ID,
    name: "MacBook Pro",
    platform: "macos",
    osVersion: "macOS Sonoma 14.5",
    collectorVersion: "1.2.0",
    keyFingerprint: "ed25519:SHA256:a71c...04d8",
    status: "ACTIVE",
    lastSyncAt: "8 分钟前",
    pendingEvents: 3,
  },
];

const INITIAL_OUTBOX: OutboxItem[] = [
  {
    id: "out-001",
    deliveryStatus: "QUEUED",
    retryCount: 0,
    queuedAt: "2026-08-30T10:14:00Z",
    envelope: {
      schemaVersion: PROTOCOL_VERSION,
      eventId: wireEventId("C"),
      adapterId: "adapter-claude",
      adapterVersion: "1.5.0",
      agentId: "claude-code",
      installationId: DEFAULT_INSTALLATION_ID,
      occurredAt: "2026-08-30T10:13:58Z",
      sessionHash: WIRE_HMAC,
      source: { kind: "otlp", cursorHmac: WIRE_HMAC, rawFingerprintHmac: WIRE_HMAC },
      accuracy: "exact",
      payload: {
        type: "model_usage_recorded",
        providerId: "anthropic",
        modelId: "claude-3-7-sonnet-20250219",
        tokens: { inputTokens: "4200", outputTokens: "850", cacheReadTokens: "1200", totalTokens: "6250" },
      },
    },
  },
  {
    id: "out-002",
    deliveryStatus: "QUEUED",
    retryCount: 0,
    queuedAt: "2026-08-30T10:14:05Z",
    envelope: {
      schemaVersion: PROTOCOL_VERSION,
      eventId: wireEventId("D"),
      adapterId: "adapter-codex",
      adapterVersion: "1.4.2",
      agentId: "codex",
      installationId: DEFAULT_INSTALLATION_ID,
      occurredAt: "2026-08-30T10:14:02Z",
      sessionHash: WIRE_HMAC,
      source: { kind: "file_snapshot", cursorHmac: WIRE_HMAC, rawFingerprintHmac: WIRE_HMAC },
      accuracy: "exact",
      payload: {
        type: "code_changed",
        addedLines: "48",
        removedLines: "6",
        fileCount: 2,
      },
    },
  },
  {
    id: "out-003",
    deliveryStatus: "QUEUED",
    retryCount: 0,
    queuedAt: "2026-08-30T10:14:10Z",
    envelope: {
      schemaVersion: PROTOCOL_VERSION,
      eventId: wireEventId("E"),
      adapterId: "adapter-claude",
      adapterVersion: "1.5.0",
      agentId: "claude-code",
      installationId: DEFAULT_INSTALLATION_ID,
      occurredAt: "2026-08-30T10:14:08Z",
      sessionHash: WIRE_HMAC,
      source: { kind: "otlp", cursorHmac: WIRE_HMAC, rawFingerprintHmac: WIRE_HMAC },
      accuracy: "exact",
      payload: {
        type: "skill_invoked",
        skillKey: WIRE_HMAC,
        invokeType: "native",
        durationMs: "1820",
        success: true,
      },
    },
  },
];

const INITIAL_SYNC_LOGS: SyncLogEntry[] = [
  {
    id: "sync-101",
    batchId: "batch_20260830_0950_a1",
    timestamp: "2026-08-30 09:50:12",
    status: "ACKED",
    eventCount: 42,
    acceptedCount: 42,
    duplicatesCount: 0,
    rejectedCount: 0,
    deviceInstallationId: DEFAULT_INSTALLATION_ID,
  },
  {
    id: "sync-100",
    batchId: "batch_20260830_0940_b2",
    timestamp: "2026-08-30 09:40:05",
    status: "ACKED",
    eventCount: 28,
    acceptedCount: 28,
    duplicatesCount: 0,
    rejectedCount: 0,
    deviceInstallationId: SECONDARY_INSTALLATION_ID,
  },
];

const INITIAL_LEADERBOARD: LeaderboardEntry[] = [
  {
    rank: 1,
    handle: "maxbauer",
    nickname: "maxbauer",
    avatarText: "MB",
    totalTokens: 325700000,
    codeLines: 864200,
    topAgent: "Claude Code",
    topAgentShare: 62,
    accuracy: "exact",
  },
  {
    rank: 2,
    handle: "sophiadev",
    nickname: "sophiadev",
    avatarText: "SD",
    totalTokens: 215400000,
    codeLines: 612000,
    topAgent: "Claude Code",
    topAgentShare: 58,
    accuracy: "exact",
  },
  {
    rank: 3,
    handle: "deworap",
    nickname: "deworap",
    avatarText: "DW",
    totalTokens: 178900000,
    codeLines: 490500,
    topAgent: "Codex",
    topAgentShare: 55,
    accuracy: "exact",
  },
  {
    rank: 4,
    handle: "builderdan",
    nickname: "builderdan",
    avatarText: "BD",
    totalTokens: 142600000,
    codeLines: 388000,
    topAgent: "Claude Code",
    topAgentShare: 60,
    accuracy: "derived",
  },
  {
    rank: 5,
    handle: "kaytee",
    nickname: "kaytee",
    avatarText: "KT",
    totalTokens: 118300000,
    codeLines: 320100,
    topAgent: "Codex",
    topAgentShare: 52,
    accuracy: "exact",
  },
];

export interface TokenShowContextValue {
  // State
  accountStatus: AccountStatus;
  user: UserProfile;
  privacy: PrivacyScopeSettings;
  agents: AgentInfo[];
  metricToggles: Record<Capability, boolean>;
  globalPaused: boolean;
  isOnline: boolean;
  devices: CollectorDevice[];
  configBackups: ConfigBackup[];
  outbox: OutboxItem[];
  syncLogs: SyncLogEntry[];
  metrics: PersonalMetrics;
  skills: SkillUsage[];
  leaderboard: LeaderboardEntry[];
  isAuthModalOpen: boolean;
  isOnboardingOpen: boolean;
  isUploadPreviewOpen: boolean;
  activeLanguage: "zh" | "en";
  activeTab: "dashboard" | "agents" | "queue" | "privacy" | "devices" | "leaderboard";

  // Actions
  setAccountStatus: (status: AccountStatus) => void;
  setUser: React.Dispatch<React.SetStateAction<UserProfile>>;
  setActiveTab: (tab: "dashboard" | "agents" | "queue" | "privacy" | "devices" | "leaderboard") => void;
  setActiveLanguage: (lang: "zh" | "en") => void;
  setIsAuthModalOpen: (open: boolean) => void;
  setIsOnboardingOpen: (open: boolean) => void;
  setIsUploadPreviewOpen: (open: boolean) => void;

  // Real State Transitions
  login: (email: string, password?: string) => Promise<boolean>;
  register: (email: string, code: string, password?: string) => Promise<boolean>;
  completeOnboarding: (profile: Partial<UserProfile>, privacyChoice: "private" | "public") => void;
  toggleGlobalPause: () => void;
  toggleAgent: (agentId: string, enabled?: boolean) => void;
  setAgentRuntimeStatus: (agentId: string, status: AdapterRuntimeStatus) => void;
  toggleMetric: (metric: Capability, enabled?: boolean) => void;
  updatePrivacyScope: (updates: Partial<PrivacyScopeSettings>) => void;
  triggerSyncNow: () => Promise<UploadAck>;
  toggleNetworkSimulation: () => void;
  revokeDevice: (deviceId: string) => void;
  createConfigBackup: (description?: string) => ConfigBackup;
  restoreConfigBackup: (backupId: string) => boolean;
  requestDataDeletion: () => void;
  generateSampleEnvelope: (eventType?: EventType) => EventEnvelope;
  generateSampleBatch: () => UploadBatch;
}

const TokenShowContext = createContext<TokenShowContextValue | null>(null);

export const TokenShowProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [accountStatus, setAccountStatus] = useState<AccountStatus>("active_private");
  const [user, setUser] = useState<UserProfile>(INITIAL_PROFILE);
  const [privacy, setPrivacy] = useState<PrivacyScopeSettings>(INITIAL_PRIVACY);
  const [agents, setAgents] = useState<AgentInfo[]>(INITIAL_AGENTS);
  const [globalPaused, setGlobalPaused] = useState<boolean>(false);
  const [isOnline, setIsOnline] = useState<boolean>(true);
  const [devices, setDevices] = useState<CollectorDevice[]>(INITIAL_DEVICES);
  const [outbox, setOutbox] = useState<OutboxItem[]>(INITIAL_OUTBOX);
  const [syncLogs, setSyncLogs] = useState<SyncLogEntry[]>(INITIAL_SYNC_LOGS);
  const [metrics, setMetrics] = useState<PersonalMetrics>(INITIAL_METRICS);
  const [skills] = useState<SkillUsage[]>(INITIAL_SKILLS);
  const [activeLanguage, setActiveLanguage] = useState<"zh" | "en">("zh");
  const [activeTab, setActiveTab] = useState<"dashboard" | "agents" | "queue" | "privacy" | "devices" | "leaderboard">("dashboard");
  const [isAuthModalOpen, setIsAuthModalOpen] = useState<boolean>(false);
  const [isOnboardingOpen, setIsOnboardingOpen] = useState<boolean>(false);
  const [isUploadPreviewOpen, setIsUploadPreviewOpen] = useState<boolean>(false);

  const [metricToggles, setMetricToggles] = useState<Record<Capability, boolean>>({
    tokens: true,
    sessions: true,
    turns: true,
    tools: true,
    skills: true,
    code: true,
    cost: true,
    subagents: true,
  });

  const [configBackups, setConfigBackups] = useState<ConfigBackup[]>([
    {
      id: "backup_baseline_v1",
      createdAt: "2026-08-30T09:00:00Z",
      versionTag: "v1.0.0-initial",
      description: "首次建档默认标准配置",
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
        privacy: INITIAL_PRIVACY,
      },
    },
  ]);

  // Auth: Login
  const login = useCallback(async (email: string, _password?: string): Promise<boolean> => {
    setUser((prev) => ({ ...prev, email }));
    // If not onboarded yet, set to new and open onboarding
    setAccountStatus("active_private");
    setIsAuthModalOpen(false);
    return true;
  }, []);

  // Auth: Register
  const register = useCallback(async (email: string, _code: string, _password?: string): Promise<boolean> => {
    setUser((prev) => ({ ...prev, email }));
    setAccountStatus("new");
    setIsAuthModalOpen(false);
    setIsOnboardingOpen(true);
    return true;
  }, []);

  // Onboarding Wizard completion
  const completeOnboarding = useCallback((profile: Partial<UserProfile>, privacyChoice: "private" | "public") => {
    setUser((prev) => ({ ...prev, ...profile }));
    const isPublic = privacyChoice === "public";
    setPrivacy((prev) => ({ ...prev, isPublicLeaderboard: isPublic }));
    setAccountStatus(isPublic ? "active_public" : "active_private");
    // Approve initial proposed setup plans
    setAgents((prev) =>
      prev.map((agent) => {
        if (agent.id === "codex" || agent.id === "claude-code" || agent.id === "grok-build" || agent.id === "cursor") {
          return { ...agent, setupPlanStatus: "APPLIED", status: "ACTIVE", enabled: true };
        }
        return agent;
      })
    );
    setIsOnboardingOpen(false);
  }, []);

  // Global Pause Toggle
  const toggleGlobalPause = useCallback(() => {
    setGlobalPaused((prev) => {
      const next = !prev;
      setAgents((currAgents) =>
        currAgents.map((ag) => {
          if (next) {
            return ag.enabled ? { ...ag, status: "DEGRADED" } : ag;
          } else {
            return ag.enabled ? { ...ag, status: "ACTIVE" } : ag;
          }
        })
      );
      return next;
    });
  }, []);

  // Single Agent Toggle
  const toggleAgent = useCallback((agentId: string, enabled?: boolean) => {
    setAgents((prev) =>
      prev.map((agent) => {
        if (agent.id === agentId) {
          const nextEnabled = enabled !== undefined ? enabled : !agent.enabled;
          const nextStatus: AdapterRuntimeStatus = nextEnabled ? "ACTIVE" : "DISABLED";
          return {
            ...agent,
            enabled: nextEnabled,
            status: nextStatus,
            setupPlanStatus: nextEnabled ? "APPLIED" : "ROLLED_BACK",
          };
        }
        return agent;
      })
    );
  }, []);

  // Set Agent Runtime Status
  const setAgentRuntimeStatus = useCallback((agentId: string, status: AdapterRuntimeStatus) => {
    setAgents((prev) =>
      prev.map((agent) => {
        if (agent.id === agentId) {
          return {
            ...agent,
            status,
            enabled: status === "ACTIVE" || status === "CONFIGURING" || status === "NEEDS_PERMISSION",
          };
        }
        return agent;
      })
    );
  }, []);

  // Toggle Capability / Metric Switch
  const toggleMetric = useCallback((metric: Capability, enabled?: boolean) => {
    setMetricToggles((prev) => ({
      ...prev,
      [metric]: enabled !== undefined ? enabled : !prev[metric],
    }));
  }, []);

  // Update Privacy Settings
  const updatePrivacyScope = useCallback((updates: Partial<PrivacyScopeSettings>) => {
    setPrivacy((prev) => {
      const next = { ...prev, ...updates };
      setAccountStatus(next.isPublicLeaderboard ? "active_public" : "active_private");
      return next;
    });
  }, []);

  // Trigger Sync Now: Builds batch from Outbox, marks events ACKED, updates Sync Log
  const triggerSyncNow = useCallback(async (): Promise<UploadAck> => {
    if (!isOnline) {
      throw new Error("网络不可用，数据已暂存在离线 WAL Spool 中");
    }
    if (globalPaused) {
      throw new Error("全局采集与上报已暂停，请先恢复采集");
    }

    const pendingOutbox = outbox.filter((item) => item.deliveryStatus !== "ACKED");
    const count = pendingOutbox.length;
    const batchId = `batch_${Date.now()}_${Math.random().toString(36).substring(2, 6)}`;
    const nowIso = new Date().toISOString();

    // Transition outbox to ACKED
    setOutbox((prev) =>
      prev.map((item) => ({
        ...item,
        deliveryStatus: "ACKED",
      }))
    );

    const ack: UploadAck = {
      batchId,
      accepted: count,
      duplicates: 0,
      rejected: [],
      serverTime: nowIso,
    };

    const newLog: SyncLogEntry = {
      id: `sync-${Date.now()}`,
      batchId,
      timestamp: new Date().toLocaleString("zh-CN", { hour12: false }),
      status: "ACKED",
      eventCount: count,
      acceptedCount: count,
      duplicatesCount: 0,
      rejectedCount: 0,
      deviceInstallationId: devices[0]?.installationId || DEFAULT_INSTALLATION_ID,
    };

    setSyncLogs((prev) => [newLog, ...prev]);

    // Update devices last sync
    setDevices((prev) =>
      prev.map((dev) => ({
        ...dev,
        lastSyncAt: "刚刚",
        pendingEvents: 0,
      }))
    );

    return ack;
  }, [isOnline, globalPaused, outbox, devices]);

  // Toggle Network Online / Offline Simulation
  const toggleNetworkSimulation = useCallback(() => {
    setIsOnline((prev) => !prev);
  }, []);

  // Revoke Device
  const revokeDevice = useCallback((deviceId: string) => {
    setDevices((prev) =>
      prev.map((dev) => {
        if (dev.id === deviceId) {
          return { ...dev, status: "REVOKED", lastSyncAt: "已撤销" };
        }
        return dev;
      })
    );
  }, []);

  // Create Config Backup
  const createConfigBackup = useCallback((description?: string): ConfigBackup => {
    const backup: ConfigBackup = {
      id: `backup_${Date.now()}`,
      createdAt: new Date().toISOString(),
      versionTag: `v1.0.${configBackups.length + 1}`,
      description: description || "用户手动创建的配置快照",
      snapshot: {
        agentToggles: agents.reduce((acc, ag) => ({ ...acc, [ag.id]: ag.enabled }), {}),
        metricToggles: { ...metricToggles },
        globalPaused,
        privacy: { ...privacy },
      },
    };
    setConfigBackups((prev) => [backup, ...prev]);
    return backup;
  }, [configBackups.length, agents, metricToggles, globalPaused, privacy]);

  // Restore / Rollback Config Backup
  const restoreConfigBackup = useCallback((backupId: string): boolean => {
    const target = configBackups.find((b) => b.id === backupId);
    if (!target) return false;

    // Restore snapshots
    const snap = target.snapshot;
    setAgents((prev) =>
      prev.map((ag) => {
        const shouldEnable = snap.agentToggles[ag.id] ?? ag.enabled;
        return {
          ...ag,
          enabled: shouldEnable,
          status: shouldEnable ? "ACTIVE" : "DISABLED",
          setupPlanStatus: shouldEnable ? "APPLIED" : "ROLLED_BACK",
        };
      })
    );
    setMetricToggles({ ...snap.metricToggles });
    setGlobalPaused(snap.globalPaused);
    setPrivacy({ ...snap.privacy });
    setAccountStatus(snap.privacy.isPublicLeaderboard ? "active_public" : "active_private");
    return true;
  }, [configBackups]);

  // Request Data Deletion / GDPR Purge
  const requestDataDeletion = useCallback(() => {
    setAccountStatus("deletion_pending");
    setPrivacy((prev) => ({ ...prev, isPublicLeaderboard: false }));
    setMetrics({
      estimatedCost: 0,
      totalTokens: 0,
      inputContextTokens: 0,
      outputTokens: 0,
      cacheTokens: 0,
      cacheHitRate: 0,
      codeLinesAdded: 0,
      codeLinesDeleted: 0,
      tokensPerLine: 0,
      totalHours: 0,
      totalSessions: 0,
      totalTurns: 0,
      userMessages: 0,
      globalRank: 0,
      streakDays: 0,
    });
    setOutbox([]);
  }, []);

  // Generate a schema-valid sample event while respecting metric-level collection choices.
  const generateSampleEnvelope = useCallback((eventType: EventType = "model_usage_recorded"): EventEnvelope => {
    let payload: EventPayload;
    switch (eventType) {
      case "session_started":
        payload = { type: eventType, modelId: "claude-3-7-sonnet-20250219", workspaceHash: WIRE_HMAC };
        break;
      case "session_ended":
        payload = { type: eventType, reason: "completed", durationMs: "3120" };
        break;
      case "turn_started":
        payload = { type: eventType, trigger: "user" };
        break;
      case "turn_completed":
        payload = { type: eventType, success: true, durationMs: "3120" };
        break;
      case "tool_invoked":
        payload = { type: eventType, toolCategory: "terminal_execution", success: true, durationMs: "120" };
        break;
      case "skill_invoked":
        payload = { type: eventType, skillKey: WIRE_HMAC, invokeType: "native", success: true, durationMs: "1820" };
        break;
      case "code_changed":
        payload = { type: eventType, addedLines: "64", removedLines: "12", fileCount: 3, language: "typescript" };
        break;
      case "cost_recorded":
        payload = { type: eventType, amount: "0.0384", currency: "USD", source: "provider_reported" };
        break;
      case "agent_spawned":
        payload = { type: eventType, childSessionHash: WIRE_HMAC, spawnedAgentType: "reviewer" };
        break;
      case "model_usage_recorded":
      default:
        payload = {
          type: "model_usage_recorded",
          providerId: "anthropic",
          modelId: "claude-3-7-sonnet-20250219",
          tokens: { inputTokens: "5400", outputTokens: "1100", cacheReadTokens: "1800", cacheWriteTokens: "400", totalTokens: "8700" },
        };
    }

    return {
      schemaVersion: PROTOCOL_VERSION,
      eventId: wireEventId(`${Date.now().toString(36)}${Math.random().toString(36).slice(2)}`),
      adapterId: "adapter-claude",
      adapterVersion: "1.5.0",
      agentId: "claude-code",
      installationId: devices[0]?.installationId || DEFAULT_INSTALLATION_ID,
      occurredAt: new Date().toISOString(),
      sessionHash: WIRE_HMAC,
      turnHash: eventType === "turn_started" || eventType === "turn_completed" ? WIRE_HMAC : undefined,
      toolCallHash: eventType === "tool_invoked" ? WIRE_HMAC : undefined,
      source: { kind: "otlp", cursorHmac: WIRE_HMAC, rawFingerprintHmac: WIRE_HMAC },
      accuracy: "exact",
      payload,
    };
  }, [devices]);

  // Generate only event categories the user has enabled.
  const generateSampleBatch = useCallback((): UploadBatch => {
    const events: EventEnvelope[] = [];
    if (metricToggles.tokens) events.push(generateSampleEnvelope("model_usage_recorded"));
    if (metricToggles.code) events.push(generateSampleEnvelope("code_changed"));
    if (metricToggles.skills) events.push(generateSampleEnvelope("skill_invoked"));
    if (metricToggles.tools) events.push(generateSampleEnvelope("tool_invoked"));
    if (metricToggles.cost) events.push(generateSampleEnvelope("cost_recorded"));
    return {
      batchId: wireBatchId(Date.now().toString(36)),
      installationId: devices[0]?.installationId || DEFAULT_INSTALLATION_ID,
      createdAt: new Date().toISOString(),
      events,
    };
  }, [devices, generateSampleEnvelope, metricToggles]);

  // Leaderboard with user reflection
  const leaderboard = useMemo((): LeaderboardEntry[] => {
    if (accountStatus === "deletion_pending" || accountStatus === "purged") {
      return INITIAL_LEADERBOARD;
    }

    if (!privacy.isPublicLeaderboard) {
      // User is private
      return INITIAL_LEADERBOARD.map((item) => ({ ...item, isCurrentUser: false }));
    }

    // When public, include current user in the community leaderboard
    const currentUserEntry: LeaderboardEntry = {
      rank: 1, // User is Top 1 with 325.7M
      handle: user.handle,
      nickname: user.nickname,
      avatarText: user.avatarText || "HD",
      totalTokens: metrics.totalTokens,
      codeLines: privacy.showAICodeLines ? metrics.codeLinesAdded : 0,
      topAgent: "Claude Code",
      topAgentShare: 42,
      accuracy: "exact",
      isCurrentUser: true,
      isPrivate: false,
    };

    const othersAdjusted = INITIAL_LEADERBOARD.map((item) => ({
      ...item,
      rank: item.rank + 1,
      isCurrentUser: false,
    }));

    return [currentUserEntry, ...othersAdjusted];
  }, [privacy.isPublicLeaderboard, privacy.showAICodeLines, accountStatus, user, metrics]);

  const value = useMemo<TokenShowContextValue>(
    () => ({
      accountStatus,
      user,
      privacy,
      agents,
      metricToggles,
      globalPaused,
      isOnline,
      devices,
      configBackups,
      outbox,
      syncLogs,
      metrics,
      skills,
      leaderboard,
      isAuthModalOpen,
      isOnboardingOpen,
      isUploadPreviewOpen,
      activeLanguage,
      activeTab,

      setAccountStatus,
      setUser,
      setActiveTab,
      setActiveLanguage,
      setIsAuthModalOpen,
      setIsOnboardingOpen,
      setIsUploadPreviewOpen,

      login,
      register,
      completeOnboarding,
      toggleGlobalPause,
      toggleAgent,
      setAgentRuntimeStatus,
      toggleMetric,
      updatePrivacyScope,
      triggerSyncNow,
      toggleNetworkSimulation,
      revokeDevice,
      createConfigBackup,
      restoreConfigBackup,
      requestDataDeletion,
      generateSampleEnvelope,
      generateSampleBatch,
    }),
    [
      accountStatus,
      user,
      privacy,
      agents,
      metricToggles,
      globalPaused,
      isOnline,
      devices,
      configBackups,
      outbox,
      syncLogs,
      metrics,
      skills,
      leaderboard,
      isAuthModalOpen,
      isOnboardingOpen,
      isUploadPreviewOpen,
      activeLanguage,
      activeTab,
      login,
      register,
      completeOnboarding,
      toggleGlobalPause,
      toggleAgent,
      setAgentRuntimeStatus,
      toggleMetric,
      updatePrivacyScope,
      triggerSyncNow,
      toggleNetworkSimulation,
      revokeDevice,
      createConfigBackup,
      restoreConfigBackup,
      requestDataDeletion,
      generateSampleEnvelope,
      generateSampleBatch,
    ]
  );

  return <TokenShowContext.Provider value={value}>{children}</TokenShowContext.Provider>;
};

export const useTokenShow = (): TokenShowContextValue => {
  const context = useContext(TokenShowContext);
  if (!context) {
    throw new Error("useTokenShow must be used within a TokenShowProvider");
  }
  return context;
};
