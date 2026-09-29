import { describe, expect, it } from 'vitest';
import { readdirSync, readFileSync, statSync } from 'node:fs';
import path from 'node:path';
import { translations } from '@/i18n';

function flatten(value: unknown, prefix = ''): Record<string, string> {
  if (typeof value === 'string') return { [prefix]: value };
  if (value && typeof value === 'object') {
    return Object.entries(value).reduce<Record<string, string>>((acc, [key, child]) => (
      Object.assign(acc, flatten(child, prefix ? `${prefix}.${key}` : key))
    ), {});
  }
  return {};
}

const placeholders = (text: string) => [...text.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe('translation dictionaries', () => {
  const zh = flatten(translations['zh-CN']);
  const en = flatten(translations['en-US']);

  it('have the same keys', () => {
    expect(Object.keys(en).filter((key) => !(key in zh))).toEqual([]);
    expect(Object.keys(zh).filter((key) => !(key in en))).toEqual([]);
  });

  it('have no empty strings and the same {placeholders} in both languages', () => {
    for (const key of Object.keys(zh)) {
      expect(zh[key].trim(), `zh-CN ${key} is empty`).not.toBe('');
      expect(en[key]?.trim(), `en-US ${key} is empty`).not.toBe('');
      expect(placeholders(en[key] ?? ''), `placeholders differ in ${key}`).toEqual(placeholders(zh[key]));
    }
  });
});

// Two mechanisms exist: the dictionaries above and inline `zh ? '中文' : 'English'` ternaries.
// New copy should go into the dictionaries; this ratchet stops the inline count from growing.
// When you move copy into the dictionaries, lower the number.
const INLINE_TERNARY_BASELINE = 272; // 270 on main plus the 2 in the mobile menu (#154)

function sourceFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = path.join(dir, name);
    if (statSync(full).isDirectory()) return name === 'test' || name === 'i18n' ? [] : sourceFiles(full);
    return /\.tsx?$/.test(name) ? [full] : [];
  });
}

describe('inline zh/en ternaries', () => {
  it('do not grow beyond the baseline', () => {
    const pattern = /(?:\bzh|\bisZh|locale === 'zh-CN') \? '/g;
    const count = sourceFiles(path.resolve(__dirname, '..'))
      .reduce((sum, file) => sum + (readFileSync(file, 'utf8').match(pattern)?.length ?? 0), 0);
    expect(count, 'Put new copy in src/i18n/locales instead of inline zh ? … : …').toBeLessThanOrEqual(INLINE_TERNARY_BASELINE);
  });
});
