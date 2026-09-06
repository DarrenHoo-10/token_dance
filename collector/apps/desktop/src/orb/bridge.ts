import { useEffect, useLayoutEffect, useRef } from 'react';
import { invoke } from '@tauri-apps/api/core';
import { listen, type UnlistenFn } from '@tauri-apps/api/event';
import { isTauriEnvironment } from '../tauri-bridge';
import { ORB_ROOT_CLASS, type OrbAction, type OrbDetailsSnapshot, type OrbPreferences, type OrbPreferencesPatch, type OrbRenderSnapshot, type OrbSnapshot } from './types';

export const ORB_SNAPSHOT_EVENT = 'orb://snapshot';
export const ORB_RENDER_EVENT = 'orb://render';
export const ORB_PREFERENCES_EVENT = 'orb://preferences';

function mockNow(): number {
  return Date.now();
}

function mockSnapshot(revision = '1'): OrbSnapshot {
  const now = mockNow();
  // Browser-only visual fixtures; native commands never use this path.
  const params = new URLSearchParams(window.location.search);
  const fixtureRemaining = Number(params.get('previewRemaining') ?? 72);
  const remaining = Number.isFinite(fixtureRemaining) ? Math.min(100, Math.max(0, fixtureRemaining)) : 72;
  const fixtureTokens = params.get('previewTokens') ?? '5100000';
  return {
    schemaVersion: 1,
    streamId: 'preview-stream',
    revision,
    emittedAtMs: now,
    preferencesRevision: '1',
    hidden: false,
    usage: {
      localDate: new Date(now).toISOString().slice(0, 10),
      state: 'known',
      todayTokens: /^\d{1,18}$/.test(fixtureTokens) ? fixtureTokens : '5100000',
      knownSourceCount: 3,
      hasUnmeasuredSources: false,
      capturedAtMs: now,
      lastRecordedChangeAtMs: now - 12_000,
    },
    collector: { state: 'running', syncState: 'SYNCED' },
    quota: {
      selection: { agentId: 'codex', windowId: 'codex:primary:300m' },
      agentName: 'Codex',
      windowLabel: '5 小时额度',
      state: params.get('previewState') === 'stale' ? 'stale' : 'fresh',
      remainingPercent: remaining,
      lastKnownRemainingPercent: remaining,
      observedAtMs: now - 60_000,
      resetsAtMs: now + 2 * 3_600_000 + 18 * 60_000,
      staleAtMs: now + 29 * 60_000,
      identityConfidence: 'unavailable',
    },
    effect: { mode: 'orbit', reducedMotion: false, pulse: null },
  };
}

function mockRenderSnapshot(): OrbRenderSnapshot {
  const snap = mockSnapshot();
  return {
    schemaVersion: 1,
    streamId: snap.streamId,
    revision: snap.revision,
    emittedAtMs: snap.emittedAtMs,
    diameterDip: 112,
    hidden: false,
    collectorPaused: false,
    quota: {
      state: snap.quota.state,
      remainingPercent: snap.quota.remainingPercent,
      staleAtMs: snap.quota.staleAtMs,
      selectionKey: 'codex\0codex:primary:300m',
    },
    effect: snap.effect,
  };
}

