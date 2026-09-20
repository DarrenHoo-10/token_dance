import { describe, expect, it } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import { hasRankedTokens, personalTokenRank } from '@/components/analytics/tokenRanking';
import type { PersonalSummary } from '@/types/api';

describe('Ranking requires positive usage', () => {
  it('does not treat missing, invalid, or zero usage as ranked', () => {
    for (const value of ['0', '', null, undefined, '-1', 'NaN', 'Infinity']) expect(hasRankedTokens(value)).toBe(false);
    expect(hasRankedTokens('1')).toBe(true);
    expect(hasRankedTokens('23100000000')).toBe(true);
  });

  it('uses the ranking entry rather than a different metric period', () => {
    const summary = { ranking: { rank: 10, entry: { metricValue: '0' } }, metrics: { totalTokens: { value: '999' } } } as PersonalSummary;
    expect(personalTokenRank(summary)).toBeNull();
    summary.ranking.entry!.metricValue = '5';
    summary.metrics.totalTokens.value = '0';
    expect(personalTokenRank(summary)).toBe(10);
    expect(personalTokenRank(null)).toBeNull();
  });

  it('keeps zero-usage developers discoverable without giving them ranks or movements', () => {
    render(<LocaleProvider><MemoryRouter><LeaderboardTable entries={[
      { handle: 'active', displayName: 'Active', avatarUrl: null, rankNo: 1, metricValue: '5', rankDelta: 2 },
      { handle: 'new', displayName: 'New', avatarUrl: null, rankNo: 2, metricValue: '0', rankDelta: 7 },
    ]} /></MemoryRouter></LocaleProvider>);
    const row = screen.getByRole('link', { name: 'New' }).closest('tr')!;
    expect(within(row).getByText('暂未上榜')).toBeInTheDocument();
    expect(within(row).queryByLabelText('上升 7 名')).not.toBeInTheDocument();
    expect(screen.getByLabelText('上升 2 名')).toBeInTheDocument();
  });
});
