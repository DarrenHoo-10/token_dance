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
    vi.spyOn(api, 'getLeaderboard').mockResolvedValue({
      snapshotId: '',
      boardKey: 'global',
      window: '7d',
      metric: 'tokens',
      entries: [
        { rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue: '200', rankDelta: 0 },
        { rankNo: 2, handle: 'grace', displayName: 'Grace', avatarUrl: null, metricValue: '100', rankDelta: 0 },
        { rankNo: 3, handle: 'linus', displayName: 'Linus', avatarUrl: null, metricValue: '50', rankDelta: 0 },
      ],
    });
    vi.spyOn(api, 'getMyLeaderboard');
    vi.spyOn(api, 'getCommunityStats').mockResolvedValue({ metricDate: '2026-09-09', timezone: 'UTC', window: '7d' });
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

  it('lets guests open a podium builder without signing in', async () => {
    showHome();
    expect(await screen.findByRole('heading', { name: 'Ada的创作轨迹' })).toBeInTheDocument();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: '30d' }));
    expect(api.getMyLeaderboard).not.toHaveBeenCalled();
    expect(screen.queryByText('登录，留下你的足迹')).not.toBeInTheDocument();
    expect(screen.queryByText('登录后查看用量趋势与活跃记录。')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看 Linus 的公开创作轨迹' }));
    expect(await screen.findByRole('heading', { name: 'Linus的创作轨迹' })).toBeInTheDocument();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('linus', { range: '30d' }));
    expect(screen.getByRole('link', { name: '公开资料' })).toHaveAttribute('href', '/u/linus');
  });

  it('shows an empty-range state instead of a login wall', async () => {
    vi.mocked(api.getPublicTokenTrends).mockResolvedValue({ visible: true, points: [] });
    showHome();
    expect(await screen.findByText('这个范围里，还没有创作记录。')).toBeInTheDocument();
    expect(screen.queryByText('该开发者尚未公开用量趋势。')).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '登录，留下你的足迹' })).not.toBeInTheDocument();
  });
});
