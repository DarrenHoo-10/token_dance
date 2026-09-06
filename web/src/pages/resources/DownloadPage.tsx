import { ArrowDownToLine, ArrowRight, ArrowUpRight, Monitor, Minus } from 'lucide-react';
import { Link } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { releasesUrl, useWindowsRelease } from './windowsRelease';
import { useResourceNavigation } from './useResourceNavigation';
import './resources.css';

function DesktopPreview({ zh }: { zh: boolean }) {
  return <figure className="desktop-art">
    <div className="art-caption"><span>{zh ? '你的用量，一眼看清' : 'Your usage, at a glance'}</span><span>{zh ? '桌面面板' : 'Desktop panel'}</span></div>
    <div className="app-preview">
      <div className="app-title"><span><img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="" />TokenDance</span><Minus size={14} aria-hidden="true" /></div>
      <div className="app-periods"><span>{zh ? '今日' : 'Today'}</span><span>{zh ? '近 7 天' : '7 days'}</span><span>{zh ? '全部时间' : 'All time'}</span></div>
      <span className="resource-small resource-muted">{zh ? '今日 Token' : 'Today’s tokens'}</span><div className="app-value">1.42M</div>
      <div className="preview-chart" role="img" aria-label={zh ? '示例七日用量趋势' : 'Example seven-day usage trend'}>{[28, 49, 39, 70, 53, 83, 64].map((height, i) => <i key={i} style={{ height: `${height}%` }} />)}</div>
      <div className="chart-label">{(zh ? ['一', '二', '三', '四', '五', '六', '日'] : ['M', 'T', 'W', 'T', 'F', 'S', 'S']).map((day, i) => <span key={i}>{day}</span>)}</div>
      {[['Codex', '824.6K'], ['Claude Code', '421.2K'], ['Cursor', '174.2K']].map(([name, value]) => <div className="app-tool" key={name}><span>{name}</span><span>{value}</span></div>)}
      <div className="app-status"><span>{zh ? '采集中' : 'Collecting'}</span><span>{zh ? '已同步' : 'Synced'}</span></div>
    </div>
    <figcaption>{zh ? '面板示意 · 示例数据' : 'Panel illustration · example data'}</figcaption>
  </figure>;
}

