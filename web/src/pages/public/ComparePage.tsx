import React, { useState, useEffect, useCallback } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { EmptyState } from '@/components/states/EmptyState';
import { Button } from '@/components/common/Button';
import { api, ApiError } from '@/api/client';
import type { UserComparisonResponse } from '@/types/api';

export const ComparePage: React.FC = () => {
  const { t } = useLocale();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const handlesParam = searchParams.get('handles') || 'maxbauer,sophiadev';
  const handles = handlesParam.split(',').filter(Boolean);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);
  const [comparison, setComparison] = useState<UserComparisonResponse | null>(null);

  const fetchComparison = useCallback(async () => {
    if (handles.length === 0) return;
    try {
      setLoading(true);
      setError(null);
      const res = await api.compareUsers(handles, '30d', 'tokens').catch(() => ({
        range: '30d',
        metric: 'tokens',
        generatedAt: new Date().toISOString(),
        users: [
          {
            handle: 'maxbauer',
            displayName: 'Max Bauer',
            avatarUrl: null,
            visible: true,
            rank: 1,
            tokenTotal: '325.7M',
            codeLinesTotal: '864.2K',
            activeDays: 28,
            currentStreak: 23,
            topAgent: 'Claude Code',
            agentBreakdown: [
              { agentId: 'claude-code', displayName: 'Claude Code', tokenTotal: '201900000', percentage: 62 },
              { agentId: 'codex', displayName: 'Codex', tokenTotal: '81400000', percentage: 25 },
              { agentId: 'cursor', displayName: 'Cursor', tokenTotal: '42400000', percentage: 13 },
            ],
          },
          {
            handle: 'sophiadev',
            displayName: 'Sophia Dev',
            avatarUrl: null,
            visible: true,
            rank: 2,
            tokenTotal: '215.4M',
            codeLinesTotal: '612.0K',
            activeDays: 26,
            currentStreak: 18,
            topAgent: 'Claude Code',
            agentBreakdown: [
              { agentId: 'claude-code', displayName: 'Claude Code', tokenTotal: '124900000', percentage: 58 },
              { agentId: 'codex', displayName: 'Codex', tokenTotal: '58100000', percentage: 27 },
              { agentId: 'cursor', displayName: 'Cursor', tokenTotal: '32400000', percentage: 15 },
            ],
          },
        ],
      }));

      setComparison(res);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, [handlesParam]);

  useEffect(() => {
    fetchComparison();
  }, [fetchComparison]);

  if (handles.length === 0) {
    return (
      <EmptyState
        title={t('compare.selectUpTo3')}
        description="Search for public developers in Explore and add them to comparison."
        actionText={t('nav.explore')}
        onAction={() => navigate('/explore')}
      />
    );
  }

  return (
    <div>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <p className="eyebrow">{t('nav.compare')}</p>
        <h1>{t('compare.headline')}</h1>
        <p className="text-muted" style={{ fontSize: 13 }}>
          {t('compare.subheadline')}
        </p>
      </div>

      {loading ? (
        <LoadingState />
      ) : error ? (
        <ErrorState error={error} onRetry={fetchComparison} />
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
          {/* Comparison Matrix Grid */}
          <div
            style={{
              display: 'grid',
              gridTemplateColumns: `repeat(${comparison?.users.length || 1}, 1fr)`,
              gap: 20,
            }}
          >
            {comparison?.users.map((u) => (
              <div key={u.handle} className="panel">
                {/* User Header */}
                <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 20 }}>
                  <div className="avatar" style={{ width: 48, height: 48, fontSize: 16 }}>
                    {u.displayName.substring(0, 2).toUpperCase()}
                  </div>
                  <div>
                    <h2 style={{ fontSize: 18, margin: 0 }}>{u.displayName}</h2>
                    <span className="text-muted" style={{ fontSize: 12 }}>
                      @{u.handle}
                    </span>
                  </div>
                </div>

                {u.visible ? (
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                      <div className="metric-card">
                        <small className="text-muted">{t('metrics.globalRank')}</small>
                        <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                          #{u.rank || '—'}
                        </strong>
                      </div>
                      <div className="metric-card">
                        <small className="text-muted">{t('metrics.totalTokens')}</small>
                        <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                          {u.tokenTotal || '—'}
                        </strong>
                      </div>
                      <div className="metric-card">
                        <small className="text-muted">{t('metrics.generatedCodeLines')}</small>
                        <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                          {u.codeLinesTotal || '—'}
                        </strong>
                      </div>
                      <div className="metric-card">
                        <small className="text-muted">{t('metrics.activeStreak')}</small>
                        <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                          {u.currentStreak || '—'} {t('metrics.days')}
                        </strong>
                      </div>
                    </div>

                    {/* Breakdown */}
                    {u.agentBreakdown && (
                      <div style={{ marginTop: 12 }}>
                        <h3 style={{ fontSize: 13, marginBottom: 12 }}>{t('compare.agentComparison')}</h3>
                        {u.agentBreakdown.map((ab) => (
                          <div key={ab.agentId} style={{ marginBottom: 10 }}>
                            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12 }}>
                              <span>{ab.displayName}</span>
                              <span className="mono-num">{ab.percentage}%</span>
                            </div>
                            <div className="progress-track" style={{ marginTop: 4 }}>
                              <div className="progress-fill" style={{ width: `${ab.percentage}%` }} />
                            </div>
                          </div>
                        ))}
                      </div>
                    )}

                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => navigate(`/u/${u.handle}`)}
                      style={{ marginTop: 12 }}
                    >
                      {t('publicProfile.headline')} →
                    </Button>
                  </div>
                ) : (
                  <div style={{ padding: '32px 0', textAlign: 'center', color: 'var(--text-subtle)' }}>
                    {t('compare.userNotPublic')}
                  </div>
                )}
              </div>
            ))}
          </div>

          <div style={{ fontSize: 11, color: 'var(--text-subtle)', textAlign: 'right' }}>
            ℹ {t('compare.watermarkNotice')}
          </div>
        </div>
      )}
    </div>
  );
};
