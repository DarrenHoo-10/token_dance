import React from 'react';
import { ChevronDown, Monitor } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import type { CollectorDevice } from '@/types/api';

export interface SyncStatusCardProps {
  lastCommittedAt: string | null;
  status?: 'healthy' | 'warning' | 'delayed' | 'unknown' | string;
  pendingLocalCount?: number | null;
  devices?: CollectorDevice[];
  devicesOpen?: boolean;
  devicesLoading?: boolean;
  onToggleDevices?: () => void;
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

function deviceNote(device: CollectorDevice, zh: boolean) {
  if (device.installationStatus === 'disabled') return zh ? '采集已暂停' : 'Collection paused';
  if (device.installationStatus === 'revoked') return zh ? '设备已撤销' : 'Device revoked';
  if (device.adapterHealth === 'error') return zh ? '采集异常' : 'Collection error';
  if (device.adapterHealth === 'warning') return zh ? '采集告警' : 'Collection warning';
  return zh ? '采集与同步正常' : 'Collecting and syncing';
}

export const SyncStatusCard: React.FC<SyncStatusCardProps> = ({
  lastCommittedAt,
  status,
  pendingLocalCount = null,
  devices = [],
  devicesOpen = false,
  devicesLoading = false,
  onToggleDevices,
}) => {
  const { t, locale } = useLocale();
  const zh = locale === 'zh-CN';
  const resolvedStatus = status || (lastCommittedAt ? 'healthy' : 'unknown');
  const pendingLabel = pendingLocalCount == null ? '—' : String(pendingLocalCount);

  return (
    <div className="sync-status-card">
      <div className="sync-facts">
        <div>
          <span>{zh ? '最近提交' : 'Last committed'}</span>
          <strong>{timeAgo(lastCommittedAt, t)}</strong>
        </div>
        <div>
          <span>{zh ? '待同步记录' : 'Pending records'}</span>
          <strong>{pendingLabel}</strong>
        </div>
        {!onToggleDevices && (
          <div>
            <span>{t('dashboard.syncStatus')}</span>
            <strong>{resolvedStatus === 'healthy' ? t('common.healthy') : resolvedStatus === 'warning' ? t('common.warning') : t('common.unknown')}</strong>
          </div>
        )}
      </div>
      {onToggleDevices && (
        <>
          <button
            type="button"
            className="sync-details-toggle"
            aria-expanded={devicesOpen}
            onClick={onToggleDevices}
          >
            <Monitor size={15} />
            {zh ? '已连接的设备' : 'Connected device'}
            <ChevronDown size={14} className={devicesOpen ? 'rotate' : ''} />
          </button>
          {devicesOpen && (
            devicesLoading ? (
              <p className="sync-device-empty">{t('common.loading')}</p>
            ) : devices.length === 0 ? (
              <p className="sync-device-empty">{zh ? '还没有已连接的设备' : 'No connected devices yet'}</p>
            ) : devices.map((device) => (
              <div className="sync-device" key={device.installationId}>
                <Monitor size={24} />
                <div>
                  <strong>{device.deviceName || (zh ? '未命名设备' : 'Unnamed device')}</strong>
                  <span>{[device.osType, device.architecture].filter(Boolean).join(' · ')} · {deviceNote(device, zh)}</span>
                </div>
                <span className={`sync-device-dot ${device.installationStatus === 'active' && device.adapterHealth !== 'error' ? 'ok' : 'warn'}`} />
              </div>
            ))
          )}
        </>
      )}
    </div>
  );
};
