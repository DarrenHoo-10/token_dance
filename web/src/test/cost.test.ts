import { describe, expect, it } from 'vitest';
import { formatCommunityCost, formatCost, formatCostList, formatPersonalCost } from '@/utils/cost';

describe('cost formatting', () => {
  it('shows known zero cost while leaving unpriced cost unknown', () => {
    expect(formatPersonalCost(
      { amount: '0.00000000', currency: 'USD', supported: true },
      [{ amount: '0.00000000', currency: 'USD', supported: true }],
    ).value).toBe('$0.00');
    expect(formatPersonalCost(
      { amount: null, currency: 'USD', supported: false },
      [{ amount: null, currency: 'USD', supported: false }],
    ).supported).toBe(false);
  });

  it('keeps a single USD amount as a dollar symbol', () => {
    expect(formatCost('1', 'USD')).toBe('$1.00');
    expect(formatCost('275.31', 'USD')).toBe('$275.31');
    expect(formatCostList([{ amount: '1.00000000', currency: 'USD', supported: true }])).toBe('$1.00');
  });

  it('uses currency symbols instead of ISO codes', () => {
    expect(formatCost('7', 'CNY')).toBe('¥7.00');
    expect(formatCost('7', 'RMB')).toBe('¥7.00');
    expect(formatCost('12.5', 'EUR')).toBe('€12.50');
    expect(formatCost('9', 'GBP')).toBe('£9.00');
  });

  it('lists multi-currency totals with symbols instead of summing them', () => {
    const value = formatCostList([
      { amount: '1.00000000', currency: 'USD', supported: true },
      { amount: '7.00000000', currency: 'CNY', supported: true },
    ]);
    expect(value).toBe('$1.00 · ¥7.00');
    expect(value).not.toContain('USD');
    expect(value).not.toContain('CNY');
    expect(value).not.toContain('$8');
  });

  it('uses estimatedCosts when the scalar amount is null', () => {
    const shown = formatPersonalCost(
      { amount: null, currency: null, supported: true },
      [
        { amount: '1.00000000', currency: 'USD', supported: true },
        { amount: '7.00000000', currency: 'CNY', supported: true },
      ],
    );
    expect(shown.supported).toBe(true);
    expect(shown.value).toBe('$1.00 · ¥7.00');
  });

  it('omits a mixed community scalar instead of showing $8.00', () => {
    expect(formatCommunityCost({
      costAmount: null,
      costs: [{ amount: 1, currency: 'USD' }, { amount: 7, currency: 'CNY' }],
    })).toBe('$1.00 · ¥7.00');
    expect(formatCommunityCost({ costAmount: 268.42 })).toBe('$268.42');
    expect(formatCommunityCost({})).toBe('—');
  });

  it('falls back to the code for unknown currencies', () => {
    expect(formatCost('3', 'UNK')).toBe('UNK 3.00');
  });
});
