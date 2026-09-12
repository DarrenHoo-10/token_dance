import React from 'react';
import { Link } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';

export const Footer: React.FC = () => {
  const { t, locale } = useLocale();

  return (
    <footer
      className="site-footer"
      style={{
        borderTop: '1px solid var(--border-light)',
        backgroundColor: 'var(--bg-surface)',
        padding: '24px 32px',
        marginTop: 'auto',
        fontSize: '12px',
        color: 'var(--text-muted)',
      }}
    >
      <div
        style={{
          maxWidth: 1360,
          margin: '0 auto',
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          flexWrap: 'wrap',
          gap: 12,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="TokenDance" style={{ width: 18, height: 18 }} />
          <strong>TokenDance</strong>
          <span>·</span>
          <span>{t('common.heroTagline')}</span>
        </div>

        <nav aria-label={locale === 'zh-CN' ? '帮助与下载' : 'Help and downloads'} style={{ display: 'flex', flexWrap: 'wrap', gap: 20, fontSize: 12 }}>
          <Link to="/download">{locale === 'zh-CN' ? '客户端下载' : 'Download'}</Link>
          <Link to="/docs/quickstart">{locale === 'zh-CN' ? '使用文档' : 'Docs'}</Link>
          <Link to="/docs/privacy">{locale === 'zh-CN' ? '数据与隐私' : 'Data & privacy'}</Link>
          <span>TOKENDANCE · 2026</span>
        </nav>
      </div>
    </footer>
  );
};
