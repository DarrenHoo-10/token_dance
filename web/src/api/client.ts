// Real typed HTTP API client for TokenDance targeting /api/v1 and /v1
import type {
  ApiErrorDetail,
  ApiErrorResponse,
  SessionResponse,
  AuthResponse,
  LoginRequest,
  RegisterCodeRequest,
  RegisterRequest,
  PasswordCodeRequest,
  PasswordResetRequest,
  ActiveSession,
  UserProfile,
  UpdateProfileRequest,
  PrivacySettings,
  UpdatePrivacyRequest,
  OnboardingRequest,
  OnboardingResponse,
  AvatarUploadIntentResponse,
  PersonalSummary,
  TokenTrendsResponse,
  AgentBreakdownItem,
  ModelBreakdownItem,
  SkillMetricItem,
  ActivityCalendarDay,
  ActivityRow,
  FilterOptionsResponse,
  CollectorDevice,
  DeviceBindingChallengeResponse,
  ClaimInstallationRequest,
  ClaimInstallationResponse,
  ExportJob,
  CreateExportRequest,
  ExportDownloadResponse,
  DeletionRequest,
  CreateDeletionRequest,
  PublicUserProfile,
  SearchResponse,
  LeaderboardResponse,
  UserComparisonResponse,
} from '@/types/api';

export class ApiError extends Error {
  public readonly status: number;
  public readonly code: string;
  public readonly messageKey: string;
  public readonly requestId?: string;
  public readonly details?: Record<string, unknown>;

  constructor(status: number, errorDetail: ApiErrorDetail) {
    super(errorDetail.messageKey || errorDetail.code || `API error ${status}`);
    this.name = 'ApiError';
    this.status = status;
    this.code = errorDetail.code || 'UNKNOWN_ERROR';
    this.messageKey = errorDetail.messageKey || 'errors.unknown';
    this.requestId = errorDetail.requestId;
    this.details = errorDetail.details;
  }
}

class ApiHttpClient {
  private csrfToken: string | null = null;
  private baseUrl = '';

  public setCsrfToken(token: string | null): void {
    this.csrfToken = token;
  }

  public getCsrfToken(): string | null {
    return this.csrfToken;
  }

  public setBaseUrl(url: string): void {
    this.baseUrl = url;
  }

  private generateRequestId(): string {
    return 'req_' + Math.random().toString(36).substring(2, 11) + Date.now().toString(36);
  }

  private async request<T>(
    path: string,
    options: RequestInit = {},
    versionPrefix = '/api/v1'
  ): Promise<T> {
    const url = `${this.baseUrl}${versionPrefix}${path}`;
    const headers = new Headers(options.headers || {});

    if (!headers.has('Accept')) {
      headers.set('Accept', 'application/json');
    }

    if (
      ['POST', 'PATCH', 'PUT', 'DELETE'].includes((options.method || 'GET').toUpperCase()) &&
      !headers.has('Content-Type') &&
      options.body &&
      typeof options.body === 'string'
    ) {
      headers.set('Content-Type', 'application/json; charset=utf-8');
    }

    if (this.csrfToken && ['POST', 'PATCH', 'PUT', 'DELETE'].includes((options.method || 'GET').toUpperCase())) {
      headers.set('X-CSRF-Token', this.csrfToken);
    }

    if (!headers.has('X-Request-Id')) {
      headers.set('X-Request-Id', this.generateRequestId());
    }

    const response = await fetch(url, {
      ...options,
      headers,
      credentials: 'include',
    });

    if (response.status === 204) {
      return {} as T;
    }

    const contentType = response.headers.get('Content-Type') || '';
    const isJson = contentType.includes('application/json');

    if (!response.ok) {
      let errorDetail: ApiErrorDetail = {
        code: `HTTP_${response.status}`,
        messageKey: `errors.http_${response.status}`,
      };

      if (isJson) {
        try {
          const body = (await response.json()) as ApiErrorResponse;
          if (body && body.error) {
            errorDetail = body.error;
          }
        } catch {
          // ignore parse error
        }
      } else {
        const text = await response.text();
        errorDetail.details = { text };
      }

      throw new ApiError(response.status, errorDetail);
    }

    if (!isJson) {
      return (await response.text()) as unknown as T;
    }

    return (await response.json()) as T;
  }

  // --- Auth APIs ---
  public async getSession(): Promise<SessionResponse> {
    const res = await this.request<SessionResponse>('/auth/session', { method: 'GET' });
    if (res.csrfToken) {
      this.setCsrfToken(res.csrfToken);
    }
    return res;
  }

