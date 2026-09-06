import { describe, it, expect } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';

describe('leaderboard rank comparisons', () => {
  it('shows arrows only for real rank movement and leaves ties or missing baselines empty', () => {
    render(<LocaleProvider><MemoryRouter><LeaderboardTable entries={[
      { rankNo: 1, handle: 'up', displayName: 'Up', avatarUrl: null, metricValue: '100', rankDelta: 2 },
      { rankNo: 2, handle: 'down', displayName: 'Down', avatarUrl: null, metricValue: '90', rankDelta: -1 },
      { rankNo: 3, handle: 'same', displayName: 'Same', avatarUrl: null, metricValue: '80', rankDelta: 0 },
      { rankNo: 4, handle: 'new', displayName: 'New', avatarUrl: null, metricValue: '70', isNew: true },
      { rankNo: 5, handle: 'unknown', displayName: 'Unknown', avatarUrl: null, metricValue: '60' },
    ]} /></MemoryRouter></LocaleProvider>);
    expect(screen.getByLabelText('上升 2 名')).toBeInTheDocument();
    expect(screen.getByLabelText('下降 1 名')).toBeInTheDocument();
    expect(screen.queryByText('持平')).not.toBeInTheDocument();
    expect(screen.queryByText('Unchanged')).not.toBeInTheDocument();
    expect(screen.queryByText('暂无对比')).not.toBeInTheDocument();
    expect(screen.queryByText('No comparison')).not.toBeInTheDocument();
    expect(screen.queryByText('新上榜')).not.toBeInTheDocument();
    expect(screen.queryByText('−')).not.toBeInTheDocument();
    const rows = screen.getAllByRole('row');
    expect(within(rows[3]).getAllByRole('cell')[3]).toBeEmptyDOMElement();
    expect(within(rows[4]).getAllByRole('cell')[3]).toBeEmptyDOMElement();
    expect(within(rows[5]).getAllByRole('cell')[3]).toBeEmptyDOMElement();
    expect(screen.getByRole('columnheader', { name: '较昨日' })).toBeInTheDocument();
  });
});
