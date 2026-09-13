import { describe, it, expect, beforeEach, vi } from 'vitest';
import { act, fireEvent, render, renderHook, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { api } from '@/api/client';
import { teamsApi, type TeamAnalysisReady, type TeamAnalysisUpdating } from '@/api/teams';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { TeamProvider } from '@/context/TeamContext';
import { TeamAnalyticsPage } from '@/pages/teams/TeamAnalyticsPage';
import { TeamLayout } from '@/pages/teams/TeamLayout';
import { memberPercent } from '@/pages/teams/TeamMemberInsights';
import { TeamMembersPage } from '@/pages/teams/TeamMembersPage';
import { useTeamAnalysis } from '@/pages/teams/useTeamAnalysis';
import { renderTeams, renderTeamWorkspace, sampleScope, signedInUser } from './teams-test-helpers';

function pickIsoDate(label: string, iso: string) {
  fireEvent.click(screen.getByLabelText(label));
  const day = String(Number(iso.slice(8, 10)));
  const dialog = screen.getByRole('dialog', { name: label });
  const match = within(dialog).getAllByRole('button').filter((el) => el.textContent === day);
  const enabled = match.find((el) => el.getAttribute('aria-disabled') !== 'true' && !(el as HTMLButtonElement).disabled);
  fireEvent.click(enabled || match[0]);
}

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
    ['panel', ''],
    ['analytics-alias', '/analytics'],
  ] as const)('shows today while custom dates are incomplete on a %s deep link', async (_name, suffix) => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    const query = vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    renderTeamWorkspace(`/teams/tem_0123456789abcdefghijklmnop${suffix}?range=custom`);
    await screen.findByLabelText('开始日期');
    await waitFor(() => expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument());
    expect(screen.getByLabelText('开始日期')).toBeInTheDocument();
    expect(screen.getByLabelText('结束日期')).toBeInTheDocument();
    expect(screen.queryByText('选齐日期后自动更新')).not.toBeInTheDocument();
    expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument();
    await waitFor(() => expect(query).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ range: 'today', from: undefined, to: undefined }), expect.any(AbortSignal)));
    pickIsoDate('开始日期', '2026-09-01');
    await waitFor(() => expect(query).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ range: 'today', from: undefined, to: undefined }), expect.any(AbortSignal)));
    pickIsoDate('结束日期', '2026-09-06');
    await waitFor(() => expect(query).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ range: 'custom', from: '2026-09-01', to: '2026-09-06' }), expect.any(AbortSignal)));
    await waitFor(() => expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument());
    expect(screen.queryByText('选齐日期后自动更新')).not.toBeInTheDocument();
    expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '7 天' }));
    await waitFor(() => expect(query).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ range: '7d' }), expect.any(AbortSignal)));
  });

  it.each([
    ['panel', ''],
    ['analytics-alias', '/analytics'],
  ] as const)('does not show the legacy summary banner on %s', async (_name, suffix) => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    const result = readyAnalysis('1', '120000');
    result.quality.hasLegacyAggregates = true;
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeamWorkspace(`/teams/tem_0123456789abcdefghijklmnop${suffix}?range=7d`);
    expect(await screen.findByRole('tab', { name: '7 天' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByText(/包含历史日汇总/)).not.toBeInTheDocument();
    expect(screen.queryByText(/团队时区/)).not.toBeInTheDocument();
  });

  it('does not show date range controls on the members page', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    renderTeams(<TeamMembersPage />, '/teams/tem_0123456789abcdefghijklmnop/members?range=7d');
    expect(await screen.findByText(/已加入/)).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: '7 天' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('开始日期')).not.toBeInTheDocument();
    expect(screen.queryByText(/包含历史日汇总/)).not.toBeInTheDocument();
  });

  it('keeps date controls above the charts and preserves a complete range while editing', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const query = vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    renderTeamWorkspace('/teams/tem_0123456789abcdefghijklmnop?range=7d');
    const chart = await screen.findByRole('heading', { name: '团队用量趋势' });
    const from = screen.getByLabelText('开始日期');
    expect(from.compareDocumentPosition(chart) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByRole('heading', { name: '团队用量趋势' })).toBeInTheDocument();
    expect(screen.getAllByText('120.0K')[0]).toBeInTheDocument();
    pickIsoDate('开始日期', '2026-09-01');
    expect(query).toHaveBeenCalledTimes(1);
    expect(screen.getAllByText('120.0K')[0]).toBeInTheDocument();
    pickIsoDate('结束日期', '2026-09-06');
    await waitFor(() => expect(query).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ range: 'custom', from: '2026-09-01', to: '2026-09-06' }), expect.any(AbortSignal)));
  });

  it('clears retained charts when authorization changes during date editing', async () => {
    const query = vi.spyOn(teamsApi, 'getAnalysis')
      .mockResolvedValueOnce(readyAnalysis('1', '120000'))
      .mockImplementation(() => new Promise(() => undefined));
    const { result, rerender } = renderHook(({ revision, range }) => useTeamAnalysis({
      teamId: 'tem_0123456789abcdefghijklmnop', authRevision: revision, range,
    }), { initialProps: { revision: '1', range: '7d' } });
    await waitFor(() => expect(result.current.analysis?.summary.tokens.value).toBe('120000'));
    rerender({ revision: '1', range: 'custom' });
    expect(result.current.analysis?.summary.tokens.value).toBe('120000');
    rerender({ revision: '2', range: 'custom' });
    expect(result.current.analysis?.summary.tokens.value).toBe('120000');
    expect(query).toHaveBeenCalledTimes(2);
  });

  it('lists contributions without named-share copy', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const result = readyAnalysis('1', '120000');
    result.contributions = {
      items: [{ membershipId: 'tmb_1', displayName: 'Ada', handle: 'ada', rank: '1', tokens: { value: '120000', state: 'available' } }],
      nextCursor: null,
    };
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect(await screen.findAllByText('Ada').then(items => items[0])).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '成员数据' })).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: 'Token 效率' })).toBeInTheDocument();
    expect(screen.queryByText(/仅显示主动授权/)).not.toBeInTheDocument();
    expect(screen.queryByText(/团队时区/)).not.toBeInTheDocument();
  });

  it('does not show usage-share toggles, private-team label, or in-page overview anchors', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    await screen.findByRole('heading', { name: '团队用量趋势' });
    expect(screen.queryByRole('heading', { name: '团队 Token 趋势' })).not.toBeInTheDocument();
    expect(screen.queryByText('私密团队')).not.toBeInTheDocument();
    expect(screen.queryByText('成员表现')).not.toBeInTheDocument();
    expect(screen.queryByText('我的共享')).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '用量构成' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '团队 Token 数据' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '成员数据' })).toBeInTheDocument();
    expect(screen.queryByText('Harness、模型与 Skill 的成员分布')).not.toBeInTheDocument();
    expect(screen.queryByText('查看每日明细')).not.toBeInTheDocument();
    expect(screen.queryByText('查看成员明细 ↓')).not.toBeInTheDocument();
    expect(screen.queryByText('按已计价用量统计 · USD')).not.toBeInTheDocument();
    expect(screen.queryByText(/当前共享/)).not.toBeInTheDocument();
    expect(screen.queryByText('跨成员汇总')).not.toBeInTheDocument();
    expect(screen.queryByText('按成员累计使用时长，成员之间的同时使用分别计入')).not.toBeInTheDocument();
    expect(screen.queryByText('缓存命中率按有效输入样本计算')).not.toBeInTheDocument();
    expect(document.querySelector('.team-cache-track')).toBeNull();
    expect(screen.queryByText('谁贡献了用量，以及每个人的使用变化')).not.toBeInTheDocument();
    expect(screen.queryByText('各成员用量与 Token 效率')).not.toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Harness' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Skill' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Skill 使用' })).not.toBeInTheDocument();
  });

  it('shows skill ranking and member distribution from static analysis', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const result = readyAnalysis('1', '120000');
    result.skills = {
      items: [
        {
          id: 'codex\u001ffrontend-design',
          label: 'frontend-design',
          agentId: 'codex',
          useCount: '224',
          share: '28.6',
          memberCount: '2',
          members: [
            { membershipId: 'tmb_1', displayName: 'Ada', useCount: '140', share: '62.5' },
            { membershipId: 'tmb_2', displayName: 'Bo', useCount: '84', share: '37.5' },
          ],
        },
        {
          id: 'codex\u001fcode-review',
          label: 'code-review',
          agentId: 'codex',
          useCount: '100',
          share: '12.8',
          memberCount: '1',
          members: [{ membershipId: 'tmb_1', displayName: 'Ada', useCount: '100', share: '100' }],
        },
      ],
      nextCursor: null,
    };
    result.agents = {
      items: [{
        id: 'codex', label: 'codex', tokens: { value: '80000', state: 'available' }, share: '66.7',
        members: [{ membershipId: 'tmb_1', displayName: 'Ada', useCount: '50000', share: '62.5' }],
      }],
      nextCursor: null,
    };
    result.models = {
      items: [{
        id: 'gpt-test', label: 'openai/gpt-test', tokens: { value: '40000', state: 'available' }, share: '33.3',
        members: [{ membershipId: 'tmb_2', displayName: 'Bo', useCount: '40000', share: '100' }],
      }],
      nextCursor: null,
    };
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect(await screen.findByRole('heading', { name: '用量构成' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /codex/ })).toBeInTheDocument();
    expect(screen.getByText('Ada')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '模型' }));
    expect(screen.getByRole('button', { name: /gpt-test/ })).toBeInTheDocument();
    expect(screen.getByText('Bo')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: 'Skill' }));
    fireEvent.click(screen.getByRole('button', { name: /frontend-design/ }));
    expect(screen.getAllByText('frontend-design · Codex').length).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: /code-review/ }));
    expect(screen.getAllByText('code-review · Codex').length).toBeGreaterThan(0);
  });

  it('shows a historical footnote and does not render a fake zero for empty ranges', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const empty = readyAnalysis('1', '0');
    empty.summary.tokens = { value: '0', state: 'empty' };
    empty.quality.includesHistoricalUsers = true;
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(empty);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect((await screen.findAllByText('此范围没有数据。')).length).toBeGreaterThan(0);
    expect(screen.getByText(/合计含已退出成员的历史用量/)).toBeInTheDocument();
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

    renderTeamWorkspace('/teams/tem_0123456789abcdefghijklmnop');

    expect(await screen.findByTestId('analysis-skeleton')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: '今天' })).toHaveAttribute('aria-selected', 'true');
    expect(teamsApi.getAnalysis).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ range: 'today' }), expect.any(AbortSignal));
    expect(screen.getAllByRole('tab').map((tab) => tab.textContent)).toEqual(['今天', '7 天', '30 天']);
    expect(screen.getByLabelText('开始日期')).toBeInTheDocument();
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

    const view = renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop');
    expect(await screen.findAllByText('120.0K').then(items => items[0])).toBeInTheDocument();

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
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop');

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

  it('does not show snapshot export controls on the data panel', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect(await screen.findByRole('heading', { name: '团队用量趋势' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '导出当前快照' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '每日' })).not.toBeInTheDocument();
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
    renderTeamWorkspace('/teams/tem_0123456789abcdefghijklmnop');
    expect(await screen.findByText('团队总 Token')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '团队 Token 数据' })).toBeInTheDocument();
    expect(screen.getByText('人均 Token')).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '用量构成' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Skill' })).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Token 使用效率' })).not.toBeInTheDocument();
    expect(screen.getByLabelText('开始日期')).toBeInTheDocument();
    expect(screen.queryByTestId('analysis-skeleton')).not.toBeInTheDocument();
  });
});


