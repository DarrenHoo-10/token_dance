import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import { Badge } from '@/components/common/Badge';

export const PrivacySettingsPage: React.FC = () => {
  const { t } = useLocale();

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
          <Badge variant="lime">{t('settings.publicByDefault')}</Badge>
        </div>
        <p className="text-muted" style={{ fontSize: 13, lineHeight: 1.6, margin: 0 }}>
          {t('settings.alwaysPublicNote')}
        </p>
      </div>
    </div>
  );
};
