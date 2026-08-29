// TokenDance API Types
// Aligned with docs/tokendance-user-system-technical-design.md

export type Locale = 'zh-CN' | 'en-US';

export type AccountStatus = 'active' | 'suspended' | 'deletion_pending' | 'deleted';
export type ProductState = 'email_unverified' | 'new' | 'active_private' | 'active_public' | 'suspended' | 'deletion_pending';
export type LeaderboardVisibility = 'private' | 'team' | 'public';
export type InstallationStatus = 'active' | 'disabled' | 'revoked';
export type ExportJobStatus = 'pending' | 'running' | 'completed' | 'failed' | 'expired' | 'cancelled';
export type DeletionRequestStatus = 'pending' | 'running' | 'completed' | 'failed' | 'cancelled';
export type DeletionScope = 'installation' | 'time_range' | 'all_usage' | 'account';

// API Error representation
export interface ApiErrorDetail {
  code: string;
  messageKey: string;
  requestId?: string;
  details?: Record<string, unknown>;
}

export interface ApiErrorResponse {
  error: ApiErrorDetail;
}

// Auth Types
export interface SessionUser {
  userId?: string;
  handle: string | null;
  displayName: string;
  avatarUrl: string | null;
  locale: Locale;
  onboardingRequired: boolean;
  productState: ProductState;
}

export interface SessionResponse {
  authenticated: boolean;
  user: SessionUser | null;
  csrfToken?: string;
  idleExpiresAt?: string;
  absoluteExpiresAt?: string;
}

export interface AuthResponse {
  user: SessionUser;
  csrfToken?: string;
  returnTo?: string;
}

export interface OnboardingResponse {
  user: UserProfile;
  privacy: PrivacySettings;
  returnTo?: string;
  // Fallback for profile alias
  profile?: UserProfile;
}

export interface LoginRequest {
  email: string;
  password?: string;
  returnTo?: string;
}

export interface RegisterCodeRequest {
  email: string;
  locale?: Locale;
}

export interface RegisterRequest {
  email: string;
  code: string;
  password?: string;
  returnTo?: string;
}

export interface PasswordCodeRequest {
  email: string;
}

export interface PasswordResetRequest {
  email: string;
  code: string;
  newPassword?: string;
}

export interface ActiveSession {
  sessionId: string;
  deviceLabel: string | null;
  isCurrent: boolean;
  idleExpiresAt: string;
  absoluteExpiresAt: string;
  createdAt: string;
}

// Profile & Privacy Types
export interface UserProfile {
  userId?: string;
  displayName: string;
  handle: string | null;
  avatarUrl: string | null;
  bio: string | null;
  timezone: string;
  locale: Locale;
  onboardingCompletedAt: string | null;
  profileVersion: number;
}

export interface UpdateProfileRequest {
  displayName?: string;
  handle?: string;
  bio?: string | null;
  timezone?: string;
  locale?: Locale;
}

export interface PrivacySettings {
  publicProfileEnabled: boolean;
  leaderboardVisibility: LeaderboardVisibility;
  showBio: boolean;
  showTokenTotal: boolean;
  showTrends: boolean;
  showActivityCalendar: boolean;
  showAgentBreakdown: boolean;
  showSkillRanking: boolean;
  showAchievements: boolean;
  privacyVersion: number;
}

export interface UpdatePrivacyRequest {
  publicProfileEnabled?: boolean;
  leaderboardVisibility?: LeaderboardVisibility;
  showBio?: boolean;
  showTokenTotal?: boolean;
  showTrends?: boolean;
  showActivityCalendar?: boolean;
  showAgentBreakdown?: boolean;
  showSkillRanking?: boolean;
  showAchievements?: boolean;
}

export interface OnboardingRequest {
  displayName: string;
  handle: string;
  timezone: string;
  locale: Locale;
  privacy: {
    publicProfileEnabled: boolean;
    leaderboardVisibility: LeaderboardVisibility;
  };
  returnTo?: string;
}

export interface AvatarUploadIntentResponse {
  objectId: string;
  uploadUrl: string;
  expiresAt: string;
}

// Personal Analytics & Metrics
export interface MetricValue {
  value: string | null;
  supported: boolean;
  change?: string;
}

export interface CostMetricValue {
  amount: string | null;
  currency: string;
  supported: boolean;
  status?: string;
}

export interface PersonalSummaryMetrics {
  estimatedCost: CostMetricValue;
  totalTokens: MetricValue;
  generatedCodeLines: MetricValue;
  tokensPerCodeLine: MetricValue;
  inputContextTokens: MetricValue;
  outputTokens: MetricValue;
  cacheHitRate: MetricValue;
  activeDurationMs: MetricValue;
  messageCount: MetricValue;
  userMessageCount: MetricValue;
}

export interface PersonalSummary {
  range: {
    key: string;
    from: string;
    to: string;
    timezone: string;
  };
  metrics: PersonalSummaryMetrics;
  ranking: {
    visibility: LeaderboardVisibility;
    rank: number | null;
    delta: number | null;
    percentile: string | null;
  };
  sync: {
    lastCommittedAt: string | null;
    pendingLocalCount: number | null;
    status?: 'healthy' | 'warning' | 'delayed';
  };
  dataWatermarkAt: string;
  aggregationVersion: number;
}

export interface TokenTrendItem {
  date: string;
  tokenTotal?: string;
  inputTokens?: string;
  outputTokens?: string;
  cacheReadTokens?: string;
  cacheWriteTokens?: string;
  reasoningTokens?: string;
}

export interface TokenTrendsResponse {
  from?: string;
  to?: string;
  timezone?: string;
  granularity?: string;
  agent?: string;
  model?: string;
  mode?: string;
  trends?: TokenTrendItem[];
  points?: TokenTrendItem[];
  dataWatermarkAt?: string;
  aggregationVersion?: number;
}

