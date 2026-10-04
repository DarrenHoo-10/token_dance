import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { compactNumber, formatRatioPercent } from '@/utils/formatNumber';
import { copyText } from '@/utils/clipboard';
import { formatCalendarCompact } from '@/components/analytics/ActivityCalendar';
import { ChangeBadge } from '@/components/common/ChangeBadge';
import { DeltaChip } from '@/pages/public/LeaderboardPage';
import { readHomeBoard, readHomeBoardTotal, writeHomeBoard } from '@/utils/publicHomeCache';

const label = (n: number, options?: Parameters<typeof compactNumber>[1]) => {
  const c = compactNumber(n, options);
  return c ? `${c.value}${c.unit}` : null;
};

describe('compactNumber', () => {
  it('scales into K, M and B', () => {
    expect(label(1_500)).toBe('1.5K');
    expect(label(83_000_000)).toBe('83.0M');
    expect(label(1_000_000_000)).toBe('1.0B');
  });

  it('carries into the next unit instead of showing 1000.0K', () => {
    expect(label(999_949)).toBe('999.9K');
    expect(label(999_950)).toBe('1.0M');
    expect(label(999_999)).toBe('1.0M');
    expect(label(999_950_000)).toBe('1.0B');
    expect(label(999_999, { decimals: 2, kDecimals: 1 })).toBe('1.00M');
  });

  it('leaves small numbers to the caller', () => {
    expect(compactNumber(0)).toBeNull();
    expect(compactNumber(999)).toBeNull();
    expect(label(999.6)).toBe('1.0K');
    expect(compactNumber(Number.NaN)).toBeNull();
  });

  it('keeps calendar labels compact', () => {
    expect(formatCalendarCompact(999_999)).toBe('1M');
    expect(formatCalendarCompact(1_200_000)).toBe('1.2M');
  });
});

describe('formatRatioPercent', () => {
  it('treats API values as 0–1 ratios', () => {
    expect(formatRatioPercent('0.684')).toBe('68.4%');
    expect(formatRatioPercent('1')).toBe('100.0%');
    expect(formatRatioPercent('0.01')).toBe('1.0%');
    expect(formatRatioPercent(0)).toBe('0.0%');
  });

  it('does not guess for values outside the contract', () => {
    expect(formatRatioPercent('42')).toBeNull();
    expect(formatRatioPercent('-0.1')).toBeNull();
    expect(formatRatioPercent('')).toBeNull();
    expect(formatRatioPercent(null)).toBeNull();
    expect(formatRatioPercent('abc')).toBeNull();
  });
});

describe('change indicators', () => {
  it('shows a flat delta with no arrow or sign at zero', () => {
    const { container } = render(<DeltaChip value={0} suffix="vs prior" />);
    expect(container.textContent).toBe('0.0% vs prior');
    expect(container.firstElementChild).toHaveClass('flat');
  });

  it('treats values that round to zero as flat', () => {
    const { container } = render(<DeltaChip value={0.04} />);
    expect(container.textContent).toBe('0.0%');
    expect(container.firstElementChild).toHaveClass('flat');
  });

  it('keeps arrows and signs for real changes', () => {
    const up = render(<DeltaChip value={12.34} />);
    expect(up.container.textContent).toBe('↑ +12.3%');
    const down = render(<DeltaChip value={-3} />);
    expect(down.container.textContent).toBe('↓ −3.0%');
  });

  it('badges use the rounded value for direction', () => {
    render(<ChangeBadge value={0.02} en />);
    expect(screen.getByLabelText('flat, 0 percent')).toBeInTheDocument();
  });
});

describe('home board cache', () => {
  beforeEach(() => { localStorage.clear(); });

  it('remembers the real participant count, not the number of cached rows', () => {
    const rows = Array.from({ length: 10 }, (_, i) => ({ rankNo: i + 1, handle: `u${i}`, displayName: `U${i}`, avatarUrl: null, metricValue: '5' }));
    writeHomeBoard('board:today', rows, 137);
    expect(readHomeBoard('board:today')).toHaveLength(10);
    expect(readHomeBoardTotal('board:today')).toBe(137);
  });

  it('reports no total when none was stored', () => {
    expect(readHomeBoardTotal('board:none')).toBeNull();
  });
});

describe('copyText', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('reports success from the Clipboard API', async () => {
    const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
    await expect(copyText('hello')).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith('hello');
  });

  it('reports failure when both the API and the legacy path fail', async () => {
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('denied'));
    document.execCommand = vi.fn().mockReturnValue(false);
    await expect(copyText('hello')).resolves.toBe(false);
  });
});
