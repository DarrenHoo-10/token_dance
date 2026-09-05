import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardListPage } from '@/pages/public/LeaderboardListPage';
import { api } from '@/api/client';
const entry={rankNo:1,handle:'only_user',displayName:'Only User',avatarUrl:null,metricValue:'123456'};
const board={snapshotId:'',boardKey:'global',window:'today',metric:'tokens',entries:[entry],totalEntries:1};
function showList(route='/leaderboard/list') {render(<LocaleProvider><MemoryRouter initialEntries={[route]} future={{v7_startTransition:true,v7_relativeSplatPath:true}}><LeaderboardListPage /></MemoryRouter></LocaleProvider>);}
beforeEach(()=>{vi.restoreAllMocks();localStorage.clear();vi.spyOn(api,'getLeaderboard').mockResolvedValue(board);});
describe('Top 1000 list',()=>{
  it('renders a table even for a single first-place user with a profile link',async()=>{
    showList();const table=await screen.findByRole('table',{name:'排行榜列表'});
    expect(within(table).getAllByRole('row')).toHaveLength(2);
    expect(within(table).getByRole('link',{name:/Only User/})).toHaveAttribute('href','/u/only_user');
    expect(screen.getByRole('button',{name:'下一页'})).toBeDisabled();
  });
  it('paginates and resets the cursor when the period changes',async()=>{
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board,totalEntries:40,nextCursor:'20'});
    showList();fireEvent.click(await screen.findByRole('button',{name:'下一页'}));
    await waitFor(()=>expect(api.getLeaderboard).toHaveBeenLastCalledWith({window:'today',cursor:'20',limit:20}));
    fireEvent.click(screen.getByRole('tab',{name:'近 7 天'}));
    await waitFor(()=>expect(api.getLeaderboard).toHaveBeenLastCalledWith({window:'7d',cursor:undefined,limit:20}));
  });
  it('stops at the top 1000 boundary even if the API supplies another cursor',async()=>{
    vi.mocked(api.getLeaderboard).mockResolvedValue({...board,totalEntries:1005,entries:[{...entry,rankNo:1000}],nextCursor:'1000'});
    showList('/leaderboard/list?cursor=980');
    expect(await screen.findByText('共 1000 位开发者')).toBeInTheDocument();expect(screen.getByRole('button',{name:'下一页'})).toBeDisabled();
  });
});
