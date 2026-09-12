import React, { useEffect, useState } from 'react';
import { ApiError } from '@/api/client';
import {
  teamsApi,
  type InviteLink,
  type ManagedInvitation,
  type MemberDetail,
  type TeamMember,
  type TeamRole,
} from '@/api/teams';
import { Badge } from '@/components/common/Badge';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { Input } from '@/components/common/Input';
import { Modal } from '@/components/common/Modal';
import { Select } from '@/components/common/Select';
import { ErrorState } from '@/components/states/ErrorState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import { createIdempotencyKey, formatDecimalAmount, formatTokenCompact, metricDisplay } from './teamUtils';
import { AnalysisSkeleton, RoleBadge, TeamAvatar, TeamDateRangeBar, teamErrorMessage, useTeamSearchFilters } from './TeamShared';
import { useTeamAnalysis } from './useTeamAnalysis';

type MemberTab = 'joined' | 'pending' | 'links';

export const TeamMembersPage: React.FC = () => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const { scope, authRevision, refresh, applyScope } = useTeam();
  const { range, from, to, agent, provider, model } = useTeamSearchFilters();
  const { analysis, updating, waitingForDates, error: analysisError } = useTeamAnalysis({
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
  const [tab, setTab] = useState<MemberTab>('joined');
  const [query, setQuery] = useState('');
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [invitations, setInvitations] = useState<ManagedInvitation[]>([]);
  const [links, setLinks] = useState<InviteLink[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [detail, setDetail] = useState<MemberDetail | null>(null);
  const [sourceRow, setSourceRow] = useState<string | null>(null);
  const [removeTarget, setRemoveTarget] = useState<TeamMember | null>(null);
  const [roleTarget, setRoleTarget] = useState<TeamMember | null>(null);
  const [nextRole, setNextRole] = useState<Exclude<TeamRole, 'owner'>>('member');

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
  if (waitingForDates) return <div><TeamDateRangeBar timezone={scope.team.timezone} /><p role="status">{t('teams.range.chooseDates')}</p></div>;
  if (analysisError) return <div><TeamDateRangeBar timezone={scope.team.timezone} /><ErrorState error={analysisError} description={teamErrorMessage(t, analysisError)} /></div>;
  if (updating && !analysis) return <div><TeamDateRangeBar timezone={scope.team.timezone} /><AnalysisSkeleton /></div>;
  if (error) return <ErrorState error={error} description={teamErrorMessage(t, error)} />;

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

  return (
    <div>
      <TeamDateRangeBar timezone={scope.team.timezone} />
      {analysis?.quality.hasLegacyAggregates && <p className="team-status-banner">{t('teams.quality.legacySummary')}</p>}
      <div className="segmented-control" role="tablist" style={{ marginBottom: 16 }}>
        <button type="button" className={`segmented-item ${tab === 'joined' ? 'active' : ''}`} onClick={() => setTab('joined')}>
          {t('teams.members.joined', { count: members.length })}
        </button>
        {canManage && (
          <button type="button" className={`segmented-item ${tab === 'pending' ? 'active' : ''}`} onClick={() => setTab('pending')}>
            {t('teams.members.pending', { count: invitations.filter((item) => item.status === 'pending').length })}
          </button>
        )}
        {canManage && (
          <button type="button" className={`segmented-item ${tab === 'links' ? 'active' : ''}`} onClick={() => setTab('links')}>
            {t('teams.members.links')}
          </button>
        )}
      </div>

      {tab === 'joined' && (
        <>
          <Input label={t('teams.members.search')} value={query} onChange={(e) => setQuery(e.target.value)} />
          <Card>
            <table className="team-member-table">
              <thead>
                <tr>
                  <th>{t('teams.members.person')}</th>
                  <th>{t('teams.members.role')}</th>
                  <th>{t('teams.members.sharing')}</th>
                  <th>{t('teams.members.sync')}</th>
                  <th>{t('teams.members.periodTokens')}</th>
                  <th>{t('common.actions')}</th>
                </tr>
              </thead>
              <tbody>
                {members.map((member) => (
                  <MemberRow
                    key={member.membershipId}
                    member={member}
                    canManage={canManage}
                    assignAdmins={scope.permissions.assignAdmins}
                    onOpen={() => void openDetail(member)}
                    onRemove={() => setRemoveTarget(member)}
                    onRole={() => { setRoleTarget(member); setNextRole(member.role === 'admin' ? 'member' : 'admin'); }}
                  />
                ))}
              </tbody>
            </table>
            <div className="team-compact-cards">
              {members.map((member) => (
                <Card key={`${member.membershipId}-card`}>
                  <strong>{member.displayName}</strong>
                  <div style={{ margin: '8px 0' }}><RoleBadge role={member.role} /></div>
                  <p className="text-muted" style={{ fontSize: 12 }}>{t(`teams.members.share.${member.sharing.base ? 'on' : 'off'}`)}</p>
                  {member.canOpenDetail && <Button size="sm" onClick={() => void openDetail(member)}>{t('teams.members.detail')}</Button>}
                </Card>
              ))}
            </div>
          </Card>
        </>
      )}

      {tab === 'pending' && canManage && (
        <Card>
          {invitations.map((invitation) => (
            <div key={invitation.id} className="team-member-row">
              <div>
                <strong>{invitation.recipientMasked}</strong>
                <small>{t(`teams.invite.delivery.${invitation.deliveryState}`)}</small>
              </div>
              <RoleBadge role={invitation.invitedRole} />
              <div>{invitation.expiresAt}</div>
              <div style={{ display: 'flex', gap: 8 }}>
                <Button size="sm" onClick={() => void teamsApi.resendInvitation(scope.team.id, invitation.id, { expectedInvitationVersion: invitation.version }, { idempotencyKey: createIdempotencyKey() }).then(() => showToast(t('teams.invite.resent'), 'success'))}>
                  {t('teams.invite.resend')}
                </Button>
                <Button size="sm" variant="danger" onClick={() => void teamsApi.revokeInvitation(scope.team.id, invitation.id, { expectedInvitationVersion: invitation.version }, { idempotencyKey: createIdempotencyKey() }).then(() => setInvitations((prev) => prev.filter((item) => item.id !== invitation.id)))}>
                  {t('teams.invite.revoke')}
                </Button>
              </div>
            </div>
          ))}
        </Card>
      )}

      {tab === 'links' && canManage && (
        <Card>
          {links.map((link) => (
            <div key={link.id} className="team-member-row">
              <div>
                <strong>{link.creatorDisplayName || t('teams.role.admin')}</strong>
                <small>{t(`teams.invite.linkState.${link.effectiveState}`)}</small>
              </div>
              <div>{t('teams.invite.linkMeta', { used: link.usedCount, max: link.maxUses, expires: link.expiresAt })}</div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {link.effectiveState === 'active' && <Button size="sm" onClick={() => void copyLink(link)}>{t('common.copy')}</Button>}
                {link.effectiveState === 'active' && (
                  <Button
                    size="sm"
                    variant="danger"
                    onClick={() => void teamsApi.revokeInviteLink(scope.team.id, link.id, { expectedLinkVersion: link.version }, { idempotencyKey: createIdempotencyKey() }).then(() => setLinks((prev) => prev.map((item) => item.id === link.id ? { ...item, effectiveState: 'revoked' } : item)))}
                  >
                    {t('teams.invite.revoke')}
                  </Button>
                )}
                <Button
                  size="sm"
                  onClick={() => void teamsApi.regenerateInviteLink(scope.team.id, link.id, { expectedLinkVersion: link.version, expiresInDays: 7, maxUses: 50 }, { idempotencyKey: createIdempotencyKey() }).then((next) => setLinks((prev) => [next, ...prev.filter((item) => item.id !== link.id)]))}
                >
                  {t('teams.invite.regenerate')}
                </Button>
              </div>
            </div>
          ))}
        </Card>
      )}

      {detail && (
        <div className="team-drawer-overlay" onClick={closeDetail}>
          <aside className="team-drawer" role="dialog" aria-modal="true" aria-labelledby="member-drawer-title" onClick={(e) => e.stopPropagation()}>
            <button type="button" className="btn btn-ghost" onClick={closeDetail} autoFocus>{t('common.close')}</button>
            <h2 id="member-drawer-title">{detail.displayName}</h2>
            <p className="text-muted">@{detail.handle || t('common.private')}</p>
            <p>{t('teams.members.joinedAt', { date: detail.joinedAt })}</p>
            <p>{metricDisplay(detail.tokens, formatTokenCompact).available ? metricDisplay(detail.tokens, formatTokenCompact).text : t('teams.members.notSharedDimension')}</p>
            {detail.costs.reported.map((item) => (
              <p key={item.currency}>{formatDecimalAmount(item.amount, item.currency)}</p>
            ))}
          </aside>
        </div>
      )}

      <Modal isOpen={Boolean(removeTarget)} onClose={() => setRemoveTarget(null)} title={t('teams.members.removeTitle')} footer={
        <>
          <Button variant="outline" onClick={() => setRemoveTarget(null)}>{t('common.cancel')}</Button>
          <Button
            variant="danger"
            onClick={async () => {
              if (!removeTarget) return;
              await teamsApi.removeMember(scope.team.id, removeTarget.membershipId, { expectedAuthRevision: scope.team.authRevision }, { idempotencyKey: createIdempotencyKey() });
              setRemoveTarget(null);
              setMembers((prev) => prev.filter((item) => item.membershipId !== removeTarget.membershipId));
              await refresh();
            }}
          >
            {t('teams.members.remove')}
          </Button>
        </>
      }>
        <p>{t('teams.members.removeDesc', { name: removeTarget?.displayName || '' })}</p>
      </Modal>

      <Modal isOpen={Boolean(roleTarget)} onClose={() => setRoleTarget(null)} title={t('teams.members.roleTitle')} footer={
        <>
          <Button variant="outline" onClick={() => setRoleTarget(null)}>{t('common.cancel')}</Button>
          <Button
            variant="dark"
            onClick={async () => {
              if (!roleTarget) return;
              const next = await teamsApi.changeMemberRole(scope.team.id, roleTarget.membershipId, { role: nextRole, expectedAuthRevision: scope.team.authRevision });
              applyScope(next);
              setRoleTarget(null);
            }}
          >
            {t('common.confirm')}
          </Button>
        </>
      }>
        <Select
          label={t('teams.invite.role')}
          value={nextRole}
          onChange={(e) => setNextRole(e.target.value as Exclude<TeamRole, 'owner'>)}
          options={[
            { value: 'member', label: t('teams.role.member') },
            { value: 'admin', label: t('teams.role.admin') },
          ]}
        />
      </Modal>
    </div>
  );
};

const MemberRow: React.FC<{
  member: TeamMember;
  canManage: boolean;
  assignAdmins: boolean;
  onOpen: () => void;
  onRemove: () => void;
  onRole: () => void;
}> = ({ member, canManage, assignAdmins, onOpen, onRemove, onRole }) => {
  const { t } = useLocale();
  const tokens = metricDisplay(member.periodTokens, formatTokenCompact);
  return (
    <tr>
      <td>
        <button type="button" id={`member-${member.membershipId}`} className="btn btn-ghost" onClick={onOpen} disabled={!member.canOpenDetail} style={{ justifyContent: 'flex-start' }}>
          <TeamAvatar team={{ id: member.membershipId, name: member.displayName, avatarUrl: null }} size="sm" />
          <span style={{ marginLeft: 8 }}>{member.displayName}</span>
        </button>
      </td>
      <td><RoleBadge role={member.role} /></td>
      <td><Badge variant={member.sharing.base ? 'good' : 'warning'}>{t(`teams.members.share.${member.sharing.base ? 'on' : 'off'}`)}</Badge></td>
      <td>{t(`teams.members.syncState.${member.syncStatus}`)}</td>
      <td className="mono-num">{member.canOpenDetail && tokens.available ? tokens.text : '—'}</td>
      <td>
        {canManage && member.role !== 'owner' && (
          <Button size="sm" onClick={member.role === 'admin' && !assignAdmins ? undefined : onRole} disabled={member.role === 'admin' && !assignAdmins}>
            {t('common.manage')}
          </Button>
        )}
        {canManage && member.role === 'member' && <Button size="sm" variant="ghost" onClick={onRemove}>{t('teams.members.remove')}</Button>}
        {assignAdmins && member.role === 'admin' && <Button size="sm" variant="ghost" onClick={onRemove}>{t('teams.members.remove')}</Button>}
      </td>
    </tr>
  );
};
