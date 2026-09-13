import React, { useEffect, useState } from 'react';
import { Link, useLocation, useNavigate, useParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { TEAM_JOIN_SHARING, teamsApi, type InviteLinkPreview } from '@/api/teams';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import {
  captureJoinToken,
  clearJoinToken,
  createIdempotencyKey,
  joinReturnTo,
  readJoinToken,
  redactInviteSecrets,
} from './teamUtils';
import { RoleBadge, TeamGateLink, teamErrorMessage } from './TeamShared';

type JoinState =
  | 'reading'
  | 'missingToken'
  | 'needLogin'
  | 'verifying'
  | 'confirm'
  | 'submitting'
  | 'joined'
  | 'alreadyHere'
  | 'otherTeam'
  | 'expired'
  | 'revoked'
  | 'exhausted'
  | 'reinvitation'
  | 'unavailable'
  | 'failed';

export const JoinTeamPage: React.FC = () => {
  const { linkId } = useParams<{ linkId: string }>();
  const location = useLocation();
  const { authenticated, loading: authLoading } = useAuth();
  const { t } = useLocale();
  const { scope, applyScope, refresh } = useTeam();
  const navigate = useNavigate();
  const [pageState, setPageState] = useState<JoinState>('reading');
  const [token, setToken] = useState<string | null>(null);
  const [preview, setPreview] = useState<InviteLinkPreview | null>(null);
  const [errorText, setErrorText] = useState<string | null>(null);
  const [idempotencyKey] = useState(() => createIdempotencyKey());

  const safeReturnTo = linkId ? joinReturnTo(linkId) : '/teams';

  useEffect(() => {
    const meta = document.createElement('meta');
    meta.name = 'referrer';
    meta.content = 'no-referrer';
    document.head.appendChild(meta);
    return () => {
      meta.remove();
    };
  }, []);

  useEffect(() => {
    if (!linkId) {
      setPageState('unavailable');
      return;
    }
    const captured = captureJoinToken(linkId, location.hash || window.location.hash);
    if (!captured) {
      setPageState('missingToken');
      setErrorText(t('teams.join.reopenLink'));
      return;
    }
    setToken(captured);
    if (!authenticated && !authLoading) {
      setPageState('needLogin');
    }
  }, [authLoading, authenticated, linkId, location.hash, t]);

  useEffect(() => {
    if (!linkId || !authenticated || authLoading) return;
    const stored = token || readJoinToken(linkId);
    if (!stored) {
      setPageState('missingToken');
      return;
    }
    const controller = new AbortController();
    setPageState('verifying');
    teamsApi.previewInviteLink(linkId, stored, { signal: controller.signal })
      .then((result) => {
        setPreview(result);
        if (result.alreadyMember) {
          setPageState('alreadyHere');
          return;
        }
        if (result.currentOtherTeam || scope) {
          setPageState('otherTeam');
          return;
        }
        if (result.removedPreviously) {
          setPageState('reinvitation');
          return;
        }
        if (result.effectiveState === 'expired') setPageState('expired');
        else if (result.effectiveState === 'revoked') setPageState('revoked');
        else if (result.effectiveState === 'exhausted') setPageState('exhausted');
        else setPageState('confirm');
      })
      .catch((err) => {
        const apiErr = err instanceof ApiError ? err : null;
        setErrorText(apiErr ? redactInviteSecrets(teamErrorMessage(t, apiErr)) : t('errors.unknown'));
        if (apiErr?.code === 'TEAM_INVITE_LINK_EXPIRED') setPageState('expired');
        else if (apiErr?.code === 'TEAM_INVITE_LINK_REVOKED') setPageState('revoked');
        else if (apiErr?.code === 'TEAM_INVITE_LINK_EXHAUSTED') setPageState('exhausted');
        else if (apiErr?.code === 'TEAM_REINVITATION_REQUIRED') setPageState('reinvitation');
        else if (apiErr?.code === 'TEAM_MEMBERSHIP_EXISTS') setPageState('otherTeam');
        else if (apiErr?.code === 'TEAM_INVITE_LINK_NOT_FOUND' || apiErr?.status === 404) setPageState('unavailable');
        else setPageState('failed');
      });
    return () => controller.abort();
  }, [authLoading, authenticated, linkId, t, token]);

  const accept = async () => {
    if (!linkId || !token || !preview) return;
    try {
      setPageState('submitting');
      const result = await teamsApi.acceptInviteLink(
        linkId,
        { token, expectedLinkVersion: preview.linkVersion, sharing: TEAM_JOIN_SHARING },
        { idempotencyKey }
      );
      applyScope(result);
      clearJoinToken(linkId);
      setPageState(result.alreadyMember ? 'alreadyHere' : 'joined');
      navigate(`/teams/${result.team.id}`, { replace: true, state: { alreadyMember: result.alreadyMember } });
    } catch (err) {
      const apiErr = err instanceof ApiError ? err : null;
      setErrorText(apiErr ? redactInviteSecrets(teamErrorMessage(t, apiErr)) : t('errors.unknown'));
      if (apiErr?.code === 'TEAM_MEMBERSHIP_EXISTS') {
        await refresh();
        setPageState('otherTeam');
        return;
      }
      if (apiErr?.code === 'TEAM_REINVITATION_REQUIRED') setPageState('reinvitation');
      else if (apiErr?.code === 'TEAM_INVITE_LINK_EXPIRED') setPageState('expired');
      else if (apiErr?.code === 'TEAM_INVITE_LINK_REVOKED') setPageState('revoked');
      else if (apiErr?.code === 'TEAM_INVITE_LINK_EXHAUSTED') setPageState('exhausted');
      else if (apiErr?.code === 'TEAM_INVITE_LINK_ALREADY_USED') setPageState('reinvitation');
      else setPageState('failed');
    }
  };

  if (authLoading || pageState === 'reading' || pageState === 'verifying') {
    return <LoadingState />;
  }

  const closed = ['expired', 'revoked', 'exhausted', 'reinvitation', 'unavailable', 'missingToken'].includes(pageState);

  return (
    <section className="product-page-shell team-page">
      <div className="product-page-heading">
        <span>{t('teams.label')}</span>
        <h1>{t('teams.join.title')}</h1>
      </div>
      <Card>
        {pageState === 'needLogin' && (
          <>
            <h2>{t('teams.join.needLoginTitle')}</h2>
            <p>{t('teams.join.needLogin')}</p>
            <div style={{ display: 'flex', gap: 12, marginTop: 16 }}>
              <Link className="btn btn-primary" to={`/login?return_to=${encodeURIComponent(safeReturnTo)}`}>{t('states.loginButton')}</Link>
              <Link className="btn" to={`/register?return_to=${encodeURIComponent(safeReturnTo)}`}>{t('nav.register')}</Link>
            </div>
          </>
        )}

        {preview && !closed && pageState !== 'needLogin' && (
          <>
            <h2>{preview.teamName}</h2>
            <p className="text-muted">{t('teams.invite.from', { name: preview.inviterDisplayName })}</p>
            <RoleBadge role={preview.role} />
            <p className="text-muted" style={{ fontSize: 12 }}>{t('teams.invite.expiresAt', { date: preview.expiresAt })}</p>
          </>
        )}

        {pageState === 'alreadyHere' && <p role="status">{t('teams.join.alreadyHere')}</p>}
        {pageState === 'otherTeam' && (
          <div className="team-status-banner warn">
            <p>{t('teams.create.alreadyDesc')}</p>
            {scope && <TeamGateLink to={`/teams/${scope.team.id}`}>{t('teams.create.viewMine')}</TeamGateLink>}
          </div>
        )}
        {pageState === 'expired' && <p>{t('teams.join.expired')}</p>}
        {pageState === 'revoked' && <p>{t('teams.join.revoked')}</p>}
        {pageState === 'exhausted' && <p>{t('teams.join.exhausted')}</p>}
        {pageState === 'reinvitation' && <p>{t('teams.join.reinvitation')}</p>}
        {pageState === 'unavailable' && <p>{t('teams.join.unavailable')}</p>}
        {pageState === 'missingToken' && <p>{t('teams.join.reopenLink')}</p>}
        {pageState === 'failed' && <p role="alert">{errorText || t('errors.unknown')}</p>}

        {(pageState === 'confirm' || pageState === 'submitting') && (
          <Button variant="primary" loading={pageState === 'submitting'} onClick={() => void accept()}>
            {t('teams.join.submit')}
          </Button>
        )}
      </Card>
    </section>
  );
};

