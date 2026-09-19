import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { publicHomeDay, readHomeBoard, writeHomeBoard, readHomeCommunity, writeHomeCommunity } from '@/utils/publicHomeCache';

const entries = [{ rankNo: 1, handle: 'ada', displayName: 'Ada', avatarUrl: null, metricValue: '123' }];
beforeEach(() => { localStorage.clear(); vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-19T12:00:00Z')); });
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

it('persists only public row fields, separated by period and capped at 100', () => {
  writeHomeBoard('board:today', entries.map(entry => ({...entry, privateEmail: 'not-for-cache'})));
  expect(readHomeBoard('board:today')).toEqual(entries);
  expect(readHomeBoard('board:7d')).toBeNull();
  expect(localStorage.getItem(localStorage.key(0)!)).not.toContain('privateEmail');
  writeHomeBoard('board:all', Array.from({length: 101}, (_, i) => ({...entries[0], rankNo: i + 1})));
  expect(readHomeBoard('board:all')).toHaveLength(100);
});

it('invalidates snapshots at Beijing midnight without depending on browser timezone', () => {
  vi.setSystemTime(new Date('2026-09-19T15:59:59Z'));
  expect(publicHomeDay()).toBe('2026-09-19');
  writeHomeBoard('board:today', entries);
  writeHomeCommunity('community', {metricDate: '2026-09-19', timezone: 'UTC+8', tokens: '999'});
  vi.setSystemTime(new Date('2026-09-19T16:00:00Z'));
  expect(publicHomeDay()).toBe('2026-09-20');
  expect(readHomeBoard('board:today')).toBeNull();
  expect(readHomeCommunity('community')).toBeNull();
});

it('ignores corrupt and incompatible local data', () => {
  writeHomeBoard('board:today', entries);
  const key = localStorage.key(0)!;
  for (const bad of ['{', JSON.stringify({day: publicHomeDay(), data: [null]}), JSON.stringify({day: publicHomeDay(), data: [{...entries[0], metricValue: {}}]})]) {
    localStorage.setItem(key, bad);
    expect(readHomeBoard('board:today')).toBeNull();
  }
  writeHomeCommunity('community', {metricDate: publicHomeDay(), timezone: 'UTC+8', harnesses: [{agentId:'codex', label:'Codex'}]});
  expect(readHomeCommunity('community')?.harnesses?.[0].label).toBe('Codex');
  const communityKey = Array.from({length: localStorage.length}, (_, i) => localStorage.key(i)!).find(k => k.endsWith(':community'))!;
  localStorage.setItem(communityKey, JSON.stringify({day: publicHomeDay(), data: {metricDate: publicHomeDay(), timezone:'UTC+8', harnesses: [null]}}));
  expect(readHomeCommunity('community')).toBeNull();
});

it('continues when storage is disabled or full', () => {
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('disabled'); });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
  expect(readHomeBoard('board:today')).toBeNull();
  expect(() => writeHomeBoard('board:today', entries)).not.toThrow();
});
