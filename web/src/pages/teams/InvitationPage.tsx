import React, { useEffect, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { TEAM_JOIN_SHARING, teamsApi, type InvitationPreview } from '@/api/teams';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { createIdempotencyKey } from './teamUtils';
import { RoleBadge, TeamGateLink, teamErrorMessage } from './TeamShared';

export const InvitationPage: React.FC = () => {
  const { invitationId } = useParams<{ invitationId: string }>();
  const { authenticated, loading: authLoading } = useAuth();
  const { t } = useLocale();
  const { scope, refresh, applyScope } = useTeam();
  const navigate = useNavigate();
  const [preview, setPreview] = useState<InvitationPreview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [alreadyHere, setAlreadyHere] = useState(false);
  const [idempotencyKey] = useState(() => createIdempotencyKey());

  const returnTo = `/teams/invitations/${invitationId || ''}`;

  useEffect(() => {
    if (authLoading) return;
    if (!authenticated) {
      setLoading(false);
      return;
    }
    if (!invitationId) return;
    const controller = new AbortController();
    setLoading(true);
    Promise.all([teamsApi.getInvitation(invitationId, controller.signal), refresh(controller.signal)])
      .then(([invitation]) => {
        setPreview(invitation);
        if (scope?.team.id === invitation.team.id) setAlreadyHere(true);
        setError(null);
      })
      .catch((err) => setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' })))
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [authLoading, authenticated, invitationId, refresh, scope?.team.id]);

  if (authLoading || (authenticated && loading)) return <LoadingState />;

  if (!authenticated) {
    return (
      <section className="product-page-shell team-page">
        <Card>
          <h1>{t('teams.join.needLoginTitle')}</h1>
          <p>{t('teams.invite.needLogin')}</p>
          <div style={{ display: 'flex', gap: 12, marginTop: 16 }}>
            <Link className="btn btn-primary" to={`/login?return_to=${encodeURIComponent(returnTo)}`}>{t('states.loginButton')}</Link>
            <Link className="btn" to={`/register?return_to=${encodeURIComponent(returnTo)}`}>{t('nav.register')}</Link>
          </div>
        </Card>
      </section>
    );
  }

  if (error) {
    return <ErrorState error={error} description={teamErrorMessage(t, error)} onRetry={() => window.location.reload()} />;
  }

  if (!preview) return <LoadingState />;

  const otherTeam = scope && scope.team.id !== preview.team.id ? scope : null;

  const accept = async () => {
    if (!invitationId) return;
    try {
      setBusy(true);
      const result = await teamsApi.acceptInvitation(
        invitationId,
        { expectedInvitationVersion: preview.version, sharing: TEAM_JOIN_SHARING },
        { idempotencyKey }
      );
      applyScope(result);
      if (result.alreadyMember) setAlreadyHere(true);
      navigate(`/teams/${result.team.id}`, { replace: true, state: { alreadyMember: result.alreadyMember } });
    } catch (err) {
      if (err instanceof ApiError && err.code === 'TEAM_MEMBERSHIP_EXISTS') {
        await refresh();
        return;
      }
      setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="product-page-shell team-page">
      <div className="product-page-heading">
        <span>{t('teams.label')}</span>
        <h1>{t('teams.invite.acceptTitle')}</h1>
      </div>
      <Card>
        <h2 style={{ marginTop: 0 }}>{preview.team.name}</h2>
        <p className="text-muted">{t('teams.invite.from', { name: preview.inviterDisplayName })}</p>
        <RoleBadge role={preview.invitedRole} />
        <p className="text-muted" style={{ fontSize: 12 }}>{t('teams.invite.expiresAt', { date: preview.expiresAt })}</p>

        {alreadyHere && <p role="status">{t('teams.join.alreadyHere')}</p>}
        {otherTeam && (
          <div className="team-status-banner warn">
            <p>{t('teams.create.alreadyDesc')}</p>
            <TeamGateLink to={`/teams/${otherTeam.team.id}`}>{t('teams.create.viewMine')}</TeamGateLink>
          </div>
        )}
        {!alreadyHere && !otherTeam && (
          <Button variant="primary" loading={busy} onClick={() => void accept()}>{t('teams.join.submit')}</Button>
        )}
      </Card>
    </section>
  );
};
