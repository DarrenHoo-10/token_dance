import React from 'react';
import { Code2, Layers3, UsersRound, Wallet } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import type { MetricValue, TeamAnalysisReady } from '@/api/teams';
import {
  formatDecimalAmount,
  formatDurationHours,
  formatRatePercent,
  formatTokenCompact,
  metricDisplay,
} from './teamUtils';
import { TeamMemberInsights } from './TeamMemberInsights';
import { TeamUsageMix } from './TeamUsageMix';

function metricOf(analysis: TeamAnalysisReady, key: string): MetricValue | undefined {
  return analysis.summary.metrics?.[key];
}

function dash(value: string | null | undefined): string {
  return value || '—';
}

export const TeamOverviewBoard: React.FC<{
  analysis: TeamAnalysisReady;
  teamId: string;
  authRevision: string | null;
}> = ({ analysis, teamId, authRevision }) => {
  const { t, locale } = useLocale();
  const tokens = metricDisplay(analysis.summary.tokens);
  const estimated = analysis.costs.estimatedUncovered || [];
  const estimatedMetric = metricDisplay(metricOf(analysis, 'estimatedCosts'), (value) => formatDecimalAmount(value, 'USD'));
  const costValue = estimated.length
    ? estimated.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ')
    : dash(estimatedMetric.available ? estimatedMetric.text : null);
  const namedTokens = analysis.summary.currentMemberTokens || analysis.summary.tokens.value || '0';
  const activeMembers = analysis.summary.activeMembers || '0';
  const avgTokens = analysis.summary.tokens.state === 'available' && BigInt(activeMembers) > 0n
    ? formatTokenCompact((BigInt(namedTokens || '0') / BigInt(activeMembers)).toString())
    : null;
  const code = metricDisplay(metricOf(analysis, 'generatedCodeLines'));
  const input = metricDisplay(metricOf(analysis, 'inputContextTokens'));
  const output = metricDisplay(metricOf(analysis, 'outputTokens'));
  const cache = metricDisplay(metricOf(analysis, 'cacheHitRate'), (value) => formatRatePercent(value) || '—');
  const duration = metricDisplay(metricOf(analysis, 'activeDurationMs'), (value) => formatDurationHours(value) || '—');
  const messages = metricDisplay(metricOf(analysis, 'messageCount'));
  const userMessages = metricDisplay(metricOf(analysis, 'userMessageCount'));
  const avgDuration = duration.available && BigInt(activeMembers) > 0n && metricOf(analysis, 'activeDurationMs')?.value
    ? formatDurationHours(String(BigInt(metricOf(analysis, 'activeDurationMs')!.value || '0') / BigInt(activeMembers)))
    : null;

  const change = analysis.summary.comparison?.tokensDeltaPct;
  const delta = change == null ? null : Number(change);
  const reported = (analysis.costs.reported || []).map(item => formatDecimalAmount(item.amount, item.currency)).join(' / ') || '—';
  const details = [
    [t('teams.metrics.avgTokens'), dash(avgTokens)],
    [t('teams.metrics.input'), input.available ? input.text : '—'],
    [t('teams.metrics.output'), output.available ? output.text : '—'],
    [t('teams.metrics.cache'), cache.available ? cache.text : '—'],
    [t('teams.metrics.duration'), duration.available ? duration.text : '—'],
    [t('teams.metrics.messages'), messages.available ? messages.text : '—'],
    [t('teams.metrics.userMessages'), userMessages.available ? userMessages.text : '—'],
    [t('teams.metrics.avgDuration'), avgDuration || '—'],
  ];
  return <>
    {analysis.summary.tokens.state === 'empty' && <div className="team-status-banner">{t('teams.overview.emptyRange')}</div>}
    {analysis.quality?.includesHistoricalUsers && <p className="text-muted team-trend-hint">{t('teams.overview.historicalNote')}</p>}
    <section className="team-kpis sky-team-kpis" aria-label={t('teams.insights.usageTitle')}>
      <div className="team-kpi lead"><div className="label"><Layers3 size={17} />{t('teams.metrics.totalTokens')}</div><div className="sky-team-value"><strong className="value mono-num">{tokens.available ? tokens.text : '—'}</strong>{delta !== null && Number.isFinite(delta) && <span className={`sky-change ${delta < 0 ? 'down' : delta === 0 ? 'flat' : 'up'}`}>{delta < 0 ? '↘ −' : delta > 0 ? '↗ +' : ''}{Math.abs(delta).toFixed(1)}%</span>}</div><p className="sub">{t('teams.overview.selectedPeriod')}</p></div>
      <div className="team-kpi"><div className="label"><Wallet size={17} />{t('teams.metrics.estimatedCost')}</div><div className="value mono-num">{costValue}</div><p className="sub">{locale === 'zh-CN' ? '未覆盖用量预估' : 'Uncovered usage estimate'}</p></div>
      <div className="team-kpi"><div className="label"><UsersRound size={17} />{t('teams.metrics.activeMembers')}</div><div className="value mono-num">{analysis.summary.activeMembers} <small>/ {analysis.summary.currentMembers}</small></div><p className="sub">{analysis.summary.currentSharingMembers} {locale === 'zh-CN' ? '人共享基础用量' : 'sharing basic usage'}</p></div>
      <div className="team-kpi"><div className="label"><Code2 size={17} />{t('teams.metrics.codeLines')}</div><div className="value mono-num">{code.available ? code.text : '—'}</div><p className="sub">{t('teams.overview.selectedPeriod')}</p></div>
    </section>
    <div className="team-section-heading sky-detail-heading"><h2>{t('teams.overview.tokenData')}</h2><span className="text-muted">{locale === 'zh-CN' ? '已记录费用' : 'Reported cost'} · {reported}</span></div>
    <section className="sky-team-details" aria-label={t('teams.overview.tokenData')}>{details.map(([label, value]) => <div key={label}><span>{label}</span><strong className="mono-num">{value}</strong></div>)}</section>
    <TeamMemberInsights key={`${teamId}:${authRevision || ''}`} analysis={analysis} />
    <TeamUsageMix analysis={analysis} />
  </>;
};
