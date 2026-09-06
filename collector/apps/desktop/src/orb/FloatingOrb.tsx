import { useEffect, useRef, type CSSProperties } from 'react';
import { orbAction, readOrbGeneration, useOrbTransparentRoot, useOrbWindowReady } from './bridge';
import { formatRemainingLabel, formatTodayTokens, quotaStatusLine } from './format';
import './orb.css';
import { OrbMaterial } from './OrbMaterial';
import { useOrbGesture } from './useOrbGesture';
import { selectionKey, shouldSnapQuotaVisual } from './types';
import { useAnimatedQuotaVisual, useOrbLanguage, useOrbSnapshot, usePrefersReducedMotion } from './useOrbSnapshot';

export function FloatingOrb({ generation }: { generation?: number } = {}) {
  useOrbTransparentRoot();
  const gen = generation ?? readOrbGeneration();
  useOrbWindowReady('orb', gen);
  const zh = useOrbLanguage();
  const reducedMotion = usePrefersReducedMotion();
  const { snapshot, error, quotaState, remainingPercent, paused, hidden } = useOrbSnapshot();
  const t = (cn: string, en: string) => zh ? cn : en;
  const prev = useRef({ state: quotaState, key: selectionKey(snapshot?.quota.selection) ?? null });
  const snapVisual = shouldSnapQuotaVisual(prev.current.state, quotaState, prev.current.key, selectionKey(snapshot?.quota.selection) ?? null);
  useEffect(() => {
    prev.current = { state: quotaState, key: selectionKey(snapshot?.quota.selection) ?? null };
  }, [quotaState, snapshot?.quota.selection]);
  const freeze = reducedMotion || hidden || snapshot?.effect.reducedMotion === true;
  const sphere = useAnimatedQuotaVisual(remainingPercent, quotaState, snapVisual, freeze);
  const loading = !snapshot && !error;
  const tokenText = snapshot ? formatTodayTokens(snapshot.usage.todayTokens, snapshot.usage.state) : '—';
  const noRecords = !!snapshot && (snapshot.usage.state !== 'known' || snapshot.usage.todayTokens == null);
  const status = quotaState === 'fresh' && !paused
    ? formatRemainingLabel(remainingPercent ?? 0, zh)
    : quotaStatusLine(error ? 'unavailable' : quotaState, zh, paused);
  const gesture = useOrbGesture(hidden);

  const stroke = 2.5;
  const size = 100;
  const r = 47.8;
  const cx = size / 2;
  const showArc = sphere.arcDegrees > 0;
  const full = sphere.arcDegrees >= 359.9;
  const dash = sphere.remaining;

  return (
    <div className="orb-root">
      <button
        type="button"
        className={`orb-sphere${paused ? ' is-paused' : ''}`}
        style={{ '--orb-color': sphere.color, '--orb-value-size': Math.min(25, 133 / tokenText.length) + 'cqw' } as CSSProperties}
        data-motion={freeze || paused || quotaState !== 'fresh' || snapshot?.effect.mode !== 'orbit' ? 'still' : 'orbit'}
        data-dragging={gesture.dragging ? 'true' : 'false'}
        data-charging={gesture.charging ? 'true' : 'false'}
        aria-label={`${t('今日 Token', 'Today tokens')} ${tokenText}${noRecords ? ` · ${t('暂无记录', 'No records')}` : ''}, ${status}`}
        onPointerDown={gesture.onPointerDown}
        onPointerMove={gesture.onPointerMove}
        onPointerUp={gesture.onPointerUp}
        onDoubleClick={gesture.onDoubleClick}
        onPointerCancel={gesture.onPointerCancel}
        onLostPointerCapture={gesture.onLostPointerCapture}
        onContextMenu={event => event.preventDefault()}
        title={gesture.charging ? undefined : gesture.error || t('双击打开主界面 · 右键向后拉，松手反向弹出 · 左键拖动，靠边收起', 'Double-click to open · Right-drag to pull back and launch · Drag to an edge to tuck away')}
        onKeyDown={event => {
          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); void orbAction('activate').catch(() => {}); }
        }}
      >
        <OrbMaterial />
        {gesture.charging && <div className="orb-charge" style={{'--pull-power':gesture.pull.power/(1+gesture.pull.power),'--pull-angle':`${gesture.pull.angle}deg`} as CSSProperties} aria-hidden="true"><i />{gesture.pull.power>0.025 && <span className="orb-launch-direction">➜</span>}</div>}
        <svg className="orb-ring" viewBox={`0 0 ${size} ${size}`} aria-hidden="true">
          <circle cx={cx} cy={cx} r={r} fill="none" stroke="rgba(46,65,53,0.65)" strokeWidth={stroke} />
          {showArc && (
            <circle
              cx={cx}
              cy={cx}
              r={r}
              fill="none"
              stroke={sphere.color}
              strokeWidth={stroke}
              pathLength={100}
              strokeLinecap={full ? 'butt' : 'round'}
              strokeDasharray={full ? undefined : `${dash} ${100 - dash}`}
              transform={`rotate(-90 ${cx} ${cx})`}
            />
          )}
        </svg>
        <div className={`orb-copy${paused ? ' is-paused' : ''}`}>
          {loading ? (
            <div className="orb-skeleton" aria-hidden="true"><i /><i /><i /></div>
          ) : (
            <>
              <span className="orb-kicker">{t('今日 Token', 'Today tokens')}</span>
              <strong className="orb-value">{tokenText}</strong>
              <span className="orb-status">{gesture.charging ? (gesture.pull.power>0.025 ? t(`力度 ${gesture.pull.power.toFixed(1)}×`,`Power ${gesture.pull.power.toFixed(1)}×`) : t('向后拉动', 'Pull back')) : status}</span>
            </>
          )}
        </div>
      </button>
    </div>
  );
}
