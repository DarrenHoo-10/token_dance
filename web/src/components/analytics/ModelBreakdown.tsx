import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import { usageColorAt } from '@/utils/usageColors';
import { compactNumber } from '@/utils/formatNumber';
import type { BreakdownItem } from '@/types/api';

export interface ModelBreakdownProps {
  /** `null` means the breakdown could not be loaded; an empty array means no model usage in range. */
  items: BreakdownItem[] | null;
  /** Model currently picked in the filter, highlighted when present in the list. */
  selectedModel?: string;
}

const MAX_ROWS = 8;
const OTHER_KEY = '\0other';
const OTHER_COLOR = '#c9d3bd';

interface ModelRow {
  key: string;
  label: string;
  tokens: number;
  percentage: number;
  color: string;
}

function formatTokens(tokens: number): string {
  const compact = compactNumber(tokens, { decimals: 2, kDecimals: 1 });
  return compact ? `${compact.value}${compact.unit}` : Math.round(tokens).toLocaleString();
}

function formatPercent(percentage: number): string {
  if (percentage > 0 && percentage < 0.1) return '<0.1%';
  return `${percentage.toFixed(1)}%`;
}

function buildRows(items: BreakdownItem[], otherLabel: string): ModelRow[] {
  const sorted = items
    .map((item) => ({
      key: item.key,
      label: item.label || item.key,
      tokens: Number(item.tokenTotal) || 0,
      percentage: Math.max(0, Number(item.percentage) || 0),
    }))
    .filter((item) => item.tokens > 0)
    .sort((a, b) => b.tokens - a.tokens);
  const head = sorted.slice(0, sorted.length > MAX_ROWS ? MAX_ROWS - 1 : MAX_ROWS);
  const rows: ModelRow[] = head.map((item, index) => ({ ...item, color: usageColorAt(index) }));
  const rest = sorted.slice(head.length);
  if (rest.length) {
    rows.push({
      key: OTHER_KEY,
      label: `${otherLabel} (${rest.length})`,
      tokens: rest.reduce((sum, item) => sum + item.tokens, 0),
      percentage: rest.reduce((sum, item) => sum + item.percentage, 0),
      color: OTHER_COLOR,
    });
  }
  return rows;
}

export const ModelBreakdown: React.FC<ModelBreakdownProps> = ({ items, selectedModel }) => {
  const { t } = useLocale();
  const rows = items ? buildRows(items, t('dashboard.modelShareOthers')) : [];

  return (
    <div className="model-breakdown">
      <div className="model-breakdown-head">
        <strong>{t('dashboard.modelShare')}</strong>
        {items && rows.length > 0 && <small>{t('dashboard.modelShareCount', { count: items.length })}</small>}
      </div>
      {items === null ? (
        <p className="filter-status">{t('dashboard.modelShareError')}</p>
      ) : rows.length === 0 ? (
        <p className="filter-status">{t('dashboard.modelShareEmpty')}</p>
      ) : (
        <>
          <div
            className="model-share-bar"
            role="img"
            aria-label={rows.map((row) => `${row.label} ${formatPercent(row.percentage)}`).join(', ')}
          >
            {rows.map((row) => (
              <i key={row.key} style={{ flexGrow: row.tokens, background: row.color }} />
            ))}
          </div>
          <ul className="model-share-list">
            {rows.map((row) => (
              <li key={row.key} className={selectedModel && row.key === selectedModel ? 'selected' : undefined}>
                <i style={{ background: row.color }} />
                <span title={row.label}>{row.label}</span>
                <b>{formatPercent(row.percentage)}</b>
                <em>{formatTokens(row.tokens)}<small>Token</small></em>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
};
