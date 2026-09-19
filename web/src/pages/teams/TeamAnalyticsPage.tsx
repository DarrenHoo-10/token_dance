import React, { useEffect, useState } from 'react';
import { useLocation, useNavigate, useOutletContext } from 'react-router-dom';
import { ArrowDownToLine, ArrowRight, ChevronDown, ShieldCheck, SlidersHorizontal, X } from 'lucide-react';
import { teamsApi, type TeamFilterOptions } from '@/api/teams';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import { formatSkyClock } from './teamUtils';
import { AnalysisSkeleton, TeamDateRangeBar, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';
import { TeamOverviewBoard } from './TeamOverviewBoard';
import type { TeamOutletContext } from './TeamLayout';

export const TeamAnalyticsPage: React.FC = () => {
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const { scope, authRevision } = useTeam();
  const outlet = useOutletContext<TeamOutletContext | undefined>();
  const navigate = useNavigate();
  const location = useLocation();
  const createState = (location.state as { justCreated?: boolean; avatarFailed?: boolean } | null) || {};
  const justCreated = Boolean(createState.justCreated);
  const avatarFailed = Boolean(createState.avatarFailed);
  const { range, from, to, agent, provider, model, setFilter } = useTeamSearchFilters();
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
    if (!analysis || !outlet?.setUpdatedAt) return;
    outlet.setUpdatedAt(formatSkyClock(analysis.snapshot.asOf, analysis.range.timezone));
  }, [analysis, outlet]);

  if (!scope) return null;

  const exportSnapshot = async () => {
    if (!snapshotId) return;
    try {
      await teamsApi.createExport(scope.team.id, { snapshotId, kind: 'members', agent, provider, model });
      showToast(t('teams.analytics.exportQueued'), 'success');
    } catch {
      showToast(t('errors.unknown'), 'error');
    }
  };

  const createdBanner = justCreated ? (
    <div className="team-status-banner">
      {t('teams.overview.firstUse')}
      {avatarFailed && <p className="form-error" role="status">{t('teams.create.avatarFailed')}</p>}
    </div>
  ) : null;

  const filterBar = (
    <>
      <section className="tw-toolbar team-filter-toolbar" aria-label={t('teams.insights.usageTitle')}>
        <TeamDateRangeBar timezone={scope.team.timezone} part="controls" />
        <div className="tw-filter-selects">
          <label>
            <span className="sr-only">{t('dashboard.agentFilter')}</span>
            <select aria-label={t('dashboard.agentFilter')} value={agent || 'all'} onChange={(e) => setFilter('agent', e.target.value)}>
              <option value="all">{t('dashboard.allAgents')}</option>
              {filters.agents.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
            </select>
            <ChevronDown size={13} />
          </label>
          <label>
            <span className="sr-only">{t('dashboard.modelFilter')}</span>
            <select aria-label={t('dashboard.modelFilter')} value={model || 'all'} onChange={(e) => setFilter('model', e.target.value)}>
              <option value="all">{t('dashboard.allModels')}</option>
              {filters.models.map((item) => <option key={item.id} value={item.id}>{item.label}</option>)}
            </select>
            <ChevronDown size={13} />
          </label>
          {scope.permissions.inviteMembers && (
            <button type="button" className="tw-export" onClick={() => void exportSnapshot()}>
              <ArrowDownToLine size={16} />{locale === 'zh-CN' ? '导出' : 'Export'}
            </button>
          )}
        </div>
      </section>
      <TeamDateRangeBar timezone={scope.team.timezone} part="custom" />
      {(agent || model) && (
        <div className="tw-filter-note">
          <SlidersHorizontal size={14} />
          {t('teams.overview.filtersApply')}
          <button type="button" onClick={() => { setFilter('agent', 'all'); setFilter('model', 'all'); }}>
            {t('teams.overview.resetFilters')}<X size={12} />
          </button>
        </div>
      )}
    </>
  );

  if (updating && !analysis) {
    return <div className="tw-overview">{createdBanner}{filterBar}<AnalysisSkeleton message={updatingMessageKey ? t(updatingMessageKey) : undefined} /></div>;
  }
  if (error && !analysis) {
    return <div className="tw-overview">{createdBanner}{filterBar}<ErrorState error={error} description={teamErrorMessage(t, error)} /></div>;
  }
  if (!analysis) {
    return <div className="tw-overview">{createdBanner}{filterBar}<AnalysisSkeleton /></div>;
  }

  return (
    <div className="tw-overview">
      {createdBanner}
      {filterBar}
      <TeamOverviewBoard analysis={analysis} teamId={scope.team.id} authRevision={authRevision} />
      <div className="tw-bottom-note">
        <ShieldCheck size={15} />
        <span>{t('teams.overview.privacyNote')}</span>
        <button type="button" onClick={() => navigate(`/teams/${scope.team.id}/settings${location.search}`)}>
          {t('teams.overview.mySharing')}<ArrowRight size={13} />
        </button>
      </div>
    </div>
  );
};
