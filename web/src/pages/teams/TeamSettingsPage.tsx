import React, { useEffect, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { Check, Crown, Globe2, Layers3, LockKeyhole, Settings2, Trash2, Upload } from 'lucide-react';
import { ApiError } from '@/api/client';
import { teamsApi, type TeamMember } from '@/api/teams';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { useTeam } from '@/context/TeamContext';
import {
  TEAM_DESCRIPTION_MAX,
  TEAM_AVATAR_MAX_BYTES,
  TEAM_AVATAR_TYPES,
  TEAM_NAME_MAX,
  TEAM_NAME_MIN,
  createIdempotencyKey,
  formatUtcOffset,
  graphemeLength,
} from './teamUtils';
import { persistTeamAvatar, teamErrorMessage } from './TeamShared';
import { TeamSharingCard } from './TeamSharingCard';
import { teamAvatarUrl } from '@/utils/avatar';

export const TeamSettingsPage: React.FC = () => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const { scope, applyScope, clearScope } = useTeam();
  const navigate = useNavigate();
  const fileRef = useRef<HTMLInputElement>(null);

  const [name, setName] = useState(scope?.team.name || '');
  const [description, setDescription] = useState(scope?.team.description || '');
  const [members, setMembers] = useState<TeamMember[]>([]);
  const [error, setError] = useState<ApiError | null>(null);
  const [busy, setBusy] = useState(false);
  const [avatarFile, setAvatarFile] = useState<File | null>(null);
  const [avatarPreview, setAvatarPreview] = useState('');
  const [leaveOpen, setLeaveOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [dissolveOpen, setDissolveOpen] = useState(false);
  const [targetMembershipId, setTargetMembershipId] = useState('');
  const [confirmName, setConfirmName] = useState('');

  useEffect(() => {
    if (!avatarFile) {
      setAvatarPreview('');
      return;
    }
    const url = URL.createObjectURL(avatarFile);
    setAvatarPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [avatarFile]);

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

  const canEdit = scope.permissions.editProfile;
  const avatarSrc = avatarPreview || teamAvatarUrl(scope.team);

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
    if (!TEAM_AVATAR_TYPES.includes(file.type) || file.size <= 0 || file.size > TEAM_AVATAR_MAX_BYTES) {
      showToast(t('teams.create.avatarInvalid'), 'error');
      return;
    }
    setAvatarFile(file);
    setBusy(true);
    try {
      const next = await persistTeamAvatar(scope, file);
      applyScope(next);
    } catch (err) {
      showToast(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'), 'error');
    } finally {
      setAvatarFile(null);
      setBusy(false);
    }
  };

  const confirmDialog = (open: boolean, onClose: () => void, title: string, lead: string, danger: boolean, onConfirm: () => Promise<void>, extra?: React.ReactNode) => (
    open ? (
      <dialog className="tw-dialog" open onCancel={onClose} onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
        <div className="tw-dialog-inner">
          <h2>{title}</h2>
          <p className="tw-dialog-lead">{lead}</p>
          <form className="tw-form" onSubmit={(event) => { event.preventDefault(); void onConfirm(); }}>
            {extra}
            <div className="tw-form-actions">
              <button type="button" className="button secondary" onClick={onClose}>{t('common.cancel')}</button>
              <button type="submit" className={`button ${danger ? 'tw-danger' : 'primary'}`}>{t('common.confirm')}</button>
            </div>
          </form>
        </div>
      </dialog>
    ) : null
  );

  return (
    <div className="tw-settings-grid">
      <section className="tw-card">
        <div className="tw-card-heading">
          <div>
            <h2><Settings2 size={19} />{t('teams.settings.profile')}</h2>
            <p>{t('teams.settings.profileHint')}</p>
          </div>
        </div>
        <form className="tw-form" onSubmit={(event) => { event.preventDefault(); void saveProfile(); }}>
          <div className="tw-avatar-upload">
            <div className="tw-team-avatar">{avatarSrc ? <img src={avatarSrc} alt="" /> : <Layers3 size={30} />}</div>
            <div>
              <label className={`button secondary ${!canEdit || busy ? 'disabled' : ''}`}>
                <Upload size={14} />{t('teams.settings.changeAvatar')}
                <input
                  ref={fileRef}
                  type="file"
                  disabled={!canEdit || busy}
                  accept="image/png,image/jpeg,image/webp"
                  aria-label={t('teams.settings.changeAvatar')}
                  onChange={(e) => { const file = e.target.files?.[0]; if (file) void uploadAvatar(file); e.target.value = ''; }}
                />
              </label>
              <small>PNG / JPG / WebP · ≤ 2 MB</small>
            </div>
          </div>
          <label>
            {t('teams.create.name')}
            <input value={name} onChange={(e) => setName(e.target.value)} minLength={TEAM_NAME_MIN} maxLength={TEAM_NAME_MAX} required disabled={!canEdit || busy} />
          </label>
          <label>
            {t('teams.settings.intro')}
            <textarea value={description} onChange={(e) => setDescription(e.target.value)} maxLength={TEAM_DESCRIPTION_MAX} rows={3} disabled={!canEdit || busy} />
            <small>{graphemeLength(description)} / {TEAM_DESCRIPTION_MAX}</small>
          </label>
          <div className="tw-profile-meta">
            <span><LockKeyhole size={14} />{t('teams.settings.privateTeam')}</span>
            <span><Globe2 size={14} />{scope.team.timezone} · {formatUtcOffset(scope.team.timezone)}</span>
          </div>
          {canEdit && (
            <div className="tw-form-actions">
              <button type="button" className="button secondary" disabled={busy} onClick={() => { setName(scope.team.name); setDescription(scope.team.description); }}>{t('teams.settings.discard')}</button>
              <button type="submit" className="button primary" disabled={busy}><Check size={15} />{t('teams.settings.saveProfile')}</button>
            </div>
          )}
        </form>
      </section>

      <TeamSharingCard />

      {(scope.permissions.leave || scope.permissions.transferOwnership || scope.permissions.dissolve) && (
        <section className="tw-card tw-management">
          <div>
            <h2>{t('teams.settings.management')}</h2>
            <p>{t('teams.settings.managementHint')}</p>
          </div>
          <div>
            {scope.permissions.transferOwnership && (
              <button type="button" className="button secondary" onClick={() => setTransferOpen(true)}>
                <Crown size={15} />{t('teams.settings.transfer')}
              </button>
            )}
            {scope.permissions.dissolve && (
              <button type="button" className="button tw-danger" onClick={() => setDissolveOpen(true)}>
                <Trash2 size={15} />{t('teams.settings.dissolve')}
              </button>
            )}
            {scope.permissions.leave && (
              <button type="button" className="button tw-danger" onClick={() => setLeaveOpen(true)}>
                {t('teams.settings.leave')}
              </button>
            )}
          </div>
        </section>
      )}

      {confirmDialog(leaveOpen, () => setLeaveOpen(false), t('teams.settings.leave'), t('teams.settings.leaveDesc'), true, async () => {
        await teamsApi.leaveTeam(scope.team.id, { expectedAuthRevision: scope.team.authRevision }, { idempotencyKey: createIdempotencyKey() });
        clearScope();
        navigate('/teams', { replace: true });
      })}

      {confirmDialog(transferOpen, () => setTransferOpen(false), t('teams.settings.transfer'), t('teams.settings.transferDesc'), false, async () => {
        const next = await teamsApi.transferOwnership(scope.team.id, {
          targetMembershipId,
          confirmTeamName: confirmName,
          expectedAuthRevision: scope.team.authRevision,
        }, { idempotencyKey: createIdempotencyKey() });
        applyScope(next);
        setTransferOpen(false);
        showToast(t('teams.settings.transferred'), 'success');
      }, (
        <>
          <label>
            {t('teams.settings.chooseMember')}
            <select value={targetMembershipId} onChange={(e) => setTargetMembershipId(e.target.value)} aria-label={t('teams.settings.chooseMember')}>
              <option value="">{t('teams.settings.chooseMember')}</option>
              {members.filter((item) => item.role !== 'owner').map((item) => (
                <option key={item.membershipId} value={item.membershipId}>{item.displayName}</option>
              ))}
            </select>
          </label>
          <label>
            {t('teams.settings.confirmName')}
            <input value={confirmName} placeholder={scope.team.name} onChange={(e) => setConfirmName(e.target.value)} required />
          </label>
        </>
      ))}

      {confirmDialog(dissolveOpen, () => setDissolveOpen(false), t('teams.settings.dissolve'), t('teams.settings.dissolveDesc'), true, async () => {
        await teamsApi.dissolveTeam(scope.team.id, { confirmTeamName: confirmName, expectedAuthRevision: scope.team.authRevision }, { idempotencyKey: createIdempotencyKey() });
        clearScope();
        navigate('/teams', { replace: true });
      }, (
        <label>
          {t('teams.settings.confirmName')}
          <input value={confirmName} placeholder={scope.team.name} onChange={(e) => setConfirmName(e.target.value)} required />
        </label>
      ))}
    </div>
  );
};
