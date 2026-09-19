import { useState } from 'react';
import type { ModelUsage } from '../tauri-bridge';
import type { UsageRange } from '../usage-analytics';

export function ModelUsageList({ models = [], range, zh }: { models?: ModelUsage[]; range: UsageRange; zh: boolean }) {
  const [expanded, setExpanded] = useState(false);
  const field = range === 'today' ? 'todayTokens' : range === 'week' ? 'weekTokens' : 'totalTokens';
  const ranked = models.filter(model => Number.isFinite(model[field]) && model[field] > 0)
    .sort((a, b) => b[field] - a[field] || a.modelKey - b.modelKey);
  if (!ranked.length) return null;
  const visible = expanded ? ranked : ranked.slice(0, 3);
  const format = new Intl.NumberFormat('en', { maximumFractionDigits: 2, notation: 'compact' });
  return <details className="usage-models" open>
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
