import React, { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  ArrowUpRight, BarChart3, ChevronLeft, ChevronRight, CircleHelp,
  Flame, TrendingDown, TrendingUp, Users,
} from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';

type Range = 'Today' | '7 Days' | '30 Days' | 'All Time';
type Leader = { rank: number; name: string; displayName: string; avatar: number; tokens: number; growth: number; tools: string; spark: number[] };

const ranges: Range[] = ['Today', '7 Days', '30 Days', 'All Time'];
const multipliers: Record<Range, number> = { Today: 1, '7 Days': 6.4, '30 Days': 24.8, 'All Time': 138.2 };
const leaders: Leader[] = [
  { rank: 1, name: 'maxbauer', displayName: 'Max Bauer', avatar: 12, tokens: 325.7, growth: 18.7, tools: 'Claude 62% · Codex 25% · Pi 13%', spark: [12, 17, 15, 23, 22, 31, 29] },
  { rank: 2, name: 'sophiadev', displayName: 'Sophia Chen', avatar: 47, tokens: 215.4, growth: 12.4, tools: 'Claude 58% · Codex 27% · Pi 15%', spark: [11, 14, 19, 16, 23, 25, 30] },
  { rank: 3, name: 'deworap', displayName: 'Dev Patel', avatar: 11, tokens: 178.9, growth: 7.3, tools: 'Claude 55% · Codex 22% · Pi 23%', spark: [10, 13, 12, 18, 20, 25, 24] },
  { rank: 4, name: 'builderdan', displayName: 'Dan Morris', avatar: 13, tokens: 142.6, growth: 2, tools: 'Claude 60% · Codex 23% · Cursor 17%', spark: [10, 14, 12, 17, 13, 20, 19] },
  { rank: 5, name: 'kaytee', displayName: 'Katie Hall', avatar: 32, tokens: 118.3, growth: -1, tools: 'Claude 52% · Codex 30% · Pi 18%', spark: [11, 13, 16, 14, 19, 16, 18] },
  { rank: 6, name: 'alexm', displayName: 'Alex Morgan', avatar: 15, tokens: 97.6, growth: 3, tools: 'Claude 45% · Cursor 33% · Codex 22%', spark: [10, 12, 11, 15, 17, 14, 19] },
  { rank: 7, name: 'julesbuilds', displayName: 'Jules Rivera', avatar: 44, tokens: 86.1, growth: 1, tools: 'Claude 48% · Codex 26% · Pi 26%', spark: [10, 11, 13, 12, 17, 15, 18] },
  { rank: 8, name: 'notgpt', displayName: 'Noah Foster', avatar: 5, tokens: 74.2, growth: -2, tools: 'Claude 40% · Cursor 35% · Codex 25%', spark: [12, 10, 13, 15, 13, 17, 16] },
  { rank: 9, name: 'skratchdev', displayName: 'Sam Wright', avatar: 52, tokens: 66.8, growth: 0, tools: 'Claude 46% · Codex 29% · Pi 25%', spark: [10, 13, 12, 15, 17, 14, 18] },
  { rank: 10, name: 'codeforcoffee', displayName: 'Mia Evans', avatar: 45, tokens: 59.3, growth: 4, tools: 'Claude 43% · Cursor 28% · Codex 29%', spark: [9, 12, 11, 14, 13, 17, 19] },
];
const heat = [0, 0, 1, 0, 0, 1, 1, 0, 1, 1, 2, 1, 1, 2, 1, 1, 2, 3, 2, 3, 2, 2, 3, 4, 4, 3, 2, 3, 3, 4, 5, 4, 4, 3, 2, 2, 3, 5, 5, 4, 2, 1, 1, 2, 3, 2, 1, 0, 1];
const tools = [
  { name: 'Claude', value: 58, mark: '✳' }, { name: 'Codex', value: 26, mark: '◎' },
  { name: 'Cursor', value: 10, mark: '◆' }, { name: 'Pi', value: 4, mark: 'π' },
  { name: 'OpenCode', value: 2, mark: 'C' },
];

