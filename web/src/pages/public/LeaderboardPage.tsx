import { HomeLeaderboard } from '@/components/analytics/HomeLeaderboard';
import { publicLeaderboardName } from '@/components/analytics/leaderboardName';
import { CommunityShareBoard } from '@/components/analytics/CommunityShareBoard';
import { HarnessMark } from '@/components/common/HarnessMark';
import { UserAvatar } from '@/components/common/UserAvatar';
import { resolveHarnessBrand } from '@/components/common/harnessBrand';
import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowRight, ArrowUpRight, BarChart3, Code2, Crown, Download, Monitor, UsersRound, Wallet, Zap,
  Flame, ShieldCheck, TrendingDown, TrendingUp, HelpCircle,
} from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { usePersonalAnalytics } from '@/context/PersonalAnalyticsContext';
import { usePublicHomeResource } from '@/hooks/usePublicHomeResource';
import { useVisibleRefresh } from '@/hooks/useVisibleRefresh';
import { readHomeBoard, readHomeCommunity, writeHomeBoard, writeHomeCommunity } from '@/utils/publicHomeCache';
import { api, ApiError } from '@/api/client';
import type {
  LeaderboardEntry, LeaderboardResponse, PersonalSummary, CalendarDay, CommunityStatsResponse,
  PublicUserProfile, TimeRange, TokenTrendItem, TokenTrendsResponse,
} from '@/types/api';
import { formatCommunityCost } from '@/utils/cost';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { calendarPeriodChange } from '@/components/analytics/calendarPeriodChange';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { hasRankedTokens, personalTokenRank } from '@/components/analytics/tokenRanking';

type Range = 'Today' | '7 Days' | '30 Days' | 'All Time';

const ranges: Range[] = ['Today', '7 Days', '30 Days', 'All Time'];
const windowByRange: Record<Range, 'today' | '7d' | '30d' | 'all'> = { Today: 'today', '7 Days': '7d', '30 Days': '30d', 'All Time': 'all' };

function readHomeBoardView(key: string): Partial<LeaderboardResponse> | null {
  const entries = readHomeBoard(key);
  return entries ? { entries, totalParticipants: entries.length } : null;
}

function writeHomeBoardView(key: string, board: Partial<LeaderboardResponse>) {
  writeHomeBoard(key, board.entries ?? []);
}

function formatTokens(raw: string | null | undefined): string {
  if (raw == null || raw === '') return '—';
  const value = Number(raw);
  if (!Number.isFinite(value) || value < 0) return '—';
  if (value === 0) return '0';
  if (value >= 1e9) return `${(value / 1e9).toFixed(1)}B`;
  if (value >= 1e6) return `${(value / 1e6).toFixed(1)}M`;
  if (value >= 1e3) return `${(value / 1e3).toFixed(1)}K`;
  return String(Math.round(value));
}

function formatPercentile(value: number | string): string {
  const n = Number(value);
  if (!Number.isFinite(n)) return '—';
  return (Math.ceil(n * 100) / 100).toFixed(2);
}

function publicTrendPoints(res: TokenTrendsResponse | null): TokenTrendItem[] {
  if (!res || res.visible === false) return [];
  return res.points || res.trends || [];
}

function DeltaChip({ value, suffix }: { value?: number | null; suffix?: string }) {
  if (value == null || !Number.isFinite(value)) return null;
  const positive = value >= 0;
  return (
    <span className={`hero-delta ${value === 0 ? 'flat' : positive ? 'up' : 'down'}`}>
      {positive ? '↑' : '↓'} {positive ? '+' : '−'}{Math.abs(value).toFixed(1)}%{suffix && <span className="sky-delta-suffix"> {suffix}</span>}
    </span>
  );
}

