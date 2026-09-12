import { Fragment, useEffect, useRef } from 'react';
import { Link, NavLink, useLocation, useParams } from 'react-router-dom';
import { ArrowDownToLine, ArrowRight, ArrowLeft } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import { NotFoundPage } from '@/pages/system/NotFoundPage';
import { getArticles, type DocSection } from './docsContent';
import { useResourceNavigation } from './useResourceNavigation';
import './resources.css';

function SectionBody({ section }: { section: DocSection }) {
  return <>
    {section.paragraphs?.map(text => <p key={text}>{text}</p>)}
    {section.note && <div className="doc-callout">{section.note}</div>}
    {section.rows && <div className="doc-table-wrap"><table className="doc-table"><thead><tr>{section.columns?.map(label => <th scope="col" key={label}>{label}</th>)}</tr></thead><tbody>{section.rows.map(row => <tr key={row[0]}>{row.map((cell, i) => <td key={i}>{cell}</td>)}</tr>)}</tbody></table></div>}
    {section.action && <Link className="resource-button" to={section.action.to}>{section.action.label}<ArrowRight size={15} aria-hidden="true" /></Link>}
  </>;
}

export function DocsPage() {
  const { locale } = useLocale();
  const zh = locale === 'zh-CN';
  const { slug } = useParams();
  const { hash } = useLocation();
  const articles = getArticles(zh);
  const article = articles.find(item => item.slug === slug);
  const next = article ? articles[(articles.indexOf(article) + 1) % articles.length] : undefined;
  const articleRef = useRef<HTMLElement>(null);
  useResourceNavigation(article?.label ?? (zh ? '文档未找到' : 'Document not found'));
  useEffect(() => {
    // Open the target FAQ before the shared navigation effect scrolls to it.
    const details = articleRef.current?.querySelector<HTMLDetailsElement>(`details[id="${hash.slice(1).replace(/[^a-z-]/g, '')}"]`);
    if (details) details.open = true;
  }, [hash, slug]);
  if (!article) return <NotFoundPage />;
  return <div className="desktop-resources">
    <header className="docs-heading"><div><h1>{zh ? '使用文档' : 'Documentation'}</h1><p>{zh ? '从第一次接入，到读懂每一份用量。' : 'From your first connection to understanding your usage.'}</p></div><Link className="resource-link" to="/download">{zh ? '获取 Windows 桌面客户端' : 'Get the Windows desktop app'}<ArrowDownToLine size={16} aria-hidden="true" /></Link></header>
    <div className="docs-layout">
      <nav className="docs-menu" aria-label={zh ? '文档导航' : 'Documentation navigation'}>
        {articles.map((item, index) => <Fragment key={item.slug}>{item.group !== articles[index - 1]?.group && <div className="doc-menu-group">{item.group}</div>}<NavLink to={`/docs/${item.slug}`} className={({ isActive }) => isActive ? 'active' : ''}>{item.label}</NavLink></Fragment>)}
        <div className="doc-help-box"><p>{zh ? '还没安装客户端？' : 'Need the desktop app?'}</p><Link className="resource-link" to="/download">{zh ? '前往下载页' : 'Go to downloads'}<ArrowRight size={14} aria-hidden="true" /></Link></div>
      </nav>
      <article className="doc-article" ref={articleRef}>
        <div className="doc-breadcrumb">{zh ? '使用文档' : 'Docs'} / {article.group}</div><h2>{article.title}</h2><p className="doc-lead">{article.lead}</p>
        {article.sections.map((section, index) => article.slug === 'faq' ? <details className="doc-faq" key={section.id} id={section.id} open={index === 0 || hash === `#${section.id}`}><summary>{section.title}</summary><SectionBody section={section} /></details> : <section className="doc-section" key={section.id} id={section.id}><h3>{section.title}</h3><SectionBody section={section} /></section>)}
        {article.slug === 'releases' && <Link className="resource-link" to="/download">{zh ? '查看当前版本与更新说明' : 'View the current version and release notes'}<ArrowRight size={15} aria-hidden="true" /></Link>}
        <div className="doc-next"><Link className="resource-link" to="/leaderboard"><ArrowLeft size={15} aria-hidden="true" />{zh ? '返回排行榜' : 'Back to leaderboard'}</Link>{next && <Link to={`/docs/${next.slug}`}><small>{zh ? '继续阅读' : 'Read next'}</small><strong>{next.label} →</strong></Link>}</div>
      </article>
      <nav className="doc-toc" aria-label={zh ? '本页目录' : 'On this page'}><strong>{zh ? '本页内容' : 'On this page'}</strong>{article.sections.map(section => <Link key={section.id} to={`#${section.id}`} aria-current={hash === `#${section.id}` ? 'location' : undefined}>{section.title.replace(/^\d\. /, '')}</Link>)}</nav>
    </div>
  </div>;
}
