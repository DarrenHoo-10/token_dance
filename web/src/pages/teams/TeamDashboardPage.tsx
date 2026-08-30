import React, { useState } from 'react';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { useLocale } from '@/context/LocaleContext';
import type { ActivityCalendarDay, AgentBreakdownItem, TokenTrendItem } from '@/types/api';

const trend: TokenTrendItem[] = Array.from({ length: 30 }, (_, index) => {
  const total = 9_600_000 + index * 310_000 + Math.sin(index * 0.62) * 1_900_000;
  return { date: `08-${String(index + 1).padStart(2, '0')}`, tokenTotal: String(Math.round(total)) };
});

const agents: AgentBreakdownItem[] = [
  { key: 'claude-code', label: 'Claude Code', tokenTotal: '188400000', percentage: 46 },
  { key: 'codex', label: 'Codex', tokenTotal: '131100000', percentage: 32 },
  { key: 'cursor', label: 'Cursor', tokenTotal: '57400000', percentage: 14 },
  { key: 'others', label: 'Others', tokenTotal: '32800000', percentage: 8 },
];

const calendar: ActivityCalendarDay[] = Array.from({ length: 70 }, (_, index) => ({
  date: `day-${index + 1}`,
  tokenTotal: String((index % 11) * 720000),
  level: Math.max(0, Math.min(4, Math.round(2 + Math.sin(index * 0.43) * 2))),
}));

const members = [
  { name: 'Max Bauer', handle: '@maxbauer', tokens: '112.7M', share: '27.5%', change: '+18.7%' },
  { name: 'Sophia Chen', handle: '@sophiadev', tokens: '94.3M', share: '23.0%', change: '+12.4%' },
  { name: 'Dev Patel', handle: '@deworap', tokens: '78.9M', share: '19.3%', change: '+7.3%' },
  { name: 'Katie Hall', handle: '@kaytee', tokens: '66.8M', share: '16.3%', change: '+5.8%' },
  { name: 'Alex Morgan', handle: '@alexm', tokens: '57.0M', share: '13.9%', change: '+3.1%' },
];

export const TeamDashboardPage: React.FC = () => {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  const [range, setRange] = useState('30d');
  const labels = zh
    ? ['团队 Token', '预估费用', '活跃成员', '人均 Token', '活跃天数']
    : ['Team Tokens', 'Estimated Cost', 'Active Members', 'Tokens per Member', 'Active Days'];
  const values = ['410.1M', '$1,884.20', '5', '82.0M', '29'];

  return (
    <section className="product-page-shell team-dashboard" aria-labelledby="team-title">
      <div className="product-page-heading with-actions">
        <div><span>{zh ? '团队' : 'Teams'}</span><h1 id="team-title">{zh ? '小团队 Token 分析' : 'Team Token Analytics'}</h1><p>{zh ? '查看团队成员、Agent 与每日 Token 消耗。' : 'Review token consumption across members, agents, and days.'}</p></div>
        <div className="segmented-control" role="tablist" aria-label={zh ? '时间范围' : 'Time range'}>
          {[['7d', zh ? '7 天' : '7 Days'], ['30d', zh ? '30 天' : '30 Days'], ['all', zh ? '全部' : 'All Time']].map(([key, label]) => <button key={key} type="button" role="tab" aria-selected={range === key} className={`segmented-item ${range === key ? 'active' : ''}`} onClick={() => setRange(key)}>{label}</button>)}
        </div>
      </div>

      <div className="team-metric-grid">
        {labels.map((label, index) => <article className="metric-card" key={label}><span className="metric-card-label">{label}</span><strong className="metric-card-value mono-num">{values[index]}</strong><span className="metric-card-hint">{index < 2 ? (zh ? '本周期' : 'Current period') : (zh ? '团队汇总' : 'Team total')}</span></article>)}
      </div>

      <div className="team-primary-grid">
        <article className="panel"><div className="panel-header"><div><h2>{zh ? '团队 Token 趋势' : 'Team Token Trend'}</h2><p className="text-muted">{zh ? '按日汇总团队 Token 消耗' : 'Daily aggregated token consumption'}</p></div></div><TokenTrendChart trends={trend} /></article>
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Agent 构成' : 'Agent Breakdown'}</h2><p className="text-muted">{zh ? '按 Token 占比' : 'By token share'}</p></div></div><AgentBreakdown items={agents} /></article>
      </div>

      <div className="team-secondary-grid">
        <article className="panel"><div className="panel-header"><div><h2>{zh ? '成员消耗' : 'Member Consumption'}</h2><p className="text-muted">{zh ? '当前周期团队成员排名' : 'Member ranking for the current period'}</p></div></div><div className="team-member-list">{members.map((member, index) => <div className="team-member-row" key={member.handle}><span className="team-member-rank">{index + 1}</span><div><strong>{member.name}</strong><small>{member.handle}</small></div><span className="mono-num">{member.tokens}</span><span className="mono-num text-muted">{member.share}</span><span className="team-change">{member.change}</span></div>)}</div></article>
        <article className="panel"><div className="panel-header"><div><h2>{zh ? '团队活跃日历' : 'Team Activity'}</h2><p className="text-muted">{zh ? '近 10 周活跃轨迹' : 'Activity across the last 10 weeks'}</p></div></div><ActivityCalendar days={calendar} /></article>
      </div>
    </section>
  );
};
