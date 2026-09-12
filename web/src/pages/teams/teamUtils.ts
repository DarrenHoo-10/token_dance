import type { MetricValue, MetricValueState, SharingFlags } from '@/api/teams';
import { EMPTY_SHARING } from '@/api/teams';

export const TEAM_NAME_MIN = 2;
export const TEAM_NAME_MAX = 40;
export const TEAM_DESCRIPTION_MAX = 120;
export const TEAM_RANGE_MAX_DAYS = 90;
export const JOIN_TOKEN_TTL_MS = 15 * 60 * 1000;
export const CREATE_DRAFT_KEY = 'td.team-create-draft';
const JOIN_TOKEN_PREFIX = 'td.team-join.';

export const TEAM_TIMEZONES = [
  { value: 'Asia/Shanghai', label: 'Asia / Shanghai (UTC+8)' },
  { value: 'Asia/Tokyo', label: 'Asia / Tokyo (UTC+9)' },
  { value: 'Asia/Singapore', label: 'Asia / Singapore (UTC+8)' },
  { value: 'Asia/Kolkata', label: 'Asia / Kolkata (UTC+5:30)' },
  { value: 'Europe/London', label: 'Europe / London' },
  { value: 'Europe/Berlin', label: 'Europe / Berlin' },
  { value: 'Europe/Paris', label: 'Europe / Paris' },
  { value: 'America/New_York', label: 'America / New York' },
  { value: 'America/Chicago', label: 'America / Chicago' },
  { value: 'America/Denver', label: 'America / Denver' },
  { value: 'America/Los_Angeles', label: 'America / Los Angeles' },
  { value: 'America/Sao_Paulo', label: 'America / São Paulo' },
  { value: 'Australia/Sydney', label: 'Australia / Sydney' },
  { value: 'Pacific/Auckland', label: 'Pacific / Auckland' },
  { value: 'UTC', label: 'UTC' },
];

export function graphemeLength(value: string): number {
  if (typeof Intl !== 'undefined' && 'Segmenter' in Intl) {
    return [...new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(value)].length;
  }
  return [...value].length;
}

export function firstGrapheme(value: string): string {
  const trimmed = value.trim();
  if (!trimmed) return 'T';
  if (typeof Intl !== 'undefined' && 'Segmenter' in Intl) {
    const first = new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(trimmed)[Symbol.iterator]().next().value;
    return first?.segment || trimmed[0];
  }
  return [...trimmed][0] || 'T';
}

export function createIdempotencyKey(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `idemp_${Date.now().toString(36)}_${Math.random().toString(36).slice(2, 12)}`;
}

function groupInteger(digits: string): string {
  return digits.replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

export function formatDecimalAmount(amount: string, currency: string): string {
  const negative = amount.startsWith('-');
  const raw = negative ? amount.slice(1) : amount;
  const [whole = '0', fraction = ''] = raw.split('.');
  const trimmedFraction = fraction.replace(/0+$/, '');
  const grouped = groupInteger(whole.replace(/^0+(?=\d)/, ''));
  const body = trimmedFraction ? `${grouped}.${trimmedFraction}` : grouped;
  const signed = `${negative ? '-' : ''}${body}`;
  return currency === 'USD' ? `$${signed}` : `${signed} ${currency}`;
}

function formatScaled(value: bigint, scale: bigint, suffix: string): string {
  const negative = value < 0n;
  const abs = negative ? -value : value;
  const whole = abs / scale;
  const frac = scale >= 10n ? (abs % scale) / (scale / 10n) : 0n;
  return `${negative ? '-' : ''}${whole.toString()}.${frac.toString()}${suffix}`;
}

export function formatTokenCompact(value: string): string {
  const n = BigInt(value);
  const abs = n < 0n ? -n : n;
  if (abs >= 1_000_000_000n) return formatScaled(n, 1_000_000_000n, 'B');
  if (abs >= 1_000_000n) return formatScaled(n, 1_000_000n, 'M');
  if (abs >= 1_000n) return formatScaled(n, 1_000n, 'K');
  return n.toString();
}

export function formatTokenExact(value: string): string {
  const n = BigInt(value);
  const negative = n < 0n;
  const digits = (negative ? -n : n).toString();
  return `${negative ? '-' : ''}${groupInteger(digits)}`;
}

export function metricDisplay(
  metric: MetricValue | null | undefined,
  format: (value: string) => string = formatTokenCompact
): { text: string; available: boolean; state: MetricValueState | 'missing' } {
  if (!metric) {
    return { text: '—', available: false, state: 'missing' };
  }
  if (metric.state !== 'available' || metric.value === null || metric.value === undefined) {
    return { text: '—', available: false, state: metric.state };
  }
  return { text: format(metric.value), available: true, state: 'available' };
}

export function normalizeSharing(flags: SharingFlags): SharingFlags {
  if (!flags.base) {
    return { ...EMPTY_SHARING };
  }
  return {
    base: true,
    named: true,
    classification: flags.classification,
    cost: flags.cost,
  };
}

export function joinReturnTo(linkId: string): string {
  return `/teams/join/${encodeURIComponent(linkId)}`;
}

export function parseJoinTokenFromHash(hash: string): string | null {
  if (!hash) return null;
  const trimmed = hash.startsWith('#') ? hash.slice(1) : hash;
  const params = new URLSearchParams(trimmed);
  const token = params.get('key') || params.get('token');
  return token && token.trim() ? token.trim() : null;
}

export function clearWindowHash(): void {
  if (typeof window === 'undefined') return;
  const path = `${window.location.pathname}${window.location.search}`;
  window.history.replaceState(window.history.state, document.title, path);
}

export interface StoredJoinToken {
  linkId: string;
  token: string;
  expiresAt: number;
}

function joinStorageKey(linkId: string): string {
  return `${JOIN_TOKEN_PREFIX}${linkId}`;
}

export function writeJoinToken(linkId: string, token: string, now = Date.now()): void {
  const payload: StoredJoinToken = { linkId, token, expiresAt: now + JOIN_TOKEN_TTL_MS };
  sessionStorage.setItem(joinStorageKey(linkId), JSON.stringify(payload));
}

export function readJoinToken(linkId: string, now = Date.now()): string | null {
  const raw = sessionStorage.getItem(joinStorageKey(linkId));
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as StoredJoinToken;
    if (!parsed.token || parsed.linkId !== linkId || parsed.expiresAt <= now) {
      sessionStorage.removeItem(joinStorageKey(linkId));
      return null;
    }
    return parsed.token;
  } catch {
    sessionStorage.removeItem(joinStorageKey(linkId));
    return null;
  }
}

