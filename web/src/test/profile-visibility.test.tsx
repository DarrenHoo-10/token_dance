import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { AuthProvider } from '@/context/AuthContext';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { PersonalDashboardPage } from '@/pages/me/PersonalDashboardPage';
import { api, ApiError } from '@/api/client';

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

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

function mockAuthenticatedDashboard() {
  vi.spyOn(api, 'getSession').mockResolvedValue({
    authenticated: true,
    user: {
      userId: 'usr_01',
      displayName: 'Test Dev',
      handle: 'testdev',
      avatarUrl: null,
      locale: 'zh-CN',
      onboardingRequired: false,
      productState: 'active_public',
    },
  });
  vi.spyOn(api, 'getPersonalSummary').mockResolvedValue({
    range: { key: 'today', from: '2026-08-30', to: '2026-08-30', timezone: 'UTC' },
    metrics: {
      estimatedCost: { amount: '1.00', currency: 'USD', supported: true },
      totalTokens: { value: '1000', supported: true },
      generatedCodeLines: { value: '10', supported: true },
      tokensPerCodeLine: { value: '100', supported: true },
      inputContextTokens: { value: '600', supported: true },
      outputTokens: { value: '400', supported: true },
      cacheHitRate: { value: '0.1', supported: true },
      activeDurationMs: { value: '1000', supported: true },
      messageCount: { value: '5', supported: true },
      userMessageCount: { value: '2', supported: true },
    },
    ranking: { visibility: 'public', rank: 1, delta: 0, percentile: 99 },
    sync: { lastCommittedAt: '2026-08-30T15:00:00Z', pendingLocalCount: null },
    dataWatermarkAt: '2026-08-30T15:00:00Z',
    aggregationVersion: 2,
  });
  vi.spyOn(api, 'getTokenTrends').mockResolvedValue({ points: [] });
  vi.spyOn(api, 'getAgentBreakdowns').mockResolvedValue({
    range: { key: '30d', from: '2026-08-01', to: '2026-08-30', timezone: 'UTC' },
    items: [],
    aggregationVersion: 1,
  });
  vi.spyOn(api, 'getPersonalSkills').mockResolvedValue({ skills: [], aggregationVersion: 1 });
  vi.spyOn(api, 'getActivityCalendar').mockResolvedValue({
    days: [],
    currentStreak: 0,
    longestStreak: 0,
    totalActiveDays: 0,
    aggregationVersion: 1,
  });
  vi.spyOn(api, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
}

describe('Personal data visibility', () => {
  it('does not show the public-data toggle on the personal dashboard', async () => {
    mockAuthenticatedDashboard();
    const getPrivacy = vi.spyOn(api, 'getPrivacy');

    renderWithProviders(<PersonalDashboardPage />, '/me');

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '个人数据页' })).toBeInTheDocument();
    });

    expect(screen.queryByRole('checkbox', { name: '公开我的数据' })).not.toBeInTheDocument();
    expect(screen.queryByText('公开我的数据')).not.toBeInTheDocument();
    expect(screen.queryByText(/开启后其他人可查看你的详细资料页/)).not.toBeInTheDocument();
    expect(getPrivacy).not.toHaveBeenCalled();
  });

  it('still loads personal metrics when privacy APIs are unused', async () => {
    mockAuthenticatedDashboard();
    vi.spyOn(api, 'getPrivacy').mockRejectedValue(
      new ApiError(500, { code: 'HTTP_500', messageKey: 'errors.http_500' }),
    );

    renderWithProviders(<PersonalDashboardPage />, '/me');

    await waitFor(() => {
      expect(screen.getByRole('heading', { name: '个人数据页' })).toBeInTheDocument();
    });
    expect(screen.queryByText(/公开状态暂时无法读取/)).not.toBeInTheDocument();
  });
});