function Sparkline({ values }: { values: number[] }) {
  const points = values.map((value, index) => `${(index / (values.length - 1)) * 72},${Math.max(2, Math.min(24, 24 - ((value - 8) / 24) * 22))}`).join(' ');
  return <svg className="sparkline" viewBox="0 0 72 26" aria-hidden="true"><polyline points={points} fill="none" stroke="currentColor" strokeWidth="1.5" /></svg>;
}

function TrendBadge({ value }: { value: number }) {
  if (value === 0) return <span className="trend-neutral">−</span>;
  const positive = value > 0;
  return <span className={`trend-badge ${positive ? 'positive' : 'negative'}`}>{positive ? <TrendingUp /> : <TrendingDown />}{Math.abs(value)}</span>;
}

function PersonAvatar({ person, className = '' }: { person: Leader; className?: string }) {
  return <img className={`leader-avatar ${className}`} src={`https://i.pravatar.cc/160?img=${person.avatar}`} alt={`${person.displayName} profile`} />;
}

function PodiumCard({ leader }: { leader: Leader }) {
  const winner = leader.rank === 1;
  return <article className={`podium-card ${winner ? 'winner' : ''}`}>
    <div className={`rank-medal rank-${leader.rank}`}>{leader.rank}</div>
    <div className="podium-avatar-wrap"><PersonAvatar person={leader} className="podium-avatar" />{winner && <span className="crown">♛</span>}</div>
    <strong>{leader.name}</strong>
    <div className="podium-score-row"><span>{leader.tokens.toFixed(1)}M</span><small>▲ {leader.growth}%</small></div>
    <p>{leader.tools}</p>
  </article>;
}

function RankingRow({ leader, range }: { leader: Leader; range: Range }) {
  const value = leader.tokens * multipliers[range];
  return <div className="ranking-row">
    <span className="rank-number">{leader.rank}</span>
    <div className="rank-person"><PersonAvatar person={leader} className="row-avatar" /><span>{leader.name}</span></div>
    <strong className="rank-tokens">{value >= 1000 ? `${(value / 1000).toFixed(1)}B` : `${value.toFixed(1)}M`}</strong>
    <Sparkline values={leader.spark} /><span className="rank-tools">{leader.tools}</span><TrendBadge value={leader.growth} />
  </div>;
}

