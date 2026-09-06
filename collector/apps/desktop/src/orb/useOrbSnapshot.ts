import { useCallback, useEffect, useRef, useState } from 'react';
import { mixFreshVisual, quotaVisual, type QuotaVisual } from './color';
import {
  getOrbDetails,
  getOrbPreferences,
  getOrbRenderSnapshot,
  getOrbSnapshot,
  listenOrbPreferences,
  listenOrbRender,
  listenOrbSnapshot,
} from './bridge';
import {
  classifyOrbEvent,
  estimatedNowMs,
  isCollectorPaused,
  isNewerRevision,
  QUOTA_TRANSITION_MS,
  resolveQuotaState,
  resolveRemainingPercent,
  type OrbDetailsSnapshot,
  type OrbPreferences,
  type OrbRenderSnapshot,
  type OrbSnapshot,
  type QuotaState,
} from './types';

type StreamGate = { id: string | null; revision: string | null };

function applyCommand<T extends { streamId: string; revision: string }>(gate: StreamGate, snapshot: T, buffered: T[]): T {
  gate.id = snapshot.streamId;
  let latest = snapshot;
  for (const item of buffered) {
    if (item.streamId === gate.id && isNewerRevision(latest.revision, item.revision)) latest = item;
  }
  buffered.length = 0;
  gate.revision = latest.revision;
  return latest;
}

function handleEvent<T extends { streamId: string; revision: string }>(
  gate: StreamGate,
  incoming: T,
  buffered: T[],
  resync: () => void,
): T | null {
  if (gate.id == null) {
    buffered.push(incoming);
    return null;
  }
  const decision = classifyOrbEvent(gate.id, gate.revision, incoming.streamId, incoming.revision);
  if (decision === 'resync') {
    resync();
    return null;
  }
  if (decision === 'drop') return null;
  gate.revision = incoming.revision;
  return incoming;
}

function useNowClock(emittedAtMs: number | null, receivedPerf: number | null, staleAtMs: number | null, state: QuotaState | null): number {
  const [, setTick] = useState(0);
  useEffect(() => {
    if (emittedAtMs == null || receivedPerf == null || state !== 'fresh' || staleAtMs == null) return;
    const delay = staleAtMs - estimatedNowMs(emittedAtMs, receivedPerf);
    if (delay <= 0) {
      setTick(n => n + 1);
      return;
    }
    const timer = window.setTimeout(() => setTick(n => n + 1), delay);
    return () => clearTimeout(timer);
  }, [emittedAtMs, receivedPerf, staleAtMs, state]);
  if (emittedAtMs == null || receivedPerf == null) return Date.now();
  return estimatedNowMs(emittedAtMs, receivedPerf);
}

export function useOrbLanguage(): boolean {
  const [zh, setZh] = useState(() => localStorage.getItem('tokendance.language') !== 'en');
  useEffect(() => {
    const sync = () => setZh(localStorage.getItem('tokendance.language') !== 'en');
    window.addEventListener('storage', sync);
    window.addEventListener('focus', sync);
    return () => {
      window.removeEventListener('storage', sync);
      window.removeEventListener('focus', sync);
    };
  }, []);
  return zh;
}

export function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(() => typeof window !== 'undefined' && window.matchMedia('(prefers-reduced-motion: reduce)').matches);
  useEffect(() => {
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)');
    const onChange = () => setReduced(mq.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);
  return reduced;
}