export function DownloadPage() {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  const { release, status, retry } = useWindowsRelease();
  useResourceNavigation(zh ? '下载桌面客户端' : 'Download the desktop app');
  return <div className="desktop-resources">
    <section className="download-hero">
      <div className="download-copy"><div className="resource-eyebrow">TOKENDANCE DESKTOP</div>
        <h1>{zh ? '专注创造。' : 'Focus on creating.'}<br /><span>{zh ? '让用量自动记录。' : 'Let usage track itself.'}</span></h1>
        <p className="resource-intro">{zh ? '一个轻量的 Windows 桌面客户端，汇总你的 AI 工具用量。留在托盘里，与你的每一次创造同行。' : 'A lightweight Windows desktop app for your AI tool usage. Quietly in your system tray, ready whenever you need it.'}</p>
        <div className="source-list" aria-label={zh ? '主要采集来源' : 'Main supported tools'}>{['Codex', 'Claude Code', 'Cursor', 'Grok Build', 'ZCode'].map(name => <span key={name}>{name}</span>)}</div>
        <div className="resource-actions">{release ? <a className="resource-button resource-button-primary" href={release.exeUrl}>{zh ? '下载 Windows 版' : 'Download for Windows'}<ArrowDownToLine size={17} aria-hidden="true" /></a> : <a className="resource-button resource-button-primary" href="#windows-heading">{zh ? '获取 Windows 客户端' : 'Get the Windows app'}<ArrowDownToLine size={17} aria-hidden="true" /></a>}<Link className="resource-link" to="/docs/quickstart">{zh ? '阅读安装指南' : 'Read the setup guide'}<ArrowUpRight size={16} aria-hidden="true" /></Link></div>
        <p className="download-meta">Windows x64{release && ` · v${release.version} · ${release.prerelease ? (zh ? '预览版' : 'Preview') : (zh ? '正式版' : 'Stable')}`}</p>
        <p className="resource-small resource-muted">{zh ? '不上传提示词、模型回复或代码正文。' : 'Prompts, model responses and source code are not uploaded.'}</p>
      </div><DesktopPreview zh={zh} />
    </section>
    <section className="windows-download" aria-labelledby="windows-heading">
      <div><h2 id="windows-heading">{zh ? '下载桌面客户端' : 'Get the desktop app'}</h2><p>{zh ? '目前仅支持 Windows。一个客户端连接多种 AI 工具，无需分别安装扩展。' : 'Currently available for Windows only. One desktop app connects multiple AI tools.'}</p></div>
      <div className="windows-release" aria-busy={status === 'loading'}>
        <div className="windows-heading"><span className="windows-icon"><Monitor size={27} aria-hidden="true" /></span><div><h3>Windows <span className="resource-pill">{zh ? '便携版' : 'Portable'}</span></h3><p>x64 · .exe{release && ` · ${(release.bytes / 1024 / 1024).toFixed(1)} MiB`}</p></div></div>
        <p className="release-description">{zh ? '下载后运行，常驻系统托盘。更新前请先从托盘菜单退出旧版本。' : 'Run the downloaded app to open it in your system tray. Quit the previous version from the tray menu before updating.'}</p>
        {release ? <><div className="resource-actions"><a className="resource-button resource-button-dark" href={release.exeUrl}>{zh ? '下载 Windows 版' : 'Download for Windows'}<ArrowDownToLine size={16} aria-hidden="true" /></a>{release.zipUrl && <a className="resource-link" href={release.zipUrl}>{zh ? '下载 ZIP 压缩包' : 'Download ZIP'}</a>}<Link className="resource-link" to="/docs/install">{zh ? '安装说明' : 'Installation guide'}<ArrowUpRight size={15} aria-hidden="true" /></Link></div>
        <div className="release-details"><span>v{release.version} · {release.publishedAt} · {release.prerelease ? (zh ? '预览版' : 'Preview') : (zh ? '正式版' : 'Stable')}</span><a href={release.releaseUrl} target="_blank" rel="noreferrer">{zh ? 'GitHub 发布说明' : 'GitHub release notes'} ↗</a>{release.checksumsUrl && <a href={release.checksumsUrl}>{zh ? 'SHA-256 校验文件' : 'SHA-256 checksums'}</a>}</div></> : <div className="release-status" role={status === 'error' ? 'alert' : 'status'}>
          <p>{status === 'loading' ? (zh ? '正在获取最新 Windows 版本…' : 'Checking the latest Windows release…') : status === 'empty' ? (zh ? '暂未发布 Windows 客户端。' : 'No Windows release is available yet.') : (zh ? '暂时无法获取最新版本，请重试或前往发布页下载。' : 'Unable to check the latest release. Retry or download from the releases page.')}</p>
          <div className="resource-actions">{status !== 'loading' && <button className="resource-button" onClick={retry}>{zh ? '重新获取' : 'Retry'}</button>}<a className="resource-link" href={releasesUrl} target="_blank" rel="noreferrer">{zh ? '前往 GitHub 发布页' : 'Open GitHub releases'}<ArrowUpRight size={15} aria-hidden="true" /></a></div>
        </div>}
      </div>
    </section>
    <section className="resource-start-strip"><div><h2>{zh ? '第一次使用？从这里开始。' : 'First time? Start here.'}</h2><p>{zh ? '获取客户端 → 登录账号 → 确认采集 → 查看榜单' : 'Get the app → Sign in → Check collection → Open the board'}</p></div><Link className="resource-button" to="/docs/quickstart">{zh ? '打开接入指南' : 'Open the setup guide'}<ArrowRight size={16} aria-hidden="true" /></Link></section>
    <section className="resource-privacy-strip"><h2>{zh ? '你的内容，留在你的设备上。' : 'Your content stays on your device.'}</h2><div><p>{zh ? 'TokenDance 同步经过隐私过滤的用量记录。排行榜会展示头像、昵称、Token 和排名；详细资料页的公开设置单独管理。' : 'TokenDance syncs privacy-filtered usage records. The leaderboard displays your avatar, name, tokens and rank. Detailed profile visibility is managed separately.'}</p><Link className="resource-link" to="/docs/privacy">{zh ? '了解数据与隐私' : 'Learn about data & privacy'}<ArrowRight size={15} aria-hidden="true" /></Link></div></section>
  </div>;
}
