import { describe, it, expect, vi, afterEach } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { ActivityCalendar, formatCalendarCompact } from '@/components/analytics/ActivityCalendar';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { LocaleProvider } from '@/context/LocaleContext';
import { TeamSharingCard } from '@/pages/teams/TeamSharingCard';

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
    expect(zero).toHaveAccessibleName('2026-02-01：0 Token');
    expect(screen.getByRole('button', { name: /2026-02-02/ })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '上个月' }));
    expect(screen.getByRole('button', { name: '2026-01-31：2100 Token' })).toHaveAttribute('aria-pressed', 'true');
    expect(screen.getByRole('button', { name: '上个月' })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: '下个月' }));
    expect(screen.getByRole('button', { name: '2026-02-28：8400 Token' })).toHaveAttribute('aria-pressed', 'true');
  });
  it('prints real compact usage on used days and keeps empty days unmarked', () => {
    render(<LocaleProvider><ActivityCalendar days={[
      { date: '2026-02-01', level: 2, tokenTotal: '1200000' },
      { date: '2026-02-02', level: 3, tokenTotal: '373000' },
      { date: '2026-02-03', level: 0, tokenTotal: '0' },
    ]} streakDays={12} /></LocaleProvider>);
    const usedMillion = screen.getByRole('button', { name: /2026-02-01/ });
    const usedThousand = screen.getByRole('button', { name: /2026-02-02/ });
    const zero = screen.getByRole('button', { name: /2026-02-03/ });
    const missing = screen.getByRole('button', { name: /2026-02-04/ });
    expect(usedMillion).toHaveTextContent('1.2M');
    expect(usedThousand).toHaveTextContent('373K');
    expect(zero).not.toHaveTextContent('0K');
    expect(zero.querySelector('.calendar-day-usage')).toBeNull();
    expect(missing).toBeDisabled();
    expect(missing.querySelector('.calendar-day-usage')).toBeNull();
    expect(screen.getByText('连续 12 天')).toBeInTheDocument();
    expect(document.querySelector('.streak-badge')).toBeTruthy();
  });
  it('formats calendar compact totals like other product numbers', () => {
    expect(formatCalendarCompact(1_200_000)).toBe('1.2M');
    expect(formatCalendarCompact(373_000)).toBe('373K');
    expect(formatCalendarCompact(3_000_000)).toBe('3M');
    expect(formatCalendarCompact(42)).toBe('42');
    expect(formatCalendarCompact(0)).toBe('');
    expect(formatCalendarCompact(-8)).toBe('');
    expect(formatCalendarCompact(Number.NaN)).toBe('');
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
  it('explains that joining a team always shares usage', () => {
    render(<LocaleProvider><TeamSharingCard /></LocaleProvider>);
    expect(screen.getByText('加入团队后，用量会自动计入团队统计。')).toBeInTheDocument();
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();
  });
});
