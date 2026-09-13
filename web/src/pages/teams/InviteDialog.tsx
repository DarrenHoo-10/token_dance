import React, { useState } from 'react';
import { ApiError } from '@/api/client';
import { teamsApi, type InviteLink, type TeamPermissions, type TeamRole } from '@/api/teams';
import { Button } from '@/components/common/Button';
import { Input } from '@/components/common/Input';
import { Modal } from '@/components/common/Modal';
import { Select } from '@/components/common/Select';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { createIdempotencyKey } from './teamUtils';
import { teamErrorMessage } from './TeamShared';

interface InviteDialogProps {
  isOpen: boolean;
  onClose: () => void;
  teamId: string;
  permissions: TeamPermissions;
  onChanged?: () => void;
}

export const InviteDialog: React.FC<InviteDialogProps> = ({ isOpen, onClose, teamId, permissions, onChanged }) => {
  const { t } = useLocale();
  const { showToast } = useNotification();
  const [tab, setTab] = useState<'link' | 'email'>('link');
  const [expiresInDays, setExpiresInDays] = useState(7);
  const [maxUses, setMaxUses] = useState(50);
  const [generated, setGenerated] = useState<InviteLink | null>(null);
  const [copied, setCopied] = useState(false);
  const [linkBusy, setLinkBusy] = useState(false);
  const [email, setEmail] = useState('');
  const [role, setRole] = useState<Exclude<TeamRole, 'owner'>>('member');
  const [emailBusy, setEmailBusy] = useState(false);
  const [emailResult, setEmailResult] = useState<{ created: boolean; deliveryState: string } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [linkKey] = useState(() => createIdempotencyKey());
  const [emailKey, setEmailKey] = useState(() => createIdempotencyKey());

  const resetOnClose = () => {
    setTab('link');
    setGenerated(null);
    setCopied(false);
    setEmail('');
    setEmailResult(null);
    setError(null);
    onClose();
  };

  const generate = async () => {
    try {
      setLinkBusy(true);
      setError(null);
      setCopied(false);
      const link = await teamsApi.createInviteLink(teamId, { expiresInDays, maxUses }, { idempotencyKey: linkKey });
      setGenerated(link);
      onChanged?.();
    } catch (err) {
      setError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'));
    } finally {
      setLinkBusy(false);
    }
  };

  const copy = async () => {
    if (!generated?.shareUrl) return;
    try {
      await navigator.clipboard.writeText(generated.shareUrl);
      setCopied(true);
      showToast(t('common.copied'), 'success');
    } catch {
      setCopied(false);
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
      setEmailKey(createIdempotencyKey());
      onChanged?.();
    } catch (err) {
      setError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'));
    } finally {
      setEmailBusy(false);
    }
  };

  return (
    <Modal isOpen={isOpen} onClose={resetOnClose} title={t('teams.invite.title')}>
      <div className="team-invite-tabs" role="tablist">
        <Button variant={tab === 'link' ? 'dark' : 'ghost'} onClick={() => setTab('link')} role="tab" aria-selected={tab === 'link'}>
          {t('teams.invite.linkTab')}
        </Button>
        <Button variant={tab === 'email' ? 'dark' : 'ghost'} onClick={() => setTab('email')} role="tab" aria-selected={tab === 'email'}>
          {t('teams.invite.emailTab')}
        </Button>
      </div>

      {tab === 'link' && (
        <div>
          <p className="text-muted" style={{ fontSize: 13 }}>{t('teams.invite.linkHint')}</p>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
            <Select
              label={t('teams.invite.expiresIn')}
              value={String(expiresInDays)}
              onChange={(e) => setExpiresInDays(Number(e.target.value))}
              options={[
                { value: '1', label: t('teams.invite.day1') },
                { value: '7', label: t('teams.invite.day7') },
                { value: '30', label: t('teams.invite.day30') },
              ]}
            />
            <Input
              label={t('teams.invite.maxUses')}
              type="number"
              min={1}
              max={100}
              value={String(maxUses)}
              onChange={(e) => setMaxUses(Math.min(100, Math.max(1, Number(e.target.value) || 1)))}
            />
          </div>
          {!generated && (
            <Button variant="primary" loading={linkBusy} onClick={generate} style={{ marginTop: 8 }}>
              {t('teams.invite.generate')}
            </Button>
          )}
          {generated && (
            <div style={{ marginTop: 16, display: 'grid', gap: 10 }}>
              <label className="form-label">{t('teams.invite.shareUrl')}</label>
              <textarea className="form-input team-share-url" readOnly value={generated.shareUrl || ''} rows={3} />
              <p className="text-muted" style={{ fontSize: 12 }}>
                {t('teams.invite.linkMeta', { used: generated.usedCount, max: generated.maxUses, expires: generated.expiresAt })}
              </p>
              <Button variant="dark" onClick={copy}>{copied ? t('common.copied') : t('common.copy')}</Button>
            </div>
          )}
        </div>
      )}

      {tab === 'email' && (
        <div>
          <Input
            label={t('auth.email')}
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder={t('auth.emailPlaceholder')}
          />
          {permissions.assignAdmins && (
            <Select
              label={t('teams.invite.role')}
              value={role}
              onChange={(e) => setRole(e.target.value as Exclude<TeamRole, 'owner'>)}
              options={[
                { value: 'member', label: t('teams.role.member') },
                { value: 'admin', label: t('teams.role.admin') },
              ]}
            />
          )}
          <Button variant="primary" loading={emailBusy} onClick={sendEmail} disabled={!email.trim()}>
            {t('teams.invite.sendEmail')}
          </Button>
          {emailResult && (
            <p role="status" style={{ fontSize: 13, marginTop: 12 }}>
              {emailResult.deliveryState === 'failed' ? t('teams.invite.createdButFailed') : t('teams.invite.createdPending')}
            </p>
          )}
        </div>
      )}

      {error && <p className="form-error" role="alert">{error}</p>}
    </Modal>
  );
};
