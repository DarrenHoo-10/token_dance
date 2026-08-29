import type { Capability, UploadAck, UploadBatch } from "../protocol/generated.ts";
import type { PrivacyScopeSettings, UserProfile } from "../types.ts";
import type {
  CommandAck,
  ControlPlaneClient,
  ControlPlaneState,
  DangerousCommand,
  SessionResult,
} from "../api/controlPlane.ts";

const HMAC = `hmac-sha256:${"A".repeat(43)}`;
const installationId = `ins_${"0".repeat(26)}`;
const eventId = `evt_${"B".repeat(39)}`;

const recentBatch: UploadBatch = {
  batchId: `bat_${"C".repeat(26)}`,
  installationId,
  createdAt: "2026-08-30T10:14:10Z",
  events: [{
    schemaVersion: "1.0",
    eventId,
    adapterId: "adapter-claude",
    adapterVersion: "1.5.0",
    agentId: "claude-code",
    installationId,
    occurredAt: "2026-08-30T10:14:08Z",
    sessionHash: HMAC,
    source: { kind: "otlp", cursorHmac: HMAC, rawFingerprintHmac: HMAC },
    accuracy: "exact",
    payload: {
      type: "model_usage_recorded",
      providerId: "anthropic",
      modelId: "claude-sonnet",
      tokens: { inputTokens: "4200", outputTokens: "850", totalTokens: "5050" },
    },
  }],
};

const agentDefinitions = [
  ["codex", "Codex", "exact"],
  ["claude-code", "Claude Code", "exact"],
  ["grok-build", "Grok Build", "derived"],
  ["cursor", "Cursor", "correlated"],
  ["zcode", "ZCode", "estimated"],
  ["deepseek-harness", "DeepSeek Harness", "derived"],
] as const;

export const createMockState = (accountStatus: ControlPlaneState["accountStatus"] = "active_private"): ControlPlaneState => ({
  accountStatus,
  user: {
    id: "usr_1",
    email: "developer@tokendance.io",
    nickname: "Hoo Darren",
    handle: "darrenhoo",
    bio: "Building with AI",
    avatarText: "HD",
    timezone: "Asia/Shanghai",
    language: "zh",
    createdAt: "2026-08-01T08:00:00Z",
  },
  privacy: {
    isPublicLeaderboard: false,
    showTokenTotals: true,
    showTokenTrends: true,
    showAgentBreakdown: true,
    showActivityHeatmap: false,
    showTopSkills: true,
    showAICodeLines: true,
  },
  agents: agentDefinitions.map(([id, name, accuracy]) => ({
    id,
    name,
    adapterId: `adapter-${id}`,
    adapterVersion: "1.0.0",
    status: id === "deepseek-harness" ? "NEEDS_PERMISSION" : "ACTIVE",
    setupPlanStatus: id === "deepseek-harness" ? "PROPOSED" : "APPLIED",
    checkpointStatus: id === "deepseek-harness" ? "DISCOVERED" : "CURRENT",
    capabilities: ["tokens", "sessions", "turns", "code"] as Capability[],
    sources: ["otlp"],
    accuracy,
    enabled: id !== "deepseek-harness",
    todayTokens: 1000,
    totalTokens: 1000000,
    lastActive: "刚刚",
    version: "1.0.0",
  })),
  adapterManifests: agentDefinitions.map(([id, name]) => ({
    adapterId: `adapter-${id}`,
    adapterName: name,
    version: "1.0.0",
    approved: accountStatus !== "new",
    permissions: [{ id: "read-telemetry", label: "读取聚合遥测", description: "仅读取 manifest 声明的数据源", required: true }],
  })),
  metricToggles: { tokens: true, sessions: true, turns: true, tools: true, skills: true, code: true, cost: true, subagents: true },
  globalPaused: false,
  isOnline: true,
  devices: [
    { id: "dev-win-01", installationId, name: "Windows Studio", platform: "windows", osVersion: "Windows 11", collectorVersion: "1.2.0", keyFingerprint: "ed25519:key", status: "ACTIVE", lastSyncAt: "刚刚", pendingEvents: 1 },
    { id: "dev-mac-02", installationId: `ins_${"1".repeat(26)}`, name: "MacBook Pro", platform: "macos", osVersion: "macOS", collectorVersion: "1.2.0", keyFingerprint: "ed25519:key2", status: "ACTIVE", lastSyncAt: "8 分钟前", pendingEvents: 0 },
  ],
  configBackups: [{
    id: "backup-1",
    createdAt: "2026-08-30T09:00:00Z",
    versionTag: "v1.0.0",
    description: "Baseline",
    snapshot: {
      agentToggles: Object.fromEntries(agentDefinitions.map(([id]) => [id, id !== "deepseek-harness"])),
      metricToggles: { tokens: true, sessions: true, turns: true, tools: true, skills: true, code: true, cost: true, subagents: true },
      globalPaused: false,
      privacy: { isPublicLeaderboard: false, showTokenTotals: true, showTokenTrends: true, showAgentBreakdown: true, showActivityHeatmap: false, showTopSkills: true, showAICodeLines: true },
    },
  }],
  outbox: [{ id: "out-1", envelope: recentBatch.events[0], deliveryStatus: "QUEUED", retryCount: 0, queuedAt: "2026-08-30T10:14:10Z" }],
  syncLogs: [],
  metrics: { estimatedCost: 10, totalTokens: 5050, inputContextTokens: 4200, outputTokens: 850, cacheTokens: 0, cacheHitRate: 0, codeLinesAdded: 64, codeLinesDeleted: 12, tokensPerLine: 78.9, totalHours: 2, totalSessions: 3, totalTurns: 5, userMessages: 5, globalRank: 37, streakDays: 2 },
  skills: [{ id: "skill-1", name: "review", invokeCount: 3, daysUsed: 2, trend: "+1", accuracy: "exact" }],
  leaderboard: [{ rank: 1, handle: "max", nickname: "Max", avatarText: "MX", totalTokens: 9000, codeLines: 80, topAgent: "Claude Code", topAgentShare: 60, accuracy: "exact" }],
  recentBatch,
  deletionJob: null,
});

