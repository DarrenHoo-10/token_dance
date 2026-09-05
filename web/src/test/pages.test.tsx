import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { api, ApiError } from '@/api/client';

import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { PublicProfilePage } from '@/pages/public/PublicProfilePage';
import { CommunityPage } from '@/pages/public/CommunityPage';
import { TeamDashboardPage } from '@/pages/teams/TeamDashboardPage';
import { ActivityPage } from '@/pages/me/ActivityPage';
import { PersonalDashboardPage } from '@/pages/me/PersonalDashboardPage';
import { PrivacySettingsPage } from '@/pages/settings/PrivacySettingsPage';
import { DevicesSettingsPage } from '@/pages/settings/DevicesSettingsPage';
import { ExportsSettingsPage } from '@/pages/settings/ExportsSettingsPage';
import { SettingsLayout } from '@/components/layout/SettingsLayout';
import { ProfileSettingsPage } from '@/pages/settings/ProfileSettingsPage';

function renderWithProviders(ui: React.ReactElement, initialRoute = '/') {
  return render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[initialRoute]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
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
    const leaderboardPayload = {
      snapshotId: 'snap-1',
      boardKey: 'global',
      window: 'today',
      metric: 'tokens',
      entries: [
        { rankNo: 1, handle: 'ada', displayName: 'Ada Lovelace', avatarUrl: null, metricValue: '325700000', rankDelta: 2, topAgent: 'Claude Code', activeDays: 21 },
        { rankNo: 2, handle: 'grace', displayName: 'Grace Hopper', avatarUrl: null, metricValue: '215400000', rankDelta: -1, topAgent: 'Codex CLI', activeDays: 18 },
        { rankNo: 3, handle: 'linus', displayName: 'Linus Torvalds', avatarUrl: null, metricValue: '178900000', rankDelta: 0, topAgent: 'Cursor', activeDays: 15 },
        { rankNo: 4, handle: 'margaret', displayName: 'Margaret Hamilton', avatarUrl: null, metricValue: '142600000', rankDelta: 1, topAgent: 'Claude Code', activeDays: 12 },
      ],
      dataWatermarkAt: null,
    };

    it('renders real leaderboard entries returned by the API', async () => {
      const spy = vi.spyOn(api, 'getLeaderboard').mockResolvedValue(leaderboardPayload);
      renderWithProviders(<LeaderboardPage />, '/leaderboard');
      await waitFor(() => expect(screen.getByText('ada')).toBeInTheDocument());
      expect(screen.getByText('grace')).toBeInTheDocument();
      expect(screen.getAllByText('325.7M')).toHaveLength(2);
      expect(spy).toHaveBeenCalledWith({ window: 'today', limit: 10 });
    });

    it('shows an empty state instead of mock data when the API returns no entries', async () => {
      vi.spyOn(api, 'getLeaderboard').mockResolvedValue({ ...leaderboardPayload, entries: [] });
      renderWithProviders(<LeaderboardPage />, '/leaderboard');
      await waitFor(() => expect(screen.getByText('本周期暂无公开排行数据。只有已公开 Token 用量的用户会参与排名。')).toBeInTheDocument());
      expect(screen.queryByRole('search')).not.toBeInTheDocument();
    });
  });

  describe('CommunityPage and TeamDashboardPage', () => {
    it('renders the intentionally blank community page', () => {
      renderWithProviders(<CommunityPage />, '/community');
      expect(screen.getByRole('heading', { name: '社区' })).toBeInTheDocument();
      expect(screen.getByText('这里暂时留白')).toBeInTheDocument();
    });

    it('shows teams as unavailable without fabricated metrics or controls', () => {
      renderWithProviders(<TeamDashboardPage />, '/teams');
      expect(screen.getByRole('heading', { name: '小团队 Token 分析' })).toBeInTheDocument();
      expect(screen.getByText('团队功能尚未开放')).toBeInTheDocument();
      expect(screen.queryByText('410.1M')).not.toBeInTheDocument();
      expect(screen.queryByText('Max Bauer')).not.toBeInTheDocument();
      expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
      expect(screen.getByRole('link', { name: '查看个人数据' })).toHaveAttribute('href', '/me');
    });
  });

  describe('PublicProfilePage', () => {
    it('loads public profile and renders details, fetching trends and skills separately', async () => {
      const getProfileSpy = vi.spyOn(api, 'getPublicProfile').mockResolvedValue({
        handle: 'maxbauer',
        displayName: 'Max Bauer',
        avatarUrl: null,
        bio: 'Building with AI',
        rank: 1,
        rankDelta: 0,
        percentile: 99.9,
        tokenTotal: '325700000',
        activeDays: 28,
        currentStreak: 23,
        dataWatermarkAt: '2026-08-30T10:00:00Z',
        generatedAt: '2026-08-30T10:00:00Z',
        projectionVersion: 1,
      });

      const getTrendsSpy = vi.spyOn(api, 'getPublicTokenTrends').mockResolvedValue({
        points: [
          { date: '2026-08-30', tokenTotal: '5000000' },
        ],
      });

      const getSkillsSpy = vi.spyOn(api, 'getPublicSkills').mockResolvedValue({
        skills: [
          { skillId: 'sk_01', skillPublicName: 'test-runner', useCount: '45', activeDays: 10 },
        ],
      });

      render(
        <LocaleProvider>
          <NotificationProvider>
            <AuthProvider>
              <MemoryRouter initialEntries={['/u/maxbauer']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
                <Routes>
                  <Route path="/u/:handle" element={<PublicProfilePage />} />
                </Routes>
              </MemoryRouter>
            </AuthProvider>
          </NotificationProvider>
        </LocaleProvider>
      );

      await waitFor(() => {
        expect(getProfileSpy).toHaveBeenCalledWith('maxbauer');
        expect(getTrendsSpy).toHaveBeenCalledWith('maxbauer', { range: '30d' });
        expect(getSkillsSpy).toHaveBeenCalledWith('maxbauer', '30d');
        expect(screen.getAllByText('Max Bauer').length).toBeGreaterThanOrEqual(1);
        expect(screen.getByText(/@maxbauer/)).toBeInTheDocument();
        expect(screen.getByText(/Building with AI/)).toBeInTheDocument();
        expect(screen.getByText('#1')).toBeInTheDocument();
        expect(screen.getByText('325.7M')).toBeInTheDocument();
        expect(screen.getByText('test-runner')).toBeInTheDocument();
      });
    });

    it('shows a neutral unavailable state and skips metrics for a private or missing profile', async () => {
      const trendsSpy = vi.spyOn(api, 'getPublicTokenTrends');
      const skillsSpy = vi.spyOn(api, 'getPublicSkills');
      vi.spyOn(api, 'getPublicProfile').mockRejectedValue(
        new ApiError(404, { code: 'PUBLIC_PROFILE_NOT_FOUND', messageKey: 'errors.PUBLIC_PROFILE_NOT_FOUND' })
      );

      render(
        <LocaleProvider>
          <NotificationProvider>
            <AuthProvider>
              <MemoryRouter initialEntries={['/u/unknown_user']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
                <Routes>
                  <Route path="/u/:handle" element={<PublicProfilePage />} />
                </Routes>
              </MemoryRouter>
            </AuthProvider>
          </NotificationProvider>
        </LocaleProvider>
      );

      await waitFor(() => {
        expect(screen.getByText('公开主页暂不可用')).toBeInTheDocument();
        expect(screen.queryByText('PUBLIC_PROFILE_NOT_FOUND')).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument();
        expect(screen.getByRole('link', { name: '返回 TokenBoard' })).toHaveAttribute('href', '/leaderboard');
        expect(screen.queryByRole('link', { name: '隐私与公开' })).not.toBeInTheDocument();
        expect(trendsSpy).not.toHaveBeenCalled();
        expect(skillsSpy).not.toHaveBeenCalled();
      });
    });
  });

  describe('Unavailable profiles and settings navigation', () => {
    const signedIn = {
      authenticated: true,
      user: { userId: 'usr_01', displayName: 'Test User', handle: 'testuser', avatarUrl: null,
        locale: 'zh-CN' as const, onboardingRequired: false, productState: 'active_private' as const },
    };

    it('opens the owner personal dashboard without requesting public data or changing privacy', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue(signedIn);
      const profileSpy = vi.spyOn(api, 'getPublicProfile').mockRejectedValue(new ApiError(404, {
        code: 'PUBLIC_PROFILE_NOT_FOUND', messageKey: 'errors.PUBLIC_PROFILE_NOT_FOUND',
      }));
      const privacySpy = vi.spyOn(api, 'updatePrivacy');
      renderWithProviders(<Routes><Route path="/u/:handle" element={<PublicProfilePage />} /><Route path="/me" element={<h1>自己的数据</h1>} /></Routes>, '/u/TestUser');
      expect(await screen.findByRole('heading', { name: '自己的数据' })).toBeInTheDocument();
      expect(profileSpy).not.toHaveBeenCalled();
      expect(privacySpy).not.toHaveBeenCalled();
    });

    it('keeps retry available for actual service failures', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
      const profileSpy = vi.spyOn(api, 'getPublicProfile').mockRejectedValue(new ApiError(503, {
        code: 'SERVICE_UNAVAILABLE', messageKey: 'errors.unknown',
      }));
      renderWithProviders(<Routes><Route path="/u/:handle" element={<PublicProfilePage />} /></Routes>, '/u/testuser');
      fireEvent.click(await screen.findByRole('button', { name: '重试' }));
      await waitFor(() => expect(profileSpy).toHaveBeenCalledTimes(2));
      expect(screen.queryByText('公开主页暂不可用')).not.toBeInTheDocument();
    });

    it('uses only the dark button variant for the selected settings section', async () => {
      vi.spyOn(api, 'getSession').mockResolvedValue(signedIn);
      renderWithProviders(<Routes><Route path="/settings" element={<SettingsLayout />}>
        <Route path="devices" element={<div>Device content</div>} />
      </Route></Routes>, '/settings/devices');
      const selected = await screen.findByRole('link', { name: 'Collector 设备' });
      expect(selected).toHaveAttribute('aria-current', 'page');
      expect(selected).toHaveClass('btn-dark');
      expect(selected).not.toHaveClass('btn-ghost');
      expect(screen.getByRole('link', { name: '个人资料' })).toHaveClass('btn-ghost');
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

    it('renders activity items when authenticated', async () => {
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
        items: [
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
    it('toggles public profile and leaderboard visibility together', async () => {
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

      const updatePrivacySpy = vi.spyOn(api, 'updatePrivacy')
        .mockResolvedValueOnce({
          publicProfileEnabled: true,
          leaderboardVisibility: 'public',
          showBio: false,
          showTokenTotal: false,
          showTrends: false,
          showActivityCalendar: false,
          showAgentBreakdown: false,
          showSkillRanking: false,
          showAchievements: false,
          privacyVersion: 2,
        })
        .mockResolvedValueOnce({
          publicProfileEnabled: false,
          leaderboardVisibility: 'private',
          showBio: false,
          showTokenTotal: false,
          showTrends: false,
          showActivityCalendar: false,
          showAgentBreakdown: false,
          showSkillRanking: false,
          showAchievements: false,
          privacyVersion: 3,
        });

      renderWithProviders(<PrivacySettingsPage />, '/settings/privacy');

      const visibilitySwitch = await screen.findByRole('checkbox', { name: '参加公开排行榜' });
      const saveBtn = screen.getByText('保存');

      fireEvent.click(visibilitySwitch);
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(updatePrivacySpy).toHaveBeenNthCalledWith(
          1,
          expect.objectContaining({
            publicProfileEnabled: true,
            leaderboardVisibility: 'public',
          }),
          1
        );
        expect(getSessionSpy).toHaveBeenCalledTimes(2);
      });

      fireEvent.click(visibilitySwitch);
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(updatePrivacySpy).toHaveBeenNthCalledWith(
          2,
          expect.objectContaining({
            publicProfileEnabled: false,
            leaderboardVisibility: 'private',
          }),
          2
        );
        expect(getSessionSpy).toHaveBeenCalledTimes(3);
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
    it('creates export job and lists jobs from exports array', async () => {
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
          exports: [],
        })
        .mockResolvedValueOnce({
          exports: [
            {
              exportId: 'exp_1',
              jobStatus: 'completed',
              exportScope: 'all_aggregates',
              exportFormat: 'csv',
              createdAt: '2026-08-30T10:00:00Z',
              completedAt: '2026-08-30T10:01:00Z',
              expiresAt: null,
              fileSize: 2048,
            },
          ],
        });

      const createExportSpy = vi.spyOn(api, 'createExport').mockResolvedValue({
        exportId: 'exp_1',
        jobStatus: 'pending',
        exportScope: 'all_aggregates',
        exportFormat: 'csv',
        createdAt: '2026-08-30T10:00:00Z',
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
        expect(screen.getByText(/CSV 导出/)).toBeInTheDocument();
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
        range: { key: '30d', from: '2026-08-01', to: '2026-08-30', timezone: 'UTC' },
        items: [
          { key: 'claude-code', label: 'Claude Code', tokenTotal: '35000000', percentage: 70 },
          { key: 'codex', label: 'Codex', tokenTotal: '15000000', percentage: 30 },
        ],
        aggregationVersion: 1,
      });

      vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({
        skills: [
          { skillId: 'sk_01', skillPublicName: 'code-review', useCount: '150', activeDays: 10 },
        ],
        aggregationVersion: 1,
      });

      vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({
        days: [
          { date: '2026-08-30', tokenTotal: '3000000', level: 4, active: true },
        ],
        currentStreak: 5,
        longestStreak: 10,
        totalActiveDays: 20,
        aggregationVersion: 1,
      });

      vi.spyOn(api, 'getFilterOptions').mockResolvedValue({
        agents: ['claude-code', 'codex'],
        providers: ['anthropic', 'openai'],
        models: ['claude-3-7-sonnet', 'gpt-4o'],
      });

      renderWithProviders(<PersonalDashboardPage />, '/me');

      await waitFor(() => {
        expect(screen.getByRole('heading', { name: '个人数据页' })).toBeInTheDocument();
        expect(screen.getByText('$120.50')).toBeInTheDocument();
        expect(screen.getByText('50.0M')).toBeInTheDocument();
        expect(screen.getByText('Claude Code')).toBeInTheDocument();
        expect(screen.getByText('code-review')).toBeInTheDocument();
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
      vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({ range: { key: '30d', from: '', to: '', timezone: 'UTC' }, items: [], aggregationVersion: 1 });
      vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({ skills: [], aggregationVersion: 1 });
      vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({ days: [], currentStreak: 0, longestStreak: 0, totalActiveDays: 0, aggregationVersion: 1 });
      vi.spyOn(api, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });

      renderWithProviders(<PersonalDashboardPage />, '/me');

      await waitFor(() => {
        expect(screen.getByText('数据加载失败')).toBeInTheDocument();
      });
    });
  });
});
