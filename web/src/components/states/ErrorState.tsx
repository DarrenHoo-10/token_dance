import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import { Button } from '@/components/common/Button';
import { ApiError } from '@/api/client';
import { getApiErrorMessage } from '@/i18n';

export interface ErrorStateProps {
  error?: Error | ApiError | null;
  title?: string;
  description?: string;
  onRetry?: () => void;
}

export const ErrorState: React.FC<ErrorStateProps> = ({
  error,
  title,
  description,
  onRetry,
}) => {
  const { t } = useLocale();

  let errorMessage = description;
  let errorCode: string | undefined;

  if (error instanceof ApiError) {
    errorCode = error.code;
    errorMessage = getApiErrorMessage(t, error);
  } else if (error instanceof Error) {
    errorMessage = error.message;
  }

  return (
    <div className="state-box" style={{ borderColor: 'var(--danger-border)' }}>
      <div
        style={{
          width: 48,
          height: 48,
          borderRadius: '50%',
          backgroundColor: 'var(--danger-bg)',
          color: 'var(--danger)',
          display: 'grid',
          placeItems: 'center',
          fontSize: 20,
          fontWeight: 700,
        }}
      >
        !
      </div>
      <h3>{title || t('states.errorTitle')}</h3>
      <p>{errorMessage || t('states.errorDesc')}</p>
      {errorCode && (
        <span
          className="badge badge-danger"
          style={{ marginBottom: 16, fontFamily: 'var(--font-mono)' }}
        >
          {errorCode}
        </span>
      )}
      {onRetry && (
        <Button variant="outline" onClick={onRetry}>
          {t('common.retry')}
        </Button>
      )}
    </div>
  );
};
