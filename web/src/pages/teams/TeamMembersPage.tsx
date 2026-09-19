import React, { useEffect, useState } from 'react';
import { useOutletContext } from 'react-router-dom';
import { Crown, Link2, Mail, Plus, Search, ShieldCheck, Trash2, X } from 'lucide-react';
import { ApiError } from '@/api/client';
import {
  teamsApi,
  type InviteLink,
  type ManagedInvitation,
  type MemberDetail,
  type TeamMember,
  type TeamRole,
} from '@/api/teams';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import { createIdempotencyKey, formatDecimalAmount, formatDotDate, formatTokenCompact, metricDisplay } from './teamUtils';
import { AnalysisSkeleton, MemberAvatar, memberContributionState, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';

type MemberTab = 'joined' | 'pending' | 'links';

export const TeamMembersPage: React.FC = () => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const { scope, authRevision, refresh, applyScope } = useTeam();
  const outlet = useOutletContext<{ openInvite?: () => void } | undefined>();
  const { range, from, to, agent, provider, model } = useTeamSearchFilters();
  const { analysis, updating, error: analysisError } = useTeamAnalysis({
    teamId: scope?.team.id,
    authRevision,
    range,
    from,
    to,
    agent,
    provider,
    model,
  });
  const snapshotId = analysis?.snapshot.id;
  const canManage = Boolean(scope?.permissions.inviteMembers);
  const isOwner = scope?.membership.role === 'owner';
  const [tab, setTab] = useState<MemberTab>('joined');
  const [query, setQuery] = useState('');
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [invitations, setInvitations] = useState<ManagedInvitation[]>([]);
  const [links, setLinks] = useState<InviteLink[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [detail, setDetail] = useState<MemberDetail | null>(null);
  const [sourceRow, setSourceRow] = useState<string | null>(null);
  const [removeTarget, setRemoveTarget] = useState<TeamMember | null>(null);

  useEffect(() => {
    if (!scope) return;
    const controller = new AbortController();
    const load = async () => {
      try {
        const memberRes = await teamsApi.getMembers(scope.team.id, { q: query || undefined, snapshotId }, controller.signal);
        setMembers(memberRes.members || []);
        if (canManage) {
          const [inviteRes, linkRes] = await Promise.all([
            teamsApi.getInvitations(scope.team.id, {}, controller.signal),
            teamsApi.getInviteLinks(scope.team.id, {}, controller.signal),
          ]);
          setInvitations(inviteRes.invitations || []);
          setLinks(linkRes.links || []);
        }
        setError(null);
      } catch (err) {
        if (!controller.signal.aborted) {
          setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' }));
        }
      }
    };
    void load();
    return () => controller.abort();
  }, [canManage, query, scope, snapshotId]);

  if (!scope) return null;
  if (analysisError) return <ErrorState error={analysisError} description={teamErrorMessage(t, analysisError)} />;
  if (updating && !analysis) return <AnalysisSkeleton />;
  if (error) return <ErrorState error={error} description={teamErrorMessage(t, error)} />;

  const visible = members.filter((member) => `${member.displayName} ${member.handle || ''}`.toLowerCase().includes(query.toLowerCase()));
  const pendingCount = invitations.filter((item) => item.status === 'pending').length;

  const openDetail = async (member: TeamMember) => {
    if (!member.canOpenDetail || !snapshotId) return;
    const next = await teamsApi.getMemberDetail(scope.team.id, member.membershipId, snapshotId);
    setDetail(next);
    setSourceRow(member.membershipId);
  };

  const closeDetail = () => {
    setDetail(null);
    if (sourceRow) document.getElementById(`member-${sourceRow}`)?.focus();
  };

  const copyLink = async (link: InviteLink) => {
    const result = await teamsApi.getInviteLinkShareUrl(scope.team.id, link.id, { idempotencyKey: createIdempotencyKey() });
    try {
      await navigator.clipboard.writeText(result.shareUrl);
      showToast(t('common.copied'), 'success');
    } catch {
      showToast(result.shareUrl, 'info');
    }
  };

  const changeRole = async (member: TeamMember, role: Exclude<TeamRole, 'owner'>) => {
    const next = await teamsApi.changeMemberRole(scope.team.id, member.membershipId, { role, expectedAuthRevision: scope.team.authRevision });
    applyScope(next);
    setMembers((prev) => prev.map((item) => item.membershipId === member.membershipId ? { ...item, role } : item));
  };

  return (
    <section className="tw-card tw-members-page">
      <div className="tw-card-heading">
        <div>
          <h2>{t('teams.members.title')}</h2>
          <p>{t('teams.members.subtitle')}</p>
        </div>
        {canManage && (
          <button type="button" className="button primary" onClick={() => outlet?.openInvite?.()}>
            <Plus size={16} />{t('teams.invite.action')}
          </button>
        )}
      </div>

      <div className="tw-member-toolbar">
        <div className="tw-mini-tabs">
          <button type="button" aria-pressed={tab === 'joined'} onClick={() => { setTab('joined'); setQuery(''); }}>
            {t('teams.members.joined', { count: members.length })}
          </button>
          {canManage && (
            <button type="button" aria-pressed={tab === 'pending'} onClick={() => { setTab('pending'); setQuery(''); }}>
              {t('teams.members.pending', { count: pendingCount })}
            </button>
          )}
          {canManage && (
            <button type="button" aria-pressed={tab === 'links'} onClick={() => { setTab('links'); setQuery(''); }}>
              {t('teams.members.links')}
            </button>
          )}
        </div>
        {tab === 'joined' && (
          <label className="tw-search">
            <Search size={16} />
            <input aria-label={t('teams.members.search')} placeholder={t('teams.members.search')} value={query} onChange={(e) => setQuery(e.target.value)} />
            {query && <button type="button" aria-label={t('teams.members.clearSearch')} onClick={() => setQuery('')}><X size={14} /></button>}
          </label>
        )}
      </div>

      {tab === 'joined' && (
        <>
          <div className="tw-table-scroll">
            <table className="tw-table tw-member-table">
              <thead>
                <tr>
                  <th>{t('teams.members.person')}</th>
                  <th>{t('teams.members.role')}</th>
                  <th>{t('teams.members.sharing')}</th>
                  <th>{t('teams.members.joinedDate')}</th>
                  <th>{t('teams.members.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {visible.map((member) => {
                  const contribution = memberContributionState(member);
                  const mine = member.membershipId === scope.membership.id;
                  return (
                    <tr key={member.membershipId}>
                      <td>
                        <button type="button" id={`member-${member.membershipId}`} className="tw-person" onClick={() => void openDetail(member)} disabled={!member.canOpenDetail}>
                          <MemberAvatar name={member.displayName} url={member.avatarUrl} />
                          <span>
                            <strong>
                              {member.displayName}
                              {mine && <em>{t('teams.members.you')}</em>}
                            </strong>
                            <small>{member.handle ? `@${member.handle}` : ''}</small>
                          </span>
                        </button>
                      </td>
                      <td>
                        {isOwner && member.role !== 'owner' ? (
                          <select
                            className="tw-role-select"
                            aria-label={member.displayName}
                            value={member.role}
                            onChange={(e) => { void changeRole(member, e.target.value as Exclude<TeamRole, 'owner'>); }}
                          >
                            <option value="admin">{t('teams.role.admin')}</option>
                            <option value="member">{t('teams.role.member')}</option>
                          </select>
                        ) : (
                          <span className={`tw-role ${member.role}`}>
                            {member.role === 'owner' && <Crown size={12} />}
                            {t(`teams.role.${member.role}`)}
                          </span>
                        )}
                      </td>
                      <td>
                        <span className={`tw-status ${contribution === 'waiting' ? 'waiting' : ''}`}>
                          <i />{t(`teams.members.contributionState.${contribution}`)}
                        </span>
                      </td>
                      <td className="tw-muted">{member.joinedAt ? formatDotDate(member.joinedAt) : '—'}</td>
                      <td>
                        <div className="tw-row-actions">
                          {member.canOpenDetail && <button type="button" onClick={() => void openDetail(member)}>{t('teams.members.details')}</button>}
                          {canManage && member.role !== 'owner' && !mine && (isOwner || member.role === 'member') && (
                            <button type="button" className="tw-remove" aria-label={t('teams.members.remove')} onClick={() => setRemoveTarget(member)}>
                              <Trash2 size={14} />
                            </button>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          {visible.length === 0 && (
            <div className="tw-no-data">
              <Search size={28} />
              <h3>{t('teams.members.noMatch')}</h3>
              <button type="button" className="text-link" onClick={() => setQuery('')}>{t('teams.members.clearSearch')}</button>
            </div>
          )}
          <div className="tw-table-footer">
            <ShieldCheck size={14} />{t('teams.members.footer')}
          </div>
        </>
      )}

      {tab === 'pending' && canManage && (
        <div className="tw-invite-list">
          {invitations.length ? invitations.map((invitation) => (
            <div key={invitation.id}>
              <span className="tw-round-icon"><Mail size={18} /></span>
              <span>
                <strong>{invitation.recipientMasked}</strong>
                <small>{t(`teams.role.${invitation.invitedRole}`)} · {t(`teams.invite.delivery.${invitation.deliveryState}`)}</small>
              </span>
              <span className={`tw-status ${invitation.status === 'revoked' ? 'paused' : 'waiting'}`}>
                <i />{invitation.status === 'pending' ? t('teams.members.pendingStatus') : t(`teams.invite.linkState.${invitation.status === 'revoked' ? 'revoked' : 'expired'}`)}
              </span>
              {invitation.status === 'pending' && (
                <button
                  type="button"
                  className="tw-text-button"
                  onClick={() => void teamsApi.revokeInvitation(scope.team.id, invitation.id, { expectedInvitationVersion: invitation.version }, { idempotencyKey: createIdempotencyKey() }).then(() => setInvitations((prev) => prev.filter((item) => item.id !== invitation.id)))}
                >
                  {t('teams.invite.revoke')}
                </button>
              )}
            </div>
          )) : (
            <div className="tw-no-data"><Mail size={30} /><h3>{t('teams.members.noPending')}</h3></div>
          )}
        </div>
      )}

      {tab === 'links' && canManage && (
        <div className="tw-invite-list">
          {links.length ? links.map((link) => (
            <div key={link.id}>
              <span className="tw-round-icon"><Link2 size={18} /></span>
              <span>
                <strong>{t('teams.members.links')} #{link.id.slice(-4)}</strong>
                <small>{t('teams.invite.linkMeta', { used: link.usedCount, max: link.maxUses, expires: link.expiresAt })}</small>
              </span>
              <span className={`tw-status ${link.effectiveState === 'active' ? '' : 'paused'}`}>
                <i />{t(`teams.invite.linkState.${link.effectiveState}`)}
              </span>
              {link.effectiveState === 'active' && (
                <div className="tw-row-actions">
                  <button type="button" onClick={() => void copyLink(link)}>{t('common.copy')}</button>
                  <button
                    type="button"
                    onClick={() => void teamsApi.revokeInviteLink(scope.team.id, link.id, { expectedLinkVersion: link.version }, { idempotencyKey: createIdempotencyKey() }).then(() => setLinks((prev) => prev.map((item) => item.id === link.id ? { ...item, effectiveState: 'revoked' } : item)))}
                  >
                    {t('teams.invite.revoke')}
                  </button>
                </div>
              )}
            </div>
          )) : (
            <div className="tw-no-data">
              <Link2 size={30} />
              <h3>{t('teams.members.noLinks')}</h3>
              <p>{t('teams.members.noLinksHint')}</p>
              <button type="button" className="button secondary" onClick={() => outlet?.openInvite?.()}>{t('teams.members.createLink')}</button>
            </div>
          )}
        </div>
      )}

      {detail && (
        <dialog className="tw-dialog" open onCancel={closeDetail} onClick={(event) => { if (event.target === event.currentTarget) closeDetail(); }} aria-labelledby="member-drawer-title">
          <div className="tw-dialog-inner">
            <button type="button" className="icon-button tw-dialog-close" onClick={closeDetail} autoFocus aria-label={t('common.close')}><X size={20} /></button>
            <div className="tw-member-detail-id">
              <MemberAvatar name={detail.displayName} url={detail.avatarUrl} size="md" />
              <div>
                <span className={`tw-role ${detail.role || 'member'}`}>{t(`teams.role.${detail.role || 'member'}`)}</span>
                <h2 id="member-drawer-title">{detail.displayName}</h2>
                <p>@{detail.handle || t('common.private')}</p>
              </div>
            </div>
            <p className="tw-detail-period">{t('teams.members.joinedAt', { date: detail.joinedAt })}</p>
            <div className="tw-detail-metrics">
              <div>
                <span>Token</span>
                <strong>{metricDisplay(detail.tokens, formatTokenCompact).available ? metricDisplay(detail.tokens, formatTokenCompact).text : '—'}</strong>
              </div>
              {(detail.costs.reported || []).map((item) => (
                <div key={item.currency}>
                  <span>{t('teams.metrics.recordedCost')}</span>
                  <strong>{formatDecimalAmount(item.amount, item.currency)}</strong>
                </div>
              ))}
            </div>
          </div>
        </dialog>
      )}

      {removeTarget && (
        <dialog className="tw-dialog" open onCancel={() => setRemoveTarget(null)} onClick={(event) => { if (event.target === event.currentTarget) setRemoveTarget(null); }}>
          <div className="tw-dialog-inner">
            <h2>{t('teams.members.removeTitle')}</h2>
            <p className="tw-dialog-lead">{t('teams.members.removeDesc', { name: removeTarget.displayName })}</p>
            <div className="tw-form-actions">
              <button type="button" className="button secondary" onClick={() => setRemoveTarget(null)}>{t('common.cancel')}</button>
              <button
                type="button"
                className="button tw-danger"
                onClick={async () => {
                  await teamsApi.removeMember(scope.team.id, removeTarget.membershipId, { expectedAuthRevision: scope.team.authRevision }, { idempotencyKey: createIdempotencyKey() });
                  setMembers((prev) => prev.filter((item) => item.membershipId !== removeTarget.membershipId));
                  setRemoveTarget(null);
                  await refresh();
                }}
              >
                {t('common.confirm')}
              </button>
            </div>
          </div>
        </dialog>
      )}

    </section>
  );
};
