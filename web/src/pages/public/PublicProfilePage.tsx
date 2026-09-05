import React, { useCallback, useEffect, useMemo, useState } from 'react';
import { Link2, LockKeyhole } from 'lucide-react';
import { Link, useParams } from 'react-router-dom';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { MetricGrid } from '@/components/analytics/MetricGrid';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { Button } from '@/components/common/Button';
import { useLocale } from '@/context/LocaleContext';
import { useAuth } from '@/context/AuthContext';
import { EmptyState } from '@/components/states/EmptyState';
import { useNotification } from '@/context/NotificationContext';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { api, ApiError } from '@/api/client';
import type { PersonalSummaryMetrics, PublicUserProfile, SkillItem, TokenTrendPoint } from '@/types/api';

export const PublicProfilePage: React.FC = () => {
  const { handle } = useParams<{ handle: string }>();
  const { locale, t } = useLocale();
  const { user } = useAuth();
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
      const profileRes = await api.getPublicProfile(handle);
      const [trendRes, skillRes] = await Promise.all([
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
    const total = Number(profile.tokenTotal || 0);
    const codeLines = Number(profile.codeLinesTotal || 0);
    return {
      estimatedCost: { amount: profile.estimatedCostTotal ?? null, currency: 'USD', supported: Boolean(profile.estimatedCostTotal) },
      totalTokens: { value: profile.tokenTotal ?? null, supported: Boolean(profile.tokenTotal) },
      generatedCodeLines: { value: profile.codeLinesTotal ?? null, supported: Boolean(profile.codeLinesTotal) },
      tokensPerCodeLine: { value: total > 0 && codeLines > 0 ? String(total / codeLines) : null, supported: total > 0 && codeLines > 0 },
      inputContextTokens: { value: null, supported: false },
      outputTokens: { value: null, supported: false },
      cacheHitRate: { value: null, supported: false },
      activeDurationMs: { value: null, supported: false },
      messageCount: { value: null, supported: false },
      userMessageCount: { value: null, supported: false },
    };
  }, [profile]);

  if (loading) return <LoadingState />;
  if (error instanceof ApiError && error.status === 404 && error.code === 'PUBLIC_PROFILE_NOT_FOUND') {
    const isOwner = Boolean(user?.handle && user.handle.toLowerCase() === handle?.toLowerCase());
    return (
      <section className="product-page-shell">
        <EmptyState
          icon={<LockKeyhole size={32} aria-hidden="true" />}
          title={t('publicProfile.unavailableTitle')}
          description={t(isOwner ? 'publicProfile.ownerUnavailableDesc' : 'publicProfile.unavailableDesc')}
        />
        <div className="unavailable-actions">
          {isOwner && <Link className="btn btn-dark" to="/settings/privacy">{t('settings.tabPrivacy')}</Link>}
          <Link className={`btn ${isOwner ? 'btn-outline' : 'btn-dark'}`} to={isOwner ? '/me' : '/leaderboard'}>
            {t(isOwner ? 'publicProfile.viewOwnData' : 'publicProfile.backToLeaderboard')}
          </Link>
        </div>
      </section>
    );
  }
  if (error) return <ErrorState error={error} onRetry={fetchProfile} />;
  if (!profile || !metrics) return null;

  const initials = profile.displayName.split(' ').map((part) => part[0]).join('').slice(0, 2).toUpperCase();
  const displayTrends = trends;
  const displaySkills = skills;
  const displayAgents = profile.agentBreakdown || [];
  const displayCalendar = profile.activityCalendar || [];

  return (
    <section className="product-page-shell public-data-page" aria-labelledby="public-data-title">
      <div className="public-data-header">
        <div className="public-identity">
          <div className="public-avatar">{profile.avatarUrl ? <img src={profile.avatarUrl} alt={profile.displayName} /> : <span>{initials}</span>}</div>
          <div><span>{zh ? '个人数据页' : 'Personal Data'}</span><h1 id="public-data-title">{profile.displayName}</h1><p>@{profile.handle}{profile.bio ? `  ${profile.bio}` : ''}</p></div>
        </div>
        <Button variant="outline" onClick={() => { navigator.clipboard.writeText(window.location.href); showToast(t('publicProfile.linkCopied'), 'success'); }}><Link2 size={16} aria-hidden="true" />{zh ? '复制链接' : 'Copy link'}</Button>
      </div>

      <MetricGrid metrics={metrics} />

      <div className="public-data-primary-grid">
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Token 趋势' : 'Token Trend'}</h2><p className="text-muted">{zh ? '最近 30 天' : 'Last 30 days'}</p></div></div><TokenTrendChart trends={displayTrends} /></article>
        <article className="panel"><div className="public-rank-label">{zh ? '全球排名' : 'Global Rank'}</div><strong className="public-rank-value mono-num">{profile.rank ? `#${profile.rank}` : '—'}</strong><span className="public-rank-delta">{profile.rankDelta ? `${profile.rankDelta > 0 ? '+' : ''}${profile.rankDelta}` : ''}</span><p>{zh ? 'TokenBoard 公开排名' : 'Public TokenBoard position'}</p></article>
      </div>

      <div className="public-data-secondary-grid">
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Agent 构成' : 'Agent Breakdown'}</h2><p className="text-muted">{zh ? '按 Token 占比' : 'By token share'}</p></div></div><AgentBreakdown items={displayAgents} /></article>
        <article className="panel"><div className="panel-header"><div><h2>{zh ? '活跃日历' : 'Activity Calendar'}</h2><p className="text-muted">{zh ? '近 10 周活跃轨迹' : 'Activity across the last 10 weeks'}</p></div></div><ActivityCalendar days={displayCalendar} /></article>
        <article className="panel"><div className="panel-header"><div><h2>{zh ? 'Skill 排行榜' : 'Skill Ranking'}</h2><p className="text-muted">{zh ? '按调用次数排序' : 'Ranked by call count'}</p></div></div><SkillRanking skills={displaySkills} /></article>
      </div>
    </section>
  );
};
