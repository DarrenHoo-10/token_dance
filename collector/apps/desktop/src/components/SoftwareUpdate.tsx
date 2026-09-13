import { createPortal } from 'react-dom';
import { useEffect, useId, useRef, useState } from 'react';
import { checkUpdates, installUpdate, openUpdateDownloads, setAutoUpdate, updateBusy, updateError, updateStatusText, useUpdates } from '../update-state';
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
  if (status?.required) return createPortal(<RequiredUpdate zh={zh} />, document.body);
  if (!status?.version || !status.supported || status.phase === 'latest') return null;
  const t = (cn: string, en: string) => zh ? cn : en;
  const updating = busy || updateBusy(status);
  const failure = error || (status.phase === 'error' ? updateError(status.error, zh) : '');
  return <div className="desktop-update-notice" ref={root}>
    <button type="button" ref={trigger} className="desktop-update-badge" aria-expanded={open} aria-controls={id} aria-label={t('发现新版本，查看更新说明', 'New version. View release notes')} onClick={() => setOpen(!open)}>NEW</button>
    {open && <section className="desktop-update-popover" id={id} role="dialog" aria-modal="false" aria-labelledby={`${id}-title`}>
      <div className="desktop-update-heading"><div><h2 id={`${id}-title`}>{status.phase === 'ready' ? t('新版本已准备好', 'Update ready') : t('有新版本可用', 'Update available')}</h2><p>v{status.version}{status.publishedAt && ` · ${new Date(status.publishedAt).toLocaleDateString(zh ? 'zh-CN' : 'en-US')}`}</p></div><button type="button" aria-label={t('收起更新说明', 'Close release notes')} onClick={() => { setOpen(false); trigger.current?.focus(); }}>⌃</button></div>
      <div className="desktop-update-notes">{status.notes.trim() || t('此版本包含体验优化与问题修复。', 'This release contains improvements and fixes.')}</div>
      <p className={failure ? 'desktop-update-error' : 'desktop-update-status'} role={failure ? 'alert' : 'status'}>{failure || updateStatusText(status, zh)}</p>
      {status.phase === 'downloading' && <progress aria-label={t('更新下载进度', 'Update download progress')} max={100} value={status.progress} />}
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
  const disabled = busy || !status || updateBusy(status);
  const failure = error || (status?.phase === 'error' ? updateError(status.error, zh) : '');
  return <section className="settings-section settings-updates" aria-labelledby="software-update-heading">
    <div className="settings-section-heading"><h2 id="software-update-heading">{t('软件更新', 'Software updates')}</h2><span>{status ? `v${status.currentVersion}` : '—'}</span></div>
    <div className="settings-sheet">
      <div className="settings-row"><div><h3>{t('检查更新', 'Check for updates')}</h3><p role={failure ? 'alert' : 'status'} className={failure ? 'desktop-update-error' : undefined}>{failure || (status ? updateStatusText(status, zh) : t('正在读取更新状态…', 'Loading update status…'))}</p>{status?.checkedAt && <p>{t('上次检查：', 'Last checked: ')}{new Date(status.checkedAt).toLocaleString(zh ? 'zh-CN' : 'en-US')}</p>}</div>
        <div className="desktop-update-buttons"><button type="button" className="desktop-update-action" disabled={disabled} onClick={() => void run(checkUpdates)}>{status?.phase === 'checking' ? t('检查中…', 'Checking…') : t('检查更新', 'Check for updates')}</button>{status?.supported && status.version && <button type="button" className="desktop-update-action" disabled={disabled} onClick={() => void run(installUpdate)}>{status.phase === 'ready' ? t('重启并更新', 'Restart and update') : t('立即更新', 'Update now')}</button>}{status && !status.supported && <button type="button" className="desktop-update-action" disabled={disabled} onClick={() => void run(openUpdateDownloads)}>{t('下载安装包', 'Download installer')}</button>}</div>
      </div>
      {status?.phase === 'downloading' && <progress aria-label={t('更新下载进度', 'Update download progress')} max={100} value={status.progress} />}
      <div className="settings-row"><div><h3>{t('自动更新', 'Automatic updates')}</h3><p>{t('后台下载，下次启动时安装', 'Download in the background. Install on next launch.')}</p></div><button type="button" className="settings-toggle" role="switch" aria-checked={status?.autoUpdate ?? false} aria-label={t('自动更新', 'Automatic updates')} disabled={busy || !status || !status.supported || status.phase === 'installing'} onClick={() => void run(() => setAutoUpdate(!status?.autoUpdate))}><span /></button></div>
    </div>
  </section>;
}

