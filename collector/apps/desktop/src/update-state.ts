import { invoke } from '@tauri-apps/api/core';
import { useEffect, useState } from 'react';
import { isTauriEnvironment } from './tauri-bridge';

export interface UpdateStatus {
  currentVersion: string;
  version: string | null;
  notes: string;
  publishedAt: string | null;
  phase: 'idle' | 'checking' | 'latest' | 'available' | 'downloading' | 'ready' | 'installing' | 'error';
  autoUpdate: boolean;
  progress: number;
  checkedAt: string | null;
  error: string | null;
  supported: boolean;
}
const demo: UpdateStatus = { currentVersion: '0.1.12', version: null, notes: '', publishedAt: null, phase: 'idle', autoUpdate: true, progress: 0, checkedAt: null, error: null, supported: true };
let current: UpdateStatus | null = null;
const subscribers = new Set<(value: UpdateStatus | null) => void>();
let timer: ReturnType<typeof setInterval> | undefined;
let reading = false;
function publish(value: UpdateStatus) { current = value; subscribers.forEach(callback => callback(value)); }
async function refresh() {
  if (reading) return;
  reading = true;
  try { publish(isTauriEnvironment() ? await invoke<UpdateStatus>('get_update_status') : { ...demo }); }
  catch { /* Update polling must never prevent first paint or collector use. */ }
  finally { reading = false; }
}
export function useUpdates() {
  const [status, setStatus] = useState(current);
  useEffect(() => {
    subscribers.add(setStatus);
    setStatus(current);
    if (!timer) { void refresh(); timer = setInterval(() => void refresh(), 1000); }
    return () => { subscribers.delete(setStatus); if (!subscribers.size) { clearInterval(timer); timer = undefined; } };
  }, []);
  return status;
}
export async function checkUpdates() {
  if (isTauriEnvironment()) publish(await invoke<UpdateStatus>('check_for_updates'));
  else { demo.phase = 'latest'; demo.checkedAt = new Date().toISOString(); publish({ ...demo }); }
}
export async function setAutoUpdate(enabled: boolean) {
  if (isTauriEnvironment()) publish(await invoke<UpdateStatus>('set_auto_update', { enabled }));
  else { demo.autoUpdate = enabled; publish({ ...demo }); }
}
export async function installUpdate() { if (isTauriEnvironment()) await invoke('install_update'); }

export function updateBusy(status: UpdateStatus | null) { return !!status && ['checking', 'downloading', 'installing'].includes(status.phase); }
export function updateError(code: string | null, zh: boolean): string {
  const errors: Record<string, [string, string]> = {
    network: ['网络暂不可用，请稍后重试', 'Network unavailable. Try again later.'],
    rate_limited: ['检查过于频繁，请稍后重试', 'Too many requests. Try again later.'],
    asset_missing: ['新版安装包尚未就绪，请稍后重试', 'The new package is not ready yet.'],
    unverified_release: ['新版校验信息不完整，暂不能更新', 'Release verification is unavailable.'],
    integrity: ['安装包校验未通过，请重试', 'Package verification failed. Please retry.'],
    storage: ['无法保存更新，请检查磁盘空间和目录权限', 'Cannot save update. Check disk space and permissions.'],
    install_failed: ['无法替换程序，请检查程序目录权限后重试', 'Cannot replace the app. Check folder permissions and retry.'],
    restore_failed: ['更新未完成，请从官网下载程序重新运行，数据仍保留在本机', 'Update could not complete. Download the app again; your local data is preserved.'],
    busy: ['更新正在进行，请稍候', 'An update is already in progress.'],
    no_update: ['当前已是最新版本', 'You are up to date.'],
  };
  return (errors[code ?? ''] ?? ['暂时无法检查更新，请重试', 'Cannot check for updates. Please retry.'])[zh ? 0 : 1];
}
export function updateStatusText(status: UpdateStatus, zh: boolean): string {
  const t = (cn: string, en: string) => zh ? cn : en;
  if (!status.supported) return t('此平台暂不支持应用内更新', 'In-app updates are not available on this platform yet.');
  switch (status.phase) {
    case 'checking': return t('正在检查更新…', 'Checking for updates…');
    case 'downloading': return t(`正在后台下载 · ${status.progress}%`, `Downloading in background · ${status.progress}%`);
    case 'installing': return t('正在更新，即将重新启动…', 'Installing. Restarting shortly…');
    case 'ready': return status.autoUpdate ? t('已准备好，下次启动时自动安装', 'Ready. Installs on next launch.') : t('已准备好，可立即更新', 'Ready to install.');
    case 'latest': return t('已是最新版本', 'You are up to date');
    case 'available': return t(`发现新版 v${status.version}`, `Version ${status.version} available`);
    case 'error': return updateError(status.error, zh);
    default: return t('自动检查新版本', 'Checks for new versions automatically');
  }
}
