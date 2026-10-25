import { useState } from 'react';
import type { ModelUsage } from '../tauri-bridge';
import type { UsageRange } from '../usage-analytics';

// Agents report the same model under different spellings (GLM-5.3-Flash and
// glm-5.3-flash are separate dimensions), so one model would be split across
// rows. Model ids are case-insensitive: merge by lowercased id and let the
// highest-usage spelling carry the label and identity.
export function mergeModelVariants(models: ModelUsage[]): ModelUsage[] {
  const sum = (value: number) => (Number.isFinite(value) && value > 0 ? value : 0);
  const merged = new Map<string, { model: ModelUsage; top: number; today: number; week: number; total: number }>();
  for (const model of models) {
    const key = model.modelId.trim().toLowerCase();
    const today = sum(model.todayTokens);
    const week = sum(model.weekTokens);
    const total = sum(model.totalTokens);
    const existing = merged.get(key);
    if (!existing) {
      merged.set(key, { model, top: total, today, week, total });
      continue;
    }
    existing.today += today;
    existing.week += week;
    existing.total += total;
    if (total > existing.top) {
      existing.model = model;
      existing.top = total;
    }
  }
  return [...merged.values()].map(({ model, today, week, total }) => (
    { ...model, todayTokens: today, weekTokens: week, totalTokens: total }
  ));
}

export function ModelUsageList({ models = [], range, zh }: { models?: ModelUsage[]; range: UsageRange; zh: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const field = range === 'today' ? 'todayTokens' : range === 'week' ? 'weekTokens' : 'totalTokens';
  const ranked = mergeModelVariants(models)
    .filter(model => Number.isFinite(model[field]) && model[field] > 0)
    .sort((a, b) => b[field] - a[field] || a.modelKey - b.modelKey);
  if (!ranked.length) return null;
  const visible = expanded ? ranked : ranked.slice(0, 3);
  const format = new Intl.NumberFormat('en', { maximumFractionDigits: 2, notation: 'compact' });
  return <details className="usage-models">
    <summary>{zh ? '模型用量' : 'Model usage'}</summary>
    <ul>{visible.map(model => <li key={model.modelKey}>
      <span className="usage-model-name" title={model.providerId ? `${model.providerId} / ${model.modelId}` : model.modelId}>{model.modelId || (zh ? '未知模型' : 'Unknown model')}</span>
      <span className="usage-model-tokens" title={`${model[field].toLocaleString()} tokens`}>{format.format(model[field])}<small>tokens</small></span>
    </li>)}</ul>
    {ranked.length > 3 && <button type="button" className="usage-model-more" aria-expanded={expanded} onClick={() => setExpanded(!expanded)}>
      {expanded ? (zh ? '收起' : 'Show less') : (zh ? `展开全部 (${ranked.length})` : `Show all (${ranked.length})`)}
    </button>}
  </details>;
}
