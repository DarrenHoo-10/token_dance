import React, { useState } from 'react';
import { NavLink, useNavigate, useSearchParams } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';

export const ForgotPasswordPage: React.FC = () => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const rawReturnTo = searchParams.get('return_to');

  const [email, setEmail] = useState('');
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email) {
      setErrorMessage(t('errors.http_400'));
      return;
    }

    try {
      setLoading(true);
      setErrorMessage(null);
      await api.requestPasswordResetCode({ email });
      showToast(t('auth.codeSent'), 'info');
      const params = new URLSearchParams({ email });
      if (rawReturnTo) params.set('return_to', rawReturnTo);
      navigate(`/reset-password?${params.toString()}`);
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(getApiErrorMessage(t, err));
      } else if (err instanceof Error) {
        setErrorMessage(err.message);
      }
    } finally {
      setLoading(false);
    }
  };

  return (
    <div
      style={{
        display: 'flex',
        minHeight: '100vh',
        justifyContent: 'center',
        alignItems: 'center',
        padding: '32px 16px',
        backgroundColor: 'var(--bg-app)',
        position: 'relative',
      }}
    >
      <div style={{ position: 'absolute', top: 24, right: 32 }}>
        <LocaleSwitcher />
      </div>

      <div className="panel" style={{ width: '100%', maxWidth: 420, padding: 36 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 24 }}>
          <img src="/logo.png" alt="TokenDance" style={{ width: 28, height: 28 }} />
          <strong style={{ fontSize: 18 }}>TokenDance</strong>
        </div>

        <h2 style={{ fontSize: 24, marginBottom: 6 }}>{t('auth.titleForgot')}</h2>
        <p className="text-muted" style={{ fontSize: 13, marginBottom: 24 }}>
          {t('auth.forgotSub')}
        </p>

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
            autoFocus
          />

          <Button
            type="submit"
            variant="dark"
            loading={loading}
            style={{ width: '100%', height: 44, marginTop: 8 }}
          >
            {t('auth.submitForgot')}
          </Button>
        </form>

        <div style={{ marginTop: 24, textAlign: 'center', fontSize: 12 }}>
          <NavLink
            to={rawReturnTo ? `/login?return_to=${encodeURIComponent(rawReturnTo)}` : '/login'}
            style={{ color: 'var(--text-muted)' }}
          >
            ← {t('auth.signInLink')}
          </NavLink>
        </div>
      </div>
    </div>
  );
};
