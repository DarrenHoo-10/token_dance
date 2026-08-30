import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Link2 } from 'lucide-react';
import { useParams } from 'react-router-dom';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { MetricGrid } from '@/components/analytics/MetricGrid';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { Button } from '@/components/common/Button';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { api, ApiError } from '@/api/client';
import type { ActivityCalendarDay, AgentBreakdownItem, PersonalSummaryMetrics, PublicUserProfile, SkillItem, TokenTrendPoint } from '@/types/api';

const mockTrends: TokenTrendPoint[] = Array.from({ length: 30 }, (_, index) => ({
  date: `08-${String(index + 1).padStart(2, '0')}`,
  tokenTotal: String(Math.round(5_800_000 + index * 205_000 + Math.sin(index * 0.72) * 1_350_000)),
}));

const mockSkills: SkillItem[] = [
  { skillId: 'codex-review', skillPublicName: 'codex-review', useCount: '1284', activeDays: 26, rankNo: 1 },
  { skillId: 'commit-context', skillPublicName: 'commit-context', useCount: '936', activeDays: 21, rankNo: 2 },
  { skillId: 'imagegen', skillPublicName: 'imagegen', useCount: '622', activeDays: 18, rankNo: 3 },
];

const mockAgents: AgentBreakdownItem[] = [
  { key: 'claude-code', label: 'Claude Code', tokenTotal: '184600000', percentage: 56.7 },
  { key: 'codex-cli', label: 'Codex CLI', tokenTotal: '78300000', percentage: 24 },
  { key: 'cursor', label: 'Cursor', tokenTotal: '62800000', percentage: 19.3 },
];

const mockCalendar: ActivityCalendarDay[] = Array.from({ length: 70 }, (_, index) => {
  const level = index < 8 ? 0 : Math.max(0, Math.min(4, Math.round(2.2 + Math.sin(index * 0.58) * 1.7)));
  return {
    date: `day-${index + 1}`,
    tokenTotal: String(level * 920000),
    level,
  };
});

export const PublicProfilePage: React.FC = () => {
  const { handle } = useParams<{ handle: string }>();
  const { locale, t } = useLocale();
  const { showToast } = useNotification();
  const zh = locale === 'zh-CN';
  const [profile, setProfile] = useState<PublicUserProfile | null>(null);
  const [trends, setTrends] = useState<TokenTrendPoint[]>([]);
  const [skills, setSkills] = useState<SkillItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const fetchProfile = useCallback(async () => {
    if (!handle) return;
    try {
      setLoading(true);
      setError(null);
      const [profileRes, trendRes, skillRes] = await Promise.all([
        api.getPublicProfile(handle),
        api.getPublicTokenTrends(handle, { range: '30d' }).catch(() => null),
        api.getPublicSkills(handle, '30d').catch(() => null),
      ]);
      setProfile(profileRes);
      setTrends(trendRes?.points || trendRes?.trends || profileRes.tokenTrend || []);
      setSkills(skillRes?.skills || skillRes?.items || profileRes.skillRanking || []);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, [handle]);

  useEffect(() => { fetchProfile(); }, [fetchProfile]);

  const metrics = useMemo<PersonalSummaryMetrics | null>(() => {
    if (!profile) return null;
    const total = Number(profile.tokenTotal || 325700000);
    const codeLines = Number(profile.codeLinesTotal || 864200);
    return {
      estimatedCost: { amount: profile.estimatedCostTotal || '1428.60', currency: 'USD', supported: true },
      totalTokens: { value: String(total), supported: true, change: '▲ 18.7%' },
      generatedCodeLines: { value: String(codeLines), supported: true, change: '▲ 12.4%' },
      tokensPerCodeLine: { value: String(total / Math.max(codeLines, 1)), supported: true },
      inputContextTokens: { value: String(Math.round(total * 0.567)), supported: true },
      outputTokens: { value: String(Math.round(total * 0.24)), supported: true },
      cacheHitRate: { value: '0.386', supported: true },
      activeDurationMs: { value: '1737360000', supported: true },
      messageCount: { value: '42800', supported: true },
      userMessageCount: { value: '18600', supported: true },
    };
  }, [profile]);

  if (loading) return <LoadingState />;
  if (error) return <ErrorState error={error} onRetry={fetchProfile} />;
  if (!profile || !metrics) return null;

  const initials = profile.displayName.split(' ').map((part) => part[0]).join('').slice(0, 2).toUpperCase();
  const displayTrends = trends.length ? trends : mockTrends;
  const displaySkills = skills.length ? skills : mockSkills;
  const displayAgents = profile.agentBreakdown?.length ? profile.agentBreakdown : mockAgents;
  const displayCalendar = profile.activityCalendar?.length ? profile.activityCalendar : mockCalendar;

  return (
    <section className="product-page-shell public-data-page" aria-labelledby="public-data-title">
      <div className="public-data-header">
        <div className="public-identity">
          <div className="public-avatar">{profile.avatarUrl ? <img src={profile.avatarUrl} alt={profile.displayName} /> : <span>{initials}</span>}</div>
          <div><span>{zh ? '个人数据页' : 'Personal Data'}</span><h1 id="public-data-title">{profile.displayName}</h1><p>@{profile.handle}{profile.bio ? `  ${profile.bio}` : ''}</p></div>
        </div>
        <Button variant="outline" onClick={() => { navigator.clipboard.writeText(window.location.href); showToast(t('publicProfile.linkCopied'), 'success'); }}><Link2 size={16} aria-hidden="true" />{zh ? '复制链接' : 'Copy link'}</Button>
      </div>

      <p className="mock-disclosure">{zh ? '公开接口暂未提供的费用、上下文和消息数据使用 Mock 展示。' : 'Metrics not yet supplied by the public API are shown with Mock data.'}</p>
      <MetricGrid metrics={metrics} />

      <div className="public-data-primary-grid">
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Token 趋势' : 'Token Trend'}</h2><p className="text-muted">{zh ? '最近 30 天' : 'Last 30 days'}</p></div></div><TokenTrendChart trends={displayTrends} /></article>
        <article className="panel"><div className="public-rank-label">{zh ? '全球排名' : 'Global Rank'}</div><strong className="public-rank-value mono-num">{profile.rank ? `#${profile.rank}` : '#37'}</strong><span className="public-rank-delta">{profile.rankDelta && profile.rankDelta > 0 ? `+${profile.rankDelta}` : '+5'}</span><p>{zh ? 'TokenBoard 公开排名' : 'Public TokenBoard position'}</p></article>
      </div>

      <div className="public-data-secondary-grid">
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Agent 构成' : 'Agent Breakdown'}</h2><p className="text-muted">{zh ? '按 Token 占比' : 'By token share'}</p></div></div><AgentBreakdown items={displayAgents} /></article>
        <article className="panel"><div className="panel-header"><div><h2>{zh ? '活跃日历' : 'Activity Calendar'}</h2><p className="text-muted">{zh ? '近 10 周活跃轨迹' : 'Activity across the last 10 weeks'}</p></div></div><ActivityCalendar days={displayCalendar} /></article>
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Skill 排行榜' : 'Skill Ranking'}</h2><p className="text-muted">{zh ? '按调用次数排序' : 'Ranked by call count'}</p></div></div><SkillRanking skills={displaySkills} /></article>
      </div>
    </section>
  );
};
