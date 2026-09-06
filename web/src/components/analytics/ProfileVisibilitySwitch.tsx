import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { api, ApiError } from '@/api/client';
import { Switch } from '@/components/common/Switch';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { getApiErrorMessage } from '@/i18n';
import type { PrivacySettings } from '@/types/api';

export function ProfileVisibilitySwitch() {
  const { locale, t } = useLocale();
  const { showToast } = useNotification();
  const zh = locale === 'zh-CN';
  const [privacy, setPrivacy] = useState<PrivacySettings | null>(null);
  const [saving, setSaving] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);

  useEffect(() => {
    let active = true;
    api.getPrivacy().then(
      value => { if (active) setPrivacy(value); },
      () => { if (active) setLoadFailed(true); },
    );
    return () => { active = false; };
  }, []);

  async function updateVisibility(enabled: boolean) {
    if (!privacy || saving) return;
    setSaving(true);
    try {
      const updated = await api.updatePrivacy({
        publicProfileEnabled: enabled,
        leaderboardVisibility: enabled ? 'public' : 'private',
        showBio: privacy.showBio,
        showTokenTotal: privacy.showTokenTotal,
        showTrends: privacy.showTrends,
        showActivityCalendar: privacy.showActivityCalendar,
        showAgentBreakdown: privacy.showAgentBreakdown,
        showSkillRanking: privacy.showSkillRanking,
        showAchievements: privacy.showAchievements,
      }, privacy.privacyVersion);
      setPrivacy(updated);
      showToast(t('common.saved'), 'success');
    } catch (error) {
      showToast(error instanceof ApiError ? getApiErrorMessage(t, error) : t('errors.unknown'), 'error');
      // Another tab may have changed the privacy version. Refresh before retrying.
      try {
        setPrivacy(await api.getPrivacy());
      } catch {
        setPrivacy(null);
        setLoadFailed(true);
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="panel" style={{ marginBottom: 24, padding: '16px 20px' }}>
      {loadFailed ? (
        <p role="status" style={{ margin: 0 }}>
          {zh ? '公开状态暂时无法读取，不影响查看自己的数据。' : 'Visibility is unavailable. Your personal data remains accessible.'}
          {' '}<Link to="/settings/privacy">{t('settings.tabPrivacy')}</Link>
        </p>
      ) : (
        <Switch
          label={zh ? '公开我的数据' : 'Make my data public'}
          description={!privacy ? t('common.loading') : saving ? (zh ? '正在保存…' : 'Saving…') : zh
            ? '开启后其他人可查看你的详细资料页；关闭后仍可查看自己的全部数据。排行榜仍显示头像、昵称、Token 和排名。'
            : 'When on, others can open your detailed profile. Your own data stays available. Your avatar, nickname, tokens, and rank stay on the leaderboard.'}
          checked={privacy?.publicProfileEnabled ?? false}
          disabled={!privacy || saving}
          onChange={updateVisibility}
        />
      )}
    </div>
  );
}
