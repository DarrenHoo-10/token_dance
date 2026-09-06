import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';

describe('leaderboard rank comparisons', () => {
  it('distinguishes unchanged, new, unavailable, up and down', () => {
    render(<LocaleProvider><MemoryRouter><LeaderboardTable entries={[
      { rankNo: 1, handle: 'up', displayName: 'Up', avatarUrl: null, metricValue: '100', rankDelta: 2 },
      { rankNo: 2, handle: 'down', displayName: 'Down', avatarUrl: null, metricValue: '90', rankDelta: -1 },
      { rankNo: 3, handle: 'same', displayName: 'Same', avatarUrl: null, metricValue: '80', rankDelta: 0 },
      { rankNo: 4, handle: 'new', displayName: 'New', avatarUrl: null, metricValue: '70', isNew: true },
      { rankNo: 5, handle: 'unknown', displayName: 'Unknown', avatarUrl: null, metricValue: '60' },
    ]} /></MemoryRouter></LocaleProvider>);
    expect(screen.getByLabelText('上升 2 名')).toBeInTheDocument();
    expect(screen.getByLabelText('下降 1 名')).toBeInTheDocument();
    expect(screen.getByText('持平')).toBeInTheDocument();
    expect(screen.getByText('新上榜')).toBeInTheDocument();
    expect(screen.getByText('暂无对比')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '较昨日' })).toBeInTheDocument();
  });
});
