import React, { useState } from 'react';
import { NavLink, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { ApiError } from '@/api/client';

export const LoginPage: React.FC = () => {
  const { login } = useAuth();
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const returnTo = searchParams.get('return_to') || '/me';

  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password) {
      setErrorMessage(t('errors.http_400'));
      return;
    }

    try {
      setLoading(true);
      setErrorMessage(null);
      await login({ email, password, returnTo });
      showToast(t('auth.loginSuccess'), 'success');
      navigate(returnTo);
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(t(err.messageKey) || t('auth.invalidCredentials') || err.message);
      } else if (err instanceof Error) {
        setErrorMessage(err.message);
      } else {
        setErrorMessage(t('errors.unknown'));
      }
    } finally {
      setLoading(false);
    }
  };

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
            <img src="/logo.png" alt="TokenDance" style={{ width: 36, height: 36 }} />
            <span>TokenDance</span>
          </div>

          <div style={{ marginTop: 120, maxWidth: 440 }}>
            <p className="eyebrow" style={{ color: 'var(--lime)' }}>
              {t('common.brandTagline')}
            </p>
            <h1 style={{ fontSize: 48, color: 'white', letterSpacing: '-0.05em', lineHeight: 1.05 }}>
              Let Token <br />
              <span style={{ color: 'var(--lime)' }}>Dance.</span>
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
            <small style={{ color: '#88928a', fontSize: 11 }}>{t('metrics.tokensDanced')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 20, marginTop: 4 }}>
              325.7M
            </strong>
          </div>
          <div>
            <small style={{ color: '#88928a', fontSize: 11 }}>{t('metrics.globalRank')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 20, marginTop: 4 }}>
              #37
            </strong>
          </div>
          <div>
            <small style={{ color: '#88928a', fontSize: 11 }}>{t('metrics.activeStreak')}</small>
            <strong className="mono-num" style={{ display: 'block', fontSize: 20, marginTop: 4 }}>
              23 days
            </strong>
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
              to={`/register?return_to=${encodeURIComponent(returnTo)}`}
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
              role="alert"
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
              autoFocus
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
                marginBottom: 20,
                fontSize: 12,
              }}
            >
              <span className="text-muted">{t('auth.keepSignedIn')}</span>
              <NavLink to="/forgot-password" style={{ fontWeight: 600, color: 'var(--text-main)' }}>
                {t('auth.forgotPassword')}
              </NavLink>
            </div>

            <Button
              type="submit"
              variant="dark"
              loading={loading}
              style={{ width: '100%', height: 44 }}
            >
              {t('auth.submitLogin')}
            </Button>
          </form>

          <div style={{ marginTop: 24, textAlign: 'center', fontSize: 12, color: 'var(--text-muted)' }}>
            <span>{t('auth.newToTokenDance')} </span>
            <NavLink to={`/register?return_to=${encodeURIComponent(returnTo)}`} style={{ fontWeight: 700, color: 'var(--text-main)' }}>
              {t('auth.createAccountLink')}
            </NavLink>
          </div>

          <div
            style={{
              marginTop: 32,
              paddingTop: 20,
              borderTop: '1px solid var(--border-light)',
              fontSize: 11,
              color: 'var(--text-subtle)',
              lineHeight: 1.5,
            }}
          >
            <strong>{t('auth.privacyPledge')} </strong>
            {t('auth.privacyPledgeDesc')}
          </div>
        </div>
      </main>
    </div>
  );
};
