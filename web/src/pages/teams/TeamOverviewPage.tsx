import React from 'react';
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
  const { range, from, to, agent, provider, model } = useTeamSearchFilters();
  const { openInvite } = useOutletContext<{ openInvite: () => void }>();
  const location = useLocation();
  const navigate = useNavigate();
  const justCreated = Boolean((location.state as { justCreated?: boolean } | null)?.justCreated);
  const { analysis, updating, updatingMessageKey, error, waitingForDates } = useTeamAnalysis({
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

  if (waitingForDates) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><p role="status" className="text-muted">{t('teams.range.chooseDates')}</p></div>;
  }

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

      <TeamDateRangeBar timezone={scope.team.timezone} />

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

      <div className="team-metric-grid-4">
        <MetricCard label={t('teams.metrics.tokens')} value={tokens.available ? tokens.text : null} supported={tokens.available} hint={t('teams.metrics.tokensHint')} />
        <MetricCard
          label={t('teams.metrics.activeMembers')}
          value={analysis ? `${analysis.summary.activeMembers} / ${analysis.summary.currentMembers}` : null}
          supported={Boolean(analysis)}
          hint={t('teams.metrics.activeHint')}
        />
        <MetricCard
          label={t('teams.metrics.sharingMembers')}
          value={sharingCount && currentMembers ? `${sharingCount}` : null}
          supported={Boolean(analysis)}
          hint={t('teams.metrics.sharingHint')}
        />
        <MetricCard label={costLabel} value={analysis ? costValue : null} supported={Boolean(analysis) && costValue !== '—'} hint={t('teams.metrics.costHint')} />
      </div>

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

      <Card>
        <div className="panel-header">
          <div>
            <h2>{t('teams.overview.contributions')}</h2>
            <p>{t('teams.overview.namedShare')}</p>
          </div>
        </div>
        {!analysis?.contributions.items.length && <p className="text-muted">{t('teams.overview.noContributions')}</p>}
        <div className="team-member-list">
          {analysis?.contributions.items.map((row) => {
            const value = metricDisplay(row.tokens, formatTokenCompact);
            return (
              <div className="team-member-row" key={row.membershipId}>
                <div className="team-member-rank">{row.rank}</div>
                <div>
                  <strong>{row.displayName}</strong>
                  <small>{row.handle ? `@${row.handle}` : t('common.private')}</small>
                </div>
                <div className="mono-num">{value.available ? value.text : '—'}</div>
              </div>
            );
          })}
        </div>
      </Card>
    </div>
  );
};
