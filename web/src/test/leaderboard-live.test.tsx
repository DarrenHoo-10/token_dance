import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { api } from '@/api/client';
import type { LeaderboardResponse, PrivacySettings } from '@/types/api';

vi.mock('@/context/AuthContext', () => ({ useAuth: () => ({ authenticated: true, user: { userId: 'owner', handle: 'owner' } }) }));
const privacy: PrivacySettings = { publicProfileEnabled: false, leaderboardVisibility: 'private', showTokenTotal: true, showBio:false,showTrends:false,showActivityCalendar:false,showAgentBreakdown:false,showSkillRanking:false,showAchievements:false,privacyVersion:1 };
const board: LeaderboardResponse = {snapshotId:'',boardKey:'global',window:'today',metric:'tokens',entries:[],totalEntries:0,totalTokens:'0',timezone:'UTC'};
function showPage() {
  return render(<LocaleProvider><MemoryRouter initialEntries={['/leaderboard']} future={{v7_startTransition:true,v7_relativeSplatPath:true}}><Routes>
    <Route path="/leaderboard" element={<LeaderboardPage />} /><Route path="/me" element={<h1>我的数据</h1>} /><Route path="/settings/devices" element={<h1>设备设置</h1>} />
  </Routes></MemoryRouter></LocaleProvider>);
}
beforeEach(() => {
  vi.restoreAllMocks(); localStorage.clear();
  vi.spyOn(api,'getPrivacy').mockResolvedValue(privacy);
  vi.spyOn(api,'getPersonalSummary').mockRejectedValue(new Error('unavailable'));
  vi.spyOn(api,'getActivityCalendar').mockRejectedValue(new Error('unavailable'));
  vi.spyOn(api,'getLeaderboard').mockResolvedValue(board);
  vi.spyOn(api,'getMyLeaderboard').mockImplementation((params) => api.getLeaderboard(params));
  vi.spyOn(api,'getCommunityStats').mockResolvedValue({ metricDate:'2026-09-09', timezone:'UTC', window:'7d' });
  vi.spyOn(api,'getPublicTokenTrends').mockResolvedValue({ visible: false });
  vi.spyOn(api,'getPublicProfile').mockRejectedValue(new Error('hidden'));
});
describe('Live leaderboard', () => {
  it('does not offer a public-profile switch on the homepage', async () => {
    const update=vi.spyOn(api,'updatePrivacy'); showPage();
    expect(await screen.findByRole('heading', { name: '平台排行榜' })).toBeInTheDocument();
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    expect(screen.queryByRole('button',{name:'管理公开设置'})).not.toBeInTheDocument();
    expect(update).not.toHaveBeenCalled();
  });
  it('does not show a redundant personal analytics action on the owner trend card', async () => {
    vi.mocked(api.getLeaderboard).mockResolvedValue({
      ...board,
      entries: [{ rankNo: 1, handle: 'owner', displayName: 'Owner', avatarUrl: null, metricValue: '100', rankDelta: 0 }],
    });
    vi.mocked(api.getPublicTokenTrends).mockResolvedValue({
      visible: true,
      points: [{ date: '2026-09-19', tokenTotal: '100' }],
    });
    showPage();
    expect(await screen.findByRole('heading', { name: 'Owner的创作轨迹' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: '个人数据' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: '个人数据' })).not.toBeInTheDocument();
  });
  it('ignores a late response from the previously selected period', async () => {
    const ranked = (window: LeaderboardResponse['window'], metricValue: string): LeaderboardResponse => ({
      ...board,
      window,
      entries: [{ rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue, rankDelta: 0 }],
    });
    let resolveWeek!: (value: LeaderboardResponse) => void;
    vi.mocked(api.getLeaderboard).mockImplementation(({window}={}) => window==='7d' ? new Promise(resolve => {resolveWeek=resolve;}) : Promise.resolve(ranked('30d','30000000')));
    showPage();
    expect(screen.getByRole('tab',{name:'近 7 天'})).toHaveAttribute('aria-selected', 'true');
    fireEvent.click(screen.getByRole('tab',{name:'近 30 天'}));
    expect((await screen.findAllByText('30.0M')).length).toBeGreaterThan(0);
    resolveWeek(ranked('7d','7000000'));
    await waitFor(()=>expect(screen.getAllByText('30.0M').length).toBeGreaterThan(0));
    expect(screen.queryAllByText('7.0M')).toHaveLength(0);
  });
  it('renders precomputed community totals in the hero', async () => {
    vi.mocked(api.getCommunityStats).mockResolvedValue({
      metricDate:'2026-09-09', timezone:'UTC', window:'7d',
      tokens:'186400000', developers:128, codeLines:'32800', interactions:'4600', costAmount:268.42,
      deltas:{ tokens:12.6, developers:8.4, codeLines:-50, interactions:12.3, costAmount:11.8 },
      harnesses:[
        { agentId:'zcode', label:'Zcode', tokens:'186400000', sharePct:64.2 },
        { agentId:'codex', label:'Codex CLI', tokens:'58000000', sharePct:20 },
      ],
    });
    showPage();
    expect(await screen.findByText('186.4M')).toBeInTheDocument();
    expect(screen.getByText('128')).toBeInTheDocument();
    expect(screen.getByText('32.8K')).toBeInTheDocument();
    expect(screen.getByText('4.6K')).toBeInTheDocument();
    expect(screen.getByText('$268.42')).toBeInTheDocument();
    expect(screen.getByText('较上 7 天').closest('.hero-delta')).toHaveTextContent('↑ +12.6% 较上 7 天');
    expect(screen.getByText('↓ −50.0%')).toBeInTheDocument();
    expect(screen.getByText('Zcode')).toBeInTheDocument();
    expect(screen.getByText('Codex CLI')).toBeInTheDocument();
    expect(screen.getByText('64%')).toBeInTheDocument();
    expect(screen.getByText('社区近 7 天 Token 占比 · 按 harness')).toBeInTheDocument();
    expect(document.querySelector('[data-harness="zcode"]')).toBeTruthy();
    expect(document.querySelector('[data-harness="codex"]')).toBeTruthy();
    expect(document.querySelector('[data-harness="zcode"] svg')).toBeTruthy();
    expect(document.querySelector('[data-harness="codex"] svg')).toBeTruthy();
    expect(screen.queryByText('社区今日 Token 占比 · 按 harness')).not.toBeInTheDocument();
    expect(screen.queryByText('今天，整个社区正在持续燃烧 Token')).not.toBeInTheDocument();
    expect(screen.queryByText(/More builders/)).not.toBeInTheDocument();
    expect(screen.queryByText(/A brighter tomorrow/)).not.toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '社区模型排行榜' })).toBeInTheDocument();
    expect(screen.getByRole('heading', { name: '社区 Skill 排行榜' })).toBeInTheDocument();
    expect(screen.getByText('暂无社区模型用量数据。')).toBeInTheDocument();
    expect(screen.getByText('暂无社区 Skill 用量数据。')).toBeInTheDocument();
    expect(document.querySelector('.sky-share-grid')?.querySelectorAll('.sky-share-board')).toHaveLength(3);
    expect(document.querySelector('.sky-share-grid')?.lastElementChild).toHaveClass('sky-share-board-wide');
    expect(document.querySelector('.sky-side')?.querySelector('.sky-share-board')).toBeNull();
    expect(screen.getByRole('heading', { name: '正在创造的他们' })).toBeInTheDocument();
  });

  it('switches the shared trend chart to the clicked podium builder', async () => {
    vi.mocked(api.getLeaderboard).mockResolvedValue({
      ...board,
      entries: [
        { rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue: '200', rankDelta: 0 },
        { rankNo: 2, handle: 'grace', displayName: 'Grace', avatarUrl: null, metricValue: '100', rankDelta: 0 },
        { rankNo: 3, handle: 'linus', displayName: 'Linus', avatarUrl: null, metricValue: '50', rankDelta: 0 },
      ],
    });
    vi.mocked(api.getPublicTokenTrends).mockImplementation(async (handle) => ({
      visible: true,
      points: handle === 'grace'
        ? [{ date: '2026-09-18', tokenTotal: '40' }, { date: '2026-09-19', tokenTotal: '60' }]
        : [{ date: '2026-09-18', tokenTotal: '10' }, { date: '2026-09-19', tokenTotal: '20' }],
    }));
    showPage();
    expect(await screen.findByRole('heading', { name: 'Ada的创作轨迹' })).toBeInTheDocument();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('ada', { range: '30d' }));
    expect(screen.queryByText('登录，留下你的足迹')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: '查看 Grace 的公开创作轨迹' }));
    expect(await screen.findByRole('heading', { name: 'Grace的创作轨迹' })).toBeInTheDocument();
    await waitFor(() => expect(api.getPublicTokenTrends).toHaveBeenCalledWith('grace', { range: '30d' }));
    expect(screen.getByRole('link', { name: '公开资料' })).toHaveAttribute('href', '/u/grace');
    expect(screen.queryByRole('link', { name: 'Ada Lovelace' })).not.toBeInTheDocument();
  });

  it('renders community model and skill boards from the public stats payload', async () => {
    vi.mocked(api.getCommunityStats).mockResolvedValue({
      metricDate: '2026-09-09', timezone: 'UTC', window: '7d',
      harnesses: [{ agentId: 'codex', label: 'Codex CLI', tokens: '70', sharePct: 70 }],
      models: [{ modelId: 'gpt-5', label: 'gpt-5', tokens: '50', sharePct: 50 }],
      skills: [{ skillId: 'review', label: 'review', uses: '9', sharePct: 90 }],
    });
    showPage();
    expect(await screen.findByText('gpt-5')).toBeInTheDocument();
    expect(screen.getByText('review')).toBeInTheDocument();
    expect(screen.getByText('社区近 7 天 Token 占比 · 按模型')).toBeInTheDocument();
    expect(screen.getByText('社区近 7 天 调用占比 · 按 Skill')).toBeInTheDocument();
    expect(screen.getByText('50%')).toBeInTheDocument();
    expect(screen.getByText('90%')).toBeInTheDocument();
  });

  it('uses seven days by default and reloads hero stats with the selected homepage period', async () => {
    vi.mocked(api.getCommunityStats).mockImplementation(async (window) => ({ metricDate:'2026-09-09', timezone:'UTC+8', window }));
    showPage();
    await waitFor(() => expect(api.getCommunityStats).toHaveBeenCalledWith('7d'));
    expect(api.getMyLeaderboard).toHaveBeenCalledWith(expect.objectContaining({ window: '7d' }));
    expect(screen.getByText('近 7 天 Token')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '今天' }));
    await waitFor(() => expect(api.getCommunityStats).toHaveBeenLastCalledWith('today'));
    expect(api.getMyLeaderboard).toHaveBeenLastCalledWith(expect.objectContaining({ window: 'today' }));
    expect(screen.getAllByText('今日 Token').length).toBeGreaterThan(0);
  });
  it('updates the common harness breakdown with the selected homepage period', async () => {
    vi.mocked(api.getCommunityStats).mockImplementation(async (window) => ({
      metricDate:'2026-09-09', timezone:'UTC+8', window,
      harnesses: window === 'all'
        ? [{ agentId:'cursor', label:'Cursor', tokens:'900', sharePct:90 }]
        : [{ agentId:'codex', label:'Codex CLI', tokens:'700', sharePct:70 }],
    }));
    showPage();
    expect(await screen.findByText('Codex CLI')).toBeInTheDocument();
    expect(screen.getByText('70%')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', { name: '全部时间' }));
    expect(await screen.findByText('Cursor')).toBeInTheDocument();
    expect(screen.getByText('90%')).toBeInTheDocument();
    expect(screen.queryByText('Codex CLI')).not.toBeInTheDocument();
  });
  it('does not display harness data returned for a different period', async () => {
    vi.mocked(api.getCommunityStats).mockResolvedValue({
      metricDate:'2026-09-09', timezone:'UTC+8', window:'today',
      harnesses: [{ agentId:'codex', label:'Codex CLI', tokens:'700', sharePct:70 }],
    });
    showPage();
    await waitFor(() => expect(api.getCommunityStats).toHaveBeenCalledWith('7d'));
    expect(screen.queryByText('Codex CLI')).not.toBeInTheDocument();
  });
  it('ignores late hero statistics from the previously selected period', async () => {
    let resolveWeek!: (value: Awaited<ReturnType<typeof api.getCommunityStats>>) => void;
    vi.mocked(api.getCommunityStats).mockImplementation((window) => window === '7d'
      ? new Promise(resolve => { resolveWeek = resolve; })
      : Promise.resolve({ metricDate:'2026-09-09', timezone:'UTC+8', window:'today', tokens:'1000' }));
    showPage();
    fireEvent.click(screen.getByRole('tab', { name: '今天' }));
    expect(await screen.findByText('1.0K')).toBeInTheDocument();
    resolveWeek({ metricDate:'2026-09-09', timezone:'UTC+8', window:'7d', tokens:'7000000' });
    await act(async () => {});
    expect(screen.queryByText('7.0M')).not.toBeInTheDocument();
    expect(screen.getByText('1.0K')).toBeInTheDocument();
  });
  it('lists community multi-currency costs instead of a fake $8.00', async () => {
    vi.mocked(api.getCommunityStats).mockResolvedValue({
      metricDate:'2026-09-09', timezone:'UTC', window:'7d',
      tokens:'1215', developers:1, codeLines:'0', interactions:'6',
      costAmount: null,
      costs: [{ amount: 1, currency: 'USD' }, { amount: 7, currency: 'CNY' }],
    });
    showPage();
    expect(await screen.findByText('$1.00 · ¥7.00')).toBeInTheDocument();
    expect(screen.queryByText('$8.00')).not.toBeInTheDocument();
  });
  it('shows personal today tokens from the live board entry, not an empty event sum', async () => {
    vi.mocked(api.getActivityCalendar).mockResolvedValue({ days: [], currentStreak: 1, longestStreak: 1, totalActiveDays: 0 });
    vi.mocked(api.getPersonalSummary).mockImplementation(async (range) => {
      const unsupported = { value: null, supported: false };
      const unsupportedCost = { amount: null, currency: 'USD', supported: false };
      if (range === 'all') {
        return { range: { key: 'all', from: '', to: '', timezone: 'Asia/Shanghai' }, metrics: { estimatedCost: unsupportedCost, totalTokens: { value: '4600000000', supported: true }, generatedCodeLines: unsupported, tokensPerCodeLine: unsupported, inputContextTokens: unsupported, outputTokens: unsupported, cacheHitRate: unsupported, activeDurationMs: unsupported, messageCount: unsupported, userMessageCount: unsupported }, ranking: { rank: 2, percentile: 100 }, sync: { lastCommittedAt: null, pendingLocalCount: null }, aggregationVersion: 2 };
      }
      return { range: { key: 'today', from: '', to: '', timezone: 'Asia/Shanghai' }, metrics: { estimatedCost: unsupportedCost, totalTokens: { value: '0', supported: true }, generatedCodeLines: unsupported, tokensPerCodeLine: unsupported, inputContextTokens: unsupported, outputTokens: unsupported, cacheHitRate: unsupported, activeDurationMs: unsupported, messageCount: unsupported, userMessageCount: unsupported }, ranking: { rank: 1, percentile: 100, entry: { rankNo: 1, handle: 'darrenhoomessi', displayName: 'darrenhoo', avatarUrl: null, metricValue: '218700000', rankDelta: 0 } }, sync: { lastCommittedAt: null, pendingLocalCount: null }, aggregationVersion: 2 };
    });
    showPage();
    expect(await screen.findByText('218.7M')).toBeInTheDocument();
    expect(screen.getByText('4.6B')).toBeInTheDocument();
    expect(screen.queryByText(/^0$/)).not.toBeInTheDocument();
  });

  it('keeps the hero empty instead of fabricating numbers when stats are unavailable', async () => {
    vi.mocked(api.getCommunityStats).mockRejectedValue(new Error('offline'));
    showPage();
    expect((await screen.findAllByText('—')).length).toBeGreaterThan(0);
    expect(screen.queryByText('+0.0%')).not.toBeInTheDocument();
  });
  it('keeps existing rows and shows a short connection error after a failed refresh', async () => {
    const ranked = {
      ...board,
      entries: [{ rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue: '100', rankDelta: 0 }],
      totalEntries: 1,
      totalTokens: '100',
    };
    vi.mocked(api.getLeaderboard).mockResolvedValueOnce(ranked).mockRejectedValueOnce(new Error('offline'));
    showPage();
    expect(await screen.findAllByText('Ada')).toHaveLength(2);
    expect(screen.queryByText('ada')).not.toBeInTheDocument();
    expect(screen.queryByText('持平')).not.toBeInTheDocument();
    expect(screen.queryByText('−')).not.toBeInTheDocument();
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    expect(await screen.findByRole('alert')).toHaveTextContent('连接异常');
    expect(screen.getAllByText('Ada')).toHaveLength(2);
  });
});

describe('Percentile formatting', () => {
  it('renders the rank percentile with two decimals rounded up', async () => {
    vi.spyOn(api,'getPersonalSummary').mockResolvedValue({
      range: { key: 'today', from: '', to: '', timezone: 'Asia/Shanghai' },
      metrics: { estimatedCost: { supported: false }, totalTokens: { value: '0', supported: true }, generatedCodeLines: { supported: false }, tokensPerCodeLine: { supported: false }, inputContextTokens: { supported: false }, outputTokens: { supported: false }, cacheHitRate: { supported: false }, activeDurationMs: { supported: false }, messageCount: { supported: false }, userMessageCount: { supported: false } },
      ranking: { rank: 3, percentile: 77.77777777777779 },
      sync: { lastCommittedAt: null, pendingLocalCount: null },
      aggregationVersion: 2,
    } as never);
    vi.spyOn(api,'getActivityCalendar').mockResolvedValue({ days: [], currentStreak: 0, longestStreak: 0, totalActiveDays: 0 });
    showPage();
    expect(await screen.findByText('前 77.78%')).toBeInTheDocument();
    expect(screen.queryByText(/77\.7778?7?%/)).not.toBeInTheDocument();
  });
});
