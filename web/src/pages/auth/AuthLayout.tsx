import React, { useEffect, useState } from 'react';
import { NavLink } from 'react-router-dom';
import { ChartNoAxesColumnIncreasing, CircleAlert, Pause, Play, ShieldCheck, SlidersHorizontal } from 'lucide-react';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { useLocale } from '@/context/LocaleContext';
import { LoginCompanions, TokenScene, type CompanionMood } from './LoginArt';
import './login.css';

interface AuthLayoutProps {
  mode: 'login' | 'register';
  returnTo: string | null;
  mood: CompanionMood;
  errorMessage: string | null;
  children: React.ReactNode;
}

export function AuthLayout({ mode, returnTo, mood, errorMessage, children }: AuthLayoutProps) {
  const { locale, t } = useLocale();
  const [motionPaused, setMotionPaused] = useState(false);
  const [pageHidden, setPageHidden] = useState(() => document.hidden);
  useEffect(() => {
    const updateVisibility = () => setPageHidden(document.hidden);
    document.addEventListener('visibilitychange', updateVisibility);
    return () => document.removeEventListener('visibilitychange', updateVisibility);
  }, []);
  const isLogin = mode === 'login';
  const caption = t(`auth.companion${mood[0].toUpperCase()}${mood.slice(1)}`);
  const route = (path: string) => returnTo ? `${path}?return_to=${encodeURIComponent(returnTo)}` : path;

  return (
    <div className="login-page" lang={locale} data-auth-mode={mode} data-motion={motionPaused || pageHidden ? 'paused' : 'playing'}>
      <aside className="login-brand">
        <TokenScene paused={motionPaused || pageHidden} />
        <NavLink to="/" className="login-brand__logo" aria-label="TokenDance">
          <img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="" />
          <span>TokenDance</span>
        </NavLink>
        <button
          type="button"
          className="login-motion-toggle"
          onClick={() => setMotionPaused(!motionPaused)}
          aria-label={t(motionPaused ? 'auth.resumeMotion' : 'auth.pauseMotion')}
          aria-pressed={motionPaused}
        >
          {motionPaused ? <Play size={13} aria-hidden="true" /> : <Pause size={13} aria-hidden="true" />}
        </button>
        <div className="login-brand__body">
          <div className="login-brand__copy">
            <h1>
              {t(`auth.${mode}HeroLine1`)} <br />
              <span>{t(`auth.${mode}HeroLine2`)}</span>
            </h1>
          </div>
        </div>
        <div className="login-brand__footer">
          <div>
            <ShieldCheck size={17} aria-hidden="true" />
            <strong>{t('auth.editorialPrivacyTitle')}</strong>
            <small>{t('auth.editorialPrivacyCopy')}</small>
          </div>
          <div>
            <SlidersHorizontal size={17} aria-hidden="true" />
            <strong>{t('auth.editorialControlTitle')}</strong>
            <small>{t('auth.editorialControlCopy')}</small>
          </div>
          <div>
            <ChartNoAxesColumnIncreasing size={17} aria-hidden="true" />
            <strong>{t('auth.editorialInsightTitle')}</strong>
            <small>{t('auth.editorialInsightCopy')}</small>
          </div>
        </div>
      </aside>
      <main className="login-main">
        <div className="login-locale"><LocaleSwitcher /></div>
        <div className="login-form-shell">
          <LoginCompanions mood={mood} caption={caption} />
          <div className="login-heading">
            <h2>{t(isLogin ? 'auth.titleLogin' : 'auth.titleRegister')}</h2>
            <p>{t(isLogin ? 'auth.loginSub' : 'auth.registerSub')}</p>
          </div>
          <div className="login-tabs">
            {isLogin ? <span aria-current="page">{t('auth.tabLogin')}</span>
              : <NavLink to={route('/login')}>{t('auth.tabLogin')}</NavLink>}
            {isLogin ? <NavLink to={route('/register')}>{t('auth.tabRegister')}</NavLink>
              : <span aria-current="page">{t('auth.tabRegister')}</span>}
          </div>
          {errorMessage && (
            <div className="login-error" role="alert" id={`${mode}-error`}>
              <CircleAlert size={14} aria-hidden="true" />{errorMessage}
            </div>
          )}
          {children}
          <p className="login-terms">{t('auth.termsNotice')}</p>
        </div>
      </main>
    </div>
  );
}
