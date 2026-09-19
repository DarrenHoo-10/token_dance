import { ArrowDownRight, ArrowRight, ArrowUpRight } from 'lucide-react';

export function ChangeBadge({ value, className = '', en = false }: { value: number; className?: string; en?: boolean }) {
  const direction = value > 0 ? 'up' : value < 0 ? 'down' : 'flat';
  const Icon = value > 0 ? ArrowUpRight : value < 0 ? ArrowDownRight : ArrowRight;
  const label = en ? `${direction}, ${Math.abs(value)} percent` : `${value > 0 ? '上涨' : value < 0 ? '下降' : '持平'} ${Math.abs(value)}%`;
  return (
    <span className={`change-badge change-${direction} ${className}`} aria-label={label}>
      <Icon size={15} aria-hidden="true" />
      <span>{value > 0 ? '+' : value < 0 ? '−' : ''}{Math.abs(value).toFixed(1)}%</span>
    </span>
  );
}
