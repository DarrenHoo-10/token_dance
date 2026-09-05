import React from 'react';
import { useLocale } from '@/context/LocaleContext';

export const Footer: React.FC = () => {
  const { t } = useLocale();

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

        <div style={{ display: 'flex', gap: 20, fontFamily: 'var(--font-mono)', fontSize: 11 }}>
          <span>TOKENDANCE · 2026</span>
          <span>{t('auth.privacyPledge')}</span>
        </div>
      </div>
    </footer>
  );
};
