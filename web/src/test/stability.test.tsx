import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { api, SESSION_EXPIRED_EVENT } from '@/api/client';
import { teamsApi } from '@/api/teams';
import { AuthProvider, useAuth } from '@/context/AuthContext';
import { LocaleProvider, useLocale } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { PersonalAnalyticsProvider } from '@/context/PersonalAnalyticsContext';
import { TeamProvider } from '@/context/TeamContext';
import { ErrorBoundary } from '@/components/common/ErrorBoundary';
import { PersonalAnalytics } from '@/pages/me/PersonalAnalytics';
import { safeLocalStorage, safeSessionStorage } from '@/utils/safeStorage';
import { readJoinToken, writeJoinToken } from '@/pages/teams/teamUtils';

const user = {
  userId: 'usr_01', displayName: 'Test Dev', handle: 'testdev', avatarUrl: null,
  locale: 'zh-CN' as const, onboardingRequired: false, productState: 'active_public' as const,
};

function summaryFor(tokens: string) {
  return {
    range: { key: 'today', from: '2026-09-19', to: '2026-09-19', timezone: 'Asia/Shanghai' },
    metrics: { totalTokens: { value: tokens, supported: true } },
    ranking: { rank: null, delta: null, percentile: null },
    sync: { lastCommittedAt: new Date().toISOString(), pendingLocalCount: 0 },
    aggregationVersion: 2,
  } as never;
}

function stubAnalyticsApi() {
  vi.spyOn(api, 'getTokenTrends').mockResolvedValue({ points: [] });
  vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({ items: [], aggregationVersion: 1 });
  vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({ skills: [], aggregationVersion: 1 });
  vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({ days: [], currentStreak: 0, longestStreak: 0, totalActiveDays: 0, aggregationVersion: 1 });
  vi.spyOn(api, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
}

function withProviders(ui: React.ReactNode, route = '/leaderboard') {
  return (
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={[route]} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            {ui}
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>
  );
}

function blockWebStorage() {
  const fail = () => { throw new DOMException('denied', 'SecurityError'); };
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(fail);
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(fail);
  vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(fail);
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe('blocked browser storage', () => {
  it('keeps values in memory when Web Storage throws', () => {
    blockWebStorage();
    safeLocalStorage.setItem('k', 'v');
    expect(safeLocalStorage.getItem('k')).toBe('v');
    safeLocalStorage.removeItem('k');
    expect(safeLocalStorage.getItem('k')).toBeNull();
  });

  it('still renders and switches language', () => {
    blockWebStorage();
    function Probe() {
      const { locale, setLocale } = useLocale();
      return <button onClick={() => setLocale('en-US')}>{locale}</button>;
    }
    render(<LocaleProvider><Probe /></LocaleProvider>);
    fireEvent.click(screen.getByText('zh-CN'));
    expect(screen.getByText('en-US')).toBeInTheDocument();
  });

  it('keeps team join tokens usable for the current page', () => {
    blockWebStorage();
    writeJoinToken('lnk_1', 'secret');
    expect(readJoinToken('lnk_1')).toBe('secret');
    safeSessionStorage.removeItem('anything');
  });
});

describe('ErrorBoundary', () => {
  it('shows a recovery page instead of a blank screen', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {});
    const Broken = () => { throw new Error('boom'); };
    render(<ErrorBoundary><Broken /></ErrorBoundary>);
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /刷新页面|Reload page/ })).toBeInTheDocument();
  });
});

describe('background requests', () => {
  it('does not fetch or poll team data outside /teams pages', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    const getMyTeam = vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue({ team: null } as never);
    render(withProviders(<TeamProvider><div>home</div></TeamProvider>, '/leaderboard'));
    await screen.findByText('home');
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
    expect(getMyTeam).not.toHaveBeenCalled();
  });

  it('loads team data on /teams pages', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    const getMyTeam = vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue({ team: null } as never);
    render(withProviders(<TeamProvider><div>teams</div></TeamProvider>, '/teams'));
    await waitFor(() => expect(getMyTeam).toHaveBeenCalled());
  });

  it('does not request personal analytics until the dialog is opened', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    stubAnalyticsApi();
    const summary = vi.spyOn(api, 'getPersonalSummary').mockResolvedValue(summaryFor('100'));
    render(withProviders(<PersonalAnalyticsProvider><div>docs</div></PersonalAnalyticsProvider>, '/docs/quickstart'));
    await screen.findByText('docs');
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
    expect(summary).not.toHaveBeenCalled();
  });
});

describe('personal analytics period switching', () => {
  it('keeps the newest period when an older request finishes last', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    stubAnalyticsApi();
    const pending = new Map<string, (value: never) => void>();
    vi.spyOn(api, 'getPersonalSummary').mockImplementation((range?: string) => {
      if (range === 'today') return Promise.resolve(summaryFor('1000000'));
      return new Promise((resolve) => { pending.set(range ?? '', resolve as (value: never) => void); });
    });
    render(withProviders(<PersonalAnalytics />, '/me'));
    await screen.findByText('1.00');
    fireEvent.click(screen.getByRole('button', { name: '近 7 天' }));
    fireEvent.click(screen.getByRole('button', { name: '近 30 天' }));
    await waitFor(() => expect(pending.size).toBe(2));
    await act(async () => { pending.get('30d')!(summaryFor('3000000')); });
    await act(async () => { pending.get('7d')!(summaryFor('7000000')); });
    const total = document.querySelector('[data-metric="totalTokens"]')!;
    expect(total.textContent).toContain('3.00');
    expect(total.textContent).not.toContain('7.00');
  });

  it('tells the user when a refresh fails and old figures are still shown', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    stubAnalyticsApi();
    vi.spyOn(api, 'getPersonalSummary').mockImplementation(async (range?: string) => {
      if (range === 'today') return summaryFor('1000000');
      throw new Error('offline');
    });
    render(withProviders(<PersonalAnalytics />, '/me'));
    await screen.findByText('1.00');
    fireEvent.click(screen.getByRole('button', { name: '近 7 天' }));
    expect(await screen.findByText(/刷新失败/)).toBeInTheDocument();
  });
});

describe('expired sessions', () => {
  it('signs the user out when a signed-in request returns 401', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    function Probe() {
      const { authenticated, loading } = useAuth();
      return <div>{loading ? 'loading' : authenticated ? 'signed-in' : 'signed-out'}</div>;
    }
    render(withProviders(<Probe />));
    await screen.findByText('signed-in');
    fetchMock(401);
    await expect(api.getPersonalSummary('today')).rejects.toMatchObject({ status: 401 });
    expect(await screen.findByText('signed-out')).toBeInTheDocument();
  });

  it('ignores 401 from the sign-in endpoints themselves', async () => {
    const listener = vi.fn();
    window.addEventListener(SESSION_EXPIRED_EVENT, listener);
    fetchMock(401);
    await expect(api.login({ email: 'a@b.c', password: 'x' } as never)).rejects.toMatchObject({ status: 401 });
    window.removeEventListener(SESSION_EXPIRED_EVENT, listener);
    expect(listener).not.toHaveBeenCalled();
  });
});

function fetchMock(status: number) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(
    JSON.stringify({ error: { code: 'UNAUTHENTICATED', messageKey: 'errors.http_401' } }),
    { status, headers: { 'Content-Type': 'application/json' } },
  )));
}