function HeroMiniCard({ label, value, delta, icon, help, unit }: { label: string; value: string; delta?: number | null; icon: React.ReactNode; help?: string; unit?: string }) {
  return (
    <div className="hero-mini-card">
      <span className="sky-metric-icon" aria-hidden="true">{icon}</span>
      <div>
        <span className="hero-mini-label">{label}{help && <details className="metric-help"><summary aria-label={`${label} · ${help}`}><HelpCircle size={13} /></summary><p>{help}</p></details>}</span>
        <div className="sky-metric-value">
          <strong className="hero-mini-value">{value ?? '—'}</strong>
          {unit && <small>{unit}</small>}
          <DeltaChip value={delta} />
        </div>
      </div>
    </div>
  );
}

function TrendBadge({ value }: { value: number | null | undefined }) {
  if (value == null || value === 0) return null;
  const positive = value > 0;
  return <span className={`trend-badge ${positive ? 'positive' : 'negative'}`}>{positive ? <TrendingUp /> : <TrendingDown />}{Math.abs(value)}</span>;
}

function PersonAvatar({ entry, className = '' }: { entry: LeaderboardEntry; className?: string }) {
  const name = publicLeaderboardName(entry);
  return <UserAvatar url={entry.avatarUrl} name={name} className={`leader-avatar ${className}`} fallbackClassName={`leader-avatar ${className} avatar-fallback`} alt={`${name} profile`} fetchPriority="high" />;
}

function PodiumCard({ entry, selected, onSelect, zh }: {
  entry: LeaderboardEntry;
  selected: boolean;
  onSelect: (entry: LeaderboardEntry) => void;
  zh: boolean;
}) {
  const winner = entry.rankNo === 1;
  const name = publicLeaderboardName(entry);
  return (
    <button
      type="button"
      className={`podium-card ${winner ? 'winner' : ''} ${selected ? 'selected' : ''}`}
      aria-pressed={selected}
      aria-label={zh ? `查看 ${name} 的公开创作轨迹` : `Show ${name}'s public creative rhythm`}
      onClick={() => onSelect(entry)}
    >
      <div className={`rank-medal rank-${entry.rankNo}`}>{entry.rankNo}</div>
      <div className="podium-avatar-wrap">
        <PersonAvatar entry={entry} className="podium-avatar" />
        {winner && <Crown className="crown" size={28} aria-hidden="true" />}
      </div>
      <div className="podium-id">
        <strong>{name}</strong>
      </div>
      <div className="podium-score-row"><span>{formatTokens(entry.metricValue)}</span></div>
      <small className="sky-podium-unit">Token</small>
    </button>
  );
}

