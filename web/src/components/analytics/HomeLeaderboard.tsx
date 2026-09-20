import { useEffect, useRef, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ChevronDown, Search, X } from 'lucide-react';
import type { LeaderboardEntry } from '@/types/api';
import { useLocale } from '@/context/LocaleContext';
import { LeaderboardTable } from './LeaderboardTable';
import { publicLeaderboardName } from './leaderboardName';

export function HomeLeaderboard({ entries, ownEntry, window: period }: {
  entries: LeaderboardEntry[];
  ownEntry?: LeaderboardEntry | null;
  window: string;
}) {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  const location = useLocation();
  const searchInput = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (new URLSearchParams(location.search).get('search') !== '1') return;
    searchInput.current?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
    searchInput.current?.focus({ preventScroll: true });
  }, [location.key, location.search]);
  const [query, setQuery] = useState('');
  const [expanded, setExpanded] = useState(false);
  const term = query.trim().toLocaleLowerCase();
  const filtered = entries.filter(entry => `${publicLeaderboardName(entry)} ${entry.handle}`.toLocaleLowerCase().includes(term));
  const visible = term || expanded ? filtered : filtered.slice(0, 6);
  return <>
    <div className="panel-header">
      <div><h2>{zh ? '正在创造的他们' : 'Meet the builders'}</h2><p>{zh ? '从一个灵感，到下一个可能。' : 'From a spark to something real.'}</p></div>
      <label className="sky-board-search"><Search size={16} /><input ref={searchInput} value={query} onChange={event => setQuery(event.target.value)} placeholder={zh ? '搜索开发者' : 'Find a developer'} aria-label={zh ? '搜索当前榜单开发者' : 'Search developers in this board'} />{query && <button type="button" onClick={() => setQuery('')} aria-label={zh ? '清除搜索' : 'Clear search'}><X size={14} /></button>}</label>
    </div>
    {visible.length ? <LeaderboardTable entries={visible} ownEntry={term ? null : ownEntry} window={period} /> : <p className="leaderboard-empty">{zh ? (term ? '当前榜单没有匹配的开发者' : '暂无账号') : (term ? 'No matching developers in this board' : 'No accounts yet')}</p>}
    <div className="sky-board-actions">
      {!term && entries.length > 6 && <button type="button" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{zh ? (expanded ? '收起榜单' : '展开当前榜单') : (expanded ? 'Show less' : 'Show this board')}<ChevronDown size={16} className={expanded ? 'rotate' : ''} /></button>}
      <Link to={`/leaderboard/list?window=${period}`}>{zh ? '查看全部开发者' : 'See all developers'}<ChevronDown size={15} /></Link>
    </div>
  </>;
}
