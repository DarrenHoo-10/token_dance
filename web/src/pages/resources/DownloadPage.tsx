import { ArrowDownToLine, ArrowUpRight } from 'lucide-react';
import { Link } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { useWindowsRelease, type WindowsRelease } from './windowsRelease';
import { useMacReleases, type MacRelease } from './macosRelease';
import { useResourceNavigation } from './useResourceNavigation';
import './resources.css';
import './download.css';

type Package = {
  id: string; platform: 'windows' | 'mac'; name: string; architecture: string;
  requirement: string; format: string; label: string; url?: string;
  status: 'loading' | 'ready' | 'empty' | 'error'; release: WindowsRelease | MacRelease | null;
  retry: () => void;
};

function PlatformIcon({ platform }: { platform: Package['platform'] }) {
  return <svg className="download-platform-icon" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    {platform === 'windows'
      ? <path d="M2 3h9v9H2zm11 0h9v9h-9zM2 14h9v9H2zm11 0h9v9h-9z" />
      : <path d="M17.05 12.54c.02 3.18 2.79 4.23 2.82 4.25-.02.07-.44 1.52-1.46 3.01-.89 1.29-1.81 2.58-3.27 2.61-1.43.04-1.89-.85-3.53-.85-1.64 0-2.15.83-3.51.88-1.41.06-2.48-1.4-3.37-2.68C2.9 17.14 1.51 12.35 3.4 9.1a5.23 5.23 0 0 1 4.42-2.68c1.38-.03 2.68.94 3.53.94.85 0 2.44-1.16 4.11-.99.7.03 2.66.28 3.92 2.12-.1.06-2.34 1.37-2.33 4.05ZM14.36 4.59c.74-.9 1.24-2.14 1.1-3.39-1.07.04-2.37.71-3.14 1.61-.69.8-1.29 2.08-1.13 3.3 1.19.09 2.41-.61 3.17-1.52Z" />}
  </svg>;
}

function PackageCard({ item, zh }: { item: Package; zh: boolean }) {
  const t = (cn: string, en: string) => zh ? cn : en;
  const unsigned = item.release && 'notarized' in item.release && !item.release.notarized;
  return <article className="download-package" aria-label={`${item.name} ${item.architecture}`} aria-busy={item.status === 'loading'}>
    <PlatformIcon platform={item.platform} />
    <h2>{item.name}</h2>
    <p className="download-architecture">{item.architecture}</p>
    <p className="download-requirement">{item.requirement}</p>
    <div className="download-package-action">
      {item.release && item.url
        ? <a className="download-button" href={item.url} aria-label={item.label}><ArrowDownToLine size={17} aria-hidden="true" />{t('下载', 'Download')} <span>{item.format}</span></a>
        : <button className="download-button" disabled={item.status !== 'error'} onClick={item.retry} aria-label={item.status === 'error' ? t(`重新获取 ${item.name} ${item.architecture}`, `Retry ${item.name} ${item.architecture}`) : undefined}>
          {item.status === 'loading' ? t('正在获取版本…', 'Checking release…') : item.status === 'error' ? t('重新获取', 'Retry') : t('暂未发布', 'Coming soon')}
        </button>}
      <p className="download-version" role={item.status === 'error' ? 'alert' : 'status'}>
        {item.release ? <>v{item.release.version} · {(item.release.bytes / 1_000_000).toFixed(1)} MB{unsigned && <> · {t('未公证', 'Not notarized')}</>}</>
          : item.status === 'error' ? t('暂时无法获取最新版本', 'Unable to check this release') : item.status === 'empty' ? t('安装包准备中', 'Package not yet available') : '\u00a0'}
      </p>
    </div>
  </article>;
}

export function DownloadPage() {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  const t = (cn: string, en: string) => zh ? cn : en;
  const windows = useWindowsRelease();
  const mac = useMacReleases();
  useResourceNavigation(t('下载 TokenDance', 'Download TokenDance'));
  const packages: Package[] = [
    { id: 'windows', platform: 'windows', name: 'Windows', architecture: 'x64', requirement: t('64 位 Windows', '64-bit Windows'), format: 'EXE', label: t('下载 Windows 版', 'Download for Windows'), url: windows.release?.exeUrl, ...windows },
    { id: 'mac-arm64', platform: 'mac', name: 'macOS', architecture: 'Apple Silicon', requirement: `macOS ${mac.arm64.release?.minimumSystemVersion ?? '13.0'}+ · ${t('M 系列芯片', 'M-series chips')}`, format: 'DMG', label: t('下载 Apple Silicon DMG', 'Download Apple Silicon DMG'), url: mac.arm64.release?.dmgUrl, ...mac.arm64, retry: mac.retry },
    { id: 'mac-intel', platform: 'mac', name: 'macOS', architecture: 'Intel', requirement: `macOS ${mac.x64.release?.minimumSystemVersion ?? '13.0'}+ · ${t('Intel 芯片', 'Intel chips')}`, format: 'DMG', label: t('下载 Intel DMG', 'Download Intel DMG'), url: mac.x64.release?.dmgUrl, ...mac.x64, retry: mac.retry },
  ];
  const unsigned = [mac.arm64, mac.x64].some(({ release }) => release && !release.notarized);
  return <div className="desktop-resources download-page">
    <header className="download-page-heading">
      <h1>{t('下载 TokenDance', 'Download TokenDance')}</h1>
      <p>{t('在桌面上，轻松查看你的 AI 工具用量。', 'Your AI tool usage, right on your desktop.')}</p>
    </header>
    <section className="download-packages" aria-label={t('选择你的平台', 'Choose your platform')}>
      {packages.map(item => <PackageCard key={item.id} item={item} zh={zh} />)}
    </section>
    <div className="download-install-note">
      <p>{t('Windows 下载后直接运行；Mac 打开 DMG，拖入 Applications。', 'Run the app on Windows. On Mac, open the DMG and drag TokenDance to Applications.')}</p>
      {unsigned && <p>{t('Mac 未公证版本：首次打开若被阻止，请到「系统设置 → 隐私与安全」选择「仍要打开」。', 'Mac builds are not notarized. If blocked on first launch, choose Open Anyway in System Settings → Privacy & Security.')}</p>}
    </div>
    <footer className="download-page-footer">
      <Link to="/docs/install">{t('安装指南', 'Installation guide')}<ArrowUpRight size={14} aria-hidden="true" /></Link>
      <a href="https://github.com/DarrenHoo-10/token_dance/releases" target="_blank" rel="noopener noreferrer">{t('版本记录', 'Release history')}<ArrowUpRight size={14} aria-hidden="true" /></a>
      {packages.some(item => item.release) && <details className="download-checksums">
        <summary>{t('校验信息', 'Checksums')}</summary>
        <div>{packages.filter(item => item.release).map(item => <p key={item.id}><strong>{item.name} · {item.architecture} · v{item.release!.version}</strong><code>SHA-256: {item.release!.sha256}</code>{item.platform === 'windows' && windows.release?.zipUrl && <a href={windows.release.zipUrl}>{t('下载 ZIP 压缩包', 'Download ZIP')}</a>}</p>)}</div>
      </details>}
    </footer>
  </div>;
}
