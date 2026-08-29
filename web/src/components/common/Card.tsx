import React from 'react';

export interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: 'light' | 'dark';
}

export const Card: React.FC<CardProps> = ({
  children,
  variant = 'light',
  className = '',
  ...props
}) => {
  return (
    <div
      className={`panel ${variant === 'dark' ? 'panel-dark' : ''} ${className}`.trim()}
      {...props}
    >
      {children}
    </div>
  );
};
