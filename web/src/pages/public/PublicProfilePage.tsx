import React, { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { TokenTrendChart } from '@/components/analytics/TokenTrendChart';
import { AgentBreakdown } from '@/components/analytics/AgentBreakdown';
import { ActivityCalendar } from '@/components/analytics/ActivityCalendar';
import { SkillRanking } from '@/components/analytics/SkillRanking';
import { api, ApiError } from '@/api/client';
import type { PublicUserProfile, TokenTrendPoint, SkillItem } from '@/types/api';

function formatNumber(val: string | null | undefined): string {
  if (!val) return '—';
  const num = parseFloat(val);
  if (isNaN(num)) return val;
  if (num >= 1_000_000_000) return (num / 1_000_000_000).toFixed(1) + 'B';
  if (num >= 1_000_000) return (num / 1_000_000).toFixed(1) + 'M';
  if (num >= 1_000) return (num / 1_000).toFixed(1) + 'K';
  return num.toLocaleString();
}

function formatRelativeTime(dateStr: string | null | undefined, updatedAgoText: string, justNowText: string): string {
  if (!dateStr) return '';
  const date = new Date(dateStr);
  const diffMs = Date.now() - date.getTime();
  if (diffMs < 0 || isNaN(diffMs)) {
    return date.toLocaleDateString();
  }
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 1) return `${updatedAgoText} ${justNowText}`;
  if (diffMins < 60) return `${updatedAgoText} ${diffMins}m`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${updatedAgoText} ${diffHours}h`;
  const diffDays = Math.floor(diffHours / 24);
  return `${updatedAgoText} ${diffDays}d`;
}

export const PublicProfilePage: React.FC = () => {
  const { handle } = useParams<{ handle: string }>();
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();

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

      // Fetch profile, trends, and skills separately
      const [profileRes, trendsRes, skillsRes] = await Promise.all([
        api.getPublicProfile(handle),
        api.getPublicTokenTrends(handle, { range: '30d' }).catch(() => null),
        api.getPublicSkills(handle, '30d').catch(() => null),
      ]);

      setProfile(profileRes);
      if (trendsRes && (trendsRes.points || trendsRes.trends)) {
        setTrends(trendsRes.points || trendsRes.trends || []);
      } else if (profileRes.tokenTrend) {
        setTrends(profileRes.tokenTrend);
      } else {
        setTrends([]);
      }

      if (skillsRes && (skillsRes.skills || skillsRes.items)) {
        setSkills(skillsRes.skills || skillsRes.items || []);
      } else if (profileRes.skillRanking) {
        setSkills(profileRes.skillRanking);
      } else {
        setSkills([]);
      }
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, [handle]);

  useEffect(() => {
    fetchProfile();
  }, [fetchProfile]);

  const handleCopyLink = () => {
    navigator.clipboard.writeText(window.location.href);
    showToast(t('publicProfile.linkCopied'), 'success');
  };

  const handleAddCompare = () => {
    if (!profile?.handle) return;
    navigate(`/compare?handles=${encodeURIComponent(profile.handle)}`);
  };

  if (loading) return <LoadingState />;
  if (error) return <ErrorState error={error} onRetry={fetchProfile} />;
  if (!profile) return null;

  const initials = profile.displayName
    ? profile.displayName.split(' ').map((n) => n[0]).join('').substring(0, 2).toUpperCase()
    : 'TD';

  const updatedTimeDisplay = formatRelativeTime(
    profile.generatedAt || profile.dataWatermarkAt,
    t('publicProfile.updatedAgo'),
    t('dashboard.justNow')
  );

  return (
    <div>
      {/* Header Actions */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-end',
          marginBottom: 20,
          flexWrap: 'wrap',
          gap: 12,
        }}
      >
        <div>
          <p className="eyebrow">{t('publicProfile.headline')}</p>
          <h1>{profile.displayName}</h1>
          <p className="text-muted" style={{ fontSize: 13 }}>
            {t('publicProfile.subheadline')}
          </p>
        </div>

        <div style={{ display: 'flex', gap: 10 }}>
          <Button variant="outline" onClick={handleCopyLink}>
            🔗 {t('publicProfile.copyLink')}
          </Button>
          <Button variant="primary" onClick={handleAddCompare}>
            + {t('publicProfile.addCompare')}
          </Button>
        </div>
      </div>

      {/* Hero Panel */}
      <div
        className="panel"
        style={{
          display: 'grid',
          gridTemplateColumns: '1fr auto',
          alignItems: 'center',
          gap: 24,
          marginBottom: 20,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 20 }}>
          <div
            className="avatar"
            style={{
              width: 72,
              height: 72,
              fontSize: 22,
              backgroundColor: 'var(--bg-dark)',
              border: '2px solid var(--lime-border)',
            }}
          >
            {profile.avatarUrl ? (
              <img src={profile.avatarUrl} alt={profile.displayName} style={{ width: '100%', height: '100%', borderRadius: '50%' }} />
            ) : (
              <span>{initials}</span>
            )}
          </div>

          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
              <h2 style={{ fontSize: 24, margin: 0 }}>{profile.displayName}</h2>
              <span className="text-muted" style={{ fontSize: 14 }}>
                @{profile.handle}
              </span>
            </div>
            {profile.bio && (
              <p style={{ fontSize: 13, color: 'var(--text-muted)', marginTop: 4, marginBottom: 8 }}>
                {profile.bio}
              </p>
            )}
            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
              <Badge variant="lime">{t('publicProfile.publicLeaderboardTag')}</Badge>
              {updatedTimeDisplay && <Badge>{updatedTimeDisplay}</Badge>}
            </div>
          </div>
        </div>

        <div
          style={{
            display: 'flex',
            gap: 32,
            borderLeft: '1px solid var(--border-light)',
            paddingLeft: 32,
          }}
        >
          <div>
            <small style={{ color: 'var(--text-muted)', fontSize: 11 }}>{t('metrics.totalTokens')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 22, marginTop: 4 }}>
              {formatNumber(profile.tokenTotal)}
            </strong>
          </div>
          <div>
            <small style={{ color: 'var(--text-muted)', fontSize: 11 }}>{t('metrics.generatedCodeLines')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 22, marginTop: 4 }}>
              {formatNumber(profile.codeLinesTotal)}
            </strong>
          </div>
          <div>
            <small style={{ color: 'var(--text-muted)', fontSize: 11 }}>{t('metrics.activeStreak')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 22, marginTop: 4 }}>
              {profile.currentStreak !== null && profile.currentStreak !== undefined ? `${profile.currentStreak} ${t('metrics.days')}` : '—'}
            </strong>
          </div>
        </div>
      </div>

      {/* Middle Grid: Token Trend + Global Rank */}
      <div className="grid-2" style={{ marginBottom: 20 }}>
        {/* Public Token Trend */}
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('publicProfile.publicTrends')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('common.days30')}
              </p>
            </div>
          </div>

          <TokenTrendChart trends={trends} />
        </div>

        {/* Global Rank Card */}
        <div className="panel panel-dark">
          <p className="eyebrow" style={{ color: 'var(--lime)' }}>
            {t('publicProfile.globalPosition')}
          </p>
          <h2>{t('publicProfile.tokenLeaderboard')}</h2>

          <div style={{ display: 'flex', alignItems: 'flex-end', gap: 12, marginTop: 24 }}>
            <strong
              className="mono-num"
              style={{ fontSize: 72, lineHeight: 0.9, letterSpacing: '-0.06em' }}
            >
              {profile.rank ? `#${profile.rank}` : '—'}
            </strong>
            <span style={{ color: 'var(--lime)', fontWeight: 700, marginBottom: 8, fontSize: 13 }}>
              {profile.percentile !== null && profile.percentile !== undefined ? (
                typeof profile.percentile === 'number' ? `Top ${(100 - profile.percentile).toFixed(1)}%` : String(profile.percentile)
              ) : '—'}
            </span>
          </div>

          <p style={{ fontSize: 12, color: '#a0aaa2', marginTop: 16, lineHeight: 1.5 }}>
            {t('publicProfile.rankNotice')}
          </p>

          <div className="progress-track" style={{ marginTop: 24, backgroundColor: '#273129' }}>
            <div
              className="progress-fill"
              style={{
                width: profile.percentile
                  ? typeof profile.percentile === 'number'
                    ? `${profile.percentile}%`
                    : `${String(profile.percentile).replace(/[^0-9.]/g, '')}%`
                  : '100%',
              }}
            />
          </div>
        </div>
      </div>

      {/* Lower Grid: Breakdown + Activity + Skills */}
      <div className="grid-3">
        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('publicProfile.publicAgentBreakdown')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('publicProfile.publicSummary')}
              </p>
            </div>
          </div>
          <AgentBreakdown items={profile.agentBreakdown || []} />
        </div>

        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('publicProfile.publicActivity')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.activityCalendarSub')}
              </p>
            </div>
          </div>
          <ActivityCalendar days={profile.activityCalendar || []} />
        </div>

        <div className="panel">
          <div className="panel-header">
            <div>
              <h2>{t('publicProfile.publicSkills')}</h2>
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('dashboard.skillRankingSub')}
              </p>
            </div>
            {skills.length > 0 && <Badge variant="lime">Top {skills.length}</Badge>}
          </div>
          <SkillRanking skills={skills} />
        </div>
      </div>
    </div>
  );
};