export const LeaderboardPage: React.FC = () => {
  const { locale } = useLocale();
  const navigate = useNavigate();
  const zh = locale === 'zh-CN';
  const [range, setRange] = useState<Range>('Today');
  const [connected, setConnected] = useState(false);
  const podium = [leaders[1], leaders[0], leaders[2]];
  const rows = leaders.filter((leader) => leader.rank > 3);

  return <div className="token-home"><div className="home-dashboard">
    <section className="main-column" aria-label={zh ? 'Token 排行榜' : 'Token leaderboard'}>
      <section className="hero-block">
        <div className="hero-copy"><h1>Let Token Dance</h1><p>{zh ? '看看今天谁正在与 AI 一起创造。' : 'See who’s building the most with AI today.'}</p>
          <div className="hero-actions">
            <button className="primary-cta" type="button" onClick={() => document.querySelector('#leaderboard')?.scrollIntoView({ behavior: 'smooth' })}>{zh ? '查看排行榜' : 'View Leaderboard'} <ArrowUpRight /></button>
            <button className={`secondary-cta ${connected ? 'connected' : ''}`} type="button" onClick={() => setConnected((value) => !value)}>{connected ? (zh ? '工具已连接' : 'Tools Connected') : (zh ? '连接工具' : 'Connect Tools')}</button>
          </div>
        </div>
        <div className="hero-landscape" aria-hidden="true"><span className="line-dot dot-a" /><span className="line-dot dot-b" /><span className="line-dot dot-c" /><div className="line-segment segment-a" /><div className="line-segment segment-b" /><div className="line-segment segment-c" /><div className="peak peak-a" /><div className="peak peak-b" /><div className="peak peak-c" /><div className="bar bar-a" /><div className="bar bar-b" /><div className="bar bar-c" /></div>
        <div className="summary-grid">
          <div className="summary-card"><span className="summary-icon"><Flame /></span><div><strong>12.4B</strong><p>{zh ? '已消耗 Token' : 'Tokens Burned'}</p></div></div>
          <div className="summary-card"><span className="summary-icon"><Users /></span><div><strong>48.2K</strong><p>{zh ? '开发者' : 'Developers'}</p></div></div>
          <div className="summary-card"><span className="summary-icon"><Users /></span><div><strong>1.6K</strong><p>{zh ? '团队' : 'Teams'}</p></div></div>
        </div>
      </section>

      <section className="leaderboard-panel" id="leaderboard">
        <div className="range-tabs" role="tablist" aria-label={zh ? '排行榜周期' : 'Leaderboard period'}>
          {ranges.map((item) => <button key={item} type="button" role="tab" aria-selected={range === item} className={range === item ? 'active' : ''} onClick={() => setRange(item)}>{item === 'Today' && zh ? '今天' : item}{item === 'Today' && <span className="live-dot" />}</button>)}
        </div>
        {podium.length > 0 && <div className="podium-grid">{podium.map((leader) => <PodiumCard key={leader.rank} leader={leader} />)}</div>}
        <div className="ranking-table">
          {rows.map((leader) => <RankingRow key={leader.rank} leader={leader} range={range} />)}
          <div className="ranking-row current-user"><span className="rank-number">37</span><div className="rank-person"><img className="leader-avatar row-avatar" src="https://i.pravatar.cc/120?img=12" alt="" /><span>{zh ? '你' : 'You'}<small>{zh ? '继续创造！' : 'keep shipping!'}</small></span></div><strong className="rank-tokens">{(12.7 * multipliers[range]).toFixed(range === 'Today' ? 1 : 0)}M</strong><Sparkline values={[10, 12, 11, 18, 14, 22, 19]} /><span className="rank-tools">Claude 51% · Cursor 27% · Codex 22%</span><TrendBadge value={5} /></div>
        </div>
      </section>
    </section>

    <aside className="side-column">
      <section className="side-card stats-card"><div className="card-heading"><h2>{zh ? '你的数据' : 'Your Stats'}</h2><button type="button" onClick={() => navigate('/me')} aria-label={zh ? '打开个人数据' : 'Open analytics'}><BarChart3 /></button></div><div className="stat-block"><span>{zh ? '全球排名' : 'Global Rank'}</span><div className="stat-line"><strong>37</strong><small>▲ 5</small><em>Top 1%</em></div></div><div className="stat-block"><span>{zh ? '今日 Token' : 'Today’s Tokens'}</span><div className="stat-line"><strong>12.7M</strong><small>▲ 15.2%</small></div></div><div className="streak-line"><span>{zh ? '连续活跃' : 'Streak'}</span><div><Flame /><strong>23</strong>{zh ? '天' : 'days'}</div></div></section>
      <section className="side-card activity-card"><div className="card-heading"><h2>{zh ? 'Token 活跃度' : 'Token Activity'}</h2><CircleHelp /></div><div className="month-row"><span>May 2025</span><div><button type="button" aria-label="Previous month"><ChevronLeft /></button><button type="button" aria-label="Next month"><ChevronRight /></button></div></div><div className="week-labels">{['M', 'T', 'W', 'T', 'F', 'S', 'S'].map((day, index) => <span key={`${day}-${index}`}>{day}</span>)}</div><div className="home-heatmap">{heat.map((level, index) => <span key={index} data-level={level} />)}</div><div className="heat-legend"><span>{zh ? '少' : 'Less'}</span>{[0, 1, 2, 3, 4, 5].map((level) => <i key={level} data-level={level} />)}<span>{zh ? '多' : 'More'}</span></div></section>
      <section className="side-card tools-card"><div className="card-heading"><h2>{zh ? '常用工具' : 'Top Tools'}</h2><button type="button" className="view-all">{zh ? '全部' : 'View all'}</button></div><div className="tool-list">{tools.map((tool) => <div className="tool-row" key={tool.name}><span className={`tool-mark tool-${tool.name.toLowerCase()}`}>{tool.mark}</span><strong>{tool.name}</strong><div className="tool-track"><i style={{ width: `${tool.value}%` }} /></div><span>{tool.value}%</span></div>)}</div><p>{zh ? '基于今日消耗的 Token' : 'Based on tokens burned today'}</p></section>
    </aside>
  </div></div>;
};