export function clearJoinToken(linkId: string): void {
  sessionStorage.removeItem(joinStorageKey(linkId));
}

export function clearAllJoinTokens(): void {
  const keys: string[] = [];
  for (let i = 0; i < sessionStorage.length; i += 1) {
    const key = sessionStorage.key(i);
    if (key && key.startsWith(JOIN_TOKEN_PREFIX)) keys.push(key);
  }
  keys.forEach((key) => sessionStorage.removeItem(key));
}

export function captureJoinToken(linkId: string, hash = typeof window !== 'undefined' ? window.location.hash : ''): string | null {
  const fromHash = parseJoinTokenFromHash(hash);
  if (fromHash) {
    writeJoinToken(linkId, fromHash);
    clearWindowHash();
    return fromHash;
  }
  return readJoinToken(linkId);
}

export function redactInviteSecrets(value: string): string {
  return value
    .replace(/#key=[^&\s#]*/gi, '#key=redacted')
    .replace(/([?&](?:key|token)=)[^&\s#]*/gi, '$1redacted')
    .replace(/("(?:token|key|shareUrl)"\s*:\s*")[^"]*/gi, '$1redacted');
}

export function sanitizeErrorDetails(details?: Record<string, unknown>): Record<string, unknown> | undefined {
  if (!details) return undefined;
  const clone: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(details)) {
    if (key === 'token' || key === 'key' || key === 'shareUrl') continue;
    clone[key] = typeof value === 'string' ? redactInviteSecrets(value) : value;
  }
  return clone;
}

export interface CreateDraft {
  userId: string;
  name: string;
  description: string;
  timezone: string;
}

export function readCreateDraft(userId: string): CreateDraft | null {
  const raw = sessionStorage.getItem(CREATE_DRAFT_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as CreateDraft;
    if (!parsed || parsed.userId !== userId) {
      sessionStorage.removeItem(CREATE_DRAFT_KEY);
      return null;
    }
    return parsed;
  } catch {
    sessionStorage.removeItem(CREATE_DRAFT_KEY);
    return null;
  }
}

export function writeCreateDraft(draft: CreateDraft): void {
  sessionStorage.setItem(CREATE_DRAFT_KEY, JSON.stringify(draft));
}

export function clearCreateDraft(): void {
  sessionStorage.removeItem(CREATE_DRAFT_KEY);
}

export function inclusiveDaySpan(from: string, to: string): number {
  const start = Date.parse(`${from}T00:00:00Z`);
  const end = Date.parse(`${to}T00:00:00Z`);
  if (Number.isNaN(start) || Number.isNaN(end)) return Number.POSITIVE_INFINITY;
  return Math.floor((end - start) / 86400000) + 1;
}

export function formatInTimezone(iso: string, timeZone: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale === 'zh-CN' ? 'zh-CN' : 'en-US', {
      dateStyle: 'medium',
      timeStyle: 'short',
      timeZone,
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export function formatDateInTimezone(iso: string, timeZone: string, locale: string): string {
  try {
    return new Intl.DateTimeFormat(locale === 'zh-CN' ? 'zh-CN' : 'en-US', {
      dateStyle: 'medium',
      timeZone,
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

export function sha256Hex(buffer: ArrayBuffer): Promise<string> {
  return crypto.subtle.digest('SHA-256', buffer).then((digest) =>
    [...new Uint8Array(digest)].map((byte) => byte.toString(16).padStart(2, '0')).join('')
  );
}

export function downloadBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

export function coverageRatio(covered: string, eligible: string): string | null {
  try {
    const denom = BigInt(eligible);
    if (denom === 0n) return null;
    const numer = BigInt(covered);
    const pct = (numer * 1000n) / denom;
    const whole = pct / 10n;
    const frac = pct % 10n;
    return `${whole.toString()}.${frac.toString()}%`;
  } catch {
    return null;
  }
}
