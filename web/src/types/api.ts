// TokenDance API Types
// Canonical types aligned with server/api/openapi/tokendance-user-v1.yaml

export type Locale = 'zh-CN' | 'en-US';

export type AccountStatus = 'active' | 'suspended' | 'deletion_pending' | 'deleted';
export type ProductState = 'email_unverified' | 'new' | 'active_private' | 'active_public' | 'suspended' | 'deletion_pending' | 'deleted';
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
  profile?: UserProfile;
}

export interface LoginRequest {
  email: string;
  password?: string;
  returnTo?: string;
  deviceLabel?: string;
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
  sessionStatus?: string;
  isCurrent?: boolean;
  lastSeenAt?: string;
  idleExpiresAt?: string;
  absoluteExpiresAt: string;
  createdAt: string;
}

// Profile & Privacy Types
export interface UserProfile {
  userId?: string;
  displayName: string;
  handle: string | null;
  avatarUrl: string | null;
  bio?: string | null;
  timezone?: string;
  timezoneName?: string;
  locale: Locale;
  accountStatus?: AccountStatus;
  leaderboardVisibility?: LeaderboardVisibility;
  onboardingCompletedAt?: string | null;
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
  userId?: string;
  publicProfileEnabled: boolean;
  leaderboardVisibility?: LeaderboardVisibility;
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
  timezone?: string;
  locale?: Locale;
  privacy: {
    publicProfileEnabled: boolean;
    leaderboardVisibility?: LeaderboardVisibility;
  };
  returnTo?: string;
}

export interface AvatarUploadIntentResponse {
  objectId: string;
  uploadUrl: string;
  expiresAt: string;
}

// Personal Analytics & Metrics
export interface TimeRange {
  key: string;
  from: string;
  to: string;
  timezone: string;
}

export interface MetricValue {
  value: string | null;
  supported: boolean;
  change?: string;
}

