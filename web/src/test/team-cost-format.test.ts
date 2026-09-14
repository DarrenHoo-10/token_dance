import { describe, expect, it } from 'vitest';
import { formatDecimalAmount } from '@/pages/teams/teamUtils';

describe('team money display', () => {
  it('keeps two decimals by truncating without floating-point rounding', () => {
    expect(formatDecimalAmount('36.35990000', 'USD')).toBe('$36.35');
    expect(formatDecimalAmount('1', 'USD')).toBe('$1.00');
    expect(formatDecimalAmount('0.00999999', 'USD')).toBe('$0.00');
    expect(formatDecimalAmount('-0.00999999', 'USD')).toBe('$0.00');
    expect(formatDecimalAmount('12345678901234567890.99999999', 'EUR')).toBe('12,345,678,901,234,567,890.99 EUR');
  });
});
