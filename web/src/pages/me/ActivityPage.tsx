import React, { useState, useEffect, useCallback } from 'react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { EmptyState } from '@/components/states/EmptyState';
import { UnauthorizedState } from '@/components/states/UnauthorizedState';
import { Badge } from '@/components/common/Badge';
import { api, ApiError } from '@/api/client';
import type { ActivityRow } from '@/types/api';

export const ActivityPage: React.FC = () => {
  const { authenticated, loading: authLoading } = useAuth();
  const { t } = useLocale();

  const [range, setRange] = useState('30d');
  const [agent, setAgent] = useState('all');
  const [rows, setRows] = useState<ActivityRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const fetchActivity = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const res = await api.getActivityRows({
        range,
        agent: agent !== 'all' ? agent : undefined,
        limit: 50,
      });
      setRows(res.rows || []);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, [range, agent]);

  useEffect(() => {
    if (authenticated) {
      fetchActivity();
    }
  }, [authenticated, fetchActivity]);

  if (authLoading) return <LoadingState />;
  if (!authenticated) return <UnauthorizedState />;

  return (
    <div>
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
          <p className="eyebrow">{t('nav.tokenBoard')}</p>
          <h1>{t('dashboard.viewAllActivity')}</h1>
          <p className="text-muted" style={{ fontSize: 13 }}>
            {t('auth.privacyPledgeDesc')}
          </p>
        </div>

        <div style={{ display: 'flex', gap: 12 }}>
          <select
            value={agent}
            onChange={(e) => setAgent(e.target.value)}
            className="form-input"
            style={{ height: 36, fontSize: 12, padding: '0 10px' }}
          >
            <option value="all">{t('dashboard.allAgents')}</option>
            <option value="codex">Codex</option>
            <option value="claude-code">Claude Code</option>
            <option value="cursor">Cursor</option>
          </select>

          <select
            value={range}
            onChange={(e) => setRange(e.target.value)}
            className="form-input"
            style={{ height: 36, fontSize: 12, padding: '0 10px' }}
          >
            <option value="today">{t('common.today')}</option>
            <option value="7d">{t('common.days7')}</option>
            <option value="30d">{t('common.days30')}</option>
            <option value="all">{t('common.allTime')}</option>
          </select>
        </div>
      </div>

      {loading ? (
        <LoadingState />
      ) : error ? (
        <ErrorState error={error} onRetry={fetchActivity} />
      ) : rows.length === 0 ? (
        <EmptyState
          title={t('states.emptyTitle')}
          description={t('dashboard.noActivityYet')}
        />
      ) : (
        <div className="panel" style={{ padding: 0, overflow: 'hidden' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', textAlign: 'left', fontSize: 12 }}>
            <thead>
              <tr style={{ backgroundColor: 'var(--bg-subtle)', borderBottom: '1px solid var(--border-light)' }}>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Timestamp</th>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Agent</th>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Model</th>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Total Tokens</th>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Input / Output</th>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Device</th>
                <th style={{ padding: '12px 16px', fontWeight: 700 }}>Status</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr
                  key={i}
                  style={{
                    borderBottom: '1px solid var(--border-light)',
                    backgroundColor: i % 2 === 0 ? 'var(--bg-surface)' : 'var(--bg-subtle)',
                  }}
                >
                  <td style={{ padding: '12px 16px', fontFamily: 'var(--font-mono)' }}>
                    {new Date(r.occurredAt).toLocaleString()}
                  </td>
                  <td style={{ padding: '12px 16px', fontWeight: 600 }}>{r.agentId}</td>
                  <td style={{ padding: '12px 16px', color: 'var(--text-muted)' }}>{r.modelId}</td>
                  <td style={{ padding: '12px 16px', fontWeight: 700 }} className="mono-num">
                    {parseInt(r.tokenTotal, 10).toLocaleString()}
                  </td>
                  <td style={{ padding: '12px 16px', color: 'var(--text-muted)' }} className="mono-num">
                    {r.inputTokens} / {r.outputTokens}
                  </td>
                  <td style={{ padding: '12px 16px' }}>{r.deviceName}</td>
                  <td style={{ padding: '12px 16px' }}>
                    <Badge variant={r.syncStatus === 'normal' ? 'good' : 'warning'}>
                      {r.syncStatus}
                    </Badge>
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
