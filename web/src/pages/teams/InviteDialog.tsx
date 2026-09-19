import React, { useEffect, useRef, useState } from 'react';
import { Check, Copy, Link2, Mail, Plus, UserRound, UsersRound, X } from 'lucide-react';
import { ApiError } from '@/api/client';
import { teamsApi, type InviteLink, type TeamPermissions, type TeamRole } from '@/api/teams';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { createIdempotencyKey } from './teamUtils';
import { teamErrorMessage } from './TeamShared';

interface InviteDialogProps {
  isOpen: boolean;
  onClose: () => void;
  teamId: string;
  teamName: string;
  permissions: TeamPermissions;
  onChanged?: () => void;
}

export const InviteDialog: React.FC<InviteDialogProps> = ({ isOpen, onClose, teamId, teamName, permissions, onChanged }) => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [tab, setTab] = useState<'link' | 'email'>('link');
  const [expiresInDays, setExpiresInDays] = useState(7);
  const [maxUses, setMaxUses] = useState(50);
  const [generated, setGenerated] = useState<InviteLink | null>(null);
  const [shareUrl, setShareUrl] = useState('');
  const [linkBusy, setLinkBusy] = useState(false);
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<Exclude<TeamRole, 'owner'>>('member');
  const [emailBusy, setEmailBusy] = useState(false);
  const [emailResult, setEmailResult] = useState<{ created: boolean; deliveryState: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [linkKey] = useState(() => createIdempotencyKey());
  const [emailKey, setEmailKey] = useState(() => createIdempotencyKey());

  useEffect(() => {
    if (!isOpen) return;
    const node = dialogRef.current;
    if (node && typeof node.showModal === 'function' && !node.open) {
      try { node.showModal(); } catch { /* jsdom */ }
    }
    document.body.style.overflow = 'hidden';
    return () => { document.body.style.overflow = ''; };
  }, [isOpen]);

  if (!isOpen) return null;

  const resetOnClose = () => {
    setTab('link');
    setGenerated(null);
    setShareUrl('');
    setEmail('');
    setEmailResult(null);
    setError(null);
    onClose();
  };

  const generate = async () => {
    try {
      setLinkBusy(true);
      setError(null);
      const link = await teamsApi.createInviteLink(teamId, { expiresInDays, maxUses }, { idempotencyKey: linkKey });
      setGenerated(link);
      if (link.shareUrl) {
        setShareUrl(link.shareUrl);
      } else {
        const result = await teamsApi.getInviteLinkShareUrl(teamId, link.id, { idempotencyKey: createIdempotencyKey() });
        setShareUrl(result.shareUrl);
      }
      onChanged?.();
    } catch (err) {
      setError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'));
    } finally {
      setLinkBusy(false);
    }
  };

  const copy = async () => {
    if (!shareUrl) return;
    try {
      await navigator.clipboard.writeText(shareUrl);
      showToast(t('common.copied'), 'success');
    } catch {
      showToast(shareUrl, 'info');
    }
  };

  const sendEmail = async () => {
    if (!email.trim()) return;
    try {
      setEmailBusy(true);
      setError(null);
      const result = await teamsApi.createInvitation(
        teamId,
        { email: email.trim(), role: permissions.assignAdmins ? role : 'member' },
        { idempotencyKey: emailKey }
      );
      setEmailResult({ created: true, deliveryState: result.deliveryState || result.invitation.deliveryState });
      setEmail('');
      setEmailKey(createIdempotencyKey());
      onChanged?.();
    } catch (err) {
      setError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'));
    } finally {
      setEmailBusy(false);
    }
  };

  return (
    <dialog
      ref={dialogRef}
      className="tw-dialog"
      onCancel={resetOnClose}
      onClick={(event) => { if (event.target === event.currentTarget) resetOnClose(); }}
      aria-labelledby="team-dialog-title"
    >
      <div className="tw-dialog-inner">
        <button type="button" className="icon-button tw-dialog-close" aria-label={t('common.close')} onClick={resetOnClose}>
          <X size={20} />
        </button>
        <span className="tw-dialog-icon"><UsersRound size={26} /></span>
        <p className="tw-dialog-eyebrow">GOOD IDEAS NEED GOOD COMPANY</p>
        <h2 id="team-dialog-title">{t('teams.invite.nextCreator')}</h2>
        <p className="tw-dialog-lead">{t('teams.invite.nextCreatorLead', { name: teamName })}</p>
        <div className="tw-mini-tabs tw-invite-tabs">
          <button type="button" aria-pressed={tab === 'link'} onClick={() => setTab('link')}>
            <Link2 size={15} />{t('teams.invite.linkTab')}
          </button>
          <button type="button" aria-pressed={tab === 'email'} onClick={() => setTab('email')}>
            <Mail size={15} />{t('teams.invite.emailTab')}
          </button>
        </div>

        {tab === 'link' ? (
          <form className="tw-form" onSubmit={(event) => { event.preventDefault(); void generate(); }}>
            <div className="tw-form-columns">
              <label>
                {t('teams.invite.expiresIn')}
                <select aria-label={t('teams.invite.expiresIn')} value={expiresInDays} onChange={(e) => setExpiresInDays(Number(e.target.value))}>
                  <option value={1}>{t('teams.invite.day1')}</option>
                  <option value={7}>{t('teams.invite.day7')}</option>
                  <option value={30}>{t('teams.invite.day30')}</option>
                </select>
              </label>
              <label>
                {t('teams.invite.maxUses')}
                <input type="number" value={maxUses} onChange={(e) => setMaxUses(Math.min(100, Math.max(1, Number(e.target.value) || 1)))} min={1} max={100} required />
              </label>
            </div>
            <p className="tw-form-hint"><UserRound size={14} />{t('teams.invite.linkHint')}</p>
            <button className="button primary full-width" type="submit" disabled={linkBusy || Boolean(generated)}>
              <Plus size={16} />{t('teams.invite.generate')}
            </button>
            {generated && (
              <div className="tw-generated-link">
                <span><Check size={14} />{t('teams.invite.ready')}</span>
                <div>
                  <input readOnly value={shareUrl} aria-label={t('teams.invite.shareUrl')} onFocus={(e) => e.target.select()} />
                  <button className="icon-button" aria-label={t('common.copy')} type="button" onClick={() => void copy()}>
                    <Copy size={17} />
                  </button>
                </div>
                <small>{t('teams.invite.linkMeta', { used: generated.usedCount, max: generated.maxUses, expires: generated.expiresAt })}</small>
              </div>
            )}
          </form>
        ) : (
          <form className="tw-form" onSubmit={(event) => { event.preventDefault(); void sendEmail(); }}>
            <label>
              {t('teams.invite.teammateEmail')}
              <input type="email" required placeholder="teammate@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
            </label>
            <label>
              {t('teams.invite.role')}
              <select aria-label={t('teams.invite.role')} value={role} onChange={(e) => setRole(e.target.value as Exclude<TeamRole, 'owner'>)}>
                <option value="member">{t('teams.role.member')}</option>
                {permissions.assignAdmins && <option value="admin">{t('teams.role.admin')}</option>}
              </select>
            </label>
            <button className="button primary full-width" type="submit" disabled={emailBusy || !email.trim()}>
              <Mail size={16} />{t('teams.invite.sendEmail')}
            </button>
            {emailResult && (
              <p className="tw-form-hint" role="status">
                {emailResult.deliveryState === 'failed' ? t('teams.invite.createdButFailed') : t('teams.invite.createdPending')}
              </p>
            )}
          </form>
        )}
        {error && <p role="alert" className="tw-form-hint">{error}</p>}
      </div>
    </dialog>
  );
};
