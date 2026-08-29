import React, { useState, useEffect, useCallback } from 'react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { Modal } from '@/components/common/Modal';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { EmptyState } from '@/components/states/EmptyState';
import { api, ApiError } from '@/api/client';
import type { CollectorDevice, DeviceBindingChallengeResponse } from '@/types/api';

export const DevicesSettingsPage: React.FC = () => {
  const { refreshSession } = useAuth();
  const { t } = useLocale();
  const { showToast } = useNotification();

  const [devices, setDevices] = useState<CollectorDevice[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);

  // Binding Modal State
  const [bindModalOpen, setBindModalOpen] = useState(false);
  const [challenge, setChallenge] = useState<DeviceBindingChallengeResponse | null>(null);
  const [generatingCode, setGeneratingCode] = useState(false);

  const fetchDevices = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const res = await api.getDevices();
      setDevices(res.devices || []);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchDevices();
  }, [fetchDevices]);

  const handleCreateBindCode = async () => {
    try {
      setGeneratingCode(true);
      const res = await api.createDeviceBindingChallenge();
      setChallenge(res);
      setBindModalOpen(true);
    } catch (err) {
      if (err instanceof ApiError) {
        showToast(t(err.messageKey) || err.message, 'error');
      } else {
        showToast(t('errors.unknown'), 'error');
      }
    } finally {
      setGeneratingCode(false);
    }
  };

  const handlePauseDevice = async (installationId: string) => {
    try {
      await api.pauseDevice(installationId);
      showToast(t('common.saved'), 'success');
      await fetchDevices();
      await refreshSession();
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    }
  };

  const handleResumeDevice = async (installationId: string) => {
    try {
      await api.resumeDevice(installationId);
      showToast(t('common.saved'), 'success');
      await fetchDevices();
      await refreshSession();
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    }
  };

  const handleRevokeDevice = async (installationId: string) => {
    if (!window.confirm(t('settings.revokeDeviceConfirm'))) return;

    try {
      await api.revokeDevice(installationId);
      showToast(t('common.saved'), 'success');
      await fetchDevices();
      await refreshSession();
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    }
  };

  const copyCodeToClipboard = () => {
    if (challenge?.code) {
      navigator.clipboard.writeText(challenge.code);
      showToast(t('common.copied'), 'success');
    }
  };

  if (loading && devices.length === 0) return <LoadingState />;
  if (error) return <ErrorState error={error} onRetry={fetchDevices} />;

  return (
    <div>
      <div className="panel">
        <div className="panel-header">
          <div>
            <h2>{t('settings.devicesCardTitle')}</h2>
            <p className="text-muted" style={{ fontSize: 12 }}>
              {t('settings.devicesCardSub')}
            </p>
          </div>
          <Button
            variant="primary"
            onClick={handleCreateBindCode}
            loading={generatingCode}
          >
            + {t('settings.connectDevice')}
          </Button>
        </div>

        {devices.length === 0 ? (
          <EmptyState
            title="No connected devices"
            description="Install TokenDance Collector on your workstation to start synchronizing AI coding metrics."
            actionText={t('settings.connectDevice')}
            onAction={handleCreateBindCode}
          />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
            {devices.map((device) => (
              <div
                key={device.installationId}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '16px',
                  borderRadius: 'var(--radius-md)',
                  border: '1px solid var(--border-light)',
                  backgroundColor: 'var(--bg-subtle)',
                  flexWrap: 'wrap',
                  gap: 12,
                }}
              >
                <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
                  <div
                    style={{
                      width: 42,
                      height: 42,
                      borderRadius: 10,
                      backgroundColor: 'var(--bg-dark)',
                      color: 'white',
                      display: 'grid',
                      placeItems: 'center',
                      fontWeight: 800,
                      fontSize: 12,
                    }}
                  >
                    {device.osType?.toUpperCase().substring(0, 3) || 'DEV'}
                  </div>

                  <div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <strong style={{ fontSize: 14 }}>{device.deviceName}</strong>
                      <Badge
                        variant={
                          device.installationStatus === 'active'
                            ? 'good'
                            : device.installationStatus === 'disabled'
                            ? 'warning'
                            : 'danger'
                        }
                      >
                        {device.installationStatus}
                      </Badge>
                    </div>

                    <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
                      {device.osType} {device.osVersion} · Collector {device.collectorVersion} ·{' '}
                      {t('settings.lastSeen')}:{' '}
                      <span className="mono-num">
                        {device.lastSeenAt ? new Date(device.lastSeenAt).toLocaleString() : 'Never'}
                      </span>
                    </div>
                  </div>
                </div>

                <div style={{ display: 'flex', gap: 8 }}>
                  {device.installationStatus === 'active' ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handlePauseDevice(device.installationId)}
                    >
                      {t('settings.pauseSync')}
                    </Button>
                  ) : device.installationStatus === 'disabled' ? (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => handleResumeDevice(device.installationId)}
                    >
                      {t('settings.resumeSync')}
                    </Button>
                  ) : null}

                  {device.installationStatus !== 'revoked' && (
                    <Button
                      variant="danger"
                      size="sm"
                      onClick={() => handleRevokeDevice(device.installationId)}
                    >
                      {t('settings.revokeDevice')}
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Bind Code Modal */}
      <Modal
        isOpen={bindModalOpen}
        onClose={() => setBindModalOpen(false)}
        title={t('settings.bindModalTitle')}
        footer={
          <Button variant="outline" onClick={() => setBindModalOpen(false)}>
            {t('common.close')}
          </Button>
        }
      >
        <div style={{ textAlign: 'center', padding: '16px 0' }}>
          <p className="text-muted" style={{ fontSize: 13, marginBottom: 20 }}>
            {t('settings.bindModalDesc')}
          </p>

          <div
            onClick={copyCodeToClipboard}
            style={{
              display: 'inline-flex',
              alignItems: 'center',
              justifyContent: 'center',
              padding: '16px 32px',
              borderRadius: 'var(--radius-lg)',
              backgroundColor: 'var(--bg-dark)',
              color: 'var(--lime)',
              fontFamily: 'var(--font-mono)',
              fontSize: 28,
              fontWeight: 800,
              letterSpacing: '0.15em',
              cursor: 'pointer',
              marginBottom: 16,
              border: '2px dashed var(--lime-border)',
            }}
            title="Click to copy"
          >
            {challenge?.code || '••••••••'}
          </div>

          <div style={{ fontSize: 12, color: 'var(--text-subtle)' }}>
            🕒 {t('settings.bindCodeExpiresIn')}
          </div>
        </div>
      </Modal>
    </div>
  );
};
