import React from 'react';

export interface BadgeProps {
  children: React.ReactNode;
  variant?: 'default' | 'lime' | 'good' | 'warning' | 'danger';
  className?: string;
}

export const Badge: React.FC<BadgeProps> = ({
  children,
  variant = 'default',
  className = '',
}) => {
  const variantClass = {
    default: '',
    lime: 'badge-lime',
    good: 'badge-good',
    warning: 'badge-warning',
    danger: 'badge-danger',
  }[variant];

  return <span className={`badge ${variantClass} ${className}`.trim()}>{children}</span>;
};
