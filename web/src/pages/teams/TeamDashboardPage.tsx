import React, { useEffect, useState } from 'react';
import { Link, Navigate } from 'react-router-dom';
import { UsersRound } from 'lucide-react';
import { ApiError } from '@/api/client';
import { teamsApi, type TeamInboxInvitation } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { RoleBadge, teamErrorMessage } from './TeamShared';

export const TeamDashboardPage: React.FC = () => {
  const { authenticated, loading: authLoading } = useAuth();
  const { t } = useLocale();
  const { scope, loading, error, refresh } = useTeam();
  const [invitations, setInvitations] = useState<TeamInboxInvitation[]>([]);
  const [inviteError, setInviteError] = useState<ApiError | null>(null);

  useEffect(() => {
    if (!authenticated || scope) return;
    const controller = new AbortController();
    teamsApi.getMyInvitations({}, controller.signal)
      .then((res) => setInvitations(res.invitations || []))
      .catch((err) => {
        if (!controller.signal.aborted) {
          setInviteError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
        }
      });
    return () => controller.abort();
  }, [authenticated, scope]);

  if (authLoading || (authenticated && loading && scope === null && !error)) {
    return <LoadingState />;
  }

  if (!authenticated) {
    return (
      <section className="product-page-shell team-dashboard team-page" aria-labelledby="team-title">
        <div className="product-page-heading">
          <div>
            <span>{t('teams.label')}</span>
            <h1 id="team-title">{t('teams.entryHeadline')}</h1>
            <p>{t('teams.entrySub')}</p>
          </div>
        </div>
        <Card>
          <p>{t('teams.singleSeat')}</p>
          <div style={{ display: 'flex', gap: 12, marginTop: 16, flexWrap: 'wrap' }}>
            <Link className="btn btn-primary" to="/login?return_to=%2Fteams">{t('states.loginButton')}</Link>
            <Link className="btn" to="/register?return_to=%2Fteams">{t('nav.register')}</Link>
          </div>
        </Card>
      </section>
    );
  }

  if (error && !scope) {
    return <ErrorState error={error} description={teamErrorMessage(t, error)} onRetry={() => void refresh()} />;
  }

  if (scope) {
    return <Navigate to={`/teams/${scope.team.id}`} replace />;
  }

  return (
    <section className="product-page-shell team-dashboard team-page" aria-labelledby="team-title">
      <div className="product-page-heading with-actions">
        <div>
          <span>{t('teams.label')}</span>
          <h1 id="team-title">{t('teams.entryHeadline')}</h1>
          <p>{t('teams.entrySub')}</p>
        </div>
        <Link className="btn btn-primary" to="/teams/new">{t('teams.create.action')}</Link>
      </div>

      <Card>
        <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
          <UsersRound size={28} aria-hidden="true" />
          <div>
            <h2 style={{ margin: '0 0 8px', fontSize: 18 }}>{t('teams.entry.emptyTitle')}</h2>
            <p className="text-muted" style={{ fontSize: 13 }}>{t('teams.singleSeat')}</p>
          </div>
        </div>
      </Card>

      <div style={{ marginTop: 24 }}>
        <h2 style={{ fontSize: 18 }}>{t('teams.entry.pendingTitle')}</h2>
        {inviteError && <ErrorState error={inviteError} onRetry={() => void refresh()} />}
        {!inviteError && invitations.length === 0 && (
          <p className="text-muted" style={{ fontSize: 13 }}>{t('teams.entry.noInvites')}</p>
        )}
        {invitations.map((invitation) => (
          <Card key={invitation.id} style={{ marginTop: 12 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', gap: 16, flexWrap: 'wrap' }}>
              <div>
                <strong>{invitation.team.name}</strong>
                <p className="text-muted" style={{ fontSize: 12, margin: '6px 0' }}>
                  {t('teams.invite.from', { name: invitation.inviterDisplayName })}
                </p>
                <RoleBadge role={invitation.invitedRole} />
              </div>
              <Link className="btn btn-dark" to={`/teams/invitations/${invitation.id}`}>{t('teams.invite.review')}</Link>
            </div>
          </Card>
        ))}
      </div>
    </section>
  );
};
