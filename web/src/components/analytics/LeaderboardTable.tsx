import { Link } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { UserAvatar } from '@/components/common/UserAvatar';
import type { LeaderboardEntry } from '@/types/api';
import { publicLeaderboardName } from './leaderboardName';
import { RankChange } from './RankChange';
import { hasRankedTokens } from './tokenRanking';

export function LeaderboardTable({ entries, ownEntry, window: period }: { entries: LeaderboardEntry[]; ownEntry?: LeaderboardEntry | null; window?: string }) {
  const { locale } = useLocale(); const zh = locale === 'zh-CN';
  const rows = ownEntry && ownEntry.rankNo > 1000 ? [...entries, ownEntry] : entries;
  const comparisonTitle = period === 'today'
    ? (zh ? '与前一个 24 小时周期比较 · 北京时间' : 'Compared with the prior 24-hour window · Beijing time')
    : (zh ? '同一统计周期与昨日比较 · 北京时间' : 'Same ranking window compared with yesterday · Beijing time');
  const comparisonLabel = period === 'today' ? (zh ? '较前 24h' : 'Vs prior 24h') : (zh ? '较昨日' : 'Vs yesterday');
  return <div className="leaderboard-table-scroll"><table className="leaderboard-data-table" aria-label={zh ? '排行榜列表' : 'Leaderboard list'}>
    <thead><tr><th scope="col">{zh ? '排名' : 'Rank'}</th><th scope="col">{zh ? '开发者' : 'Developer'}</th><th scope="col">Token</th><th scope="col" title={comparisonTitle}>{comparisonLabel}</th></tr></thead>
    <tbody>{rows.map(entry => <tr key={entry.handle} className={entry === ownEntry ? 'leaderboard-own-row' : undefined} aria-label={entry === ownEntry ? (zh ? '我的排名' : 'My rank') : undefined}>
      <td><span className={hasRankedTokens(entry.metricValue) ? `list-rank rank-${entry.rankNo}` : 'list-unranked'}>{hasRankedTokens(entry.metricValue) ? entry.rankNo : (zh ? '暂未上榜' : 'Not ranked yet')}</span></td>
      <td><Link className="leaderboard-person" to={`/u/${encodeURIComponent(entry.handle)}`}>
        <UserAvatar url={entry.avatarUrl} name={publicLeaderboardName(entry)} fallbackClassName="list-avatar" loading={entry.rankNo <= 3 ? 'eager' : 'lazy'} />
        <span className="leaderboard-person-name"><strong>{publicLeaderboardName(entry)}{entry === ownEntry && <span className="leaderboard-me-badge">{zh ? '我' : 'You'}</span>}</strong></span>
      </Link></td>
      <td className="mono-num" title={Number(entry.metricValue).toLocaleString()}>{new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 1 }).format(Number(entry.metricValue))}</td>
      <td>{hasRankedTokens(entry.metricValue) ? <RankChange value={entry.rankDelta} isNew={entry.isNew} /> : '—'}</td>
    </tr>)}</tbody>
  </table></div>;
}
