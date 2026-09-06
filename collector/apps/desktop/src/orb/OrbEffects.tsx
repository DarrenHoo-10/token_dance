import { useEffect, useRef, useState, type CSSProperties } from 'react';
import { readOrbGeneration, useOrbTransparentRoot, useOrbWindowReady } from './bridge';
import { LOW_QUOTA_FLASH_MS, USAGE_PULSE_MS, isPulsePlayable, shouldSnapQuotaVisual } from './types';
import { useAnimatedQuotaVisual, useOrbRenderSnapshot, usePrefersReducedMotion } from './useOrbSnapshot';
import './orb.css';

const PARTICLES = [18, 148, 268];

export function OrbEffects({ generation }: { generation?: number } = {}) {
  useOrbTransparentRoot();
  const gen = generation ?? readOrbGeneration();
  useOrbWindowReady('orb-effects', gen);
  const reducedMotion = usePrefersReducedMotion();
  const { snapshot, quotaState, remainingPercent, paused, hidden } = useOrbRenderSnapshot();
  const prev = useRef({ state: quotaState, key: snapshot?.quota.selectionKey ?? null });
  const snapVisual = shouldSnapQuotaVisual(prev.current.state, quotaState, prev.current.key, snapshot?.quota.selectionKey ?? null);
  useEffect(() => {
    prev.current = { state: quotaState, key: snapshot?.quota.selectionKey ?? null };
  }, [quotaState, snapshot?.quota.selectionKey]);
  const nativeReduced = snapshot?.effect.reducedMotion === true;
  const freeze = reducedMotion || nativeReduced || hidden;
  const visual = useAnimatedQuotaVisual(remainingPercent, quotaState, snapVisual, freeze);
  const mode = snapshot?.effect.mode ?? 'orbit';
  const loops = !freeze && !paused && quotaState === 'fresh' && mode !== 'off';
  const played = useRef<string | null>(null);
  const [pulseKind, setPulseKind] = useState<'usage' | 'low_quota' | null>(null);

  useEffect(() => {
    const pulse = snapshot?.effect.pulse;
    if (!loops || !pulse) {
      setPulseKind(null);
      return;
    }
    if (played.current === pulse.id) return;
    if (!isPulsePlayable(pulse, Date.now(), played.current)) return;
    played.current = pulse.id;
    setPulseKind(pulse.kind);
    const ms = pulse.kind === 'low_quota' ? LOW_QUOTA_FLASH_MS : USAGE_PULSE_MS;
    const timer = window.setTimeout(() => setPulseKind(current => current === pulse.kind ? null : current), ms);
    return () => clearTimeout(timer);
  }, [snapshot?.effect.pulse?.id, snapshot?.effect.pulse?.kind, snapshot?.effect.pulse?.expiresAtMs, loops]);

  if (mode === 'off') return <div className="orb-effects-root" />;

  const classes = [
    'orb-effects',
    mode === 'soft' ? 'is-soft' : '',
    loops ? '' : 'is-static',
    pulseKind ? `is-pulse-${pulseKind}` : '',
  ].filter(Boolean).join(' ');

  return (
    <div className="orb-effects-root">
      <div className={classes} style={{ '--orb-color': visual.color } as CSSProperties} aria-hidden="true">
        <div className="orb-glow" />
        {mode === 'orbit' && (
          <>
            <svg className="orb-drift" viewBox="0 0 100 100">
              <circle className="orb-drift-arc" cx="50" cy="50" r="46" pathLength={100} strokeDasharray="16 84" />
            </svg>
            <div className="orb-particle-ring">
              {PARTICLES.map(angle => (
                <span key={angle} className="orb-particle-arm" style={{ '--a': `${angle}deg` } as CSSProperties}><i className="orb-particle" /></span>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