describe('Team member insights', () => {
  it('keeps large integer share precision and handles zero totals', () => {
    expect(memberPercent('900719925474099300', '1801439850948198600')).toBe(50);
    expect(memberPercent('1', '0')).toBe(0);
  });
  it('defaults the usage trend to the team series and switches with the member dropdown', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const result = readyAnalysis('1', '1000');
    result.contributions = { nextCursor: 'more', items: Array.from({ length: 6 }, (_, i) => ({
      membershipId: `member-${i}`, displayName: `Person ${i}`, handle: null, rank: String(i + 1),
      tokens: { state: 'available' as const, value: '100' },
      trend: [{ date: '2026-09-05', tokens: { value: '100', state: 'available' as const } }],
    })) };
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    const picker = await screen.findByLabelText('选择成员');
    expect(picker).toHaveValue('');
    expect(screen.getByRole('heading', { name: '团队用量趋势' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '团队效率趋势' })).toBeInTheDocument();
    expect(screen.getByLabelText('选择成员')).toHaveDisplayValue('全部');
    expect(screen.queryByText('Token / 天')).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '成员用量趋势' })).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: '团队 Token 趋势' })).not.toBeInTheDocument();
    expect(screen.getAllByText(/10\.0%/)).toHaveLength(11);
    expect(screen.getByText('50.0%')).toBeInTheDocument();
    fireEvent.change(picker, { target: { value: 'member-0' } });
    expect(picker).toHaveValue('member-0');
  });

  it('shows token efficiency for the team and each member', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const result = readyAnalysis('1', '1000');
    result.summary.metrics = { tokensPerCodeLine: { value: '250', state: 'available' } };
    result.contributions = {
      items: [{
        membershipId: 'tmb_1', displayName: 'Ada', handle: 'ada', rank: '1',
        tokens: { state: 'available', value: '1000' },
        generatedCodeLines: '4',
        tokensPerCodeLine: '250',
      }],
      nextCursor: null,
    };
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect(await screen.findByRole('heading', { name: '成员数据' })).toBeInTheDocument();
    expect(screen.getAllByText('250').length).toBeGreaterThan(0);
    expect(screen.getByRole('columnheader', { name: 'Token 效率' })).toBeInTheDocument();
  });

  it('plots team efficiency by day', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    const result = readyAnalysis('1', '1000');
    result.efficiencyTrend = [
      { date: '2026-09-05', tokens: { value: '250', state: 'available' } },
      { date: '2026-09-06', tokens: { value: '80', state: 'available' } },
    ];
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(result);
    renderTeams(<TeamAnalyticsPage />, '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect(await screen.findByRole('heading', { name: '团队效率趋势' })).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '团队效率趋势' })).toBeInTheDocument();
  });
});

