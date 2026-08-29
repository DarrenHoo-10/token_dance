import React, { useState, useEffect } from 'react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Switch } from '@/components/common/Switch';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { api, ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';
import type { PrivacySettings } from '@/types/api';

export const PrivacySettingsPage: React.FC = () => {
  const { refreshSession } = useAuth();
  const { t } = useLocale();
  const { showToast } = useNotification();

  const [privacy, setPrivacy] = useState<PrivacySettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<ApiError | Error | null>(null);

  useEffect(() => {
    async function load() {
      try {
        setLoading(true);
        setError(null);
        const p = await api.getPrivacy();
        setPrivacy(p);
      } catch (err) {
        setError(err instanceof ApiError ? err : new Error(String(err)));
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  const handleSave = async () => {
    if (!privacy) return;

    try {
      setSaving(true);
      const updated = await api.updatePrivacy(
        {
          publicProfileEnabled: privacy.publicProfileEnabled,
          leaderboardVisibility: privacy.leaderboardVisibility,
          showBio: privacy.showBio,
          showTokenTotal: privacy.showTokenTotal,
          showTrends: privacy.showTrends,
          showActivityCalendar: privacy.showActivityCalendar,
          showAgentBreakdown: privacy.showAgentBreakdown,
          showSkillRanking: privacy.showSkillRanking,
          showAchievements: privacy.showAchievements,
        },
        privacy.privacyVersion
      );

      setPrivacy(updated);
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
  if (!privacy) return null;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div className="panel">
        <div className="panel-header">
          <div>
            <h2>{t('settings.privacyCardTitle')}</h2>
            <p className="text-muted" style={{ fontSize: 12 }}>
              {t('settings.privacyCardSub')}
            </p>
          </div>
          <Badge variant="lime">{t('settings.privateByDefault')}</Badge>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
          <div style={{ paddingBottom: 16, borderBottom: '1px solid var(--border-light)' }}>
            <Switch
              label={t('settings.joinLeaderboard')}
              description={t('settings.joinLeaderboardDesc')}
              checked={privacy.leaderboardVisibility === 'public'}
              onChange={(checked) =>
                setPrivacy({
                  ...privacy,
                  leaderboardVisibility: checked ? 'public' : 'private',
                  publicProfileEnabled: checked,
                })
              }
            />
          </div>

          <div style={{ paddingBottom: 16, borderBottom: '1px solid var(--border-light)' }}>
            <Switch
              label={t('settings.showTokenTotal')}
              description={t('settings.showTokenTotalDesc')}
              checked={privacy.showTokenTotal}
              onChange={(checked) =>
                setPrivacy({
                  ...privacy,
                  showTokenTotal: checked,
                  showTrends: checked,
                })
              }
            />
          </div>

          <div style={{ paddingBottom: 16, borderBottom: '1px solid var(--border-light)' }}>
            <Switch
              label={t('settings.showAgentBreakdown')}
              description={t('settings.showAgentBreakdownDesc')}
              checked={privacy.showAgentBreakdown}
              onChange={(checked) => setPrivacy({ ...privacy, showAgentBreakdown: checked })}
            />
          </div>

          <div style={{ paddingBottom: 16, borderBottom: '1px solid var(--border-light)' }}>
            <Switch
              label={t('settings.showActivityCalendar')}
              description={t('settings.showActivityCalendarDesc')}
              checked={privacy.showActivityCalendar}
              onChange={(checked) => setPrivacy({ ...privacy, showActivityCalendar: checked })}
            />
          </div>

          <div style={{ paddingBottom: 16, borderBottom: '1px solid var(--border-light)' }}>
            <Switch
              label={t('settings.showBio')}
              description={t('settings.showBioDesc')}
              checked={privacy.showBio}
              onChange={(checked) => setPrivacy({ ...privacy, showBio: checked })}
            />
          </div>

          <div>
            <Switch
              label={t('settings.showSkillRanking')}
              description={t('settings.showSkillRankingDesc')}
              checked={privacy.showSkillRanking}
              onChange={(checked) => setPrivacy({ ...privacy, showSkillRanking: checked })}
            />
          </div>
        </div>

        <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 24 }}>
          <Button variant="primary" loading={saving} onClick={handleSave}>
            {t('common.save')}
          </Button>
        </div>
      </div>
    </div>
  );
};
