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
import type { PublicUserProfile } from '@/types/api';

export const PublicProfilePage: React.FC = () => {
  const { handle } = useParams<{ handle: string }>();
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();

  const [profile, setProfile] = useState<PublicUserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const fetchProfile = useCallback(async () => {
    if (!handle) return;
    try {
      setLoading(true);
      setError(null);
      const res = await api.getPublicProfile(handle);
      setProfile(res);
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
    : 'MB';

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
              <img src={profile.avatarUrl} alt={profile.displayName} />
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
            <div style={{ display: 'flex', gap: 8 }}>
              <Badge variant="lime">{t('publicProfile.publicLeaderboardTag')}</Badge>
              <Badge>{t('publicProfile.updatedAgo')} 4m</Badge>
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
              {profile.tokenTotal || '—'}
            </strong>
          </div>
          <div>
            <small style={{ color: 'var(--text-muted)', fontSize: 11 }}>{t('metrics.generatedCodeLines')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 22, marginTop: 4 }}>
              {profile.codeLinesTotal || '—'}
            </strong>
          </div>
          <div>
            <small style={{ color: 'var(--text-muted)', fontSize: 11 }}>{t('metrics.estimatedCost')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 22, marginTop: 4 }}>
              {profile.estimatedCostTotal || '—'}
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

          <TokenTrendChart trends={profile.tokenTrend || []} />
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
              {profile.percentile ? profile.percentile : '—'}
            </span>
          </div>

          <p style={{ fontSize: 12, color: '#a0aaa2', marginTop: 16, lineHeight: 1.5 }}>
            {t('publicProfile.rankNotice')}
          </p>

          <div className="progress-track" style={{ marginTop: 24, backgroundColor: '#273129' }}>
            <div
              className="progress-fill"
              style={{
                width: profile.percentile && profile.percentile.includes('%')
                  ? profile.percentile.replace(/[^0-9.]/g, '') + '%'
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
            <Badge variant="lime">Top 3</Badge>
          </div>
          <SkillRanking skills={profile.skillRanking || []} />
        </div>
      </div>
    </div>
  );
};
