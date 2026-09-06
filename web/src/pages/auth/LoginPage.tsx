import React, { useEffect, useState } from 'react';
import { NavLink, useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import { ArrowRight } from 'lucide-react';
import type { CompanionMood } from './LoginArt';
import { AuthLayout } from './AuthLayout';
import { AuthPasswordInput } from './AuthPasswordInput';

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

  const [focusedField, setFocusedField] = useState<'email' | 'password' | null>(null);
  const [showPassword, setShowPassword] = useState(false);
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

  const mood: CompanionMood = loading ? 'loading'
    : errorMessage ? 'error'
      : focusedField === 'password' || showPassword ? 'password'
        : focusedField === 'email' ? 'email' : 'idle';

  return (
    <AuthLayout mode="login" returnTo={rawReturnTo} mood={mood} errorMessage={errorMessage}>
      <form onSubmit={handleSubmit} aria-busy={loading}>
        <Input
          label={t('auth.email')} type="email" autoComplete="username" name="email"
          placeholder={t('auth.emailPlaceholder')} value={email}
          onChange={(e) => { setEmail(e.target.value); setErrorMessage(null); }} onFocus={() => setFocusedField('email')}
          onBlur={() => setFocusedField(null)} required
        />
        <AuthPasswordInput
          label={t('auth.password')} autoComplete="current-password" name="password"
          placeholder={t('auth.passwordPlaceholder')} value={password}
          onChange={(e) => { setPassword(e.target.value); setErrorMessage(null); }} onFocus={() => setFocusedField('password')}
          onBlur={() => setFocusedField(null)} required
          visible={showPassword} onToggleVisibility={() => setShowPassword(!showPassword)}
        />
        <div className="login-options">
          <label>
            <input type="checkbox" checked={keepSignedIn} onChange={(e) => { setKeepSignedIn(e.target.checked); setErrorMessage(null); }} />
            <span>{t('auth.keepSignedIn')}</span>
          </label>
          <NavLink to={rawReturnTo ? `/forgot-password?return_to=${encodeURIComponent(rawReturnTo)}` : '/forgot-password'}>
            {t('auth.forgotPassword')}
          </NavLink>
        </div>
        <Button type="submit" variant="primary" size="lg" loading={loading} className="login-submit">
          {t('auth.submitLogin')}{!loading && <ArrowRight size={16} aria-hidden="true" />}
        </Button>
      </form>
    </AuthLayout>
  );
};
