import React, { useState } from 'react';
import type { AnalysisTrendPoint, TeamAnalysisReady } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { useLocale } from '@/context/LocaleContext';
import { firstGrapheme, formatTokenCompact, formatTokenExact, metricDisplay } from './teamUtils';

const COLORS = ['#577d21', '#277d96', '#8668a6', '#bc7939', '#bb5275'];
const integer = (value?: string | null) => /^\d+$/.test(value || '') ? BigInt(value!) : 0n;
export function memberPercent(value: string, total: string): number {
  const denominator = integer(total);
  return denominator > 0n ? Number(integer(value) * 10000n / denominator) / 100 : 0;
}
function pointsFor(trend: AnalysisTrendPoint[] | undefined, dates: string[], max: bigint, width = 720, height = 190) {
  const values = new Map((trend || []).map(point => [point.date, integer(point.tokens.value)]));
  const padding = Math.min(12, height * 0.16);
  return dates.map((date, index) => {
    const value = values.get(date) || 0n;
    return { date, value, x: dates.length === 1 ? width / 2 : 12 + index * (width - 24) / (dates.length - 1), y: height - padding - Number(value * 10000n / (max || 1n)) / 10000 * (height - padding * 2) };
  });
}

export const TeamMemberInsights: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t } = useLocale();
  const members = analysis.contributions.items;
  const [selectedId, setSelectedId] = useState('');
  const available = members.filter(member => member.trend !== undefined);
  const selectedMember = available.find(member => member.membershipId === selectedId);
  const teamTrend = analysis.trend || [];
  const activeTrend = selectedMember?.trend || teamTrend;
  const seriesLabel = selectedMember?.displayName || t('teams.insights.teamSeries');
  const seriesColor = selectedMember ? COLORS[members.findIndex(member => member.membershipId === selectedMember.membershipId) % COLORS.length] || COLORS[0] : COLORS[0];
  const dates = [...new Set(activeTrend.map(point => point.date))].sort();
  const rankingDates = [...new Set([
    ...teamTrend.map(point => point.date),
    ...available.flatMap(member => (member.trend || []).map(point => point.date)),
  ])].sort();
  const max = activeTrend.reduce((value, point) => integer(point.tokens.value) > value ? integer(point.tokens.value) : value, 1n);
  const chartPoints = pointsFor(activeTrend, dates, max);
  const total = analysis.summary.tokens.value || '0';
  const teamEfficiency = metricDisplay(analysis.summary.metrics?.tokensPerCodeLine, (value) => formatTokenCompact(value));
  const leaders = members.slice(0, 5);
  const shown = leaders.reduce((sum, member) => sum + integer(member.tokens.value), 0n);
  const remainder = integer(total) > shown ? integer(total) - shown : 0n;
  const colorFor = (id: string) => COLORS[members.findIndex(member => member.membershipId === id) % COLORS.length] || COLORS[0];
  const donutStops = [
    ...leaders.map((member, index) => {
      const start = leaders.slice(0, index).reduce((sum, item) => sum + memberPercent(item.tokens.value || '0', total), 0);
      const end = start + memberPercent(member.tokens.value || '0', total);
      return `${COLORS[index]} ${start}% ${end}%`;
    }),
    remainder > 0n ? `#d6ddd1 ${leaders.reduce((sum, item) => sum + memberPercent(item.tokens.value || '0', total), 0)}% 100%` : '',
  ].filter(Boolean).join(', ');

  return <section className="team-people-section" id="members" aria-label={t('teams.overview.contributions')}>
    <div className="team-section-heading">
      <h2>{t('teams.overview.contributions')}</h2>
      <div className="team-token-eff">
        <span className="label">{t('teams.insights.tokenEfficiency')}</span>
        <strong className="mono-num">{teamEfficiency.available ? teamEfficiency.text : '—'}</strong>
        <small>{t('teams.insights.tokenEfficiencyUnit')}</small>
      </div>
    </div>
    <div className="team-people-grid">
      <Card className="team-member-trend">
        <div className="panel-header">
          <div><h2>{t('teams.insights.memberTrend')}</h2></div>
          <div className="team-trend-controls">
            <select className="form-input team-member-trend-select" aria-label={t('teams.insights.chooseMembers')} value={selectedId} onChange={(event) => setSelectedId(event.target.value)}>
              <option value="">{t('teams.insights.teamSeries')}</option>
              {available.map(member => <option key={member.membershipId} value={member.membershipId}>{member.displayName}</option>)}
            </select>
            <span className="team-chart-unit">Token / {t('teams.insights.day')}</span>
          </div>
        </div>
        {dates.length > 0 ? <>
          <div className="team-line-scale"><span>{formatTokenCompact(max.toString())}</span></div>
          <svg className="team-multiline-chart" viewBox="0 0 720 190" preserveAspectRatio="none" role="img" aria-label={t('teams.insights.memberTrend')}>
            {[12, 95, 178].map(y => <line key={y} x1="12" x2="708" y1={y} y2={y} stroke="#e6ebe4" strokeDasharray="4 5" />)}
            <g>
              <polyline points={chartPoints.map(point => `${point.x},${point.y}`).join(' ')} fill="none" stroke={seriesColor} strokeWidth="2.5" vectorEffect="non-scaling-stroke" />
              {chartPoints.map(point => <circle key={point.date} cx={point.x} cy={point.y} r="3.5" fill={seriesColor}><title>{`${seriesLabel} · ${point.date} · ${formatTokenExact(point.value.toString())} Token`}</title></circle>)}
            </g>
          </svg>
          <div className="team-line-scale"><span>0</span></div>
          <div className="team-line-dates"><span>{dates[0]}</span><span>{dates.length > 1 ? dates[dates.length - 1] : ''}</span></div>
        </> : <p className="team-chart-empty">{t(selectedMember ? 'teams.insights.trendUnavailable' : 'teams.overview.emptyRange')}</p>}
      </Card>
      <Card>
        <div className="panel-header"><div><h2>{t('teams.insights.memberShare')}</h2></div><span className="team-chart-unit">Token</span></div>
        {integer(total) > 0n ? <>
          <div className="team-donut-layout">
            <div className="team-donut" role="img" aria-label={t('teams.insights.memberShare')} style={{ background: `conic-gradient(${donutStops})` }}>
              <div className="team-donut-center">
                <strong className="mono-num">{formatTokenCompact(total)}</strong>
                <span>{t('teams.insights.teamTotal')}</span>
              </div>
            </div>
          </div>
          <div className="team-share-legend two-col">
            {leaders.map((member, index) => <div key={member.membershipId}><i style={{ background: COLORS[index] }} aria-hidden="true" /><span>{member.displayName}</span><strong className="mono-num">{memberPercent(member.tokens.value || '0', total).toFixed(1)}%</strong></div>)}
            {remainder > 0n && <div><i style={{ background: '#d6ddd1' }} aria-hidden="true" /><span>{t('teams.insights.others')}</span><strong>{memberPercent(remainder.toString(), total).toFixed(1)}%</strong></div>}
          </div>
        </> : <p className="team-chart-empty">{t('teams.overview.noContributions')}</p>}
      </Card>
    </div>
    <Card>
      <div className="panel-header" id="ranking"><div><h2>{t('teams.insights.detailTitle')}</h2></div><span className="team-chart-unit">{t('teams.insights.rankingHint')}</span></div>
      {members.length ? <div className="team-table-scroll"><table className="team-contribution-table"><thead><tr><th>{t('teams.insights.member')}</th><th>Token / {t('teams.insights.share')}</th><th>{t('teams.insights.tokenEfficiency')}</th><th>{t('teams.insights.activeDays')}</th><th>{t('teams.insights.periodTrend')}</th></tr></thead><tbody>{members.map(member => {
        const memberMax = (member.trend || []).reduce((value, point) => integer(point.tokens.value) > value ? integer(point.tokens.value) : value, 1n);
        const points = pointsFor(member.trend, rankingDates, memberMax, 100, 30);
        const efficiency = member.tokensPerCodeLine ? formatTokenCompact(member.tokensPerCodeLine) : '—';
        return <tr key={member.membershipId}><td><div className="team-person-cell"><span className="team-person-rank">{member.rank}</span><span className="team-person-avatar" aria-hidden="true">{firstGrapheme(member.displayName)}</span><span><strong>{member.displayName}</strong><small>{member.handle ? `@${member.handle}` : ''}</small></span></div></td><td><span className="share-cell"><span className="mono-num">{member.tokens.state === 'available' ? formatTokenCompact(member.tokens.value || '0') : '—'}</span> <span className="text-muted"> · {integer(total) > 0n ? `${memberPercent(member.tokens.value || '0', total).toFixed(1)}%` : '—'}</span><span className="team-person-share"><div><i style={{ width: `${memberPercent(member.tokens.value || '0', total)}%`, background: colorFor(member.membershipId) }} /></div></span></span></td><td className="mono-num">{efficiency}</td><td>{member.trend ? member.trend.filter(point => integer(point.tokens.value) > 0n).length : '—'}</td><td>{member.trend && points.length ? <svg width="100" height="30" viewBox="0 0 100 30" aria-label={`${member.displayName} ${t('teams.insights.periodTrend')}`} role="img"><polyline points={points.map(point => `${point.x},${point.y}`).join(' ')} stroke={colorFor(member.membershipId)} strokeWidth="2" fill="none" />{points.length === 1 && <circle cx={points[0].x} cy={points[0].y} r="3" fill={colorFor(member.membershipId)} />}</svg> : '—'}</td></tr>;
      })}</tbody></table></div> : <p className="team-chart-empty">{t('teams.overview.noContributions')}</p>}
    </Card>
  </section>;
};
