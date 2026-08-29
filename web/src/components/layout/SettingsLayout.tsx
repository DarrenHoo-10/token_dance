import React from 'react';
import { NavLink, Outlet, Navigate } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { UnauthorizedState } from '@/components/states/UnauthorizedState';

export const SettingsLayout: React.FC = () => {
  const { user, authenticated, loading } = useAuth();
  const { t } = useLocale();

  if (loading) {
    return <LoadingState />;
  }

  if (!authenticated || !user) {
    return <UnauthorizedState />;
  }

  if (user.onboardingRequired || user.productState === 'new') {
    return <Navigate to="/onboarding" replace />;
  }

  return (
    <div>
      <div style={{ marginBottom: 24 }}>
        <p className="eyebrow">{t('nav.settings')}</p>
        <h1>{t('settings.headline')}</h1>
        <p className="text-muted">{t('settings.subheadline')}</p>
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '220px 1fr', gap: 24 }}>
        <aside>
          <div
            className="panel"
            style={{
              padding: '12px',
              display: 'flex',
              flexDirection: 'column',
              gap: 4,
            }}
          >
            <div
              style={{
                fontSize: 11,
                fontWeight: 700,
                color: 'var(--text-subtle)',
                textTransform: 'uppercase',
                padding: '6px 12px',
              }}
            >
              {t('nav.settings')}
            </div>

            <NavLink
              to="/settings/profile"
              className={({ isActive }) =>
                `btn btn-ghost ${isActive ? 'btn-dark' : ''}`
              }
              style={{ justifyContent: 'flex-start', textAlign: 'left', width: '100%' }}
            >
              {t('settings.tabProfile')}
            </NavLink>

            <NavLink
              to="/settings/privacy"
              className={({ isActive }) =>
                `btn btn-ghost ${isActive ? 'btn-dark' : ''}`
              }
              style={{ justifyContent: 'flex-start', textAlign: 'left', width: '100%' }}
            >
              {t('settings.tabPrivacy')}
            </NavLink>

            <NavLink
              to="/settings/devices"
              className={({ isActive }) =>
                `btn btn-ghost ${isActive ? 'btn-dark' : ''}`
              }
              style={{ justifyContent: 'flex-start', textAlign: 'left', width: '100%' }}
            >
              {t('settings.tabDevices')}
            </NavLink>

            <NavLink
              to="/settings/exports"
              className={({ isActive }) =>
                `btn btn-ghost ${isActive ? 'btn-dark' : ''}`
              }
              style={{ justifyContent: 'flex-start', textAlign: 'left', width: '100%' }}
            >
              {t('settings.tabExports')}
            </NavLink>
          </div>
        </aside>

        <main>
          <Outlet />
        </main>
      </div>
    </div>
  );
};
