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
import type { Locale } from '@/types/api';

export const OnboardingPage: React.FC = () => {
  const { user, setUser, refreshSession } = useAuth();
  const { locale, setLocale, t } = useLocale();
  const { showToast } = useNotification();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();

  const returnTo = searchParams.get('return_to') || '/me';

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
        returnTo,
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
      navigate(returnTo);
    } catch (err) {
      if (err instanceof ApiError) {
        setErrorMessage(t(err.messageKey) || err.message);
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

      {/* Main split onboarding card */}
      <div
        className="panel"
        style={{
          width: '100%',
          maxWidth: 1040,
          padding: 0,
          overflow: 'hidden',
          display: 'grid',
          gridTemplateColumns: '320px 1fr',
        }}
      >
        {/* Rail */}
        <aside
          style={{
            backgroundColor: 'var(--bg-dark)',
            color: 'var(--text-inverse)',
            padding: '36px 32px',
            display: 'flex',
            flexDirection: 'column',
            justifyContent: 'space-between',
          }}
        >
          <div>
            <p className="eyebrow" style={{ color: 'var(--lime)' }}>
              {t('onboarding.railTitle')}
            </p>
            <h2 style={{ fontSize: 24, color: 'white', marginTop: 4 }}>
              {t('onboarding.railHeadline')}
            </h2>
            <p style={{ color: '#abb4ac', fontSize: 13, marginTop: 8, lineHeight: 1.5 }}>
              {t('onboarding.railSub')}
            </p>

            <div style={{ marginTop: 40, display: 'flex', flexDirection: 'column', gap: 20 }}>
              {/* Step 1 */}
              <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
                <div
                  style={{
                    width: 26,
                    height: 26,
                    borderRadius: 6,
                    backgroundColor: '#273129',
                    color: 'var(--lime)',
                    display: 'grid',
                    placeItems: 'center',
                    fontSize: 12,
                    fontWeight: 700,
                  }}
                >
                  ✓
                </div>
                <div>
                  <strong style={{ fontSize: 13, color: '#d0d8d1' }}>{t('onboarding.step1')}</strong>
                  <div style={{ fontSize: 11, color: '#7a857b' }}>{t('onboarding.step1Desc')}</div>
                </div>
              </div>

              {/* Step 2 (Active) */}
              <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
                <div
                  style={{
                    width: 26,
                    height: 26,
                    borderRadius: 6,
                    backgroundColor: 'var(--lime)',
                    color: '#111512',
                    display: 'grid',
                    placeItems: 'center',
                    fontSize: 12,
                    fontWeight: 800,
                  }}
                >
                  2
                </div>
                <div>
                  <strong style={{ fontSize: 13, color: 'white' }}>{t('onboarding.step2')}</strong>
                  <div style={{ fontSize: 11, color: '#a0aaa1' }}>{t('onboarding.step2Desc')}</div>
                </div>
              </div>

              {/* Step 3 */}
              <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
                <div
                  style={{
                    width: 26,
                    height: 26,
                    borderRadius: 6,
                    border: '1px solid #3c483f',
                    color: '#889289',
                    display: 'grid',
                    placeItems: 'center',
                    fontSize: 12,
                    fontWeight: 700,
                  }}
                >
                  3
                </div>
                <div>
                  <strong style={{ fontSize: 13, color: '#889289' }}>{t('onboarding.step3')}</strong>
                  <div style={{ fontSize: 11, color: '#687269' }}>{t('onboarding.step3Desc')}</div>
                </div>
              </div>
            </div>
          </div>

          <div style={{ fontSize: 11, color: '#7e887f', borderTop: '1px solid var(--border-dark)', paddingTop: 16 }}>
            {t('auth.privacyPledge')} {t('auth.privacyPledgeDesc')}
          </div>
        </aside>

        {/* Main form */}
        <div style={{ padding: '40px 48px' }}>
          <p className="eyebrow">{t('onboarding.step2')}</p>
          <h1 style={{ fontSize: 28, marginBottom: 4 }}>{t('onboarding.headline')}</h1>
          <p className="text-muted" style={{ fontSize: 13, marginBottom: 28 }}>
            {t('onboarding.subheadline')}
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
                marginBottom: 20,
              }}
            >
              {errorMessage}
            </div>
          )}

          <form onSubmit={handleSubmit}>
            {/* Avatar row */}
            <div
              style={{
                display: 'flex',
                alignItems: 'center',
                gap: 16,
                padding: '12px 16px',
                border: '1px solid var(--border-light)',
                borderRadius: 'var(--radius-md)',
                backgroundColor: 'var(--bg-subtle)',
                marginBottom: 20,
              }}
            >
              <div className="avatar" style={{ width: 48, height: 48, fontSize: 16 }}>
                {initials}
              </div>
              <div>
                <strong style={{ fontSize: 13 }}>{t('onboarding.avatarLabel')}</strong>
                <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>{t('onboarding.avatarHint')}</div>
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
                placeholder="darrenhoo"
                prefix="@"
                value={handle}
                onChange={(e) => setHandle(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
                hint={t('onboarding.handleHint')}
                required
              />
            </div>

            <div className="form-group" style={{ marginBottom: 16 }}>
              <label className="form-label">{t('onboarding.bioLabel')}</label>
              <textarea
                className="form-input"
                style={{ height: 68, padding: '8px 14px', resize: 'vertical' }}
                placeholder={t('onboarding.bioPlaceholder')}
                value={bio}
                onChange={(e) => setBio(e.target.value)}
                maxLength={280}
              />
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
              <Select
                label={t('onboarding.timezoneLabel')}
                value={timezone}
                onChange={(e) => setTimezone(e.target.value)}
                options={[
                  { value: 'Asia/Shanghai', label: 'Asia / Shanghai (UTC+8)' },
                  { value: 'America/New_York', label: 'America / New York (EST)' },
                  { value: 'America/Los_Angeles', label: 'America / Los Angeles (PST)' },
                  { value: 'Europe/London', label: 'Europe / London (UTC+0)' },
                  { value: 'UTC', label: 'UTC (Coordinated Universal Time)' },
                ]}
              />

              <Select
                label={t('onboarding.localeLabel')}
                value={selectedLocale}
                onChange={(e) => setSelectedLocale(e.target.value as Locale)}
                options={[
                  { value: 'zh-CN', label: '简体中文 (zh-CN)' },
                  { value: 'en-US', label: 'English (en-US)' },
                ]}
              />
            </div>

            {/* Visibility Choice */}
            <div style={{ marginTop: 8, marginBottom: 20 }}>
              <label className="form-label">{t('onboarding.step3')}</label>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14, marginTop: 6 }}>
                <div
                  onClick={() => setIsPublic(false)}
                  style={{
                    padding: 16,
                    borderRadius: 'var(--radius-md)',
                    border: `1.5px solid ${!isPublic ? 'var(--text-main)' : 'var(--border-light)'}`,
                    backgroundColor: !isPublic ? 'var(--bg-subtle)' : 'var(--bg-surface)',
                    cursor: 'pointer',
                  }}
                >
                  <strong style={{ fontSize: 13, display: 'block', marginBottom: 4 }}>
                    {t('onboarding.choicePrivateTitle')}
                  </strong>
                  <p style={{ fontSize: 11, color: 'var(--text-muted)', margin: 0, lineHeight: 1.4 }}>
                    {t('onboarding.choicePrivateDesc')}
                  </p>
                </div>

                <div
                  onClick={() => setIsPublic(true)}
                  style={{
                    padding: 16,
                    borderRadius: 'var(--radius-md)',
                    border: `1.5px solid ${isPublic ? 'var(--lime-border)' : 'var(--border-light)'}`,
                    backgroundColor: isPublic ? 'var(--lime-bg)' : 'var(--bg-surface)',
                    cursor: 'pointer',
                  }}
                >
                  <strong style={{ fontSize: 13, display: 'block', marginBottom: 4 }}>
                    {t('onboarding.choicePublicTitle')}
                  </strong>
                  <p style={{ fontSize: 11, color: 'var(--text-muted)', margin: 0, lineHeight: 1.4 }}>
                    {t('onboarding.choicePublicDesc')}
                  </p>
                </div>
              </div>
            </div>

            <div
              style={{
                padding: '12px 16px',
                borderRadius: 'var(--radius-md)',
                backgroundColor: 'var(--bg-subtle)',
                fontSize: 11,
                color: 'var(--text-muted)',
                lineHeight: 1.5,
                marginBottom: 24,
              }}
            >
              ℹ {t('onboarding.consentNotice')}
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 12 }}>
              <Button type="submit" variant="dark" loading={loading} style={{ minWidth: 160 }}>
                {t('onboarding.saveAndContinue')}
              </Button>
            </div>
          </form>
        </div>
      </div>
    </div>
  );
};
