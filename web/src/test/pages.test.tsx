import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { api, ApiError } from '@/api/client';

import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { ExplorePage } from '@/pages/public/ExplorePage';
import { PublicProfilePage } from '@/pages/public/PublicProfilePage';
import { ComparePage } from '@/pages/public/ComparePage';
import { ActivityPage } from '@/pages/me/ActivityPage';
import { PersonalDashboardPage } from '@/pages/me/PersonalDashboardPage';
import { PrivacySettingsPage } from '@/pages/settings/PrivacySettingsPage';
import { DevicesSettingsPage } from '@/pages/settings/DevicesSettingsPage';
import { ExportsSettingsPage } from '@/pages/settings/ExportsSettingsPage';
import { ProfileSettingsPage } from '@/pages/settings/ProfileSettingsPage';

function renderWithProviders(ui: React.ReactElement, initialRoute = '/') {
  return render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[initialRoute]}>
            {ui}
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>
  );
}

describe('Shipped Pages & Failed API Paths Tests', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  describe('LeaderboardPage', () => {
    it('renders real leaderboard entries from typed API', async () => {
      vi.spyOn(api, 'getLeaderboard').mockResolvedValue({
        boardKey: 'global',
        window: '30d',
        metric: 'tokens',
        agent: 'all',
        snapshotId: 'snp_123',
        generatedAt: new Date().toISOString(),
        totalEntries: 2,
        nextCursor: null,
        entries: [
          {
            rankNo: 1,
            rankDelta: 0,
            handle: 'alice',
            displayName: 'Alice Engineer',
            avatarUrl: null,
            metricValue: '500000000',
            formattedMetric: '500M',
            topAgent: 'Claude Code',
            activeDays: 30,
          },
        ],
      });

      renderWithProviders(<LeaderboardPage />, '/leaderboard');

      expect(screen.getByRole('status')).toBeInTheDocument(); // LoadingState

      await waitFor(() => {
        expect(screen.getByText('Alice Engineer')).toBeInTheDocument();
        expect(screen.getByText('@alice')).toBeInTheDocument();
        expect(screen.getByText('#1')).toBeInTheDocument();
      });
    });

    it('renders EmptyState when entries array is empty', async () => {
      vi.spyOn(api, 'getLeaderboard').mockResolvedValue({
        boardKey: 'global',
        window: '30d',
        metric: 'tokens',
        agent: 'all',
        snapshotId: 'snp_empty',
        generatedAt: new Date().toISOString(),
        totalEntries: 0,
        nextCursor: null,
        entries: [],
      });

      renderWithProviders(<LeaderboardPage />, '/leaderboard');

      await waitFor(() => {
        expect(screen.getByText('暂无相关数据')).toBeInTheDocument();
      });
    });

    it('renders ErrorState with retry button when API fails', async () => {
      const getLeaderboardSpy = vi.spyOn(api, 'getLeaderboard')
        .mockRejectedValueOnce(new ApiError(500, { code: 'HTTP_500', messageKey: 'errors.http_500' }))
        .mockResolvedValueOnce({
          boardKey: 'global',
          window: '30d',
          metric: 'tokens',
          agent: 'all',
          snapshotId: 'snp_retry',
          generatedAt: new Date().toISOString(),
          totalEntries: 1,
          nextCursor: null,
          entries: [
            { rankNo: 1, handle: 'bob', displayName: 'Bob Dev', avatarUrl: null, metricValue: '1000', formattedMetric: '1K' },
          ],
        });

      renderWithProviders(<LeaderboardPage />, '/leaderboard');

      await waitFor(() => {
        expect(screen.getByText('数据加载失败')).toBeInTheDocument();
        expect(screen.getByText('重试')).toBeInTheDocument();
      });

      fireEvent.click(screen.getByText('重试'));

      await waitFor(() => {
        expect(screen.getByText('Bob Dev')).toBeInTheDocument();
      });
      expect(getLeaderboardSpy).toHaveBeenCalledTimes(2);
    });
  });

  describe('ExplorePage', () => {
    it('executes search and displays user and agent results', async () => {
      vi.spyOn(api, 'searchPublic').mockResolvedValue({
        query: 'claude',
        totalCount: 2,
        users: [
          { handle: 'dev1', displayName: 'Dev One', avatarUrl: null, bio: 'Coding', rank: 5, tokenTotal: '100M', topAgent: 'Claude Code' },
        ],
        agents: [
          { agentId: 'claude-code', displayName: 'Claude Code', developerCount: '10K', tokenTotal30d: '1T', tags: ['agent'] },
        ],
        skills: [],
      });

      renderWithProviders(<ExplorePage />, '/explore?q=claude');

      await waitFor(() => {
        expect(screen.getByText('Dev One')).toBeInTheDocument();
        expect(screen.getByText('@dev1 · Claude Code')).toBeInTheDocument();
      });
    });

    it('renders ErrorState when search API fails', async () => {
      vi.spyOn(api, 'searchPublic').mockRejectedValue(
        new ApiError(503, { code: 'HTTP_503', messageKey: 'errors.http_503' })
      );

      renderWithProviders(<ExplorePage />, '/explore?q=test');

      await waitFor(() => {
        expect(screen.getByText('数据加载失败')).toBeInTheDocument();
      });
    });
  });

  describe('PublicProfilePage', () => {
    it('loads public profile and renders details', async () => {
      vi.spyOn(api, 'getPublicProfile').mockResolvedValue({
        handle: 'maxbauer',
        displayName: 'Max Bauer',
        avatarUrl: null,
        bio: 'Building with AI',
        rank: 1,
        rankDelta: 0,
        percentile: 'Top 0.1%',
        tokenTotal: '325.7M',
        codeLinesTotal: '864.2K',
        estimatedCostTotal: '$1,428.60',
        activeDays: 28,
        currentStreak: 23,
        dataWatermarkAt: new Date().toISOString(),
        generatedAt: new Date().toISOString(),
        tokenTrend: [],
        agentBreakdown: [],
        skillRanking: [],
      });

      render(
        <LocaleProvider>
          <NotificationProvider>
            <AuthProvider>
              <MemoryRouter initialEntries={['/u/maxbauer']}>
                <Routes>
                  <Route path="/u/:handle" element={<PublicProfilePage />} />
                </Routes>
              </MemoryRouter>
            </AuthProvider>
          </NotificationProvider>
        </LocaleProvider>
      );

      await waitFor(() => {
        expect(screen.getAllByText('Max Bauer').length).toBeGreaterThanOrEqual(1);
        expect(screen.getByText('@maxbauer')).toBeInTheDocument();
        expect(screen.getByText('Building with AI')).toBeInTheDocument();
        expect(screen.getByText('#1')).toBeInTheDocument();
        expect(screen.getByText('Top 0.1%')).toBeInTheDocument();
      });
    });

    it('renders ErrorState when public profile is not found (404)', async () => {
      vi.spyOn(api, 'getPublicProfile').mockRejectedValue(
        new ApiError(404, { code: 'PUBLIC_PROFILE_NOT_FOUND', messageKey: 'errors.PUBLIC_PROFILE_NOT_FOUND' })
      );

      render(
        <LocaleProvider>
          <NotificationProvider>
            <AuthProvider>
              <MemoryRouter initialEntries={['/u/unknown_user']}>
                <Routes>
                  <Route path="/u/:handle" element={<PublicProfilePage />} />
                </Routes>
              </MemoryRouter>
            </AuthProvider>
          </NotificationProvider>
        </LocaleProvider>
      );

      await waitFor(() => {
        expect(screen.getByText('数据加载失败')).toBeInTheDocument();
        expect(screen.getByText('PUBLIC_PROFILE_NOT_FOUND')).toBeInTheDocument();
      });
    });
  });

  describe('ComparePage', () => {
    it('renders comparison data for selected handles', async () => {
      vi.spyOn(api, 'compareUsers').mockResolvedValue({
        range: '30d',
        metric: 'tokens',
        generatedAt: new Date().toISOString(),
        users: [
          {
            handle: 'user1',
            displayName: 'User One',
            avatarUrl: null,
            visible: true,
            rank: 1,
            tokenTotal: '100M',
            codeLinesTotal: '50K',
            activeDays: 20,
            currentStreak: 15,
            topAgent: 'Codex',
          },
          {
            handle: 'user2',
            displayName: 'User Two',
            avatarUrl: null,
            visible: false,
            rank: null,
            tokenTotal: null,
            codeLinesTotal: null,
            activeDays: null,
            currentStreak: null,
            topAgent: null,
          },
        ],
      });

      renderWithProviders(<ComparePage />, '/compare?handles=user1,user2');

      await waitFor(() => {
        expect(screen.getByText('User One')).toBeInTheDocument();
        expect(screen.getByText('@user1')).toBeInTheDocument();
        expect(screen.getByText('User Two')).toBeInTheDocument();
        expect(screen.getByText('该用户未公开此项数据')).toBeInTheDocument();
      });
    });

    it('renders EmptyState when no handles are provided', async () => {
      renderWithProviders(<ComparePage />, '/compare');

      await waitFor(() => {
        expect(screen.getByText('最多选择 3 位公开用户进行对比')).toBeInTheDocument();
      });
    });
  });

  describe('ActivityPage & Unauthorized', () => {
    it('renders UnauthorizedState when user is not authenticated', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: false,
        user: null,
      });

      renderWithProviders(<ActivityPage />, '/me/activity');

      await waitFor(() => {
        expect(screen.getByText('需要登录')).toBeInTheDocument();
        expect(screen.getByText('前往登录')).toBeInTheDocument();
      });
    });

    it('renders activity rows when authenticated', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test User',
          handle: 'testuser',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      vi.spyOn(api, 'getActivityRows').mockResolvedValue({
        rows: [
          {
            occurredAt: '2026-08-30T10:00:00Z',
            agentId: 'claude-code',
            modelId: 'claude-3-7-sonnet',
            tokenTotal: '150000',
            inputTokens: '100000',
            outputTokens: '50000',
            sessionCount: 2,
            turnCount: 10,
            deviceName: 'Workstation',
            syncStatus: 'normal',
          },
        ],
        nextCursor: null,
      });

      renderWithProviders(<ActivityPage />, '/me/activity');

      await waitFor(() => {
        expect(screen.getByText('claude-code')).toBeInTheDocument();
        expect(screen.getByText('Workstation')).toBeInTheDocument();
        expect(screen.getByText('150,000')).toBeInTheDocument();
      });
    });
  });

  describe('Privacy & Device Updates Invalidate Shared Surfaces', () => {
    it('invalidates and refetches session when privacy is updated', async () => {
      const getSessionSpy = vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test User',
          handle: 'testuser',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      vi.spyOn(api, 'getPrivacy').mockResolvedValue({
        publicProfileEnabled: false,
        leaderboardVisibility: 'private',
        showBio: false,
        showTokenTotal: false,
        showTrends: false,
        showActivityCalendar: false,
        showAgentBreakdown: false,
        showSkillRanking: false,
        showAchievements: false,
        privacyVersion: 1,
      });

      const updatePrivacySpy = vi.spyOn(api, 'updatePrivacy').mockResolvedValue({
        publicProfileEnabled: true,
        leaderboardVisibility: 'public',
        showBio: true,
        showTokenTotal: true,
        showTrends: true,
        showActivityCalendar: true,
        showAgentBreakdown: true,
        showSkillRanking: true,
        showAchievements: true,
        privacyVersion: 2,
      });

      renderWithProviders(<PrivacySettingsPage />, '/settings/privacy');

      await waitFor(() => {
        expect(screen.getByText('参加公开排行榜')).toBeInTheDocument();
      });

      const saveBtn = screen.getByText('保存');
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(updatePrivacySpy).toHaveBeenCalled();
        // refreshSession was triggered to update shared productState
        expect(getSessionSpy).toHaveBeenCalledTimes(2);
      });
    });

    it('refetches devices and shared session after device pause/resume/revoke', async () => {
      const getSessionSpy = vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test User',
          handle: 'testuser',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      const getDevicesSpy = vi.spyOn(api, 'getDevices')
        .mockResolvedValueOnce({
          devices: [
            {
              installationId: 'inst_1',
              deviceName: 'Dev MacBook',
              osType: 'darwin',
              osVersion: '14.0',
              architecture: 'arm64',
              collectorVersion: '1.0.0',
              installationStatus: 'active',
              registeredAt: new Date().toISOString(),
              lastSeenAt: new Date().toISOString(),
              disabledAt: null,
              disabledReason: null,
              statusVersion: 1,
              adapterHealth: 'healthy',
            },
          ],
        })
        .mockResolvedValueOnce({
          devices: [
            {
              installationId: 'inst_1',
              deviceName: 'Dev MacBook',
              osType: 'darwin',
              osVersion: '14.0',
              architecture: 'arm64',
              collectorVersion: '1.0.0',
              installationStatus: 'disabled',
              registeredAt: new Date().toISOString(),
              lastSeenAt: new Date().toISOString(),
              disabledAt: new Date().toISOString(),
              disabledReason: 'user_paused',
              statusVersion: 2,
              adapterHealth: 'healthy',
            },
          ],
        });

      const pauseDeviceSpy = vi.spyOn(api, 'pauseDevice').mockResolvedValue({
        installationId: 'inst_1',
        deviceName: 'Dev MacBook',
        osType: 'darwin',
        osVersion: '14.0',
        architecture: 'arm64',
        collectorVersion: '1.0.0',
        installationStatus: 'disabled',
        registeredAt: new Date().toISOString(),
        lastSeenAt: new Date().toISOString(),
        disabledAt: new Date().toISOString(),
        disabledReason: 'user_paused',
        statusVersion: 2,
        adapterHealth: 'healthy',
      });

      renderWithProviders(<DevicesSettingsPage />, '/settings/devices');

      await waitFor(() => {
        expect(screen.getByText('Dev MacBook')).toBeInTheDocument();
        expect(screen.getByText('暂停同步')).toBeInTheDocument();
      });

      fireEvent.click(screen.getByText('暂停同步'));

      await waitFor(() => {
        expect(pauseDeviceSpy).toHaveBeenCalledWith('inst_1');
        expect(getDevicesSpy).toHaveBeenCalledTimes(2);
        expect(getSessionSpy).toHaveBeenCalledTimes(2);
      });
    });
  });

  describe('Exports & Deletions Settings', () => {
    it('creates export job and lists jobs', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test User',
          handle: 'testuser',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      const getExportsSpy = vi.spyOn(api, 'getExports')
        .mockResolvedValueOnce({
          jobs: [],
        })
        .mockResolvedValueOnce({
          jobs: [
            {
              exportId: 'exp_1',
              jobStatus: 'completed',
              scope: 'all_aggregates',
              format: 'csv',
              createdAt: new Date().toISOString(),
              completedAt: new Date().toISOString(),
              expiresAt: null,
              fileSizeBytes: 2048,
            },
          ],
        });

      const createExportSpy = vi.spyOn(api, 'createExport').mockResolvedValue({
        exportId: 'exp_1',
        jobStatus: 'pending',
        scope: 'all_aggregates',
        format: 'csv',
        createdAt: new Date().toISOString(),
        completedAt: null,
        expiresAt: null,
      });

      renderWithProviders(<ExportsSettingsPage />, '/settings/exports');

      await waitFor(() => {
        expect(screen.getByRole('button', { name: /\+?\s*创建导出任务/ })).toBeInTheDocument();
      });

      fireEvent.click(screen.getByRole('button', { name: /\+?\s*创建导出任务/ }));

      await waitFor(() => {
        expect(createExportSpy).toHaveBeenCalled();
        expect(getExportsSpy).toHaveBeenCalledTimes(2);
      });
    });
  });

  describe('ProfileSettingsPage', () => {
    it('loads and updates user profile and invalidates shared session', async () => {
      const getSessionSpy = vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test Dev',
          handle: 'testdev',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      vi.spyOn(api, 'getProfile').mockResolvedValue({
        userId: 'usr_01',
        displayName: 'Test Dev',
        handle: 'testdev',
        avatarUrl: null,
        bio: 'Hello world',
        timezone: 'Asia/Shanghai',
        locale: 'zh-CN',
        onboardingCompletedAt: '2026-08-30T10:00:00Z',
        profileVersion: 1,
      });

      const updateProfileSpy = vi.spyOn(api, 'updateProfile').mockResolvedValue({
        userId: 'usr_01',
        displayName: 'Updated Dev',
        handle: 'testdev',
        avatarUrl: null,
        bio: 'Updated bio',
        timezone: 'Asia/Shanghai',
        locale: 'zh-CN',
        onboardingCompletedAt: '2026-08-30T10:00:00Z',
        profileVersion: 2,
      });

      renderWithProviders(<ProfileSettingsPage />, '/settings/profile');

      await waitFor(() => {
        expect(screen.getByDisplayValue('Test Dev')).toBeInTheDocument();
      });

      const nameInput = screen.getByDisplayValue('Test Dev');
      fireEvent.change(nameInput, { target: { value: 'Updated Dev' } });

      const saveBtn = screen.getByRole('button', { name: '保存' });
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(updateProfileSpy).toHaveBeenCalled();
        expect(getSessionSpy).toHaveBeenCalledTimes(2);
      });
    });
  });

  describe('PersonalDashboardPage Full Integration', () => {
    it('loads and renders dashboard metrics, trends, and sync status', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test Dev',
          handle: 'testdev',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      vi.spyOn(api, 'getPersonalSummary').mockResolvedValue({
        range: { key: '30d', from: '2026-08-01', to: '2026-08-30', timezone: 'UTC' },
        metrics: {
          estimatedCost: { amount: '120.50', currency: 'USD', supported: true },
          totalTokens: { value: '50000000', supported: true },
          generatedCodeLines: { value: '12000', supported: true },
          tokensPerCodeLine: { value: '416.6', supported: true },
          inputContextTokens: { value: '35000000', supported: true },
          outputTokens: { value: '15000000', supported: true },
          cacheHitRate: { value: '0.42', supported: true },
          activeDurationMs: { value: '36000000', supported: true },
          messageCount: { value: '2500', supported: true },
          userMessageCount: { value: '1100', supported: true },
        },
        ranking: { visibility: 'private', rank: null, delta: null, percentile: null },
        sync: { lastCommittedAt: '2026-08-30T15:00:00Z', pendingLocalCount: null },
        dataWatermarkAt: '2026-08-30T15:00:00Z',
        aggregationVersion: 2,
      });

      vi.spyOn(api, 'getTokenTrends').mockResolvedValue({
        points: [
          { date: '2026-08-29', tokenTotal: '2000000', inputTokens: '1500000', outputTokens: '500000' },
          { date: '2026-08-30', tokenTotal: '3000000', inputTokens: '2000000', outputTokens: '1000000' },
        ],
      });

      vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({
        items: [
          { agentId: 'claude-code', displayName: 'Claude Code', tokenTotal: '35000000', percentage: 70 },
          { agentId: 'codex', displayName: 'Codex', tokenTotal: '15000000', percentage: 30 },
        ],
      });

      vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({
        items: [
          { rankNo: 1, skillPublicName: 'code-review', useCount: 150, activeDays: 10 },
        ],
      });

      vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({
        days: [
          { date: '2026-08-30', tokenTotal: '3000000', level: 4 },
        ],
        currentStreak: 5,
      });

      vi.spyOn(api, 'getFilterOptions').mockResolvedValue({
        agents: ['claude-code', 'codex'],
        providers: ['anthropic', 'openai'],
        models: ['claude-3-7-sonnet', 'gpt-4o'],
      });

      renderWithProviders(<PersonalDashboardPage />, '/me');

      await waitFor(() => {
        expect(screen.getByText('你的 Token 正在起舞')).toBeInTheDocument();
        expect(screen.getByText('$120.50')).toBeInTheDocument();
        expect(screen.getByText('50.0M')).toBeInTheDocument();
        expect(screen.getByText('Claude Code')).toBeInTheDocument();
        expect(screen.getByText('code-review')).toBeInTheDocument();
        // pendingLocalCount is null -> renders 未知
        expect(screen.getByText('未知')).toBeInTheDocument();
      });
    });

    it('renders ErrorState when personal summary fails', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue({
        authenticated: true,
        user: {
          userId: 'usr_01',
          displayName: 'Test Dev',
          handle: 'testdev',
          avatarUrl: null,
          locale: 'zh-CN',
          onboardingRequired: false,
          productState: 'active_private',
        },
      });

      vi.spyOn(api, 'getPersonalSummary').mockRejectedValue(
        new ApiError(500, { code: 'HTTP_500', messageKey: 'errors.http_500' })
      );
      vi.spyOn(api, 'getTokenTrends').mockResolvedValue({ points: [] });
      vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({ items: [] });
      vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({ items: [] });
      vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({ days: [], currentStreak: 0 });
      vi.spyOn(api, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });

      renderWithProviders(<PersonalDashboardPage />, '/me');

      await waitFor(() => {
        expect(screen.getByText('数据加载失败')).toBeInTheDocument();
      });
    });
  });
});
