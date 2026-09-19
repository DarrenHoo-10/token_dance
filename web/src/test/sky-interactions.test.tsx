import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { LocaleProvider } from '@/context/LocaleContext';
import { api, ApiError } from '@/api/client';
import { teamsApi, type MySharingResponse } from '@/api/teams';
import { TeamSharingCard } from '@/pages/teams/TeamSharingCard';
import { renderTeams, sampleScope, signedInUser } from './teams-test-helpers';

afterEach(() => vi.restoreAllMocks());
describe('Sky analytics interactions', () => {
  it('navigates real calendar months and distinguishes zero usage from missing records', () => {
    render(<LocaleProvider><ActivityCalendar days={[
      { date: '2026-01-31', level: 2, tokenTotal: '2100' },
      { date: '2026-02-01', level: 0, tokenTotal: '0' },
      { date: '2026-02-28', level: 4, tokenTotal: '8400' },
    ]} /></LocaleProvider>);
    expect(screen.getByRole('button', { name: '下个月' })).toBeDisabled();
    const zero = screen.getByRole('button', { name: /2026-02-01/ });
    expect(zero).toBeEnabled();
    fireEvent.click(zero);
    expect(screen.getByText('0 Token')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /2026-02-02/ })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '上个月' }));
    expect(screen.getByText('2,100 Token')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '上个月' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '下个月' }));
    expect(screen.getByText('8,400 Token')).toBeInTheDocument();
  });
  it('supports keyboard trend selection and clamps it after the date range shrinks', () => {
    const view = (trends: { date: string; tokenTotal: string }[]) => <LocaleProvider><TokenTrendChart trends={trends} /></LocaleProvider>;
    const { rerender } = render(view([{date:'2026-09-01',tokenTotal:'10'}, {date:'2026-09-02',tokenTotal:'20'}, {date:'2026-09-03',tokenTotal:'30'}]));
    fireEvent.change(screen.getByRole('slider'), {target:{value:'1'}});
    expect(screen.getByRole('slider')).toHaveAttribute('aria-valuetext', '2026-09-02: 20 Token');
    rerender(view([{date:'2026-09-19',tokenTotal:'0'}]));
    expect(screen.getByRole('slider')).toHaveAttribute('aria-valuetext', '2026-09-19: 0 Token');
    expect(screen.getByRole('img')).not.toHaveAttribute('aria-label', expect.stringContaining('NaN'));
  });
});

describe('Team sharing persistence', () => {
  const initial: MySharingResponse = {membershipId:'tmb_0123456789abcdefghijklmnop',sharingVersion:'1',authRevision:'1',sharing:{base:true,named:true,classification:true,cost:true}};
  function setup() {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getMySharing').mockResolvedValue(initial);
    return renderTeams(<TeamSharingCard />);
  }
  it('turns off dependent sharing flags and uses the returned version on the next save', async () => {
    const patch = vi.spyOn(teamsApi, 'updateMySharing').mockImplementation(async (_id, body) => ({...initial,sharing:body.sharing,sharingVersion:String(Number(body.expectedSharingVersion)+1),authRevision:'2'}));
    setup();
    const switches = await screen.findAllByRole('checkbox');
    fireEvent.click(switches[0]);
    await waitFor(() => expect(switches[0]).not.toBeChecked());
    expect(patch).toHaveBeenLastCalledWith(sampleScope().team.id, {expectedSharingVersion:'1',sharing:{base:false,named:false,classification:false,cost:false}}, expect.objectContaining({signal:expect.any(AbortSignal)}));
    expect(switches[1]).toBeDisabled();
    fireEvent.click(switches[0]);
    await waitFor(() => expect(switches[0]).toBeChecked());
    expect(patch).toHaveBeenLastCalledWith(sampleScope().team.id, {expectedSharingVersion:'2',sharing:{base:true,named:true,classification:false,cost:false}}, expect.anything());
  });
  it('keeps the confirmed values when saving fails and reloads before retrying', async () => {
    const patch = vi.spyOn(teamsApi, 'updateMySharing').mockRejectedValue(new ApiError(409,{code:'CONFLICT',messageKey:'errors.unknown'}));
    setup();
    const switches = await screen.findAllByRole('checkbox');
    fireEvent.click(switches[0]);
    expect(await screen.findByRole('alert')).toBeInTheDocument();
    expect(switches[0]).toBeChecked();
    fireEvent.click(screen.getByRole('button', {name:'重试'}));
    await waitFor(() => expect(teamsApi.getMySharing).toHaveBeenCalledTimes(2));
    expect(patch).toHaveBeenCalledTimes(1);
  });
});

