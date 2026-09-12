import React, { useEffect, useState } from 'react';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { MetricCard } from '@/components/analytics/MetricCard';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import type { TokenTrendItem } from '@/types/api';
import { teamsApi, type ExportKind, type TeamExportJob, type TeamFilterOptions } from '@/api/teams';
import {
  coverageRatio,
  createIdempotencyKey,
  downloadBlob,
  formatDecimalAmount,
  formatInTimezone,
  formatTokenCompact,
  formatTokenExact,
  metricDisplay,
} from './teamUtils';
import { AnalysisSkeleton, TeamDateRangeBar, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';

export const TeamAnalyticsPage: React.FC = () => {
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const { scope, authRevision } = useTeam();
  const { range, from, to, agent, provider, model, setFilter } = useTeamSearchFilters();
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
  if (waitingForDates) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><p role="status" className="text-muted">{t('teams.range.chooseDates')}</p></div>;
  }
  if (updating && !analysis) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><AnalysisSkeleton message={updatingMessageKey ? t(updatingMessageKey) : undefined} /></div>;
  }
  if (error && !analysis) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><ErrorState error={error} description={teamErrorMessage(t, error)} /></div>;
  }
  if (!analysis) {
    return <div><TeamDateRangeBar timezone={scope.team.timezone} /><AnalysisSkeleton /></div>;
  }

  const tokens = metricDisplay(analysis.summary.tokens, formatTokenCompact);
  const trends: TokenTrendItem[] = (analysis.trend || [])
    .filter((point) => point.tokens.state === 'available' && point.tokens.value)
    .map((point) => ({ date: point.date, tokenTotal: point.tokens.value as string }));
  const agentItems = analysis.agents.items.map((item) => ({
    key: item.id,
    label: item.bucketType === 'unshared_classification' ? t('teams.analytics.unsharedBucket') : item.label,
    tokenTotal: item.tokens.value && item.tokens.state === 'available' ? item.tokens.value : '0',
    percentage: item.share ? Number(item.share) : 0,
  }));
  const reportedCoverage = coverageRatio(analysis.costs.coverage.reportedUsageEvents, analysis.costs.coverage.eligibleUsageEvents);

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
      <TeamDateRangeBar timezone={scope.team.timezone} />
      <p className="text-muted" style={{ fontSize: 12, margin: '12px 0' }}>
        {t('teams.overview.updatedAt', { time: formatInTimezone(analysis.snapshot.asOf, analysis.range.timezone, locale) })}
        {analysis.snapshot.refreshing ? ` · ${t('teams.analytics.refreshing')}` : ''}
      </p>

      <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap', marginBottom: 16 }}>
        <select className="form-input" aria-label={t('dashboard.agentFilter')} value={agent || 'all'} onChange={(e) => setFilter('agent', e.target.value)} style={{ height: 36, width: 'auto' }}>
          <option value="all">{t('dashboard.allAgents')}</option>
          {filters.agents.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
        </select>
        <select className="form-input" aria-label={t('dashboard.modelFilter')} value={model || 'all'} onChange={(e) => setFilter('model', e.target.value)} style={{ height: 36, width: 'auto' }}>
          <option value="all">{t('dashboard.allModels')}</option>
          {filters.models.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
        </select>
      </div>

      <div className="team-metric-grid-4">
        <MetricCard label={t('teams.metrics.tokens')} value={tokens.available ? tokens.text : null} supported={tokens.available} hint={t('teams.overview.currentAuth')} />
        <MetricCard
          label={t('teams.metrics.activeMembers')}
          value={`${analysis.summary.activeMembers} / ${analysis.summary.currentMembers}`}
          hint={analysis.summary.comparison ? undefined : t(`teams.analytics.comparison.${analysis.summary.comparisonReason || 'no_baseline'}`)}
        />
        <MetricCard
          label={t('teams.metrics.recordedCost')}
          value={analysis.costs.reported.length ? analysis.costs.reported.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ') : null}
          supported={analysis.costs.reported.length > 0}
          hint={reportedCoverage ? t('teams.analytics.coverage', { value: reportedCoverage }) : t('teams.metrics.costHint')}
        />
        <MetricCard
          label={t('teams.quality.unsupported')}
          value={analysis.quality.unsupportedEvents}
          hint={t('teams.quality.hint')}
        />
      </div>

      <div className="team-primary-grid">
        <Card>
          <div className="panel-header"><h2>{t('dashboard.tokenTrends')}</h2></div>
          <TokenTrendChart trends={trends} />
        </Card>
        <Card>
          <div className="panel-header"><h2>{t('dashboard.agentBreakdown')}</h2></div>
          <AgentBreakdown items={agentItems} />
        </Card>
      </div>

      <Card>
        <div className="panel-header"><h2>{t('teams.analytics.breakdown')}</h2></div>
        <table className="team-member-table">
          <thead>
            <tr>
              <th>{t('activity.agent')}</th>
              <th>{t('metrics.tokens')}</th>
              <th>{t('teams.analytics.share')}</th>
              <th>{t('teams.analytics.events')}</th>
            </tr>
          </thead>
          <tbody>
            {analysis.models.items.map((item) => {
              const value = metricDisplay(item.tokens, formatTokenExact);
              return (
                <tr key={item.id}>
                  <td>{item.bucketType === 'unshared_classification' ? t('teams.analytics.unsharedBucket') : item.label}</td>
                  <td className="mono-num">{value.available ? value.text : '—'}</td>
                  <td>{item.share != null ? `${item.share}%` : '—'}</td>
                  <td>{item.usageEvents ?? '—'}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </Card>

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
