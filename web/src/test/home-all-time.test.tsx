import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { api } from '@/api/client';
import type { PersonalSummary } from '@/types/api';

vi.mock('@/context/AuthContext', () => ({
  useAuth: () => ({ authenticated: true, user: { userId: 'usr_totals', handle: 'totals' } }),
}));

function summary(range: string | undefined, tokens: string): PersonalSummary {
  const unsupported = { value: null, supported: false };
  return {
    range: { key: range ?? '30d', from: '', to: '', timezone: 'UTC' },
    metrics: {
      totalTokens: { value: tokens, supported: true },
      estimatedCost: { amount: null, currency: 'USD', supported: false },
      generatedCodeLines: unsupported, tokensPerCodeLine: unsupported,
      inputContextTokens: unsupported, outputTokens: unsupported,
      cacheHitRate: unsupported, activeDurationMs: unsupported,
      messageCount: unsupported, userMessageCount: unsupported,
    },
    ranking: { rank: null, percentile: null },
    sync: { lastCommittedAt: null, pendingLocalCount: null },
    aggregationVersion: 1,
  };
}

function renderHome() {
  return render(<LocaleProvider><MemoryRouter><LeaderboardPage /></MemoryRouter></LocaleProvider>);
}

function allTimeBlock() {
  return within(screen.getByText('累计 Token · All time').closest('.stat-block') as HTMLElement);
}

describe('Home all-time usage', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    localStorage.clear();
    vi.spyOn(api, 'getLeaderboard').mockResolvedValue({
      snapshotId: 'empty', boardKey: 'global', window: 'today', metric: 'tokens', entries: [],
    });
    vi.spyOn(api, 'getMyLeaderboard').mockImplementation((params) => api.getLeaderboard(params));
    vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({
      days: [], currentStreak: 0, longestStreak: 0, totalActiveDays: 0, aggregationVersion: 1,
    });
    vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({
      range: { key: 'today', from: '', to: '', timezone: 'UTC' }, items: [], aggregationVersion: 1,
    });
  });

  it('shows historical totals separately from today using range=all', async () => {
    const request = vi.spyOn(api, 'getPersonalSummary').mockImplementation(async range =>
      summary(range, range === 'all' ? '3280000000' : '19170000'));
    renderHome();
    await waitFor(() => expect(allTimeBlock().getByText('3.3B')).toBeInTheDocument());
    expect(screen.getByText('19.2M')).toBeInTheDocument();
    expect(request).toHaveBeenCalledWith('all');
    expect(request).toHaveBeenCalledWith('today');
  });

  it('keeps today visible if the historical request fails', async () => {
    vi.spyOn(api, 'getPersonalSummary').mockImplementation(async range => {
      if (range === 'all') throw new Error('History unavailable');
      return summary(range, '19170000');
    });
    renderHome();
    await waitFor(() => expect(screen.getByText('19.2M')).toBeInTheDocument());
    expect(allTimeBlock().getByText('—')).toBeInTheDocument();
  });

  it('distinguishes a recorded zero from unavailable history', async () => {
    vi.spyOn(api, 'getPersonalSummary').mockImplementation(async range => summary(range, '0'));
    renderHome();
    await waitFor(() => expect(allTimeBlock().getByText('0')).toBeInTheDocument());
  });
});
