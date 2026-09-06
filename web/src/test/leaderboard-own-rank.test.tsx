import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardListPage } from '@/pages/public/LeaderboardListPage';
import { api } from '@/api/client';
const auth = vi.hoisted(() => ({ authenticated:true,user:{userId:'me',handle:'me',displayName:'Me'} }));
vi.mock('@/context/AuthContext',()=>({useAuth:()=>auth}));
vi.mock('@/api/client',()=>({api:{getLeaderboard:vi.fn(),getMyLeaderboard:vi.fn(),getLeaderboardView:vi.fn()}}));
const own = {rankNo:1432,handle:'me',displayName:'Me',avatarUrl:null,metricValue:'8620000',rankDelta:26};
const publicBoard = {entries:[{...own,rankNo:1,handle:'leader',displayName:'Leader'}],totalEntries:12486,totalParticipants:12486,nextCursor:'20',ownEntry:null};
function page() { return render(<LocaleProvider><MemoryRouter><LeaderboardListPage /></MemoryRouter></LocaleProvider>); }
describe('personal leaderboard footer',()=>{
  beforeEach(()=>{
    vi.clearAllMocks();auth.authenticated=true;
    vi.mocked(api.getLeaderboardView).mockImplementation(async (signedIn, params) => {
      if (signedIn) return api.getMyLeaderboard(params);
      return api.getLeaderboard(params);
    });
    vi.mocked(api.getMyLeaderboard).mockResolvedValue({...publicBoard, ownEntry: own} as never);
    vi.mocked(api.getLeaderboard).mockResolvedValue(publicBoard as never);
  });
  it('shows the uncapped participant total and own rank as the last row',async()=>{
    const {container}=page();
    await screen.findByRole('row',{name:'我的排名'});
    expect(screen.getByText('12,486')).toBeInTheDocument();
    expect(container.querySelector('tbody tr:last-child')).toHaveAccessibleName('我的排名');
    expect(screen.getByText('1432')).toBeInTheDocument();
    expect(screen.getByText('第 1 / 50 页')).toBeInTheDocument();
  });
  it('does not duplicate users inside the top 1000 or fetch private data when signed out',async()=>{
    vi.mocked(api.getMyLeaderboard).mockResolvedValue({...publicBoard, ownEntry: {...own, rankNo: 1000}} as never);
    const first=page();await screen.findByText('12,486');
    expect(screen.queryByRole('row',{name:'我的排名'})).not.toBeInTheDocument();
    first.unmount();vi.clearAllMocks();auth.authenticated=false;
    vi.mocked(api.getLeaderboardView).mockImplementation(async (signedIn, params) => {
      if (signedIn) return api.getMyLeaderboard(params);
      return api.getLeaderboard(params);
    });
    vi.mocked(api.getLeaderboard).mockResolvedValue(publicBoard as never);
    page();await screen.findByText('12,486');
    expect(api.getMyLeaderboard).not.toHaveBeenCalled();
  });
  it('clears the previous period personal row when the next period has no rank',async()=>{
    page();await screen.findByRole('row',{name:'我的排名'});
    vi.mocked(api.getMyLeaderboard).mockResolvedValue({...publicBoard, window: '7d', ownEntry: null} as never);
    fireEvent.click(screen.getByRole('tab',{name:'近 7 天'}));
    await waitFor(()=>expect(api.getMyLeaderboard).toHaveBeenLastCalledWith({window:'7d',cursor:undefined,limit:20}));
    expect(screen.queryByRole('row',{name:'我的排名'})).not.toBeInTheDocument();
  });
});
