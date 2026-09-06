import { ArrowDownToLine, ArrowUpRight } from 'lucide-react';
import { Link } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import './resources.css';

export function DesktopDownloadCard() {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  return <section className="side-card desktop-connect">
    <ArrowDownToLine className="connect-icon" size={25} aria-hidden="true" />
    <h2>{zh ? <>让你的用量，<br />也加入这张榜单。</> : 'Bring your usage to the board.'}</h2>
    <p>{zh ? '安装 Windows 桌面客户端，连接本机工具，自动同步用量。' : 'Connect your local tools and sync usage with the Windows desktop app.'}</p>
    <Link className="resource-button resource-button-dark" to="/download">{zh ? '获取桌面客户端' : 'Get the desktop app'}<ArrowDownToLine size={16} aria-hidden="true" /></Link>
    <Link className="resource-link" to="/docs/quickstart">{zh ? '第一次使用？查看接入指南' : 'New here? Read the setup guide'}<ArrowUpRight size={14} aria-hidden="true" /></Link>
  </section>;
}
