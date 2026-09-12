import { describe, it, expect, beforeEach, vi } from 'vitest';
import { act, fireEvent, renderHook, screen, waitFor } from '@testing-library/react';
import { api } from '@/api/client';
import { teamsApi, type TeamAnalysisReady, type TeamAnalysisUpdating } from '@/api/teams';
import { TeamAnalyticsPage } from '@/pages/teams/TeamAnalyticsPage';
import { TeamOverviewPage } from '@/pages/teams/TeamOverviewPage';
import { TeamMembersPage } from '@/pages/teams/TeamMembersPage';
import { useTeamAnalysis } from '@/pages/teams/useTeamAnalysis';
import { renderTeams, sampleScope, signedInUser } from './teams-test-helpers';

vi.mock('react-router-dom', async (importOriginal) => ({
  ...await importOriginal<typeof import('react-router-dom')>(),
  useOutletContext: () => ({ openInvite: () => undefined }),
}));

const readyAnalysis = (authRevision: string, tokenValue: string): TeamAnalysisReady => ({
  state: 'ready',
  snapshot: {
    id: 'tas_01',
    authRevision,
    sourceRevision: '1',
    ruleVersion: '1',
    asOf: '2026-09-06T08:00:00.000Z',
    refreshing: false,
  },
  range: {
    timezone: 'Asia/Shanghai',
    from: '2026-08-30T16:00:00.000Z',
    toExclusive: '2026-09-06T16:00:00.000Z',
    dataToExclusive: '2026-09-06T08:00:00.000Z',
  },
  filters: { agent: null, provider: null, model: null },
  summary: {
    tokens: { value: tokenValue, state: 'available' },
    activeMembers: '3',
    currentMembers: '5',
    currentSharingMembers: '4',
    comparison: null,
    comparisonReason: 'insufficient_history',
  },
  costs: {
    reported: [{ currency: 'USD', amount: '12.34000000' }],
    estimatedUncovered: [],
    coverage: { reportedUsageEvents: '40', eligibleUsageEvents: '100' },
    unattributedCostCount: '0',
  },
  trend: [{ date: '2026-09-05', tokens: { value: tokenValue, state: 'available' } }],
  agents: { items: [], nextCursor: null },
  models: { items: [], nextCursor: null },
  contributions: { items: [], nextCursor: null },
  quality: { unsupportedEvents: '0', estimatedEvents: '0' },
});

