import React, { useState } from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Select } from '@/components/common/Select';
import { Button } from '@/components/common/Button';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import type { Locale } from '@/types/api';

export const OnboardingPage: React.FC = () => {
  const { user, setUser, refreshSession } = useAuth();
  const { locale, setLocale, t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const rawReturnTo = searchParams.get('return_to');

  const [displayName, setDisplayName] = useState(user?.displayName || '');
  const [handle, setHandle] = useState(user?.handle || '');
  const [bio, setBio] = useState('');
  const [timezone, setTimezone] = useState('Asia/Shanghai');
  const [selectedLocale, setSelectedLocale] = useState<Locale>(locale);
  const [isPublic, setIsPublic] = useState(false);
  const [loading, setLoading] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim() || !handle.trim()) {
      setErrorMessage(t('errors.http_400'));
      return;
    }

    const cleanHandle = handle.trim().toLowerCase().replace(/^@/, '');
    if (!/^[a-z][a-z0-9_]{2,31}$/.test(cleanHandle)) {
      setErrorMessage(t('errors.PROFILE_HANDLE_INVALID'));
      return;
    }

    try {
      setLoading(true);
      setErrorMessage(null);
      const res = await api.completeOnboarding({
        displayName: displayName.trim(),
        handle: cleanHandle,
        timezone,
        locale: selectedLocale,
        privacy: {
          publicProfileEnabled: isPublic,
          leaderboardVisibility: isPublic ? 'public' : 'private',
        },
        returnTo: rawReturnTo || undefined,
      });

      setLocale(selectedLocale);
      const profileObj = res.user || res.profile;
      if (user) {
        setUser({
          ...user,
          displayName: profileObj?.displayName || displayName.trim(),
          handle: profileObj?.handle || cleanHandle,
          locale: (profileObj?.locale as Locale) || selectedLocale,
          onboardingRequired: false,
          productState: isPublic ? 'active_public' : 'active_private',
        });
      }
      await refreshSession();

      showToast(t('common.saved'), 'success');
      const destination = res.returnTo || '/me';
      navigate(destination);
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

  const initials = displayName
    ? displayName.split(' ').map((n) => n[0]).join('').substring(0, 2).toUpperCase()
    : 'TD';

  return (
    <div
      style={{
        minHeight: '100vh',
        backgroundColor: 'var(--bg-app)',
        padding: '32px 24px 64px',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
      }}
    >
      {/* Header */}
      <header
        style={{
          width: '100%',
          maxWidth: 1040,
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'center',
          marginBottom: 32,
        }}
      >
        <div style={{ display: 'flex', alignItems: 'center', gap: 10, fontSize: 20, fontWeight: 800 }}>
          <img src="/logo.png" alt="TokenDance" style={{ width: 32, height: 32 }} />
          <span>TokenDance</span>
        </div>

        <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
          <span style={{ fontSize: 12, fontWeight: 700, color: 'var(--text-muted)' }}>
            {t('onboarding.stepIndicator')}
          </span>
          <LocaleSwitcher />
        </div>
      </header>

      {/* Main card grid */}
      <div
        style={{
          width: '100%',
          maxWidth: 1040,
          display: 'grid',
          gridTemplateColumns: '320px 1fr',
          gap: 24,
        }}
      >
        {/* Left Step Rail */}
        <aside
          style={{
            backgroundColor: 'var(--bg-surface)',
            border: '1px solid var(--border-light)',
            borderRadius: 'var(--radius-lg)',
            padding: '32px 24px',
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'space-between',
          }}
        >
          <div>
            <p className="eyebrow">{t('onboarding.railTitle')}</p>
            <h2 style={{ fontSize: 22, marginTop: 4, lineHeight: 1.2 }}>{t('onboarding.railHeadline')}</h2>
            <p className="text-muted" style={{ fontSize: 13, marginTop: 8 }}>
              {t('onboarding.railSub')}
            </p>

            <div style={{ display: 'flex', flexDirection: 'column', gap: 16, marginTop: 32 }}>
              <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
                <span className="badge badge-good">✓</span>
                <div>
                  <strong style={{ fontSize: 13 }}>{t('onboarding.step1')}</strong>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('onboarding.step1Desc')}</div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
                <span className="badge badge-lime">2</span>
                <div>
                  <strong style={{ fontSize: 13 }}>{t('onboarding.step2')}</strong>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('onboarding.step2Desc')}</div>
                </div>
              </div>

              <div style={{ display: 'flex', gap: 12, alignItems: 'center' }}>
                <span className="badge" style={{ backgroundColor: 'var(--border-light)' }}>3</span>
                <div>
                  <strong style={{ fontSize: 13 }}>{t('onboarding.step3')}</strong>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('onboarding.step3Desc')}</div>
                </div>
              </div>
            </div>
          </div>

          <div
            style={{
              padding: '16px',
              backgroundColor: 'var(--bg-subtle)',
              borderRadius: 'var(--radius-sm)',
              fontSize: 11,
              color: 'var(--text-muted)',
              lineHeight: 1.5,
            }}
          >
            🔒 {t('onboarding.consentNotice')}
          </div>
        </aside>

        {/* Right Form Card */}
        <main
          style={{
            backgroundColor: 'var(--bg-surface)',
            border: '1px solid var(--border-light)',
            borderRadius: 'var(--radius-lg)',
            padding: '36px',
          }}
        >
          <div style={{ marginBottom: 28 }}>
            <h1 style={{ fontSize: 24 }}>{t('onboarding.headline')}</h1>
            <p className="text-muted" style={{ fontSize: 13, marginTop: 4 }}>
              {t('onboarding.subheadline')}
            </p>
          </div>

          {errorMessage && (
            <div
              style={{
                backgroundColor: 'var(--danger-bg)',
                border: '1px solid var(--danger-border)',
                color: 'var(--danger)',
                padding: '12px 16px',
                borderRadius: 'var(--radius-sm)',
                fontSize: 12,
                marginBottom: 20,
              }}
            >
              {errorMessage}
            </div>
          )}

          <form onSubmit={handleSubmit}>
            {/* Avatar & Display Name */}
            <div style={{ display: 'flex', gap: 20, alignItems: 'center', marginBottom: 24 }}>
              <div
                className="avatar"
                style={{
                  width: 64,
                  height: 64,
                  fontSize: 20,
                  backgroundColor: 'var(--bg-dark)',
                  color: 'var(--lime)',
                  border: '2px solid var(--lime-border)',
                }}
              >
                {initials}
              </div>

              <div>
                <label className="form-label">{t('onboarding.avatarLabel')}</label>
                <div style={{ fontSize: 12, color: 'var(--text-muted)' }}>
                  {t('onboarding.avatarHint')}
                </div>
              </div>
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
              <Input
                label={t('onboarding.displayNameLabel')}
                placeholder={t('onboarding.displayNamePlaceholder')}
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                required
              />

              <Input
                label={t('onboarding.handleLabel')}
                prefix="@"
                placeholder="developer_handle"
                value={handle}
                onChange={(e) => setHandle(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                hint={t('onboarding.handleHint')}
                required
              />
            </div>

            <div style={{ marginBottom: 16 }}>
              <label className="form-label">{t('onboarding.bioLabel')}</label>
              <textarea
                value={bio}
                onChange={(e) => setBio(e.target.value)}
                placeholder={t('onboarding.bioPlaceholder')}
                className="form-input"
                style={{ height: 72, padding: '8px 12px', resize: 'vertical' }}
              />
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16, marginBottom: 28 }}>
              <Select
                label={t('onboarding.timezoneLabel')}
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                options={[
                  { value: 'Asia/Shanghai', label: 'Asia/Shanghai (UTC+8)' },
                  { value: 'Asia/Tokyo', label: 'Asia/Tokyo (UTC+9)' },
                  { value: 'America/Los_Angeles', label: 'America/Los_Angeles (PT)' },
                  { value: 'America/New_York', label: 'America/New_York (ET)' },
                  { value: 'Europe/London', label: 'Europe/London (UTC+0)' },
                  { value: 'UTC', label: 'UTC' },
                ]}
              />

              <Select
                label={t('onboarding.localeLabel')}
                value={selectedLocale}
                onChange={(e) => setSelectedLocale(e.target.value as Locale)}
                options={[
                  { value: 'zh-CN', label: '简体中文' },
                  { value: 'en-US', label: 'English (US)' },
                ]}
              />
            </div>

            {/* Visibility Mode Selector */}
            <div style={{ marginBottom: 28 }}>
              <label className="form-label" style={{ marginBottom: 10 }}>
                {t('onboarding.step3')}
              </label>

              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                <div
                  onClick={() => setIsPublic(false)}
                  style={{
                    padding: '16px',
                    borderRadius: 'var(--radius-md)',
                    border: !isPublic ? '2px solid var(--lime-border)' : '1px solid var(--border-light)',
                    backgroundColor: !isPublic ? 'var(--lime-subtle)' : 'var(--bg-surface)',
                    cursor: 'pointer',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <strong style={{ fontSize: 13 }}>{t('onboarding.choicePrivateTitle')}</strong>
                    {!isPublic && <span className="badge badge-lime">✓</span>}
                  </div>
                  <p style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 6, margin: 0 }}>
                    {t('onboarding.choicePrivateDesc')}
                  </p>
                </div>

                <div
                  onClick={() => setIsPublic(true)}
                  style={{
                    padding: '16px',
                    borderRadius: 'var(--radius-md)',
                    border: isPublic ? '2px solid var(--lime-border)' : '1px solid var(--border-light)',
                    backgroundColor: isPublic ? 'var(--lime-subtle)' : 'var(--bg-surface)',
                    cursor: 'pointer',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                    <strong style={{ fontSize: 13 }}>{t('onboarding.choicePublicTitle')}</strong>
                    {isPublic && <span className="badge badge-lime">✓</span>}
                  </div>
                  <p style={{ fontSize: 12, color: 'var(--text-muted)', marginTop: 6, margin: 0 }}>
                    {t('onboarding.choicePublicDesc')}
                  </p>
                </div>
              </div>
            </div>

            <Button type="submit" variant="primary" size="lg" loading={loading} style={{ width: '100%' }}>
              {t('onboarding.saveAndContinue')}
            </Button>
          </form>
        </main>
      </div>
    </div>
  );
};
