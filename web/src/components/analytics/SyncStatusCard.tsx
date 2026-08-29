import React from 'react';
import { useLocale } from '@/context/LocaleContext';

export interface SyncStatusCardProps {
  lastCommittedAt: string | null;
  pendingLocalCount: number | null;
  status?: 'healthy' | 'warning' | 'delayed';
}

function timeAgo(dateStr: string | null): string {
  if (!dateStr) return '—';
  const diffMs = Date.now() - new Date(dateStr).getTime();
  if (diffMs < 0 || isNaN(diffMs)) return '—';
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 1) return 'Just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return `${diffHours}h ago`;
  return `${Math.floor(diffHours / 24)}d ago`;
}

export const SyncStatusCard: React.FC<SyncStatusCardProps> = ({
  lastCommittedAt,
  pendingLocalCount,
  status = 'healthy',
}) => {
  const { t } = useLocale();

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingBottom: 8, borderBottom: '1px solid var(--border-light)', fontSize: 12 }}>
        <span>{t('dashboard.syncStatus')}</span>
        <span
          className={`badge ${
            status === 'healthy' ? 'badge-good' : status === 'warning' ? 'badge-warning' : 'badge-danger'
          }`}
        >
          {status === 'healthy' ? t('common.healthy') : status === 'warning' ? t('common.warning') : t('common.failed')}
        </span>
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingBottom: 8, borderBottom: '1px solid var(--border-light)', fontSize: 12 }}>
        <span className="text-muted">{t('settings.lastSeen')}</span>
        <strong className="mono-num">{timeAgo(lastCommittedAt)}</strong>
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', fontSize: 12 }}>
        <span className="text-muted">{t('dashboard.pendingEvents')}</span>
        <strong className="mono-num">
          {pendingLocalCount !== null && pendingLocalCount !== undefined
            ? pendingLocalCount.toLocaleString()
            : t('common.unknown')}
        </strong>
      </div>
    </div>
  );
};
