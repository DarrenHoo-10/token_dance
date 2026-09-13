import React from 'react';
import { useLocation, useOutletContext } from 'react-router-dom';
import { Button } from '@/components/common/Button';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { formatInTimezone } from './teamUtils';
import { AnalysisSkeleton, TeamDateRangeBar, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';
import { TeamOverviewBoard } from './TeamOverviewBoard';

export const TeamOverviewPage: React.FC = () => {
  const { t, locale } = useLocale();
  const { scope, authRevision } = useTeam();
  const { range, from, to, agent, provider, model, search } = useTeamSearchFilters();
  const { openInvite } = useOutletContext<{ openInvite: () => void }>();
  const location = useLocation();
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
      {analysis && (
        <TeamOverviewBoard
          analysis={analysis}
          teamId={scope.team.id}
          authRevision={authRevision}
          search={search}
        />
      )}
    </div>
  );
};
