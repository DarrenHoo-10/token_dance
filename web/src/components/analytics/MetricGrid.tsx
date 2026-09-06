import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import type { PersonalSummaryMetrics } from '@/types/api';
import { MetricCard } from './MetricCard';

export interface MetricGridProps {
  metrics: PersonalSummaryMetrics;
}

function formatNumber(val: string | null | undefined): string | null {
  if (val === null || val === undefined) return null;
  const num = parseFloat(val);
  if (isNaN(num)) return val;
  if (num >= 1_000_000_000) return (num / 1_000_000_000).toFixed(1) + 'B';
  if (num >= 1_000_000) return (num / 1_000_000).toFixed(1) + 'M';
  if (num >= 1_000) return (num / 1_000).toFixed(1) + 'K';
  return num.toLocaleString();
}

function formatDurationHours(msStr: string | null | undefined): string | null {
  if (!msStr) return null;
  const ms = parseFloat(msStr);
  if (isNaN(ms)) return null;
  const hours = (ms / (1000 * 60 * 60)).toFixed(1);
  return `${hours}h`;
}

function formatPercentage(val: string | null | undefined): string | null {
  if (!val) return null;
  const num = parseFloat(val);
  if (isNaN(num)) return null;
  const pct = num <= 1 ? (num * 100).toFixed(1) : num.toFixed(1);
  return `${pct}%`;
}

function formatCost(amount: string | null | undefined, currency: string | null | undefined = 'USD'): string | null {
  if (!amount) return null;
  const num = parseFloat(amount);
  if (isNaN(num)) return null;
  const curr = currency || 'USD';
  const formatted = num.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  return curr === 'USD' ? `$${formatted}` : `${formatted} ${curr}`;
}

export const MetricGrid: React.FC<MetricGridProps> = ({ metrics }) => {
  const { t, locale } = useLocale();
  const zh=locale==='zh-CN';
  const cost=metrics.estimatedCost;
  const unpriced=Math.max(0,(cost?.totalRequests??0)-(cost?.pricedRequests??0));
  const costHint=cost?.pricingSource==='openrouter'
    ? (zh ? `OpenRouter 参考价估算${unpriced ? ` · ${unpriced} 次用量未匹配价格` : ''}` : `OpenRouter estimate${unpriced ? ` · ${unpriced} unpriced requests` : ''}`)
    : cost?.supported ? t('metrics.currentPeriod') : (zh?'尚无可用费用记录':'No cost data yet');

  return (
    <div className="metric-grid-10" aria-label={t('dashboard.coreMetricsLabel')}>
      {/* 1. Estimated Cost */}
      <MetricCard
        label={t('metrics.estimatedCost')}
        value={formatCost(metrics.estimatedCost?.amount, metrics.estimatedCost?.currency)}
        hint={costHint}
        supported={metrics.estimatedCost?.supported}
      />

      {/* 2. Total Tokens */}
      <MetricCard
        label={t('metrics.totalTokens')}
        value={formatNumber(metrics.totalTokens?.value)}
        hint={metrics.totalTokens?.change || t('metrics.exactDerived')}
        supported={metrics.totalTokens?.supported}
      />

      {/* 3. Generated Code Lines */}
      <MetricCard
        label={t('metrics.generatedCodeLines')}
        value={formatNumber(metrics.generatedCodeLines?.value)}
        hint={metrics.generatedCodeLines?.supported ? (metrics.generatedCodeLines?.change || t('metrics.lines')) : (zh?'尚未采集代码行':'No code-line data yet')}
        supported={metrics.generatedCodeLines?.supported}
      />

      {/* 4. Tokens per Code Line */}
      <MetricCard
        label={t('metrics.tokensPerCodeLine')}
        value={metrics.tokensPerCodeLine?.value ? parseFloat(metrics.tokensPerCodeLine.value).toFixed(1) : null}
        hint={metrics.tokensPerCodeLine?.supported ? t('metrics.average') : (zh?'需要代码行数据':'Code-line data required')}
        supported={metrics.tokensPerCodeLine?.supported}
      />

      {/* 5. Input Context */}
      <MetricCard
        label={t('metrics.inputContextTokens')}
        value={formatNumber(metrics.inputContextTokens?.value)}
        hint={t('metrics.inputContextHint')}
        supported={metrics.inputContextTokens?.supported}
      />

      {/* 6. Output Tokens */}
      <MetricCard
        label={t('metrics.outputTokens')}
        value={formatNumber(metrics.outputTokens?.value)}
        hint={t('metrics.outputTokensHint')}
        supported={metrics.outputTokens?.supported}
      />

      {/* 7. Cache Hit Rate */}
      <MetricCard
        label={t('metrics.cacheHitRate')}
        value={formatPercentage(metrics.cacheHitRate?.value)}
        hint={t('metrics.cacheHitRateHint')}
        supported={metrics.cacheHitRate?.supported}
      />

      {/* 8. Total Active Duration */}
      <MetricCard
        label={t('metrics.activeDurationMs')}
        value={formatDurationHours(metrics.activeDurationMs?.value)}
        hint={metrics.activeDurationMs?.supported ? t('metrics.activeTime') : (zh?'尚未采集时长':'No duration data yet')}
        supported={metrics.activeDurationMs?.supported}
      />

      {/* 9. Total Messages */}
      <MetricCard
        label={t('metrics.messageCount')}
        value={formatNumber(metrics.messageCount?.value)}
        hint={t('metrics.messageCountHint')}
        supported={metrics.messageCount?.supported}
      />

      {/* 10. User Messages */}
      <MetricCard
        label={t('metrics.userMessageCount')}
        value={formatNumber(metrics.userMessageCount?.value)}
        hint={t('metrics.userMessageCountHint')}
        supported={metrics.userMessageCount?.supported}
      />
    </div>
  );
};