const AnalyticsRedirect: React.FC = () => {
  const { search } = useLocation();
  return <Navigate to={{ pathname: '..', search }} relative="path" replace />;
};

describe('Team page tabs', () => {
  it('puts the data panel first, then members and settings, with dates only on the panel', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });
    vi.spyOn(teamsApi, 'getMembers').mockResolvedValue({ members: [], nextCursor: null });
    vi.spyOn(teamsApi, 'getInvitations').mockResolvedValue({ invitations: [], nextCursor: null });
    vi.spyOn(teamsApi, 'getInviteLinks').mockResolvedValue({ links: [], nextCursor: null });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter
              initialEntries={['/teams/tem_0123456789abcdefghijklmnop?range=7d']}
              future={{ v7_startTransition: true, v7_relativeSplatPath: true }}
            >
              <TeamProvider>
                <Routes>
                  <Route path="/teams/:teamId" element={<TeamLayout />}>
                    <Route index element={<TeamAnalyticsPage />} />
                    <Route path="analytics" element={<AnalyticsRedirect />} />
                    <Route path="members" element={<TeamMembersPage />} />
                    <Route path="settings" element={<div>settings-tab</div>} />
                  </Route>
                </Routes>
              </TeamProvider>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    const nav = await screen.findByRole('navigation', { name: '团队页面' });
    const links = within(nav).getAllByRole('link');
    expect(links.map((link) => link.textContent)).toEqual(['数据面板', '成员', '设置']);
    expect(links[0]).toHaveAttribute('href', '/teams/tem_0123456789abcdefghijklmnop?range=7d');
    expect(links[1]).toHaveAttribute('href', '/teams/tem_0123456789abcdefghijklmnop/members?range=7d');
    expect(links[2]).toHaveAttribute('href', '/teams/tem_0123456789abcdefghijklmnop/settings?range=7d');
    expect(screen.queryByRole('link', { name: '总览' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '用量分析' })).not.toBeInTheDocument();
    expect(await screen.findByRole('tab', { name: '7 天' })).toHaveAttribute('aria-selected', 'true');
    const heading = document.querySelector('.product-page-heading');
    const toolbar = document.querySelector('.team-filter-toolbar');
    expect(heading?.contains(screen.getByRole('tab', { name: '7 天' }))).toBe(false);
    expect(heading?.contains(screen.getByRole('button', { name: '邀请成员' }))).toBe(true);
    expect(toolbar?.contains(screen.getByRole('tab', { name: '7 天' }))).toBe(true);
    expect(toolbar?.contains(screen.getByLabelText('Agent 筛选'))).toBe(true);
    expect(nav.contains(screen.getByRole('tab', { name: '7 天' }))).toBe(false);

    fireEvent.click(screen.getByRole('link', { name: '成员' }));
    expect(await screen.findByText(/已加入/)).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: '7 天' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('开始日期')).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('link', { name: '设置' }));
    expect(await screen.findByText('settings-tab')).toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: '7 天' })).not.toBeInTheDocument();
    expect(screen.queryByLabelText('开始日期')).not.toBeInTheDocument();
  });

  it('redirects /analytics onto the data panel and keeps the date range', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(readyAnalysis('1', '120000'));
    vi.spyOn(teamsApi, 'getExports').mockResolvedValue({ exports: [] });
    vi.spyOn(teamsApi, 'getFilterOptions').mockResolvedValue({ agents: [], providers: [], models: [] });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter
              initialEntries={['/teams/tem_0123456789abcdefghijklmnop/analytics?range=7d']}
              future={{ v7_startTransition: true, v7_relativeSplatPath: true }}
            >
              <TeamProvider>
                <Routes>
                  <Route path="/teams/:teamId" element={<TeamLayout />}>
                    <Route index element={<TeamAnalyticsPage />} />
                    <Route path="analytics" element={<AnalyticsRedirect />} />
                  </Route>
                </Routes>
              </TeamProvider>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    expect(await screen.findByRole('tab', { name: '7 天' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('navigation', { name: '团队页面' }).querySelector('a.active')).toHaveTextContent('数据面板');
    expect(screen.queryByRole('link', { name: '用量分析' })).not.toBeInTheDocument();
  });
});