export interface AgentBreakdownItem {
  agentId: string;
  displayName: string;
  tokenTotal: string;
  percentage: number;
  sessionCount?: number;
  activeDays?: number;
}

export interface ModelBreakdownItem {
  providerId: string;
  modelId: string;
  displayName: string;
  tokenTotal: string;
  percentage: number;
  inputPercentage?: number;
  outputPercentage?: number;
}

export interface SkillMetricItem {
  rankNo: number;
  skillKey?: string;
  skillPublicName: string;
  useCount: number;
  successCount?: number;
  activeDays: number;
  durationMs?: number;
  deltaPercent?: number;
}

export interface ActivityCalendarDay {
  date: string;
  tokenTotal: string;
  level: 0 | 1 | 2 | 3 | 4;
  activeDurationMs?: number;
}

export interface ActivityRow {
  occurredAt: string;
  agentId: string;
  modelId: string;
  tokenTotal: string;
  inputTokens: string;
  outputTokens: string;
  sessionCount: number;
  turnCount: number;
  deviceName: string;
  syncStatus: 'normal' | 'delayed';
}

export interface FilterOptionsResponse {
  agents: Array<string | { id: string; name: string }>;
  providers: Array<string | { id: string; name: string }>;
  models: Array<string | { id: string; name: string; providerId?: string }>;
}

// Devices
export interface CollectorDevice {
  installationId: string;
  deviceName: string;
  osType: string;
  osVersion: string;
  architecture: string;
  collectorVersion: string;
  installationStatus: InstallationStatus;
  registeredAt: string;
  lastSeenAt: string | null;
  disabledAt: string | null;
  disabledReason: string | null;
  statusVersion: number;
  adapterHealth: 'healthy' | 'warning' | 'error';
  recentBatchesCount?: number;
}

export interface DeviceBindingChallengeResponse {
  challengeId: string;
  code: string;
  expiresAt: string;
}

export interface ClaimInstallationRequest {
  code: string;
  devicePublicKey: string;
  deviceName: string;
  osType: string;
  osVersion: string;
  architecture: string;
  collectorVersion: string;
}

export interface ClaimInstallationResponse {
  installationId: string;
  uploadPolicy: {
    maxBatchBytes: number;
    ingestEndpoint: string;
  };
}

// Exports & Deletions
export interface ExportJob {
  exportId: string;
  jobStatus: ExportJobStatus;
  scope: string;
  format: string;
  createdAt: string;
  completedAt: string | null;
  expiresAt: string | null;
  objectKey?: string;
  fileSizeBytes?: number;
  checksum?: string;
}

export interface CreateExportRequest {
  scope?: string;
  format?: 'csv' | 'json';
  filter?: Record<string, unknown>;
  rangeKey?: string;
  from?: string;
  to?: string;
}

export interface ExportDownloadResponse {
  downloadUrl: string;
  expiresAt: string;
}

export interface DeletionRequest {
  requestId: string;
  deletionScope?: DeletionScope;
  scope?: DeletionScope;
  targetId?: string | null;
  requestStatus: DeletionRequestStatus;
  createdAt: string;
  cancelBefore: string | null;
  completedAt: string | null;
  details?: Record<string, unknown>;
}

export interface CreateDeletionRequest {
  scope?: DeletionScope;
  deletionScope?: DeletionScope;
  targetId?: string;
  confirmation: boolean | string;
}

// Public & Community
export interface PublicUserProfile {
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  bio: string | null;
  rank: number | null;
  rankDelta: number | null;
  percentile: string | null;
  tokenTotal: string | null;
  codeLinesTotal?: string | null;
  estimatedCostTotal?: string | null;
  activeDays: number | null;
  currentStreak: number | null;
  tokenTrend?: TokenTrendItem[];
  activityCalendar?: ActivityCalendarDay[];
  agentBreakdown?: AgentBreakdownItem[];
  skillRanking?: SkillMetricItem[];
  dataWatermarkAt: string;
  generatedAt: string;
}

export interface SearchUserResult {
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  bio: string | null;
  rank: number | null;
  tokenTotal: string;
  topAgent?: string;
}

export interface SearchAgentResult {
  agentId: string;
  displayName: string;
  developerCount: string;
  tokenTotal30d: string;
  tags: string[];
}

export interface SearchSkillResult {
  skillPublicName: string;
  useCount: number;
  userCount: number;
  growthDelta: number;
  tags: string[];
}

export interface SearchResponse {
  query: string;
  users: SearchUserResult[];
  agents: SearchAgentResult[];
  skills: SearchSkillResult[];
  totalCount: number;
}

export interface LeaderboardEntry {
  rankNo: number;
  rankDelta?: number;
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  metricValue: string;
  formattedMetric: string;
  topAgent?: string;
  activeDays?: number;
}

export interface LeaderboardResponse {
  boardKey: string;
  window: string;
  metric: string;
  agent: string;
  snapshotId: string;
  generatedAt: string;
  entries: LeaderboardEntry[];
  nextCursor: string | null;
  totalEntries: number;
}

export interface UserComparisonItem {
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  visible: boolean;
  rank: number | null;
  tokenTotal: string | null;
  codeLinesTotal: string | null;
  activeDays: number | null;
  currentStreak: number | null;
  topAgent: string | null;
  agentBreakdown?: AgentBreakdownItem[];
  skillRanking?: SkillMetricItem[];
  dataWatermarkAt?: string;
}

export interface UserComparisonResponse {
  range: string;
  metric: string;
  users: UserComparisonItem[];
  generatedAt: string;
}
