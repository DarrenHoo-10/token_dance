import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { AuthProvider } from '@/context/AuthContext';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { PersonalAnalyticsProvider, usePersonalAnalytics } from '@/context/PersonalAnalyticsContext';
import { api } from '@/api/client';

function LocationLabel() {
  const { pathname } = useLocation();
  return <div data-testid="path">{pathname}</div>;
}

function Openers() {
  const { show } = usePersonalAnalytics();
  return (
    <>
      <LocationLabel />
      <Routes>
        <Route path="/leaderboard" element={<><h1>平台排行榜</h1><button type="button" onClick={show}>个人数据</button></>} />
        <Route path="/teams/:teamId" element={<><h1>团队面板</h1><button type="button" onClick={show}>个人数据</button></>} />
        <Route path="/me" element={<h1>独立个人页</h1>} />
      </Routes>
    </>
  );
}

function mockSession() {
  vi.spyOn(api, 'getSession').mockResolvedValue({
    authenticated: true,
    user: {
      userId: 'usr_01',
      displayName: 'Test Dev',
      handle: 'testdev',
      avatarUrl: '/api/v1/public/avatars/astronaut',
      locale: 'zh-CN',
      onboardingRequired: false,
      productState: 'active_public',
    },
  });
}

function mockAnalytics(range = 'today') {
  vi.spyOn(api, 'getPersonalSummary').mockResolvedValue({
    range: { key: range, from: '2026-09-19', to: '2026-09-19', timezone: 'Asia/Shanghai' },
    metrics: {
      estimatedCost: { amount: '12.48', currency: 'USD', supported: true },
      totalTokens: { value: '1120000', supported: true },
      generatedCodeLines: { value: '2100', supported: true },
      tokensPerCodeLine: { value: '533', supported: true },
      inputContextTokens: { value: '884000', supported: true },
      outputTokens: { value: '236000', supported: true },
      cacheHitRate: { value: '0.684', supported: true },
      activeDurationMs: { value: '16560000', supported: true },
      messageCount: { value: '328', supported: true },
      userMessageCount: { value: '126', supported: true },
    },
    ranking: { rank: 6, delta: 4, percentile: 80 },
    sync: { lastCommittedAt: new Date().toISOString(), pendingLocalCount: 0 },
    aggregationVersion: 2,
  });
  vi.spyOn(api, 'getTokenTrends').mockImplementation(async (params) => {
    if (params.agent && params.agent !== 'all') return { points: [] };
    return { points: [{ date: '2026-09-19 23:00', tokenTotal: '50000' }], granularity: 'hour' };
  });
  vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({
    items: [{ key: 'codex', label: 'Codex', tokenTotal: '580000', percentage: 52 }],
    aggregationVersion: 1,
  });
  vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({
    skills: [{ skillId: 'sk_1', skillPublicName: 'codex-review', useCount: '24', activeDays: 1 }],
    aggregationVersion: 1,
  });
  vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({
    days: [{ date: '2026-09-19', tokenTotal: '1120000', level: 4, active: true }],
    currentStreak: 12,
    longestStreak: 12,
    totalActiveDays: 18,
    aggregationVersion: 1,
  });
  vi.spyOn(api, 'getFilterOptions').mockResolvedValue({
    agents: ['codex', 'cursor'],
    providers: [],
    models: ['gpt-5.4'],
  });
}

function renderOverlay(route: string) {
  mockSession();
  mockAnalytics();
  return render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <PersonalAnalyticsProvider>
              <Openers />
            </PersonalAnalyticsProvider>
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>,
  );
}

afterEach(() => {
  vi.restoreAllMocks();
  document.body.style.overflow = '';
});

describe('Personal analytics overlay', () => {
  it('opens over the homepage and closes without leaving the page', async () => {
    renderOverlay('/leaderboard');
    expect(screen.getByRole('heading', { name: '平台排行榜' })).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '个人数据' }));
    const dialog = await screen.findByRole('dialog', { name: 'Test Dev，你的创造正在发生。' });
    expect(within(dialog).getByRole('button', { name: '过去 24 小时' })).toHaveAttribute('aria-pressed', 'true');
    expect(within(dialog).getByRole('button', { name: '近 7 天' })).toBeInTheDocument();
    expect(within(dialog).getByRole('img', { name: 'Test Dev' })).toHaveClass('personal-heading-avatar');
    expect(within(dialog).getByText('连续 12 天')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: /2026-09-19/ })).toHaveTextContent('1.1M');
    expect(screen.queryByText('独立个人页')).not.toBeInTheDocument();
    expect(screen.getByTestId('path')).toHaveTextContent('/leaderboard');
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('heading', { name: '平台排行榜' })).toBeInTheDocument();
    expect(screen.getByTestId('path')).toHaveTextContent('/leaderboard');
  });

  it('opens over a team page and keeps the team route after close', async () => {
    renderOverlay('/teams/tem_demo');
    fireEvent.click(screen.getByRole('button', { name: '个人数据' }));
    expect(await screen.findByRole('dialog', { name: 'Test Dev，你的创造正在发生。' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '团队面板' })).toBeInTheDocument();
    expect(screen.getByTestId('path')).toHaveTextContent('/teams/tem_demo');
    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(screen.getByRole('heading', { name: '团队面板' })).toBeInTheDocument();
    expect(screen.getByTestId('path')).toHaveTextContent('/teams/tem_demo');
  });

  it('changes range, filters the chart, and resets empty filters inside the overlay', async () => {
    renderOverlay('/leaderboard');
    fireEvent.click(screen.getByRole('button', { name: '个人数据' }));
    const dialog = await screen.findByRole('dialog', { name: 'Test Dev，你的创造正在发生。' });
    fireEvent.click(within(dialog).getByRole('button', { name: '近 7 天' }));
    await waitFor(() => expect(api.getPersonalSummary).toHaveBeenCalledWith('7d'));
    fireEvent.change(within(dialog).getByLabelText('趋势 Agent 筛选'), { target: { value: 'cursor' } });
    expect(await within(dialog).findByText('没有符合筛选的记录')).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole('button', { name: /重置筛选/ }));
    await waitFor(() => expect(within(dialog).queryByText('没有符合筛选的记录')).not.toBeInTheDocument());
    expect(api.getPersonalSummary).toHaveBeenCalled();
  });

  it('keeps export on the overlay and shows an error state when summary fails', async () => {
    renderOverlay('/leaderboard');
    const createExport = vi.spyOn(api, 'createExport').mockResolvedValue({
      exportId: 'exp_1',
      exportScope: 'all_aggregates',
      exportFormat: 'csv',
      jobStatus: 'pending',
      createdAt: '2026-09-19T00:00:00Z',
    });
    fireEvent.click(screen.getByRole('button', { name: '个人数据' }));
    const dialog = await screen.findByRole('dialog', { name: 'Test Dev，你的创造正在发生。' });
    fireEvent.click(within(dialog).getByRole('button', { name: '导出数据' }));
    await waitFor(() => expect(createExport).toHaveBeenCalled());
    expect(screen.getByTestId('path')).toHaveTextContent('/leaderboard');
    expect(screen.queryByText('独立个人页')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: '关闭' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: '个人数据' }));
    const reopened = await screen.findByRole('dialog', { name: 'Test Dev，你的创造正在发生。' });
    expect(within(reopened).getByRole('button', { name: '过去 24 小时' })).toBeInTheDocument();
    expect(screen.queryByText('数据加载失败')).not.toBeInTheDocument();
  });
});
