import React, { useEffect, useState } from 'react';
import { NavLink, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';

function safeReturnTo(value: string | null): string | undefined {
  if (!value || !value.startsWith('/') || value.startsWith('//')) {
    return undefined;
  }
  return value;
}

export const LoginPage: React.FC = () => {
  const { login, authenticated, loading: sessionLoading } = useAuth();
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const rawReturnTo = searchParams.get('return_to');

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [keepSignedIn, setKeepSignedIn] = useState(true);
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  useEffect(() => {
    if (sessionLoading || !authenticated) {
      return;
    }
    navigate(safeReturnTo(rawReturnTo) || '/', { replace: true });
  }, [authenticated, navigate, rawReturnTo, sessionLoading]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password) {
      setErrorMessage(t('errors.http_400'));
      return;
    }

    try {
      setLoading(true);
      setErrorMessage(null);
      const res = await login({
        email,
        password,
        returnTo: safeReturnTo(rawReturnTo),
        keepSignedIn,
      });
      showToast(t('auth.loginSuccess'), 'success');
      const target = res?.returnTo || '/';
      if (res?.user?.onboardingRequired || res?.user?.productState === 'new') {
        navigate(`/onboarding?return_to=${encodeURIComponent(target)}`);
      } else {
        navigate(target);
      }
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(getApiErrorMessage(t, err));
      } else if (err instanceof Error) {
        setErrorMessage(err.message);
      } else {
        setErrorMessage(t('errors.unknown'));
      }
    } finally {
      setLoading(false);
    }
  };

  if (sessionLoading || authenticated) {
    return null;
  }

  return (
    <div
      style={{
        display: 'grid',
        gridTemplateColumns: 'minmax(320px, 1fr) minmax(360px, 1fr)',
        minHeight: '100vh',
        backgroundColor: 'var(--bg-app)',
      }}
    >
      {/* Left editorial brand panel */}
      <aside
        style={{
          backgroundColor: 'var(--bg-dark)',
          color: 'var(--text-inverse)',
          padding: '48px',
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'space-between',
        }}
      >
        <div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 22, fontWeight: 800 }}>
            <img src="/logo-tokendance-v2.png" alt="TokenDance" style={{ width: 36, height: 36 }} />
            <span>TokenDance</span>
          </div>

          <div style={{ marginTop: 120, maxWidth: 440 }}>
            <p className="eyebrow" style={{ color: 'var(--lime)' }}>
              {t('common.brandTagline')}
            </p>
            <h1 style={{ fontSize: 48, color: 'white', letterSpacing: '-0.05em', lineHeight: 1.05 }}>
              {t('auth.loginHeroLine1')} <br />
              <span style={{ color: 'var(--lime)' }}>{t('auth.loginHeroLine2')}</span>
            </h1>
            <p style={{ marginTop: 20, color: '#b2bbb4', fontSize: 16, lineHeight: 1.6 }}>
              {t('common.heroSub')}
            </p>
          </div>
        </div>

        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(3, 1fr)',
            gap: 12,
            borderTop: '1px solid var(--border-dark)',
            paddingTop: 24,
          }}
        >
          <div>
            <strong style={{ display: 'block', fontSize: 15 }}>{t('auth.editorialPrivacyTitle')}</strong>
            <small style={{ display: 'block', color: '#88928a', fontSize: 11, marginTop: 4 }}>
              {t('auth.editorialPrivacyCopy')}
            </small>
          </div>
          <div>
            <strong style={{ display: 'block', fontSize: 15 }}>{t('auth.editorialControlTitle')}</strong>
            <small style={{ display: 'block', color: '#88928a', fontSize: 11, marginTop: 4 }}>
              {t('auth.editorialControlCopy')}
            </small>
          </div>
          <div>
            <strong style={{ display: 'block', fontSize: 15 }}>{t('auth.editorialInsightTitle')}</strong>
            <small style={{ display: 'block', color: '#88928a', fontSize: 11, marginTop: 4 }}>
              {t('auth.editorialInsightCopy')}
            </small>
          </div>
        </div>
      </aside>

      {/* Right sign in form */}
      <main
        style={{
          display: 'flex',
          flexDirection: 'column',
          justifyContent: 'center',
          alignItems: 'center',
          padding: '48px 32px',
          position: 'relative',
        }}
      >
        <div style={{ position: 'absolute', top: 24, right: 32 }}>
          <LocaleSwitcher />
        </div>

        <div style={{ width: '100%', maxWidth: 400 }}>
          <div style={{ marginBottom: 28 }}>
            <p className="eyebrow">{t('common.appName')}</p>
            <h2 style={{ fontSize: 26 }}>{t('auth.titleLogin')}</h2>
            <p className="text-muted" style={{ fontSize: 13, marginTop: 4 }}>
              {t('auth.loginSub')}
            </p>
          </div>

          <div
            style={{
              display: 'flex',
              gap: 16,
              borderBottom: '1px solid var(--border-light)',
              marginBottom: 24,
            }}
          >
            <span
              style={{
                paddingBottom: 8,
                fontWeight: 700,
                borderBottom: '2px solid var(--bg-dark)',
                fontSize: 13,
              }}
            >
              {t('auth.tabLogin')}
            </span>
            <NavLink
              to={rawReturnTo ? `/register?return_to=${encodeURIComponent(rawReturnTo)}` : '/register'}
              style={{ paddingBottom: 8, color: 'var(--text-muted)', fontSize: 13 }}
            >
              {t('auth.tabRegister')}
            </NavLink>
          </div>

          {errorMessage && (
            <div
              style={{
                backgroundColor: 'var(--danger-bg)',
                border: '1px solid var(--danger-border)',
                color: 'var(--danger)',
                padding: '10px 14px',
                borderRadius: 'var(--radius-sm)',
                fontSize: 12,
                marginBottom: 16,
              }}
            >
              {errorMessage}
            </div>
          )}

          <form onSubmit={handleSubmit}>
            <Input
              label={t('auth.email')}
              type="email"
              placeholder={t('auth.emailPlaceholder')}
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />

            <Input
              label={t('auth.password')}
              type="password"
              placeholder={t('auth.passwordPlaceholder')}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />

            <div
              style={{
                display: 'flex',
                justifyContent: 'space-between',
                alignItems: 'center',
                marginBottom: 24,
                fontSize: 12,
              }}
            >
              <label style={{ display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer' }}>
                <input
                  type="checkbox"
                  checked={keepSignedIn}
                  onChange={(event) => setKeepSignedIn(event.target.checked)}
                />
                <span>{t('auth.keepSignedIn')}</span>
              </label>

              <NavLink
                to={rawReturnTo ? `/forgot-password?return_to=${encodeURIComponent(rawReturnTo)}` : '/forgot-password'}
                style={{ color: 'var(--text-muted)' }}
              >
                {t('auth.forgotPassword')}
              </NavLink>
            </div>

            <Button type="submit" variant="primary" size="lg" loading={loading} style={{ width: '100%' }}>
              {t('auth.submitLogin')}
            </Button>
          </form>

          <p
            style={{
              fontSize: 11,
              color: 'var(--text-subtle)',
              textAlign: 'center',
              marginTop: 24,
              lineHeight: 1.5,
            }}
          >
            {t('auth.termsNotice')}
          </p>
        </div>
      </main>
    </div>
  );
};
