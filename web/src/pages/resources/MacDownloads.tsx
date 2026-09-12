import { ArrowDownToLine } from 'lucide-react';
import { Link } from 'react-router-dom';
import { useMacReleases, type MacReleaseState } from './macosRelease';

function Package({ name, detail, result, zh }: { name: string; detail: string; result: MacReleaseState; zh: boolean }) {
  const { release, status } = result;
  return <article className="mac-package" aria-label={`macOS ${name}`} aria-busy={status === 'loading'}>
    <h3>{name}</h3><p>{detail}</p>
    {release ? <>
      <p className="resource-pill">{release.notarized ? (zh ? '已通过 Apple 公证' : 'Notarized by Apple') : (zh ? '未公证版本 · 免费分发' : 'Not notarized · Free distribution')}</p>
      {!release.notarized && <p>{zh ? '首次打开若被 macOS 阻止：确认下载来源后，在“系统设置 → 隐私与安全”中选择“仍要打开”。' : 'If macOS blocks first launch, verify the source and choose Open Anyway in System Settings → Privacy & Security.'}</p>}
      <p className="mac-package-meta">v{release.version} · macOS {release.minimumSystemVersion}+ · {(release.bytes / 1024 / 1024).toFixed(1)} MiB{release.prerelease ? (zh ? ' · 预览版' : ' · Preview') : ''}</p>
      <a className="resource-button resource-button-dark" href={release.dmgUrl}>{zh ? `下载 ${name} DMG` : `Download ${name} DMG`}<ArrowDownToLine size={16} aria-hidden="true" /></a>
      <div className="release-details"><span>{release.publishedAt}</span><span className="release-checksum">SHA-256: <code>{release.sha256}</code></span></div>
      <details className="release-notes"><summary>{zh ? '更新说明' : 'Release notes'}</summary><p>{release.notes}</p></details>
    </> : <p className="release-status" role="status">{status === 'loading' ? (zh ? '正在获取版本…' : 'Checking releases…') : status === 'empty' ? (zh ? '该架构暂未发布安装包。' : 'No package has been published for this architecture.') : (zh ? '暂时无法获取安装包信息。' : 'Unable to verify this package right now.')}</p>}
  </article>;
}

export function MacDownloads({ zh }: { zh: boolean }) {
  const releases = useMacReleases();
  return <section className="mac-download" aria-labelledby="macos-heading">
    <div><h2 id="macos-heading">macOS</h2><p>{zh ? '打开 DMG，将 TokenDance 拖入 Applications，再从“应用程序”启动。安装包以实际发布版本为准。' : 'Open the DMG, drag TokenDance to Applications, then launch it from Applications. Packages appear when published.'}</p></div>
    <div className="mac-download-grid">
      <Package name="Apple Silicon" detail={zh ? '适用于搭载 M 系列芯片的 Mac' : 'For Macs with an M-series chip'} result={releases.arm64} zh={zh} />
      <Package name="Intel" detail={zh ? '适用于搭载 Intel 处理器的 Mac' : 'For Macs with an Intel processor'} result={releases.x64} zh={zh} />
    </div>
    <div className="resource-actions mac-download-help">
      <Link className="resource-link" to="/docs/install#macos">{zh ? 'Mac 安装说明' : 'Mac installation guide'}</Link>
      {(releases.arm64.status === 'error' || releases.x64.status === 'error') && <button className="resource-button" onClick={releases.retry}>{zh ? '重新获取 Mac 版本' : 'Retry Mac releases'}</button>}
      <p className="resource-small">{zh ? '不确定芯片类型？在苹果菜单 → 关于本机中查看“芯片”或“处理器”。' : 'Check Apple menu → About This Mac for Chip or Processor.'}</p>
    </div>
  </section>;
}
