import React, { useState, useEffect, useCallback } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { EmptyState } from '@/components/states/EmptyState';
import { CompareTray } from '@/components/compare/CompareTray';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { api, ApiError } from '@/api/client';
import type { SearchResponse } from '@/types/api';

export const ExplorePage: React.FC = () => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();

  const initialQuery = searchParams.get('q') || '';
  const [query, setQuery] = useState(initialQuery);
  const [activeTab, setActiveTab] = useState<'all' | 'users' | 'agents' | 'skills'>('all');
  const [compareHandles, setCompareHandles] = useState<string[]>([]);

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);
  const [results, setResults] = useState<SearchResponse | null>(null);

  const performSearch = useCallback(async (q: string) => {
    try {
      setLoading(true);
      setError(null);
      const res = await api.searchPublic(q);
      setResults(res);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    performSearch(initialQuery);
  }, [initialQuery, performSearch]);

  const handleSearchSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (query.trim()) {
      setSearchParams({ q: query.trim() });
      performSearch(query.trim());
    }
  };

  const handleToggleCompare = (handle: string) => {
    if (compareHandles.includes(handle)) {
      setCompareHandles((prev) => prev.filter((h) => h !== handle));
    } else {
      if (compareHandles.length >= 3) {
        showToast(t('compare.selectUpTo3'), 'info');
        return;
      }
      setCompareHandles((prev) => [...prev, handle]);
      showToast(`Added @${handle} to comparison`, 'info');
    }
  };

  return (
    <div>
      {/* Header */}
      <div style={{ marginBottom: 24 }}>
        <p className="eyebrow">{t('nav.explore')}</p>
        <h1>{t('explore.headline')}</h1>
        <p className="text-muted" style={{ fontSize: 13 }}>
          {t('explore.subheadline')}
        </p>
      </div>

      {/* Main Search Input */}
      <form onSubmit={handleSearchSubmit} style={{ marginBottom: 20 }}>
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            height: 56,
            padding: '0 20px',
            backgroundColor: 'var(--bg-surface)',
            border: '2px solid var(--text-main)',
            borderRadius: 'var(--radius-lg)',
            boxShadow: 'var(--shadow-card)',
            gap: 12,
          }}
        >
          <span style={{ fontSize: 20 }}>🔍</span>
          <input
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t('explore.searchPlaceholder')}
            style={{
              flex: 1,
              border: 'none',
              outline: 'none',
              fontSize: 16,
              fontWeight: 600,
              color: 'var(--text-main)',
            }}
          />
          <Button type="submit" variant="dark" size="sm">
            {t('common.search')}
          </Button>
        </div>
      </form>

      {/* Filter Tabs */}
      <div style={{ display: 'flex', gap: 8, marginBottom: 24, flexWrap: 'wrap' }}>
        {[
          { key: 'all', label: t('explore.allTab') },
          { key: 'users', label: `${t('explore.usersTab')} ${results?.users?.length || ''}` },
          { key: 'agents', label: `${t('explore.agentsTab')} ${results?.agents?.length || ''}` },
          { key: 'skills', label: `${t('explore.skillsTab')} ${results?.skills?.length || ''}` },
        ].map((tab) => (
          <button
            key={tab.key}
            type="button"
            className={`btn ${activeTab === tab.key ? 'btn-dark' : 'btn-ghost'}`}
            style={{ height: 32, fontSize: 12, borderRadius: 8 }}
            onClick={() => setActiveTab(tab.key as typeof activeTab)}
          >
            {tab.label}
          </button>
        ))}
      </div>

      {loading ? (
        <LoadingState />
      ) : error ? (
        <ErrorState error={error} onRetry={() => performSearch(query)} />
      ) : (
        <div className="grid-2">
          {/* Users column */}
          {(activeTab === 'all' || activeTab === 'users') && (
            <div className="panel">
              <div className="panel-header">
                <div>
                  <h2>{t('explore.usersSection')}</h2>
                  <p className="text-muted" style={{ fontSize: 12 }}>
                    {t('explore.usersSectionSub')}
                  </p>
                </div>
                <span className="text-muted" style={{ fontSize: 12 }}>
                  {results?.users?.length || 0} {t('explore.resultsCount')}
                </span>
              </div>

              {results?.users && results.users.length > 0 ? (
                <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                  {results.users.map((u) => {
                    const isCompared = compareHandles.includes(u.handle);
                    return (
                      <div
                        key={u.handle}
                        style={{
                          display: 'flex',
                          justifyContent: 'space-between',
                          alignItems: 'center',
                          padding: '12px 14px',
                          borderBottom: '1px solid var(--border-light)',
                        }}
                      >
                        <div
                          onClick={() => navigate(`/u/${u.handle}`)}
                          style={{ display: 'flex', alignItems: 'center', gap: 12, cursor: 'pointer' }}
                        >
                          <div className="avatar" style={{ width: 40, height: 40 }}>
                            {u.displayName.substring(0, 2).toUpperCase()}
                          </div>
                          <div>
                            <strong style={{ fontSize: 13 }}>{u.displayName}</strong>
                            <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                              @{u.handle} {u.topAgent && `· ${u.topAgent}`}
                            </div>
                          </div>
                        </div>

                        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
                          <div style={{ textAlign: 'right' }}>
                            <strong className="mono-num" style={{ fontSize: 13, display: 'block' }}>
                              {u.tokenTotal}
                            </strong>
                            <small className="mono-num" style={{ color: 'var(--text-muted)', fontSize: 10 }}>
                              #{u.rank || '—'} RANK
                            </small>
                          </div>

                          <Button
                            variant={isCompared ? 'dark' : 'outline'}
                            size="sm"
                            style={{ fontSize: 11, padding: '0 8px' }}
                            onClick={() => handleToggleCompare(u.handle)}
                          >
                            {isCompared ? '✓ Added' : '+ Compare'}
                          </Button>
                        </div>
                      </div>
                    );
                  })}
                </div>
              ) : (
                <EmptyState title={t('explore.noResultsFound')} />
              )}
            </div>
          )}

          {/* Agents & Skills Column */}
          {(activeTab === 'all' || activeTab === 'agents' || activeTab === 'skills') && (
            <div className="panel">
              <div className="panel-header">
                <div>
                  <h2>{t('explore.agentsSection')}</h2>
                  <p className="text-muted" style={{ fontSize: 12 }}>
                    {t('explore.agentsSectionSub')}
                  </p>
                </div>
              </div>

              {/* Agents */}
              {results?.agents?.map((agent) => (
                <div key={agent.agentId} style={{ padding: '12px 0', borderBottom: '1px solid var(--border-light)' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <h3 style={{ margin: 0 }}>{agent.displayName}</h3>
                    <Badge variant="lime">Agent</Badge>
                  </div>
                  <p style={{ fontSize: 12, color: 'var(--text-muted)', margin: '6px 0 10px' }}>
                    {agent.developerCount} developers produced {agent.tokenTotal30d} tokens in 30 days.
                  </p>
                  <div style={{ display: 'flex', gap: 6 }}>
                    {agent.tags?.map((tag) => (
                      <Badge key={tag}>#{tag}</Badge>
                    ))}
                  </div>
                </div>
              ))}

              {/* Skills */}
              {results?.skills?.map((skill) => (
                <div key={skill.skillPublicName} style={{ padding: '12px 0', borderBottom: '1px solid var(--border-light)' }}>
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <h3 style={{ margin: 0 }}>{skill.skillPublicName}</h3>
                    <Badge>Skill</Badge>
                  </div>
                  <p style={{ fontSize: 12, color: 'var(--text-muted)', margin: '6px 0 10px' }}>
                    Used {skill.useCount.toLocaleString()} times by public developers (+{skill.growthDelta}%).
                  </p>
                  <div style={{ display: 'flex', gap: 6 }}>
                    {skill.tags?.map((tag) => (
                      <Badge key={tag} variant="lime">
                        {tag}
                      </Badge>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* Floating compare tray */}
      <CompareTray
        handles={compareHandles}
        onRemove={(h) => setCompareHandles((prev) => prev.filter((item) => item !== h))}
        onClear={() => setCompareHandles([])}
      />
    </div>
  );
};
