import type { AgentConfig } from './tauri-bridge.ts';
import { lastSevenDays } from './weekly-usage.ts';

export type UsageRange = 'today' | 'week' | 'all';
export interface AgentQuota {
  agentId: string;
  observedAt: string;
  plan?: string;
  status?: string;
  windows: { usedPercent: number; windowMinutes: number; resetsAt: number | null; provider?: string }[];
}

export function quotaStatusText(quota: AgentQuota | undefined, zh: boolean): string | null {
  switch (quota?.status) {
    case 'not_connected': return zh ? '请在 ZCode 登录并启用 Coding Plan' : 'Sign in and enable Coding Plan in ZCode';
    case 'auth_required': return zh ? '登录已失效，请在 ZCode 重新登录' : 'Session expired · Sign in again in ZCode';
    case 'unavailable': return quota.windows.length
      ? (zh ? '查询失败，显示上次记录，稍后重试' : 'Query failed · Showing previous reading · Retrying')
      : (zh ? '额度暂时无法查询，稍后自动重试' : 'Quota temporarily unavailable · Retrying');
    case 'no_quota': return zh ? '未返回支持的个人套餐额度' : 'No supported personal plan quota returned';
    default: return null;
  }
}

export function usageTokens(agent: AgentConfig, range: UsageRange, now = new Date()): number | null {
  if (agent.accuracy === 'unknown') return null;
  if (range !== 'week') {
    const value = range === 'all' ? agent.totalTokens : agent.todayTokens;
    return Number.isFinite(value) && value >= 0 ? value : null;
  }
  const days = lastSevenDays(now).map(date => agent.dailyUsage?.find(day => day.date === date)?.tokens);
  return days.some(value => value == null || !Number.isFinite(value) || value < 0) ? null : (days as number[]).reduce((a, b) => a + b, 0);
}

export function usageCosts(agents: AgentConfig[], range: UsageRange, now = new Date()) {
  const dates = lastSevenDays(now);
  const currencies: Record<string, number> = {};
  let covered = 0;
  for (const agent of agents) {
    const values = range === 'all' ? [agent.totalCosts ?? {}] : (agent.dailyUsage ?? [])
      .filter(day => range === 'today' ? day.date === dates[6] : dates.includes(day.date)).map(day => day.costs ?? {});
    let known = false;
    for (const value of values) for (const [currency, units] of Object.entries(value)) {
      if (!/^[A-Z]{3}$/.test(currency) || !Number.isFinite(units) || units < 0) continue;
      currencies[currency] = (currencies[currency] ?? 0) + units / 1e8;
      known = true;
    }
    if (known) covered++;
  }
  return { currencies, covered };
}

export function annualUsage(agents: AgentConfig[], now = new Date()) {
  const start = new Date(now.getFullYear() - 1, now.getMonth(), now.getDate() + 1, 12);
  const end = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 12);
  const totals = new Map<string, number>();
  for (const agent of agents) {
    if (agent.accuracy === 'unknown') continue;
    for (const day of agent.dailyUsage ?? []) {
      if (agent.historyStart && day.date < agent.historyStart) continue;
      if (Number.isFinite(day.tokens) && day.tokens >= 0) totals.set(day.date, (totals.get(day.date) ?? 0) + day.tokens);
    }
  }
  const days: { date: string; tokens: number | null }[] = [];
  for (const day = new Date(start); day <= end; day.setDate(day.getDate() + 1)) {
    const date = `${day.getFullYear()}-${String(day.getMonth() + 1).padStart(2, '0')}-${String(day.getDate()).padStart(2, '0')}`;
    days.push({ date, tokens: totals.get(date) ?? null });
  }
  return { days, offset: (start.getDay() + 6) % 7, active: days.filter(day => (day.tokens ?? 0) > 0).length };
}

export function quotaStale(quota: AgentQuota, resetsAt: number | null, now = Date.now()) {
  if (quota.status && quota.status !== 'ready') return true;
  const observed = Date.parse(quota.observedAt);
  return !Number.isFinite(observed) || observed > now || now - observed > 30 * 60 * 1000 || (resetsAt != null && resetsAt * 1000 <= now);
}
