import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { EMPTY_SHARING, teamsApi, type AuditEvent, type MySharingResponse, type SharingFlags, type TeamMember } from '@/api/teams';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { Input } from '@/components/common/Input';
import { Modal } from '@/components/common/Modal';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import {
  TEAM_DESCRIPTION_MAX,
  TEAM_NAME_MAX,
  TEAM_NAME_MIN,
  createIdempotencyKey,
  graphemeLength,
  normalizeSharing,
  sha256Hex,
} from './teamUtils';
import { SharingControls, TeamAvatar, teamErrorMessage } from './TeamShared';

export const TeamSettingsPage: React.FC = () => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const { scope, applyScope, clearScope, refresh } = useTeam();
  const navigate = useNavigate();
  const fileRef = useRef<HTMLInputElement>(null);

  const [name, setName] = useState(scope?.team.name || '');
  const [description, setDescription] = useState(scope?.team.description || '');
  const [sharingServer, setSharingServer] = useState<MySharingResponse | null>(null);
  const [sharingDraft, setSharingDraft] = useState<SharingFlags>(EMPTY_SHARING);
  const [audits, setAudits] = useState<AuditEvent[]>([]);
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [confirmShare, setConfirmShare] = useState(false);
  const [leaveOpen, setLeaveOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [dissolveOpen, setDissolveOpen] = useState(false);
  const [targetMembershipId, setTargetMembershipId] = useState('');
  const [confirmName, setConfirmName] = useState('');

  useEffect(() => {
    if (!scope) return;
    setName(scope.team.name);
    setDescription(scope.team.description);
    const controller = new AbortController();
    Promise.all([
      teamsApi.getMySharing(scope.team.id, controller.signal),
      scope.permissions.inviteMembers ? teamsApi.getAuditEvents(scope.team.id, {}, controller.signal) : Promise.resolve({ events: [] as AuditEvent[], nextCursor: null }),
      scope.permissions.transferOwnership ? teamsApi.getMembers(scope.team.id, {}, controller.signal) : Promise.resolve({ members: [] as TeamMember[], nextCursor: null }),
    ])
      .then(([sharing, auditRes, memberRes]) => {
        setSharingServer(sharing);
        setSharingDraft(sharing.sharing);
        setAudits(auditRes.events || []);
        setMembers(memberRes.members || []);
      })
      .catch((err) => setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' })));
    return () => controller.abort();
  }, [scope]);

  if (!scope) return <LoadingState />;
  if (error) return <ErrorState error={error} description={teamErrorMessage(t, error)} />;

  const saveProfile = async () => {
    const trimmed = name.trim();
    if (graphemeLength(trimmed) < TEAM_NAME_MIN || graphemeLength(trimmed) > TEAM_NAME_MAX) return;
    if (graphemeLength(description.trim()) > TEAM_DESCRIPTION_MAX) return;
    setBusy(true);
    try {
      const next = await teamsApi.updateTeam(scope.team.id, {
        name: trimmed,
        description: description.trim(),
        expectedProfileVersion: scope.team.profileVersion,
      });
      applyScope(next);
      showToast(t('common.saved'), 'success');
    } catch (err) {
      showToast(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'), 'error');
    } finally {
      setBusy(false);
    }
  };

  const reducing = Boolean(
    sharingServer &&
      ((sharingServer.sharing.base && !sharingDraft.base) ||
        (sharingServer.sharing.classification && !sharingDraft.classification) ||
        (sharingServer.sharing.cost && !sharingDraft.cost))
  );

  const saveSharing = async () => {
    if (!sharingServer) return;
    if (reducing && !confirmShare) {
      setConfirmShare(true);
      return;
    }
    setBusy(true);
    try {
      const next = await teamsApi.updateMySharing(scope.team.id, {
        expectedSharingVersion: sharingServer.sharingVersion,
        sharing: normalizeSharing(sharingDraft),
      });
      setSharingServer(next);
      setSharingDraft(next.sharing);
      setConfirmShare(false);
      await refresh();
      showToast(t('common.saved'), 'success');
    } catch (err) {
      if (err instanceof ApiError && err.code === 'TEAM_VERSION_CONFLICT') {
        const latest = await teamsApi.getMySharing(scope.team.id);
        setSharingServer(latest);
        showToast(t('teams.settings.versionConflict'), 'error');
      } else {
        showToast(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'), 'error');
      }
    } finally {
      setBusy(false);
    }
  };

  const uploadAvatar = async (file: File) => {
    const bytes = await file.arrayBuffer();
    const hash = await sha256Hex(bytes);
    const intent = await teamsApi.createAvatarUploadIntent(scope.team.id, { contentType: file.type, byteSize: file.size, sha256: hash });
    await teamsApi.uploadAvatarContent(scope.team.id, intent.objectId, file);
    const next = await teamsApi.completeAvatarUpload(scope.team.id, intent.objectId, { expectedProfileVersion: scope.team.profileVersion });
    applyScope(next);
  };

  return (
    <div style={{ display: 'grid', gap: 20 }}>
      <Card>
        <div className="panel-header"><h2>{t('teams.settings.profile')}</h2></div>
        <div style={{ display: 'flex', gap: 16, alignItems: 'center', marginBottom: 16 }}>
          <TeamAvatar team={scope.team} size="lg" />
          {scope.permissions.editProfile && (
            <>
              <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp" hidden onChange={(e) => { const file = e.target.files?.[0]; if (file) void uploadAvatar(file); e.target.value = ''; }} />
              <Button variant="outline" onClick={() => fileRef.current?.click()}>{t('teams.settings.changeAvatar')}</Button>
            </>
          )}
        </div>
        <Input label={t('teams.create.name')} value={name} onChange={(e) => setName(e.target.value)} disabled={!scope.permissions.editProfile || busy} />
        <div className="form-group">
          <label className="form-label">{t('teams.create.description')}</label>
          <textarea className="form-input" value={description} disabled={!scope.permissions.editProfile || busy} onChange={(e) => setDescription(e.target.value)} style={{ minHeight: 80, padding: '10px 14px' }} />
        </div>
        <p className="text-muted" style={{ fontSize: 12 }}>{t('teams.settings.timezoneFixed', { timezone: scope.team.timezone })}</p>
        {scope.permissions.editProfile && (
          <Button variant="dark" loading={busy} onClick={() => void saveProfile()}>{t('common.save')}</Button>
        )}
      </Card>

      <Card>
        <div className="panel-header"><h2>{t('teams.settings.mySharing')}</h2></div>
        <SharingControls value={sharingDraft} onChange={setSharingDraft} disabled={busy} revealDetailsWithBase={false} />
        <p className="text-muted" style={{ fontSize: 12, marginTop: 12 }}>{t('teams.sharing.closeBaseHint')}</p>
        <Button variant="primary" loading={busy} onClick={() => void saveSharing()} style={{ marginTop: 16 }}>{t('teams.settings.saveSharing')}</Button>
      </Card>

      <Card>
        <div className="panel-header"><h2>{t('teams.settings.management')}</h2></div>
        {scope.permissions.leave && <Button variant="outline" onClick={() => setLeaveOpen(true)}>{t('teams.settings.leave')}</Button>}
        {scope.permissions.transferOwnership && <Button variant="outline" onClick={() => setTransferOpen(true)}>{t('teams.settings.transfer')}</Button>}
        {scope.permissions.dissolve && <Button variant="danger" onClick={() => setDissolveOpen(true)}>{t('teams.settings.dissolve')}</Button>}
        {!scope.permissions.leave && scope.membership.role === 'owner' && (
          <p className="text-muted" style={{ fontSize: 13 }}>{t('teams.settings.ownerLeaveHint')}</p>
        )}
      </Card>

      {scope.permissions.inviteMembers && (
        <Card>
          <div className="panel-header"><h2>{t('teams.settings.audit')}</h2></div>
          {audits.map((event) => (
            <div key={event.id} className="team-member-row">
              <div>{t(`teams.audit.${event.action}`) === `teams.audit.${event.action}` ? event.action : t(`teams.audit.${event.action}`)}</div>
              <div className="text-muted">{event.createdAt}</div>
            </div>
          ))}
        </Card>
      )}

      <Modal isOpen={confirmShare} onClose={() => setConfirmShare(false)} title={t('teams.settings.confirmShareTitle')} footer={
        <>
          <Button variant="outline" onClick={() => setConfirmShare(false)}>{t('common.cancel')}</Button>
          <Button variant="danger" onClick={() => { setConfirmShare(true); void saveSharing(); }}>{t('common.confirm')}</Button>
        </>
      }>
        <p>{sharingDraft.base ? t('teams.settings.confirmReduce') : t('teams.settings.confirmCloseBase')}</p>
      </Modal>

      <Modal isOpen={leaveOpen} onClose={() => setLeaveOpen(false)} title={t('teams.settings.leave')} footer={
        <>
          <Button variant="outline" onClick={() => setLeaveOpen(false)}>{t('common.cancel')}</Button>
          <Button variant="danger" onClick={async () => {
            await teamsApi.leaveTeam(scope.team.id, { expectedAuthRevision: scope.team.authRevision }, { idempotencyKey: createIdempotencyKey() });
            clearScope();
            navigate('/teams', { replace: true });
          }}>{t('common.confirm')}</Button>
        </>
      }>
        <p>{t('teams.settings.leaveDesc')}</p>
      </Modal>

      <Modal isOpen={transferOpen} onClose={() => setTransferOpen(false)} title={t('teams.settings.transfer')} footer={
        <>
          <Button variant="outline" onClick={() => setTransferOpen(false)}>{t('common.cancel')}</Button>
          <Button variant="dark" onClick={async () => {
            const next = await teamsApi.transferOwnership(scope.team.id, {
              targetMembershipId,
              confirmTeamName: confirmName,
              expectedAuthRevision: scope.team.authRevision,
            }, { idempotencyKey: createIdempotencyKey() });
            applyScope(next);
            setTransferOpen(false);
            showToast(t('teams.settings.transferred'), 'success');
          }}>{t('common.confirm')}</Button>
        </>
      }>
        <p>{t('teams.settings.transferDesc')}</p>
        <select className="form-input" value={targetMembershipId} onChange={(e) => setTargetMembershipId(e.target.value)}>
          <option value="">{t('teams.settings.chooseMember')}</option>
          {members.filter((item) => item.role !== 'owner').map((item) => (
            <option key={item.membershipId} value={item.membershipId}>{item.displayName}</option>
          ))}
        </select>
        <Input label={t('teams.settings.confirmName')} value={confirmName} onChange={(e) => setConfirmName(e.target.value)} />
      </Modal>

      <Modal isOpen={dissolveOpen} onClose={() => setDissolveOpen(false)} title={t('teams.settings.dissolve')} footer={
        <>
          <Button variant="outline" onClick={() => setDissolveOpen(false)}>{t('common.cancel')}</Button>
          <Button variant="danger" onClick={async () => {
            await teamsApi.dissolveTeam(scope.team.id, { confirmTeamName: confirmName, expectedAuthRevision: scope.team.authRevision }, { idempotencyKey: createIdempotencyKey() });
            clearScope();
            navigate('/teams', { replace: true });
          }}>{t('common.confirm')}</Button>
        </>
      }>
        <p>{t('teams.settings.dissolveDesc')}</p>
        <Input label={t('teams.settings.confirmName')} value={confirmName} onChange={(e) => setConfirmName(e.target.value)} />
      </Modal>
    </div>
  );
};
