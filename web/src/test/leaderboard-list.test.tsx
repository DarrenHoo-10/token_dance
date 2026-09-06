import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardListPage } from '@/pages/public/LeaderboardListPage';
import { api } from '@/api/client';
vi.mock('@/context/AuthContext', () => ({ useAuth: () => ({ authenticated: false, user: null }) }));
const entry={rankNo:1,handle:'only_user',displayName:'Only User',avatarUrl:null,metricValue:'123456'};
const board={snapshotId:'',boardKey:'global',window:'today',metric:'tokens',entries:[entry],totalEntries:1};
function showList(route='/leaderboard/list') {render(<LocaleProvider><MemoryRouter initialEntries={[route]} future={{v7_startTransition:true,v7_relativeSplatPath:true}}><LeaderboardListPage /></MemoryRouter></LocaleProvider>);}
beforeEach(()=>{vi.restoreAllMocks();vi.useRealTimers();localStorage.clear();vi.spyOn(api,'getLeaderboard').mockResolvedValue(board);});
describe('Leaderboard list',()=>{
  it('renders a table even for a single first-place user with a profile link',async()=>{
    showList();const table=await screen.findByRole('table',{name:'排行榜列表'});
    expect(within(table).getAllByRole('row')).toHaveLength(2);
    expect(within(table).getByRole('link',{name:/Only User/})).toHaveAttribute('href','/u/only_user');
    expect(screen.getByRole('button',{name:'下一页'})).toBeDisabled();
    expect(screen.getByRole('heading',{name:'排行榜'})).toBeInTheDocument();
    expect(screen.getByText('总人数')).toBeInTheDocument();
    expect(screen.queryByText(/Top 1000/)).not.toBeInTheDocument();
    expect(screen.queryByText(/仅展示前 1000/)).not.toBeInTheDocument();
  });
  it('paginates and resets the cursor when the period changes',async()=>{
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board,totalEntries:40,nextCursor:'20'});
    showList();fireEvent.click(await screen.findByRole('button',{name:'下一页'}));
    await waitFor(()=>expect(api.getLeaderboard).toHaveBeenLastCalledWith({window:'today',cursor:'20',limit:20}));
    fireEvent.click(screen.getByRole('tab',{name:'近 7 天'}));
    await waitFor(()=>expect(api.getLeaderboard).toHaveBeenLastCalledWith({window:'7d',cursor:undefined,limit:20}));
  });
  it('stops at the public list boundary even if the API supplies another cursor',async()=>{
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board,totalEntries:1005,entries:[{...entry,rankNo:1000}],nextCursor:'1000'});
    showList('/leaderboard/list?cursor=980');
    expect(await screen.findByText('1,005')).toBeInTheDocument();
    expect(screen.getByRole('button',{name:'下一页'})).toBeDisabled();
    expect(screen.queryByText('共 1000 位开发者')).not.toBeInTheDocument();
  });
  it('uses a short empty label when there are no accounts',async()=>{
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board,entries:[],totalEntries:0});
    showList();
    expect(await screen.findByText('暂无账号')).toBeInTheDocument();
    expect(screen.queryByText(/公开排行/)).not.toBeInTheDocument();
  });
  it('keeps rows and shows a short connection error when a background refresh fails',async()=>{
    vi.mocked(api.getLeaderboard)
      .mockResolvedValueOnce(board)
      .mockRejectedValueOnce(new Error('offline'));
    showList();
    expect(await screen.findByText('Only User')).toBeInTheDocument();
    await act(async () => { document.dispatchEvent(new Event('visibilitychange')); });
    expect(await screen.findByRole('alert')).toHaveTextContent('连接异常');
    expect(screen.getByText('Only User')).toBeInTheDocument();
    expect(screen.getByRole('table',{name:'排行榜列表'})).toBeInTheDocument();
  });
  it('refreshes every 30 seconds while the page is visible',async()=>{
    vi.useFakeTimers();
    try {
      showList();
      await act(async()=>{await Promise.resolve();});
      expect(screen.getByText('Only User')).toBeInTheDocument();
      expect(api.getLeaderboard).toHaveBeenCalledTimes(1);
      await act(async()=>{await vi.advanceTimersByTimeAsync(30_000);});
      expect(api.getLeaderboard).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
