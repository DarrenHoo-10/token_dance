import test from 'node:test';
import assert from 'node:assert/strict';
import { usageTokens, usageCosts, annualUsage, quotaStale, quotaStatusText, quotaWindowLabel } from '../src/usage-analytics.ts';
import { lastSevenDays } from '../src/weekly-usage.ts';
const now = new Date(2026, 8, 5, 12);
const dates = lastSevenDays(now);
test('quota labels separate shared weekly usage and Cursor billing pools', () => {
  assert.equal(quotaWindowLabel({label:'shared_week', windowMinutes:10080}, true), '共享周额度');
  assert.equal(quotaWindowLabel({label:'auto', windowMinutes:44640}, true), 'Auto 额度');
  assert.equal(quotaWindowLabel({label:'api', windowMinutes:0}, false), 'API quota');
  assert.equal(quotaWindowLabel({windowMinutes:0}, true), '当前周期额度');
  assert.equal(quotaWindowLabel({windowMinutes:300}, true), '5 小时额度');
  for (const [agentId, name] of [['grok-build','Grok Build'],['cursor','Cursor']]) {
    const message = quotaStatusText({agentId,status:'auth_required',windows:[]}, true);
    assert.ok(message.includes(name)); assert.ok(!message.includes('ZCode'));
  }
});
const agent = { id: 'codex', accuracy: 'exact', todayTokens: 7, totalTokens: 1000,
  dailyUsage: dates.map((date, i) => ({ date, tokens: i + 1, costs: { USD: 10000000 } })), totalCosts: { USD: 200000000 }, historyStart: dates[0] };

test('today, week and all use distinct recorded totals', () => {
  assert.equal(usageTokens(agent, 'today', now), 7);
  assert.equal(usageTokens(agent, 'week', now), 28);
  assert.equal(usageTokens(agent, 'all', now), 1000);
  assert.equal(usageTokens({ ...agent, accuracy: 'unknown' }, 'all', now), null);
});
test('costs preserve currency and unknown versus recorded zero', () => {
  assert.deepEqual(usageCosts([agent], 'all', now).currencies, { USD: 2 });
  assert.equal(usageCosts([agent], 'today', now).currencies.USD, .1);
  assert.ok(Math.abs(usageCosts([agent], 'week', now).currencies.USD - .7) < 1e-10);
  assert.deepEqual(usageCosts([{ ...agent, totalCosts: { CNY: 0 } }, agent], 'all', now).currencies, { CNY: 0, USD: 2 });
  assert.deepEqual(usageCosts([{ ...agent, totalCosts: undefined }], 'all', now).currencies, {});
});
test('annual calendar does not fill unrecorded history with zeros', () => {
  const year = annualUsage([agent], now);
  assert.equal(year.days.length, 365);
  assert.equal(year.active, 7);
  assert.equal(year.days[0].tokens, null);
  assert.equal(year.days.at(-1).tokens, 7);
  const oldZero = { ...agent, dailyUsage: [{ date: '2026-08-01', tokens: 0 }, ...agent.dailyUsage] };
  assert.equal(annualUsage([oldZero], now).days.find(day => day.date === '2026-08-01').tokens, null);
});
test('annual calendar includes leap day and local date boundaries', () => {
  const year = annualUsage([], new Date(2024, 2, 1, 0, 1));
  assert.equal(year.days.length, 366);
  assert.ok(year.days.some(day => day.date === '2024-02-29'));
});
test('quota cannot remain current after reset or stale observation', () => {
  const time = now.getTime();
  const quota = { observedAt: now.toISOString() };
  assert.equal(quotaStale(quota, time / 1000 + 600, time), false);
  assert.equal(quotaStale(quota, time / 1000 - 1, time), true);
  assert.equal(quotaStale(quota, null, time + 31 * 60000), true);
  assert.equal(quotaStale({ observedAt: 'invalid' }, null, time), true);
});

test('ZCode query failures mark a previous reading stale without claiming zero quota', () => {
  const quota = { agentId: 'zcode', observedAt: now.toISOString(), status: 'unavailable', windows: [{ usedPercent: 52, resetsAt: null, windowMinutes: 10080 }] };
  assert.equal(quotaStale(quota, null, now.getTime()), true);
  assert.equal(quotaStatusText(quota, true), '查询异常');
  assert.equal(quotaStatusText({ ...quota, status: 'network_error' }, true), '连接异常');
  assert.equal(quotaStale({ ...quota, status: 'network_error' }, null, now.getTime()), true);
  assert.match(quotaStatusText({ ...quota, status: 'auth_required' }, true), /重新登录/);
  assert.equal(quotaStatusText({ ...quota, status: 'ready' }, true), null);
  assert.equal(quotaStale({ ...quota, status: 'ready' }, null, now.getTime()), false);
});

test('estimates combine with recorded currencies, preserve free models and reveal gaps', () => {
  const p = { estimatedUsd: 150000000, estimatedRequests: 2, unpricedRequests: 1, detailedTokens: 900 };
  const a = { ...agent, pricing: p };
  const all = usageCosts([a], 'all', now);
  assert.equal(all.currencies.USD, 3.5);
  assert.equal(all.estimatedRequests, 2);
  assert.equal(all.unpricedRequests, 1);
  assert.equal(all.historyIncomplete, true);
  const free = usageCosts([{ ...agent, totalCosts: {}, pricing: { ...p, estimatedUsd: 0, unpricedRequests: 0, detailedTokens: 1000 } }], 'all', now);
  assert.deepEqual(free.currencies, { USD: 0 });
  assert.equal(free.historyIncomplete, false);
  const recent = { ...a, dailyUsage: dates.map((date, i) => ({ date, tokens: 100, pricing: { ...p, estimatedUsd: (i+1)*100000000, detailedTokens: 100 } })) };
  assert.equal(usageCosts([recent], 'today', now).currencies.USD, 7);
  assert.equal(usageCosts([recent], 'week', now).currencies.USD, 28);
});
