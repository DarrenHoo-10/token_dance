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
  /** Raw model ids merged into this row (single-spelling rows have one alias). */
  aliases: string[];
}

function formatTokens(tokens: number): string {
  const compact = compactNumber(tokens, { decimals: 2, kDecimals: 1 });
  return compact ? `${compact.value}${compact.unit}` : Math.round(tokens).toLocaleString();
}

function formatPercent(percentage: number): string {
  if (percentage > 0 && percentage < 0.1) return '<0.1%';
  return `${percentage.toFixed(1)}%`;
}

interface MergedModel {
  key: string;
  label: string;
  tokens: number;
  percentage: number;
  /** Every raw model id merged into this row, so the model filter still highlights. */
  aliases: string[];
}

// Agents report the same model under different spellings (GLM-5.3-Flash and
// glm-5.3-flash are separate dimensions, and Fireworks routes surface
// `accounts/<org>/models/...` paths), so one model would be split across
// rows. Merge by the folded spelling key and let the highest-usage variant
// carry the label; a provider path never does — it displays its folded key
// so the row still reads as a model name.
function foldModelKey(modelId: string): string {
  const lowered = modelId.trim().toLowerCase();
  const idx = lowered.indexOf('/models/');
  const slug = idx >= 0 ? lowered.slice(idx + '/models/'.length) : lowered;
  let decoded = '';
  for (let i = 0; i < slug.length; i++) {
    const dot = slug[i] === 'p' && i > 0 && i + 1 < slug.length
      && /\d/.test(slug[i - 1]) && /\d/.test(slug[i + 1]);
    decoded += dot ? '.' : slug[i];
  }
  return decoded;
}

const isProviderPath = (modelId: string) => modelId.trim().toLowerCase().includes('/models/');

function mergeByModelId(items: BreakdownItem[]): MergedModel[] {
  const merged = new Map<string, MergedModel & { top: number }>();
  for (const item of items) {
    const tokens = Number(item.tokenTotal) || 0;
    if (tokens <= 0) continue;
    const key = foldModelKey(item.key);
    const label = isProviderPath(item.key) ? key : (item.label || item.key);
    const existing = merged.get(key);
    if (existing) {
      existing.tokens += tokens;
      existing.percentage += Math.max(0, Number(item.percentage) || 0);
      existing.aliases.push(item.key);
      if (tokens > existing.top) {
        existing.key = key;
        existing.label = label;
        existing.top = tokens;
      }
      continue;
    }
    merged.set(key, { key, label, top: tokens, tokens, percentage: Math.max(0, Number(item.percentage) || 0), aliases: [item.key] });
  }
  return [...merged.values()].map(({ key, label, tokens, percentage, aliases }) => ({ key, label, tokens, percentage, aliases }));
}

function buildRows(items: BreakdownItem[], otherLabel: string): ModelRow[] {
  const sorted = mergeByModelId(items).sort((a, b) => b.tokens - a.tokens);
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
      aliases: [],
    });
  }
  return rows;
}

export const ModelBreakdown: React.FC<ModelBreakdownProps> = ({ items, selectedModel }) => {
  const { t } = useLocale();
  const modelCount = items ? mergeByModelId(items).length : 0;
  const rows = items ? buildRows(items, t('dashboard.modelShareOthers')) : [];

  return (
    <div className="model-breakdown">
      <div className="model-breakdown-head">
        <strong>{t('dashboard.modelShare')}</strong>
        {items && rows.length > 0 && <small>{t('dashboard.modelShareCount', { count: modelCount })}</small>}
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
              <li key={row.key} className={selectedModel && (row.key === selectedModel || row.aliases.includes(selectedModel)) ? 'selected' : undefined}>
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
