import { useEffect, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { api } from '@/api/client';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { LeaderboardTable } from '@/components/analytics/LeaderboardTable';
import type { LeaderboardResponse, LeaderboardEntry } from '@/types/api';

export function LeaderboardListPage() {
  const { locale } = useLocale(); const zh = locale === 'zh-CN';
  const { authenticated, user } = useAuth();
  const accountKey = user?.userId ?? user?.handle ?? '';
  const [params,setParams] = useSearchParams();
  const selected = params.get('window') ?? 'today';
  const window = ['today','7d','30d','all'].includes(selected) ? selected : 'today';
  const cursor = params.get('cursor') || undefined;
  const [data,setData] = useState<LeaderboardResponse | null>(null);
  const [loading,setLoading] = useState(true);
  const [failed,setFailed] = useState(false);
  const [retry,setRetry] = useState(0);
  const [own,setOwn] = useState<{window:string;userId:string;entry:LeaderboardEntry} | null>(null);
  useEffect(() => {
    let active = true; setOwn(null);
    if (authenticated && user) api.getPersonalSummary(window).then(summary => {
      if (active && summary.ranking.entry) setOwn({window,userId:accountKey,entry:summary.ranking.entry});
    }, () => { if (active) setOwn(null); });
    return () => { active = false; };
  },[window,authenticated,accountKey,retry]);
  const ownEntry = authenticated && own?.window === window && own.userId === accountKey ? own.entry : null;
  useEffect(() => {
    let active = true; setLoading(true); setFailed(false);
    api.getLeaderboard({window,cursor,limit:20}).then(value => { if (active) setData(value); }, () => { if (active) setFailed(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  },[window,cursor,retry]);
  return <section className="leaderboard-list-page">
    <div className="panel-header"><div><p className="eyebrow">TokenBoard · Top 1000</p><h1>{zh ? '排行榜列表' : 'Leaderboard'}</h1><p className="text-muted">{zh ? '仅展示前 1000 名 · UTC' : 'Top 1000 · UTC'}</p></div><div className="leaderboard-total"><span>{zh ? '总参与人数' : 'Total participants'}</span><strong>{loading || failed || data?.totalEntries == null ? '—' : data.totalEntries.toLocaleString()}</strong><Link to="/leaderboard">{zh ? '返回概览' : 'Back to overview'} →</Link></div></div>
    <div className="panel leaderboard-list-panel">
      <div className="range-tabs" role="tablist" aria-label={zh ? '排行榜周期' : 'Leaderboard period'}>
        {(['today','7d','30d','all'] as const).map((key,index) => <button key={key} role="tab" aria-selected={window===key} className={window===key?'active':''} onClick={() => setParams({window:key})}>{(zh ? ['今天','近 7 天','近 30 天','全部时间'] : ['Today','7 days','30 days','All time'])[index]}</button>)}
      </div>
      {loading ? <p className="leaderboard-empty" role="status">{zh?'加载中…':'Loading…'}</p> : failed ? <div className="leaderboard-empty" role="alert"><p>{zh?'排行榜加载失败':'Could not load rankings'}</p><button className="btn btn-outline" onClick={()=>setRetry(v=>v+1)}>{zh?'重试':'Retry'}</button></div> : <>
        {data?.entries.length ? <LeaderboardTable entries={data.entries} ownEntry={ownEntry} /> : <p className="leaderboard-empty">{zh?'本周期暂无公开排行数据':'No public rankings this period'}</p>}
        <div className="leaderboard-pagination"><span>{zh ? `第 ${Math.floor(Number(cursor || 0)/20)+1} / ${Math.max(1,Math.ceil(Math.min(1000,data?.totalEntries ?? 0)/20))} 页` : `Page ${Math.floor(Number(cursor || 0)/20)+1} / ${Math.max(1,Math.ceil(Math.min(1000,data?.totalEntries ?? 0)/20))}`}</span><div>
          <button className="btn btn-outline" disabled={!cursor} onClick={()=>setParams({window,...(Number(cursor)>20 ? {cursor:String(Number(cursor)-20)} : {})})}>{zh?'上一页':'Previous'}</button>
          <button className="btn btn-outline" disabled={!data?.nextCursor || Number(data.nextCursor)>=1000} onClick={()=>data?.nextCursor && Number(data.nextCursor)<1000 && setParams({window,cursor:data.nextCursor})}>{zh?'下一页':'Next'}</button>
        </div></div>
      </>}
    </div>
  </section>;
}
