import React, { useState, useEffect, useCallback } from 'react';
import { useSearchParams, useNavigate } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { EmptyState } from '@/components/states/EmptyState';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { api, ApiError } from '@/api/client';
import type { CompareResponse } from '@/types/api';

function formatNumber(val: string | null | undefined): string {
  if (!val) return '—';
  const num = parseFloat(val);
  if (isNaN(num)) return val;
  if (num >= 1_000_000_000) return (num / 1_000_000_000).toFixed(1) + 'B';
  if (num >= 1_000_000) return (num / 1_000_000).toFixed(1) + 'M';
  if (num >= 1_000) return (num / 1_000).toFixed(1) + 'K';
  return num.toLocaleString();
}

export const ComparePage: React.FC = () => {
  const { t } = useLocale();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const handlesParam = searchParams.get('handles') || '';
  const handles = handlesParam.split(',').map((h) => h.trim()).filter(Boolean);

  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<ApiError | Error | null>(null);
  const [comparison, setComparison] = useState<CompareResponse | null>(null);

  const fetchComparison = useCallback(async () => {
    if (handles.length === 0) {
      setLoading(false);
      setComparison(null);
      return;
    }
    try {
      setLoading(true);
      setError(null);
      const res = await api.compareUsers(handles, '30d', 'tokens');
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
        description={t('explore.subheadline')}
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
            {comparison?.users.map((u) => {
              const cleanHandle = u.handle.replace(/^@/, '');
              const displayName = u.displayName || `@${cleanHandle}`;
              const initials = (u.displayName || cleanHandle).substring(0, 2).toUpperCase();

              return (
                <div key={u.handle} className="panel">
                  {/* User Header */}
                  <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginBottom: 20 }}>
                    <div className="avatar" style={{ width: 48, height: 48, fontSize: 16 }}>
                      {u.avatarUrl ? (
                        <img src={u.avatarUrl} alt={displayName} style={{ width: '100%', height: '100%', borderRadius: '50%' }} />
                      ) : (
                        initials
                      )}
                    </div>
                    <div>
                      <h2 style={{ fontSize: 18, margin: 0 }}>{displayName}</h2>
                      {u.displayName && (
                        <span className="text-muted" style={{ fontSize: 12 }}>
                          @{cleanHandle}
                        </span>
                      )}
                    </div>
                  </div>

                  {u.visible ? (
                    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
                      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
                        <div className="metric-card">
                          <small className="text-muted">{t('metrics.globalRank')}</small>
                          <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                            {u.rank ? `#${u.rank}` : '—'}
                          </strong>
                        </div>
                        <div className="metric-card">
                          <small className="text-muted">{t('metrics.totalTokens')}</small>
                          <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                            {formatNumber(u.tokenTotal)}
                          </strong>
                        </div>
                        <div className="metric-card">
                          <small className="text-muted">{t('metrics.generatedCodeLines')}</small>
                          <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                            {formatNumber(u.codeLinesTotal)}
                          </strong>
                        </div>
                        <div className="metric-card">
                          <small className="text-muted">{t('metrics.activeStreak')}</small>
                          <strong className="mono-num" style={{ fontSize: 20, marginTop: 4 }}>
                            {u.currentStreak ?? '—'} {t('metrics.days')}
                          </strong>
                        </div>
                      </div>

                      {/* Breakdown */}
                      {u.agentBreakdown && u.agentBreakdown.length > 0 && (
                        <div style={{ marginTop: 12 }}>
                          <h3 style={{ fontSize: 13, marginBottom: 12 }}>{t('compare.agentComparison')}</h3>
                          {u.agentBreakdown.map((ab) => (
                            <div key={ab.agentId || ab.key} style={{ marginBottom: 10 }}>
                              <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12 }}>
                                <span>{ab.displayName || ab.label}</span>
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
                        onClick={() => navigate(`/u/${cleanHandle}`)}
                        style={{ marginTop: 12 }}
                      >
                        {t('publicProfile.headline')} →
                      </Button>
                    </div>
                  ) : (
                    <div style={{ padding: '32px 16px', textAlign: 'center', color: 'var(--text-subtle)' }}>
                      <div style={{ marginBottom: 12 }}>
                        <Badge variant="default">{t('common.private')}</Badge>
                      </div>
                      <p style={{ margin: 0, fontSize: 13 }}>{t('compare.userNotPublic')}</p>
                    </div>
                  )}
                </div>
              );
            })}
          </div>

          <div style={{ fontSize: 11, color: 'var(--text-subtle)', textAlign: 'right' }}>
            ℹ {t('compare.watermarkNotice')}
          </div>
        </div>
      )}
    </div>
  );
};
