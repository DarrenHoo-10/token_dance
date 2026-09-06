import { useEffect, useRef, useState } from 'react';
import { ApiError } from '@/api/client';
import { teamsApi, type TeamAnalysisReady, type TeamAnalysisResponse } from '@/api/teams';

export function useTeamAnalysis(input: {
  teamId?: string;
  authRevision: string | null;
  range: string;
  from?: string;
  to?: string;
  agent?: string;
  provider?: string;
  model?: string;
  enabled?: boolean;
}) {
  const { teamId, authRevision, range, from, to, agent, provider, model, enabled = true } = input;
  const [analysis, setAnalysis] = useState<TeamAnalysisReady | null>(null);
  const [updating, setUpdating] = useState(false);
  const [updatingMessageKey, setUpdatingMessageKey] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);
  const seqRef = useRef(0);
  const shownAuthRef = useRef<string | null>(null);
  const teamRef = useRef<string | undefined>(teamId);

  useEffect(() => {
    if (teamRef.current !== teamId) {
      teamRef.current = teamId;
      setAnalysis(null);
      shownAuthRef.current = null;
    }
    if (authRevision && shownAuthRef.current && shownAuthRef.current !== authRevision) {
      setAnalysis(null);
      shownAuthRef.current = authRevision;
    }
  }, [authRevision, teamId]);

  useEffect(() => {
    if (!teamId || !enabled) return undefined;
    if (range === 'custom' && (!from || !to)) return undefined;

    const seq = seqRef.current + 1;
    seqRef.current = seq;
    const controller = new AbortController();
    let timer: number | undefined;

    const load = async () => {
      if (seq !== seqRef.current) return;
      try {
        setError(null);
        const result: TeamAnalysisResponse = await teamsApi.getAnalysis(
          teamId,
          { range, from, to, agent, provider, model },
          controller.signal
        );
        if (seq !== seqRef.current) return;

        if (result.state === 'updating') {
          if (shownAuthRef.current && result.authRevision !== shownAuthRef.current) {
            setAnalysis(null);
          }
          shownAuthRef.current = result.authRevision;
          setUpdating(true);
          setUpdatingMessageKey(result.messageKey || 'teams.analytics.updating');
          timer = window.setTimeout(load, result.retryAfterMs || 2000);
          return;
        }

        if (authRevision && result.snapshot.authRevision !== authRevision) {
          setAnalysis(null);
          shownAuthRef.current = authRevision;
          setUpdating(true);
          setUpdatingMessageKey('teams.analytics.authChanged');
          timer = window.setTimeout(load, 2000);
          return;
        }

        shownAuthRef.current = result.snapshot.authRevision;
        setAnalysis(result);
        setUpdating(false);
        setUpdatingMessageKey(result.snapshot.refreshing ? 'teams.analytics.refreshing' : null);
        timer = window.setTimeout(load, result.snapshot.refreshing ? 2000 : 15000);
      } catch (err) {
        if (controller.signal.aborted || seq !== seqRef.current) return;
        if (err instanceof ApiError && err.code === 'TEAM_SNAPSHOT_OBSOLETE') {
          setAnalysis(null);
          setUpdating(true);
          setUpdatingMessageKey('teams.analytics.authChanged');
          timer = window.setTimeout(load, 1200);
          return;
        }
        setUpdating(false);
        setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
      }
    };

    void load();
    return () => {
      seqRef.current += 1;
      controller.abort();
      if (timer) window.clearTimeout(timer);
    };
  }, [agent, authRevision, enabled, from, model, provider, range, teamId, to]);

  return { analysis, updating, updatingMessageKey, error };
}
