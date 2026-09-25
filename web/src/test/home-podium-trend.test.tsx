import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { api } from '@/api/client';

const auth = { authenticated: false, user: null };
vi.mock('@/context/AuthContext', () => ({ useAuth: () => auth }));

function showHome() {
  return render(
    <LocaleProvider>
      <MemoryRouter>
        <LeaderboardPage />
      </MemoryRouter>
    </LocaleProvider>,
  );
}

describe('Homepage podium public trends', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    auth.authenticated = false;
    auth.user = null;
    vi.spyOn(api, 'getLeaderboard').mockImplementation(async (params) => ({
      snapshotId: '',
      boardKey: 'global',
      window: params.window ?? 'today',
      metric: 'tokens',
      entries: [
        { rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue: '200', rankDelta: 0 },
        { rankNo: 2, handle: 'grace', displayName: 'Grace', avatarUrl: null, metricValue: '100', rankDelta: 0 },
        { rankNo: 3, handle: 'linus', displayName: 'Linus', avatarUrl: null, metricValue: '50', rankDelta: 0 },
      ],
    }));
    vi.spyOn(api, 'getMyLeaderboard');
    vi.spyOn(api, 'getCommunityStats').mockImplementation(async (window) => ({ metricDate: '2026-09-09', timezone: 'UTC', window }));
    vi.spyOn(api, 'getPublicTokenTrends').mockImplementation(async (handle) => ({
      visible: true,
      points: handle === 'linus'
        ? [{ date: '2026-09-19', tokenTotal: '50' }]
        : [{ date: '2026-09-19', tokenTotal: '200' }],
    }));
    vi.spyOn(api, 'getPublicProfile').mockResolvedValue({
      handle: 'ada',
      displayName: 'Ada',
      avatarUrl: null,
      generatedAt: '2026-09-19T00:00:00Z',
      projectionVersion: 1,
      currentStreak: 3,
    });
  });

  it('shows zero-usage accounts as unranked and excludes them from the podium', async () => {
    auth.authenticated = true;
    vi.mocked(api.getMyLeaderboard).mockResolvedValue({
      snapshotId: '', boardKey: 'global', window: 'today', metric: 'tokens',
      entries: [{ rankNo: 1, handle: 'new', displayName: 'New', avatarUrl: null, metricValue: '0', rankDelta: 4 }],
    });
    vi.spyOn(api, 'getPersonalSummary').mockResolvedValue({
      metrics: { totalTokens: { value: '0', supported: true } },
      ranking: { rank: 1, delta: 4, percentile: 10 },
    } as never);
    vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({ days: [] } as never);
    showHome();
    await waitFor(() => expect(screen.getAllByText('暂未上榜')).toHaveLength(2));
    expect(screen.getByText('本周期暂无有效用量，暂未有人上榜。')).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '查看 New 的公开创作轨迹' })).not.toBeInTheDocument();
    expect(screen.queryByText('前 10%')).not.toBeInTheDocument();
  });

  it('lets guests open a podium builder without signing in', async () => {
    showHome();
    expect(await screen.findByRole('heading', { name: 'Ada的创作轨迹' })).toBeInTheDocument();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: 'today' }));
    expect(api.getMyLeaderboard).not.toHaveBeenCalled();
    expect(screen.queryByText('登录，留下你的足迹')).not.toBeInTheDocument();
    expect(screen.queryByText('登录后查看用量趋势与活跃记录。')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看 Linus 的公开创作轨迹' }));
    expect(await screen.findByRole('heading', { name: 'Linus的创作轨迹' })).toBeInTheDocument();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('linus', { range: 'today' }));
    expect(screen.getByRole('link', { name: '公开资料' })).toHaveAttribute('href', '/u/linus');
  });

  it('shows an empty-range state instead of a login wall', async () => {
    vi.mocked(api.getPublicTokenTrends).mockResolvedValue({ visible: true, points: [] });
    showHome();
    expect(await screen.findByText('这个范围里，还没有创作记录。')).toBeInTheDocument();
    expect(screen.queryByText('该开发者尚未公开用量趋势。')).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '登录，留下你的足迹' })).not.toBeInTheDocument();
  });

  it('shows all hourly buckets when the public trend has no usage', async () => {
    vi.mocked(api.getPublicTokenTrends).mockResolvedValue({
      visible: true, points: [],
      range: { key: 'today', from: '2026-09-19T15:00:00Z', to: '2026-09-20T14:42:00Z', timezone: 'Asia/Shanghai' },
    });
    showHome();
    await waitFor(() => expect(screen.getByRole('slider', { name: '选择趋势日期' })).toHaveAttribute('max', '23'));
    expect(screen.getByRole('slider', { name: '选择趋势日期' })).toHaveAttribute('aria-valuetext', '2026-09-20 22:00: 0 Token');
    expect(screen.queryByText('这个范围里，还没有创作记录。')).not.toBeInTheDocument();
  });

  it('keeps the trajectory period in sync with the leaderboard period', async () => {
    showHome();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: 'today' }));
    expect(screen.queryByRole('combobox', { name: '用量趋势周期' })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('tab', { name: '近 7 天' }));
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: '7d' }));

    fireEvent.click(screen.getByRole('tab', { name: '近 30 天' }));
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: '30d' }));

    fireEvent.click(screen.getByRole('tab', { name: '全部时间' }));
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: 'all' }));
  });

  it('does not wait for or reload the profile when switching trend periods', async () => {
    vi.mocked(api.getPublicProfile).mockImplementation(() => new Promise(() => {}));
    showHome();
    await waitFor(() => expect(screen.getByRole('slider', { name: '选择趋势日期' })).toBeInTheDocument());

    fireEvent.click(screen.getByRole('tab', { name: '近 7 天' }));
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: '7d' }));
    await waitFor(() => expect(screen.getByRole('slider', { name: '选择趋势日期' })).toBeInTheDocument());
    expect(api.getPublicProfile).toHaveBeenCalledTimes(1);
  });

  it('reuses a recently loaded trend when revisiting a period', async () => {
    showHome();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: 'today' }));

    fireEvent.click(screen.getByRole('tab', { name: '近 7 天' }));
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: '7d' }));
    await waitFor(() => expect(screen.getByRole('slider', { name: '选择趋势日期' })).toBeInTheDocument());

    fireEvent.click(screen.getByRole('tab', { name: '过去 24 小时' }));
    fireEvent.click(screen.getByRole('tab', { name: '近 7 天' }));
    await waitFor(() => expect(screen.getByRole('slider', { name: '选择趋势日期' })).toBeInTheDocument());
    expect(vi.mocked(api.getPublicTokenTrends).mock.calls.filter(([, params]) => params?.range === '7d')).toHaveLength(1);
    expect(api.getPublicProfile).toHaveBeenCalledTimes(1);
  });
});
