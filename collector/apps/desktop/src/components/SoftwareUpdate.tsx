import { useEffect, useId, useRef, useState } from 'react';
import { checkUpdates, installUpdate, setAutoUpdate, updateBusy, updateError, updateStatusText, useUpdates } from '../update-state';
import '../styles/updates.css';

export function UpdateNotice({ zh }: { zh: boolean }) {
  const status = useUpdates();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  useEffect(() => {
    if (!open) return;
    const outside = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false); };
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.stopImmediatePropagation(); event.preventDefault(); setOpen(false); trigger.current?.focus(); }
    };
    document.addEventListener('pointerdown', outside);
    window.addEventListener('keydown', escape, true);
    return () => { document.removeEventListener('pointerdown', outside); window.removeEventListener('keydown', escape, true); };
  }, [open]);
  if (!status?.version || !status.supported || status.phase === 'latest') return null;
  const t = (cn: string, en: string) => zh ? cn : en;
  const updating = busy || updateBusy(status);
  return <div className="desktop-update-notice" ref={root}>
    <button type="button" ref={trigger} className="desktop-update-badge" aria-expanded={open} aria-controls={id} aria-label={t('发现新版本，查看更新说明', 'New version. View release notes')} onClick={() => setOpen(!open)}>NEW</button>
    {open && <section className="desktop-update-popover" id={id} role="dialog" aria-modal="false" aria-labelledby={`${id}-title`}>
      <div className="desktop-update-heading"><div><h2 id={`${id}-title`}>{status.phase === 'ready' ? t('新版本已准备好', 'Update ready') : t('有新版本可用', 'Update available')}</h2><p>v{status.version}{status.publishedAt && ` · ${new Date(status.publishedAt).toLocaleDateString(zh ? 'zh-CN' : 'en-US')}`}</p></div><button type="button" aria-label={t('收起更新说明', 'Close release notes')} onClick={() => { setOpen(false); trigger.current?.focus(); }}>⌃</button></div>
      <div className="desktop-update-notes">{status.notes.trim() || t('此版本包含体验优化与问题修复。', 'This release contains improvements and fixes.')}</div>
      <p className="desktop-update-status" role="status">{updateStatusText(status, zh)}</p>
      {status.phase === 'downloading' && <progress aria-label={t('更新下载进度', 'Update download progress')} max={100} value={status.progress} />}
      {error && <p className="desktop-update-error" role="alert">{error}</p>}
      <button type="button" className="desktop-update-action" disabled={updating} onClick={async () => {
        setBusy(true); setError(''); try { await installUpdate(); } catch (err) { setError(updateError(String(err), zh)); } finally { setBusy(false); }
      }}>{updating ? t('更新中…', 'Updating…') : status.phase === 'ready' ? t('重启并更新', 'Restart and update') : t('立即更新', 'Update now')}</button>
      {!updating && <p className="desktop-update-caption">{t('更新完成后将重新启动 TokenDance', 'TokenDance will restart after updating')}</p>}
    </section>}
  </div>;
}

export function SoftwareUpdateCard({ zh }: { zh: boolean }) {
  const status = useUpdates();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const t = (cn: string, en: string) => zh ? cn : en;
  const run = async (action: () => Promise<unknown>) => {
    setBusy(true); setError(''); try { await action(); } catch (err) { setError(updateError(String(err), zh)); } finally { setBusy(false); }
  };
  const disabled = busy || !status || !status.supported || updateBusy(status);
  return <section className="settings-section settings-updates" aria-labelledby="software-update-heading">
    <div className="settings-section-heading"><h2 id="software-update-heading">{t('软件更新', 'Software updates')}</h2><span>{status ? `v${status.currentVersion}` : '—'}</span></div>
    <div className="settings-sheet">
      <div className="settings-row"><div><h3>{t('检查更新', 'Check for updates')}</h3><p role="status">{status ? updateStatusText(status, zh) : t('正在读取更新状态…', 'Loading update status…')}</p>{status?.checkedAt && <p>{t('上次检查：', 'Last checked: ')}{new Date(status.checkedAt).toLocaleString(zh ? 'zh-CN' : 'en-US')}</p>}</div>
        <div className="desktop-update-buttons"><button type="button" className="desktop-update-action" disabled={disabled} onClick={() => void run(checkUpdates)}>{status?.phase === 'checking' ? t('检查中…', 'Checking…') : t('检查更新', 'Check for updates')}</button>{status?.version && <button type="button" className="desktop-update-action" disabled={disabled} onClick={() => void run(installUpdate)}>{status.phase === 'ready' ? t('重启并更新', 'Restart and update') : t('立即更新', 'Update now')}</button>}</div>
      </div>
      {status?.phase === 'downloading' && <progress aria-label={t('更新下载进度', 'Update download progress')} max={100} value={status.progress} />}
      <div className="settings-row"><div><h3>{t('自动更新', 'Automatic updates')}</h3><p>{t('后台下载，下次启动时安装', 'Download in the background. Install on next launch.')}</p></div><button type="button" className="settings-toggle" role="switch" aria-checked={status?.autoUpdate ?? false} aria-label={t('自动更新', 'Automatic updates')} disabled={busy || !status || !status.supported || status.phase === 'installing'} onClick={() => void run(() => setAutoUpdate(!status?.autoUpdate))}><span /></button></div>
    </div>
    {error && <p role="alert" className="desktop-update-error">{error}</p>}
  </section>;
}
