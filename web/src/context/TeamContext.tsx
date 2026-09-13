import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { ApiError } from '@/api/client';
import { isTeamScope, teamsApi, type TeamScope } from '@/api/teams';
import { useAuth } from '@/context/AuthContext';
import { clearAllJoinTokens, clearCreateDraft } from '@/pages/teams/teamUtils';

interface TeamContextValue {
  scope: TeamScope | null;
  loading: boolean;
  error: ApiError | null;
  authRevision: string | null;
  refresh: (signal?: AbortSignal, options?: { silent?: boolean }) => Promise<TeamScope | null>;
  applyScope: (next: TeamScope | null) => void;
  clearScope: () => void;
}

const TeamContext = createContext<TeamContextValue | undefined>(undefined);

export const TeamProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const { authenticated, user } = useAuth();
  const [scope, setScope] = useState<TeamScope | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);
  const requestSeq = useRef(0);
  const scopeRef = useRef<TeamScope | null>(null);
  const userId = user?.userId || user?.handle || null;

  const applyScope = useCallback((next: TeamScope | null) => {
    scopeRef.current = next;
    setScope(next);
    setError(null);
  }, []);

  const clearScope = useCallback(() => {
    scopeRef.current = null;
    setScope(null);
    setError(null);
  }, []);

  const refresh = useCallback(
    async (signal?: AbortSignal, options?: { silent?: boolean }) => {
      if (!authenticated) {
        scopeRef.current = null;
        setScope(null);
        setError(null);
        setLoading(false);
        return null;
      }

      const seq = requestSeq.current + 1;
      requestSeq.current = seq;
      if (!options?.silent) setLoading(true);
      setError(null);

      try {
        const result = await teamsApi.getMyTeam(signal);
        if (seq !== requestSeq.current) return scopeRef.current;
        const next = isTeamScope(result) ? result : null;
        scopeRef.current = next;
        setScope(next);
        setLoading(false);
        return next;
      } catch (err) {
        if (signal?.aborted) return scopeRef.current;
        if (seq !== requestSeq.current) return scopeRef.current;
        if (err instanceof ApiError && err.status === 401) {
          scopeRef.current = null;
          setScope(null);
          setError(null);
          setLoading(false);
          return null;
        }
        setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
        setLoading(false);
        return scopeRef.current;
      }
    },
    [authenticated]
  );

  useEffect(() => {
    if (!authenticated) {
      scopeRef.current = null;
      setScope(null);
      setError(null);
      setLoading(false);
      clearCreateDraft();
      clearAllJoinTokens();
      return;
    }

    const controller = new AbortController();
    void refresh(controller.signal);
    return () => controller.abort();
  }, [authenticated, refresh, userId]);

  useEffect(() => {
    if (!authenticated) return undefined;

    const tick = () => {
      void refresh(undefined, { silent: true });
    };
    const interval = window.setInterval(tick, 15_000);
    window.addEventListener('focus', tick);
    return () => {
      window.clearInterval(interval);
      window.removeEventListener('focus', tick);
    };
  }, [authenticated, refresh]);

  const value = useMemo<TeamContextValue>(
    () => ({
      scope,
      loading,
      error,
      authRevision: scope?.team.authRevision ?? null,
      refresh,
      applyScope,
      clearScope,
    }),
    [applyScope, clearScope, error, loading, refresh, scope]
  );

  return <TeamContext.Provider value={value}>{children}</TeamContext.Provider>;
};

export function useTeam(): TeamContextValue {
  const context = useContext(TeamContext);
  if (!context) {
    throw new Error('useTeam must be used within a TeamProvider');
  }
  return context;
}
