import { useState } from 'react';
import { patchOrbPreferences } from './bridge';
import { ORB_DIAMETERS, type EffectsMode, type OrbDiameter, type OrbPreferencesPatch } from './types';
import { useOrbDetailsSnapshot, useOrbLanguage, useOrbPreferences } from './useOrbSnapshot';
import './orb.css';

function Toggle({ checked, disabled, label, onChange }: { checked: boolean; disabled: boolean; label: string; onChange: (value: boolean) => void }) {
  return (
    <button type="button" className="settings-toggle" role="switch" aria-checked={checked} aria-label={label} disabled={disabled} onClick={() => onChange(!checked)}>
      <span />
    </button>
  );
}

export function OrbSettings({ zh: zhProp }: { zh?: boolean } = {}) {
  const zhLang = useOrbLanguage();
  const zh = zhProp ?? zhLang;
  const t = (cn: string, en: string) => zh ? cn : en;
  const { preferences, error, refetch } = useOrbPreferences();
  const details = useOrbDetailsSnapshot();
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState('');
  const disabled = busy || !preferences || error;

  const apply = async (patch: Omit<OrbPreferencesPatch, 'expectedRevision'>) => {
    if (!preferences || busy) return;
    setBusy(true);
    try {
      await patchOrbPreferences({ expectedRevision: preferences.revision, ...patch });
      refetch();
      details.refetch();
    } catch (err) {
      setNotice(String(err));
      refetch();
    } finally {
      setBusy(false);
      window.setTimeout(() => setNotice(''), 4000);
    }
  };

  const enabled = preferences?.enabled ?? false;
  const diameter = preferences?.diameterDip ?? 112;
  const effectsMode = preferences?.effectsMode ?? 'orbit';
  const hideOnFullscreen = preferences?.hideOnFullscreen ?? true;
  const selected = preferences?.selection ? `${preferences.selection.agentId}\0${preferences.selection.windowId}` : '';
  const options = details.snapshot?.options ?? [];

  return (
    <section className="settings-section orb-settings" aria-labelledby="orb-heading">
      <div className="settings-section-heading">
        <h2 id="orb-heading">{t('悬浮球', 'Floating orb')}</h2>
        <span>{t('更改自动保存', 'Changes save automatically')}</span>
      </div>
      <div className="settings-sheet">
        <div className="settings-row">
          <div>
            <h3>{t('显示悬浮球', 'Show floating orb')}</h3>
            <p>{t('主窗口收起后显示，打开主窗口时自动隐藏', 'Show when the main window is minimized; hide when it opens')}</p>
          </div>
          <Toggle label={t('显示悬浮球', 'Show floating orb')} checked={enabled} disabled={disabled} onChange={value => void apply({ enabled: value })} />
        </div>
        <div className="settings-row">
          <div>
            <h3>{t('球体尺寸', 'Orb size')}</h3>
            <p>{t('默认 112 DIP，可按桌面缩放加大', 'Default 112 DIP. Larger sizes follow desktop scaling.')}</p>
          </div>
          <select aria-label={t('球体尺寸', 'Orb size')} value={diameter} disabled={disabled} onChange={event => void apply({ diameterDip: Number(event.target.value) as OrbDiameter })}>
            {ORB_DIAMETERS.map(size => <option key={size} value={size}>{size} DIP</option>)}
          </select>
        </div>
        <div className="settings-row">
          <div>
            <h3>{t('特效', 'Effects')}</h3>
            <p>{t('默认呼吸加环绕微光；也可只保留光晕或关闭', 'Breathing glow and orbit by default, or glow only / off')}</p>
          </div>
          <select aria-label={t('特效', 'Effects')} value={effectsMode} disabled={disabled} onChange={event => void apply({ effectsMode: event.target.value as EffectsMode })}>
            <option value="orbit">{t('呼吸 + 环绕微光', 'Orbit')}</option>
            <option value="soft">{t('仅呼吸光晕', 'Soft glow')}</option>
            <option value="off">{t('关闭特效', 'Off')}</option>
          </select>
        </div>
        <div className="settings-row">
          <div>
            <h3>{t('全屏时隐藏', 'Hide on fullscreen')}</h3>
            <p>{t('前台应用全屏时临时隐藏，不会关闭开关', 'Temporarily hide while another app is fullscreen')}</p>
          </div>
          <Toggle label={t('全屏时隐藏', 'Hide on fullscreen')} checked={hideOnFullscreen} disabled={disabled} onChange={value => void apply({ hideOnFullscreen: value })} />
        </div>
        <div className="settings-row">
          <div>
            <h3>{t('关注额度', 'Watched quota')}</h3>
            <p>{t('颜色与弧长只反映这一来源窗口，不会自动轮播', 'Color and arc follow this source window only')}</p>
          </div>
          <select
            aria-label={t('关注额度', 'Watched quota')}
            value={selected}
            disabled={disabled || options.length === 0}
            onChange={event => {
              const option = options.find(item => `${item.agentId}\0${item.windowId}` === event.target.value);
              void apply({ selection: option ? { agentId: option.agentId, windowId: option.windowId } : null });
            }}
          >
            <option value="">{t('尚未选择', 'Not selected')}</option>
            {options.map(option => (
              <option key={`${option.agentId}:${option.windowId}`} value={`${option.agentId}\0${option.windowId}`}>
                {option.agentName} · {option.windowLabel}
              </option>
            ))}
          </select>
        </div>
      </div>
      {notice && <p className="orb-empty" role="status">{notice}</p>}
    </section>
  );
}
