import type { QuotaState, UsageValueState } from './types';

const COMPACT_SUFFIXES = ['K', 'M', 'B', 'T', 'P', 'E', 'Z', 'Y'] as const;

export function parseTokenCount(value: string | null | undefined): bigint | null {
  if (value == null || value === '') return null;
  if (!/^-?\d+$/.test(value)) return null;
  try {
    return BigInt(value);
  } catch {
    return null;
  }
}

function formatScaled(abs: bigint, scale: bigint, suffix: string): string {
  const places = 2n;
  const factor = 10n ** places;
  let rounded = (abs * factor + scale / 2n) / scale;
  const intPart = rounded / factor;
  const fracPart = rounded % factor;
  return `${intPart.toString()}.${fracPart.toString().padStart(2, '0')}${suffix}`;
}

export function formatCompactTokens(tokens: bigint): string {
  const negative = tokens < 0n;
  const abs = negative ? -tokens : tokens;
  if (abs < 1000n) return tokens.toString();
  let scale = 1000n;
  let suffix: string = COMPACT_SUFFIXES[0];
  for (let i = 1; i < COMPACT_SUFFIXES.length; i++) {
    const next = scale * 1000n;
    if (abs < next) break;
    scale = next;
    suffix = COMPACT_SUFFIXES[i];
  }
  const text = formatScaled(abs, scale, suffix);
  return negative ? `-${text}` : text;
}

export function formatTodayTokens(value: string | null | undefined, usageState: UsageValueState = 'known'): string {
  if (usageState !== 'known') return '—';
  const tokens = parseTokenCount(value);
  if (tokens == null) return '—';
  return formatCompactTokens(tokens);
}

export function formatRemainingLabel(remaining: number, zh: boolean): string {
  const n = Math.round(remaining);
  return zh ? `余量 ${n}%` : `${n}% left`;
}

export function quotaStatusLine(state: QuotaState, zh: boolean, paused: boolean): string {
  if (paused) return zh ? '已暂停' : 'Paused';
  switch (state) {
    case 'fresh': return '';
    case 'loading': return zh ? '加载中' : 'Loading';
    case 'not_connected':
    case 'auth_required': return zh ? '额度未接入' : 'Quota not connected';
    case 'unlimited': return zh ? '不限额' : 'Unlimited';
    case 'no_quota': return zh ? '暂无额度' : 'No quota';
    default: return zh ? '额度待更新' : 'Awaiting update';
  }
}

export function formatObservedTime(observedAtMs: number, zh: boolean): string {
  return new Date(observedAtMs).toLocaleString(zh ? 'zh-CN' : 'en');
}

export function formatResetCountdown(resetsAtMs: number, nowMs: number, zh: boolean): string {
  const remaining = Math.max(0, resetsAtMs - nowMs);
  if (remaining === 0) return zh ? '等待更新' : 'Awaiting update';
  const totalMins = Math.max(1, Math.ceil(remaining / 60000));
  const days = Math.floor(totalMins / 1440);
  const hours = Math.floor((totalMins % 1440) / 60);
  const mins = totalMins % 60;
  if (days > 1) return zh ? `${days} 天后重置` : `Resets in ${days}d`;
  if (days === 1 && hours === 0) return zh ? '1 天后重置' : 'Resets in 1d';
  if (hours > 0) return zh ? `${hours} 小时 ${mins} 分后重置` : `Resets in ${hours}h ${mins}m`;
  return zh ? `${mins} 分钟后重置` : `Resets in ${mins}m`;
}

export function compareTokenStrings(a: string | null, b: string | null): number {
  const av = parseTokenCount(a);
  const bv = parseTokenCount(b);
  if (av == null && bv == null) return 0;
  if (av == null) return 1;
  if (bv == null) return -1;
  return av > bv ? -1 : av < bv ? 1 : 0;
}
