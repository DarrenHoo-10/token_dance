import React from 'react';
import { useNavigate } from 'react-router-dom';
import { useLocale } from '@/context/LocaleContext';
import { Button } from '@/components/common/Button';

export interface CompareTrayProps {
  handles: string[];
  onRemove: (handle: string) => void;
  onClear: () => void;
}

export const CompareTray: React.FC<CompareTrayProps> = ({
  handles,
  onRemove,
  onClear,
}) => {
  const { t } = useLocale();
  const navigate = useNavigate();

  if (!handles || handles.length === 0) {
    return null;
  }

  const handleStartCompare = () => {
    navigate(`/compare?handles=${encodeURIComponent(handles.join(','))}`);
  };

  return (
    <div className="compare-tray-float" role="region" aria-label={t('explore.compareTrayTitle')}>
      <strong style={{ fontSize: 13, marginRight: 8 }}>{t('explore.compareTrayTitle')}</strong>

      <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        {handles.map((handle) => (
          <div key={handle} className="compare-tray-user">
            <span>@{handle}</span>
            <button
              type="button"
              className="compare-tray-remove"
              onClick={() => onRemove(handle)}
              aria-label={`Remove @${handle}`}
            >
              ✕
            </button>
          </div>
        ))}
      </div>

      <div style={{ display: 'flex', gap: 8, marginLeft: 'auto' }}>
        <Button variant="ghost" size="sm" onClick={onClear} style={{ color: '#a0aaa2' }}>
          {t('explore.clearCompare')}
        </Button>
        <Button variant="primary" size="sm" onClick={handleStartCompare}>
          {t('explore.startCompare')} ({handles.length})
        </Button>
      </div>
    </div>
  );
};
