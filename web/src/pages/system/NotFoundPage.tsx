import React from 'react';
import { useNavigate } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { Button } from '@/components/common/Button';

export const NotFoundPage: React.FC = () => {
  const { t } = useLocale();
  const navigate = useNavigate();

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        minHeight: '60vh',
        textAlign: 'center',
        padding: '32px',
      }}
    >
      <div
        className="mono-num"
        style={{ fontSize: 72, fontWeight: 900, letterSpacing: '-0.06em', color: 'var(--text-main)' }}
      >
        404
      </div>
      <h2 style={{ fontSize: 24, margin: '12px 0 6px' }}>{t('states.notFoundTitle')}</h2>
      <p className="text-muted" style={{ fontSize: 14, maxWidth: 400, marginBottom: 24 }}>
        {t('states.notFoundDesc')}
      </p>
      <Button variant="dark" onClick={() => navigate('/')}>
        {t('states.goHome')}
      </Button>
    </div>
  );
};
