import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import { publicLeaderboardName } from '@/components/analytics/leaderboardName';
import { RankChange } from '@/components/analytics/RankChange';
import { UserAvatar } from '@/components/common/UserAvatar';
import React, { useCallback, useRef, useEffect, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import {
  ArrowRight, ArrowUpRight, BarChart3, Code2, Crown, Download, Monitor, UsersRound, Wallet, Zap,
  Flame, TrendingDown, TrendingUp,
} from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { useVisibleRefresh } from '@/hooks/useVisibleRefresh';
import { api } from '@/api/client';
import type { LeaderboardEntry, LeaderboardResponse, PersonalSummary, CalendarDay, CommunityStatsResponse } from '@/types/api';
import { formatCommunityCost } from '@/utils/cost';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';

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

function formatPercentile(value: number | string): string {
  const n = Number(value);
  if (!Number.isFinite(n)) return '—';
  return (Math.ceil(n * 100) / 100).toFixed(2);
}

function DeltaChip({ value, suffix }: { value?: number | null; suffix?: string }) {
  if (value == null) return null;
  const positive = value >= 0;
  return (
    <span className={`hero-delta ${value === 0 ? 'flat' : positive ? 'up' : 'down'}`}>
      {positive ? '↑' : '↓'} {positive ? '+' : '−'}{Math.abs(value).toFixed(1)}%{suffix ? ` ${suffix}` : ''}
    </span>
  );
}

function HeroMiniCard({ label, value, delta, icon }: { label: string; value: string; delta?: number | null; icon: React.ReactNode }) {
  return (
    <div className="hero-mini-card">
      <span className="sky-metric-icon" aria-hidden="true">{icon}</span><div><span className="hero-mini-label">{label}</span><div className="sky-metric-value"><strong className="hero-mini-value">{value ?? '—'}</strong><DeltaChip value={delta} /></div></div>
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
    <div className="podium-avatar-wrap"><PersonAvatar entry={entry} className="podium-avatar" />{winner && <Crown className="crown" size={28} aria-hidden="true" />}</div>
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
  const [trendRange, setTrendRange] = useState('30d');
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
  const trendDays = calendarDays.slice(trendRange === '7d' ? -7 : -30);
  const connectionError = <div className={entries.length ? 'leaderboard-refresh-status' : 'leaderboard-empty'} role="alert"><p>{zh ? '连接异常' : 'Connection error'}</p><button className="btn btn-outline" type="button" onClick={() => setRefreshTick(tick => tick + 1)}>{zh ? '重试' : 'Retry'}</button></div>;
  const emptyBoard = loading && !entries.length && !loadError ? <p className="leaderboard-empty">{zh ? '加载中…' : 'Loading…'}</p> : loadError ? connectionError : !entries.length ? <p className="leaderboard-empty">{zh ? '暂无账号' : 'No accounts yet'}</p> : null;

  return <div className="token-home sky-home">
    <section className="sky-hero" aria-labelledby="sky-hero-title">
      <div className="sky-hero-copy"><p className="eyebrow">DEVELOPER TOKEN OBSERVATORY</p><h1 id="sky-hero-title">Let Token <span>Dance</span></h1><p>{zh ? '看见你与 AI 一起创造的每一天。' : 'Every day you create with AI, made visible.'}</p><Link className="sky-text-link" to={authenticated ? '/me' : '/download'}>{zh ? (authenticated ? '查看我的创作足迹' : '开始记录你的创造') : (authenticated ? 'Explore my activity' : 'Start your journey')}<ArrowUpRight size={17} /></Link></div>
      <div className="sky-orb"><span>{zh ? '今日 Token' : 'Today’s tokens'}</span><strong>{community?.tokens != null ? formatTokens(community.tokens) : '—'}</strong><i aria-hidden="true" /><DeltaChip value={community?.deltas?.tokens} suffix={zh ? 'vs 昨日' : 'vs yesterday'} /><small>SMALL TOKENS<br />BIG CHANGES</small></div>
      <span className="sky-hero-note" aria-hidden="true">More builders.<br />A brighter tomorrow.</span>
      <div className="sky-metrics"><HeroMiniCard icon={<UsersRound />} label={zh ? '活跃开发者' : 'Active devs'} value={community?.developers != null ? community.developers.toLocaleString('en-US') : '—'} delta={community?.deltas?.developers} /><HeroMiniCard icon={<Code2 />} label={zh ? '生成代码行' : 'Code lines'} value={formatTokens(community?.codeLines)} delta={community?.deltas?.codeLines} /><HeroMiniCard icon={<Zap />} label={zh ? 'AI 交互' : 'AI turns'} value={formatTokens(community?.interactions)} delta={community?.deltas?.interactions} /><HeroMiniCard icon={<Wallet />} label={zh ? '预估费用' : 'Est. cost'} value={formatCommunityCost(community ?? {})} delta={community?.deltas?.costAmount} /></div>
    </section>
    <div className="sky-home-content">
      <div className="sky-feature-grid">
        <section className="panel sky-podium-panel" id="leaderboard"><div className="panel-header"><div><h2><Crown size={25} />{zh ? '平台排行榜' : 'TokenBoard'}</h2><p>{zh ? '每一份创造，都值得被看见。' : 'A little recognition for every creator.'}</p></div><Link className="sky-text-link" to={`/leaderboard/list?window=${windowByRange[range]}`}>{zh ? '完整榜单' : 'Full board'}<ArrowRight size={15} /></Link></div><div className="range-tabs" role="tablist" aria-label={zh ? '排行榜周期' : 'Leaderboard period'}>{ranges.map(item => <button key={item} type="button" role="tab" aria-selected={range === item} className={range === item ? 'active' : ''} onClick={() => setRange(item)}>{zh ? ({ Today: '今天', '7 Days': '近 7 天', '30 Days': '近 30 天', 'All Time': '全部时间' }[item]) : item}</button>)}</div>{emptyBoard}{podium.length > 0 && <div className="podium-grid">{podium.map(entry => <PodiumCard key={entry.rankNo} entry={entry} />)}</div>}<div className="sky-panel-foot"><UsersRound size={14} />{zh ? '每一个 Token，都有创造的意义。' : 'Every token is a little possibility.'}</div></section>
        <section className="panel sky-rhythm"><div className="panel-header"><div><h2><BarChart3 size={24} />{zh ? '你的创作轨迹' : 'Your creative rhythm'}</h2><p>{zh ? '让每一次与 AI 的协作，留下足迹。' : 'Small steps. A story worth seeing.'}</p></div><select className="form-input" value={trendRange} onChange={e => setTrendRange(e.target.value)} aria-label={zh ? '用量趋势周期' : 'Trend period'}><option value="7d">{zh ? '近 7 天' : '7 days'}</option><option value="30d">{zh ? '近 30 天' : '30 days'}</option></select></div>{authenticated ? <><div className="sky-rhythm-total"><strong>{trendDays.length ? formatTokens(String(trendDays.reduce((total, day) => total + Number(day.tokenTotal || 0), 0))) : '—'}<small>Token</small></strong><Link to="/me" className="sky-text-link">{zh ? '个人数据' : 'My analytics'}<ArrowUpRight size={16} /></Link></div><TokenTrendChart trends={trendDays} /><div className="sky-panel-foot"><Flame size={14} />{zh ? `连续活跃 ${streak} 天` : `${streak}-day streak`}</div></> : <div className="sky-guest"><BarChart3 size={34} /><h3>{zh ? '你的下一次创造，从这里开始' : 'Your next creation starts here'}</h3><p>{zh ? '登录后查看用量趋势与活跃记录。' : 'Sign in to see your usage and activity.'}</p><Link className="btn btn-primary" to="/login?return_to=%2Fme">{zh ? '登录，留下你的足迹' : 'Sign in to see your story'}<ArrowRight size={16} /></Link></div>}</section>
      </div>
      <div className="sky-detail-grid"><section className="panel sky-ranking"><div className="panel-header"><div><h2>{zh ? '正在创造的他们' : 'Meet the builders'}</h2><p>{zh ? '从一个灵感，到下一个可能。' : 'From a spark to something real.'}</p></div><Link className="sky-text-link" to={`/leaderboard/list?window=${windowByRange[range]}`}>{zh ? '查看完整列表' : 'View full list'}<ArrowRight size={15} /></Link></div>{entries.length ? <LeaderboardTable entries={entries} ownEntry={authenticated ? boardSummary.ownEntry : null} /> : <p className="leaderboard-empty">{zh ? '暂无账号' : 'No accounts yet'}</p>}{authenticated && sharing && !sharing.publicProfileEnabled && <div className="sky-privacy-note" role="status"><p>{zh ? '公开开关只控制详细资料页。排行榜仍显示头像、昵称、Token 和排名。' : 'The public switch only controls your detailed profile. Your avatar, nickname, tokens, and rank stay on the leaderboard.'}</p><button className="btn btn-outline" onClick={() => navigate('/me')}>{zh ? '管理公开设置' : 'Manage sharing'}</button></div>}</section>
        <aside className="sky-side"><section className="panel sky-personal"><div className="panel-header"><h2>{zh ? '我的今日' : 'My day'}</h2><button className="sky-icon-button" type="button" onClick={() => navigate('/me')} aria-label={zh ? '打开个人数据' : 'Open analytics'}><ArrowUpRight size={19} /></button></div>{authenticated ? <><div className="sky-personal-id"><UserAvatar url={user?.avatarUrl} name={user?.displayName || user?.handle || ''} alt="" /><div><strong>{user?.displayName || user?.handle}</strong><p>{zh ? '保持好奇，继续创造。' : 'Stay curious. Keep building.'}</p></div></div><div className="sky-personal-stats"><div className="stat-block"><span>{zh ? '今日排名' : 'Today’s rank'}</span><div className="stat-line"><strong>{rankValue ?? '—'}</strong><TrendBadge value={summary?.ranking?.delta} />{summary?.ranking?.percentile != null && <em>{zh ? `前 ${formatPercentile(summary.ranking.percentile)}%` : `Top ${formatPercentile(summary.ranking.percentile)}%`}</em>}</div></div><div className="stat-block"><span>{zh ? '今日 Token' : 'Today’s Tokens'}</span><div className="stat-line"><strong>{formatTokens(todayTokens)}</strong></div></div><div className="stat-block"><span>{zh ? '累计 Token' : 'All time Tokens'}</span><div className="stat-line"><strong>{formatTokens(allTimeTokens)}</strong></div></div></div><ActivityCalendar days={calendarDays} streakDays={streak} /></> : <p className="side-card-empty">{zh ? '登录后查看你的排名与统计。' : 'Sign in to see your rank and stats.'}</p>}</section>
        <section className="panel sky-harnesses"><div className="panel-header"><h2>{zh ? '常用 harness' : 'Top harnesses'}</h2><Link className="sky-text-link" to="/docs/sources"><ArrowUpRight size={18} /><span className="sr-only">{zh ? '支持的工具' : 'Supported tools'}</span></Link></div>{community?.harnesses?.length ? <div className="tool-list">{community.harnesses.map((harness, index) => <div className="tool-row" key={harness.agentId}><span className="tool-mark" data-accent={index === 0 || undefined}>{harness.label.slice(0, 1).toUpperCase()}</span><strong>{harness.label}</strong><div className="tool-track"><i style={{ width: `${Math.max(0, Math.min(100, harness.sharePct ?? 0))}%` }} /></div><span>{Math.round(harness.sharePct ?? 0)}%</span></div>)}</div> : <p className="side-card-empty">{zh ? '暂无社区 harness 用量数据。' : 'No harness usage recorded yet.'}</p>}<p className="sky-harness-caption">{zh ? '社区今日 Token 占比 · 按 harness' : 'Community share of today’s tokens · by harness'}</p></section></aside></div>
      <section className="sky-download-strip"><Monitor size={35} /><div><h2>{zh ? '让创造，常驻桌面。' : 'Keep your creativity close.'}</h2><p>{zh ? '连接你的 AI 工具，自动记录每一天的用量。' : 'Connect your AI tools. Make every day count.'}</p></div><Link className="btn btn-primary" to="/download"><Download size={17} />{zh ? '下载 TokenDance' : 'Get TokenDance'}</Link></section>
    </div>
  </div>;
};
