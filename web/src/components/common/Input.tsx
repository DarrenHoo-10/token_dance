import React from 'react';

export interface InputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'prefix'> {
  label?: string;
  hint?: string;
  error?: string;
  prefix?: React.ReactNode;
  suffix?: React.ReactNode;
}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ label, hint, error, prefix, suffix, className = '', id, ...props }, ref) => {
    const inputId = id || (label ? `input-${label.toLowerCase().replace(/\s+/g, '-')}` : undefined);

    return (
      <div className="form-group">
        {label && (
          <label htmlFor={inputId} className="form-label">
            {label}
          </label>
        )}
        <div style={{ display: 'flex', alignItems: 'center', position: 'relative' }}>
          {prefix && (
            <span style={{ position: 'absolute', left: 12, color: 'var(--text-subtle)', pointerEvents: 'none' }}>
              {prefix}
            </span>
          )}
          <input
            id={inputId}
            ref={ref}
            className={`form-input ${className}`.trim()}
            style={{
              width: '100%',
              paddingLeft: prefix ? '36px' : undefined,
              paddingRight: suffix ? '36px' : undefined,
              borderColor: error ? 'var(--danger)' : undefined,
            }}
            {...props}
          />
          {suffix && (
            <span style={{ position: 'absolute', right: 12, color: 'var(--text-subtle)' }}>
              {suffix}
            </span>
          )}
        </div>
        {error && <span className="form-error">{error}</span>}
        {hint && !error && <span className="form-hint">{hint}</span>}
      </div>
    );
  }
);

Input.displayName = 'Input';
