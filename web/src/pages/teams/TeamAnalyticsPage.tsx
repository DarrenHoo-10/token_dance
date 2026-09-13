import React, { useEffect, useState } from 'react';
import { useLocation, useOutletContext } from 'react-router-dom';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import { teamsApi, type ExportKind, type TeamExportJob, type TeamFilterOptions } from '@/api/teams';
import {
  createIdempotencyKey,
  downloadBlob,
  formatInTimezone,
} from './teamUtils';
import { AnalysisSkeleton, TeamDateRangeBar, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';
import { TeamOverviewBoard } from './TeamOverviewBoard';

export const TeamAnalyticsPage: React.FC = () => {
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const { scope, authRevision } = useTeam();
  const outlet = useOutletContext<{ openInvite?: () => void } | undefined>();
  const location = useLocation();
  const justCreated = Boolean((location.state as { justCreated?: boolean } | null)?.justCreated);
  const { range, from, to, agent, provider, model, setFilter, search } = useTeamSearchFilters();
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
  const [filters, setFilters] = useState<TeamFilterOptions>({ agents: [], providers: [], models: [] });
  const [exports, setExports] = useState<TeamExportJob[]>([]);
  const snapshotId = analysis?.snapshot.id;

  useEffect(() => {
    if (!scope || !snapshotId) {
      setFilters({ agents: [], providers: [], models: [] });
      return;
    }
    const controller = new AbortController();
    teamsApi.getFilterOptions(scope.team.id, snapshotId, controller.signal)
      .then(setFilters)
      .catch(() => setFilters({ agents: [], providers: [], models: [] }));
    return () => controller.abort();
  }, [scope, snapshotId]);

  useEffect(() => {
    if (!scope?.permissions.exportAnalytics) return undefined;
    const controller = new AbortController();
    teamsApi.getExports(scope.team.id, controller.signal)
      .then((res) => setExports(res.exports || []))
      .catch(() => {
        if (!controller.signal.aborted) setExports([]);
      });
    return () => controller.abort();
  }, [scope]);

  if (!scope) return null;

  const createdBanner = justCreated ? (
    <div className="team-status-banner">
      {t('teams.overview.firstUse')}
      {scope.permissions.inviteMembers && (
        <Button variant="primary" size="sm" style={{ marginLeft: 12 }} onClick={() => outlet?.openInvite?.()}>
          {t('teams.invite.action')}
        </Button>
      )}
    </div>
  ) : null;

  const filterBar = (
    <div className="team-filter-toolbar">
      <div className="team-filter-selects">
        <select className="form-input" aria-label={t('dashboard.agentFilter')} value={agent || 'all'} onChange={(e) => setFilter('agent', e.target.value)}>
          <option value="all">{t('dashboard.allAgents')}</option>
          {filters.agents.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
        </select>
        <select className="form-input" aria-label={t('dashboard.modelFilter')} value={model || 'all'} onChange={(e) => setFilter('model', e.target.value)}>
          <option value="all">{t('dashboard.allModels')}</option>
          {filters.models.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
        </select>
      </div>
      <TeamDateRangeBar timezone={scope.team.timezone} />
    </div>
  );

  if (updating && !analysis) {
    return <div>{createdBanner}{filterBar}<AnalysisSkeleton message={updatingMessageKey ? t(updatingMessageKey) : undefined} /></div>;
  }
  if (error && !analysis) {
    return <div>{createdBanner}{filterBar}<ErrorState error={error} description={teamErrorMessage(t, error)} /></div>;
  }
  if (!analysis) {
    return <div>{createdBanner}{filterBar}<AnalysisSkeleton /></div>;
  }

  const startExport = async (kind: ExportKind) => {
    const job = await teamsApi.createExport(
      scope.team.id,
      {
        snapshotId: analysis.snapshot.id,
        kind,
        agent: agent && agent !== 'all' ? agent : undefined,
        provider: provider && provider !== 'all' ? provider : undefined,
        model: model && model !== 'all' ? model : undefined,
        filtersHash: analysis.filtersHash,
      },
      { idempotencyKey: createIdempotencyKey() }
    );
    setExports((prev) => [job, ...prev]);
    showToast(t('teams.analytics.exportQueued'), 'success');
  };

  const download = async (job: TeamExportJob) => {
    const { blob, filename } = await teamsApi.downloadExportContent(scope.team.id, job.id);
    downloadBlob(blob, filename || `team-${job.kind}.csv`);
  };

  return (
    <div>
      {createdBanner}
      <p className="text-muted" style={{ fontSize: 12, margin: '0 0 12px' }}>
        {t('teams.overview.updatedAt', { time: formatInTimezone(analysis.snapshot.asOf, analysis.range.timezone, locale) })}
        {analysis.snapshot.refreshing ? ` · ${t('teams.analytics.refreshing')}` : ''}
      </p>
      {filterBar}

      <TeamOverviewBoard
        analysis={analysis}
        teamId={scope.team.id}
        authRevision={authRevision}
        search={search}
      />

      {scope.permissions.exportAnalytics && (
        <Card>
          <div className="panel-header">
            <div>
              <h2>{t('teams.analytics.export')}</h2>
              <p>{t('teams.analytics.exportHint')}</p>
            </div>
          </div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {(['daily', 'agents', 'models', 'members'] as ExportKind[]).map((kind) => (
              <Button key={kind} variant="outline" onClick={() => void startExport(kind)}>{t(`teams.analytics.exportKind.${kind}`)}</Button>
            ))}
          </div>
          {exports.map((job) => (
            <div key={job.id} className="team-member-row">
              <div>{t(`teams.analytics.exportKind.${job.kind}`)}</div>
              <div>{t(`teams.analytics.exportStatus.${job.status}`)}</div>
              {job.status === 'completed' && <Button size="sm" onClick={() => void download(job)}>{t('settings.downloadFile')}</Button>}
            </div>
          ))}
        </Card>
      )}
    </div>
  );
};
