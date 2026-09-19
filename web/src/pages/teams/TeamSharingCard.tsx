import { LockKeyhole, ShieldCheck } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';

export function TeamSharingCard() {
  const { t } = useLocale();
  const rows = [
    { key: 'base', title: t('teams.sharing.base'), text: t('teams.sharing.baseHint') },
    { key: 'classification', title: t('teams.sharing.classification'), text: t('teams.sharing.classificationHint') },
    { key: 'cost', title: t('teams.sharing.cost'), text: t('teams.sharing.costHint') },
  ] as const;

  return (
    <section className="tw-card tw-sharing-card">
      <div className="tw-card-heading">
        <div>
          <h2><ShieldCheck size={20} />{t('teams.settings.mySharing')}</h2>
          <p>{t('teams.sharing.alwaysOn')}</p>
        </div>
      </div>
      {rows.map((item) => (
        <div className="tw-sharing-row" key={item.key}>
          <div>
            <strong>{item.title}</strong>
            <p>{item.text}</p>
          </div>
          <button type="button" className="tw-switch" role="switch" aria-label={item.title} aria-checked disabled>
            <span />
          </button>
        </div>
      ))}
      <div className="tw-sharing-note">
        <LockKeyhole size={16} />
        <p>{t('teams.sharing.alwaysOnHint')}</p>
      </div>
    </section>
  );
}
