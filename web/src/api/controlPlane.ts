import type { Capability, UploadAck, UploadBatch } from "../protocol/generated.ts";
import type {
  AccountStatus,
  AdapterManifest,
  AgentInfo,
  CollectorDevice,
  ConfigBackup,
  DeletionJob,
  LeaderboardEntry,
  OutboxItem,
  PersonalMetrics,
  PrivacyScopeSettings,
  SkillUsage,
  SyncLogEntry,
  UserProfile,
} from "../types.ts";

export type AppTab = "dashboard" | "agents" | "queue" | "privacy" | "devices" | "leaderboard";

export interface ControlPlaneState {
  accountStatus: AccountStatus;
  user: UserProfile;
  privacy: PrivacyScopeSettings;
  agents: AgentInfo[];
  adapterManifests: AdapterManifest[];
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
  recentBatch: UploadBatch | null;
  deletionJob: DeletionJob | null;
}

export interface SessionResult {
  accountStatus: AccountStatus;
}

export interface CommandAck {
  commandId: string;
  status: "ACK";
}

export type DangerousCommand =
  | { type: "REVOKE_DEVICE"; deviceId: string }
  | { type: "RESTORE_CONFIG"; backupId: string }
  | { type: "REQUEST_DATA_DELETION" };

export interface ControlPlaneClient {
  getSession(): Promise<SessionResult | null>;
  getState(): Promise<ControlPlaneState>;
  login(email: string, password?: string): Promise<SessionResult>;
  register(email: string, code: string, password?: string): Promise<SessionResult>;
  completeOnboarding(profile: Partial<UserProfile>, privacyChoice: "private" | "public"): Promise<CommandAck>;
  approveAdapterManifest(adapterId: string, permissionIds: string[]): Promise<CommandAck>;
  setGlobalPaused(paused: boolean): Promise<CommandAck>;
  setAgentEnabled(agentId: string, enabled: boolean): Promise<CommandAck>;
  setMetricEnabled(metric: Capability, enabled: boolean): Promise<CommandAck>;
  updatePrivacyScope(updates: Partial<PrivacyScopeSettings>): Promise<CommandAck>;
  syncNow(batch: UploadBatch): Promise<UploadAck>;
  getRecentBatch(): Promise<UploadBatch | null>;
  runDangerousCommand(command: DangerousCommand): Promise<CommandAck>;
  createConfigBackup(description?: string): Promise<CommandAck>;
}

export class ControlPlaneError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly body?: unknown,
  ) {
    super(message);
    this.name = "ControlPlaneError";
  }
}

export class FetchControlPlaneClient implements ControlPlaneClient {
  constructor(
    private readonly baseUrl = "",
    private readonly fetcher: typeof fetch = globalThis.fetch.bind(globalThis),
  ) {}

  async getSession(): Promise<SessionResult | null> {
    const response = await this.fetcher(`${this.baseUrl}/api/control-plane/session`, {
      credentials: "include",
      headers: { Accept: "application/json" },
    });
    if (response.status === 401 || response.status === 404) return null;
    return this.readJson<SessionResult>(response);
  }

  getState(): Promise<ControlPlaneState> {
    return this.request<ControlPlaneState>("/api/control-plane/state");
  }

  login(email: string, password?: string): Promise<SessionResult> {
    return this.request<SessionResult>("/api/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
  }

  register(email: string, code: string, password?: string): Promise<SessionResult> {
    return this.request<SessionResult>("/api/auth/register", {
      method: "POST",
      body: JSON.stringify({ email, code, password }),
    });
  }

  completeOnboarding(profile: Partial<UserProfile>, privacyChoice: "private" | "public"): Promise<CommandAck> {
    return this.request<CommandAck>("/api/auth/onboarding", {
      method: "POST",
      body: JSON.stringify({ profile, privacyChoice }),
    });
  }

  approveAdapterManifest(adapterId: string, permissionIds: string[]): Promise<CommandAck> {
    return this.request<CommandAck>(`/api/control-plane/adapters/${encodeURIComponent(adapterId)}/approval`, {
      method: "POST",
      body: JSON.stringify({ permissionIds }),
    });
  }

  setGlobalPaused(paused: boolean): Promise<CommandAck> {
    return this.command({ type: "SET_GLOBAL_PAUSED", paused });
  }

  setAgentEnabled(agentId: string, enabled: boolean): Promise<CommandAck> {
    return this.command({ type: "SET_AGENT_ENABLED", agentId, enabled });
  }

  setMetricEnabled(metric: Capability, enabled: boolean): Promise<CommandAck> {
    return this.command({ type: "SET_METRIC_ENABLED", metric, enabled });
  }

  updatePrivacyScope(updates: Partial<PrivacyScopeSettings>): Promise<CommandAck> {
    return this.command({ type: "UPDATE_PRIVACY_SCOPE", updates });
  }

  syncNow(batch: UploadBatch): Promise<UploadAck> {
    return this.request<UploadAck>("/api/control-plane/upload-batches", {
      method: "POST",
      body: JSON.stringify(batch),
    });
  }

  getRecentBatch(): Promise<UploadBatch | null> {
    return this.request<UploadBatch | null>("/api/control-plane/upload-batches/recent");
  }

  runDangerousCommand(command: DangerousCommand): Promise<CommandAck> {
    return this.command(command);
  }

  createConfigBackup(description?: string): Promise<CommandAck> {
    return this.command({ type: "CREATE_CONFIG_BACKUP", description });
  }

  private command(command: Record<string, unknown>): Promise<CommandAck> {
    return this.request<CommandAck>("/api/control-plane/commands", {
      method: "POST",
      body: JSON.stringify(command),
    });
  }

  private async request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await this.fetcher(`${this.baseUrl}${path}`, {
      ...init,
      credentials: "include",
      headers: {
        Accept: "application/json",
        ...(init.body ? { "Content-Type": "application/json" } : {}),
        ...init.headers,
      },
    });
    return this.readJson<T>(response);
  }

  private async readJson<T>(response: Response): Promise<T> {
    const body = response.status === 204 ? undefined : await response.json().catch(() => undefined);
    if (!response.ok) {
      const message =
        body && typeof body === "object" && "message" in body && typeof body.message === "string"
          ? body.message
          : `Control plane request failed (${response.status})`;
      throw new ControlPlaneError(message, response.status, body);
    }
    return body as T;
  }
}
