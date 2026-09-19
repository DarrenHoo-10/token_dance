import { describe, expect, it } from 'vitest';
import { USAGE_COLOR_POOL, usageColor, usageColorAt } from '@/utils/usageColors';

describe('usage color pool', () => {
  it('keeps a stable color for the same key', () => {
    expect(usageColor('cursor/grok-4.6')).toBe(usageColor('cursor/grok-4.6'));
    expect(usageColor('openai/gpt-5.6')).not.toBe(usageColor('cursor/grok-4.6'));
  });

  it('returns distinct colors across the first page of the pool', () => {
    const colors = USAGE_COLOR_POOL.map((_, index) => usageColorAt(index));
    expect(new Set(colors).size).toBe(USAGE_COLOR_POOL.length);
  });
});