export interface CostMetricValue {
  amount: string | null;
  currency: string | null;
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

export interface PersonalSummaryRanking {
  visibility?: LeaderboardVisibility | string;
  rank: number | null;
  delta?: number | null;
  percentile: number | string | null;
}

export interface PersonalSummarySync {
  lastCommittedAt: string | null;
  pendingLocalCount: number | null;
  status?: 'healthy' | 'warning' | 'delayed' | 'unknown';
}

export interface PersonalSummary {
  range: TimeRange;
  metrics: PersonalSummaryMetrics;
  ranking: PersonalSummaryRanking;
  sync: PersonalSummarySync;
  dataWatermarkAt?: string | null;
  aggregationVersion: number;
}

export interface TokenTrendPoint {
  date: string;
  tokenTotal?: string | null;
  inputTokens?: string | null;
  outputTokens?: string | null;
  cacheReadTokens?: string | null;
  cacheWriteTokens?: string | null;
  reasoningTokens?: string | null;
}

export type TokenTrendItem = TokenTrendPoint;

export interface TokenTrendsResponse {
  range?: TimeRange;
  from?: string;
  to?: string;
  timezone?: string;
  granularity?: string;
  agent?: string;
  agentId?: string | null;
  providerId?: string | null;
  model?: string;
  modelId?: string | null;
  mode?: string;
  trends?: TokenTrendPoint[];
  points?: TokenTrendPoint[];
  dataWatermarkAt?: string | null;
  aggregationVersion?: number;
  visible?: boolean;
}

export interface BreakdownItem {
  key: string;
  label: string;
  tokenTotal: string;
  percentage: number;
}

export type AgentBreakdownItem = BreakdownItem & {
  agentId?: string;
  displayName?: string;
};

export type ModelBreakdownItem = BreakdownItem & {
  providerId?: string;
  modelId?: string;
  displayName?: string;
};

export interface BreakdownResponse {
  range?: TimeRange;
  items: BreakdownItem[];
  dataWatermarkAt?: string | null;
  aggregationVersion?: number;
}

export interface SkillItem {
  skillId: string;
  skillPublicName: string;
  useCount: string;
  activeDays: number;
  successRate?: number | null;
  previousDeltaPct?: number | null;
  // UI convenience
  rankNo?: number;
  durationMs?: number;
}

export type SkillMetricItem = SkillItem;

export interface SkillsResponse {
  range?: TimeRange;
  skills: SkillItem[];
  dataWatermarkAt?: string | null;
  aggregationVersion?: number;
  visible?: boolean;
  // Fallback alias
  items?: SkillItem[];
}

export interface CalendarDay {
  date: string;
  active?: boolean;
  level: number;
  tokenTotal: string;
}

export type ActivityCalendarDay = CalendarDay;

export interface CalendarResponse {
  days: CalendarDay[];
  currentStreak: number;
  longestStreak: number;
  totalActiveDays: number;
  dataWatermarkAt?: string | null;
  aggregationVersion?: number;
}

export interface ActivityRow {
  occurredAt?: string;
  agentId?: string;
  modelId?: string;
  tokenTotal?: string;
  inputTokens?: string;
  outputTokens?: string;
  sessionCount?: number;
  turnCount?: number;
  deviceName?: string;
  syncStatus?: 'normal' | 'delayed' | string;
}

export interface ActivityResponse {
  items: ActivityRow[];
  rows?: ActivityRow[];
  nextCursor?: string | null;
}

export interface FilterOptionsResponse {
  agents: Array<string | { id: string; name: string }>;
  providers: Array<string | { id: string; name: string }>;
  models: Array<string | { id: string; name: string; providerId?: string }>;
}

// Devices
export interface CollectorDevice {
  installationId: string;
  deviceName: string | null;
  osType: string;
  osVersion: string | null;
  architecture: string;
  collectorVersion: string;
  installationStatus: InstallationStatus;
  registeredAt: string;
  lastSeenAt: string | null;
  disabledAt?: string | null;
  disabledReason?: string | null;
  statusVersion?: number;
  adapterHealth?: 'healthy' | 'warning' | 'error';
}

export interface DeviceListResponse {
  devices: CollectorDevice[];
}

export interface DeviceBindingChallengeResponse {
  challengeId: string;
  code: string;
  expiresAt: string;
}

export interface ClaimInstallationRequest {
  code: string;
  publicKey: string;
  deviceName?: string | null;
  osType: 'windows' | 'macos' | 'linux' | string;
  osVersion?: string | null;
  architecture: string;
  collectorVersion: string;
  // Deprecated alias
  devicePublicKey?: string;
}

export interface ClaimInstallationResponse {
  installationId: string;
  status?: string;
  uploadPolicy?: {
    maxBatchEvents?: number;
    minIntervalSec?: number;
    maxBatchBytes?: number;
    ingestEndpoint?: string;
  };
}

// Exports & Deletions
export interface ExportJob {
  exportId: string;
  userId?: string;
  idempotencyKey?: string;
  exportScope: string;
  exportFormat: string;
  filter?: Record<string, unknown>;
  jobStatus: ExportJobStatus;
  attemptCount?: number;
  fileSize?: number | null;
  downloadUrl?: string | null;
  expiresAt?: string | null;
  startedAt?: string | null;
  completedAt?: string | null;
  createdAt: string;
  updatedAt?: string;
  // Aliases for compatibility
  scope?: string;
  format?: string;
  fileSizeBytes?: number;
}

export interface ExportListResponse {
  exports: ExportJob[];
  jobs?: ExportJob[];
}

export interface CreateExportRequest {
  scope?: string;
  format?: 'csv' | 'json' | string;
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
  userId?: string | null;
  deletionScope: DeletionScope | string;
  scopeFilter?: Record<string, unknown>;
  requestStatus: DeletionRequestStatus;
  phase?: string;
  progressCursor?: number;
  cancelBefore?: string | null;
  cancelledAt?: string | null;
  requestedAt?: string;
  completedAt?: string | null;
  auditReference?: string | null;
  // Compatibility aliases
  scope?: DeletionScope;
  createdAt?: string;
  details?: Record<string, unknown>;
}

export interface CreateDeletionRequest {
  scope?: DeletionScope | string;
  deletionScope?: DeletionScope | string;
  targetId?: string;
  confirmation: boolean | string;
}

// Public & Community
export interface PublicUserProfile {
  userId?: string;
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  bio?: string | null;
  profileStatus?: string;
  rank?: number | null;
  rankDelta?: number | null;
  percentile?: number | string | null;
  tokenTotal?: string | null;
  activeDays?: number | null;
  currentStreak?: number | null;
  dataWatermarkAt?: string | null;
  generatedAt: string;
  projectionVersion: number;
  showBio?: boolean;
  showTokenTotal?: boolean;
  showTrends?: boolean;
  showActivityCalendar?: boolean;
  showAgentBreakdown?: boolean;
  showSkillRanking?: boolean;
  showAchievements?: boolean;
  // Aliases for display
  codeLinesTotal?: string | null;
  estimatedCostTotal?: string | null;
  tokenTrend?: TokenTrendPoint[];
  activityCalendar?: CalendarDay[];
  agentBreakdown?: AgentBreakdownItem[];
  skillRanking?: SkillItem[];
}

export interface SearchUserResult {
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  bio?: string | null;
  rank?: number | null;
  tokenTotal?: string;
  topAgent?: string;
}

export interface SearchAgentResult {
  agentId: string;
  name: string;
  description?: string;
  // Compatibility aliases
  displayName?: string;
  developerCount?: string;
  tokenTotal30d?: string;
  tags?: string[];
}

export interface SearchSkillResult {
  skillId: string;
  skillPublicName: string;
  useCount: string | number;
  publicUserCount: number;
  activeDays: number;
  // Compatibility aliases
  userCount?: number;
  growthDelta?: number;
  tags?: string[];
}

export interface SearchResponse {
  users: SearchUserResult[];
  agents: SearchAgentResult[];
  skills?: SearchSkillResult[];
  query?: string;
  totalCount?: number;
}

export interface LeaderboardEntry {
  rankNo: number;
  handle: string;
  displayName: string;
  avatarUrl: string | null;
  metricValue: string;
  rankDelta?: number | null;
  formattedMetric?: string;
  topAgent?: string;
  activeDays?: number;
}

export interface LeaderboardResponse {
  snapshotId: string;
  boardKey: string;
  window: string;
  metric: string;
  entries: LeaderboardEntry[];
  nextCursor?: string | null;
  dataWatermarkAt?: string | null;
  // Aliases
  agent?: string;
  generatedAt?: string;
  totalEntries?: number;
}

export interface CompareUserItem {
  handle: string;
  displayName?: string | null;
  avatarUrl?: string | null;
  visible: boolean;
  tokenTotal?: string | null;
  rank?: number | null;
  percentile?: number | string | null;
  dataWatermarkAt?: string | null;
  // Extra fields
  codeLinesTotal?: string | null;
  activeDays?: number | null;
  currentStreak?: number | null;
  topAgent?: string | null;
  agentBreakdown?: AgentBreakdownItem[];
  skillRanking?: SkillItem[];
}

export interface CompareResponse {
  users: CompareUserItem[];
  generatedAt: string;
  range?: string;
  metric?: string;
}

export type UserComparisonItem = CompareUserItem;
export type UserComparisonResponse = CompareResponse;
