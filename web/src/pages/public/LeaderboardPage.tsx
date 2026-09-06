import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import { RankChange } from '@/components/analytics/RankChange';
import { avatarUrl } from '@/utils/avatar';
import React, { useCallback, useRef, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  ArrowUpRight, BarChart3, ChevronLeft, ChevronRight, CircleHelp,
  Flame, TrendingDown, TrendingUp, Users,
} from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { useVisibleRefresh } from '@/hooks/useVisibleRefresh';
import { api } from '@/api/client';
import type { LeaderboardEntry, LeaderboardResponse, PersonalSummary, CalendarDay, BreakdownItem } from '@/types/api';

type Range = 'Today' | '7 Days' | '30 Days' | 'All Time';

const ranges: Range[] = ['Today', '7 Days', '30 Days', 'All Time'];
const windowByRange: Record<Range, string> = { Today: 'today', '7 Days': '7d', '30 Days': '30d', 'All Time': 'all' };

function formatTokens(raw: string | null | undefined): string {
  const value = Number(raw ?? 0);
  if (!Number.isFinite(value) || value <= 0) return '—';
  if (value >= 1e9) return `${(value / 1e9).toFixed(1)}B`;
  if (value >= 1e6) return `${(value / 1e6).toFixed(1)}M`;
  if (value >= 1e3) return `${(value / 1e3).toFixed(1)}K`;
  return String(Math.round(value));
}

function TrendBadge({ value }: { value: number | null | undefined }) {
  if (value == null || value === 0) return null;
  const positive = value > 0;
  return <span className={`trend-badge ${positive ? 'positive' : 'negative'}`}>{positive ? <TrendingUp /> : <TrendingDown />}{Math.abs(value)}</span>;
}

function PersonAvatar({ entry, className = '' }: { entry: LeaderboardEntry; className?: string }) {
  if (entry.avatarUrl) {
    return <img className={`leader-avatar ${className}`} src={avatarUrl(entry.avatarUrl)} alt={`${entry.displayName} profile`} />;
  }
  return <span className={`leader-avatar ${className} avatar-fallback`} aria-hidden="true">{entry.displayName.slice(0, 1).toUpperCase()}</span>;
}

