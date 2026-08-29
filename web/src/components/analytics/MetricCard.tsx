import React from 'react';
import { useLocale } from '@/context/LocaleContext';

export interface MetricCardProps {
  label: string;
  value: string | null;
  hint?: string;
  supported?: boolean;
  unit?: string;
}

export const MetricCard: React.FC<MetricCardProps> = ({
  label,
  value,
  hint,
  supported = true,
  unit,
}) => {
  const { t } = useLocale();

  return (
    <div className="metric-card">
      <div className="metric-card-label">
        <span>{label}</span>
        {!supported && (
          <span className="badge" style={{ fontSize: 9, padding: '1px 5px' }}>
            N/A
          </span>
        )}
      </div>

      <div className="metric-card-value mono-num">
        {supported ? (
          value !== null && value !== undefined ? (
            <>
              {value}
              {unit && <span style={{ fontSize: 13, fontWeight: 500, marginLeft: 4 }}>{unit}</span>}
            </>
          ) : (
            '—'
          )
        ) : (
          <span style={{ color: 'var(--text-subtle)', fontSize: 16 }}>—</span>
        )}
      </div>

      <div className="metric-card-hint">
        {hint || (supported ? ' ' : t('common.notSupported'))}
      </div>
    </div>
  );
};
