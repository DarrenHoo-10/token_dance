import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardPage } from '@/pages/public/LeaderboardPage';
import { api } from '@/api/client';
import type { LeaderboardResponse, PrivacySettings } from '@/types/api';
import { publicHomeDay, writeHomeBoard, writeHomeCommunity } from '@/utils/publicHomeCache';

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
  vi.spyOn(api,'getCommunityStats').mockResolvedValue({ metricDate:'2026-09-09', timezone:'UTC' });
});
describe('Live leaderboard', () => {
  it('shows saved rows and community immediately, then replaces them in the background', async () => {
    writeHomeBoard('board:today', [{rankNo:1, handle:'saved', displayName:'Saved user', avatarUrl:null, metricValue:'123'}]);
    writeHomeCommunity('community', {metricDate:publicHomeDay(), timezone:'UTC+8', tokens:'123000000'});
    let resolveBoard!: (value: LeaderboardResponse) => void;
    vi.mocked(api.getLeaderboard).mockReturnValue(new Promise(resolve => { resolveBoard = resolve; }));
    let resolveStats!: (value: Awaited<ReturnType<typeof api.getCommunityStats>>) => void;
    vi.mocked(api.getCommunityStats).mockReturnValue(new Promise(resolve => { resolveStats = resolve; }));
    showPage();
    expect(screen.getAllByText('Saved user')).toHaveLength(2);
    expect(screen.getByText('123.0M')).toBeInTheDocument();
    expect(screen.queryByText('加载中…')).not.toBeInTheDocument();
    await act(async () => {
      resolveBoard({...board, entries:[{rankNo:1, handle:'fresh', displayName:'Fresh user', avatarUrl:null, metricValue:'456'}]});
      resolveStats({metricDate:publicHomeDay(), timezone:'UTC+8', tokens:'456000000'});
    });
    expect(screen.getAllByText('Fresh user')).toHaveLength(2);
    expect(screen.queryByText('Saved user')).not.toBeInTheDocument();
    expect(screen.getByText('456.0M')).toBeInTheDocument();
  });
  it('retains saved content on refresh failure and restores it after remounting', async () => {
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board, entries:[{rankNo:1,handle:'saved',displayName:'Saved user',avatarUrl:null,metricValue:'123'}]});
    vi.mocked(api.getCommunityStats).mockResolvedValue({metricDate:publicHomeDay(),timezone:'UTC+8',tokens:'123000000'});
    const page = showPage();
    expect(await screen.findAllByText('Saved user')).toHaveLength(2);
    page.unmount();
    vi.mocked(api.getLeaderboard).mockRejectedValue(new Error('offline'));
    vi.mocked(api.getCommunityStats).mockRejectedValue(new Error('offline'));
    showPage();
    expect(screen.getAllByText('Saved user')).toHaveLength(2);
    expect(await screen.findByRole('alert')).toHaveTextContent('连接异常');
    expect(screen.getByText('123.0M')).toBeInTheDocument();
  });
  it('switches to the selected period cache without showing the previous period', async () => {
    writeHomeBoard('board:today', [{rankNo:1,handle:'today',displayName:'Today cached',avatarUrl:null,metricValue:'1'}]);
    writeHomeBoard('board:7d', [{rankNo:1,handle:'week',displayName:'Week cached',avatarUrl:null,metricValue:'2'}]);
    vi.mocked(api.getLeaderboard).mockReturnValue(new Promise(() => {}));
    showPage();
    expect(screen.getAllByText('Today cached')).toHaveLength(2);
    fireEvent.click(screen.getByRole('tab', {name:'近 7 天'}));
    expect(screen.getAllByText('Week cached')).toHaveLength(2);
    expect(screen.queryByText('Today cached')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('tab', {name:'近 30 天'}));
    expect(screen.queryByText('Week cached')).not.toBeInTheDocument();
    expect(screen.getByText('加载中…')).toBeInTheDocument();
    await act(async () => {});
  });
  it('drops yesterday snapshots on a visible refresh across Beijing midnight', async () => {
    const clock = vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-09-19T15:59:59Z'));
    writeHomeBoard('board:today', [{rankNo:1,handle:'old',displayName:'Yesterday',avatarUrl:null,metricValue:'1'}]);
    writeHomeCommunity('community', {metricDate:publicHomeDay(),timezone:'UTC+8',tokens:'123000000'});
    vi.mocked(api.getLeaderboard).mockReturnValue(new Promise(() => {}));
    vi.mocked(api.getCommunityStats).mockReturnValue(new Promise(() => {}));
    showPage();
    expect(screen.getAllByText('Yesterday')).toHaveLength(2);
    clock.mockReturnValue(Date.parse('2026-09-19T16:00:00Z'));
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    expect(screen.queryByText('Yesterday')).not.toBeInTheDocument();
    expect(screen.queryByText('123.0M')).not.toBeInTheDocument();
  });
  it('requests a cacheable public top 100 and caps the rendered rows', async () => {
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board, entries: Array.from({length: 101}, (_, i) => ({rankNo: i + 1, handle: `user-${i + 1}`, displayName: `User ${i + 1}`, avatarUrl: `/api/v1/public/avatars/${i + 1}`, metricValue: '1'}))});
    showPage();
    expect(await screen.findByText('User 100')).toBeInTheDocument();
    expect(screen.queryByText('User 101')).not.toBeInTheDocument();
    expect(api.getLeaderboard).toHaveBeenCalledWith({window: 'today', limit: 100});
    expect(api.getMyLeaderboard).not.toHaveBeenCalled();
    expect(screen.getByAltText('User 1 profile')).toHaveAttribute('fetchpriority', 'high');
    expect(document.querySelector('img[src="/api/v1/public/avatars/100"]')).toHaveAttribute('loading', 'lazy');
  });
  it('explains that a private profile still stays on the board', async () => {
    const update=vi.spyOn(api,'updatePrivacy'); showPage();
    expect(await screen.findByRole('status')).toHaveTextContent('公开开关只控制详细资料页');
    fireEvent.click(screen.getByRole('button',{name:'管理公开设置'}));
    expect(screen.getByRole('heading',{name:'我的数据'})).toBeInTheDocument();
    expect(update).not.toHaveBeenCalled();
  });
  it('ignores a late response from the previously selected period', async () => {
    const ranked = (window: LeaderboardResponse['window'], metricValue: string): LeaderboardResponse => ({
      ...board,
      window,
      entries: [{ rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue, rankDelta: 0 }],
    });
    let resolveToday!: (value: LeaderboardResponse) => void;
    vi.mocked(api.getLeaderboard).mockImplementation(({window}={}) => window==='today' ? new Promise(resolve => {resolveToday=resolve;}) : Promise.resolve(ranked('7d','7000000')));
    showPage();
    fireEvent.click(screen.getByRole('tab',{name:'近 7 天'}));
    expect((await screen.findAllByText('7.0M')).length).toBeGreaterThan(0);
    resolveToday(ranked('today','1000000'));
    await waitFor(()=>expect(screen.getAllByText('7.0M').length).toBeGreaterThan(0));
    expect(screen.queryAllByText('1.0M')).toHaveLength(0);
  });
  it('renders precomputed community totals in the hero', async () => {
    vi.mocked(api.getCommunityStats).mockResolvedValue({
      metricDate:'2026-09-09', timezone:'UTC',
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
    expect(screen.getByText('↑ +12.6% vs 昨日')).toBeInTheDocument();
    expect(screen.getByText('↓ −50.0%')).toBeInTheDocument();
    expect(screen.getByText('Zcode')).toBeInTheDocument();
    expect(screen.getByText('Codex CLI')).toBeInTheDocument();
    expect(screen.getByText('64%')).toBeInTheDocument();
    expect(screen.getByText('社区今日 Token 占比 · 按 harness')).toBeInTheDocument();
  });
  it('lists community multi-currency costs instead of a fake $8.00', async () => {
    vi.mocked(api.getCommunityStats).mockResolvedValue({
      metricDate:'2026-09-09', timezone:'UTC',
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
