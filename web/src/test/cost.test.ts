import { describe, expect, it } from 'vitest';
import { formatCommunityCost, formatCost, formatCostList, formatPersonalCost } from '@/utils/cost';

describe('cost formatting', () => {
  it('keeps a single USD amount as a dollar scalar', () => {
    expect(formatCost('1', 'USD')).toBe('$1.00');
    expect(formatCostList([{ amount: '1.00000000', currency: 'USD', supported: true }])).toBe('$1.00');
  });

  it('lists multi-currency totals instead of summing them as USD', () => {
    const value = formatCostList([
      { amount: '1.00000000', currency: 'USD', supported: true },
      { amount: '7.00000000', currency: 'CNY', supported: true },
    ]);
    expect(value).toBe('USD 1.00 · CNY 7.00');
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
    expect(shown.value).toBe('USD 1.00 · CNY 7.00');
  });

  it('omits a mixed community scalar instead of showing $8.00', () => {
    expect(formatCommunityCost({
      costAmount: null,
      costs: [{ amount: 1, currency: 'USD' }, { amount: 7, currency: 'CNY' }],
    })).toBe('USD 1.00 · CNY 7.00');
    expect(formatCommunityCost({ costAmount: 268.42 })).toBe('$268.42');
    expect(formatCommunityCost({})).toBe('—');
  });
});
