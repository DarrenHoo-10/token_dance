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
import { ArrowRight, CircleAlert, Eye, EyeOff, LockKeyhole, Pause, Play, ShieldCheck, SlidersHorizontal } from 'lucide-react';
import { LoginCompanions, TokenOrbit, type CompanionMood } from './LoginArt';
import './login.css';

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
  const [motionPaused, setMotionPaused] = useState(false);
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

  const mood: CompanionMood = focusedField === 'password' || showPassword ? 'password'
    : loading ? 'loading' : focusedField === 'email' ? 'email' : errorMessage ? 'error' : 'idle';
  const caption = t(`auth.companion${mood[0].toUpperCase()}${mood.slice(1)}`);

  return (
    <div className="login-page" data-motion={motionPaused ? 'paused' : 'playing'}>
      <aside className="login-brand">
        <NavLink to="/" className="login-brand__logo" aria-label="TokenDance">
          <img src="/logo-tokendance-v2.png" alt="" /><span>TokenDance</span>
        </NavLink>
        <button type="button" className="login-motion-toggle" onClick={() => setMotionPaused(!motionPaused)}
          aria-label={t(motionPaused ? 'auth.resumeMotion' : 'auth.pauseMotion')} aria-pressed={motionPaused}>
          {motionPaused ? <Play size={13} aria-hidden="true" /> : <Pause size={13} aria-hidden="true" />}
        </button>
        <div className="login-brand__body">
          <div className="login-brand__copy">
            <p className="eyebrow">{t('common.brandTagline')}</p>
            <h1>{t('auth.loginHeroLine1')} <br /><span>{t('auth.loginHeroLine2')}</span></h1>
            <p>{t('common.heroSub')}</p>
          </div>
          <TokenOrbit />
        </div>
        <div className="login-brand__footer">
          <div><ShieldCheck size={17} aria-hidden="true" /><strong>{t('auth.editorialPrivacyTitle')}</strong><small>{t('auth.editorialPrivacyCopy')}</small></div>
          <div><SlidersHorizontal size={17} aria-hidden="true" /><strong>{t('auth.editorialControlTitle')}</strong><small>{t('auth.editorialControlCopy')}</small></div>
          <div><LockKeyhole size={17} aria-hidden="true" /><strong>{t('auth.editorialInsightTitle')}</strong><small>{t('auth.editorialInsightCopy')}</small></div>
        </div>
      </aside>
      <main className="login-main">
        <div className="login-locale"><LocaleSwitcher /></div>
        <div className="login-form-shell">
          <LoginCompanions mood={mood} caption={caption} />
          <div className="login-heading">
            <h2>{t('auth.titleLogin')}</h2>
            <p>{t('auth.loginSub')}</p>
          </div>
          <div className="login-tabs">
            <span>{t('auth.tabLogin')}</span>
            <NavLink to={rawReturnTo ? `/register?return_to=${encodeURIComponent(rawReturnTo)}` : '/register'}>{t('auth.tabRegister')}</NavLink>
          </div>
          {errorMessage && <div className="login-error" role="alert" id="login-error"><CircleAlert size={14} aria-hidden="true" />{errorMessage}</div>}
          <form onSubmit={handleSubmit} aria-busy={loading}>
            <Input label={t('auth.email')} type="email" autoComplete="username" name="email"
              placeholder={t('auth.emailPlaceholder')} value={email}
              onChange={(e) => setEmail(e.target.value)} onFocus={() => setFocusedField('email')}
              onBlur={() => setFocusedField(null)} required />
            <Input label={t('auth.password')} type={showPassword ? 'text' : 'password'} autoComplete="current-password" name="password"
              placeholder={t('auth.passwordPlaceholder')} value={password}
              onChange={(e) => setPassword(e.target.value)} onFocus={() => setFocusedField('password')}
              onBlur={() => setFocusedField(null)} required
              suffix={<button type="button" className="login-password-toggle"
                aria-label={t(showPassword ? 'auth.hidePassword' : 'auth.showPassword')} aria-pressed={showPassword}
                onClick={() => setShowPassword(!showPassword)}>
                {showPassword ? <EyeOff size={17} aria-hidden="true" /> : <Eye size={17} aria-hidden="true" />}
              </button>} />
            <div className="login-options">
              <label><input type="checkbox" checked={keepSignedIn} onChange={(e) => setKeepSignedIn(e.target.checked)} /><span>{t('auth.keepSignedIn')}</span></label>
              <NavLink to={rawReturnTo ? `/forgot-password?return_to=${encodeURIComponent(rawReturnTo)}` : '/forgot-password'}>{t('auth.forgotPassword')}</NavLink>
            </div>
            <Button type="submit" variant="primary" size="lg" loading={loading} className="login-submit">
              {t('auth.submitLogin')}{!loading && <ArrowRight size={16} aria-hidden="true" />}
            </Button>
          </form>
          <p className="login-terms">{t('auth.termsNotice')}</p>
        </div>
      </main>
    </div>
  );
};
