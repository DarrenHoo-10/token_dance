import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider } from '@/context/AuthContext';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { PersonalAnalyticsProvider } from '@/context/PersonalAnalyticsContext';
import { TeamProvider } from '@/context/TeamContext';
import { Navbar } from '@/components/layout/Navbar';
import { SettingsLayout } from '@/components/layout/SettingsLayout';
import { PersonalDashboardPage } from '@/pages/me/PersonalDashboardPage';
import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { CreateTeamPage } from '@/pages/teams/CreateTeamPage';
import { TeamLayout } from '@/pages/teams/TeamLayout';
import { api } from '@/api/client';

function LocationLabel() {
  const location = useLocation();
  return <div data-testid="current-path">{location.pathname + location.search}</div>;
}

function renderGuest(ui: React.ReactElement, route: string) {
  vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
  return render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <TeamProvider>
              <PersonalAnalyticsProvider>
                <LocationLabel />
                {ui}
              </PersonalAnalyticsProvider>
            </TeamProvider>
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>,
  );
}

function mockPublicHome() {
  vi.spyOn(api, 'getLeaderboard').mockResolvedValue({
    snapshotId: '',
    boardKey: 'global',
    window: 'today',
    metric: 'tokens',
    entries: [
      { rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue: '200', rankDelta: 0 },
      { rankNo: 2, handle: 'grace', displayName: 'Grace', avatarUrl: null, metricValue: '100', rankDelta: 0 },
      { rankNo: 3, handle: 'linus', displayName: 'Linus', avatarUrl: null, metricValue: '50', rankDelta: 0 },
    ],
  });
  vi.spyOn(api, 'getMyLeaderboard');
  vi.spyOn(api, 'getCommunityStats').mockImplementation(async (window) => ({
    metricDate: '2026-09-09',
    timezone: 'UTC',
    window,
  }));
  vi.spyOn(api, 'getPublicTokenTrends').mockResolvedValue({
    visible: true,
    points: [{ date: '2026-09-19', tokenTotal: '200' }],
  });
  vi.spyOn(api, 'getPublicProfile').mockResolvedValue({
    handle: 'ada',
    displayName: 'Ada',
    avatarUrl: null,
    generatedAt: '2026-09-19T00:00:00Z',
    projectionVersion: 1,
    currentStreak: 3,
  });
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.style.overflow = '';
  localStorage.clear();
});

describe('Guest access without a site-wide login wall', () => {
  it('keeps /me on the page and shows the shared login prompt', async () => {
    renderGuest(
      <Routes>
        <Route path="/me" element={<PersonalDashboardPage />} />
        <Route path="/login" element={<div>login-page</div>} />
        <Route path="/leaderboard" element={<h1>平台排行榜</h1>} />
      </Routes>,
      '/me',
    );

    expect(await screen.findByText('需要登录')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '前往登录' })).toBeInTheDocument();
    expect(screen.getByTestId('current-path')).toHaveTextContent('/me');
    expect(screen.queryByText('login-page')).not.toBeInTheDocument();
  });

  it('shows the shared login prompt on settings instead of jumping to /login', async () => {
    renderGuest(
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="exports" element={<div>export-settings</div>} />
        </Route>
        <Route path="/login" element={<div>login-page</div>} />
      </Routes>,
      '/settings/exports',
    );

    expect(await screen.findByText('需要登录')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '前往登录' })).toBeInTheDocument();
    expect(screen.getByTestId('current-path')).toHaveTextContent('/settings/exports');
    expect(screen.queryByText('export-settings')).not.toBeInTheDocument();
    expect(screen.queryByText('login-page')).not.toBeInTheDocument();
  });

  it('opens personal analytics as an in-page login prompt and stays on the leaderboard', async () => {
    mockPublicHome();
    renderGuest(
      <Routes>
        <Route path="/leaderboard" element={<LeaderboardPage />} />
        <Route path="/login" element={<div>login-page</div>} />
      </Routes>,
      '/leaderboard',
    );

    expect(await screen.findByRole('heading', { name: 'Ada的创作轨迹' })).toBeInTheDocument();
    expect(api.getMyLeaderboard).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '打开个人数据' }));
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('需要登录');
    expect(dialog).toHaveTextContent('该页面仅限登录用户访问，请先登录或注册。');
    expect(screen.getByRole('button', { name: '前往登录' })).toBeInTheDocument();
    expect(screen.getByTestId('current-path')).toHaveTextContent('/leaderboard');
    expect(screen.queryByText('login-page')).not.toBeInTheDocument();
  });

  it('opens the same login prompt from the navbar analytics button', async () => {
    renderGuest(
      <>
        <Navbar />
        <Routes>
          <Route path="/docs" element={<h1>使用文档</h1>} />
          <Route path="/login" element={<div>login-page</div>} />
        </Routes>
      </>,
      '/docs',
    );

    fireEvent.click(await screen.findByRole('button', { name: '个人数据页' }));
    expect(await screen.findByText('需要登录')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '前往登录' })).toBeInTheDocument();
    expect(screen.getByTestId('current-path')).toHaveTextContent('/docs');
    expect(screen.queryByText('login-page')).not.toBeInTheDocument();
  });

  it('prompts login on create-team and team workspace without leaving the route', async () => {
    renderGuest(
      <Routes>
        <Route path="/teams/new" element={<CreateTeamPage />} />
        <Route path="/teams/:teamId" element={<TeamLayout />} />
        <Route path="/login" element={<div>login-page</div>} />
      </Routes>,
      '/teams/new',
    );

    expect(await screen.findByText('需要登录')).toBeInTheDocument();
    expect(screen.getByTestId('current-path')).toHaveTextContent('/teams/new');
    expect(screen.queryByText('login-page')).not.toBeInTheDocument();
  });

  it('prompts login on a team workspace URL without a hard redirect', async () => {
    renderGuest(
      <Routes>
        <Route path="/teams/:teamId" element={<TeamLayout />} />
        <Route path="/login" element={<div>login-page</div>} />
      </Routes>,
      '/teams/tem_demo',
    );

    expect(await screen.findByText('需要登录')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '前往登录' })).toBeInTheDocument();
    expect(screen.getByTestId('current-path')).toHaveTextContent('/teams/tem_demo');
    expect(screen.queryByText('login-page')).not.toBeInTheDocument();
  });
});
