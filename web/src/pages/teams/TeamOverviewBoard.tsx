import React from 'react';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { Card } from '@/components/common/Card';
import { useLocale } from '@/context/LocaleContext';
import type { MetricValue, TeamAnalysisReady } from '@/api/teams';
import type { TokenTrendItem } from '@/types/api';
import {
  formatDecimalAmount,
  formatDurationHours,
  formatRatePercent,
  formatTokenCompact,
  metricDisplay,
} from './teamUtils';
import { TeamMemberInsights } from './TeamMemberInsights';
import { TeamSkillUsage } from './TeamSkillUsage';

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
  search: string;
}> = ({ analysis, teamId, authRevision, search }) => {
  const { t } = useLocale();
  const tokens = metricDisplay(analysis.summary.tokens);
  const reported = analysis.costs.reported || [];
  const estimated = analysis.costs.estimatedUncovered || [];
  const estimatedMetric = metricDisplay(metricOf(analysis, 'estimatedCosts'), (value) => formatDecimalAmount(value, 'USD'));
  const costValue = reported.length
    ? reported.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ')
    : estimated.length
      ? estimated.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ')
      : dash(estimatedMetric.available ? estimatedMetric.text : null);
  const namedTokens = analysis.summary.currentMemberTokens || analysis.summary.tokens.value || '0';
  const activeMembers = analysis.summary.activeMembers || '0';
  const avgTokens = analysis.summary.tokens.state === 'available' && BigInt(activeMembers) > 0n
    ? formatTokenCompact((BigInt(namedTokens || '0') / BigInt(activeMembers)).toString())
    : null;
  const code = metricDisplay(metricOf(analysis, 'generatedCodeLines'));
  const perLine = metricDisplay(metricOf(analysis, 'tokensPerCodeLine'));
  const input = metricDisplay(metricOf(analysis, 'inputContextTokens'));
  const output = metricDisplay(metricOf(analysis, 'outputTokens'));
  const cache = metricDisplay(metricOf(analysis, 'cacheHitRate'), (value) => formatRatePercent(value) || '—');
  const duration = metricDisplay(metricOf(analysis, 'activeDurationMs'), (value) => formatDurationHours(value) || '—');
  const messages = metricDisplay(metricOf(analysis, 'messageCount'));
  const userMessages = metricDisplay(metricOf(analysis, 'userMessageCount'));
  const avgDuration = duration.available && BigInt(activeMembers) > 0n && metricOf(analysis, 'activeDurationMs')?.value
    ? formatDurationHours(String(BigInt(metricOf(analysis, 'activeDurationMs')!.value || '0') / BigInt(activeMembers)))
    : null;
  const cacheWidth = cache.available ? Number(metricOf(analysis, 'cacheHitRate')?.value || '0') : 0;
  const cacheBar = `${Math.max(0, Math.min(100, (cacheWidth <= 1 ? cacheWidth * 100 : cacheWidth)))}%`;
  const trends: TokenTrendItem[] = (analysis.trend || [])
    .filter((point) => point.tokens.state === 'available' && point.tokens.value)
    .map((point) => ({ date: point.date, tokenTotal: point.tokens.value as string }));
  const agents = (analysis.agents.items || []).map((item) => ({
    key: item.id,
    label: item.bucketType === 'unshared_classification' ? t('teams.analytics.unsharedBucket') : item.label,
    tokenTotal: item.tokens.state === 'available' && item.tokens.value ? item.tokens.value : '0',
    percentage: item.share ? Number(item.share) : 0,
  }));
  const models = (analysis.models.items || []).map((item) => ({
    key: item.id + item.label,
    label: item.label,
    tokenTotal: item.tokens.state === 'available' && item.tokens.value ? item.tokens.value : '0',
    percentage: item.share ? Number(item.share) : 0,
  }));

  return (
    <>
      {analysis.summary.tokens.state === 'empty' && <div className="team-status-banner">{t('teams.overview.emptyRange')}</div>}
      {analysis.quality?.includesHistoricalUsers && (
        <p className="text-muted" style={{ fontSize: 12 }}>{t('teams.overview.historicalNote')}</p>
      )}

      <section className="team-kpis" aria-label={t('teams.metrics.totalTokens')}>
        <div className="team-kpi lead">
          <div className="label">{t('teams.metrics.totalTokens')}</div>
          <div className="value mono-num">{tokens.available ? tokens.text : '—'}</div>
          <div className="sub">{t('teams.metrics.avgTokens')} <b>{dash(avgTokens)}</b></div>
        </div>
        <div className="team-kpi">
          <div className="label">{t('teams.metrics.estimatedCost')}</div>
          <div className="value mono-num">{costValue}</div>
          <div className="sub">{t('teams.metrics.costHint')}</div>
        </div>
        <div className="team-kpi">
          <div className="label">{t('teams.metrics.activeMembers')}</div>
          <div className="value mono-num">{analysis.summary.activeMembers} <small>/ {analysis.summary.currentMembers}</small></div>
          <div className="sub">{t('teams.metrics.sharingMembers')} <b>{analysis.summary.currentSharingMembers || analysis.summary.currentMembers}</b></div>
        </div>
        <div className="team-kpi">
          <div className="label">{t('teams.metrics.codeLines')}</div>
          <div className="value mono-num">{code.available ? code.text : '—'}</div>
          <div className="sub">{t('teams.metrics.tokensPerLine')} <b>{perLine.available ? perLine.text : '—'}</b></div>
        </div>
      </section>

      <section className="team-metric-panels" aria-label={t('teams.insights.usageTitle')}>
        <Card>
          <div className="panel-header">
            <h2>{t('teams.overview.efficiency')}</h2>
            <span className="team-chart-unit">{t('teams.overview.selectedPeriod')}</span>
          </div>
          <div className="team-mini-row">
            <div><span className="label">{t('teams.metrics.input')}</span><strong className="mono-num">{input.available ? input.text : '—'}</strong></div>
            <div><span className="label">{t('teams.metrics.output')}</span><strong className="mono-num">{output.available ? output.text : '—'}</strong></div>
            <div><span className="label">{t('teams.metrics.cache')}</span><strong className="mono-num">{cache.available ? cache.text : '—'}</strong></div>
          </div>
          <div className="team-cache-track" aria-hidden="true"><span style={{ width: cache.available ? cacheBar : '0%' }} /></div>
          <div className="team-metric-foot">{t('teams.metrics.cacheFoot')}</div>
        </Card>
        <Card>
          <div className="panel-header">
            <h2>{t('teams.overview.activity')}</h2>
            <span className="team-chart-unit">{t('teams.overview.acrossMembers')}</span>
          </div>
          <div className="team-mini-row four">
            <div><span className="label">{t('teams.metrics.duration')}</span><strong className="mono-num">{duration.available ? duration.text : '—'}</strong></div>
            <div><span className="label">{t('teams.metrics.messages')}</span><strong className="mono-num">{messages.available ? messages.text : '—'}</strong></div>
            <div><span className="label">{t('teams.metrics.userMessages')}</span><strong className="mono-num">{userMessages.available ? userMessages.text : '—'}</strong></div>
            <div><span className="label">{t('teams.metrics.avgDuration')}</span><strong className="mono-num">{avgDuration || '—'}</strong></div>
          </div>
          <div className="team-metric-foot">{t('teams.metrics.activityFoot')}</div>
        </Card>
      </section>

      <TeamMemberInsights key={`${teamId}:${authRevision || ''}`} analysis={analysis} teamId={teamId} search={search} />

      <div className="team-section-heading">
        <div>
          <h2>{t('teams.overview.usageMix')}</h2>
          <p>{t('teams.overview.usageMixSub')}</p>
        </div>
      </div>
      <section className="team-usage-mix">
        <Card>
          <div className="panel-header"><h2>{t('teams.overview.teamTrend')}</h2></div>
          <TokenTrendChart trends={trends} />
        </Card>
        <Card>
          <div className="panel-header"><h2>{t('teams.overview.tools')}</h2></div>
          <AgentBreakdown items={agents} />
        </Card>
        <Card>
          <div className="panel-header"><h2>{t('teams.overview.models')}</h2></div>
          <AgentBreakdown items={models} />
        </Card>
      </section>

      <TeamSkillUsage analysis={analysis} />
    </>
  );
};
