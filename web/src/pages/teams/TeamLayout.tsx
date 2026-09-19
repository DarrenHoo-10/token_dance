import React, { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { teamsApi } from '@/api/teams';
import { Button } from '@/components/common/Button';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { InviteDialog } from './InviteDialog';
import { TeamAvatar, teamErrorMessage } from './TeamShared';
import { BarChart3, Globe2, LockKeyhole, Plus, Settings2, UsersRound } from 'lucide-react';

export const TeamLayout: React.FC = () => {
  const { teamId } = useParams<{ teamId: string }>();
  const { t, locale } = useLocale();
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

  const { team, permissions } = scope;
  const query = location.search;

  return (
    <section className="product-page-shell team-dashboard team-page">
      <div className="sky-team-breadcrumb"><span>{t('teams.label')}</span><span>/</span><LockKeyhole size={13} />{locale === 'zh-CN' ? '仅团队成员可见' : 'Private to your team'}</div>
      <div className="product-page-heading with-actions sky-team-banner">
        <div className="team-identity">
          <TeamAvatar team={team} />
          <div>
            <div className="sky-team-eyebrow">BUILD SOMETHING TOGETHER <span>{t(`teams.role.${scope.membership.role}`)}</span></div>
            <h1>{team.name}</h1>
            <p>{team.description || t('teams.overview.noDescription')}</p>
            <div className="sky-team-meta">{team.memberCount != null && <span><UsersRound size={14} />{team.memberCount} {t('teams.nav.members')}</span>}<span><Globe2 size={13} />{team.timezone}</span>{team.createdAt && <span>{locale === 'zh-CN' ? '创建于' : 'Created'} {team.createdAt.slice(0, 10)}</span>}</div>
          </div>
        </div>
        <div className="sky-team-art" aria-hidden="true"><i /><img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="" /><span>SMALL IDEAS.<br />SHARED POSSIBILITIES.</span></div>
        <div className="team-heading-actions">
          {permissions.inviteMembers ? (
            <Button variant="primary" onClick={() => setInviteOpen(true)}><Plus size={17} />{t('teams.invite.action')}</Button>
          ) : (
            <Button variant="outline" onClick={() => navigate(`/teams/${team.id}/settings`)}>{t('teams.nav.settings')}</Button>
          )}
        </div>
      </div>

      <nav className="team-tabs" aria-label={t('teams.nav.tabs')}>
        <NavLink to={`/teams/${team.id}${query}`} end className={({ isActive }) => (isActive ? 'active' : '')}><BarChart3 size={17} />{t('teams.nav.panel')}</NavLink>
        <NavLink to={`/teams/${team.id}/members${query}`} className={({ isActive }) => (isActive ? 'active' : '')}><UsersRound size={17} />{t('teams.nav.members')}</NavLink>
        <NavLink to={`/teams/${team.id}/settings${query}`} className={({ isActive }) => (isActive ? 'active' : '')}><Settings2 size={17} />{t('teams.nav.settings')}</NavLink>
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
