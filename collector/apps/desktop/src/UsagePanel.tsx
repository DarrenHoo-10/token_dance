import { useEffect, useRef, useState } from 'react';
import { getAgentConfigs, getAgentQuotas, getDaemonStatus, hideWindow, isTauriEnvironment, openSettings, openWebsite, toggleGlobalPause } from './tauri-bridge';
import { syncStatusText } from './sync-status';
import type { AgentConfig, DaemonStatus } from './tauri-bridge';
import { weeklyUsage } from './weekly-usage';
import { usageTokens, usageCosts, type UsageRange, type AgentQuota } from './usage-analytics';
import { WeeklyTrend } from './components/WeeklyTrend';
import { AnnualActivity, QuotaRings } from './components/UsageDetails';
import { brandLogo } from './brand';
import './styles/usage-panel.css';
import { useWindowReady } from './window-ready';

const format = (value: number) => new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 2 }).format(value);

export function UsagePanel() {
  const [lang, setLang] = useState<'zh' | 'en'>(() => localStorage.getItem('tokendance.language') === 'en' ? 'en' : 'zh');
  const [range, setRange] = useState<UsageRange>('today');
  const [data, setData] = useState<{ agents: AgentConfig[]; status: DaemonStatus } | null>(null);
  const [quotas, setQuotas] = useState<AgentQuota[]>([]);
  const [error, setError] = useState(false);
  useWindowReady(data !== null || error);
  const [message, setMessage] = useState('');
  const [busy, setBusy] = useState(false);
  const refresh = useRef<() => Promise<void>>(async () => {});
  const zh = lang === 'zh';
  const text = (cn: string, en: string) => zh ? cn : en;
  useEffect(() => {
    let disposed = false;
    let loading = false;
    let firstLoad = true;
    let loadingQuotas = false;
    const loadQuotas = async () => {
      if (loadingQuotas || document.hidden) return;
      loadingQuotas = true;
      try { const result = await getAgentQuotas(); if (!disposed) setQuotas(result); }
      catch { /* Keep the last observation; its timestamp still expires normally. */ }
      finally { loadingQuotas = false; }
    };
    const load = async () => {
      if (loading || (document.hidden && !firstLoad)) return;
      firstLoad = false;
      loading = true;
      setLang(localStorage.getItem('tokendance.language') === 'en' ? 'en' : 'zh');
      try {
        const [agents, status] = await Promise.all([getAgentConfigs(), getDaemonStatus()]);
        if (!disposed) { setData({ agents, status }); setError(false); }
      } catch { if (!disposed) setError(true); }
      finally { loading = false; }
    };
    refresh.current = load;
    void load();
    void loadQuotas();
    const timer = window.setInterval(load, 3000);
    const quotaTimer = window.setInterval(loadQuotas, 60000);
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') void hideWindow(); };
    window.addEventListener('keydown', onKey);
    window.addEventListener('focus', load);
    window.addEventListener('focus', loadQuotas);
    document.addEventListener('visibilitychange', load);
    document.addEventListener('visibilitychange', loadQuotas);
    return () => { disposed = true; window.clearInterval(timer); window.clearInterval(quotaTimer); window.removeEventListener('keydown', onKey); window.removeEventListener('focus', load); window.removeEventListener('focus', loadQuotas); document.removeEventListener('visibilitychange', load); document.removeEventListener('visibilitychange', loadQuotas); };
  }, []);
  useEffect(() => { if (!message) return; const timer = window.setTimeout(() => setMessage(''), 4500); return () => window.clearTimeout(timer); }, [message]);
  const act = async (action: () => Promise<unknown>) => {
    if (busy) return;
    setBusy(true);
    try { await action(); await refresh.current(); } catch (err) { setMessage(String(err)); } finally { setBusy(false); }
  };
  const agents = [...(data?.agents ?? [])].sort((a, b) => (usageTokens(b, range) ?? -1) - (usageTokens(a, range) ?? -1));
  const active = agents.filter(agent => usageTokens(agent, range) !== null).slice(0, 3);
  const others = agents.filter(agent => !active.includes(agent));
  const week = weeklyUsage(agents);
  const status = data?.status;
  const paused = status?.globalPaused;
  const healthy = status?.status === 'RUNNING' && !error;
  const periods: { key: UsageRange; label: string }[] = [{ key: 'today', label: text('今日', 'Today') }, { key: 'week', label: text('近 7 日', '7 days') }, { key: 'all', label: text('全部时间', 'All time') }];
  const costLabel = (items: AgentConfig[], key: UsageRange) => {
    const costs = usageCosts(items, key);
    const entries = Object.entries(costs.currencies);
    if (!entries.length) return '—';
    return (costs.estimatedRequests ? '≈ ' : '') + entries.map(([currency, value]) => new Intl.NumberFormat(zh ? 'zh-CN' : 'en-US', { style: 'currency', currency, maximumFractionDigits: 2 }).format(value)).join(' + ');
  };
  const selectedCosts = usageCosts(agents, range);
  const costSource = (items: AgentConfig[], key: UsageRange) => usageCosts(items, key).estimatedRequests ? text('含 OpenRouter 估算', 'Includes OpenRouter estimates') : text('已记录费用', 'Recorded cost');
  const agentState = (agent: AgentConfig) => agent.status === 'UNDETECTED' ? text('未检测到', 'Not detected') : !agent.enabled ? text('已关闭', 'Disabled') : paused || agent.status === 'PAUSED' ? text('已暂停', 'Paused') : agent.status === 'DEGRADED' && (usageTokens(agent, 'all') ?? 0) > 0 ? text('用量已记录，部分能力未接入', 'Usage recorded · Some capabilities unavailable') : ['ERROR', 'DEGRADED', 'NEEDS_PERMISSION', 'CONFIGURING'].includes(agent.status) ? text('需要配置', 'Needs setup') : range === 'today' ? text('今日暂无用量', 'No usage today') : range === 'week' ? text('近 7 日暂无用量', 'No usage in 7 days') : text('暂无历史用量', 'No recorded usage');
  return <div className="usage-panel">
    <header className="usage-header"><div className="usage-brand"><img src={brandLogo} alt="" /><strong>TokenDance</strong></div><div className="usage-window-controls" role="group" aria-label={text('语言与窗口控制', 'Language and window controls')}>
      <button className="usage-language" onClick={() => { const next = zh ? 'en' : 'zh'; setLang(next); localStorage.setItem('tokendance.language', next); }} aria-label={text('切换到英文', 'Switch to Chinese')}>{zh ? 'EN' : '中'}</button>
      <button disabled={busy} onClick={() => void act(hideWindow)} aria-label={text('最小化到托盘', 'Minimize to tray')} title={text('收起到托盘 · Esc', 'Hide to tray · Esc')}>−</button>
    </div></header>
    <main className="usage-content">
      <div className="usage-heading"><h1>{text('我的用量', 'My usage')}</h1><span>{text('本机数据', 'This device')} · {new Date().toLocaleDateString(zh ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric' })}</span></div>
      <div className="usage-totals" aria-label={text('统计周期', 'Usage period')}>{periods.map(period => {
        const values = agents.map(agent => usageTokens(agent, period.key)).filter(value => value !== null);
        return <button className="usage-total-card" key={period.key} aria-pressed={range === period.key} onClick={() => setRange(period.key)} title={period.key === 'all' ? text('本机已记录的全部历史；升级前已清理的数据不包含在内', 'All recorded local history; excludes history removed before this upgrade') : undefined}>
          <span>{period.label}</span><strong>{values.length ? format(values.reduce((a, b) => a + b, 0)) : '—'}</strong><small>tokens</small><div className="usage-cost"><b>{costLabel(agents, period.key)}</b><small>{costSource(agents, period.key)}</small></div>
        </button>;
      })}</div>
      <p className="usage-data-note">{!data ? text('正在读取采集数据…', 'Loading collector data…') : text('按 OpenRouter 参考价估算，已有费用记录优先；不代表实际账单。', 'Estimates use OpenRouter reference prices; recorded charges take precedence. Not an actual bill.')}</p>
      {data && (selectedCosts.unpricedRequests > 0 || selectedCosts.historyIncomplete) && <p className="usage-data-note" role="status">{selectedCosts.unpricedRequests > 0 ? text(`${selectedCosts.unpricedRequests} 次用量暂未定价。`, `${selectedCosts.unpricedRequests} usage records are not priced yet.`) : ''}{selectedCosts.historyIncomplete ? text('部分历史缺少计价明细，费用仅覆盖可计算部分。', 'Some history lacks pricing details; costs cover calculable usage only.') : ''}</p>}
      <WeeklyTrend points={week.points} lang={lang} />
      <section className="usage-agents" aria-label={text('Agent 用量与额度', 'Agent usage and quotas')}>
        <div className="usage-section-title"><h2>{text('Agent 用量与额度', 'Agent usage & quotas')}</h2><span>{text('用量前 3 · ', 'Top 3 · ')}{periods.find(period => period.key === range)?.label} · {text('额度独立周期', 'Separate quota windows')}</span></div>
        {active.map(agent => {
          const quota = quotas.find(item => item.agentId === agent.id);
          const tokens = usageTokens(agent, range);
          return <article className="usage-agent-card" key={agent.id}><div className="usage-agent-top"><div className="usage-agent-name"><span className="usage-agent-symbol">{agent.name.slice(0, 2)}</span><strong>{agent.name}</strong>{quota?.plan && <small>{quota.plan}</small>}</div><div className="usage-agent-value"><strong>{tokens === null ? '—' : format(tokens)}</strong><small title={costSource([agent], range)}>{costLabel([agent], range)}</small></div></div>
            {(paused || !agent.enabled || ['ERROR', 'DEGRADED', 'NEEDS_PERMISSION'].includes(agent.status)) && <div className="usage-agent-warning">{agentState(agent)}</div>}
            {agent.id === 'grok-build' && (usageTokens(agent, 'all') ?? 0) > 0 && <p className="usage-data-note">{text('用量来自本地日志，在轮次完成后更新。', 'Usage comes from local logs and updates after a turn completes.')}</p>}
            {tokens === 0 && (usageTokens(agent, 'all') ?? 0) > 0 && <p className="usage-data-note">{text('所选期间暂无用量，历史已记录', 'No usage this period. Recorded history:')} {format(usageTokens(agent, 'all')!)} tokens</p>}
            <QuotaRings quota={quota} zh={zh} />
          </article>;
        })}
        {others.length > 0 && <details className="usage-other-sources"><summary>{text('其他来源', 'Other sources')} · {others.length}</summary>{others.map(agent => {
          const quota = quotas.find(item => item.agentId === agent.id);
          return <div className="usage-other-source" key={agent.id}><div className="usage-source-row"><span>{agent.name}{quota?.plan && <small> · {quota.plan}</small>}</span><small>{agentState(agent)}</small><strong>{usageTokens(agent, range) === null ? '—' : format(usageTokens(agent, range)!)}</strong></div>{quota && <QuotaRings quota={quota} zh={zh} />}</div>;
        })}</details>}
        {data && agents.length === 0 && <p className="usage-empty">{text('尚未检测到 Agent，在设置中连接。', 'No agents found. Connect one in Settings.')}</p>}
      </section>
      <AnnualActivity agents={agents} zh={zh} />
      {!isTauriEnvironment() && <p className="usage-preview-label">{text('浏览器预览 · 示例数据', 'Browser preview · Sample data')}</p>}
    </main>
    <div className="usage-status-bar"><div><span className={`usage-status-dot ${healthy && !paused ? 'running' : ''}`} /><span>{error ? text('连接中断', 'Disconnected') : !status ? text('连接中', 'Connecting') : paused ? text('已暂停', 'Paused') : healthy ? text('采集中', 'Collecting') : text('需检查', 'Check collector')}</span><span className="usage-queue" role="status" title={status ? text(`${status.eventsPending} 条记录保留在本机，等待服务器确认`, `${status.eventsPending} records stored locally, awaiting server confirmation`) : undefined}>{status && !error ? syncStatusText(status.syncStatus, status.eventsPending, zh) : ''}</span></div><button disabled={!status || busy || error} onClick={() => void act(toggleGlobalPause)}>{paused ? text('继续', 'Resume') : text('暂停', 'Pause')}</button></div>
    <footer className="usage-footer"><button onClick={() => void act(openSettings)}><span aria-hidden="true">⚙</span>{text('设置', 'Settings')}</button><button className="usage-website" onClick={() => void act(openWebsite)}>{text('网站主页 · 看排名', 'Website · Rankings')}<span aria-hidden="true">↗</span></button></footer>
    {error && <div className="usage-notice" role="alert">{text('无法读取最新数据', 'Could not read latest data')} <button onClick={() => void refresh.current()}>{text('重试', 'Retry')}</button></div>}
    {message && <div className="usage-notice" role="status">{message}</div>}
  </div>;
}
