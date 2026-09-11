import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import { publicLeaderboardName } from '@/components/analytics/leaderboardName';
import { RankChange } from '@/components/analytics/RankChange';
import { UserAvatar } from '@/components/common/UserAvatar';
import React, { useCallback, useRef, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  BarChart3, ChevronLeft, ChevronRight, CircleHelp,
  Flame, TrendingDown, TrendingUp,
} from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { useVisibleRefresh } from '@/hooks/useVisibleRefresh';
import { api } from '@/api/client';
import type { LeaderboardEntry, LeaderboardResponse, PersonalSummary, CalendarDay, CommunityStatsResponse } from '@/types/api';

type Range = 'Today' | '7 Days' | '30 Days' | 'All Time';

const ranges: Range[] = ['Today', '7 Days', '30 Days', 'All Time'];
const windowByRange: Record<Range, string> = { Today: 'today', '7 Days': '7d', '30 Days': '30d', 'All Time': 'all' };

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

function beijingWeekdayMonday0(date: string): number {
  const [year, month, day] = date.split('-').map(Number);
  return (new Date(Date.UTC(year, month - 1, day)).getUTCDay() + 6) % 7;
}

function formatPercentile(value: number | string): string {
  const n = Number(value);
  if (!Number.isFinite(n)) return '—';
  return (Math.ceil(n * 100) / 100).toFixed(2);
}

function DeltaChip({ value, suffix }: { value?: number | null; suffix?: string }) {
  if (value == null) return null;
  const positive = value >= 0;
  return (
    <span className={`hero-delta ${positive ? 'up' : 'down'}`}>
      {positive ? '↑' : '↓'} {positive ? '+' : '−'}{Math.abs(value).toFixed(1)}%{suffix ? ` ${suffix}` : ''}
    </span>
  );
}

