import test from 'node:test';
import assert from 'node:assert/strict';
import { lastSevenDays, weeklyUsage } from '../src/weekly-usage.ts';

const now = new Date(2026, 0, 3, 0, 5);
const dates = lastSevenDays(now);
const agent = (id, values) => ({ id, accuracy: 'exact', totalTokens: 999999999, dailyUsage: dates.map((date, i) => ({ date, tokens: values[i] })) });

test('seven local calendar days include today and cross year boundaries', () => {
  assert.deepEqual(dates, ['2025-12-28', '2025-12-29', '2025-12-30', '2025-12-31', '2026-01-01', '2026-01-02', '2026-01-03']);
});

test('weekly totals and trend use daily records, excluding lifetime and old data', () => {
  const first = agent('one', [1, 2, 3, 4, 5, 6, 7]);
  first.dailyUsage.push({ date: '2025-12-27', tokens: 1000 });
  const week = weeklyUsage([first, agent('two', [2, 2, 2, 2, 2, 2, 2])], now);
  assert.equal(week.total, 42);
  assert.equal(week.totals.get('one'), 28);
  assert.deepEqual(week.points.map(point => point.tokens), [3, 4, 5, 6, 7, 8, 9]);
});

test('missing history stays unavailable instead of becoming zero or a lifetime total', () => {
  const week = weeklyUsage([{ id: 'missing', accuracy: 'exact', todayTokens: 9, totalTokens: 100 }], now);
  assert.equal(week.total, null);
  assert.equal(week.totals.get('missing'), null);
  assert.ok(week.points.every(point => point.tokens === null));
});

test('incomplete or unknown agents are excluded from both totals and trend', () => {
  const partial = agent('partial', [1, 2, 3, 4, 5, 6, 7]);
  partial.dailyUsage.pop();
  const week = weeklyUsage([partial, { ...agent('unknown', [9, 9, 9, 9, 9, 9, 9]), accuracy: 'unknown' }, agent('complete', [1, 1, 1, 1, 1, 1, 1])], now);
  assert.equal(week.total, 7);
  assert.equal(week.complete, false);
  assert.ok(week.points.every(point => point.tokens === 1));
});

test('zero usage is valid, while invalid daily values are unavailable', () => {
  assert.equal(weeklyUsage([agent('zero', [0, 0, 0, 0, 0, 0, 0])], now).total, 0);
  for (const invalid of [-1, NaN, Infinity, null]) {
    assert.equal(weeklyUsage([agent('bad', [0, 0, 0, invalid, 0, 0, 0])], now).total, null);
  }
});
