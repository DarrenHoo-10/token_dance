import React, { useState, useEffect, useRef } from 'react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { AvatarSettings } from '@/components/common/AvatarSettings';
import { Select } from '@/components/common/Select';
import { Button } from '@/components/common/Button';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import type { UserProfile, Locale } from '@/types/api';

export const ProfileSettingsPage: React.FC = () => {
  const { user, setUser, refreshSession } = useAuth();
  const { setLocale, t, locale } = useLocale();
  const { showToast } = useNotification();
  const localeRef = useRef(locale);
  localeRef.current = locale;

  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadAttempt, setLoadAttempt] = useState(0);
  const [saving, setSaving] = useState(false);
  const [avatarBusy, setAvatarBusy] = useState(false);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const [displayName, setDisplayName] = useState('');
  const [handle, setHandle] = useState('');
  const [bio, setBio] = useState('');
  const [timezone, setTimezone] = useState('Asia/Shanghai');
  const [selectedLocale, setSelectedLocale] = useState<Locale>('zh-CN');

  useEffect(() => {
    let active = true;
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      if (!active) return;
      active = false;
      controller.abort();
      setError(new Error(localeRef.current === 'zh-CN' ? '资料加载超时，请检查网络后重试。' : 'Profile loading timed out. Check your connection and retry.'));
      setLoading(false);
    }, 15_000);
    async function load() {
      try {
        setLoading(true);
        setError(null);
        const p = await api.getProfile(controller.signal);
        if (!active) return;
        setProfile(p);
        setDisplayName(p.displayName || '');
        setHandle(p.handle || '');
        setBio(p.bio || '');
        setTimezone(p.timezone || 'Asia/Shanghai');
        setSelectedLocale(p.locale || 'zh-CN');
      } catch (err) {
        if (active) setError(err instanceof ApiError ? err : new Error(String(err)));
      } finally {
        window.clearTimeout(timer);
        if (active) setLoading(false);
      }
    }
    load();
    return () => { active = false; controller.abort(); window.clearTimeout(timer); };
  }, [loadAttempt]);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim() || saving || avatarBusy) return;

    try {
      setSaving(true);
      const updated = await api.updateProfile(
        {
          displayName: displayName.trim(),
          handle: handle.trim().replace(/^@/, '') || undefined,
          bio: bio.trim() || null,
          timezone,
          locale: selectedLocale,
        },
        profile?.profileVersion
      );

      setProfile(updated);
      setLocale(selectedLocale);
      if (user) {
        setUser({
          ...user,
          displayName: updated.displayName,
          handle: updated.handle,
          locale: updated.locale,
        });
      }
      await refreshSession();
      showToast(t('common.saved'), 'success');
    } catch (err) {
      if (err instanceof ApiError) {
        showToast(getApiErrorMessage(t, err), 'error');
      } else {
        showToast(t('errors.unknown'), 'error');
      }
    } finally {
      setSaving(false);
    }
  };

  if (loading) return <LoadingState />;
  if (error) return <ErrorState error={error} onRetry={() => setLoadAttempt(value => value + 1)} />;

  return (
    <div className="panel">
      <div className="panel-header">
        <div>
          <h2>{t('settings.tabProfile')}</h2>
          <p className="text-muted" style={{ fontSize: 12 }}>
            {t('onboarding.subheadline')}
          </p>
        </div>
      </div>

      {profile && <AvatarSettings profile={profile} onUpdated={setProfile} onBusy={setAvatarBusy} disabled={saving} />}
      <form onSubmit={handleSave}>
        <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
          <Input
            label={t('onboarding.displayNameLabel')}
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            required
          />

          <Input
            label={t('onboarding.handleLabel')}
            prefix="@"
            value={handle}
            onChange={(e) => setHandle(e.target.value.toLowerCase().replace(/[^a-z0-9_]/g, ''))}
            hint={t('onboarding.handleHint')}
          />
        </div>

        <div className="form-group">
          <label className="form-label">{t('onboarding.bioLabel')}</label>
          <textarea
            className="form-input"
            style={{ height: 80, padding: '10px 14px', resize: 'vertical' }}
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
              { value: 'UTC', label: 'UTC' },
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

        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 24 }}>
          <Button type="submit" variant="dark" loading={saving} disabled={avatarBusy}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </div>
  );
};
