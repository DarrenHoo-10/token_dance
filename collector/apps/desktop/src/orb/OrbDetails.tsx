import { useEffect, useRef } from 'react';
import { syncStatusText } from '../sync-status';
import { orbAction, patchOrbPreferences, readOrbDetailsMode, readOrbGeneration, useOrbTransparentRoot, useOrbWindowReady } from './bridge';
import { compareTokenStrings, formatCompactTokens, formatObservedTime, formatRemainingLabel, formatResetCountdown, parseTokenCount, quotaStatusLine } from './format';
import { PEEK_CLOSE_MS, resolveRemainingPercent } from './types';
import { useOrbDetailsSnapshot, useOrbLanguage } from './useOrbSnapshot';
import './orb.css';

export function OrbDetails({ generation, mode }: { generation?: number; mode?: 'peek' | 'full' } = {}) {
  useOrbTransparentRoot();
  const gen = generation ?? readOrbGeneration();
  const view = mode ?? readOrbDetailsMode();
  useOrbWindowReady('orb-details', gen);
  const zh = useOrbLanguage();
  const t = (cn: string, en: string) => zh ? cn : en;
  const { snapshot, error, nowMs, quotaState, refetch } = useOrbDetailsSnapshot();
  const leaveTimer = useRef(0);
  const remaining = snapshot ? resolveRemainingPercent(snapshot.quota, nowMs) : null;
  const paused = snapshot?.collector.state === 'paused' || snapshot?.collector.state === 'stopped';

  useEffect(() => {
    if (view !== 'full') return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        void orbAction('close_details');
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [view]);

  const onPeekLeave = () => {
    window.clearTimeout(leaveTimer.current);
    leaveTimer.current = window.setTimeout(() => { void orbAction('close_details'); }, PEEK_CLOSE_MS);
  };
  const onPeekEnter = () => window.clearTimeout(leaveTimer.current);
  useEffect(() => () => window.clearTimeout(leaveTimer.current), []);

  if (!snapshot) {
    return (
      <div className="orb-details-root">
        {view === 'peek' ? (
          <div className="orb-peek"><p>{error ? t('额度待更新', 'Awaiting update') : t('加载中', 'Loading')}</p></div>
        ) : (
          <section className="orb-details">
            <header><h1>{t('悬浮球详情', 'Orb details')}</h1></header>
            <div className="orb-details-body"><p className="orb-empty">{error ? t('无法读取详情', 'Could not load details') : t('加载中', 'Loading')}</p></div>
          </section>
        )}
      </div>
    );
  }

  const source = [snapshot.quota.agentName, snapshot.quota.windowLabel].filter(Boolean).join(' · ');
  const reset = snapshot.quota.resetsAtMs != null ? formatResetCountdown(snapshot.quota.resetsAtMs, nowMs, zh) : t('重置时间未知', 'Reset time unknown');
  const observed = snapshot.quota.observedAtMs != null ? formatObservedTime(snapshot.quota.observedAtMs, zh) : t('观测时间未知', 'Observation time unknown');
  const status = quotaState === 'fresh' && remaining != null ? formatRemainingLabel(remaining, zh) : quotaStatusLine(quotaState, zh, paused);

  if (view === 'peek') {
    return (
      <div className="orb-details-root" onMouseEnter={onPeekEnter} onMouseLeave={onPeekLeave}>
        <div className="orb-peek" role="tooltip">
          <strong>{source || t('未选择额度', 'No quota selected')}</strong>
          <p>{status}{status ? ' · ' : ''}{reset}</p>
          <p>{t('记录时间', 'Observed')} · {observed}</p>
        </div>
      </div>
    );
  }

  const sources = [...snapshot.usage.sources].sort((a, b) => compareTokenStrings(a.todayTokens, b.todayTokens));
  const selected = snapshot.quota.selection ? `${snapshot.quota.selection.agentId}\0${snapshot.quota.selection.windowId}` : '';
  const lastKnown = snapshot.quota.lastKnownRemainingPercent;
  const changeSelection = async (value: string) => {
    const option = snapshot.options.find(item => `${item.agentId}\0${item.windowId}` === value);
    if (!option) return;
    await patchOrbPreferences({
      expectedRevision: snapshot.preferencesRevision,
      selection: { agentId: option.agentId, windowId: option.windowId },
    });
    refetch();
  };

  return (
    <div className="orb-details-root">
      <section className="orb-details">
        <header>
          <h1>{t('额度详情', 'Quota details')}</h1>
          <button type="button" aria-label={t('关闭', 'Close')} onClick={() => void orbAction('close_details')}>×</button>
        </header>
        <div className="orb-details-body">
          <label htmlFor="orb-source-picker">{t('关注额度', 'Watched quota')}</label>
          <select id="orb-source-picker" value={selected} onChange={event => void changeSelection(event.target.value)}>
            {snapshot.options.map(option => (
              <option key={`${option.agentId}:${option.windowId}`} value={`${option.agentId}\0${option.windowId}`}>
                {option.agentName} · {option.windowLabel}
              </option>
            ))}
          </select>
          <div className="orb-meta">
            <p>{t('状态', 'Status')} · {status || t('新鲜', 'Fresh')}</p>
            {quotaState !== 'fresh' && lastKnown != null && <p>{t('上次余量', 'Last known remaining')} · {Math.round(lastKnown)}%</p>}
            <p>{t('记录时间', 'Observed')} · {observed}</p>
            <p>{reset}</p>
            <p>{syncStatusText(snapshot.collector.syncState, 0, zh)}</p>
            {(snapshot.quota.identityConfidence === 'unavailable' || snapshot.quota.identityNote) && (
              <p className="orb-note">{snapshot.quota.identityNote || t('来自最近本地日志', 'From recent local logs')}</p>
            )}
            {snapshot.usage.hasUnmeasuredSources && <p className="orb-note">{t('部分来源尚未计入今日 Token', 'Some sources are not included in today\'s tokens')}</p>}
          </div>
          <div className="orb-sources">
            <h2>{t('今日来源', 'Today\'s sources')}</h2>
            {sources.length === 0 && <p className="orb-empty">{t('暂无记录', 'No records')}</p>}
            {sources.map(item => {
              const tokens = parseTokenCount(item.todayTokens);
              return (
                <div className="orb-source-row" key={item.agentId}>
                  <span>{item.agentName}</span>
                  <strong>{tokens == null ? '—' : formatCompactTokens(tokens)}</strong>
                </div>
              );
            })}
          </div>
          <div className="orb-actions">
            <button type="button" onClick={() => void orbAction('set_paused', { paused: !paused })}>{paused ? t('继续采集', 'Resume') : t('暂停采集', 'Pause')}</button>
            <button type="button" onClick={() => void orbAction('hide')}>{t('隐藏悬浮球', 'Hide orb')}</button>
            <button type="button" onClick={() => void orbAction('open_main')}>{t('打开面板', 'Open panel')}</button>
            <button type="button" onClick={() => void orbAction('open_settings')}>{t('设置', 'Settings')}</button>
          </div>
        </div>
      </section>
    </div>
  );
}
