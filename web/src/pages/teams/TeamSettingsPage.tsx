import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { teamsApi, type TeamMember } from '@/api/teams';
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
} from './teamUtils';
import { persistTeamAvatar, TeamAvatar, teamErrorMessage } from './TeamShared';
import { TeamSharingCard } from './TeamSharingCard';

export const TeamSettingsPage: React.FC = () => {
  const { t, locale } = useLocale();
  const { showToast } = useNotification();
  const { scope, applyScope, clearScope } = useTeam();
  const navigate = useNavigate();
  const fileRef = useRef<HTMLInputElement>(null);

  const [name, setName] = useState(scope?.team.name || '');
  const [description, setDescription] = useState(scope?.team.description || '');
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
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
    if (scope.permissions.transferOwnership) {
      teamsApi.getMembers(scope.team.id, {}, controller.signal)
        .then((memberRes) => setMembers(memberRes.members || []))
        .catch((err) => { if (!controller.signal.aborted) setError(err instanceof ApiError ? err : new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' })); });
    }
    return () => controller.abort();
  }, [scope?.team.id, scope?.team.name, scope?.team.description, scope?.permissions.transferOwnership]);

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

  const uploadAvatar = async (file: File) => {
    if (busy) return;
    setBusy(true);
    try {
      const next = await persistTeamAvatar(scope, file);
      applyScope(next);
    } catch (err) {
      showToast(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'), 'error');
    } finally { setBusy(false); }
  };

  return (
    <div className="sky-team-settings">
      <Card className="sky-team-profile">
        <div className="panel-header sky-settings-heading"><div><h2>{t('teams.settings.profile')}</h2><p>{locale === 'zh-CN' ? '给共同的创造，一个熟悉的名字。' : 'Give your shared work a familiar name.'}</p></div></div>
        <div style={{ display: 'flex', gap: 16, alignItems: 'center', marginBottom: 16 }}>
          <TeamAvatar team={scope.team} size="lg" />
          {scope.permissions.editProfile && (
            <>
              <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp" hidden onChange={(e) => { const file = e.target.files?.[0]; if (file) void uploadAvatar(file); e.target.value = ''; }} />
              <Button variant="outline" disabled={busy} onClick={() => fileRef.current?.click()}>{t('teams.settings.changeAvatar')}</Button>
            </>
          )}
        </div>
        <Input label={t('teams.create.name')} value={name} onChange={(e) => setName(e.target.value)} disabled={!scope.permissions.editProfile || busy} />
        <div className="form-group">
          <label className="form-label">{t('teams.create.description')}</label>
          <textarea className="form-input" value={description} disabled={!scope.permissions.editProfile || busy} onChange={(e) => setDescription(e.target.value)} style={{ minHeight: 80, padding: '10px 14px' }} />
        </div>
        {scope.permissions.editProfile && (
          <div className="sky-settings-actions"><Button variant="outline" disabled={busy} onClick={() => { setName(scope.team.name); setDescription(scope.team.description); }}>{t('common.cancel')}</Button><Button variant="dark" loading={busy} onClick={() => void saveProfile()}>{t('common.save')}</Button></div>
        )}
      </Card>

      <TeamSharingCard />

      {(scope.permissions.leave || scope.permissions.transferOwnership || scope.permissions.dissolve) && (
        <Card className="sky-team-management">
          <div className="panel-header sky-settings-heading"><div><h2>{t('teams.settings.management')}</h2><p>{locale === 'zh-CN' ? '成员关系发生变化时，个人记录仍然保留。' : 'Personal records remain when membership changes.'}</p></div></div>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
            {scope.permissions.leave && <Button variant="outline" onClick={() => setLeaveOpen(true)}>{t('teams.settings.leave')}</Button>}
            {scope.permissions.transferOwnership && <Button variant="outline" onClick={() => setTransferOpen(true)}>{t('teams.settings.transfer')}</Button>}
            {scope.permissions.dissolve && <Button variant="danger" onClick={() => setDissolveOpen(true)}>{t('teams.settings.dissolve')}</Button>}
          </div>
        </Card>
      )}

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