export const LeaderboardPage: React.FC = () => {
  const { locale } = useLocale();
  const { user, authenticated } = useAuth();
  const personalAnalytics = usePersonalAnalytics();
  const zh = locale === 'zh-CN';
  const accountKey = user?.userId ?? user?.handle ?? '';
  const [range, setRange] = useState<Range>('Today');
  const [summary, setSummary] = useState<PersonalSummary | null>(null);
  const [allTimeSummary, setAllTimeSummary] = useState<PersonalSummary | null>(null);
  const [calendarDays, setCalendarDays] = useState<CalendarDay[]>([]);
  const [streak, setStreak] = useState(0);
  const [refreshTick, setRefreshTick] = useState(0);
  const [selectedHandle, setSelectedHandle] = useState<string | null>(null);
  const [selectedEntry, setSelectedEntry] = useState<LeaderboardEntry | null>(null);
  const [trendState, setTrendState] = useState<{ key: string | null; points: TokenTrendItem[]; range: TimeRange | null; ready: boolean }>({ key: null, points: [], range: null, ready: false });
  const [selectedStreak, setSelectedStreak] = useState<number | null>(null);
  const [selectedProfile, setSelectedProfile] = useState<PublicUserProfile | null>(null);
  const trendCache = useRef(new Map<string, { points: TokenTrendItem[]; range: TimeRange | null; loadedAt: number }>());

  const fetchLeaderboard = useCallback(
    () => api.getLeaderboardView(authenticated, { window: windowByRange[range], limit: 10 }),
    [accountKey, authenticated, range],
  );
  const board = usePublicHomeResource(`board:${windowByRange[range]}`, readHomeBoardView, writeHomeBoardView, fetchLeaderboard, refreshTick);
  const boardSummary = board.data ?? {};
  const entries = boardSummary.entries ?? [];
  const loading = board.data === null && !board.failed;
  const loadError = board.failed;
  const selectedCommunityWindow = windowByRange[range];
  const loadCommunity = useCallback(async () => {
    const stats = await api.getCommunityStats(selectedCommunityWindow);
    if (stats.window !== selectedCommunityWindow) throw new Error('Community statistics window does not match the selected period');
    return stats;
  }, [selectedCommunityWindow]);
  const communityResource = usePublicHomeResource(`community:${selectedCommunityWindow}`, readHomeCommunity, writeHomeCommunity, loadCommunity, refreshTick);
  const community: CommunityStatsResponse | null = communityResource.data;

  const loadPersonal = useCallback(() => {
    if (!authenticated) {
      setSummary(null);
      setAllTimeSummary(null);
      setCalendarDays([]);
      setStreak(0);
      return () => {};
    }
    let cancelled = false;
    api.getPersonalSummary('all')
      .then((result) => { if (!cancelled) setAllTimeSummary(result); })
      .catch(() => { /* Keep unavailable historical totals distinct from zero. */ });
    (async () => {
      try {
        const [summaryRes, calRes] = await Promise.all([
          api.getPersonalSummary('today'),
          api.getActivityCalendar('10w'),
        ]);
        if (cancelled) return;
        setSummary(summaryRes);
        setCalendarDays(calRes.days || []);
        setStreak(calRes.currentStreak || 0);
      } catch {
        // Keep existing side cards when a background refresh fails.
      }
    })();
    return () => { cancelled = true; };
  }, [authenticated, accountKey]);

  useEffect(() => loadPersonal(), [loadPersonal, refreshTick]);
  useVisibleRefresh(() => setRefreshTick((tick) => tick + 1));

  useEffect(() => {
    if (!entries.length) {
      // A new period has no entries until its board request finishes. Keep the
      // current selection so its trend can load without waiting for the board.
      if (board.data !== null) {
        setSelectedHandle(null);
        setSelectedEntry(null);
      }
      return;
    }
    setSelectedHandle((current) => {
      if (current && entries.some((entry) => entry.handle === current)) return current;
      return (entries.find((entry) => entry.rankNo === 1) ?? entries[0]).handle;
    });
  }, [entries, board.data]);

  useEffect(() => {
    setSelectedEntry(entries.find((entry) => entry.handle === selectedHandle) ?? null);
  }, [entries, selectedHandle]);

  useEffect(() => {
    if (!selectedHandle) {
      setSelectedStreak(null);
      setSelectedProfile(null);
      return;
    }
    let cancelled = false;
    setSelectedProfile(null);
    setSelectedStreak(null);
    api.getPublicProfile(selectedHandle).then((profile) => {
      if (cancelled) return;
      setSelectedProfile(profile);
      setSelectedStreak(profile?.currentStreak ?? null);
    }).catch(() => {
      if (cancelled) return;
      setSelectedProfile(null);
      setSelectedStreak(null);
    });
    return () => { cancelled = true; };
  }, [selectedHandle]);

  useEffect(() => {
    if (!selectedHandle) {
      setTrendState({ key: null, points: [], range: null, ready: true });
      return;
    }
    const cacheKey = `${selectedHandle}:${selectedCommunityWindow}`;
    const cached = trendCache.current.get(cacheKey);
    if (cached) {
      setTrendState({ key: cacheKey, points: cached.points, range: cached.range, ready: true });
      if (Date.now() - cached.loadedAt < 30_000) return;
    } else {
      setTrendState({ key: cacheKey, points: [], range: null, ready: false });
    }
    let cancelled = false;
    api.getPublicTokenTrends(selectedHandle, { range: selectedCommunityWindow }).catch((error) => {
      if (error instanceof ApiError && (error.status === 404 || error.code === 'PUBLIC_PROFILE_NOT_FOUND')) {
        return { points: [] } as TokenTrendsResponse;
      }
      throw error;
    }).then((trends) => {
      if (cancelled) return;
      const points = publicTrendPoints(trends);
      const range = trends.visible === false ? null : trends.range ?? null;
      trendCache.current.set(cacheKey, { points, range, loadedAt: Date.now() });
      setTrendState({ key: cacheKey, points, range, ready: true });
    }).catch(() => {
      if (cancelled) return;
      if (!cached) {
        setTrendState({ key: cacheKey, points: [], range: null, ready: true });
      }
    });
    return () => { cancelled = true; };
  }, [selectedHandle, selectedCommunityWindow, refreshTick]);

  const rankedEntries = entries.filter(entry => hasRankedTokens(entry.metricValue));
  const podium = rankedEntries.length >= 3 ? [rankedEntries[1], rankedEntries[0], rankedEntries[2]] : rankedEntries;
  const rankValue = personalTokenRank(summary);
  const todayTokens = summary?.ranking?.entry?.metricValue ?? summary?.metrics?.totalTokens?.value ?? null;
  const allTimeTokens = allTimeSummary?.metrics.totalTokens.supported
    ? allTimeSummary.metrics.totalTokens.value : null;
  const selectedName = selectedEntry ? publicLeaderboardName(selectedEntry) : selectedProfile?.displayName || selectedHandle;
  const currentTrendKey = selectedHandle ? `${selectedHandle}:${selectedCommunityWindow}` : null;
  const currentTrend = trendState.key === currentTrendKey ? trendState : { points: [], range: null, ready: false };
  const trendDays = [...currentTrend.points].sort((a, b) => a.date.localeCompare(b.date));
  const trendChange = selectedCommunityWindow === '7d' || selectedCommunityWindow === '30d'
    ? calendarPeriodChange(
      trendDays.map((day) => ({ date: day.date, level: 1, tokenTotal: String(day.tokenTotal || 0) })),
      selectedCommunityWindow === '7d' ? 7 : 30,
    )
    : null;
  const rangeLabel = zh ? ({ Today: '过去 24 小时', '7 Days': '近 7 天', '30 Days': '近 30 天', 'All Time': '全部时间' }[range]) : ({ Today: 'Past 24 hours', '7 Days': '7 Days', '30 Days': '30 Days', 'All Time': 'All Time' }[range]);
  const heroTokenLabel = zh ? ({ Today: '过去 24h Token', '7 Days': '近 7 天 Token', '30 Days': '近 30 天 Token', 'All Time': '累计 Token' }[range]) : ({ Today: 'Tokens · past 24h', '7 Days': 'Tokens · 7 days', '30 Days': 'Tokens · 30 days', 'All Time': 'All-time tokens' }[range]);
  const comparisonLabel = zh ? ({ Today: '较前 24h', '7 Days': '较上 7 天', '30 Days': '较上 30 天', 'All Time': undefined }[range]) : ({ Today: 'vs prior 24h', '7 Days': 'vs prior 7 days', '30 Days': 'vs prior 30 days', 'All Time': undefined }[range]);
  const connectionError = <div className={entries.length ? 'leaderboard-refresh-status' : 'leaderboard-empty'} role="alert"><p>{zh ? '连接异常' : 'Connection error'}</p><button className="btn btn-outline" type="button" onClick={() => setRefreshTick(tick => tick + 1)}>{zh ? '重试' : 'Retry'}</button></div>;
  const emptyBoard = loading && !entries.length && !loadError ? <p className="leaderboard-empty">{zh ? '加载中…' : 'Loading…'}</p> : loadError ? connectionError : !entries.length ? <p className="leaderboard-empty">{zh ? '暂无账号' : 'No accounts yet'}</p> : !podium.length ? <p className="leaderboard-empty">{zh ? '本周期暂无有效用量，暂未有人上榜。' : 'No recorded usage in this period. No one is ranked yet.'}</p> : null;
  const viewingSelf = Boolean(authenticated && user?.handle && selectedHandle && user.handle.toLowerCase() === selectedHandle.toLowerCase());
  const rhythmTitle = selectedName
    ? (zh ? `${selectedName}的创作轨迹` : `${selectedName}'s creative rhythm`)
    : (zh ? '创作轨迹' : 'Creative rhythm');
  const rhythmLink = selectedHandle
    ? viewingSelf
      ? { to: '/me', label: zh ? '个人数据' : 'My analytics' }
      : { to: `/u/${encodeURIComponent(selectedHandle)}`, label: zh ? '公开资料' : 'Public profile' }
    : null;

  return (
    <div className="token-home sky-home">
      <section className="sky-hero" aria-labelledby="sky-hero-title">
        <div className="sky-hero-copy">
          <p className="eyebrow">DEVELOPER TOKEN OBSERVATORY</p>
          <h1 id="sky-hero-title">Let Token <span>Dance</span></h1>
          <p>{zh ? '看见你与 AI 一起创造的每一天。' : 'Every day you create with AI, made visible.'}</p>
        </div>
        <div className="sky-orb">
          <span>{heroTokenLabel}</span>
          <strong>{community?.tokens != null ? formatTokens(community.tokens) : '—'}</strong>
          <i aria-hidden="true" />
          <DeltaChip value={community?.deltas?.tokens} suffix={comparisonLabel} />
          <small>SMALL TOKENS<br />BIG CHANGES</small>
        </div>
        <div className="sky-metrics">
          <HeroMiniCard icon={<UsersRound />} label={zh ? '活跃开发者' : 'Active devs'} value={community?.developers != null ? formatTokens(String(community.developers)).replace('.0K', 'K') : '—'} delta={community?.deltas?.developers} />
          <HeroMiniCard icon={<Code2 />} label={zh ? '生成代码行' : 'Code lines'} value={formatTokens(community?.codeLines)} delta={community?.deltas?.codeLines} />
          <HeroMiniCard icon={<Zap />} label={zh ? '模型请求' : 'Model requests'} value={formatTokens(community?.interactions)} delta={community?.deltas?.interactions} unit={zh ? '次' : 'requests'} help={zh ? '统计当前周期内已同步的模型请求次数，与用户消息数、工具调用次数不同。仅覆盖已采集的来源。' : 'Synced model requests in this period, distinct from user messages and tool calls. Covers collected sources only.'} />
          <HeroMiniCard icon={<Wallet />} label={zh ? '预估费用' : 'Est. cost'} value={formatCommunityCost(community ?? {})} delta={community?.deltas?.costAmount} />
        </div>
      </section>
      <div className="sky-home-content">
        <div className="sky-feature-grid">
          <section className="panel sky-podium-panel" id="leaderboard">
            <div className="panel-header">
              <div>
                <h2><Crown size={25} />{zh ? '平台排行榜' : 'TokenBoard'}</h2>
                <p>{zh ? '每一份创造，都值得被看见。' : 'A little recognition for every creator.'}</p>
              </div>
              <Link className="sky-text-link" to={`/leaderboard/list?window=${windowByRange[range]}`}>{zh ? '完整榜单' : 'Full board'}<ArrowRight size={15} /></Link>
            </div>
            <div className="range-tabs" role="tablist" aria-label={zh ? '首页统计周期' : 'Homepage statistics period'}>
              {ranges.map((item) => (
                <button key={item} type="button" role="tab" aria-selected={range === item} className={range === item ? 'active' : ''} onClick={() => setRange(item)}>
                  {zh ? ({ Today: '过去 24 小时', '7 Days': '近 7 天', '30 Days': '近 30 天', 'All Time': '全部时间' }[item]) : ({ Today: 'Past 24 hours', '7 Days': '7 Days', '30 Days': '30 Days', 'All Time': 'All Time' }[item])}
                </button>
              ))}
            </div>
            {emptyBoard}
            {podium.length > 0 && (
              <div className="podium-grid">
                {podium.map((entry) => (
                  <PodiumCard key={entry.rankNo} entry={entry} selected={entry.handle === selectedHandle} onSelect={(next) => setSelectedHandle(next.handle)} zh={zh} />
                ))}
              </div>
            )}
            <div className="sky-panel-foot">
              <UsersRound size={14} />
              {zh ? `${boardSummary.totalParticipants ?? entries.length} 位开发者正在创造` : `${boardSummary.totalParticipants ?? entries.length} developers creating`}
              <span>{rangeLabel} · UTC+8</span>
            </div>
          </section>
          <section className="panel sky-rhythm">
            <div className="panel-header">
              <div>
                <h2><BarChart3 size={24} />{rhythmTitle}</h2>
                <p>{zh ? '让每一次与 AI 的协作，留下足迹。' : 'Small steps. A story worth seeing.'}</p>
              </div>
            </div>
            {selectedHandle ? (
              <>
                <div className="sky-rhythm-total">
                  <strong>
                    {trendDays.length || currentTrend.range ? formatTokens(String(trendDays.reduce((total, day) => total + Number(day.tokenTotal || 0), 0))) : '—'}
                    <small>Token</small>
                    <DeltaChip value={trendChange} />
                  </strong>
                  {!viewingSelf && rhythmLink ? (
                    <Link to={rhythmLink.to} className="sky-text-link">{rhythmLink.label}<ArrowUpRight size={16} /></Link>
                  ) : null}
                </div>
                {!currentTrend.ready ? (
                  <p className="side-card-empty">{zh ? '正在加载公开轨迹…' : 'Loading the public rhythm…'}</p>
                ) : !currentTrend.points.length && !currentTrend.range ? (
                  <p className="side-card-empty">{zh ? '这个范围里，还没有创作记录。' : 'No activity in this range yet.'}</p>
                ) : (
                  <TokenTrendChart trends={trendDays} range={currentTrend.range ?? undefined} height={205} />
                )}
                <div className="sky-panel-foot sky-chart-footer">
                  <span><i />{zh ? '公开 Token' : 'Public tokens'}</span>
                  {selectedStreak != null && <span><Flame size={14} />{zh ? `连续活跃 ${selectedStreak} 天` : `${selectedStreak}-day streak`}</span>}
                </div>
              </>
            ) : (
              <div className="sky-guest">
                <BarChart3 size={34} />
                <h3>{zh ? '点领奖台上的开发者，查看公开轨迹' : 'Tap a builder to see a shared rhythm'}</h3>
                <p>{zh ? '默认展示第一名的公开用量，所有人都可以看。' : 'The podium starts on first place. Anyone can look.'}</p>
              </div>
            )}
          </section>
        </div>
        <div className="sky-detail-grid">
          <section className="panel sky-ranking">
            <HomeLeaderboard key={range} entries={entries} ownEntry={authenticated ? boardSummary.ownEntry : null} window={windowByRange[range]} />
          </section>
          <aside className="sky-side">
            <section className="panel sky-personal">
              <div className="panel-header">
                <h2>{zh ? '我的过去 24h' : 'My past 24h'}</h2>
                {authenticated && <button className="sky-text-link" type="button" onClick={() => personalAnalytics.show()} aria-label={zh ? '打开个人数据' : 'Open analytics'}>{zh ? '查看我的数据' : 'View my data'}<ArrowUpRight size={16} /></button>}
              </div>
              {authenticated ? (
                <>
                  <div className="sky-personal-id">
                    <UserAvatar url={user?.avatarUrl} name={user?.displayName || user?.handle || ''} alt="" />
                    <div>
                      <strong>{user?.displayName || user?.handle}</strong>
                      <p>{zh ? '保持好奇，继续创造。' : 'Stay curious. Keep building.'}</p>
                    </div>
                  </div>
                  <div className="sky-personal-stats">
                    <div className="stat-block">
                      <span>{zh ? '过去 24h 排名' : 'Past 24h rank'}</span>
                      <div className="stat-line">
                        <strong>{rankValue ?? (summary ? (zh ? '暂未上榜' : 'Not ranked yet') : '—')}</strong>
                        {rankValue != null && <TrendBadge value={summary?.ranking?.delta} />}
                        {rankValue != null && summary?.ranking?.percentile != null && <em>{zh ? `前 ${formatPercentile(summary.ranking.percentile)}%` : `Top ${formatPercentile(summary.ranking.percentile)}%`}</em>}
                      </div>
                    </div>
                    <div className="stat-block">
                      <span>{zh ? '过去 24h Token' : 'Past 24h tokens'}</span>
                      <div className="stat-line"><strong>{formatTokens(todayTokens)}</strong></div>
                    </div>
                    <div className="stat-block">
                      <span>{zh ? '累计 Token' : 'All time Tokens'}</span>
                      <div className="stat-line"><strong>{formatTokens(allTimeTokens)}</strong></div>
                    </div>
                  </div>
                  <ActivityCalendar days={calendarDays} streakDays={streak} />
                </>
              ) : (
                <div className="sky-login-prompt"><p className="side-card-empty">{zh ? '登录后查看你的排名与统计。' : 'Sign in to see your rank and stats.'}</p><Link className="btn btn-dark" to="/login?return_to=%2Fme">{zh ? '登录查看' : 'Sign in to view'}<ArrowRight size={16} /></Link></div>
              )}
            </section>
          </aside>
          <div className="sky-share-grid">
            <CommunityShareBoard
              title={zh ? '常用 harness' : 'Top harnesses'}
              helpTo="/docs/sources"
              helpLabel={zh ? '支持的工具' : 'Supported tools'}
              empty={zh ? '暂无社区 harness 用量数据。' : 'No harness usage recorded yet.'}
              caption={zh ? `社区${rangeLabel} Token 占比 · 按 harness` : `Community token share · ${rangeLabel} · by harness`}
              items={(community?.harnesses ?? []).map((harness) => {
                const brand = resolveHarnessBrand(harness.agentId, harness.label);
                return {
                  id: harness.agentId,
                  label: harness.label,
                  sharePct: harness.sharePct,
                  color: brand.color,
                  mark: <HarnessMark agentId={harness.agentId} label={harness.label} />,
                };
              })}
            />
            <CommunityShareBoard
              title={zh ? '社区模型排行榜' : 'Community models'}
              helpTo="/docs/sources"
              helpLabel={zh ? '模型用量说明' : 'Model usage'}
              empty={zh ? '暂无社区模型用量数据。' : 'No model usage recorded yet.'}
              caption={zh ? `社区${rangeLabel} Token 占比 · 按模型` : `Community token share · ${rangeLabel} · by model`}
              items={(community?.models ?? []).map((model) => ({
                id: model.modelId,
                label: model.label,
                sharePct: model.sharePct,
              }))}
            />
            <CommunityShareBoard
              layout="wide"
              title={zh ? '社区 Skill 排行榜' : 'Community skills'}
              helpTo="/docs/sources"
              helpLabel={zh ? 'Skill 用量说明' : 'Skill usage'}
              empty={zh ? '暂无社区 Skill 用量数据。' : 'No skill usage recorded yet.'}
              caption={zh ? `社区${rangeLabel} 调用占比 · 按 Skill` : `Community call share · ${rangeLabel} · by skill`}
              items={(community?.skills ?? []).map((skill) => ({
                id: skill.skillId,
                label: skill.label,
                sharePct: skill.sharePct,
              }))}
            />
          </div>
        </div>
        <section className="sky-download-strip">
          <Monitor size={35} />
          <div>
            <h2>{zh ? '让创造，常驻桌面。' : 'Keep your creativity close.'}</h2>
            <p>{zh ? '连接你的 AI 工具，自动记录每一天的用量。' : 'Connect your AI tools. Make every day count.'}</p>
          </div>
          <Link className="btn btn-primary" to="/download"><Download size={17} />{zh ? '下载 TokenDance' : 'Get TokenDance'}</Link>
        </section>
        <footer className="sky-home-footer">
          <Link to="/leaderboard"><img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="" />TokenDance</Link>
          <span>{zh ? '每一个 Token，都是更好明天的开始。' : 'Small tokens. A brighter tomorrow.'}</span>
          <Link to="/docs/privacy"><ShieldCheck size={14} />{zh ? '数据与隐私' : 'Data & privacy'}</Link>
        </footer>
      </div>
    </div>
  );
};