function mockDetailsSnapshot(): OrbDetailsSnapshot {
  const snap = mockSnapshot();
  return {
    schemaVersion: 1,
    streamId: snap.streamId,
    revision: snap.revision,
    emittedAtMs: snap.emittedAtMs,
    preferencesRevision: snap.preferencesRevision,
    hidden: false,
    usage: {
      ...snap.usage,
      sources: [
        { agentId: 'codex', agentName: 'Codex', todayTokens: '2100000' },
        { agentId: 'claude-code', agentName: 'Claude Code', todayTokens: '1800000' },
        { agentId: 'cursor', agentName: 'Cursor', todayTokens: '1200000' },
        { agentId: 'grok-build', agentName: 'Grok Build', todayTokens: null },
      ],
    },
    collector: snap.collector,
    quota: { ...snap.quota, identityNote: '来自最近本地日志' },
    options: [
      { agentId: 'codex', agentName: 'Codex', windowId: 'codex:primary:300m', windowLabel: '5 小时额度', state: 'fresh', remainingPercent: 72, lastKnownRemainingPercent: 72, observedAtMs: snap.quota.observedAtMs, resetsAtMs: snap.quota.resetsAtMs },
      { agentId: 'codex', agentName: 'Codex', windowId: 'codex:primary:10080m', windowLabel: '7 日额度', state: 'fresh', remainingPercent: 88, lastKnownRemainingPercent: 88, observedAtMs: snap.quota.observedAtMs, resetsAtMs: snap.quota.resetsAtMs },
      { agentId: 'cursor', agentName: 'Cursor', windowId: 'cursor:auto', windowLabel: 'Auto 额度', state: 'fresh', remainingPercent: 54, lastKnownRemainingPercent: 54, observedAtMs: snap.quota.observedAtMs, resetsAtMs: snap.quota.resetsAtMs },
      { agentId: 'cursor', agentName: 'Cursor', windowId: 'cursor:api', windowLabel: 'API 额度', state: 'fresh', remainingPercent: 91, lastKnownRemainingPercent: 91, observedAtMs: snap.quota.observedAtMs, resetsAtMs: snap.quota.resetsAtMs },
      { agentId: 'grok-build', agentName: 'Grok Build', windowId: 'grok:shared_week', windowLabel: '共享周额度', state: 'fresh', remainingPercent: 40, lastKnownRemainingPercent: 40, observedAtMs: snap.quota.observedAtMs, resetsAtMs: snap.quota.resetsAtMs },
    ],
  };
}

function mockPreferences(): OrbPreferences {
  return {
    schemaVersion: 1,
    revision: '1',
    enabled: false,
    diameterDip: 112,
    effectsMode: 'orbit',
    hideOnFullscreen: true,
    selection: { agentId: 'codex', windowId: 'codex:primary:300m' },
  };
}

async function noopUnlisten(): Promise<UnlistenFn> {
  return () => {};
}

export async function getOrbSnapshot(): Promise<OrbSnapshot> {
  if (isTauriEnvironment()) return invoke<OrbSnapshot>('get_orb_snapshot');
  return mockSnapshot();
}

export async function getOrbRenderSnapshot(): Promise<OrbRenderSnapshot> {
  if (isTauriEnvironment()) return invoke<OrbRenderSnapshot>('get_orb_render_snapshot');
  return mockRenderSnapshot();
}

export async function getOrbDetails(): Promise<OrbDetailsSnapshot> {
  if (isTauriEnvironment()) return invoke<OrbDetailsSnapshot>('get_orb_details');
  return mockDetailsSnapshot();
}

export async function getOrbPreferences(): Promise<OrbPreferences> {
  if (isTauriEnvironment()) return invoke<OrbPreferences>('get_orb_preferences');
  return mockPreferences();
}

export async function patchOrbPreferences(patch: OrbPreferencesPatch): Promise<OrbPreferences> {
  if (isTauriEnvironment()) return invoke<OrbPreferences>('patch_orb_preferences', { patch });
  const { expectedRevision, ...fields } = patch;
  return { ...mockPreferences(), ...fields, revision: String(BigInt(expectedRevision) + 1n) };
}

export async function orbReady(generation: number): Promise<void> {
  if (isTauriEnvironment()) await invoke('orb_ready', { generation });
}

export async function orbAction(action: OrbAction['action'], extra?: { paused?: boolean }): Promise<void> {
  if (!isTauriEnvironment()) return;
  if (action === 'set_paused') await invoke('orb_action', { action, paused: extra?.paused ?? false });
  else await invoke('orb_action', { action });
}

export async function orbBeginDrag(): Promise<void> {
  if (isTauriEnvironment()) await invoke('orb_begin_drag');
}

