import { useState, type CSSProperties } from 'react';
import type { AgentConfig } from '../tauri-bridge';
import { annualUsage, quotaStale, type AgentQuota } from '../usage-analytics';

export function QuotaRings({ quota, zh }: { quota?: AgentQuota; zh: boolean }) {
  if (!quota?.windows.length) return <div className="usage-quota-empty"><span className="usage-ring unavailable">—</span><span>{zh ? '额度未接入' : 'Quota unavailable'}<small>{zh ? '用量采集不受影响' : 'Usage collection continues'}</small></span></div>;
  return <div className="usage-quotas">{quota.windows.map((window, index) => {
    const stale = quotaStale(quota, window.resetsAt);
    const mins = window.windowMinutes;
    const label = mins % 1440 === 0 ? `${mins / 1440}${zh ? ' 日额度' : '-day quota'}` : mins % 60 === 0 ? `${mins / 60}${zh ? ' 小时额度' : '-hour quota'}` : `${mins}${zh ? ' 分钟额度' : '-minute quota'}`;
    const remaining = window.resetsAt == null ? null : Math.max(0, window.resetsAt * 1000 - Date.now());
    const days = remaining == null ? 0 : Math.ceil(remaining / 86400000);
    const hours = remaining == null ? 0 : Math.ceil(remaining / 3600000);
    const reset = remaining === null ? (zh ? '重置时间未知' : 'Reset time unknown') : remaining === 0 ? (zh ? '等待更新' : 'Awaiting update') : days > 1 ? (zh ? `${days} 天后重置` : `Resets in ${days}d`) : (zh ? `${hours} 小时后重置` : `Resets in ${hours}h`);
    return <div className={`usage-quota ${stale ? 'stale' : ''}`} key={index} title={`${zh ? '记录时间' : 'Observed'}: ${new Date(quota.observedAt).toLocaleString()}`}>
      <div className={`usage-ring ${!stale && window.usedPercent >= 80 ? 'warning' : ''}`} style={{ '--used': window.usedPercent } as CSSProperties} role="img" aria-label={`${label}: ${window.usedPercent}% ${zh ? '已用' : 'used'}${stale ? (zh ? '，待更新' : ', stale') : ''}`}><b>{Math.round(window.usedPercent)}%</b></div>
      <span>{label}<small>{stale ? (zh ? '待更新 · ' : 'Stale · ') : ''}{reset}</small></span>
    </div>;
  })}</div>;
}

export function AnnualActivity({ agents, zh }: { agents: AgentConfig[]; zh: boolean }) {
  const { days, offset, active } = annualUsage(agents);
  const [selected, setSelected] = useState<{ date: string; tokens: number | null } | null>(null);
  const max = Math.max(1, ...days.map(day => day.tokens ?? 0));
  const columns = Math.ceil((offset + days.length) / 7);
  const months = days.filter(day => day.date.endsWith('-01'));
  return <section className="usage-activity" aria-label={zh ? '年度活动热力图' : 'Annual activity heatmap'}>
    <div className="usage-section-title"><h2>{zh ? '年度活动' : 'Annual activity'}</h2><span>{zh ? '过去 12 个月' : 'Past 12 months'}</span></div>
    <div className="usage-months">{months.filter((_, index) => index % 2 === 0).map(day => <span key={day.date}>{Number(day.date.slice(5, 7))}{zh ? '月' : ''}</span>)}</div>
    <div className="usage-heatmap" style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }} role="group" aria-label={zh ? '每日 Token 用量' : 'Daily token usage'}>
      {Array.from({ length: offset }, (_, index) => <span key={`empty-${index}`} />)}
      {days.map(day => {
        const level = day.tokens === null ? 'unknown' : day.tokens === 0 ? 0 : Math.min(4, Math.ceil(day.tokens / max * 4));
        const label = `${day.date}: ${day.tokens === null ? (zh ? '未记录' : 'Not recorded') : `${day.tokens.toLocaleString()} tokens`}`;
        return <button key={day.date} className="usage-day" data-level={level} aria-label={label} title={label} onFocus={() => setSelected(day)} onMouseEnter={() => setSelected(day)} onClick={() => setSelected(day)} />;
      })}
    </div>
    <div className="usage-heat-footer"><span>{active} {zh ? '个活跃日' : 'active days'}</span><span className="usage-heat-legend">{zh ? '少' : 'Less'}{[0, 1, 2, 3, 4].map(level => <i key={level} data-level={level} />)}{zh ? '多' : 'More'}</span></div>
    <div className="usage-heat-detail" aria-live="polite">{selected ? `${selected.date} · ${selected.tokens === null ? (zh ? '未记录' : 'Not recorded') : `${selected.tokens.toLocaleString()} tokens`}` : zh ? '灰色空格表示尚未记录的日期' : 'Empty cells indicate unrecorded dates'}</div>
  </section>;
}
