import React from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { Button } from '@/components/common/Button';

export const UnauthorizedState: React.FC = () => {
  const { t } = useLocale();
  const navigate = useNavigate();
  const location = useLocation();

  const handleLogin = () => {
    const returnTo = encodeURIComponent(location.pathname + location.search);
    navigate(`/login?return_to=${returnTo}`);
  };

  return (
    <div className="state-box" style={{ maxWidth: 500, margin: '60px auto' }}>
      <div
        style={{
          width: 48,
          height: 48,
          borderRadius: '50%',
          backgroundColor: 'var(--bg-subtle)',
          display: 'grid',
          placeItems: 'center',
          fontSize: 20,
          marginBottom: 8,
        }}
      >
        🔒
      </div>
      <h3>{t('states.unauthorizedTitle')}</h3>
      <p>{t('states.unauthorizedDesc')}</p>
      <div style={{ display: 'flex', gap: 10 }}>
        <Button variant="primary" onClick={handleLogin}>
          {t('states.loginButton')}
        </Button>
        <Button variant="outline" onClick={() => navigate('/leaderboard')}>
          {t('nav.leaderboard')}
        </Button>
      </div>
    </div>
  );
};