export async function orbEndDrag(): Promise<void> {
  if (isTauriEnvironment()) await invoke('orb_end_drag');
}

export async function orbMove(dx: number, dy: number): Promise<void> {
  if (isTauriEnvironment()) await invoke('orb_move', { dx, dy });
}

export async function orbFling(dx: number, dy: number): Promise<void> {
  if (isTauriEnvironment()) await invoke('orb_fling', { dx, dy });
}

export async function listenOrbSnapshot(handler: (snapshot: OrbSnapshot) => void): Promise<UnlistenFn> {
  if (!isTauriEnvironment()) return noopUnlisten();
  return listen<OrbSnapshot>(ORB_SNAPSHOT_EVENT, event => handler(event.payload));
}

export async function listenOrbRender(handler: (snapshot: OrbRenderSnapshot) => void): Promise<UnlistenFn> {
  if (!isTauriEnvironment()) return noopUnlisten();
  return listen<OrbRenderSnapshot>(ORB_RENDER_EVENT, event => handler(event.payload));
}

export async function listenOrbPreferences(handler: (preferences: OrbPreferences) => void): Promise<UnlistenFn> {
  if (!isTauriEnvironment()) return noopUnlisten();
  return listen<OrbPreferences>(ORB_PREFERENCES_EVENT, event => handler(event.payload));
}

export function readOrbGeneration(search = typeof window === 'undefined' ? '' : window.location.search): number {
  const raw = new URLSearchParams(search).get('generation');
  if (raw == null || raw === '') return 0;
  const n = Number.parseInt(raw, 10);
  return Number.isFinite(n) && n >= 0 ? n : 0;
}

export function readOrbDetailsMode(search = typeof window === 'undefined' ? '' : window.location.search): 'peek' | 'full' {
  return new URLSearchParams(search).get('mode') === 'peek' ? 'peek' : 'full';
}

export function useOrbTransparentRoot(): void {
  useLayoutEffect(() => {
    const root = document.documentElement;
    const body = document.body;
    root.classList.add(ORB_ROOT_CLASS);
    body.classList.add(ORB_ROOT_CLASS);
    const prevHtml = root.style.background;
    const prevBody = body.style.background;
    root.style.background = 'transparent';
    body.style.background = 'transparent';
    document.documentElement.dataset.tokendanceOrb = '1';
    return () => {
      root.classList.remove(ORB_ROOT_CLASS);
      body.classList.remove(ORB_ROOT_CLASS);
      root.style.background = prevHtml;
      body.style.background = prevBody;
      delete document.documentElement.dataset.tokendanceOrb;
    };
  }, []);
}

export function useOrbWindowReady(label: string, generation: number): void {
  const sent = useRef<number | null>(null);
  useEffect(() => {
    document.documentElement.dataset.orbWindow = label;
    if (!isTauriEnvironment() || sent.current === generation) return;
    let cancelled = false;
    let firstFrame = 0;
    let secondFrame = 0;
    let paintTimer = 0;
    let assetTimer = 0;
    const finish = async () => {
      const assets = [document.fonts.ready, ...Array.from(document.images).map(image => image.decode().catch(() => {}))];
      await Promise.race([Promise.all(assets), new Promise(resolve => { assetTimer = window.setTimeout(resolve, 500); })]);
      if (cancelled) return;
      await new Promise<void>(resolve => {
        paintTimer = window.setTimeout(resolve, 120);
        firstFrame = requestAnimationFrame(() => { secondFrame = requestAnimationFrame(() => resolve()); });
      });
      if (cancelled) return;
      try {
        await orbReady(generation);
        sent.current = generation;
      } catch { /* A closing/replaced WebView should not be reopened. */ }
    };
    void finish();
    return () => {
      cancelled = true;
      cancelAnimationFrame(firstFrame);
      cancelAnimationFrame(secondFrame);
      clearTimeout(paintTimer);
      clearTimeout(assetTimer);
    };
  }, [label, generation]);
}
