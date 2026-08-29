import type {
  AdapterRuntimeStatus,
  SetupPlanStatus,
  SourceCheckpointStatus,
  EventDeliveryStatus,
  ClientUploadBatchStatus,
  Accuracy,
  Capability,
  SourceKind,
  EventEnvelope,
} from "./protocol/generated.ts";

export type AccountStatus =
  | "unauthenticated"
  | "email_unverified"
  | "new"
  | "active_private"
  | "active_public"
  | "suspended"
  | "deletion_pending"
  | "purged";

export interface UserProfile {
  id: string;
  email: string;
  nickname: string;
  handle: string;
  bio: string;
  avatarText: string;
  timezone: string;
  language: "zh" | "en";
  createdAt: string;
}

export interface PrivacyScopeSettings {
  isPublicLeaderboard: boolean; // default: false (Private)
  showTokenTotals: boolean;
  showTokenTrends: boolean;
  showAgentBreakdown: boolean;
  showActivityHeatmap: boolean;
  showTopSkills: boolean;
  showAICodeLines: boolean;
}

export interface AdapterManifestPermission {
  id: string;
  label: string;
  description: string;
  required: boolean;
}

export interface AdapterManifest {
  adapterId: string;
  adapterName: string;
  version: string;
  permissions: AdapterManifestPermission[];
  approved: boolean;
}

export type DeletionJobStatus = "REQUESTED" | "RUNNING" | "FAILED" | "PURGED";

export interface DeletionJob {
  id: string;
  status: DeletionJobStatus;
  requestedAt: string;
  failureReason?: string;
}

export interface AgentInfo {
  id: string;
  name: string;
  adapterId: string;
  adapterVersion: string;
  status: AdapterRuntimeStatus;
  setupPlanStatus: SetupPlanStatus;
  checkpointStatus: SourceCheckpointStatus;
  capabilities: Capability[];
  sources: SourceKind[];
  accuracy: Accuracy;
  enabled: boolean;
  todayTokens: number;
  totalTokens: number;
  lastActive: string;
  version: string;
}

export interface CollectorDevice {
  id: string;
  installationId: string;
  name: string;
  platform: "windows" | "macos" | "linux";
  osVersion: string;
  collectorVersion: string;
  keyFingerprint: string;
  status: "ACTIVE" | "PAUSED" | "REVOKED";
  lastSyncAt: string;
  pendingEvents: number;
}

export interface ConfigBackup {
  id: string;
  createdAt: string;
  versionTag: string;
  description: string;
  snapshot: {
    agentToggles: Record<string, boolean>;
    metricToggles: Record<Capability, boolean>;
    globalPaused: boolean;
    privacy: PrivacyScopeSettings;
  };
}

export interface OutboxItem {
  id: string;
  envelope: EventEnvelope;
  deliveryStatus: EventDeliveryStatus;
  retryCount: number;
  queuedAt: string;
  errorReason?: string;
}

export interface SyncLogEntry {
  id: string;
  batchId: string;
  timestamp: string;
  status: ClientUploadBatchStatus;
  eventCount: number;
  acceptedCount: number;
  duplicatesCount: number;
  rejectedCount: number;
  deviceInstallationId: string;
}

export interface PersonalMetrics {
  estimatedCost: number;
  totalTokens: number;
  inputContextTokens: number;
  outputTokens: number;
  cacheTokens: number;
  cacheHitRate: number;
  codeLinesAdded: number;
  codeLinesDeleted: number;
  tokensPerLine: number;
  totalHours: number;
  totalSessions: number;
  totalTurns: number;
  userMessages: number;
  globalRank: number;
  streakDays: number;
}

export interface SkillUsage {
  id: string;
  name: string;
  invokeCount: number;
  daysUsed: number;
  trend: string;
  accuracy: Accuracy;
}

export interface LeaderboardEntry {
  rank: number;
  handle: string;
  nickname: string;
  avatarText: string;
  totalTokens: number;
  codeLines: number;
  topAgent: string;
  topAgentShare: number;
  accuracy: Accuracy;
  isCurrentUser?: boolean;
  isPrivate?: boolean;
}
