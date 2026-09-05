import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { UnauthorizedState } from '@/components/states/UnauthorizedState';
import { MetricGrid } from '@/components/analytics/MetricGrid';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { SyncStatusCard } from '@/components/analytics/SyncStatusCard';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { ProfileVisibilitySwitch } from '@/components/analytics/ProfileVisibilitySwitch';
import { api, ApiError } from '@/api/client';
import type {
  PersonalSummary,
  TokenTrendsResponse,
  BreakdownItem,
  SkillItem,
  CalendarDay,
  FilterOptionsResponse,
} from '@/types/api';

export const PersonalDashboardPage: React.FC = () => {
  const { user, authenticated, loading: authLoading } = useAuth();
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();

  const [range, setRange] = useState('30d');
  const [selectedAgent, setSelectedAgent] = useState('all');
  const [selectedModel, setSelectedModel] = useState('all');

  const [summary, setSummary] = useState<PersonalSummary | null>(null);
  const [trends, setTrends] = useState<TokenTrendsResponse | null>(null);
  const [agentBreakdowns, setAgentBreakdowns] = useState<BreakdownItem[]>([]);
  const [skills, setSkills] = useState<SkillItem[]>([]);
  const [calendarDays, setCalendarDays] = useState<CalendarDay[]>([]);
  const [calendarStreak, setCalendarStreak] = useState<number>(0);
  const [filterOptions, setFilterOptions] = useState<FilterOptionsResponse>({
    agents: [],
    providers: [],
    models: [],
  });

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);

      const [summaryRes, trendsRes, agentsRes, skillsRes, calRes, filterRes] = await Promise.all([
        api.getPersonalSummary(range),
        api.getTokenTrends({
          range,
          agent: selectedAgent !== 'all' ? selectedAgent : undefined,
          model: selectedModel !== 'all' ? selectedModel : undefined,
        }),
        api.getAgentBreakdowns(range),
        api.getPersonalSkills(range),
        api.getActivityCalendar('10w'),
        api.getFilterOptions(),
      ]);

      setSummary(summaryRes);
      setTrends(trendsRes);
      setAgentBreakdowns(agentsRes.items || []);
      setSkills(skillsRes.skills || (skillsRes as unknown as { items: SkillItem[] }).items || []);
      setCalendarDays(calRes.days || []);
      setCalendarStreak(calRes.currentStreak || 0);
      setFilterOptions(filterRes);
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        // Handled by auth gate
      } else {
        setError(err instanceof ApiError ? err : new Error(String(err)));
      }
    } finally {
      setLoading(false);
    }
  }, [range, selectedAgent, selectedModel]);

  useEffect(() => {
    if (authenticated) {
      fetchData();
    }
  }, [authenticated, fetchData]);

  if (authLoading) {
    return <LoadingState />;
  }

  if (!authenticated) {
    return <UnauthorizedState />;
  }

  if (user?.onboardingRequired || user?.productState === 'new') {
    navigate('/onboarding');
    return null;
  }

  if (loading && !summary) {
    return <LoadingState message={t('common.loading')} />;
  }

  if (error && !summary) {
    return <ErrorState error={error} onRetry={fetchData} />;
  }

  if (!summary) {
    return <LoadingState message={t('common.loading')} />;
  }

  const fetchedTrends = trends?.points || trends?.trends || [];
  const displaySummary = summary;
  const displayTrends = fetchedTrends;
  const displayAgents = agentBreakdowns;
  const displaySkills = skills;
  const displayCalendar = calendarDays;
  const displayStreak = calendarStreak;
  const displayFilters: FilterOptionsResponse = filterOptions;
  const syncStatus = displaySummary.sync.status || (displaySummary.sync.lastCommittedAt ? 'healthy' : 'unknown');

  return (
    <div className="personal-dashboard">
      {/* Header */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-end',
          marginBottom: 24,
          flexWrap: 'wrap',
          gap: 16,
        }}
      >
        <div>
          <p className="eyebrow">{t('nav.tokenBoard')}</p>
          <h1>{t('publicProfile.headline')}</h1>
          <p className="text-muted" style={{ fontSize: 13 }}>
            {t('dashboard.subheadline')}
          </p>
        </div>

        <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
          {/* Time range segmented control */}
          <div className="segmented-control" role="tablist" aria-label={t('dashboard.timeRangeSelector')}>
            {[
              { key: 'today', label: t('common.today') },
              { key: '7d', label: t('common.days7') },
              { key: '30d', label: t('common.days30') },
              { key: 'all', label: t('common.allTime') },
            ].map((item) => (
              <button
                key={item.key}
                type="button"
                role="tab"
                aria-selected={range === item.key}
                className={`segmented-item ${range === item.key ? 'active' : ''}`}
                onClick={() => setRange(item.key)}
              >
                {item.label}
              </button>
            ))}
          </div>

          <Button
            variant="outline"
            onClick={() => {
              navigate('/settings/exports');
              showToast(t('settings.exportTitle'), 'info');
            }}
          >
            {t('dashboard.exportAction')}
          </Button>
        </div>
      </div>

      <ProfileVisibilitySwitch />

      {/* Ten Core Metrics Grid */}
      <MetricGrid metrics={displaySummary.metrics} />

      {/* Middle Grid: Token Trend + Agent Breakdown */}
      <div className="grid-2" style={{ marginBottom: 20 }}>
        {/* Token Trend Chart Panel */}
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.tokenTrends')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.tokenTrendsSub')}
              </p>
            </div>

            <div style={{ display: 'flex', gap: 8 }}>
              {/* Agent Filter */}
              <select
                aria-label={t('dashboard.agentFilter')}
                value={selectedAgent}
                onChange={(e) => setSelectedAgent(e.target.value)}
                className="form-input"
                style={{ height: 32, fontSize: 11, padding: '0 8px', cursor: 'pointer' }}
              >
                <option value="all">{t('dashboard.allAgents')}</option>
                {displayFilters.agents.map((a) => {
                  const key = typeof a === 'string' ? a : a.id;
                  const label = typeof a === 'string' ? a : a.name;
                  return (
                    <option key={key} value={key}>
                      {label}
                    </option>
                  );
                })}
              </select>

              {/* Model Filter */}
              <select
                aria-label={t('dashboard.modelFilter')}
                value={selectedModel}
                onChange={(e) => setSelectedModel(e.target.value)}
                className="form-input"
                style={{ height: 32, fontSize: 11, padding: '0 8px', cursor: 'pointer' }}
              >
                <option value="all">{t('dashboard.allModels')}</option>
                {displayFilters.models.map((m) => {
                  const key = typeof m === 'string' ? m : m.id;
                  const label = typeof m === 'string' ? m : m.name;
                  return (
                    <option key={key} value={key}>
                      {label}
                    </option>
                  );
                })}
              </select>
            </div>
          </div>

          <TokenTrendChart trends={displayTrends} />
        </div>

        {/* Agent Breakdown Panel */}
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.agentBreakdown')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.agentBreakdownSub')}
              </p>
            </div>
            <Badge variant="lime">
              {displayAgents.length} {t('dashboard.sourcesCount')}
            </Badge>
          </div>

          <AgentBreakdown items={displayAgents} />
        </div>
      </div>

      {/* Lower Grid: Heatmap + Skill Ranking + Sync Status */}
      <div className="grid-3">
        {/* Activity Heatmap Calendar */}
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.activityCalendar')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.activityCalendarSub')}
              </p>
            </div>
            {displayStreak > 0 && (
              <Badge variant="good">
                ● {displayStreak} {t('dashboard.streakLabel')}
              </Badge>
            )}
          </div>

          <ActivityCalendar days={displayCalendar} streakDays={displayStreak} />
        </div>

        {/* Skill Ranking */}
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.skillRanking')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.skillRankingSub')}
              </p>
            </div>
            <Badge variant="lime">{t('dashboard.topSkills')}</Badge>
          </div>

          <SkillRanking skills={displaySkills} />
        </div>

        {/* Sync Status */}
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('dashboard.syncStatus')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.collectorDevices')}
              </p>
            </div>
            <Button
              variant="ghost"
              size="sm"
              onClick={() => navigate('/settings/devices')}
              style={{ fontSize: 11 }}
            >
              {t('common.manage')} →
            </Button>
          </div>

          <SyncStatusCard
            lastCommittedAt={displaySummary.sync.lastCommittedAt}
            pendingLocalCount={displaySummary.sync.pendingLocalCount}
            status={syncStatus}
          />
        </div>
      </div>
    </div>
  );
};