function RequiredUpdate({ zh }: { zh: boolean }) {
  const status = useUpdates();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); }
    };
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, []);
  if (!status?.required) return null;
  const t = (cn: string, en: string) => zh ? cn : en;
  const run = async (action: () => Promise<unknown>) => {
    setBusy(true); setError('');
    try { await action(); } catch (e) { setError(updateError(String(e), zh)); }
    finally { setBusy(false); }
  };
  const updating = busy || updateBusy(status);
  const failure = error || (status.phase === 'error' ? updateError(status.error, zh) : '');
  const showFailure = Boolean(failure);
  const showReady = status.phase === 'ready' && !showFailure;
  const downloading = status.phase === 'downloading' || status.phase === 'installing';
  const current = status.currentVersion;
  const minimum = status.minimumVersion || '—';
  const target = status.version;

  let title = t('必须更新才能继续', 'Update required to continue');
  if (showFailure) title = t('更新失败', 'Update failed');
  else if (showReady) title = t('更新已就绪', 'Update ready');

  return <div className="required-update-backdrop">
    <section role="alertdialog" aria-modal="true" aria-labelledby="required-update-title" className="required-update-dialog">
      {!showReady && !showFailure && <div className="required-update-icon" aria-hidden="true">!</div>}
      <h2 id="required-update-title">{title}</h2>

      {showReady ? (
        <p className="required-update-versions">
          {t(`当前 v${current} → 将安装 v${target} (最低要求 v${minimum})`, `Current v${current} → Installing v${target} (minimum v${minimum})`)}
        </p>
      ) : showFailure ? (
        <p className="required-update-versions">
          {t(`当前 v${current} · 最低要求 v${minimum}${target ? ` · 目标 v${target}` : ''}`, `Current v${current} · Minimum v${minimum}${target ? ` · Target v${target}` : ''}`)}
        </p>
      ) : (
        <p className="required-update-versions">
          {t(`当前版本 v${current} 最低支持版本 v${minimum}`, `Current v${current} · Minimum supported v${minimum}`)}
        </p>
      )}

      {showReady ? (
        <>
          <p className="required-update-copy">{t('版本过旧，必须更新后才能继续使用在线服务', 'This version is too old. Update to keep using online services.')}</p>
          <p className="required-update-copy" role="status">{t('已下载完成，可立即安装', 'Download complete. Ready to install.')}</p>
        </>
      ) : showFailure ? (
        <p className="required-update-error" role="alert">{failure}</p>
      ) : (
        <>
          <p className="required-update-copy">{t('请更新后继续使用在线服务。本机采集已在运行，数据会保留。', 'Update to keep using online services. Local collection is running and your data is retained.')}</p>
          {target && <p className="required-update-target">{t(`目标版本 v${target}`, `Target version v${target}`)}</p>}
          <p className="required-update-status" role="status">
            {downloading
              ? t(`正在下载更新 · ${status.progress}%`, `Downloading update · ${status.progress}%`)
              : updateStatusText(status, zh)}
          </p>
          {downloading && (
            <progress className="required-update-progress" aria-label={t('更新下载进度', 'Update download progress')} max={100} value={status.progress} />
          )}
        </>
      )}

      {showFailure || !status.supported ? (
        <div className="required-update-actions">
          <button type="button" className="required-update-primary" autoFocus disabled={updating} onClick={() => void run(status.supported ? installUpdate : openUpdateDownloads)}>
            {status.supported ? t('重试更新', 'Retry update') : t('下载安装包', 'Download installer')}
          </button>
          <button type="button" className="required-update-secondary" disabled={updating} onClick={() => void run(checkUpdates)}>
            {t('重新检查', 'Check again')}
          </button>
        </div>
      ) : showReady ? (
        <button type="button" className="required-update-ready" autoFocus disabled={updating} onClick={() => void run(installUpdate)}>
          {updating ? t('更新中...', 'Updating...') : t('重启并更新', 'Restart and update')}
        </button>
      ) : (
        <button
          type="button"
          className="required-update-busy"
          autoFocus={!updating}
          disabled={updating}
          onClick={() => void run(installUpdate)}
        >
          {updating ? t('更新中...', 'Updating...') : t('立即更新', 'Update now')}
        </button>
      )}

      {showReady ? (
        <>
          <p className="required-update-footer">{t('更新完成后将重新启动 TokenDance；本机数据保留', 'TokenDance will restart after updating; local data is retained')}</p>
          <p className="required-update-footer">{t('主界面已锁定，请完成更新后继续', 'The main interface is locked until you finish updating')}</p>
        </>
      ) : showFailure ? (
        <>
          <p className="required-update-footer">{t('本机采集继续运行，数据仍保留', 'Local collection keeps running; your data is retained')}</p>
          <p className="required-update-footer">{t('必须更新后才能继续使用，无法跳过', 'You must update to continue. This cannot be skipped.')}</p>
        </>
      ) : (
        <p className="required-update-footer">{t('无法关闭此窗口，更新完成前无法使用主界面', 'This window cannot be closed. The main interface stays locked until the update finishes.')}</p>
      )}
    </section>
  </div>;
}
