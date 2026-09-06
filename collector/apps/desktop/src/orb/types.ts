export type QuotaState =
  | 'loading'
  | 'fresh'
  | 'stale'
  | 'not_connected'
  | 'auth_required'
  | 'unavailable'
  | 'no_quota'
  | 'unlimited';

export type EffectsMode = 'orbit' | 'soft' | 'off';
export type OrbDiameter = 112 | 128 | 144 | 160;
export type CollectorRunState = 'running' | 'paused' | 'degraded' | 'stopped';
export type UsageValueState = 'known' | 'unknown' | 'error';
export type OrbPulseKind = 'usage' | 'low_quota';

export interface OrbQuotaSelection {
  agentId: string;
  windowId: string;
}

export interface OrbPulse {
  id: string;
  kind: OrbPulseKind;
  expiresAtMs: number;
}

export interface OrbUsageSummary {
  localDate: string;
  state: UsageValueState;
  todayTokens: string | null;
  knownSourceCount: number;
  hasUnmeasuredSources: boolean;
  capturedAtMs: number;
  lastRecordedChangeAtMs: number | null;
}

export interface OrbCollectorStatus {
  state: CollectorRunState;
  syncState: string;
}

export interface OrbQuotaView {
  selection: OrbQuotaSelection | null;
  agentName: string | null;
  windowLabel: string | null;
  state: QuotaState;
  remainingPercent: number | null;
  lastKnownRemainingPercent: number | null;
  observedAtMs: number | null;
  resetsAtMs: number | null;
  staleAtMs: number | null;
  identityConfidence: 'source_verified' | 'unavailable';
}

export interface OrbEffectState {
  mode: EffectsMode;
  reducedMotion: boolean;
  pulse: OrbPulse | null;
}

export interface OrbSnapshot {
  schemaVersion: 1;
  streamId: string;
  revision: string;
  emittedAtMs: number;
  preferencesRevision: string;
  hidden?: boolean;
  usage: OrbUsageSummary;
  collector: OrbCollectorStatus;
  quota: OrbQuotaView;
  effect: OrbEffectState;
}

export interface OrbRenderSnapshot {
  schemaVersion: 1;
  streamId: string;
  revision: string;
  emittedAtMs: number;
  diameterDip: OrbDiameter;
  hidden: boolean;
  collectorPaused: boolean;
  quota: {
    state: QuotaState;
    remainingPercent: number | null;
    staleAtMs: number | null;
    selectionKey: string | null;
  };
  effect: OrbEffectState;
}

export interface OrbTodaySource {
  agentId: string;
  agentName: string;
  todayTokens: string | null;
}

export interface OrbQuotaOption {
  agentId: string;
  agentName: string;
  windowId: string;
  windowLabel: string;
  state: QuotaState;
  remainingPercent: number | null;
  lastKnownRemainingPercent: number | null;
  observedAtMs: number | null;
  resetsAtMs: number | null;
}

export interface OrbDetailsSnapshot {
  schemaVersion: 1;
  streamId: string;
  revision: string;
  emittedAtMs: number;
  preferencesRevision: string;
  hidden?: boolean;
  usage: OrbUsageSummary & { sources: OrbTodaySource[] };
  collector: OrbCollectorStatus;
  quota: OrbQuotaView & { identityNote?: string | null };
  options: OrbQuotaOption[];
}

export interface OrbPreferences {
  schemaVersion: 1;
  revision: string;
  enabled: boolean;
  diameterDip: OrbDiameter;
  effectsMode: EffectsMode;
  hideOnFullscreen: boolean;
  selection: OrbQuotaSelection | null;
}

export interface OrbPreferencesPatch {
  expectedRevision: string;
  enabled?: boolean;
  diameterDip?: OrbDiameter;
  effectsMode?: EffectsMode;
  hideOnFullscreen?: boolean;
  selection?: OrbQuotaSelection | null;
}

export type OrbActionName = 'activate' | 'reveal' | 'grab' | 'release_grab' | 'stop_motion' | 'begin_pull' | 'cancel_pull' | 'open_details' | 'close_details' | 'hide' | 'open_main' | 'open_settings' | 'set_paused';

export type OrbAction =
  | { action: Exclude<OrbActionName, 'set_paused'> }
  | { action: 'set_paused'; paused: boolean };

export type SnapshotEventDecision = 'accept' | 'drop' | 'resync';

export const ORB_DIAMETERS: readonly OrbDiameter[] = [112, 128, 144, 160];
export const DRAG_THRESHOLD_DIP = 4;
export const ORB_ROOT_CLASS = 'tokendance-orb';
export const PEEK_OPEN_MS = 400;
export const PEEK_CLOSE_MS = 150;
export const QUOTA_TRANSITION_MS = 600;
export const USAGE_PULSE_MS = 500;
export const LOW_QUOTA_FLASH_MS = 800;

export function isNewerRevision(current: string | null | undefined, incoming: string): boolean {
  try {
    if (current == null || current === '') return true;
    return BigInt(incoming) > BigInt(current);
  } catch {
    return false;
  }
}

export function classifyOrbEvent(
  currentStreamId: string | null | undefined,
  currentRevision: string | null | undefined,
  incomingStreamId: string,
  incomingRevision: string,
): SnapshotEventDecision {
  if (!incomingStreamId) return 'drop';
  if (currentStreamId == null || currentStreamId === '') return 'drop';
  if (incomingStreamId !== currentStreamId) return 'resync';
  return isNewerRevision(currentRevision, incomingRevision) ? 'accept' : 'drop';
}

export function estimatedNowMs(emittedAtMs: number, receivedPerf: number, nowPerf = performance.now()): number {
  return emittedAtMs + (nowPerf - receivedPerf);
}

export function resolveQuotaState(quota: { state: QuotaState; staleAtMs: number | null }, nowMs: number): QuotaState {
  if (quota.state === 'fresh' && quota.staleAtMs != null && nowMs >= quota.staleAtMs) return 'stale';
  return quota.state;
}

export function resolveRemainingPercent(
  quota: { state: QuotaState; remainingPercent: number | null; staleAtMs: number | null },
  nowMs: number,
): number | null {
  return resolveQuotaState(quota, nowMs) === 'fresh' ? quota.remainingPercent : null;
}

export function isPulsePlayable(
  pulse: OrbPulse | null | undefined,
  nowMs: number,
  lastPlayedId: string | null = null,
): pulse is OrbPulse {
  return !!pulse && pulse.expiresAtMs > nowMs && pulse.id !== lastPlayedId;
}

export function selectionKey(selection: OrbQuotaSelection | null | undefined): string | null {
  return selection ? `${selection.agentId}\0${selection.windowId}` : null;
}

export function shouldSnapQuotaVisual(
  previousState: QuotaState | null,
  nextState: QuotaState,
  previousSelection: string | null,
  nextSelection: string | null,
): boolean {
  if (nextState !== 'fresh') return true;
  if (previousState != null && previousState !== 'fresh') return true;
  if (previousSelection != null && previousSelection !== nextSelection) return true;
  return false;
}

export function isCollectorPaused(state: CollectorRunState | undefined): boolean {
  return state === 'paused' || state === 'stopped';
}
