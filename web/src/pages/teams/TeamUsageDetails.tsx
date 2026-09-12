import React from 'react';
import type { TeamAnalysisReady } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { useLocale } from '@/context/LocaleContext';
import { formatTokenExact } from './teamUtils';

export const TeamUsageDetails: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t } = useLocale();
  const points = [...(analysis.trend || [])].sort((a, b) => a.date.localeCompare(b.date));
  const max = points.reduce((value, point) => BigInt(point.tokens.value || '0') > value ? BigInt(point.tokens.value || '0') : value, 1n);
  return <div className="team-people-grid">
    <Card><div className="panel-header"><div><h2>{t('teams.insights.models')}</h2><p>{t('teams.insights.modelHint')}</p></div></div>
      <AgentBreakdown items={analysis.models.items.filter(item => item.tokens.state === 'available').map(item => ({ key: item.label + item.id, label: item.label, tokenTotal: item.tokens.value || '0', percentage: Number(item.share || '0') }))} />
      {analysis.models.nextCursor && <p className="text-muted">{t('teams.insights.firstPage', { count: analysis.models.items.length })}</p>}
    </Card>
    <Card><div className="panel-header"><div><h2>{t('teams.insights.activity')}</h2><p>{t('teams.insights.activityHint')}</p></div></div>
      {points.length ? <><div className="team-usage-calendar">{points.map(point => { const value = BigInt(point.tokens.value || '0'); const level = value === 0n ? 0 : Math.min(4, 1 + Number(value * 3n / max)); return <div key={point.date} tabIndex={0} className={`team-usage-day level-${level}`} aria-label={`${point.date}: ${formatTokenExact(point.tokens.value || '0')} Token`} title={`${point.date}: ${formatTokenExact(point.tokens.value || '0')} Token`}><span>{point.date.slice(8)}</span></div>; })}</div><div className="team-line-dates"><span>{points[0].date}</span><span>{points.length > 1 ? points[points.length - 1].date : ''}</span></div></> : <p className="team-chart-empty">{t('dashboard.noTrendData')}</p>}
    </Card>
  </div>;
};
