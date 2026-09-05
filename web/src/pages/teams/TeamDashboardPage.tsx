import React from 'react';
import { UsersRound } from 'lucide-react';
import { Link } from 'react-router-dom';
import { EmptyState } from '@/components/states/EmptyState';
import { useLocale } from '@/context/LocaleContext';

export const TeamDashboardPage: React.FC = () => {
  const { t } = useLocale();

  return (
    <section className="product-page-shell team-dashboard" aria-labelledby="team-title">
      <div className="product-page-heading">
        <div><span>{t('teams.label')}</span><h1 id="team-title">{t('teams.title')}</h1></div>
      </div>
      <EmptyState
        icon={<UsersRound size={32} aria-hidden="true" />}
        title={t('teams.unavailableTitle')}
        description={t('teams.unavailableDesc')}
      />
      <div className="unavailable-actions">
        <Link className="btn btn-dark" to="/me">{t('publicProfile.viewOwnData')}</Link>
      </div>
    </section>
  );
};
