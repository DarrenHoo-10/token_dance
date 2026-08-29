import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { EmptyState } from '@/components/states/EmptyState';
import { api, ApiError } from '@/api/client';
import type { LeaderboardResponse } from '@/types/api';

export const LeaderboardPage: React.FC = () => {
  const { t } = useLocale();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  const windowParam = searchParams.get('window') || '30d';
  const metricParam = searchParams.get('metric') || 'tokens';
  const agentParam = searchParams.get('agent') || 'all';
  const qParam = searchParams.get('q') || '';

  const [leaderboard, setLeaderboard] = useState<LeaderboardResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const fetchLeaderboard = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const res = await api.getLeaderboard({
        window: windowParam,
        metric: metricParam,
        agent: agentParam !== 'all' ? agentParam : undefined,
        q: qParam || undefined,
        limit: 50,
      });

      setLeaderboard(res);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, [windowParam, metricParam, agentParam, qParam]);

  useEffect(() => {
    fetchLeaderboard();
  }, [fetchLeaderboard]);

  const updateFilters = (key: string, value: string) => {
    const params = new URLSearchParams(searchParams);
    if (value && value !== 'all') {
      params.set(key, value);
    } else {
      params.delete(key);
    }
    setSearchParams(params);
  };

  return (
    <div>
      {/* Header */}
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'flex-end',
          marginBottom: 24,
          flexWrap: 'wrap',
          gap: 16,
        }}
      >
        <div>
          <p className="eyebrow">{t('nav.leaderboard')}</p>
          <h1>{t('leaderboard.headline')}</h1>
          <p className="text-muted" style={{ fontSize: 13 }}>
            {t('leaderboard.subheadline')}
          </p>
        </div>

        {/* Windows */}
        <div className="segmented-control">
          {[
            { key: 'today', label: t('leaderboard.windowToday') },
            { key: '7d', label: t('leaderboard.window7d') },
            { key: '30d', label: t('leaderboard.window30d') },
            { key: 'all', label: t('leaderboard.windowAll') },
          ].map((w) => (
            <button
              key={w.key}
              type="button"
              className={`segmented-item ${windowParam === w.key ? 'active' : ''}`}
              onClick={() => updateFilters('window', w.key)}
            >
              {w.label}
            </button>
          ))}
        </div>
      </div>

      {/* Filter Row */}
      <div
        style={{
          display: 'flex',
          gap: 12,
          marginBottom: 20,
          flexWrap: 'wrap',
          alignItems: 'center',
        }}
      >
        <select
          value={metricParam}
          onChange={(e) => updateFilters('metric', e.target.value)}
          className="form-input"
          style={{ height: 38, fontSize: 12, padding: '0 12px' }}
        >
          <option value="tokens">{t('leaderboard.metricTokens')}</option>
          <option value="code_lines">{t('leaderboard.metricCodeLines')}</option>
          <option value="sessions">{t('leaderboard.metricSessions')}</option>
          <option value="turns">{t('leaderboard.metricTurns')}</option>
          <option value="skills">{t('leaderboard.metricSkills')}</option>
        </select>

        <select
          value={agentParam}
          onChange={(e) => updateFilters('agent', e.target.value)}
          className="form-input"
          style={{ height: 38, fontSize: 12, padding: '0 12px' }}
        >
          <option value="all">{t('dashboard.allAgents')}</option>
          <option value="codex">Codex</option>
          <option value="claude-code">Claude Code</option>
          <option value="cursor">Cursor</option>
        </select>

        <input
          type="text"
          placeholder={t('common.searchPlaceholder')}
          value={qParam}
          onChange={(e) => updateFilters('q', e.target.value)}
          className="form-input"
          style={{ height: 38, fontSize: 12, width: 220 }}
        />
      </div>

      {/* Table */}
      {loading ? (
        <LoadingState />
      ) : error ? (
        <ErrorState error={error} onRetry={fetchLeaderboard} />
      ) : !leaderboard?.entries || leaderboard.entries.length === 0 ? (
        <EmptyState title={t('states.emptyTitle')} description={t('states.emptyDesc')} />
      ) : (
        <div className="panel" style={{ padding: 0, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: 13 }}>
            <thead>
              <tr style={{ backgroundColor: 'var(--bg-subtle)', borderBottom: '1px solid var(--border-light)' }}>
                <th style={{ padding: '14px 20px', width: 80 }}>{t('leaderboard.rank')}</th>
                <th style={{ padding: '14px 20px' }}>{t('leaderboard.user')}</th>
                <th style={{ padding: '14px 20px', textAlign: 'right' }}>{t('leaderboard.metricValue')}</th>
                <th style={{ padding: '14px 20px' }}>{t('leaderboard.topAgent')}</th>
                <th style={{ padding: '14px 20px', textAlign: 'right' }}>{t('leaderboard.activeDays')}</th>
              </tr>
            </thead>
            <tbody>
              {leaderboard.entries.map((entry) => (
                <tr
                  key={entry.handle}
                  style={{
                    borderBottom: '1px solid var(--border-light)',
                    cursor: 'pointer',
                    transition: 'background-color 0.15s',
                  }}
                  onClick={() => navigate(`/u/${entry.handle}`)}
                  className="table-hover-row"
                >
                  <td style={{ padding: '14px 20px', fontWeight: 800 }} className="mono-num">
                    #{entry.rankNo}
                  </td>
                  <td style={{ padding: '14px 20px' }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      <div className="avatar" style={{ width: 34, height: 34 }}>
                        {entry.displayName.substring(0, 2).toUpperCase()}
                      </div>
                      <div>
                        <strong>{entry.displayName}</strong>
                        <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>@{entry.handle}</div>
                      </div>
                    </div>
                  </td>
                  <td style={{ padding: '14px 20px', textAlign: 'right', fontWeight: 800 }} className="mono-num">
                    {entry.formattedMetric}
                  </td>
                  <td style={{ padding: '14px 20px', color: 'var(--text-muted)' }}>
                    {entry.topAgent || '—'}
                  </td>
                  <td style={{ padding: '14px 20px', textAlign: 'right' }} className="mono-num">
                    {entry.activeDays || '—'} {t('metrics.days')}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};
