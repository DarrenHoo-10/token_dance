import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { ArrowDownRight, ArrowRight, ArrowUpRight, BarChart3, CheckCheck, Download, Flame, Layers3, Link2, LockKeyhole, RefreshCw, ShieldCheck, Sparkles, Trophy } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { UnauthorizedState } from '@/components/states/UnauthorizedState';
import { EmptyState } from '@/components/states/EmptyState';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { personalTokenRank } from '@/components/analytics/tokenRanking';
import { FilterSelect } from '@/components/common/FilterSelect';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { SyncStatusCard } from '@/components/analytics/SyncStatusCard';
import { UserAvatar } from '@/components/common/UserAvatar';
import { ChangeBadge } from '@/components/common/ChangeBadge';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import { formatPersonalCost } from '@/utils/cost';
import type {
  PersonalSummary,
  PersonalSummaryMetrics,
  TokenTrendsResponse,
  BreakdownItem,
  SkillItem,
  CalendarDay,
  FilterOptionsResponse,
  CollectorDevice,
  MetricValue,
  PublicUserProfile,
} from '@/types/api';
import '@/personal-analytics.css';

const PERIODS = [
  { key: 'today', zh: '过去 24 小时', en: 'Past 24 hours' },
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

function parseChange(metric?: MetricValue | null) {
  if (metric?.change == null || metric.change === '') return null;
  const value = parseFloat(metric.change);
  return Number.isFinite(value) ? value : null;
}

function protoCost(value: string | null) {
  if (!value) return null;
  return value.replace(/^(\$|¥|€|£)/, '$1 ');
}

function formatTokensParts(raw: string | null | undefined): { value: string; unit: string } | null {
  if (raw == null || raw === '') return null;
  const num = parseFloat(raw);
  if (!Number.isFinite(num)) return null;
  if (num >= 1_000_000_000) return { value: (num / 1_000_000_000).toFixed(2), unit: 'B' };
  if (num >= 1_000_000) return { value: (num / 1_000_000).toFixed(2), unit: 'M' };
  if (num >= 1_000) return { value: `${(num / 1_000).toFixed(1)}K`, unit: '' };
  return { value: Math.round(num).toLocaleString(), unit: '' };
}

function formatHoursParts(raw: string | null | undefined): { value: string; unit: string } | null {
  if (!raw) return null;
  const ms = parseFloat(raw);
  if (!Number.isFinite(ms)) return null;
  return { value: (ms / (1000 * 60 * 60)).toFixed(1), unit: 'h' };
}

function formatPercentParts(raw: string | null | undefined): { value: string; unit: string } | null {
  if (!raw) return null;
  const num = parseFloat(raw);
  if (!Number.isFinite(num)) return null;
  return { value: (num <= 1 ? num * 100 : num).toFixed(1), unit: '%' };
}

function formatLineParts(raw: string | null | undefined): { value: string; unit: string } | null {
  if (!raw) return null;
  const num = parseFloat(raw);
  if (!Number.isFinite(num)) return null;
  return { value: num.toFixed(1), unit: '' };
}

function formatCountParts(raw: string | null | undefined, locale: string): { value: string; unit: string } | null {
  if (raw == null || raw === '') return null;
  const num = parseFloat(raw);
  if (!Number.isFinite(num)) return null;
  return { value: Math.round(num).toLocaleString(locale), unit: '' };
}

type OverlayMetric = {
  key: string;
  name: string;
  value: string;
  unit: string;
  hint: string;
  change: number | null;
  supported: boolean;
};

function overlayMetrics(metrics: PersonalSummaryMetrics | undefined, zh: boolean, locale: string): OverlayMetric[] {
  const cost = formatPersonalCost(metrics?.estimatedCost, metrics?.estimatedCosts);
  const tokens = formatTokensParts(metrics?.totalTokens?.value);
  const lines = formatTokensParts(metrics?.generatedCodeLines?.value);
  const messages = formatCountParts(metrics?.messageCount?.value, locale);
  const duration = formatHoursParts(metrics?.activeDurationMs?.value);
  const input = formatTokensParts(metrics?.inputContextTokens?.value);
  const output = formatTokensParts(metrics?.outputTokens?.value);
  const cache = formatPercentParts(metrics?.cacheHitRate?.value);
  const perLine = formatLineParts(metrics?.tokensPerCodeLine?.value);
  const userMessages = formatCountParts(metrics?.userMessageCount?.value, locale);
  const dash = { value: '—', unit: '' };
  return [
    { key: 'totalTokens', name: zh ? '总 Token' : 'Total tokens', ...(metrics?.totalTokens?.supported === false ? dash : tokens ?? dash), hint: zh ? '本周期已同步用量' : 'Synced usage this period', change: parseChange(metrics?.totalTokens), supported: metrics?.totalTokens?.supported !== false },
    { key: 'estimatedCost', name: zh ? '预估费用' : 'Estimated cost', value: protoCost(cost.value) ?? '—', unit: '', hint: zh ? '估算值 · USD' : 'Estimated · USD', change: null, supported: cost.supported },
    { key: 'generatedCodeLines', name: zh ? '生成代码行' : 'Lines of code', ...(metrics?.generatedCodeLines?.supported === false ? dash : lines ?? dash), hint: zh ? '工具记录的代码行数' : 'Recorded by supported tools', change: parseChange(metrics?.generatedCodeLines), supported: metrics?.generatedCodeLines?.supported !== false },
    { key: 'messageCount', name: zh ? '总消息数' : 'Total messages', ...(metrics?.messageCount?.supported === false ? dash : messages ?? dash), hint: zh ? '工具记录的消息与轮次事件，非模型请求数' : 'Recorded message/turn events, not model requests', change: parseChange(metrics?.messageCount), supported: metrics?.messageCount?.supported !== false },
    { key: 'activeDurationMs', name: zh ? '会话总时长' : 'Session duration', ...(metrics?.activeDurationMs?.supported === false ? dash : duration ?? dash), hint: zh ? '已记录会话累计时长' : 'Combined recorded sessions', change: parseChange(metrics?.activeDurationMs), supported: metrics?.activeDurationMs?.supported !== false },
    { key: 'inputContextTokens', name: zh ? '输入上下文' : 'Input context', ...(metrics?.inputContextTokens?.supported === false ? dash : input ?? dash), hint: 'Prompt + Cache read', change: parseChange(metrics?.inputContextTokens), supported: metrics?.inputContextTokens?.supported !== false },
    { key: 'outputTokens', name: zh ? '输出 Token' : 'Output tokens', ...(metrics?.outputTokens?.supported === false ? dash : output ?? dash), hint: zh ? '补全与生成 Token' : 'Completion and generation', change: parseChange(metrics?.outputTokens), supported: metrics?.outputTokens?.supported !== false },
    { key: 'cacheHitRate', name: zh ? '缓存命中率' : 'Cache hit rate', ...(metrics?.cacheHitRate?.supported === false ? dash : cache ?? dash), hint: zh ? '缓存命中占比' : 'Share of cache hits', change: parseChange(metrics?.cacheHitRate), supported: metrics?.cacheHitRate?.supported !== false },
    { key: 'tokensPerCodeLine', name: zh ? '单行 Token' : 'Tokens per line', ...(metrics?.tokensPerCodeLine?.supported === false ? dash : perLine ?? dash), hint: zh ? '平均每行代码' : 'Average per line of code', change: parseChange(metrics?.tokensPerCodeLine), supported: metrics?.tokensPerCodeLine?.supported !== false },
    { key: 'userMessageCount', name: zh ? '用户消息数' : 'User messages', ...(metrics?.userMessageCount?.supported === false ? dash : userMessages ?? dash), hint: zh ? '用户触发的消息' : 'User-initiated messages', change: parseChange(metrics?.userMessageCount), supported: metrics?.userMessageCount?.supported !== false },
  ];
}

function publicMetrics(profile: PublicUserProfile | null): PersonalSummaryMetrics | undefined {
  if (!profile) return undefined;
  const unavailable: MetricValue = { value: null, supported: false };
  const tokenTotal = profile.showTokenTotal === false ? null : profile.tokenTotal ?? null;
  const codeLines = profile.codeLinesTotal ?? null;
  const estimatedCost = profile.estimatedCostTotal ?? null;
  return {
    estimatedCost: { amount: estimatedCost, currency: 'USD', supported: estimatedCost != null },
    totalTokens: { value: tokenTotal, supported: tokenTotal != null },
    generatedCodeLines: { value: codeLines, supported: codeLines != null },
    tokensPerCodeLine: tokenTotal && codeLines && Number(codeLines) > 0
      ? { value: String(Number(tokenTotal) / Number(codeLines)), supported: true } : unavailable,
    inputContextTokens: unavailable,
    outputTokens: unavailable,
    cacheHitRate: unavailable,
    activeDurationMs: unavailable,
    messageCount: unavailable,
    userMessageCount: unavailable,
  };
}

export const PersonalAnalytics: React.FC<{ onLeave?: () => void; active?: boolean; publicHandle?: string | null }> = ({ onLeave, active = true, publicHandle = null }) => {
  const { user, authenticated, loading: authLoading } = useAuth();
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const zh = locale === 'zh-CN';

  const [range, setRange] = useState(publicHandle ? '30d' : 'today');
  const [selectedAgent, setSelectedAgent] = useState('all');
  const [selectedModel, setSelectedModel] = useState('all');
  const [showDevices, setShowDevices] = useState(false);
  const [exporting, setExporting] = useState(false);

  const [summary, setSummary] = useState<PersonalSummary | null>(null);
  const [publicProfile, setPublicProfile] = useState<PublicUserProfile | null>(null);
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
  const [filterStatus, setFilterStatus] = useState<'loading' | 'ready' | 'error'>('loading');
  const [devices, setDevices] = useState<CollectorDevice[] | null>(null);

  const [error, setError] = useState<ApiError | Error | null>(null);
  const [trendError, setTrendError] = useState<ApiError | Error | null>(null);
  const [trendsReady, setTrendsReady] = useState(false);

  const periodName = PERIODS.find((item) => item.key === range);
  const periodLabel = periodName ? (zh ? periodName.zh : periodName.en) : range;

  const fetchBoard = useCallback(async () => {
    if (publicHandle) {
      const profile = await api.getPublicProfile(publicHandle);
      const publicSkills = profile.showSkillRanking === false ? null : await api.getPublicSkills(publicHandle, range).catch(() => null);
      setPublicProfile(profile);
      setAgentBreakdowns(profile.showAgentBreakdown === false ? [] : profile.agentBreakdown || []);
      setSkills(publicSkills?.skills || publicSkills?.items || []);
      setCalendarDays(profile.showActivityCalendar === false ? [] : profile.activityCalendar || []);
      setCalendarStreak(profile.showActivityCalendar === false ? 0 : profile.currentStreak || 0);
      setFilterOptions({ agents: profile.showAgentBreakdown === false ? [] : (profile.agentBreakdown || []).map(agent => ({ id: agent.key, name: agent.label })), providers: [], models: [] });
      setFilterStatus('ready');
      setError(null);
      return;
    }
    const [summaryRes, agentsRes, skillsRes] = await Promise.all([
      api.getPersonalSummary(range),
      api.getAgentBreakdowns(range),
      api.getPersonalSkills(range),
    ]);
    setSummary(summaryRes);
    setAgentBreakdowns(agentsRes.items || []);
    setSkills(skillsRes.skills || (skillsRes as unknown as { items: SkillItem[] }).items || []);
    setError(null);
  }, [range, publicHandle]);

  const fetchTrends = useCallback(async () => {
    setTrendError(null);
    if (publicHandle && publicProfile?.showTrends === false) {
      setTrends({ points: [] });
      setTrendsReady(true);
      return;
    }
    const params = {
      range,
      agent: selectedAgent !== 'all' ? selectedAgent : undefined,
      model: selectedModel !== 'all' ? selectedModel : undefined,
    };
    const trendsRes = publicHandle ? await api.getPublicTokenTrends(publicHandle, params) : await api.getTokenTrends(params);
    setTrends(trendsRes);
    setTrendsReady(true);
  }, [range, selectedAgent, selectedModel, publicHandle, publicProfile?.handle, publicProfile?.showTrends]);

  const fetchStatic = useCallback(async () => {
    setFilterStatus('loading');
    await Promise.all([
      api.getActivityCalendar('10w').then(calRes => { setCalendarDays(calRes.days || []); setCalendarStreak(calRes.currentStreak || 0); }).catch(() => setCalendarDays([])),
      api.getFilterOptions().then(filterRes => { setFilterOptions(filterRes); setFilterStatus('ready'); }).catch(() => setFilterStatus('error')),
    ]);
  }, []);

  useEffect(() => {
    if ((!authenticated && !publicHandle) || !active) return;
    let ignore = false;
    fetchBoard().catch((err) => {
      if (!ignore) setError(err instanceof ApiError ? err : new Error(String(err)));
    });
    return () => { ignore = true; };
  }, [authenticated, active, fetchBoard, publicHandle]);

  useEffect(() => {
    if ((!authenticated && !publicHandle) || !active || (publicHandle && !publicProfile)) return;
    let ignore = false;
    fetchTrends().catch((err) => {
      if (!ignore) {
        setTrends({ points: [] });
        setTrendError(err instanceof ApiError ? err : new Error(String(err)));
        setTrendsReady(true);
      }
    });
    return () => { ignore = true; };
  }, [authenticated, active, fetchTrends, publicHandle]);

  useEffect(() => {
    if (!authenticated || !active || publicHandle) return;
    fetchStatic().catch(() => setCalendarDays([]));
  }, [authenticated, active, fetchStatic, publicHandle]);

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

  const retryBoard = () => {
    fetchBoard().catch((err) => setError(err instanceof ApiError ? err : new Error(String(err))));
  };

  if (!publicHandle && authLoading && !user) return <LoadingState />;
  if (!publicHandle && !authenticated) return <UnauthorizedState />;
  if (!publicHandle && (user?.onboardingRequired || user?.productState === 'new')) {
    if (onLeave) onLeave();
    else navigate('/onboarding');
    return null;
  }
  if (publicHandle && !publicProfile && !error) return <LoadingState />;
  if (error && !summary && !publicProfile) {
    if (publicHandle && error instanceof ApiError && error.status === 404) {
      return <EmptyState icon={<LockKeyhole size={32} aria-hidden="true" />} title={t('publicProfile.unavailableTitle')} description={t('publicProfile.unavailableDesc')} />;
    }
    return <ErrorState error={error} onRetry={retryBoard} />;
  }

  const displayTrends = trends?.points || trends?.trends || [];
  const filtered = selectedAgent !== 'all' || selectedModel !== 'all';
  const chartEmpty = trendsReady && !trendError && displayTrends.length === 0 && !trends?.range;
  const selectionTotal = displayTrends.reduce((sum, point) => sum + (Number(point.tokenTotal) || 0), 0);
  const selectionLabel = formatSelectionTokens(selectionTotal, locale);
  const hourly = trendsHourly(trends, displayTrends);
  const syncStatus = summary?.sync.status || (summary?.sync.lastCommittedAt ? 'healthy' : 'unknown');
  const name = publicHandle ? publicProfile?.displayName || publicHandle : user?.displayName || user?.handle || (zh ? '开发者' : 'Builder');
  const rank = publicHandle ? publicProfile?.rank ?? null : personalTokenRank(summary);
  const rankDelta = rank != null ? (publicHandle ? publicProfile?.rankDelta : summary?.ranking.delta) : null;
  const RankIcon = rankDelta != null && rankDelta < 0 ? ArrowDownRight : ArrowUpRight;
  const metrics = overlayMetrics(publicHandle ? publicMetrics(publicProfile) : summary?.metrics, zh, locale);
  const publicUrl = publicHandle ? new URL(`${import.meta.env.BASE_URL}u/${encodeURIComponent(publicHandle)}`, window.location.origin).href : '';

  return (
    <div className="personal-analytics personal-analytics-page">
      <div className="dialog-eyebrow"><BarChart3 size={19} />{zh ? '个人数据' : publicHandle ? 'Personal analytics' : 'My analytics'}</div>
      <div className="analytics-heading">
        <div>
          <h2 id="personal-analytics-heading">{publicHandle ? (zh ? `${name} 的创造正在发生。` : `${name}'s ideas are taking shape.`) : (zh ? `${name}，你的创造正在发生。` : `${name}, your ideas are taking shape.`)}</h2>
          <p className="dialog-lead">{publicHandle ? `@${publicProfile?.handle || publicHandle}${publicProfile?.bio ? ` · ${publicProfile.bio}` : ''}` : (zh ? '从每一次协作，看见你的投入与创造。' : 'See the effort and creativity behind every collaboration.')}</p>
        </div>
        <UserAvatar
          url={publicHandle ? publicProfile?.avatarUrl : user?.avatarUrl}
          name={name}
          alt={name}
          className="personal-heading-avatar"
          fallbackClassName="personal-heading-avatar personal-heading-avatar-fallback"
        />
      </div>

      <div className="analytics-toolbar">
        <div className="range-control" aria-label={zh ? '个人数据周期' : 'Analytics period'}>
          {PERIODS.map((item) => (
            <button key={item.key} type="button" aria-pressed={range === item.key} onClick={() => setRange(item.key)}>
              {zh ? item.zh : item.en}
            </button>
          ))}
        </div>
        {publicHandle ? <button className="button secondary analytics-export" type="button" onClick={() => { navigator.clipboard.writeText(publicUrl); showToast(t('publicProfile.linkCopied'), 'success'); }}><Link2 size={15} />{zh ? '复制链接' : 'Copy link'}</button> : <button className="button secondary analytics-export" type="button" disabled={exporting} onClick={exportCurrentPeriod}>
          <Download size={15} />{t('dashboard.exportAction')}
        </button>}
      </div>

      <div className="analytics-metrics" aria-label={zh ? '10 项核心指标' : '10 core metrics'}>
        {metrics.map((metric) => (
          <div className="analytics-metric" key={metric.key} data-metric={metric.key}>
            <span className="analytics-metric-label">{metric.name}</span>
            <strong>{metric.supported ? metric.value : '—'}{metric.supported && metric.unit ? <small>{metric.unit}</small> : null}</strong>
            <div className="analytics-metric-note">
              {metric.change != null ? (
                <>
                  <ChangeBadge value={metric.change} en={!zh} />
                  <span>{zh ? (range === 'today' ? '较前 24h' : '较上期') : (range === 'today' ? 'vs prior 24h' : 'vs prior period')}</span>
                </>
              ) : (
                  <span>{publicHandle ? (!metric.supported ? (zh ? '未公开或暂无此项数据' : 'Private or unavailable') : metric.key === 'totalTokens' ? (zh ? '累计公开用量' : 'Public all-time usage') : metric.hint) : !summary?.sync.lastCommittedAt && metric.value === '—' ? (zh ? '等待首次同步' : 'Waiting for first sync') : !metric.supported ? (zh ? '当前来源暂无此项数据' : 'Unavailable from current sources') : metric.value === '—' ? (zh ? '本周期暂无记录' : 'No records in this period') : metric.hint}</span>
              )}
            </div>
          </div>
        ))}
      </div>

      <div className="analytics-context">
        <span>
          <Trophy size={14} />{publicHandle ? (zh ? '公开排名' : 'Public rank') : (zh ? '过去 24h 排名' : 'Past 24h rank')}{' '}
          <b>{rank != null ? `#${rank}` : summary ? (zh ? '暂未上榜' : 'Not ranked yet') : '—'}</b>
          {rankDelta != null && rankDelta !== 0 && (
            <span className={rankDelta < 0 ? 'rank-down' : 'rank-up'}><RankIcon size={13} />{Math.abs(rankDelta)}</span>
          )}
        </span>
        <span><Flame size={14} />{zh ? '连续活跃' : 'Day streak'} <b>{publicHandle && publicProfile?.showActivityCalendar === false ? (zh ? '未公开' : 'Private') : `${calendarStreak} ${zh ? '天' : 'days'}`}</b></span>
        <span><ShieldCheck size={14} />{publicHandle ? (zh ? '公开数据' : 'Public data') : summary?.sync.lastCommittedAt ? (zh ? '已同步至网站' : 'Synced to website') : (zh ? '等待首次同步' : 'Waiting for first sync')}</span>
        <span className="context-time">{formatContextDate(publicHandle ? publicProfile?.dataWatermarkAt || publicProfile?.generatedAt : summary?.range.to)} · UTC+8</span>
      </div>

      <div className="analytics-middle">
        <section className="analytics-section trend-section">
          <div className="analytics-section-title">
            <div>
              <h3>{zh ? 'Token 用量趋势' : 'Token usage trend'}</h3>
              <p>{zh ? `${periodLabel}的创作节奏` : `${periodLabel} of creativity`}</p>
            </div>
            <span className="source-counter"><span />{publicHandle ? (zh ? '公开用量' : 'Public usage') : (zh ? '已同步用量' : 'Synced usage')}</span>
          </div>
          <div className="analytics-filter-row">
            <FilterSelect label={zh ? '趋势 Agent 筛选' : 'Trend agent filter'} value={selectedAgent} onChange={setSelectedAgent} disabled={filterStatus !== 'ready' || !filterOptions.agents.length} options={[
              { value: 'all', label: filterOptions.agents.length ? (zh ? '全部 Agent' : 'All agents') : (zh ? '暂无 Agent' : 'No agents') },
              ...filterOptions.agents.map(agent => ({ value: optionKey(agent), label: optionLabel(agent) })),
            ]} />
            <FilterSelect label={zh ? '趋势模型筛选' : 'Trend model filter'} value={selectedModel} onChange={setSelectedModel} disabled={filterStatus !== 'ready' || !filterOptions.models.length} options={[
              { value: 'all', label: filterOptions.models.length ? (zh ? '全部模型' : 'All models') : (zh ? '暂无模型' : 'No models') },
              ...filterOptions.models.map(model => ({ value: optionKey(model), label: optionLabel(model) })),
            ]} />
            <small>{zh ? '仅筛选趋势图' : 'Chart filters only'}</small>
          </div>
          {publicHandle ? null : filterStatus === 'error' ? <p className="filter-status" role="status">{zh ? '筛选选项加载失败。' : 'Filters could not be loaded. '}<button type="button" className="text-link" onClick={fetchStatic}>{t('common.retry')}</button></p> : filterStatus === 'loading' ? <p className="filter-status">{zh ? '正在加载筛选选项…' : 'Loading filters…'}</p> : (!filterOptions.agents.length || !filterOptions.models.length) && <p className="filter-status">{zh ? '尚未同步的来源或模型暂不可筛选。' : 'Sources and models become available after sync.'}</p>}
          {publicHandle && publicProfile?.showTrends === false ? (
            <div className="analytics-chart-empty"><LockKeyhole size={28} /><strong>{zh ? '用户未公开 Token 趋势' : 'Token trend is private'}</strong></div>
          ) : !trendsReady && !displayTrends.length ? (
            <div className="analytics-chart-empty" aria-busy="true">
              <Layers3 size={28} />
              <strong>{t('common.loading')}</strong>
            </div>
          ) : trendError && !displayTrends.length ? (
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
            <TokenTrendChart key={`${range}-${selectedAgent}-${selectedModel}`} trends={displayTrends} range={trends?.range} />
          )}
          <div className="trend-precise-total">
            <span>{zh ? '筛选内 Token' : 'Tokens in selection'}</span>
            <strong>{chartEmpty || !selectionLabel ? (zh ? '暂无记录' : 'No records') : selectionLabel}</strong>
            <small>{hourly ? (zh ? '按小时' : 'Hourly') : (zh ? '按天' : 'Daily')}</small>
          </div>
        </section>

        <section className="analytics-section agent-section">
          <div className="analytics-section-title">
            <div>
              <h3>{zh ? 'Agent 构成' : 'Agent breakdown'}</h3>
              <p>{publicHandle ? (zh ? '近 30 天 Token 花在了哪里' : 'Where tokens went in the last 30 days') : (zh ? '了解 Token 花在了哪里' : 'Where your tokens go')}</p>
            </div>
            <span className="source-count">{publicHandle && publicProfile?.showAgentBreakdown === false ? (zh ? '未公开' : 'Private') : `${agentBreakdowns.length} ${zh ? '个来源' : 'sources'}`}</span>
          </div>
          {publicHandle && publicProfile?.showAgentBreakdown === false ? <p className="filter-status">{zh ? '用户未公开 Agent 构成' : 'Agent breakdown is private'}</p> : <AgentBreakdown items={agentBreakdowns} variant="donut" />}
        </section>
      </div>

      <div className="analytics-lower">
        <section className="analytics-section calendar-section">
          {publicHandle && publicProfile?.showActivityCalendar === false ? <p className="filter-status">{zh ? '用户未公开活跃日历' : 'Activity calendar is private'}</p> : <ActivityCalendar days={calendarDays} streakDays={calendarStreak} />}
        </section>
        <div className="analytics-lower-stack">
          <section className="analytics-section skills-section">
            <div className="analytics-section-title">
              <div>
                <h3><Sparkles size={17} />{zh ? 'Skill 排行' : 'Skill ranking'}</h3>
                <p>{publicHandle ? (zh ? '公开的创作能力' : 'Shared creative skills') : (zh ? '你常用的创作能力' : 'Skills behind your work')}</p>
              </div>
              <span className="source-count">Top 4</span>
            </div>
            {publicHandle && publicProfile?.showSkillRanking === false ? <p className="filter-status">{zh ? '用户未公开 Skill 排行' : 'Skill ranking is private'}</p> : <SkillRanking skills={skills} />}
          </section>
          <section className="analytics-section sync-section">
            <div className="analytics-section-title">
              <h3>{publicHandle ? <ShieldCheck size={16} /> : <RefreshCw size={16} />}{publicHandle ? (zh ? '公开资料' : 'Public profile') : t('dashboard.syncStatus')}</h3>
              {!publicHandle && <span className={`sync-healthy sync-${syncStatus}`}>
                <CheckCheck size={13} />
                {syncStatus === 'healthy' ? t('common.healthy') : syncStatus === 'warning' ? t('common.warning') : t('common.unknown')}
              </span>}
            </div>
            {publicHandle ? <p className="filter-status">{zh ? '此页只展示用户选择公开的统计信息。' : 'This page shows only statistics the user chose to share.'}</p> : <SyncStatusCard
              lastCommittedAt={summary?.sync.lastCommittedAt ?? null}
              status={syncStatus}
              pendingLocalCount={summary?.sync.pendingLocalCount}
              devices={devices || []}
              devicesOpen={showDevices}
              devicesLoading={showDevices && devices === null}
              onToggleDevices={toggleDevices}
            />}
          </section>
        </div>
      </div>
      <div className="analytics-footnote">
        <span>{publicHandle ? (zh ? '未公开或不支持的指标不会按 0 计入。' : 'Private or unsupported metrics are not counted as zero.') : (zh ? '未知或不支持的指标不会按 0 计入。' : 'Unknown metrics are not counted as zero.')}</span>
        {!publicHandle && <button type="button" className="text-link" onClick={() => { onLeave?.(); navigate('/settings/privacy'); }}>
          {zh ? '管理公开设置' : 'Manage sharing'}<ArrowUpRight size={14} />
        </button>}
      </div>
    </div>
  );
};
