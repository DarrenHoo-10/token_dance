import React, { useState, useEffect } from 'react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Input } from '@/components/common/Input';
import { Select } from '@/components/common/Select';
import { Button } from '@/components/common/Button';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import type { UserProfile, Locale } from '@/types/api';

export const ProfileSettingsPage: React.FC = () => {
  const { user, setUser, refreshSession } = useAuth();
  const { setLocale, t } = useLocale();
  const { showToast } = useNotification();

  const [profile, setProfile] = useState<UserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<ApiError | Error | null>(null);

  const [displayName, setDisplayName] = useState('');
  const [handle, setHandle] = useState('');
  const [bio, setBio] = useState('');
  const [timezone, setTimezone] = useState('Asia/Shanghai');
  const [selectedLocale, setSelectedLocale] = useState<Locale>('zh-CN');

  useEffect(() => {
    async function load() {
      try {
        setLoading(true);
        setError(null);
        const p = await api.getProfile();
        setProfile(p);
        setDisplayName(p.displayName || '');
        setHandle(p.handle || '');
        setBio(p.bio || '');
        setTimezone(p.timezone || 'Asia/Shanghai');
        setSelectedLocale(p.locale || 'zh-CN');
      } catch (err) {
        setError(err instanceof ApiError ? err : new Error(String(err)));
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!displayName.trim()) return;

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
  if (error) return <ErrorState error={error} />;

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
          <Button type="submit" variant="dark" loading={saving}>
            {t('common.save')}
          </Button>
        </div>
      </form>
    </div>
  );
};
