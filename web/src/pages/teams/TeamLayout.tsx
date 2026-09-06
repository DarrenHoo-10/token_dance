import React, { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { teamsApi } from '@/api/teams';
import { Badge } from '@/components/common/Badge';
import { Button } from '@/components/common/Button';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { InviteDialog } from './InviteDialog';
import { RoleBadge, TeamAvatar, teamErrorMessage } from './TeamShared';

export const TeamLayout: React.FC = () => {
  const { teamId } = useParams<{ teamId: string }>();
  const { t } = useLocale();
  const { authenticated, loading: authLoading } = useAuth();
  const { scope, loading, refresh, applyScope } = useTeam();
  const navigate = useNavigate();
  const location = useLocation();
  const [inviteOpen, setInviteOpen] = useState(Boolean((location.state as { openInvite?: boolean } | null)?.openInvite));
  const [pageError, setPageError] = useState<ApiError | null>(null);

  useEffect(() => {
    if (authLoading) return;
    if (!authenticated) {
      navigate(`/login?return_to=${encodeURIComponent(`/teams/${teamId || ''}`)}`, { replace: true });
    }
  }, [authLoading, authenticated, navigate, teamId]);

  useEffect(() => {
    if (!authenticated || !teamId) return;
    if (scope?.team.id === teamId) {
      setPageError(null);
      return;
    }
    const controller = new AbortController();
    teamsApi.getTeam(teamId, controller.signal)
      .then((next) => {
        applyScope(next);
        setPageError(null);
      })
      .catch(async (err) => {
        if (controller.signal.aborted) return;
        if (err instanceof ApiError && (err.code === 'TEAM_NOT_FOUND' || err.status === 404)) {
          await refresh();
          navigate('/teams', { replace: true });
          return;
        }
        setPageError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
      });
    return () => controller.abort();
  }, [applyScope, authenticated, navigate, refresh, scope?.team.id, teamId]);

  if (authLoading || (loading && !scope)) return <LoadingState />;
  if (!authenticated) return <LoadingState />;
  if (pageError) return <ErrorState error={pageError} description={teamErrorMessage(t, pageError)} onRetry={() => void refresh()} />;
  if (!scope || !teamId || scope.team.id !== teamId) return <LoadingState />;

  const { team, membership, permissions } = scope;
  const query = location.search;

  return (
    <section className="product-page-shell team-dashboard team-page">
      <div className="product-page-heading with-actions">
        <div className="team-identity">
          <TeamAvatar team={team} />
          <div>
            <span>{t('teams.label')}</span>
            <h1>{team.name}</h1>
            <p>{team.description || t('teams.overview.noDescription')}</p>
            <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
              <Badge>{t('common.private')}</Badge>
              <RoleBadge role={membership.role} />
            </div>
          </div>
        </div>
        <div>
          {permissions.inviteMembers ? (
            <Button variant="primary" onClick={() => setInviteOpen(true)}>{t('teams.invite.action')}</Button>
          ) : (
            <Button variant="outline" onClick={() => navigate(`/teams/${team.id}/settings`)}>{t('teams.settings.mySharing')}</Button>
          )}
        </div>
      </div>

      <nav className="team-tabs" aria-label={t('teams.nav.tabs')}>
        <NavLink to={`/teams/${team.id}${query}`} end className={({ isActive }) => (isActive ? 'active' : '')}>{t('teams.nav.overview')}</NavLink>
        <NavLink to={`/teams/${team.id}/members${query}`} className={({ isActive }) => (isActive ? 'active' : '')}>{t('teams.nav.members')}</NavLink>
        <NavLink to={`/teams/${team.id}/analytics${query}`} className={({ isActive }) => (isActive ? 'active' : '')}>{t('teams.nav.analytics')}</NavLink>
        <NavLink to={`/teams/${team.id}/settings`} className={({ isActive }) => (isActive ? 'active' : '')}>{t('teams.nav.settings')}</NavLink>
      </nav>

      <Outlet context={{ openInvite: () => setInviteOpen(true) }} />

      {permissions.inviteMembers && (
        <InviteDialog
          isOpen={inviteOpen}
          onClose={() => setInviteOpen(false)}
          teamId={team.id}
          permissions={permissions}
        />
      )}
    </section>
  );
};
