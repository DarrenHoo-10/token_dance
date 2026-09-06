import { Link } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { avatarUrl } from '@/utils/avatar';
import type { LeaderboardEntry } from '@/types/api';
import { RankChange } from './RankChange';

export function LeaderboardTable({ entries, ownEntry }: { entries: LeaderboardEntry[]; ownEntry?: LeaderboardEntry | null }) {
  const { locale } = useLocale(); const zh = locale === 'zh-CN';
  const rows = ownEntry && ownEntry.rankNo > 1000 ? [...entries, ownEntry] : entries;
  return <div className="leaderboard-table-scroll"><table className="leaderboard-data-table" aria-label={zh ? '排行榜列表' : 'Leaderboard list'}>
    <thead><tr><th scope="col">{zh ? '排名' : 'Rank'}</th><th scope="col">{zh ? '开发者' : 'Developer'}</th><th scope="col">Token</th><th scope="col" title={zh ? '同一统计周期与昨日比较 · UTC' : 'Same ranking window compared with yesterday · UTC'}>{zh ? '较昨日' : 'Vs yesterday'}</th></tr></thead>
    <tbody>{rows.map(entry => <tr key={entry.handle} className={entry === ownEntry ? 'leaderboard-own-row' : undefined} aria-label={entry === ownEntry ? (zh ? '我的排名' : 'My rank') : undefined}>
      <td><span className={`list-rank rank-${entry.rankNo}`}>{entry.rankNo}</span></td>
      <td><Link className="leaderboard-person" to={`/u/${encodeURIComponent(entry.handle)}`}>
        {entry.avatarUrl ? <img src={avatarUrl(entry.avatarUrl)} alt="" /> : <span className="list-avatar">{entry.displayName.slice(0,1).toUpperCase()}</span>}
        <span><strong>{entry.displayName}{entry === ownEntry && <span className="leaderboard-me-badge">{zh ? '我' : 'You'}</span>}</strong><small>@{entry.handle}</small></span>
      </Link></td>
      <td className="mono-num" title={Number(entry.metricValue).toLocaleString()}>{new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(Number(entry.metricValue))}</td>
      <td><RankChange value={entry.rankDelta} isNew={entry.isNew} /></td>
    </tr>)}</tbody>
  </table></div>;
}