  public async requestRegisterCode(data: RegisterCodeRequest): Promise<{ status: string; cooldownSeconds: number }> {
    return this.request<{ status: string; cooldownSeconds: number }>('/auth/register/code', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  public async register(data: RegisterRequest): Promise<AuthResponse> {
    const res = await this.request<AuthResponse>('/auth/register', {
      method: 'POST',
      body: JSON.stringify(data),
    });
    if (res.csrfToken) {
      this.setCsrfToken(res.csrfToken);
    }
    return res;
  }

  public async login(data: LoginRequest): Promise<AuthResponse> {
    const res = await this.request<AuthResponse>('/auth/login', {
      method: 'POST',
      body: JSON.stringify(data),
    });
    if (res.csrfToken) {
      this.setCsrfToken(res.csrfToken);
    }
    return res;
  }

  public async logout(): Promise<void> {
    await this.request<void>('/auth/logout', { method: 'POST' });
    this.setCsrfToken(null);
  }

  public async getSessions(): Promise<{ sessions: ActiveSession[] }> {
    return this.request<{ sessions: ActiveSession[] }>('/auth/sessions', { method: 'GET' });
  }

  public async revokeSession(sessionId: string): Promise<void> {
    return this.request<void>(`/auth/sessions/${encodeURIComponent(sessionId)}`, { method: 'DELETE' });
  }

  public async revokeOtherSessions(): Promise<void> {
    return this.request<void>('/auth/sessions/revoke-others', { method: 'POST' });
  }

  public async requestPasswordResetCode(data: PasswordCodeRequest): Promise<{ status: string; cooldownSeconds: number }> {
    return this.request<{ status: string; cooldownSeconds: number }>('/auth/password/code', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  public async resetPassword(data: PasswordResetRequest): Promise<void> {
    return this.request<void>('/auth/password/reset', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  }

  // --- Onboarding & Profile ---
  public async completeOnboarding(data: OnboardingRequest): Promise<OnboardingResponse> {
    const res = await this.request<OnboardingResponse>('/me/onboarding', {
      method: 'POST',
      body: JSON.stringify(data),
    });
    // Ensure profile fallback if needed
    if (res.user && !res.profile) {
      res.profile = res.user;
    }
    return res;
  }

  public async getProfile(): Promise<UserProfile> {
    return this.request<UserProfile>('/me/profile', { method: 'GET' });
  }

  public async updateProfile(data: UpdateProfileRequest, profileVersion?: number): Promise<UserProfile> {
    const headers: Record<string, string> = {};
    if (profileVersion !== undefined) {
      headers['If-Match'] = `"${profileVersion}"`;
    }
    return this.request<UserProfile>('/me/profile', {
      method: 'PATCH',
      headers,
      body: JSON.stringify(data),
    });
  }

  public async getPrivacy(): Promise<PrivacySettings> {
    return this.request<PrivacySettings>('/me/privacy', { method: 'GET' });
  }

  public async updatePrivacy(data: UpdatePrivacyRequest, privacyVersion?: number): Promise<PrivacySettings> {
    const headers: Record<string, string> = {};
    if (privacyVersion !== undefined) {
      headers['If-Match'] = `"${privacyVersion}"`;
    }
    return this.request<PrivacySettings>('/me/privacy', {
      method: 'PATCH',
      headers,
      body: JSON.stringify(data),
    });
  }

  public async getPublicPreview(): Promise<PublicUserProfile> {
    return this.request<PublicUserProfile>('/me/public-preview', { method: 'GET' });
  }

  public async createAvatarUploadIntent(contentType: string, sizeBytes: number, sha256: string): Promise<AvatarUploadIntentResponse> {
    return this.request<AvatarUploadIntentResponse>('/me/avatar-upload-intents', {
      method: 'POST',
      body: JSON.stringify({ contentType, sizeBytes, sha256 }),
    });
  }

  public async completeAvatarUpload(intentId: string): Promise<UserProfile> {
    return this.request<UserProfile>(`/me/avatar-upload-intents/${encodeURIComponent(intentId)}/complete`, {
      method: 'POST',
    });
  }

  public async deleteAvatar(): Promise<void> {
    return this.request<void>('/me/avatar', { method: 'DELETE' });
  }

  // --- Personal Analytics ---
  public async getPersonalSummary(range = '30d'): Promise<PersonalSummary> {
    const params = new URLSearchParams({ range });
    return this.request<PersonalSummary>(`/me/summary?${params.toString()}`, { method: 'GET' });
  }

  public async getTokenTrends(params: {
    range?: string;
    from?: string;
    to?: string;
    agent?: string;
    provider?: string;
    model?: string;
    mode?: 'total' | 'structure';
  }): Promise<TokenTrendsResponse> {
    const searchParams = new URLSearchParams();
    if (params.range) searchParams.set('range', params.range);
    if (params.from) searchParams.set('from', params.from);
    if (params.to) searchParams.set('to', params.to);
    if (params.agent) searchParams.set('agent', params.agent);
    if (params.provider) searchParams.set('provider', params.provider);
    if (params.model) searchParams.set('model', params.model);
    if (params.mode) searchParams.set('mode', params.mode);

    return this.request<TokenTrendsResponse>(`/me/trends/tokens?${searchParams.toString()}`, { method: 'GET' });
  }

  public async getAgentBreakdowns(range = '30d'): Promise<{ items: AgentBreakdownItem[] }> {
    const params = new URLSearchParams({ range });
    return this.request<{ items: AgentBreakdownItem[] }>(`/me/breakdowns/agents?${params.toString()}`, { method: 'GET' });
  }

  public async getModelBreakdowns(range = '30d'): Promise<{ items: ModelBreakdownItem[] }> {
    const params = new URLSearchParams({ range });
    return this.request<{ items: ModelBreakdownItem[] }>(`/me/breakdowns/models?${params.toString()}`, { method: 'GET' });
  }

  public async getPersonalSkills(range = '30d'): Promise<{ items: SkillMetricItem[] }> {
    const params = new URLSearchParams({ range });
    return this.request<{ items: SkillMetricItem[] }>(`/me/skills?${params.toString()}`, { method: 'GET' });
  }

  public async getActivityCalendar(range = '10w'): Promise<{ days: ActivityCalendarDay[]; currentStreak: number }> {
    const params = new URLSearchParams({ range });
    return this.request<{ days: ActivityCalendarDay[]; currentStreak: number }>(`/me/calendar?${params.toString()}`, { method: 'GET' });
  }

  public async getActivityRows(params: {
    range?: string;
    agent?: string;
    model?: string;
    limit?: number;
    cursor?: string;
  }): Promise<{ rows: ActivityRow[]; nextCursor: string | null }> {
    const searchParams = new URLSearchParams();
    if (params.range) searchParams.set('range', params.range);
    if (params.agent) searchParams.set('agent', params.agent);
    if (params.model) searchParams.set('model', params.model);
    if (params.limit) searchParams.set('limit', params.limit.toString());
    if (params.cursor) searchParams.set('cursor', params.cursor);

    return this.request<{ rows: ActivityRow[]; nextCursor: string | null }>(`/me/activity?${searchParams.toString()}`, { method: 'GET' });
  }

  public async getFilterOptions(): Promise<FilterOptionsResponse> {
    return this.request<FilterOptionsResponse>('/me/filter-options', { method: 'GET' });
  }

  // --- Collector Devices ---
  public async getDevices(): Promise<{ devices: CollectorDevice[] }> {
    return this.request<{ devices: CollectorDevice[] }>('/me/devices', { method: 'GET' });
  }

  public async createDeviceBindingChallenge(): Promise<DeviceBindingChallengeResponse> {
    return this.request<DeviceBindingChallengeResponse>('/me/device-bindings', { method: 'POST' });
  }

  public async cancelDeviceBindingChallenge(challengeId: string): Promise<void> {
    return this.request<void>(`/me/device-bindings/${encodeURIComponent(challengeId)}`, { method: 'DELETE' });
  }

  public async updateDeviceName(installationId: string, deviceName: string): Promise<CollectorDevice> {
    return this.request<CollectorDevice>(`/me/devices/${encodeURIComponent(installationId)}`, {
      method: 'PATCH',
      body: JSON.stringify({ deviceName }),
    });
  }

  public async pauseDevice(installationId: string): Promise<CollectorDevice> {
    return this.request<CollectorDevice>(`/me/devices/${encodeURIComponent(installationId)}/pause`, { method: 'POST' });
  }

  public async resumeDevice(installationId: string): Promise<CollectorDevice> {
    return this.request<CollectorDevice>(`/me/devices/${encodeURIComponent(installationId)}/resume`, { method: 'POST' });
  }

  public async revokeDevice(installationId: string): Promise<void> {
    return this.request<void>(`/me/devices/${encodeURIComponent(installationId)}`, { method: 'DELETE' });
  }

  public async claimInstallation(data: ClaimInstallationRequest): Promise<ClaimInstallationResponse> {
    return this.request<ClaimInstallationResponse>('/installations/claim', {
      method: 'POST',
      body: JSON.stringify(data),
    }, '/v1');
  }

  // --- Exports & Deletions ---
  public async createExport(data: CreateExportRequest, idempotencyKey?: string): Promise<ExportJob> {
    const headers: Record<string, string> = {};
    if (idempotencyKey) {
      headers['Idempotency-Key'] = idempotencyKey;
    }
    const payload = {
      scope: data.scope || 'all_aggregates',
      format: data.format || 'csv',
      filter: data.filter || {},
    };
    return this.request<ExportJob>('/me/exports', {
      method: 'POST',
      headers,
      body: JSON.stringify(payload),
    });
  }

  public async getExports(): Promise<{ jobs: ExportJob[] }> {
    return this.request<{ jobs: ExportJob[] }>('/me/exports', { method: 'GET' });
  }

  public async getExportStatus(exportId: string): Promise<ExportJob> {
    return this.request<ExportJob>(`/me/exports/${encodeURIComponent(exportId)}`, { method: 'GET' });
  }

  public async getExportDownloadUrl(exportId: string): Promise<ExportDownloadResponse> {
    return this.request<ExportDownloadResponse>(`/me/exports/${encodeURIComponent(exportId)}/download`, { method: 'GET' });
  }

  public async createDeletionRequest(data: CreateDeletionRequest): Promise<DeletionRequest> {
    const payload = {
      scope: data.scope || data.deletionScope || 'account',
      confirmation: typeof data.confirmation === 'boolean' ? data.confirmation : Boolean(data.confirmation),
    };
    return this.request<DeletionRequest>('/me/deletion-requests', {
      method: 'POST',
      body: JSON.stringify(payload),
    });
  }

  public async getDeletionRequest(requestId: string): Promise<DeletionRequest> {
    return this.request<DeletionRequest>(`/me/deletion-requests/${encodeURIComponent(requestId)}`, { method: 'GET' });
  }

  public async cancelDeletionRequest(requestId: string): Promise<void> {
    return this.request<void>(`/me/deletion-requests/${encodeURIComponent(requestId)}/cancel`, { method: 'POST' });
  }

  // --- Public APIs ---
  public async getPublicProfile(handle: string): Promise<PublicUserProfile> {
    return this.request<PublicUserProfile>(`/public/users/${encodeURIComponent(handle)}`, { method: 'GET' });
  }

  public async getPublicTokenTrends(handle: string, params: {
    range?: string;
    agent?: string;
    provider?: string;
    model?: string;
  }): Promise<TokenTrendsResponse> {
    const searchParams = new URLSearchParams();
    if (params.range) searchParams.set('range', params.range);
    if (params.agent) searchParams.set('agent', params.agent);
    if (params.provider) searchParams.set('provider', params.provider);
    if (params.model) searchParams.set('model', params.model);

    return this.request<TokenTrendsResponse>(`/public/users/${encodeURIComponent(handle)}/trends?${searchParams.toString()}`, { method: 'GET' });
  }

  public async getPublicSkills(handle: string): Promise<{ items: SkillMetricItem[] }> {
    return this.request<{ items: SkillMetricItem[] }>(`/public/users/${encodeURIComponent(handle)}/skills`, { method: 'GET' });
  }

  public async searchPublic(query: string, filter?: string): Promise<SearchResponse> {
    const params = new URLSearchParams({ q: query });
    if (filter) params.set('filter', filter);
    return this.request<SearchResponse>(`/public/search?${params.toString()}`, { method: 'GET' });
  }

  public async getLeaderboard(params: {
    window?: string;
    metric?: string;
    agent?: string;
    q?: string;
    from?: string;
    to?: string;
    cursor?: string;
    limit?: number;
  }): Promise<LeaderboardResponse> {
    const searchParams = new URLSearchParams();
    if (params.window) searchParams.set('window', params.window);
    if (params.metric) searchParams.set('metric', params.metric);
    if (params.agent) searchParams.set('agent', params.agent);
    if (params.q) searchParams.set('q', params.q);
    if (params.from) searchParams.set('from', params.from);
    if (params.to) searchParams.set('to', params.to);
    if (params.cursor) searchParams.set('cursor', params.cursor);
    if (params.limit) searchParams.set('limit', params.limit.toString());

    return this.request<LeaderboardResponse>(`/public/leaderboards?${searchParams.toString()}`, { method: 'GET' });
  }

  public async compareUsers(handles: string[], range = '30d', metric = 'tokens'): Promise<UserComparisonResponse> {
    const params = new URLSearchParams({
      handles: handles.join(','),
      range,
      metric,
    });
    return this.request<UserComparisonResponse>(`/public/compare?${params.toString()}`, { method: 'GET' });
  }
}

export const api = new ApiHttpClient();
