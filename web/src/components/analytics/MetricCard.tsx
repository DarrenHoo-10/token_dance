import React from 'react';

export interface MetricCardProps {
  label: string;
  value: string | null;
  supported?: boolean;
  unit?: string;
  hint?: string;
}

export const MetricCard: React.FC<MetricCardProps> = ({
  label,
  value,
  supported = true,
  unit,
  hint,
}) => {
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
      {hint && <div className="metric-card-hint">{hint}</div>}
    </div>
  );
};
