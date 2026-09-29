// Shared number formatting for token counts and rates.

const UNITS = [
  { scale: 1_000_000_000, unit: 'B' },
  { scale: 1_000_000, unit: 'M' },
  { scale: 1_000, unit: 'K' },
] as const;

export type CompactUnit = (typeof UNITS)[number]['unit'];

export interface CompactOptions {
  /** Decimals for M and B. */
  decimals?: number;
  /** Decimals for K; defaults to `decimals`. */
  kDecimals?: number;
}

/**
 * Splits a number into a scaled value and a K/M/B unit, carrying into the next unit
 * when rounding reaches 1000 (999,999 → 1.0M, not 1000.0K).
 * Returns null below 1,000 (after rounding) so callers keep their own small-number format.
 */
export function compactNumber(num: number, { decimals = 1, kDecimals = decimals }: CompactOptions = {}): { value: string; unit: CompactUnit } | null {
  if (!Number.isFinite(num)) return null;
  const abs = Math.abs(num);
  for (let i = 0; i < UNITS.length; i += 1) {
    const { scale, unit } = UNITS[i];
    if (abs < scale) continue;
    const digits = unit === 'K' ? kDecimals : decimals;
    if (Number((abs / scale).toFixed(digits)) >= 1000 && i > 0) {
      const larger = UNITS[i - 1];
      const largerDigits = larger.unit === 'K' ? kDecimals : decimals;
      return { value: (num / larger.scale).toFixed(largerDigits), unit: larger.unit };
    }
    return { value: (num / scale).toFixed(digits), unit };
  }
  if (Math.round(abs) >= 1000) return { value: (num / 1_000).toFixed(kDecimals), unit: 'K' };
  return null;
}

/** Cache hit rate and similar ratios are served as 0–1 decimals (see MetricDecimal in the user API). */
export function formatRatioPercent(value: string | number | null | undefined, decimals = 1): string | null {
  if (value == null || value === '') return null;
  const ratio = typeof value === 'number' ? value : Number(value);
  if (!Number.isFinite(ratio) || ratio < 0 || ratio > 1) return null;
  return `${(ratio * 100).toFixed(decimals)}%`;
}