function PodiumCard({ entry }: { entry: LeaderboardEntry }) {
  const winner = entry.rankNo === 1;
  return <article className={`podium-card ${winner ? 'winner' : ''}`}>
    <div className={`rank-medal rank-${entry.rankNo}`}>{entry.rankNo}</div>
    <div className="podium-avatar-wrap"><PersonAvatar entry={entry} className="podium-avatar" />{winner && <span className="crown">♛</span>}</div>
    <strong>{entry.handle}</strong>
    <div className="podium-score-row"><span>{formatTokens(entry.metricValue)}</span><small><RankChange value={entry.rankDelta} isNew={entry.isNew} /></small></div>
    <p>{entry.topAgent || '—'}</p>
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
  const [agentTools, setAgentTools] = useState<BreakdownItem[]>([]);
  const [refreshTick, setRefreshTick] = useState(0);

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

  const loadPersonal = useCallback(() => {
    if (!authenticated) {
      setSummary(null);
      setAllTimeSummary(null);
      setSharing(null);
      setCalendarDays([]);
      setStreak(0);
      setAgentTools([]);
      return () => {};
    }
    let cancelled = false;
    api.getPrivacy().then(result => { if (!cancelled) setSharing(result); }).catch(() => {});
    api.getPersonalSummary('all')
      .then((result) => { if (!cancelled) setAllTimeSummary(result); })
      .catch(() => { /* Keep unavailable historical totals distinct from zero. */ });
    (async () => {
      try {
        const [summaryRes, calRes, agentsRes] = await Promise.all([
          api.getPersonalSummary('today'),
          api.getActivityCalendar('10w'),
          api.getAgentBreakdowns('today'),
        ]);
        if (cancelled) return;
        setSummary(summaryRes);
        setCalendarDays(calRes.days || []);
        setStreak(calRes.currentStreak || 0);
        setAgentTools(agentsRes.items || []);
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
  const todayTokens = summary?.metrics?.totalTokens?.value ?? null;
  const allTimeTokens = allTimeSummary?.metrics.totalTokens.supported
    ? allTimeSummary.metrics.totalTokens.value : null;
  const monthLabel = calendarDays.length
    ? new Date(calendarDays[calendarDays.length - 1].date).toLocaleDateString(zh ? 'zh-CN' : 'en-US', { month: 'short', year: 'numeric' })
    : '';

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
        <div className="hero-copy"><h1>Let Token Dance</h1><p>{zh ? '看看今天谁正在与 AI 一起创造。' : 'See who’s building the most with AI today.'}</p>
          <div className="hero-actions">
            <button className="primary-cta" type="button" onClick={() => document.querySelector('#leaderboard')?.scrollIntoView({ behavior: 'smooth' })}>{zh ? '查看排行榜' : 'View Leaderboard'} <ArrowUpRight /></button>
            <button className="secondary-cta" type="button" onClick={() => navigate(authenticated ? '/settings/devices' : '/login?return_to=%2Fsettings%2Fdevices')}>{zh ? '连接工具' : 'Connect Tools'}</button>
          </div>
        </div>
        <div className="hero-landscape" aria-hidden="true"><span className="line-dot dot-a" /><span className="line-dot dot-b" /><span className="line-dot dot-c" /><div className="line-segment segment-a" /><div className="line-segment segment-b" /><div className="line-segment segment-c" /><div className="peak peak-a" /><div className="peak peak-b" /><div className="peak peak-c" /><div className="bar bar-a" /><div className="bar bar-b" /><div className="bar bar-c" /></div>
        <div className="summary-grid">
          <div className="summary-card"><span className="summary-icon"><Flame /></span><div><strong>{boardSummary.totalTokens == null ? '—' : boardSummary.totalTokens === '0' ? '0' : formatTokens(boardSummary.totalTokens)}</strong><p>{zh ? '本期 Token' : 'Tokens · period'}</p></div></div>
          <div className="summary-card"><span className="summary-icon"><Users /></span><div><strong>{boardSummary.totalParticipants ?? boardSummary.totalEntries ?? '—'}</strong><p>{zh ? '总人数' : 'Total'}</p></div></div>
          <div className="summary-card"><span className="summary-icon"><Users /></span><div><strong>UTC</strong><p>{zh ? '统一统计时区' : 'Leaderboard timezone'}</p></div></div>
        </div>
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
          <div className="stat-block"><span>{zh ? '今日排名 · UTC' : 'Today’s rank · UTC'}</span><div className="stat-line"><strong>{rankValue ?? '—'}</strong><TrendBadge value={summary?.ranking?.delta ?? null} />{summary?.ranking?.percentile != null && <em>{zh ? `前 ${summary.ranking.percentile}%` : `Top ${summary.ranking.percentile}%`}</em>}</div></div>
          <div className="stat-block"><span>{zh ? '今日 Token' : 'Today’s Tokens'}</span><div className="stat-line"><strong>{formatTokens(todayTokens)}</strong></div></div>
          <div className="stat-block"><span>{zh ? '累计 Token · All time' : 'All time Tokens'}</span><div className="stat-line"><strong>{allTimeTokens === '0' ? '0' : formatTokens(allTimeTokens)}</strong></div></div>
          <div className="streak-line"><span>{zh ? '连续活跃' : 'Streak'}</span><div><Flame /><strong>{streak || 0}</strong>{zh ? '天' : 'days'}</div></div>
        </> : <p className="side-card-empty">{zh ? '登录后查看你的排名与统计。' : 'Sign in to see your rank and stats.'}</p>}
      </section>
      <section className="side-card activity-card"><div className="card-heading"><h2>{zh ? 'Token 活跃度' : 'Token Activity'}</h2><CircleHelp /></div>
        {authenticated ? <>
          <div className="month-row"><span>{monthLabel || (zh ? '暂无数据' : 'No data')}</span><div><button type="button" aria-label="Previous month"><ChevronLeft /></button><button type="button" aria-label="Next month"><ChevronRight /></button></div></div>
          <div className="week-labels">{['M', 'T', 'W', 'T', 'F', 'S', 'S'].map((day, index) => <span key={`${day}-${index}`}>{day}</span>)}</div>
          <div className="home-heatmap">{calendarDays.map((day) => <span key={day.date} data-level={day.level} title={day.date} />)}</div>
          <div className="heat-legend"><span>{zh ? '少' : 'Less'}</span>{[0, 1, 2, 3, 4, 5].map((level) => <i key={level} data-level={level} />)}<span>{zh ? '多' : 'More'}</span></div>
        </> : <p className="side-card-empty">{zh ? '登录后查看你的活跃度热力图。' : 'Sign in to see your activity heatmap.'}</p>}
      </section>
      <section className="side-card tools-card"><div className="card-heading"><h2>{zh ? '常用工具' : 'Top Tools'}</h2><button type="button" className="view-all">{zh ? '全部' : 'View all'}</button></div>
        {agentTools.length > 0 ? <div className="tool-list">{agentTools.map((tool) => <div className="tool-row" key={tool.key}><span className="tool-mark">{tool.label.slice(0, 1).toUpperCase()}</span><strong>{tool.label}</strong><div className="tool-track"><i style={{ width: `${Math.round(tool.percentage)}%` }} /></div><span>{Math.round(tool.percentage)}%</span></div>)}</div>
          : <p className="side-card-empty">{zh ? '登录并采集数据后展示常用工具。' : 'Sign in and collect data to see your top tools.'}</p>}
        <p>{zh ? '基于今日消耗的 Token' : 'Based on tokens burned today'}</p></section>
    </aside>
  </div></div>;
};
