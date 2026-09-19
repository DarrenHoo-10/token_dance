import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowDownRight, ArrowRight, ArrowUpRight, BarChart3, CheckCheck, ChevronDown, Download, Flame, Layers3, RefreshCw, ShieldCheck, Sparkles, Trophy } from 'lucide-react';
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
import { UserAvatar } from '@/components/common/UserAvatar';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import type {
  PersonalSummary,
  TokenTrendsResponse,
  BreakdownItem,
  SkillItem,
  CalendarDay,
  FilterOptionsResponse,
  CollectorDevice,
} from '@/types/api';

const PERIODS = [
  { key: 'today', zh: '今天', en: 'Today' },
  { key: '7d', zh: '近 7 天', en: '7 days' },
  { key: '30d', zh: '近 30 天', en: '30 days' },
  { key: 'all', zh: '全部时间', en: 'All time' },
] as const;

function optionKey(item: string | { id: string; name: string }) {
  return typeof item === 'string' ? item : item.id;
}

function optionLabel(item: string | { id: string; name: string }) {
  return typeof item === 'string' ? item : item.name;
}

function formatContextDate(value?: string | null) {
  if (!value) return '—';
  const match = value.match(/(\d{4})-(\d{2})-(\d{2})/);
  return match ? `${match[2]}.${match[3]}` : value;
}

function formatSelectionTokens(total: number, locale: string) {
  if (!Number.isFinite(total) || total <= 0) return null;
  if (total >= 1_000_000) return `${(total / 1_000_000).toFixed(2)}M`;
  if (total >= 1_000) return `${(total / 1_000).toFixed(1)}K`;
  return total.toLocaleString(locale);
}

function trendsHourly(trends: TokenTrendsResponse | null, points: { date: string }[]) {
  if (trends?.granularity === 'hour' || trends?.granularity === 'hourly') return true;
  return points.some((point) => /[T ]/.test(point.date) && point.date.includes(':'));
}

