import type { CommunityStatsResponse, LeaderboardEntry } from '@/types/api';

// Bounded public snapshots for each board and community period. Never store /me data.
const prefix = `tokendance:public-home:v1:${import.meta.env.BASE_URL}:`;
const maxBytes = 512 * 1024;
export const publicHomeDay = () => new Date(Date.now() + 8 * 3600_000).toISOString().slice(0, 10);
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value);
const numberOrNull = (value: unknown) => value == null || (typeof value === 'number' && Number.isFinite(value));
const stringOrNull = (value: unknown) => value == null || typeof value === 'string';
type CommunityWindow = NonNullable<CommunityStatsResponse['window']>;

function communityWindowFromKey(key: string): CommunityWindow | null {
  const match = /^community:(today|7d|30d|all)$/.exec(key);
  return match ? match[1] as CommunityWindow : null;
}

function read(key: string): unknown {
  try {
    const raw = localStorage.getItem(prefix + key);
    if (!raw || raw.length > maxBytes) return null;
    const saved: unknown = JSON.parse(raw);
    return record(saved) && saved.day === publicHomeDay() ? saved.data : null;
  } catch { return null; }
}

function write(key: string, data: unknown) {
  try {
    const raw = JSON.stringify({ day: publicHomeDay(), data });
    if (raw.length <= maxBytes) localStorage.setItem(prefix + key, raw);
  } catch { /* Private browsing, disabled storage and quota limits must not break the page. */ }
}

export function readHomeBoard(key: string): LeaderboardEntry[] | null {
  const value = read(key);
  if (!Array.isArray(value) || value.length > 100) return null;
  if (!value.every(entry => record(entry)
    && Number.isInteger(entry.rankNo) && Number(entry.rankNo) >= 1 && Number(entry.rankNo) <= 100
    && typeof entry.handle === 'string' && typeof entry.displayName === 'string'
    && stringOrNull(entry.avatarUrl) && typeof entry.metricValue === 'string' && /^\d+$/.test(entry.metricValue)
    && numberOrNull(entry.rankDelta) && (entry.isNew == null || typeof entry.isNew === 'boolean'))) return null;
  return value as LeaderboardEntry[];
}

export function writeHomeBoard(key: string, entries: LeaderboardEntry[]) {
  // Explicit projection prevents accidental persistence of ownEntry or future private fields.
  write(key, entries.slice(0, 100).map(({ rankNo, handle, displayName, avatarUrl, metricValue, rankDelta, isNew }) =>
    ({ rankNo, handle, displayName, avatarUrl, metricValue, rankDelta, isNew })));
}

export function readHomeCommunity(key: string): CommunityStatsResponse | null {
  const value = read(key);
  if (!record(value) || value.metricDate !== publicHomeDay() || typeof value.timezone !== 'string') return null;
  const expectedWindow = communityWindowFromKey(key);
  if (expectedWindow && value.window !== expectedWindow) return null;
  if (!['tokens', 'codeLines', 'interactions', 'computedAt'].every(field => stringOrNull(value[field]))) return null;
  if (!['developers', 'costAmount'].every(field => numberOrNull(value[field]))) return null;
  if (value.deltas != null && (!record(value.deltas) || !Object.values(value.deltas).every(numberOrNull))) return null;
  if (value.costs != null && (!Array.isArray(value.costs) || !value.costs.every(item => record(item)
    && typeof item.amount === 'number' && Number.isFinite(item.amount) && typeof item.currency === 'string'))) return null;
  if (value.harnesses != null && (!Array.isArray(value.harnesses) || !value.harnesses.every(item => record(item)
    && typeof item.agentId === 'string' && typeof item.label === 'string'
    && stringOrNull(item.tokens) && numberOrNull(item.sharePct)))) return null;
  if (value.models != null && (!Array.isArray(value.models) || !value.models.every(item => record(item)
    && typeof item.modelId === 'string' && typeof item.label === 'string'
    && stringOrNull(item.tokens) && numberOrNull(item.sharePct)))) return null;
  if (value.skills != null && (!Array.isArray(value.skills) || !value.skills.every(item => record(item)
    && typeof item.skillId === 'string' && typeof item.label === 'string'
    && stringOrNull(item.uses) && numberOrNull(item.sharePct)))) return null;
  return value as unknown as CommunityStatsResponse;
}

export function writeHomeCommunity(key: string, stats: CommunityStatsResponse) {
  const { metricDate, timezone, window, tokens, developers, codeLines, interactions, costAmount, costs, deltas, harnesses, models, skills, computedAt } = stats;
  const expectedWindow = communityWindowFromKey(key);
  if (expectedWindow && window !== expectedWindow) return;
  if (metricDate === publicHomeDay()) write(key, { metricDate, timezone, window, tokens, developers, codeLines, interactions, costAmount, costs, deltas, harnesses, models, skills, computedAt });
}
