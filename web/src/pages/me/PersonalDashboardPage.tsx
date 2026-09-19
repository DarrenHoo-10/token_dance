import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { BarChart3, Download, Flame, RefreshCw, ShieldCheck, Sparkles, Trophy } from 'lucide-react';
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
import { UserAvatar } from '@/components/common/UserAvatar';
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
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();

  const [range, setRange] = useState('today');
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

  const zh = locale === 'zh-CN';
  const name = user?.displayName || user?.handle || (zh ? '开发者' : 'Builder');

  return (
    <div className="personal-dashboard personal-analytics-page">
      <section className="personal-analytics-shell">
        <div className="personal-kicker"><BarChart3 size={18} />{zh ? '个人数据' : 'My analytics'}</div>
        <div className="personal-analytics-heading">
          <div>
            <h1>{zh ? `${name}，你的创造正在发生。` : `${name}, your ideas are taking shape.`}</h1>
            <p>{zh ? '从每一次协作，看见你的投入与创造。' : 'See the effort and creativity behind every collaboration.'}</p>
          </div>
          <UserAvatar url={user?.avatarUrl} name={name} alt={name} />
        </div>

        <div className="personal-analytics-toolbar">
          <div className="segmented-control" role="tablist" aria-label={t('dashboard.timeRangeSelector')}>
            {[
              { key: 'today', label: t('common.today') },
              { key: '7d', label: t('common.days7') },
              { key: '30d', label: t('common.days30') },
              { key: 'all', label: t('common.allTime') },
            ].map((item) => <button key={item.key} type="button" role="tab" aria-selected={range === item.key} className={`segmented-item ${range === item.key ? 'active' : ''}`} onClick={() => setRange(item.key)}>{item.label}</button>)}
          </div>
          <Button variant="outline" onClick={() => { navigate('/settings/exports'); showToast(t('settings.exportTitle'), 'info'); }}><Download size={15} />{t('dashboard.exportAction')}</Button>
        </div>

        <MetricGrid metrics={displaySummary.metrics} />

        <div className="personal-analytics-context">
          <span><Trophy size={14} />{zh ? '今日排名' : 'Today’s rank'} <b>{displaySummary.ranking.rank ? `#${displaySummary.ranking.rank}` : '—'}</b>{displaySummary.ranking.delta != null && displaySummary.ranking.delta !== 0 && <em>{displaySummary.ranking.delta > 0 ? '↗' : '↘'} {Math.abs(displaySummary.ranking.delta)}</em>}</span>
          <span><Flame size={14} />{zh ? '连续活跃' : 'Day streak'} <b>{displayStreak} {zh ? '天' : 'days'}</b></span>
          <span><ShieldCheck size={14} />{displaySummary.sync.lastCommittedAt ? (zh ? '已同步至网站' : 'Synced to website') : (zh ? '等待首次同步' : 'Waiting for first sync')}</span>
          <span className="personal-context-time">{displaySummary.range.to || '—'} · UTC+8</span>
        </div>

        <div className="personal-analytics-middle">
          <section className="personal-analytics-section personal-trend-section">
            <div className="personal-section-heading"><div><h2>{zh ? 'Token 用量趋势' : 'Token usage trend'}</h2><p>{zh ? '所选周期内的创作节奏' : 'Your creative rhythm in this period'}</p></div><span><i />{zh ? '已同步用量' : 'Synced usage'}</span></div>
            <div className="personal-filter-row">
              <select aria-label={t('dashboard.agentFilter')} value={selectedAgent} onChange={(e) => setSelectedAgent(e.target.value)} className="form-input">
                <option value="all">{t('dashboard.allAgents')}</option>
                {displayFilters.agents.map((a) => { const key = typeof a === 'string' ? a : a.id; const label = typeof a === 'string' ? a : a.name; return <option key={key} value={key}>{label}</option>; })}
              </select>
              <select aria-label={t('dashboard.modelFilter')} value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)} className="form-input">
                <option value="all">{t('dashboard.allModels')}</option>
                {displayFilters.models.map((m) => { const key = typeof m === 'string' ? m : m.id; const label = typeof m === 'string' ? m : m.name; return <option key={key} value={key}>{label}</option>; })}
              </select>
              <small>{zh ? '仅筛选趋势图' : 'Chart filters only'}</small>
            </div>
            <TokenTrendChart trends={displayTrends} />
          </section>

          <section className="personal-analytics-section personal-agent-section">
            <div className="personal-section-heading"><div><h2>{t('dashboard.agentBreakdown')}</h2><p>{t('dashboard.agentBreakdownSub')}</p></div><span className="personal-source-count">{displayAgents.length} {t('dashboard.sourcesCount')}</span></div>
            <AgentBreakdown items={displayAgents} variant="donut" />
          </section>
        </div>

        <div className="personal-analytics-lower">
          <section className="personal-analytics-section personal-calendar-section"><ActivityCalendar days={displayCalendar} streakDays={displayStreak} /></section>
          <div className="personal-lower-stack">
            <section className="personal-analytics-section personal-skills-section">
              <div className="personal-section-heading"><div><h2><Sparkles size={17} />{t('dashboard.skillRanking')}</h2><p>{zh ? '你常用的创作能力' : 'Skills behind your work'}</p></div><span className="personal-source-count">{t('dashboard.topSkills')}</span></div>
              <SkillRanking skills={displaySkills} />
            </section>
            <section className="personal-analytics-section personal-sync-section">
              <div className="personal-section-heading"><h2><RefreshCw size={16} />{t('dashboard.syncStatus')}</h2><Button variant="ghost" size="sm" onClick={() => navigate('/settings/devices')}>{t('common.manage')} →</Button></div>
              <SyncStatusCard lastCommittedAt={displaySummary.sync.lastCommittedAt} status={syncStatus} />
            </section>
          </div>
        </div>
        <div className="personal-analytics-foot"><span>{zh ? '未知或不支持的指标不会按 0 计入。' : 'Unknown metrics are not counted as zero.'}</span><button type="button" onClick={() => navigate('/settings/privacy')}>{zh ? '管理公开设置 ↗' : 'Manage sharing ↗'}</button></div>
      </section>
    </div>
  );
};
