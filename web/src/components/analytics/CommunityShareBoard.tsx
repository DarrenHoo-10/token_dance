import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { HelpCircle } from 'lucide-react';
import { usageColor } from '@/utils/usageColors';

export interface CommunityShareItem {
  id: string;
  label: string;
  sharePct?: number | null;
  color?: string;
  mark?: ReactNode;
}

export function CommunityShareBoard({
  title,
  helpTo,
  helpLabel,
  items,
  empty,
  caption,
}: {
  title: string;
  helpTo: string;
  helpLabel: string;
  items: CommunityShareItem[];
  empty: string;
  caption: string;
}) {
  return (
    <section className="panel sky-harnesses sky-share-board">
      <div className="panel-header">
        <h2>{title}</h2>
        <Link className="sky-text-link" to={helpTo}>
          <HelpCircle size={18} />
          <span className="sr-only">{helpLabel}</span>
        </Link>
      </div>
      {items.length ? (
        <div className="tool-list">
          {items.map((item) => {
            const share = Math.round(item.sharePct ?? 0);
            const color = item.color ?? usageColor(item.id);
            return (
              <div className="tool-row" key={item.id}>
                {item.mark ?? (
                  <span className="tool-mark sky-share-mark" style={{ background: color }} aria-hidden="true">
                    {(item.label.trim()[0] || '?').toUpperCase()}
                  </span>
                )}
                <strong>{item.label}</strong>
                <div className="tool-track">
                  <i style={{ width: `${Math.max(0, Math.min(100, share))}%`, background: color }} />
                </div>
                <span>{share}%</span>
              </div>
            );
          })}
        </div>
      ) : (
        <p className="side-card-empty">{empty}</p>
      )}
      <p className="sky-harness-caption">{caption}</p>
    </section>
  );
}
