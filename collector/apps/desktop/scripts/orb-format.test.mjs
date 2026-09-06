import test from 'node:test';
import assert from 'node:assert/strict';
import { formatCompactTokens, formatTodayTokens, parseTokenCount } from '../src/orb/format.ts';
import { classifyOrbEvent, isNewerRevision, isPulsePlayable } from '../src/orb/types.ts';

test('compact BigInt format keeps two decimals for millions', () => {
  assert.equal(formatCompactTokens(5100000n), '5.10M');
  assert.equal(formatCompactTokens(BigInt('5100000')), '5.10M');
  assert.equal(formatTodayTokens('5100000', 'known'), '5.10M');
  assert.equal(formatCompactTokens(0n), '0');
  assert.equal(formatCompactTokens(999n), '999');
  assert.equal(formatCompactTokens(1000n), '1.00K');
});

test('missing today records stay em-dash while confirmed zero is 0', () => {
  assert.equal(formatTodayTokens(null, 'known'), '—');
  assert.equal(formatTodayTokens(undefined, 'known'), '—');
  assert.equal(formatTodayTokens('0', 'known'), '0');
  assert.equal(formatTodayTokens('5100000', 'unknown'), '—');
  assert.equal(formatTodayTokens('5100000', 'error'), '—');
  assert.equal(parseTokenCount('5100000'), 5100000n);
  assert.equal(parseTokenCount(null), null);
});

test('huge totals never go through Number', () => {
  const unsafe = 9007199254740993n;
  assert.notEqual(unsafe.toString(), Number(unsafe).toString());
  assert.equal(formatCompactTokens(unsafe), '9.01P');
  assert.equal(formatCompactTokens(10n ** 20n), '100.00E');
  assert.equal(formatTodayTokens((10n ** 24n).toString(), 'known'), '1.00Y');
  assert.equal(formatCompactTokens(-(10n ** 18n)), '-1.00E');
});

test('old revisions and other streams are rejected until a command resyncs', () => {
  assert.equal(classifyOrbEvent('alpha', '4', 'alpha', '5'), 'accept');
  assert.equal(classifyOrbEvent('alpha', '5', 'alpha', '5'), 'drop');
  assert.equal(classifyOrbEvent('alpha', '5', 'beta', '1'), 'resync');
  assert.equal(isNewerRevision('100', '99'), false);
  assert.equal(isPulsePlayable({ id: 'p1', kind: 'usage', expiresAtMs: 10 }, 11), false);
  assert.equal(isPulsePlayable({ id: 'p1', kind: 'usage', expiresAtMs: 12 }, 11, 'p1'), false);
  assert.equal(isPulsePlayable({ id: 'p2', kind: 'low_quota', expiresAtMs: 12 }, 11, 'p1'), true);
});
