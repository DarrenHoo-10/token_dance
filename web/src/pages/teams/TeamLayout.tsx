import React, { useEffect, useState } from 'react';
import { NavLink, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { teamsApi } from '@/api/teams';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { InviteDialog } from './InviteDialog';
import { MemberAvatar, teamErrorMessage } from './TeamShared';
import { avatarUrl } from '@/utils/avatar';
import { formatDotDate, formatUtcOffset } from './teamUtils';
import { BarChart3, Globe2, Layers3, LockKeyhole, Plus, Settings2, UsersRound } from 'lucide-react';
import type { TeamMember } from '@/api/teams';

export type TeamOutletContext = {
  openInvite: () => void;
  setUpdatedAt: (value: string | null) => void;
};

export const TeamLayout: React.FC = () => {
  const { teamId } = useParams<{ teamId: string }>();
  const { t } = useLocale();
  const { authenticated, loading: authLoading } = useAuth();
  const { scope, loading, refresh, applyScope } = useTeam();
  const navigate = useNavigate();
  const location = useLocation();
  const [inviteOpen, setInviteOpen] = useState(Boolean((location.state as { openInvite?: boolean } | null)?.openInvite));
  const [pageError, setPageError] = useState<ApiError | null>(null);
  const [faces, setFaces] = useState<TeamMember[]>([]);
  const [updatedAt, setUpdatedAt] = useState<string | null>(null);

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

  useEffect(() => {
    if (!authenticated || !teamId || !scope || scope.team.id !== teamId) return;
    const controller = new AbortController();
    teamsApi.getMembers(teamId, {}, controller.signal)
      .then((res) => { if (!controller.signal.aborted) setFaces(res.members || []); })
      .catch(() => { if (!controller.signal.aborted) setFaces([]); });
    return () => controller.abort();
  }, [authenticated, scope, teamId]);

  if (authLoading || (loading && !scope)) return <LoadingState />;
  if (!authenticated) return <LoadingState />;
  if (pageError) return <ErrorState error={pageError} description={teamErrorMessage(t, pageError)} onRetry={() => void refresh()} />;
  if (!scope || !teamId || scope.team.id !== teamId) return <LoadingState />;

  const { team, permissions } = scope;
  const query = location.search;
  const avatarSrc = team.avatarUrl
    ? avatarUrl(team.avatarUrl.startsWith('/api/') ? team.avatarUrl : `/api/v1/teams/${team.id}/avatar/content`)
    : '';
  const memberCount = team.memberCount ?? faces.length;
  const extraFaces = Math.max(0, faces.length - 5);
  const outlet: TeamOutletContext = {
    openInvite: () => setInviteOpen(true),
    setUpdatedAt,
  };

  return (
    <section className="team-dashboard team-page tw-workspace">
      <div className="tw-breadcrumb">
        <span>{t('teams.workspace')}</span>
        <span>/</span>
        <LockKeyhole size={12} />
        <span>{t('teams.privateNote')}</span>
      </div>
      <section className="tw-banner" aria-labelledby="team-title">
        <div className="tw-banner-copy">
          <div className="tw-team-avatar">
            {avatarSrc ? <img src={avatarSrc} alt="" /> : <Layers3 size={34} strokeWidth={1.5} />}
          </div>
          <div>
            <div className="tw-team-eyebrow">
              BUILD SOMETHING TOGETHER
              <span className={`tw-role ${scope.membership.role}`}>{t(`teams.role.${scope.membership.role}`)}</span>
            </div>
            <h1 id="team-title">{team.name}<span className="tw-team-dot">.</span></h1>
            <p>{team.description || t('teams.overview.noDescription')}</p>
            <div className="tw-team-meta">
              <span><UsersRound size={14} />{memberCount} {t('teams.builders')}</span>
              <span><Globe2 size={13} />{team.timezone}</span>
              {team.createdAt && <span className="tw-created">{t('teams.since')} {formatDotDate(team.createdAt)}</span>}
            </div>
          </div>
        </div>
        <div className="tw-banner-action">
          {faces.length > 0 && (
            <div className="tw-avatar-stack" aria-hidden="true">
              {faces.slice(0, 5).map((member) => (
                <MemberAvatar key={member.membershipId} name={member.displayName} url={member.avatarUrl} />
              ))}
              {extraFaces > 0 && <span>+{extraFaces}</span>}
            </div>
          )}
          {permissions.inviteMembers ? (
            <button type="button" className="button primary" onClick={() => setInviteOpen(true)}>
              <Plus size={17} />{t('teams.inviteTeammates')}
            </button>
          ) : (
            <button type="button" className="button secondary" onClick={() => navigate(`/teams/${team.id}/members`)}>
              <UsersRound size={17} />{t('teams.viewMembers')}
            </button>
          )}
        </div>
      </section>

      <div className="tw-tabbar">
        <nav aria-label={t('teams.nav.tabs')}>
          <NavLink to={`/teams/${team.id}${query}`} end className={({ isActive }) => (isActive ? 'active' : '')}>
            <BarChart3 size={17} />{t('teams.nav.panel')}
          </NavLink>
          <NavLink to={`/teams/${team.id}/members${query}`} className={({ isActive }) => (isActive ? 'active' : '')}>
            <UsersRound size={17} />{t('teams.nav.members')}{Number(memberCount) > 0 && <span>{memberCount}</span>}
          </NavLink>
          <NavLink to={`/teams/${team.id}/settings${query}`} className={({ isActive }) => (isActive ? 'active' : '')}>
            <Settings2 size={17} />{t('teams.nav.settings')}
          </NavLink>
        </nav>
        <span className="tw-sync">
          <i />
          {updatedAt
            ? t('teams.overview.updatedAt', { time: `${updatedAt} · ${formatUtcOffset(team.timezone)}` })
            : formatUtcOffset(team.timezone)}
        </span>
      </div>

      <Outlet context={outlet} />

      <div className="tw-footer">
        <span>
          <img src={`${import.meta.env.BASE_URL}logo-tokendance-v2.png`} alt="" />
          TokenDance <i>/</i> BETTER TOGETHER
        </span>
        <span>{t('teams.footerTag')}</span>
      </div>

      {permissions.inviteMembers && (
        <InviteDialog
          isOpen={inviteOpen}
          onClose={() => setInviteOpen(false)}
          teamId={team.id}
          teamName={team.name}
          permissions={permissions}
        />
      )}
    </section>
  );
};