describe('Team analysis updating state', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
    vi.spyOn(teamsApi, 'getMembers').mockResolvedValue({ members: [], nextCursor: null });
    vi.spyOn(teamsApi, 'getInvitations').mockResolvedValue({ invitations: [], nextCursor: null });
    vi.spyOn(teamsApi, 'getInviteLinks').mockResolvedValue({ links: [], nextCursor: null });
  });

  it.each([
    ['overview', <TeamOverviewPage />, ''],
    ['analytics', <TeamAnalyticsPage />, '/analytics'],
    ['members', <TeamMembersPage />, '/members'],
  ] as const)('keeps custom dates editable on a %s deep link without sending an incomplete query', async (_name, page, suffix) => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    const query = vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    renderTeams(page, `/teams/tem_0123456789abcdefghijklmnop${suffix}?range=custom`);
    const from = await screen.findByLabelText('开始日期');
    const to = screen.getByLabelText('结束日期');
    expect(screen.getByText('请选择开始日期和结束日期，选好后自动查询。')).toBeInTheDocument();
    expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument();
    expect(query).not.toHaveBeenCalled();
    fireEvent.change(from, { target: { value: '2026-09-01' } });
    expect(query).not.toHaveBeenCalled();
    fireEvent.change(to, { target: { value: '2026-09-06' } });
    await waitFor(() => expect(query).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ range: 'custom', from: '2026-09-01', to: '2026-09-06' }), expect.any(AbortSignal)));
    await waitFor(() => expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument());
    fireEvent.change(screen.getByLabelText('开始日期'), { target: { value: '' } });
    expect(screen.getByText('请选择开始日期和结束日期，选好后自动查询。')).toBeInTheDocument();
    expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '7 天' }));
    await waitFor(() => expect(query).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ range: '7d' }), expect.any(AbortSignal)));
  });

  it.each([
    ['overview', <TeamOverviewPage />, ''],
    ['analytics', <TeamAnalyticsPage />, '/analytics'],
    ['members', <TeamMembersPage />, '/members'],
  ] as const)('discloses legacy summary limits on %s', async (_name, page, suffix) => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    const result = readyAnalysis('1', '120000');
    result.quality.hasLegacyAggregates = true;
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeams(page, `/teams/tem_0123456789abcdefghijklmnop${suffix}?range=7d`);
    expect(await screen.findByText(/包含历史日汇总/)).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '7 天' })).toHaveAttribute('aria-selected', 'true');
  });

  it('shows a skeleton and never fakes 0 while the snapshot is updating', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope({ team: { ...sampleScope().team, authRevision: '13' } }));
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue({
      state: 'updating',
      authRevision: '13',
      retryAfterMs: 30_000,
      messageKey: 'teams.analytics.updating',
    } satisfies TeamAnalysisUpdating);
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });

    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop/analytics');

    expect(await screen.findByTestId('analysis-skeleton')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '今天' })).toHaveAttribute('aria-selected', 'true');
    expect(teamsApi.getAnalysis).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ range: 'today' }), expect.any(AbortSignal));
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['今天', '7 天', '30 天', '自定义']);
    expect(screen.getByRole('tab', { name: '自定义' })).toBeInTheDocument();
    expect(screen.getByText('正在汇总团队数据，请稍候…')).toBeInTheDocument();
    expect(screen.queryByText('0')).not.toBeInTheDocument();
    expect(screen.queryByText('$0')).not.toBeInTheDocument();
    expect(screen.queryByText('120,000')).not.toBeInTheDocument();
  });

  it('clears previous totals when authRevision changes', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    const scope = sampleScope();
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(scope);
    const analysisSpy = vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });

    const view = renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop/analytics');
    expect(await screen.findByText('120.0K')).toBeInTheDocument();

    analysisSpy.mockResolvedValue({
      state: 'updating',
      authRevision: '13',
      retryAfterMs: 30_000,
      messageKey: 'teams.analytics.authChanged',
    });
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue({
      ...scope,
      team: { ...scope.team, authRevision: '13' },
    });

    view.unmount();
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop/analytics');

    expect(await screen.findByTestId('analysis-skeleton')).toBeInTheDocument();
    await waitFor(() => {
      expect(screen.queryByText('120.0K')).not.toBeInTheDocument();
    });
  });

  it('keeps polling after the first ready snapshot so later usage can appear', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'setInterval'] });
    const analysisSpy = vi.spyOn(teamsApi, 'getAnalysis')
      .mockResolvedValueOnce(readyAnalysis('1', '120000'))
      .mockResolvedValue(readyAnalysis('1', '240000'));

    const { result, unmount } = renderHook(() => useTeamAnalysis({
      teamId: 'tem_0123456789abcdefghijklmnop',
      authRevision: '1',
      range: '7d',
    }));

    await act(async () => {
      await Promise.resolve();
    });
    expect(result.current.analysis?.summary.tokens.value).toBe('120000');
    expect(analysisSpy).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(15_000);
    });
    expect(result.current.analysis?.summary.tokens.value).toBe('240000');
    expect(analysisSpy.mock.calls.length).toBeGreaterThanOrEqual(2);

    unmount();
    await act(async () => {
      await Promise.resolve();
    });
    const callsAfterLeave = analysisSpy.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(analysisSpy.mock.calls.length).toBe(callsAfterLeave);
  });

  it('sends the current agent and model filters when starting an export', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({
      agents: [{ id: 'codex', label: 'codex' }],
      providers: [],
      models: [{ id: 'gpt-test', label: 'gpt-test' }],
    });
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    const exportSpy = vi.spyOn(teamsApi, 'createExport').mockResolvedValue({
      id: 'txj_01',
      kind: 'daily',
      status: 'queued',
      snapshotId: 'tas_01',
      createdAt: '2026-09-06T08:00:00.000Z',
    });

    renderTeams(
      <TeamAnalyticsPage />,
      '/teams/tem_0123456789abcdefghijklmnop/analytics?agent=codex&model=gpt-test'
    );
    expect(await screen.findByText('120.0K')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '每日' }));
    await waitFor(() => {
      expect(exportSpy).toHaveBeenCalledWith(
        'tem_0123456789abcdefghijklmnop',
        expect.objectContaining({
          snapshotId: 'tas_01',
          kind: 'daily',
          agent: 'codex',
          model: 'gpt-test',
        }),
        expect.any(Object)
      );
    });
  });

  it('renders empty analytics when an older server omits the empty trend array', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    const empty = readyAnalysis('1', '0');
    empty.summary.tokens = { value: '0', state: 'empty' };
    Reflect.deleteProperty(empty, 'trend');
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(empty);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop/analytics');
    expect(await screen.findByText('团队 Token')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '自定义' })).toBeInTheDocument();
    expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument();
  });
});
