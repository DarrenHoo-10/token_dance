import React from 'react';
import { useLocale } from '@/context/LocaleContext';

export interface SyncStatusCardProps {
  lastCommittedAt: string | null;
  pendingLocalCount: number | null;
  status?: 'healthy' | 'warning' | 'delayed' | 'unknown' | string;
}

function timeAgo(dateStr: string | null, t: (key: string, params?: Record<string, string | number>) => string): string {
  if (!dateStr) return '—';
  const diffMs = Date.now() - new Date(dateStr).getTime();
  if (diffMs < 0 || isNaN(diffMs)) return '—';
  const diffMins = Math.floor(diffMs / 60000);
  if (diffMins < 1) return t('dashboard.justNow');
  if (diffMins < 60) return t('dashboard.minsAgo', { count: diffMins });
  const diffHours = Math.floor(diffMins / 60);
  if (diffHours < 24) return t('dashboard.hoursAgo', { count: diffHours });
  return t('dashboard.daysAgo', { count: Math.floor(diffHours / 24) });
}

export const SyncStatusCard: React.FC<SyncStatusCardProps> = ({
  lastCommittedAt,
  pendingLocalCount,
  status,
}) => {
  const { t } = useLocale();

  // If status is not explicitly passed, do not default to healthy:
  // if lastCommittedAt is present, assume healthy, otherwise unknown.
  const resolvedStatus = status || (lastCommittedAt ? 'healthy' : 'unknown');

  const getBadgeClass = () => {
    switch (resolvedStatus) {
      case 'healthy':
        return 'badge-good';
      case 'warning':
        return 'badge-warning';
      case 'delayed':
      case 'failed':
      case 'error':
        return 'badge-danger';
      case 'unknown':
      default:
        return 'badge';
    }
  };

  const getStatusText = () => {
    switch (resolvedStatus) {
      case 'healthy':
        return t('common.healthy');
      case 'warning':
        return t('common.warning');
      case 'delayed':
      case 'failed':
      case 'error':
        return t('common.failed');
      case 'unknown':
      default:
        return t('common.unknown');
    }
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 12 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingBottom: 8, borderBottom: '1px solid var(--border-light)', fontSize: 12 }}>
        <span>{t('dashboard.syncStatus')}</span>
        <span className={`badge ${getBadgeClass()}`}>
          {getStatusText()}
        </span>
      </div>

      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', paddingBottom: 8, borderBottom: '1px solid var(--border-light)', fontSize: 12 }}>
        <span className="text-muted">{t('settings.lastSeen')}</span>
        <strong className="mono-num">{timeAgo(lastCommittedAt, t)}</strong>
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
