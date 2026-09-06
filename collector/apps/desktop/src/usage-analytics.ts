import type { AgentConfig } from './tauri-bridge.ts';
import { lastSevenDays } from './weekly-usage.ts';

export type UsageRange = 'today' | 'week' | 'all';
export interface AgentQuota {
  agentId: string;
  observedAt: string;
  plan?: string;
  status?: string;
  windows: { usedPercent: number; windowMinutes: number; resetsAt: number | null; provider?: string; label?: string }[];
}

export function quotaWindowLabel(window: AgentQuota['windows'][number], zh: boolean): string {
  const labels: Record<string, [string, string]> = {
    shared_week: ['共享周额度', 'Shared weekly quota'], shared_quota: ['共享套餐额度', 'Shared plan quota'],
    auto: ['Auto 额度', 'Auto quota'], api: ['API 额度', 'API quota'],
    plan: ['套餐额度', 'Plan quota'], personal_limit: ['个人额度上限', 'Personal limit'],
  };
  if (window.label && labels[window.label]) return labels[window.label][zh ? 0 : 1];
  const mins = window.windowMinutes;
  if (mins <= 0) return zh ? '当前周期额度' : 'Current cycle quota';
  return mins % 1440 === 0 ? `${mins / 1440}${zh ? ' 日额度' : '-day quota'}` : mins % 60 === 0 ? `${mins / 60}${zh ? ' 小时额度' : '-hour quota'}` : `${mins}${zh ? ' 分钟额度' : '-minute quota'}`;
}

export function quotaStatusText(quota: AgentQuota | undefined, zh: boolean): string | null {
  const names: Record<string, string> = { 'grok-build': 'Grok Build', cursor: 'Cursor', zcode: 'ZCode', codex: 'Codex', 'claude-code': 'Claude Code', pi: 'Pi', 'deepseek-harness': 'DeepSeek Harness' };
  const name = names[quota?.agentId ?? ''] ?? quota?.agentId ?? '';
  switch (quota?.status) {
    case 'not_connected': return zh ? `请在 ${name} 登录` : `Sign in to ${name}`;
    case 'auth_required': return zh ? `请在 ${name} 重新登录` : `Sign in again to ${name}`;
    case 'network_error': return zh ? '连接异常' : 'Connection error';
    case 'unavailable': return zh ? '查询异常' : 'Query failed';
    case 'no_quota': return zh ? '暂无额度' : 'No quota';
    case 'unlimited': return zh ? '不限额' : 'Unlimited';
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
  let covered = 0, estimatedRequests = 0, unpricedRequests = 0, historyIncomplete = false;
  for (const agent of agents) {
    const days = (agent.dailyUsage ?? []).filter(day => range === 'today' ? day.date === dates[6] : dates.includes(day.date));
    const rows = range === 'all' ? [{ costs: agent.totalCosts, pricing: agent.pricing, tokens: agent.totalTokens }] : days;
    let known = false;
    for (const row of rows) {
      for (const [currency, units] of Object.entries(row.costs ?? {})) {
        if (!/^[A-Z]{3}$/.test(currency) || !Number.isFinite(units) || units < 0) continue;
        currencies[currency] = (currencies[currency] ?? 0) + units / 1e8;
        known = true;
      }
      const p = row.pricing;
      if (p?.estimatedRequests && Number.isFinite(p.estimatedUsd) && p.estimatedUsd >= 0) {
        currencies.USD = (currencies.USD ?? 0) + p.estimatedUsd / 1e8;
        estimatedRequests += p.estimatedRequests;
        known = true;
      }
      unpricedRequests += p?.unpricedRequests ?? 0;
      if (row.tokens > (p?.detailedTokens ?? 0)) historyIncomplete = true;
    }
    if (known) covered++;
  }
  return { currencies, covered, estimatedRequests, unpricedRequests, historyIncomplete };
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
