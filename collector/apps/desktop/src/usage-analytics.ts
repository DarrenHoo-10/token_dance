import type { AgentConfig } from './tauri-bridge.ts';
import { lastSevenDays, weeklyUsage } from './weekly-usage.ts';

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
  const names: Record<string, string> = { 'grok-build': 'Grok Build', cursor: 'Cursor', zcode: 'ZCode', codex: 'Codex', 'claude-code': 'Claude Code', pi: 'Pi', 'deepseek-harness': 'DeepSeek Harness', opencode: 'OpenCode', workbuddy: 'WorkBuddy', 'doubao-work': 'Doubao Work' };
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
      if (p?.estimatedRequests) {
        const estimates = p.estimatedCosts && Object.keys(p.estimatedCosts).length > 0
          ? p.estimatedCosts : { USD: p.estimatedUsd };
        for (const [currency, units] of Object.entries(estimates)) {
          if (!/^[A-Z]{3}$/.test(currency) || !Number.isFinite(units) || units < 0) continue;
          currencies[currency] = (currencies[currency] ?? 0) + units / 1e8;
          known = true;
        }
        estimatedRequests += p.estimatedRequests;
      }
      unpricedRequests += p?.unpricedRequests ?? 0;
      if (row.tokens > (p?.detailedTokens ?? 0)) historyIncomplete = true;
    }
    if (known) covered++;
  }
  return { currencies, covered, estimatedRequests, unpricedRequests, historyIncomplete };
}

export type TrendPoint = { key: string; label: string; tokens: number | null };

function localDateKey(now: Date) {
  return `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
}

function monthDay(date: string) {
  return `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
}

export function usageTrend(agents: AgentConfig[], range: UsageRange, now = new Date()): TrendPoint[] {
  if (range === 'today') {
    const date = localDateKey(now);
    const currentHour = now.getHours();
    const hourlyAgents = agents.filter(agent => (agent.hourlyUsage?.length ?? 0) > 0);
    return Array.from({ length: 24 }, (_, hour) => {
      const tokens = hourlyAgents.length && hour <= currentHour
        ? hourlyAgents.reduce((sum, agent) => sum + (agent.hourlyUsage?.find(item => item.hour === hour)?.tokens ?? 0), 0)
        : null;
      return { key: `${date}T${String(hour).padStart(2, '0')}`, label: `${hour}:00`, tokens };
    });
  }
  if (range === 'week') {
    return weeklyUsage(agents, now).points.map(point => ({ key: point.date, label: monthDay(point.date), tokens: point.tokens }));
  }
  const year = annualUsage(agents, now);
  let firstUsage: string | null = null;
  for (const agent of agents) {
    if (agent.accuracy === 'unknown') continue;
    for (const day of agent.dailyUsage ?? []) {
      if (agent.historyStart && day.date < agent.historyStart) continue;
      if (!Number.isFinite(day.tokens) || day.tokens <= 0) continue;
      if (!firstUsage || day.date < firstUsage) firstUsage = day.date;
    }
  }
  if (!firstUsage || year.days.length === 0) {
    return year.days.map(point => ({ key: point.date, label: monthDay(point.date), tokens: point.tokens }));
  }
  const startKey = firstUsage < year.days[0].date ? year.days[0].date : firstUsage;
  const startIndex = year.days.findIndex(day => day.date >= startKey);
  const days = startIndex < 0 ? year.days : year.days.slice(startIndex);
  return days.map(point => ({ key: point.date, label: monthDay(point.date), tokens: point.tokens }));
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

/** Collection status is separate from billing quota availability. */
export function collectionStatusText(agent: AgentConfig, range: UsageRange, paused: boolean, zh: boolean): string {
  const text = (cn: string, en: string) => zh ? cn : en;
  if (!agent.enabled) return text('已关闭', 'Disabled');
  if (paused || agent.status === 'PAUSED') return text('已暂停', 'Paused');
  if (agent.id === 'doubao-work' && agent.status === 'ACTIVE') return text('活动采集中，Token 不可用', 'Collecting activity; Token unavailable');
  switch (agent.status) {
    case 'UNDETECTED': return text('未检测到', 'Not detected');
    case 'AUTH_REQUIRED': return text(`请在 ${agent.name} 重新登录`, `Sign in again to ${agent.name}`);
    case 'CONNECTING': case 'CONFIGURING': return text('正在连接用量来源', 'Connecting to usage source');
    case 'ERROR': return text('用量读取失败，将自动重试', 'Usage read failed; retrying automatically');
    case 'NEEDS_PERMISSION': return text('缺少采集权限', 'Collection permission required');
    case 'DEGRADED': return text('部分数据暂不可用', 'Some data is unavailable');
    default: return range === 'today' ? text('今日暂无用量', 'No usage today') : range === 'week' ? text('近 7 日暂无用量', 'No usage in 7 days') : text('暂无历史用量', 'No recorded usage');
  }
}
