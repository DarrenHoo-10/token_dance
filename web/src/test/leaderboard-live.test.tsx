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
  vi.spyOn(api,'getAgentBreakdowns').mockRejectedValue(new Error('unavailable'));
  vi.spyOn(api,'getLeaderboard').mockResolvedValue(board);
  vi.spyOn(api,'getMyLeaderboard').mockImplementation((params) => api.getLeaderboard(params));
});
describe('Live leaderboard', () => {
  it('explains that a private profile still stays on the board', async () => {
    const update=vi.spyOn(api,'updatePrivacy'); showPage();
    expect(await screen.findByRole('status')).toHaveTextContent('公开开关只控制详细资料页');
    fireEvent.click(screen.getByRole('button',{name:'管理公开设置'}));
    expect(screen.getByRole('heading',{name:'我的数据'})).toBeInTheDocument();
    expect(update).not.toHaveBeenCalled();
  });
  it('uses full board totals returned by the server rather than the loaded top ten', async () => {
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board,totalEntries:25,totalTokens:'9000000'});
    showPage();
    expect(await screen.findByText('9.0M')).toBeInTheDocument();
    expect(screen.getByText('25')).toBeInTheDocument();
    expect(screen.getByText('UTC')).toBeInTheDocument();
  });
  it('ignores a late response from the previously selected period', async () => {
    let resolveToday!: (value: LeaderboardResponse) => void;
    vi.mocked(api.getLeaderboard).mockImplementation(({window}={}) => window==='today' ? new Promise(resolve => {resolveToday=resolve;}) : Promise.resolve({...board,totalTokens:'7000000',window:'7d'}));
    showPage();
    fireEvent.click(screen.getByRole('tab',{name:'近 7 天'}));
    expect(await screen.findByText('7.0M')).toBeInTheDocument();
    resolveToday({...board,totalTokens:'1000000'});
    await waitFor(()=>expect(screen.getByText('7.0M')).toBeInTheDocument());
    expect(screen.queryByText('1.0M')).not.toBeInTheDocument();
  });
  it('connect tools opens device settings instead of claiming an unperformed connection', () => {
    showPage(); fireEvent.click(screen.getByRole('button',{name:'连接工具'}));
    expect(screen.getByRole('heading',{name:'设备设置'})).toBeInTheDocument();
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
    expect(await screen.findByText('ada')).toBeInTheDocument();
    expect(screen.queryByText('持平')).not.toBeInTheDocument();
    expect(screen.queryByText('−')).not.toBeInTheDocument();
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    expect(await screen.findByRole('alert')).toHaveTextContent('连接异常');
    expect(screen.getByText('ada')).toBeInTheDocument();
  });
});
