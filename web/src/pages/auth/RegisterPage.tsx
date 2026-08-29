import React, { useState, useEffect } from 'react';
import { NavLink, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';

export const RegisterPage: React.FC = () => {
  const { register } = useAuth();
  const { locale, t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const rawReturnTo = searchParams.get('return_to');

  const [email, setEmail] = useState('');
  const [code, setCode] = useState('');
  const [password, setPassword] = useState('');
  const [sendingCode, setSendingCode] = useState(false);
  const [cooldown, setCooldown] = useState(0);
  const [submitting, setSubmitting] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  useEffect(() => {
    if (cooldown <= 0) return;
    const timer = setInterval(() => {
      setCooldown((prev) => (prev > 0 ? prev - 1 : 0));
    }, 1000);
    return () => clearInterval(timer);
  }, [cooldown]);

  const handleSendCode = async () => {
    if (!email || !email.includes('@')) {
      setErrorMessage(t('errors.http_400'));
      return;
    }

    try {
      setSendingCode(true);
      setErrorMessage(null);
      const res = await api.requestRegisterCode({ email, locale });
      setCooldown(res.cooldownSeconds || 60);
      showToast(t('auth.codeSent'), 'info');
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(getApiErrorMessage(t, err));
      } else if (err instanceof Error) {
        setErrorMessage(err.message);
      }
    } finally {
      setSendingCode(false);
    }
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !code || !password) {
      setErrorMessage(t('errors.http_400'));
      return;
    }

    if (password.length < 8) {
      setErrorMessage(t('auth.newPasswordPlaceholder'));
      return;
    }

    try {
      setSubmitting(true);
      setErrorMessage(null);
      const res = await register({
        email,
        code,
        password,
        returnTo: rawReturnTo || undefined,
      });
      showToast(t('auth.registerSuccess'), 'success');
      const target = res?.returnTo || '/me';
      navigate(`/onboarding?return_to=${encodeURIComponent(target)}`);
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(getApiErrorMessage(t, err));
      } else if (err instanceof Error) {
        setErrorMessage(err.message);
      } else {
        setErrorMessage(t('errors.unknown'));
      }
    } finally {
      setSubmitting(false);
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
              Create your <br />
              <span style={{ color: 'var(--lime)' }}>Account.</span>
            </h1>
            <p style={{ marginTop: 20, color: '#b2bbb4', fontSize: 16, lineHeight: 1.6 }}>
              {t('common.heroSub')}
            </p>
          </div>
        </div>

        <div
          style={{
            borderTop: '1px solid var(--border-dark)',
            paddingTop: 24,
          }}
        >
          <strong style={{ display: 'block', color: 'white', fontSize: 14 }}>
            {t('auth.privacyPledge')}
          </strong>
          <p style={{ color: '#88928a', fontSize: 12, marginTop: 6, lineHeight: 1.5 }}>
            {t('auth.privacyPledgeDesc')}
          </p>
        </div>
      </aside>

      {/* Right registration form */}
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
            <h2 style={{ fontSize: 26 }}>{t('auth.titleRegister')}</h2>
            <p className="text-muted" style={{ fontSize: 13, marginTop: 4 }}>
              {t('auth.registerSub')}
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
            <NavLink
              to={rawReturnTo ? `/login?return_to=${encodeURIComponent(rawReturnTo)}` : '/login'}
              style={{ paddingBottom: 8, color: 'var(--text-muted)', fontSize: 13 }}
            >
              {t('auth.tabLogin')}
            </NavLink>
            <span
              style={{
                paddingBottom: 8,
                fontWeight: 700,
                borderBottom: '2px solid var(--bg-dark)',
                fontSize: 13,
              }}
            >
              {t('auth.tabRegister')}
            </span>
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
            <div style={{ display: 'flex', gap: 10, alignItems: 'flex-end', marginBottom: 16 }}>
              <div style={{ flex: 1 }}>
                <Input
                  label={t('auth.email')}
                  type="email"
                  placeholder={t('auth.emailPlaceholder')}
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  required
                />
              </div>
              <Button
                type="button"
                variant="outline"
                onClick={handleSendCode}
                loading={sendingCode}
                disabled={cooldown > 0 || !email}
                style={{ height: 42, marginBottom: 0, whiteSpace: 'nowrap' }}
              >
                {cooldown > 0 ? `${cooldown}${t('auth.cooldownSec')}` : t('auth.sendCode')}
              </Button>
            </div>

            <Input
              label={t('auth.code')}
              type="text"
              placeholder={t('auth.codePlaceholder')}
              value={code}
              onChange={(e) => setCode(e.target.value.trim())}
              required
            />

            <Input
              label={t('auth.password')}
              type="password"
              placeholder={t('auth.newPasswordPlaceholder')}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
            />

            <Button type="submit" variant="primary" size="lg" loading={submitting} style={{ width: '100%', marginTop: 8 }}>
              {t('auth.submitRegister')}
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
