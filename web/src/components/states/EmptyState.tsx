import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import { Button } from '@/components/common/Button';

export interface EmptyStateProps {
  title?: string;
  description?: string;
  actionText?: string;
  onAction?: () => void;
  icon?: React.ReactNode;
}

export const EmptyState: React.FC<EmptyStateProps> = ({
  title,
  description,
  actionText,
  onAction,
  icon,
}) => {
  const { t } = useLocale();

  return (
    <div className="state-box">
      {icon ? (
        <div style={{ fontSize: 32, color: 'var(--text-subtle)', marginBottom: 8 }}>{icon}</div>
      ) : (
        <div
          style={{
            width: 48,
            height: 48,
            borderRadius: '50%',
            backgroundColor: 'var(--bg-subtle)',
            display: 'grid',
            placeItems: 'center',
            fontSize: 20,
            color: 'var(--text-subtle)',
          }}
        >
          ∅
        </div>
      )}
      <h3>{title || t('states.emptyTitle')}</h3>
      <p>{description || t('states.emptyDesc')}</p>
      {actionText && onAction && (
        <Button variant="primary" onClick={onAction}>
          {actionText}
        </Button>
      )}
    </div>
  );
};
