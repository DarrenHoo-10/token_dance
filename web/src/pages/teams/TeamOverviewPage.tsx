import React from 'react';
import { TeamMemberInsights } from './TeamMemberInsights';
import { TeamUsageDetails } from './TeamUsageDetails';
import { useLocation, useNavigate, useOutletContext } from 'react-router-dom';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { MetricCard } from '@/components/analytics/MetricCard';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import type { TokenTrendItem } from '@/types/api';
import { formatDecimalAmount, formatInTimezone, formatTokenCompact, metricDisplay } from './teamUtils';
import { AnalysisSkeleton, TeamDateRangeBar, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';

export const TeamOverviewPage: React.FC = () => {
  const { t, locale } = useLocale();
  const { scope, authRevision } = useTeam();
  const { range, from, to, agent, provider, model, search } = useTeamSearchFilters();
  const { openInvite } = useOutletContext<{ openInvite: () => void }>();
  const location = useLocation();
  const navigate = useNavigate();
  const justCreated = Boolean((location.state as { justCreated?: boolean } | null)?.justCreated);
  const { analysis, updating, updatingMessageKey, error } = useTeamAnalysis({
    teamId: scope?.team.id,
    authRevision,
    range,
    from,
    to,
    agent,
    provider,
    model,
  });

  if (!scope) return null;


  if ((updating && !analysis) || (!analysis && !error)) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><AnalysisSkeleton message={updatingMessageKey ? t(updatingMessageKey) : undefined} /></div>;
  }

  if (error && !analysis) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><ErrorState error={error} description={teamErrorMessage(t, error)} /></div>;
  }

  const tokens = metricDisplay(analysis?.summary.tokens);
  const sharingCount = analysis?.summary.currentSharingMembers;
  const currentMembers = analysis?.summary.currentMembers;
  const noSharing = sharingCount === '0';
  const emptyTokens = analysis?.summary.tokens.state === 'empty';
  const reported = analysis?.costs.reported || [];
  const estimated = analysis?.costs.estimatedUncovered || [];
  const costLabel = reported.length ? t('teams.metrics.recordedCost') : estimated.length ? t('teams.metrics.estimatedCost') : t('teams.metrics.recordedCost');
  const costValue = reported.length
    ? reported.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ')
    : estimated.length
      ? estimated.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ')
      : '—';

  const trends: TokenTrendItem[] = (analysis?.trend || [])
    .filter((point) => point.tokens.state === 'available' && point.tokens.value)
    .map((point) => ({ date: point.date, tokenTotal: point.tokens.value as string }));

  const agents = (analysis?.agents.items || []).map((item) => ({
    key: item.id,
    label: item.bucketType === 'unshared_classification' ? t('teams.analytics.unsharedBucket') : item.label,
    tokenTotal: item.tokens.state === 'available' && item.tokens.value ? item.tokens.value : '0',
    percentage: item.share ? Number(item.share) : 0,
  }));

  return (
    <div>
      <TeamDateRangeBar timezone={scope.team.timezone} />
      {justCreated && (
        <div className="team-status-banner">
          {t('teams.overview.firstUse')}
          {scope.permissions.inviteMembers && (
            <Button variant="primary" size="sm" style={{ marginLeft: 12 }} onClick={openInvite}>
              {t('teams.invite.action')}
            </Button>
          )}
        </div>
      )}


      {analysis && (
        <p className="text-muted" style={{ fontSize: 12, margin: '12px 0 20px' }}>
          {t('teams.overview.updatedAt', { time: formatInTimezone(analysis.snapshot.asOf, analysis.range.timezone, locale) })}
          {analysis.snapshot.refreshing ? ` · ${t('teams.analytics.refreshing')}` : ''}
        </p>
      )}

      {noSharing && (
        <div className="team-status-banner warn">
          {t('teams.overview.notShared')}
          <Button variant="ghost" size="sm" onClick={() => navigate(`/teams/${scope.team.id}/settings`)}>{t('teams.settings.mySharing')}</Button>
        </div>
      )}
      {!noSharing && emptyTokens && <div className="team-status-banner">{t('teams.overview.waitingSync')}</div>}

      <div className="team-metric-grid-6">
        <MetricCard label={t('teams.metrics.tokens')} value={tokens.available ? tokens.text : null} supported={tokens.available} />
        <MetricCard
          label={t('teams.metrics.activeMembers')}
          value={analysis ? `${analysis.summary.activeMembers} / ${analysis.summary.currentMembers}` : null}
          supported={Boolean(analysis)}
        />
        <MetricCard
          label={t('teams.metrics.sharingMembers')}
          value={sharingCount && currentMembers ? `${sharingCount}` : null}
          supported={Boolean(analysis)}
        />
        <MetricCard label={costLabel} value={analysis ? costValue : null} supported={Boolean(analysis) && costValue !== '—'} />
        <MetricCard label={t('teams.insights.average')} value={analysis?.summary.tokens.state === 'available' && BigInt(analysis.summary.activeMembers) > 0n ? formatTokenCompact((BigInt(analysis.summary.tokens.value || '0') / BigInt(analysis.summary.activeMembers)).toString()) : null} supported={Boolean(analysis?.summary.tokens.state === 'available' && BigInt(analysis.summary.activeMembers) > 0n)} />
        <MetricCard label={t('teams.insights.peak')} value={trends.length ? formatTokenCompact(trends.reduce((max, point) => BigInt(point.tokenTotal || '0') > max ? BigInt(point.tokenTotal || '0') : max, 0n).toString()) : null} supported={trends.length > 0} />
      </div>

      {analysis && <TeamMemberInsights key={`${scope.team.id}:${authRevision}`} analysis={analysis} teamId={scope.team.id} search={search} />}

      <div className="team-section-heading"><h2>{t('teams.insights.usageTitle')}</h2></div>
      <div className="team-primary-grid">
        <Card>
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.tokenTrends')}</h2>
              <p>{t('teams.overview.currentAuth')}</p>
            </div>
          </div>
          <TokenTrendChart trends={trends} />
        </Card>
        <Card>
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.agentBreakdown')}</h2>
            </div>
          </div>
          <AgentBreakdown items={agents} />
        </Card>
      </div>

      {analysis && <TeamUsageDetails analysis={analysis} />}
    </div>
  );
};
