import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { LocaleProvider } from '@/context/LocaleContext';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { trendTimeAxis } from '@/components/analytics/trendTimeAxis';

const sparse = [
  { date: '2026-09-20 22:00', tokenTotal: '433500' },
  { date: '2026-09-19 23:00', tokenTotal: '20000000' },
  { date: '2026-09-20 00:00', tokenTotal: '39000000' },
  { date: '2026-09-20 01:00', tokenTotal: '100' },
];

describe('Trend time axis', () => {
  it('preserves the 21-hour gap and shows dates across midnight', () => {
    const axis = trendTimeAxis(sparse, 7);
    expect(axis.points.map(point => point.date)).toEqual([sparse[1].date, sparse[2].date, sparse[3].date, sparse[0].date]);
    expect(axis.span).toBe(23 * 3600000);
    expect((axis.points[3].time - axis.points[2].time) / (axis.points[2].time - axis.points[1].time)).toBe(21);
    expect(axis.ticks[0].label).toBe('09.19 23:00');
    expect(axis.ticks.some(tick => tick.label.startsWith('09.20 '))).toBe(true);
    expect(axis.ticks.at(-1)?.label).toBe('22:00');
    expect(axis.ticks.length).toBeLessThanOrEqual(7);
    expect(trendTimeAxis(sparse, 4).ticks.length).toBeLessThanOrEqual(4);
  });

  it('positions the plotted points and pointer selection by time, not index', () => {
    const { container } = render(<LocaleProvider><TokenTrendChart trends={sparse} /></LocaleProvider>);
    const svg = screen.getByRole('img');
    vi.spyOn(svg, 'getBoundingClientRect').mockReturnValue({ left: 0, width: 700 } as DOMRect);
    // At x=50 the closest point is 00:00 (x≈38), not the first record.
    fireEvent(svg, new MouseEvent('pointermove', { clientX: 50, bubbles: true }));
    expect(screen.getByRole('slider')).toHaveAttribute('aria-valuetext', '2026-09-20 00:00: 39,000,000 Token');
    const firstX = 8;
    expect(Number(container.querySelector('circle')?.getAttribute('cx')) - firstX).toBeCloseTo(684 / 23);
    fireEvent.change(screen.getByRole('slider'), { target: { value: '2' } });
    expect(screen.getByRole('slider')).toHaveAttribute('aria-valuetext', '2026-09-20 01:00: 100 Token');
    expect(Number(container.querySelector('circle')?.getAttribute('cx')) - firstX).toBeCloseTo(2 * 684 / 23);
  });

  it('handles ISO offsets, sparse days/months, one point, and invalid data', () => {
    const iso = trendTimeAxis([{ date: '2026-09-20T00:00:00Z', tokenTotal: '1' }], 7);
    expect(iso.ticks[0].label).toBe('09.20 08:00');
    expect(iso.span).toBe(0);
    const days = trendTimeAxis([{ date: '2026-09-01' }, { date: '2026-09-03' }], 7);
    expect(days.ticks.map(tick => tick.label)).toEqual(['09.01', '09.02', '09.03']);
    const months = trendTimeAxis([{ date: '2026-01' }, { date: '2026-04' }], 7);
    expect(months.ticks.map(tick => tick.label)).toEqual(['2026-01', '2026-02', '2026-03', '2026-04']);
    expect(trendTimeAxis([{ date: 'invalid' }], 7).points).toEqual([]);
  });
});
