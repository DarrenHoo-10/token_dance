import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import { publicLeaderboardName } from '@/components/analytics/leaderboardName';

describe('publicLeaderboardName', () => {
  it('uses the nickname and only falls back to handle when the nickname is blank', () => {
    expect(publicLeaderboardName({ displayName: '桂林仔', handle: 'dancer_uss9' })).toBe('桂林仔');
    expect(publicLeaderboardName({ displayName: '  ', handle: 'blank_user' })).toBe('blank_user');
    expect(publicLeaderboardName({ displayName: '', handle: 'only_handle' })).toBe('only_handle');
  });
});

describe('LeaderboardTable names', () => {
  it('shows the nickname as the only visible name and keeps the handle in the profile URL', () => {
    render(<LocaleProvider><MemoryRouter><LeaderboardTable entries={[
      { rankNo: 1, handle: 'dancer_uss9', displayName: '桂林仔', avatarUrl: null, metricValue: '16500000' },
      { rankNo: 2, handle: 'empty_handle', displayName: '', avatarUrl: null, metricValue: '10' },
    ]} /></MemoryRouter></LocaleProvider>);
    const table = screen.getByRole('table', { name: '排行榜列表' });
    expect(within(table).getByRole('link', { name: /桂林仔/ })).toHaveAttribute('href', '/u/dancer_uss9');
    expect(within(table).getByRole('link', { name: 'empty_handle' })).toHaveAttribute('href', '/u/empty_handle');
    expect(within(table).queryByText('@dancer_uss9')).not.toBeInTheDocument();
    expect(within(table).queryByText('dancer_uss9')).not.toBeInTheDocument();
    expect(within(table).queryByText('@empty_handle')).not.toBeInTheDocument();
  });
});