export class MockControlPlaneClient implements ControlPlaneClient {
  state: ControlPlaneState;
  session: SessionResult | null;
  calls: Array<{ method: string; payload?: unknown }> = [];
  failures = new Map<string, Error>();

  constructor(state = createMockState(), session: SessionResult | null = { accountStatus: state.accountStatus }) {
    this.state = structuredClone(state);
    this.session = session;
  }

  failNext(method: string, error: Error) { this.failures.set(method, error); }

  private record(method: string, payload?: unknown) {
    this.calls.push({ method, payload });
    const failure = this.failures.get(method);
    if (failure) {
      this.failures.delete(method);
      throw failure;
    }
  }

  private ack(): CommandAck { return { commandId: `cmd-${this.calls.length}`, status: "ACK" }; }

  async getSession() { this.record("getSession"); return this.session ? structuredClone(this.session) : null; }
  async getState() { this.record("getState"); return structuredClone(this.state); }
  async login(email: string, password?: string) { this.record("login", { email, password }); this.session = { accountStatus: this.state.accountStatus }; return this.session; }
  async register(email: string, code: string, password?: string) { this.record("register", { email, code, password }); this.state.accountStatus = "new"; this.state.user.email = email; this.state.adapterManifests.forEach((manifest) => { manifest.approved = false; }); this.session = { accountStatus: "new" }; return this.session; }
  async completeOnboarding(profile: Partial<UserProfile>, privacyChoice: "private" | "public") { this.record("completeOnboarding", { profile, privacyChoice }); this.state.user = { ...this.state.user, ...profile }; this.state.privacy.isPublicLeaderboard = privacyChoice === "public"; this.state.accountStatus = privacyChoice === "public" ? "active_public" : "active_private"; return this.ack(); }
  async approveAdapterManifest(adapterId: string, permissionIds: string[]) { this.record("approveAdapterManifest", { adapterId, permissionIds }); const manifest = this.state.adapterManifests.find((item) => item.adapterId === adapterId); if (manifest) manifest.approved = true; return this.ack(); }
  async setGlobalPaused(paused: boolean) { this.record("setGlobalPaused", { paused }); this.state.globalPaused = paused; return this.ack(); }
  async setAgentEnabled(agentId: string, enabled: boolean) { this.record("setAgentEnabled", { agentId, enabled }); const agent = this.state.agents.find((item) => item.id === agentId); if (agent) agent.enabled = enabled; return this.ack(); }
  async setMetricEnabled(metric: Capability, enabled: boolean) { this.record("setMetricEnabled", { metric, enabled }); this.state.metricToggles[metric] = enabled; return this.ack(); }
  async updatePrivacyScope(updates: Partial<PrivacyScopeSettings>) { this.record("updatePrivacyScope", updates); this.state.privacy = { ...this.state.privacy, ...updates }; return this.ack(); }
  async syncNow(batch: UploadBatch): Promise<UploadAck> { this.record("syncNow", batch); this.state.outbox = this.state.outbox.map((item) => ({ ...item, deliveryStatus: "ACKED" })); return { batchId: batch.batchId, accepted: batch.events.length, duplicates: 0, rejected: [], serverTime: "2026-08-30T10:15:00Z" }; }
  async getRecentBatch() { this.record("getRecentBatch"); return structuredClone(this.state.recentBatch); }
  async runDangerousCommand(command: DangerousCommand) { this.record("runDangerousCommand", command); if (command.type === "REVOKE_DEVICE") { const device = this.state.devices.find((item) => item.id === command.deviceId); if (device) device.status = "REVOKED"; } if (command.type === "REQUEST_DATA_DELETION") { this.state.accountStatus = "deletion_pending"; this.state.deletionJob = { id: "delete-1", status: "REQUESTED", requestedAt: "2026-08-30T10:16:00Z" }; } return this.ack(); }
  async createConfigBackup(description?: string) { this.record("createConfigBackup", { description }); this.state.configBackups.unshift({ ...this.state.configBackups[0], id: "backup-2", versionTag: "v1.0.1", description: description || "Snapshot" }); return this.ack(); }
}