export const PersonalAnalytics: React.FC<{ onLeave?: () => void }> = ({ onLeave }) => {
  const { user, authenticated, loading: authLoading } = useAuth();
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const zh = locale === 'zh-CN';

  const [range, setRange] = useState('today');
  const [selectedAgent, setSelectedAgent] = useState('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [showDevices, setShowDevices] = useState(false);
  const [exporting, setExporting] = useState(false);

  const [summary, setSummary] = useState<PersonalSummary | null>(null);
  const [trends, setTrends] = useState<TokenTrendsResponse | null>(null);
  const [agentBreakdowns, setAgentBreakdowns] = useState<BreakdownItem[]>([]);
  const [skills, setSkills] = useState<SkillItem[]>([]);
  const [calendarDays, setCalendarDays] = useState<CalendarDay[]>([]);
  const [calendarStreak, setCalendarStreak] = useState(0);
  const [filterOptions, setFilterOptions] = useState<FilterOptionsResponse>({
    agents: [],
    providers: [],
    models: [],
  });
  const [devices, setDevices] = useState<CollectorDevice[] | null>(null);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);
  const [trendError, setTrendError] = useState<ApiError | Error | null>(null);
  const [trendsReady, setTrendsReady] = useState(false);

  const periodName = PERIODS.find((item) => item.key === range);
  const periodLabel = periodName ? (zh ? periodName.zh : periodName.en) : range;

  const fetchBoard = useCallback(async () => {
    setError(null);
    const [summaryRes, agentsRes, skillsRes] = await Promise.all([
      api.getPersonalSummary(range),
      api.getAgentBreakdowns(range),
      api.getPersonalSkills(range),
    ]);
    setSummary(summaryRes);
    setAgentBreakdowns(agentsRes.items || []);
    setSkills(skillsRes.skills || (skillsRes as unknown as { items: SkillItem[] }).items || []);
    setLoading(false);
  }, [range]);

  const fetchTrends = useCallback(async () => {
    setTrendsReady(false);
    setTrendError(null);
    const trendsRes = await api.getTokenTrends({
      range,
      agent: selectedAgent !== 'all' ? selectedAgent : undefined,
      model: selectedModel !== 'all' ? selectedModel : undefined,
    });
    setTrends(trendsRes);
    setTrendsReady(true);
  }, [range, selectedAgent, selectedModel]);

  const fetchStatic = useCallback(async () => {
    const [calRes, filterRes] = await Promise.all([
      api.getActivityCalendar('10w'),
      api.getFilterOptions(),
    ]);
    setCalendarDays(calRes.days || []);
    setCalendarStreak(calRes.currentStreak || 0);
    setFilterOptions(filterRes);
  }, []);

  useEffect(() => {
    if (!authenticated) return;
    let ignore = false;
    fetchBoard().catch((err) => {
      if (!ignore) {
        setError(err instanceof ApiError ? err : new Error(String(err)));
        setLoading(false);
      }
    });
    return () => { ignore = true; };
  }, [authenticated, fetchBoard]);

  useEffect(() => {
    if (!authenticated) return;
    let ignore = false;
    fetchTrends().catch((err) => {
      if (!ignore) {
        setTrends({ points: [] });
        setTrendError(err instanceof ApiError ? err : new Error(String(err)));
        setTrendsReady(true);
      }
    });
    return () => { ignore = true; };
  }, [authenticated, fetchTrends]);

  useEffect(() => {
    if (!authenticated) return;
    fetchStatic().catch(() => setCalendarDays([]));
  }, [authenticated, fetchStatic]);

  const toggleDevices = async () => {
    const next = !showDevices;
    setShowDevices(next);
    if (!next || devices) return;
    try {
      const res = await api.getDevices();
      setDevices(res.devices || []);
    } catch {
      setDevices([]);
    }
  };

  const exportCurrentPeriod = async () => {
    try {
      setExporting(true);
      await api.createExport(
        { scope: 'all_aggregates', format: 'csv', filter: { range } },
        `me-${range}-${Date.now()}`,
      );
      showToast(zh ? '已创建当前周期的导出任务' : 'Export queued for this period', 'success');
    } catch (err) {
      showToast(err instanceof ApiError ? getApiErrorMessage(t, err) : t('errors.unknown'), 'error');
    } finally {
      setExporting(false);
    }
  };

  if (authLoading) return <LoadingState />;
  if (!authenticated) return <UnauthorizedState />;
  if (user?.onboardingRequired || user?.productState === 'new') {
    navigate('/onboarding');
    return null;
  }
  if (loading && !summary) return <LoadingState message={t('common.loading')} />;
  if (error && !summary) return <ErrorState error={error} onRetry={fetchBoard} />;
  if (!summary) return <LoadingState message={t('common.loading')} />;

  const displayTrends = trends?.points || trends?.trends || [];
  const filtered = selectedAgent !== 'all' || selectedModel !== 'all';
  const chartEmpty = trendsReady && !trendError && displayTrends.length === 0;
  const selectionTotal = displayTrends.reduce((sum, point) => sum + (Number(point.tokenTotal) || 0), 0);
  const selectionLabel = formatSelectionTokens(selectionTotal, locale);
  const hourly = trendsHourly(trends, displayTrends);
  const syncStatus = summary.sync.status || (summary.sync.lastCommittedAt ? 'healthy' : 'unknown');
  const name = user?.displayName || user?.handle || (zh ? '开发者' : 'Builder');
  const rankDelta = summary.ranking.delta;
  const RankIcon = rankDelta != null && rankDelta < 0 ? ArrowDownRight : ArrowUpRight;

  return (
    <div className="personal-analytics personal-analytics-page">
      <div className="personal-kicker"><BarChart3 size={18} />{zh ? '个人数据' : 'My analytics'}</div>
      <div className="personal-analytics-heading">
        <div>
          <h2 id="personal-analytics-heading">{zh ? `${name}，你的创造正在发生。` : `${name}, your ideas are taking shape.`}</h2>
          <p>{zh ? '从每一次协作，看见你的投入与创造。' : 'See the effort and creativity behind every collaboration.'}</p>
        </div>
        <UserAvatar
          url={user?.avatarUrl}
          name={name}
          alt={name}
          className="personal-heading-avatar"
          fallbackClassName="personal-heading-avatar personal-heading-avatar-fallback"
        />
      </div>

      <div className="personal-analytics-toolbar">
        <div className="range-control" aria-label={zh ? '个人数据周期' : 'Analytics period'}>
          {PERIODS.map((item) => (
            <button key={item.key} type="button" aria-pressed={range === item.key} onClick={() => setRange(item.key)}>
              {zh ? item.zh : item.en}
            </button>
          ))}
        </div>
        <button className="button secondary analytics-export" type="button" disabled={exporting} onClick={exportCurrentPeriod}>
          <Download size={15} />{t('dashboard.exportAction')}
        </button>
      </div>

      <MetricGrid metrics={summary.metrics} />

      <div className="personal-analytics-context">
        <span>
          <Trophy size={14} />{zh ? '今日排名' : 'Today’s rank'}{' '}
          <b>{summary.ranking.rank ? `#${summary.ranking.rank}` : '—'}</b>
          {rankDelta != null && rankDelta !== 0 && (
            <em className={rankDelta < 0 ? 'rank-down' : 'rank-up'}><RankIcon size={13} />{Math.abs(rankDelta)}</em>
          )}
        </span>
        <span><Flame size={14} />{zh ? '连续活跃' : 'Day streak'} <b>{calendarStreak} {zh ? '天' : 'days'}</b></span>
        <span><ShieldCheck size={14} />{summary.sync.lastCommittedAt ? (zh ? '已同步至网站' : 'Synced to website') : (zh ? '等待首次同步' : 'Waiting for first sync')}</span>
        <span className="personal-context-time">{formatContextDate(summary.range.to)} · UTC+8</span>
      </div>

      <div className="personal-analytics-middle">
        <section className="personal-analytics-section personal-trend-section">
          <div className="personal-section-heading">
            <div>
              <h3>{zh ? 'Token 用量趋势' : 'Token usage trend'}</h3>
              <p>{zh ? `${periodLabel}的创作节奏` : `${periodLabel} of creativity`}</p>
            </div>
            <span><i />{zh ? '已同步用量' : 'Synced usage'}</span>
          </div>
          <div className="personal-filter-row">
            <label>
              <span className="sr-only">{t('dashboard.agentFilter')}</span>
              <select aria-label={t('dashboard.agentFilter')} value={selectedAgent} onChange={(event) => setSelectedAgent(event.target.value)}>
                <option value="all">{t('dashboard.allAgents')}</option>
                {filterOptions.agents.map((agent) => {
                  const key = optionKey(agent);
                  return <option key={key} value={key}>{optionLabel(agent)}</option>;
                })}
              </select>
              <ChevronDown size={12} />
            </label>
            <label>
              <span className="sr-only">{t('dashboard.modelFilter')}</span>
              <select aria-label={t('dashboard.modelFilter')} value={selectedModel} onChange={(event) => setSelectedModel(event.target.value)}>
                <option value="all">{t('dashboard.allModels')}</option>
                {filterOptions.models.map((model) => {
                  const key = optionKey(model);
                  return <option key={key} value={key}>{optionLabel(model)}</option>;
                })}
              </select>
              <ChevronDown size={12} />
            </label>
            <small>{zh ? '仅筛选趋势图' : 'Chart filters only'}</small>
          </div>
          {!trendsReady ? (
            <LoadingState message={t('common.loading')} />
          ) : trendError ? (
            <ErrorState error={trendError} onRetry={fetchTrends} />
          ) : chartEmpty ? (
            <div className="analytics-chart-empty">
              <Layers3 size={28} />
              <strong>{filtered ? (zh ? '没有符合筛选的记录' : 'No matching records') : t('dashboard.noTrendData')}</strong>
              <span>{filtered ? (zh ? '试试其他 Agent 或模型组合。' : 'Try another agent or model.') : (zh ? '这个周期还没有同步用量。' : 'No synced usage in this period yet.')}</span>
              {filtered && (
                <button type="button" className="text-link" onClick={() => { setSelectedAgent('all'); setSelectedModel('all'); }}>
                  {zh ? '重置筛选' : 'Reset filters'}<ArrowRight size={14} />
                </button>
              )}
            </div>
          ) : (
            <TokenTrendChart key={`${range}-${selectedAgent}-${selectedModel}`} trends={displayTrends} />
          )}
          <div className="trend-precise-total">
            <span>{zh ? '筛选内 Token' : 'Tokens in selection'}</span>
            <strong>{chartEmpty || !selectionLabel ? (zh ? '暂无记录' : 'No records') : selectionLabel}</strong>
            <small>{hourly ? (zh ? '按小时' : 'Hourly') : (zh ? '按天' : 'Daily')}</small>
          </div>
        </section>

        <section className="personal-analytics-section personal-agent-section">
          <div className="personal-section-heading">
            <div>
              <h3>{zh ? 'Agent 构成' : 'Agent breakdown'}</h3>
              <p>{zh ? '了解 Token 花在了哪里' : 'Where your tokens go'}</p>
            </div>
            <span className="personal-source-count">{agentBreakdowns.length} {zh ? '个来源' : 'sources'}</span>
          </div>
          <AgentBreakdown items={agentBreakdowns} variant="donut" />
        </section>
      </div>

      <div className="personal-analytics-lower">
        <section className="personal-analytics-section personal-calendar-section">
          <ActivityCalendar days={calendarDays} streakDays={calendarStreak} />
        </section>
        <div className="personal-lower-stack">
          <section className="personal-analytics-section personal-skills-section">
            <div className="personal-section-heading">
              <div>
                <h3><Sparkles size={17} />{zh ? 'Skill 排行' : 'Skill ranking'}</h3>
                <p>{zh ? '你常用的创作能力' : 'Skills behind your work'}</p>
              </div>
              <span className="personal-source-count">Top 4</span>
            </div>
            <SkillRanking skills={skills} />
          </section>
          <section className="personal-analytics-section personal-sync-section">
            <div className="personal-section-heading">
              <h3><RefreshCw size={16} />{t('dashboard.syncStatus')}</h3>
              <span className={`sync-healthy sync-${syncStatus}`}>
                <CheckCheck size={13} />
                {syncStatus === 'healthy' ? t('common.healthy') : syncStatus === 'warning' ? t('common.warning') : t('common.unknown')}
              </span>
            </div>
            <SyncStatusCard
              lastCommittedAt={summary.sync.lastCommittedAt}
              status={syncStatus}
              pendingLocalCount={summary.sync.pendingLocalCount}
              devices={devices || []}
              devicesOpen={showDevices}
              devicesLoading={showDevices && devices === null}
              onToggleDevices={toggleDevices}
            />
          </section>
        </div>
      </div>
      <div className="personal-analytics-foot">
        <span>{zh ? '未知或不支持的指标不会按 0 计入。' : 'Unknown metrics are not counted as zero.'}</span>
        <button type="button" className="text-link" onClick={() => { onLeave?.(); navigate('/docs/privacy'); }}>
          {zh ? '查看公开说明' : 'How sharing works'}<ArrowUpRight size={14} />
        </button>
      </div>
    </div>
  );
};