function HeroMiniCard({ label, value, delta }: { label: string; value: string; delta?: number | null }) {
  return (
    <div className="hero-mini-card">
      <span className="hero-mini-label">{label}</span>
      <strong className="hero-mini-value">{value ?? '—'}</strong>
      <DeltaChip value={delta} />
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
  return <UserAvatar url={entry.avatarUrl} name={name} className={`leader-avatar ${className}`} fallbackClassName={`leader-avatar ${className} avatar-fallback`} alt={`${name} profile`} />;
}

function PodiumCard({ entry }: { entry: LeaderboardEntry }) {
  const winner = entry.rankNo === 1;
  return <article className={`podium-card ${winner ? 'winner' : ''}`}>
    <div className={`rank-medal rank-${entry.rankNo}`}>{entry.rankNo}</div>
    <div className="podium-avatar-wrap"><PersonAvatar entry={entry} className="podium-avatar" />{winner && <span className="crown">♛</span>}</div>
    <div className="podium-id">
      <strong>{publicLeaderboardName(entry)}</strong>
    </div>
    <div className="podium-score-row"><span>{formatTokens(entry.metricValue)}</span><small><RankChange value={entry.rankDelta} isNew={entry.isNew} /></small></div>
  </article>;
}

export const LeaderboardPage: React.FC = () => {
  const { locale } = useLocale();
  const navigate = useNavigate();
  const { user, authenticated } = useAuth();
  const zh = locale === 'zh-CN';
  const accountKey = user?.userId ?? user?.handle ?? '';
  const [range, setRange] = useState<Range>('Today');
  const requestId = useRef(0);
  const hasSnapshotRef = useRef(false);
  const [boardSummary, setBoardSummary] = useState<Partial<LeaderboardResponse>>({});
  const [sharing, setSharing] = useState<{ publicProfileEnabled: boolean; showTokenTotal: boolean } | null>(null);

  const [entries, setEntries] = useState<LeaderboardEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);

  const [summary, setSummary] = useState<PersonalSummary | null>(null);
  const [allTimeSummary, setAllTimeSummary] = useState<PersonalSummary | null>(null);
  const [calendarDays, setCalendarDays] = useState<CalendarDay[]>([]);
  const [streak, setStreak] = useState(0);
  const [refreshTick, setRefreshTick] = useState(0);
  const [community, setCommunity] = useState<CommunityStatsResponse | null>(null);

  const fetchLeaderboard = useCallback(async () => {
    const id = ++requestId.current;
    if (!hasSnapshotRef.current) setLoading(true);
    try {
      const res = await api.getLeaderboardView(authenticated, { window: windowByRange[range], limit: 10 });
      if (id !== requestId.current) return;
      hasSnapshotRef.current = true;
      setBoardSummary(res);
      setEntries(res.entries || []);
      setLoadError(false);
    } catch {
      if (id !== requestId.current) return;
      setLoadError(true);
    } finally {
      if (id === requestId.current) setLoading(false);
    }
  }, [range, authenticated]);

  useEffect(() => {
    void fetchLeaderboard();
  }, [fetchLeaderboard, accountKey, refreshTick]);

  const loadCommunity = useCallback(() => {
    let cancelled = false;
    api.getCommunityStats()
      .then((res) => { if (!cancelled) setCommunity(res); })
      .catch(() => { if (!cancelled) setCommunity(null); });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => loadCommunity(), [loadCommunity, refreshTick]);

  const loadPersonal = useCallback(() => {
    if (!authenticated) {
      setSummary(null);
      setAllTimeSummary(null);
      setSharing(null);
      setCalendarDays([]);
      setStreak(0);
      return () => {};
    }
    let cancelled = false;
    api.getPrivacy().then(result => { if (!cancelled) setSharing(result); }).catch(() => {});
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

  const podium = entries.length >= 3 ? [entries[1], entries[0], entries[2]] : entries.slice(0, entries.length);
  const rankValue = summary?.ranking?.rank ?? null;
  const todayTokens = summary?.ranking?.entry?.metricValue ?? summary?.metrics?.totalTokens?.value ?? null;
  const allTimeTokens = allTimeSummary?.metrics.totalTokens.supported
    ? allTimeSummary.metrics.totalTokens.value : null;
  const monthLabel = calendarDays.length
    ? new Date(`${calendarDays[calendarDays.length - 1].date}T00:00:00+08:00`).toLocaleDateString(zh ? 'zh-CN' : 'en-US', { month: 'short', year: 'numeric', timeZone: 'Asia/Shanghai' })
    : '';
  const heatmapLead = calendarDays.length ? beijingWeekdayMonday0(calendarDays[0].date) : 0;

  const connectionError = (
    <div className={entries.length ? 'leaderboard-refresh-status' : 'leaderboard-empty'} role="alert">
      <p>{zh ? '连接异常' : 'Connection error'}</p>
      <button className="btn btn-outline" type="button" onClick={() => setRefreshTick((tick) => tick + 1)}>{zh ? '重试' : 'Retry'}</button>
    </div>
  );

  const renderLeaderboardBody = () => {
    if (loading && entries.length === 0 && !loadError) {
      return <p className="leaderboard-empty">{zh ? '加载中…' : 'Loading…'}</p>;
    }
    if (loadError && entries.length === 0) return connectionError;
    if (entries.length === 0) {
      return <p className="leaderboard-empty">{zh ? '暂无账号' : 'No accounts yet'}</p>;
    }
    return <>
      {loadError && connectionError}
      {podium.length > 0 && <div className="podium-grid">{podium.map((entry) => <PodiumCard key={entry.rankNo} entry={entry} />)}</div>}
      <div className="leaderboard-list-heading"><h2>{zh ? '排行榜' : 'Rankings'}</h2><Link to={`/leaderboard/list?window=${windowByRange[range]}`}>{zh ? '查看完整列表' : 'View full list'} →</Link></div>
      <LeaderboardTable entries={entries} ownEntry={authenticated ? boardSummary.ownEntry : null} />
    </>;
  };

  return <div className="token-home"><div className="home-dashboard">
    <section className="main-column" aria-label={zh ? 'Token 排行榜' : 'Token leaderboard'}>
      <section className="hero-block">
        <div className="hero-copy">
          <div className="hero-title-row">
            <h1>Let Token Dance</h1>
            <span className="hero-live"><i aria-hidden="true" />LIVE · {zh ? '实时更新' : 'Live'}</span>
          </div>
          <p>{zh ? '今天，整个社区正在持续燃烧 Token' : 'The whole community is burning tokens today.'}</p>
          <div className="hero-today">
            <div className="hero-today-main">
              <span className="hero-today-label">{zh ? '今日 Token' : 'Today’s tokens'}</span>
              <strong className="hero-today-value">{community?.tokens != null ? formatTokens(community.tokens) : '—'}</strong>
              <DeltaChip value={community?.deltas?.tokens} suffix={zh ? 'vs 昨日' : 'vs yesterday'} />
            </div>
            <div className="hero-mini-grid">
              <HeroMiniCard label={zh ? '活跃开发者' : 'Active devs'} value={community?.developers != null ? community.developers.toLocaleString('en-US') : '—'} delta={community?.deltas?.developers} />
              <HeroMiniCard label={zh ? '生成代码行' : 'Code lines'} value={formatTokens(community?.codeLines)} delta={community?.deltas?.codeLines} />
              <HeroMiniCard label={zh ? 'AI 交互' : 'AI turns'} value={formatTokens(community?.interactions)} delta={community?.deltas?.interactions} />
              <HeroMiniCard label={zh ? '预估费用' : 'Est. cost'} value={community?.costAmount != null ? `$${community.costAmount.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}` : '—'} delta={community?.deltas?.costAmount} />
            </div>
          </div>
        </div>
        <div className="hero-landscape" aria-hidden="true"><span className="line-dot dot-a" /><span className="line-dot dot-b" /><span className="line-dot dot-c" /><div className="line-segment segment-a" /><div className="line-segment segment-b" /><div className="line-segment segment-c" /><div className="peak peak-a" /><div className="peak peak-b" /><div className="peak peak-c" /><div className="bar bar-a" /><div className="bar bar-b" /><div className="bar bar-c" /></div>
      </section>

      {authenticated && sharing && !sharing.publicProfileEnabled && <div className="panel" role="status" style={{ marginBottom: 16 }}>
        <p style={{ margin: '0 0 8px' }}>{zh
          ? '公开开关只控制详细资料页。排行榜仍显示头像、昵称、Token 和排名。'
          : 'The public switch only controls your detailed profile. Your avatar, nickname, tokens, and rank stay on the leaderboard.'}</p>
        <button className="btn btn-outline" onClick={() => navigate('/me')}>{zh ? '管理公开设置' : 'Manage sharing'}</button>
      </div>}
      <section className="leaderboard-panel" id="leaderboard">
        <div className="range-tabs" role="tablist" aria-label={zh ? '排行榜周期' : 'Leaderboard period'}>
          {ranges.map((item) => <button key={item} type="button" role="tab" aria-selected={range === item} className={range === item ? 'active' : ''} onClick={() => setRange(item)}>{zh ? ({ Today: '今天', '7 Days': '近 7 天', '30 Days': '近 30 天', 'All Time': '全部时间' }[item]) : item}{item === 'Today' && <span className="live-dot" />}</button>)}
        </div>
        {renderLeaderboardBody()}
      </section>
    </section>

    <aside className="side-column">
      <section className="side-card stats-card"><div className="card-heading"><h2>{zh ? '你的数据' : 'Your Stats'}</h2><button type="button" onClick={() => navigate('/me')} aria-label={zh ? '打开个人数据' : 'Open analytics'}><BarChart3 /></button></div>
        {authenticated ? <>
          <div className="stat-block"><span>{zh ? '今日排名 · 北京时间' : 'Today’s rank · Beijing'}</span><div className="stat-line"><strong>{rankValue ?? '—'}</strong><TrendBadge value={summary?.ranking?.delta ?? null} />{summary?.ranking?.percentile != null && <em>{zh ? `前 ${formatPercentile(summary.ranking.percentile)}%` : `Top ${formatPercentile(summary.ranking.percentile)}%`}</em>}</div></div>
          <div className="stat-block"><span>{zh ? '今日 Token · 北京时间' : 'Today’s Tokens · Beijing'}</span><div className="stat-line"><strong>{formatTokens(todayTokens)}</strong></div></div>
          <div className="stat-block"><span>{zh ? '累计 Token · All time' : 'All time Tokens'}</span><div className="stat-line"><strong>{allTimeTokens === '0' ? '0' : formatTokens(allTimeTokens)}</strong></div></div>
          <div className="streak-line"><span>{zh ? '连续活跃' : 'Streak'}</span><div><Flame /><strong>{streak || 0}</strong>{zh ? '天' : 'days'}</div></div>
        </> : <p className="side-card-empty">{zh ? '登录后查看你的排名与统计。' : 'Sign in to see your rank and stats.'}</p>}
      </section>
      <section className="side-card activity-card"><div className="card-heading"><h2>{zh ? 'Token 活跃度' : 'Token Activity'}</h2><CircleHelp /></div>
        {authenticated ? <>
          <div className="month-row"><span>{monthLabel || (zh ? '暂无数据' : 'No data')}</span><div><button type="button" aria-label="Previous month"><ChevronLeft /></button><button type="button" aria-label="Next month"><ChevronRight /></button></div></div>
          <div className="week-labels">{['M', 'T', 'W', 'T', 'F', 'S', 'S'].map((day, index) => <span key={`${day}-${index}`}>{day}</span>)}</div>
          <div className="home-heatmap">{Array.from({ length: heatmapLead }, (_, index) => <span key={`pad-${index}`} data-level={0} />)}{calendarDays.map((day) => <span key={day.date} data-level={day.level} title={day.date} />)}</div>
          <div className="heat-legend"><span>{zh ? '少' : 'Less'}</span>{[0, 1, 2, 3, 4, 5].map((level) => <i key={level} data-level={level} />)}<span>{zh ? '多' : 'More'}</span></div>
        </> : <p className="side-card-empty">{zh ? '登录后查看你的活跃度热力图。' : 'Sign in to see your activity heatmap.'}</p>}
      </section>
      <section className="side-card tools-card"><div className="card-heading"><h2>{zh ? '常用 harness' : 'Top harnesses'}</h2><button type="button" className="view-all">{zh ? '全部' : 'View all'}</button></div>
        {(community?.harnesses?.length ?? 0) > 0 ? <div className="tool-list">{community?.harnesses?.map((harness, index) => <div className="tool-row" key={harness.agentId}><span className="tool-mark" data-accent={index === 0 || undefined}>{harness.label.slice(0, 1).toUpperCase()}</span><strong>{harness.label}</strong><div className="tool-track"><i style={{ width: `${Math.round(harness.sharePct ?? 0)}%` }} data-accent={index === 0 || undefined} /></div><span>{Math.round(harness.sharePct ?? 0)}%</span></div>)}</div>
          : <p className="side-card-empty">{zh ? '暂无社区 harness 用量数据。' : 'No harness usage recorded yet.'}</p>}
        <p>{zh ? '社区今日 Token 占比 · 按 harness' : 'Community share of today’s tokens · by harness'}</p></section>
    </aside>
  </div></div>;
};
