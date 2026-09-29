import { ArrowDownRight, ArrowRight, ArrowUpRight } from 'lucide-react';

export function ChangeBadge({ value, className = '', en = false }: { value: number; className?: string; en?: boolean }) {
  // Direction follows the displayed (one-decimal) value so "0.0%" is never shown as up or down.
  const shown = Math.round(value * 10) / 10;
  const direction = shown > 0 ? 'up' : shown < 0 ? 'down' : 'flat';
  const Icon = shown > 0 ? ArrowUpRight : shown < 0 ? ArrowDownRight : ArrowRight;
  const label = en ? `${direction}, ${Math.abs(shown)} percent` : `${shown > 0 ? '上涨' : shown < 0 ? '下降' : '持平'} ${Math.abs(shown)}%`;
  return (
    <span className={`change-badge change-${direction} ${className}`} aria-label={label}>
      <Icon size={15} aria-hidden="true" />
      <span>{shown > 0 ? '+' : shown < 0 ? '−' : ''}{Math.abs(shown).toFixed(1)}%</span>
    </span>
  );
}
