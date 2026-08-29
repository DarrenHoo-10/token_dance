import React from 'react';
import { useLocale } from '@/context/LocaleContext';

export interface LoadingStateProps {
  message?: string;
  height?: number | string;
}

export const LoadingState: React.FC<LoadingStateProps> = ({ message, height = 240 }) => {
  const { t } = useLocale();

  return (
    <div
      className="state-box"
      style={{ minHeight: height, border: 'none', background: 'transparent' }}
      role="status"
      aria-live="polite"
    >
      <div className="spinner" />
      <p style={{ marginTop: 14, color: 'var(--text-muted)' }}>
        {message || t('common.loading')}
      </p>
    </div>
  );
};