export function useAnimatedQuotaVisual(
  remaining: number | null,
  state: QuotaState,
  snap: boolean,
  freeze: boolean,
): QuotaVisual {
  const target = quotaVisual(remaining, state);
  const [display, setDisplay] = useState(target);
  const displayRef = useRef(target);

  useEffect(() => {
    if (snap || freeze || state !== 'fresh') {
      displayRef.current = target;
      setDisplay(target);
      return;
    }
    const from = displayRef.current;
    if (from.remaining === target.remaining && from.color === target.color) return;
    const fromFresh = from.color === quotaVisual(from.remaining, 'fresh').color;
    if (!fromFresh) {
      displayRef.current = target;
      setDisplay(target);
      return;
    }
    const origin = from.remaining;
    const start = performance.now();
    let frame = 0;
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / QUOTA_TRANSITION_MS);
      const eased = 1 - (1 - t) ** 2;
      const next = mixFreshVisual(origin, target.remaining, eased);
      displayRef.current = next;
      setDisplay(next);
      if (t < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [remaining, state, snap, freeze, target.color, target.arcDegrees, target.remaining]);

  return snap || freeze || state !== 'fresh' ? target : display;
}

export function useOrbSnapshot(): {
  snapshot: OrbSnapshot | null;
  error: boolean;
  nowMs: number;
  quotaState: QuotaState;
  remainingPercent: number | null;
  paused: boolean;
  hidden: boolean;
  refetch: () => void;
} {
  const [snapshot, setSnapshot] = useState<OrbSnapshot | null>(null);
  const [error, setError] = useState(false);
  const [receivedPerf, setReceivedPerf] = useState<number | null>(null);
  const gate = useRef<StreamGate>({ id: null, revision: null });
  const buffered = useRef<OrbSnapshot[]>([]);
  const refetch = useCallback(() => {
    void getOrbSnapshot().then(next => {
      const applied = applyCommand(gate.current, next, buffered.current);
      setSnapshot(applied);
      setReceivedPerf(performance.now());
      setError(false);
    }).catch(() => setError(true));
  }, []);

  useEffect(() => {
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    const resync = () => { if (!cancelled) refetch(); };
    const onEvent = (incoming: OrbSnapshot) => {
      const accepted = handleEvent(gate.current, incoming, buffered.current, resync);
      if (cancelled || !accepted) return;
      setSnapshot(accepted);
      setReceivedPerf(performance.now());
      setError(false);
    };
    void (async () => {
      unlisten = await listenOrbSnapshot(onEvent);
      if (cancelled) {
        unlisten();
        return;
      }
      try {
        const next = await getOrbSnapshot();
        if (cancelled) return;
        setSnapshot(applyCommand(gate.current, next, buffered.current));
        setReceivedPerf(performance.now());
        setError(false);
      } catch {
        if (!cancelled) setError(true);
      }
    })();
    const onWake = () => { if (document.visibilityState === 'visible') refetch(); };
    document.addEventListener('visibilitychange', onWake);
    window.addEventListener('pageshow', refetch);
    return () => {
      cancelled = true;
      unlisten?.();
      document.removeEventListener('visibilitychange', onWake);
      window.removeEventListener('pageshow', refetch);
    };
  }, [refetch]);

  const nowMs = useNowClock(snapshot?.emittedAtMs ?? null, receivedPerf, snapshot?.quota.staleAtMs ?? null, snapshot?.quota.state ?? null);
  const quotaState = snapshot ? resolveQuotaState(snapshot.quota, nowMs) : 'loading';
  const remainingPercent = snapshot ? resolveRemainingPercent(snapshot.quota, nowMs) : null;
  return {
    snapshot,
    error,
    nowMs,
    quotaState,
    remainingPercent,
    paused: isCollectorPaused(snapshot?.collector.state),
    hidden: snapshot?.hidden === true || (typeof document !== 'undefined' && document.hidden),
    refetch,
  };
}

export function useOrbRenderSnapshot(): {
  snapshot: OrbRenderSnapshot | null;
  error: boolean;
  nowMs: number;
  quotaState: QuotaState;
  remainingPercent: number | null;
  paused: boolean;
  hidden: boolean;
  refetch: () => void;
} {
  const [snapshot, setSnapshot] = useState<OrbRenderSnapshot | null>(null);
  const [error, setError] = useState(false);
  const [receivedPerf, setReceivedPerf] = useState<number | null>(null);
  const gate = useRef<StreamGate>({ id: null, revision: null });
  const buffered = useRef<OrbRenderSnapshot[]>([]);
  const refetch = useCallback(() => {
    void getOrbRenderSnapshot().then(next => {
      setSnapshot(applyCommand(gate.current, next, buffered.current));
      setReceivedPerf(performance.now());
      setError(false);
    }).catch(() => setError(true));
  }, []);

  useEffect(() => {
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    const resync = () => { if (!cancelled) refetch(); };
    const onEvent = (incoming: OrbRenderSnapshot) => {
      const accepted = handleEvent(gate.current, incoming, buffered.current, resync);
      if (cancelled || !accepted) return;
      setSnapshot(accepted);
      setReceivedPerf(performance.now());
      setError(false);
    };
    void (async () => {
      unlisten = await listenOrbRender(onEvent);
      if (cancelled) {
        unlisten();
        return;
      }
      try {
        const next = await getOrbRenderSnapshot();
        if (cancelled) return;
        setSnapshot(applyCommand(gate.current, next, buffered.current));
        setReceivedPerf(performance.now());
        setError(false);
      } catch {
        if (!cancelled) setError(true);
      }
    })();
    const onWake = () => { if (document.visibilityState === 'visible') refetch(); };
    document.addEventListener('visibilitychange', onWake);
    window.addEventListener('pageshow', refetch);
    return () => {
      cancelled = true;
      unlisten?.();
      document.removeEventListener('visibilitychange', onWake);
      window.removeEventListener('pageshow', refetch);
    };
  }, [refetch]);

  const nowMs = useNowClock(snapshot?.emittedAtMs ?? null, receivedPerf, snapshot?.quota.staleAtMs ?? null, snapshot?.quota.state ?? null);
  const quotaState = snapshot ? resolveQuotaState(snapshot.quota, nowMs) : 'loading';
  const remainingPercent = snapshot ? resolveRemainingPercent(snapshot.quota, nowMs) : null;
  return {
    snapshot,
    error,
    nowMs,
    quotaState,
    remainingPercent,
    paused: snapshot?.collectorPaused === true,
    hidden: snapshot?.hidden === true || (typeof document !== 'undefined' && document.hidden),
    refetch,
  };
}

export function useOrbDetailsSnapshot(): {
  snapshot: OrbDetailsSnapshot | null;
  error: boolean;
  nowMs: number;
  quotaState: QuotaState;
  refetch: () => void;
} {
  const [snapshot, setSnapshot] = useState<OrbDetailsSnapshot | null>(null);
  const [error, setError] = useState(false);
  const [receivedPerf, setReceivedPerf] = useState<number | null>(null);
  const gate = useRef<StreamGate>({ id: null, revision: null });
  const buffered = useRef<OrbDetailsSnapshot[]>([]);
  const refetch = useCallback(() => {
    void getOrbDetails().then(next => {
      setSnapshot(applyCommand(gate.current, next, buffered.current));
      setReceivedPerf(performance.now());
      setError(false);
    }).catch(() => setError(true));
  }, []);

  useEffect(() => {
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    const resync = () => { if (!cancelled) refetch(); };
    const onEvent = (incoming: OrbSnapshot) => {
      if (gate.current.id != null && incoming.streamId !== gate.current.id) {
        resync();
        return;
      }
      if (gate.current.id != null && !isNewerRevision(gate.current.revision, incoming.revision)) return;
      if (!cancelled) refetch();
    };
    void (async () => {
      unlisten = await listenOrbSnapshot(onEvent);
      if (cancelled) {
        unlisten();
        return;
      }
      try {
        const next = await getOrbDetails();
        if (cancelled) return;
        setSnapshot(applyCommand(gate.current, next, buffered.current));
        setReceivedPerf(performance.now());
        setError(false);
      } catch {
        if (!cancelled) setError(true);
      }
    })();
    const onWake = () => { if (document.visibilityState === 'visible') refetch(); };
    document.addEventListener('visibilitychange', onWake);
    window.addEventListener('pageshow', refetch);
    return () => {
      cancelled = true;
      unlisten?.();
      document.removeEventListener('visibilitychange', onWake);
      window.removeEventListener('pageshow', refetch);
    };
  }, [refetch]);

  const nowMs = useNowClock(snapshot?.emittedAtMs ?? null, receivedPerf, snapshot?.quota.staleAtMs ?? null, snapshot?.quota.state ?? null);
  return {
    snapshot,
    error,
    nowMs,
    quotaState: snapshot ? resolveQuotaState(snapshot.quota, nowMs) : 'loading',
    refetch,
  };
}

export function useOrbPreferences(): {
  preferences: OrbPreferences | null;
  error: boolean;
  refetch: () => void;
} {
  const [preferences, setPreferences] = useState<OrbPreferences | null>(null);
  const [error, setError] = useState(false);
  const refetch = useCallback(() => {
    void getOrbPreferences().then(next => {
      setPreferences(next);
      setError(false);
    }).catch(() => setError(true));
  }, []);

  useEffect(() => {
    let cancelled = false;
    let unlisten: (() => void) | undefined;
    void (async () => {
      unlisten = await listenOrbPreferences(next => {
        if (!cancelled) {
          setPreferences(next);
          setError(false);
        }
      });
      if (cancelled) {
        unlisten();
        return;
      }
      try {
        const next = await getOrbPreferences();
        if (!cancelled) {
          setPreferences(next);
          setError(false);
        }
      } catch {
        if (!cancelled) setError(true);
      }
    })();
    return () => {
      cancelled = true;
      unlisten?.();
    };
  }, []);

  return { preferences, error, refetch };
}
