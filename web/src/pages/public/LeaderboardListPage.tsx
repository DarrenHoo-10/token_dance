import { useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { api } from '@/api/client';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { useVisibleRefresh } from '@/hooks/useVisibleRefresh';
import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import type { LeaderboardResponse } from '@/types/api';

export function LeaderboardListPage() {
  const { locale } = useLocale(); const zh = locale === 'zh-CN';
  const { authenticated, user } = useAuth();
  const accountKey = user?.userId ?? user?.handle ?? '';
  const [params, setParams] = useSearchParams();
  const selected = params.get('window') ?? 'today';
  const window = ['today', '7d', '30d', 'all'].includes(selected) ? selected : 'today';
  const cursor = params.get('cursor') || undefined;
  const [data, setData] = useState<LeaderboardResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);
  const hasSnapshotRef = useRef(false);
  const snapshotRef = useRef<string | undefined>();
  const snapshotScopeRef = useRef<string>();
  const totalParticipants = data?.totalParticipants ?? data?.totalEntries;

  useEffect(() => {
    let active = true;
    const scope = `${window}:${authenticated}:${accountKey}`;
    if (snapshotScopeRef.current !== scope) {
      snapshotScopeRef.current = scope;
      snapshotRef.current = undefined;
      hasSnapshotRef.current = false;
      setData(null);
      setFailed(false);
    }
    if (!hasSnapshotRef.current) setLoading(true);
    const snapshotId = cursor ? snapshotRef.current : undefined;
    api.getLeaderboardView(authenticated, snapshotId
      ? { window, cursor, limit: 20, snapshotId }
      : { window, cursor, limit: 20 }).then(value => {
      if (!active) return;
      hasSnapshotRef.current = true;
      snapshotRef.current = value.snapshotId || undefined;
      setData(value);
      setFailed(false);
    }, () => {
      if (active) setFailed(true);
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [window, cursor, retry, accountKey, authenticated]);

  useVisibleRefresh(() => setRetry((value) => value + 1));

  const retryButton = <button className="btn btn-outline" type="button" onClick={() => setRetry((value) => value + 1)}>{zh ? '重试' : 'Retry'}</button>;
  const connectionError = (
    <div className={data ? 'leaderboard-refresh-status' : 'leaderboard-empty'} role="alert">
      <p>{zh ? '连接异常' : 'Connection error'}</p>
      {retryButton}
    </div>
  );

  return <section className="leaderboard-list-page">
    <div className="panel-header"><div><h1>{zh ? '排行榜' : 'Leaderboard'}</h1><p className="text-muted">UTC</p></div><div className="leaderboard-total"><span>{zh ? '总人数' : 'Total'}</span><strong>{totalParticipants == null ? '—' : totalParticipants.toLocaleString()}</strong><Link to="/leaderboard">{zh ? '返回概览' : 'Back to overview'} →</Link></div></div>
    <div className="panel leaderboard-list-panel">
      <div className="range-tabs" role="tablist" aria-label={zh ? '排行榜周期' : 'Leaderboard period'}>
        {(['today', '7d', '30d', 'all'] as const).map((key, index) => <button key={key} role="tab" aria-selected={window === key} className={window === key ? 'active' : ''} onClick={() => setParams({ window: key })}>{(zh ? ['今天', '近 7 天', '近 30 天', '全部时间'] : ['Today', '7 days', '30 days', 'All time'])[index]}</button>)}
      </div>
      {loading && !data ? <p className="leaderboard-empty" role="status">{zh ? '加载中…' : 'Loading…'}</p> : !data && failed ? connectionError : data ? <>
        {failed && connectionError}
        {data.entries.length ? <LeaderboardTable entries={data.entries} ownEntry={authenticated ? data.ownEntry : null} /> : <p className="leaderboard-empty">{zh ? '暂无账号' : 'No accounts yet'}</p>}
        <div className="leaderboard-pagination"><span>{zh ? `第 ${Math.floor(Number(cursor || 0) / 20) + 1} / ${Math.max(1, Math.ceil(Math.min(1000, data.totalEntries ?? 0) / 20))} 页` : `Page ${Math.floor(Number(cursor || 0) / 20) + 1} / ${Math.max(1, Math.ceil(Math.min(1000, data.totalEntries ?? 0) / 20))}`}</span><div>
          <button className="btn btn-outline" disabled={!cursor} onClick={() => setParams({ window, ...(Number(cursor) > 20 ? { cursor: String(Number(cursor) - 20) } : {}) })}>{zh ? '上一页' : 'Previous'}</button>
          <button className="btn btn-outline" disabled={!data.nextCursor || Number(data.nextCursor) >= 1000} onClick={() => data.nextCursor && Number(data.nextCursor) < 1000 && setParams({ window, cursor: data.nextCursor })}>{zh ? '下一页' : 'Next'}</button>
        </div></div>
      </> : null}
    </div>
  </section>;
}
