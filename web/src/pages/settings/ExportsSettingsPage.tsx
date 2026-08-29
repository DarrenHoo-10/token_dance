import React, { useState, useEffect, useCallback } from 'react';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useNotification } from '@/context/NotificationContext';
import { Button } from '@/components/common/Button';
import { Badge } from '@/components/common/Badge';
import { Modal } from '@/components/common/Modal';
import { Input } from '@/components/common/Input';
import { LoadingState } from '@/components/states/LoadingState';
import { ErrorState } from '@/components/states/ErrorState';
import { api, ApiError } from '@/api/client';
import type { ExportJob, DeletionRequest } from '@/types/api';

export const ExportsSettingsPage: React.FC = () => {
  const { user } = useAuth();
  const { t } = useLocale();
  const { showToast } = useNotification();

  const [jobs, setJobs] = useState<ExportJob[]>([]);
  const [activeDeletion, setActiveDeletion] = useState<DeletionRequest | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | Error | null>(null);
  const [creatingExport, setCreatingExport] = useState(false);

  // Deletion Modal
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [confirmHandle, setConfirmHandle] = useState('');
  const [submittingDelete, setSubmittingDelete] = useState(false);

  const fetchData = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const res = await api.getExports();
      setJobs(res.jobs || []);
    } catch (err) {
      setError(err instanceof ApiError ? err : new Error(String(err)));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const handleCreateExport = async () => {
    try {
      setCreatingExport(true);
      const idempotencyKey = 'exp_' + Math.random().toString(36).substring(2, 10);
      await api.createExport(
        {
          scope: 'all_aggregates',
          format: 'csv',
        },
        idempotencyKey
      );
      showToast(t('common.saved'), 'success');
      await fetchData();
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    } finally {
      setCreatingExport(false);
    }
  };

  const handleDownload = async (exportId: string) => {
    try {
      const res = await api.getExportDownloadUrl(exportId);
      if (res.downloadUrl) {
        window.open(res.downloadUrl, '_blank');
      }
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    }
  };

  const handleRequestAccountDeletion = async (e: React.FormEvent) => {
    e.preventDefault();
    if (confirmHandle !== user?.handle && confirmHandle !== user?.displayName) {
      showToast('Handle confirmation does not match', 'error');
      return;
    }

    try {
      setSubmittingDelete(true);
      const req = await api.createDeletionRequest({
        scope: 'account',
        confirmation: true,
      });
      setActiveDeletion(req);
      setDeleteModalOpen(false);
      showToast(t('settings.deleteTitle'), 'info');
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    } finally {
      setSubmittingDelete(false);
    }
  };

  const handleCancelDeletion = async () => {
    if (!activeDeletion) return;
    try {
      await api.cancelDeletionRequest(activeDeletion.requestId);
      setActiveDeletion(null);
      showToast(t('settings.cancelDeletion'), 'success');
    } catch (err) {
      showToast(err instanceof Error ? err.message : t('errors.unknown'), 'error');
    }
  };

  if (loading && jobs.length === 0) return <LoadingState />;
  if (error) return <ErrorState error={error} onRetry={fetchData} />;

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 24 }}>
      {/* Deletion Grace Period Banner if pending */}
      {activeDeletion && activeDeletion.requestStatus === 'pending' && (
        <div
          style={{
            padding: '16px 20px',
            backgroundColor: 'var(--danger-bg)',
            border: '1px solid var(--danger-border)',
            borderRadius: 'var(--radius-md)',
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            flexWrap: 'wrap',
            gap: 12,
          }}
        >
          <div>
            <strong style={{ color: 'var(--danger)', fontSize: 13 }}>⚠️ Account Deletion Pending</strong>
            <p style={{ fontSize: 12, color: 'var(--danger)', margin: '4px 0 0' }}>
              {t('settings.deletionPendingBanner', {
                date: activeDeletion.cancelBefore
                  ? new Date(activeDeletion.cancelBefore).toLocaleDateString()
                  : '7 days',
              })}
            </p>
          </div>
          <Button variant="outline" size="sm" onClick={handleCancelDeletion}>
            {t('settings.cancelDeletion')}
          </Button>
        </div>
      )}

      {/* Export Section */}
      <div className="panel">
        <div className="panel-header">
          <div>
            <h2>{t('settings.exportTitle')}</h2>
            <p className="text-muted" style={{ fontSize: 12 }}>
              {t('settings.exportDesc')}
            </p>
          </div>
          <Button variant="primary" onClick={handleCreateExport} loading={creatingExport}>
            + {t('settings.createExport')}
          </Button>
        </div>

        {jobs.length === 0 ? (
          <div style={{ padding: '24px 0', textAlign: 'center', color: 'var(--text-subtle)', fontSize: 13 }}>
            No export jobs generated yet
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {jobs.map((job) => (
              <div
                key={job.exportId}
                style={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  padding: '14px 16px',
                  borderRadius: 'var(--radius-sm)',
                  backgroundColor: 'var(--bg-subtle)',
                  border: '1px solid var(--border-light)',
                }}
              >
                <div>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <strong style={{ fontSize: 13 }}>CSV Export ({job.scope})</strong>
                    <Badge
                      variant={
                        job.jobStatus === 'completed'
                          ? 'good'
                          : job.jobStatus === 'pending' || job.jobStatus === 'running'
                          ? 'warning'
                          : 'danger'
                      }
                    >
                      {job.jobStatus}
                    </Badge>
                  </div>
                  <div style={{ fontSize: 11, color: 'var(--text-muted)', marginTop: 2 }}>
                    Created: {new Date(job.createdAt).toLocaleString()}
                    {job.fileSizeBytes && ` · ${(job.fileSizeBytes / 1024).toFixed(1)} KB`}
                  </div>
                </div>

                <div>
                  {job.jobStatus === 'completed' && (
                    <Button variant="outline" size="sm" onClick={() => handleDownload(job.exportId)}>
                      ↓ {t('settings.downloadFile')}
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Account Deletion Section */}
      <div className="panel" style={{ borderColor: 'var(--danger-border)' }}>
        <div className="panel-header">
          <div>
            <h2 style={{ color: 'var(--danger)' }}>{t('settings.deleteTitle')}</h2>
            <p className="text-muted" style={{ fontSize: 12 }}>
              {t('settings.deleteDesc')}
            </p>
          </div>
          <Button
            variant="danger"
            onClick={() => setDeleteModalOpen(true)}
            disabled={activeDeletion?.requestStatus === 'pending'}
          >
            {t('settings.requestDeletion')}
          </Button>
        </div>
      </div>

      {/* Deletion Confirmation Modal */}
      <Modal
        isOpen={deleteModalOpen}
        onClose={() => setDeleteModalOpen(false)}
        title={t('settings.deletionModalTitle')}
        footer={
          <div style={{ display: 'flex', gap: 10 }}>
            <Button variant="outline" onClick={() => setDeleteModalOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="danger"
              onClick={handleRequestAccountDeletion}
              loading={submittingDelete}
              disabled={!confirmHandle}
            >
              {t('settings.requestDeletion')}
            </Button>
          </div>
        }
      >
        <div style={{ padding: '8px 0' }}>
          <p style={{ fontSize: 13, color: 'var(--text-muted)', marginBottom: 16 }}>
            {t('settings.deletionModalDesc')}
          </p>

          <Input
            label={t('settings.deletionConfirmationPrompt')}
            placeholder={user?.handle || 'your-handle'}
            value={confirmHandle}
            onChange={(e) => setConfirmHandle(e.target.value)}
            autoFocus
          />
        </div>
      </Modal>
    </div>
  );
};
