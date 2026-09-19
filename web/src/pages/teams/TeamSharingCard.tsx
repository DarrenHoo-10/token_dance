import { ShieldCheck } from 'lucide-react';
import { Card } from '@/components/common/Card';
import { useLocale } from '@/context/LocaleContext';

export function TeamSharingCard() {
  const { t } = useLocale();
  return (
    <Card className="sky-team-sharing">
      <div className="panel-header sky-settings-heading">
        <div>
          <h2><ShieldCheck size={21} />{t('teams.sharing.title')}</h2>
          <p>{t('teams.sharing.alwaysOn')}</p>
        </div>
      </div>
      <p className="sky-sharing-note"><ShieldCheck size={16} />{t('teams.sharing.alwaysOnHint')}</p>
    </Card>
  );
}
