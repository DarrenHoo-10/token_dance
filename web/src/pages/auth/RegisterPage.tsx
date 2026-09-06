import React, { useState, useEffect } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Button } from '@/components/common/Button';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import { ArrowRight } from 'lucide-react';
import type { CompanionMood } from './LoginArt';
import { AuthLayout } from './AuthLayout';
import { AuthPasswordInput } from './AuthPasswordInput';

export const RegisterPage: React.FC = () => {
  const { register } = useAuth();
  const { locale, t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const rawReturnTo = searchParams.get('return_to');

  const [focusedField, setFocusedField] = useState<'email' | 'password' | null>(null);
  const [showPassword, setShowPassword] = useState(false);
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
      if (res.testCode) {
        setCode(res.testCode);
        showToast(t('auth.codeSentLocal', { code: res.testCode }), 'info');
      } else {
        showToast(t('auth.codeSent'), 'info');
      }
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
        locale,
        timezone: Intl.DateTimeFormat().resolvedOptions().timeZone,
      });
      showToast(t('auth.registerSuccess'), 'success');
      // Registration completes onboarding server-side with default profile
      // settings, so new users go straight to the app.
      navigate(res?.returnTo || '/me');
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

  const mood: CompanionMood = submitting || sendingCode ? 'loading'
    : errorMessage ? 'error'
      : focusedField === 'password' || showPassword ? 'password'
        : focusedField === 'email' ? 'email' : 'idle';

  return (
    <AuthLayout mode="register" returnTo={rawReturnTo} mood={mood} errorMessage={errorMessage}>
      <form onSubmit={handleSubmit} aria-busy={submitting}>
        <div className="auth-email-code-row">
          <Input
            label={t('auth.email')} type="email" autoComplete="email" name="email"
            placeholder={t('auth.emailPlaceholder')} value={email}
            onChange={(e) => { setEmail(e.target.value); setErrorMessage(null); }} onFocus={() => setFocusedField('email')}
            onBlur={() => setFocusedField(null)} required
          />
          <Button
            type="button" variant="outline" className="auth-send-code"
            onClick={handleSendCode} loading={sendingCode} disabled={cooldown > 0 || !email}
          >
            {cooldown > 0 ? `${cooldown}${t('auth.cooldownSec')}` : t('auth.sendCode')}
          </Button>
        </div>
        <Input
          label={t('auth.code')} type="text" autoComplete="one-time-code" name="code" inputMode="numeric"
          placeholder={t('auth.codePlaceholder')} value={code}
          onChange={(e) => { setCode(e.target.value.trim()); setErrorMessage(null); }} required
        />
        <AuthPasswordInput
          label={t('auth.password')} autoComplete="new-password" name="password"
          placeholder={t('auth.newPasswordPlaceholder')} value={password}
          onChange={(e) => { setPassword(e.target.value); setErrorMessage(null); }} onFocus={() => setFocusedField('password')}
          onBlur={() => setFocusedField(null)} required
          visible={showPassword} onToggleVisibility={() => setShowPassword(!showPassword)}
        />
        <Button type="submit" variant="primary" size="lg" loading={submitting} className="login-submit auth-register-submit">
          {t('auth.submitRegister')}{!submitting && <ArrowRight size={16} aria-hidden="true" />}
        </Button>
      </form>
    </AuthLayout>
  );
};
