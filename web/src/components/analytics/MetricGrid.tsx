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

export const MetricGrid: React.FC<MetricGridProps> = ({ metrics }) => {
  const { t } = useLocale();

  return (
    <div className="metric-grid-10" aria-label={t('dashboard.coreMetricsLabel')}>
      <MetricCard label={t('metrics.estimatedCost')} value={metrics.estimatedCost?.amount == null ? null : `${metrics.estimatedCost.currency} ${Number(metrics.estimatedCost.amount).toFixed(2)}`} supported={metrics.estimatedCost?.supported} />
      {/* Total Tokens */}
      <MetricCard
        label={t('metrics.totalTokens')}
        value={formatNumber(metrics.totalTokens?.value)}
        supported={metrics.totalTokens?.supported}
      />

      {/* Generated Code Lines */}
      <MetricCard
        label={t('metrics.generatedCodeLines')}
        value={formatNumber(metrics.generatedCodeLines?.value)}
        supported={metrics.generatedCodeLines?.supported}
      />

      {/* Tokens per Code Line */}
      <MetricCard
        label={t('metrics.tokensPerCodeLine')}
        value={metrics.tokensPerCodeLine?.value ? parseFloat(metrics.tokensPerCodeLine.value).toFixed(1) : null}
        supported={metrics.tokensPerCodeLine?.supported}
      />

      {/* Input Context */}
      <MetricCard
        label={t('metrics.inputContextTokens')}
        value={formatNumber(metrics.inputContextTokens?.value)}
        supported={metrics.inputContextTokens?.supported}
      />

      {/* Output Tokens */}
      <MetricCard
        label={t('metrics.outputTokens')}
        value={formatNumber(metrics.outputTokens?.value)}
        supported={metrics.outputTokens?.supported}
      />

      {/* Cache Hit Rate */}
      <MetricCard
        label={t('metrics.cacheHitRate')}
        value={formatPercentage(metrics.cacheHitRate?.value)}
        supported={metrics.cacheHitRate?.supported}
      />

      {/* Total Active Duration */}
      <MetricCard
        label={t('metrics.activeDurationMs')}
        value={formatDurationHours(metrics.activeDurationMs?.value)}
        supported={metrics.activeDurationMs?.supported}
      />

      {/* Total Messages */}
      <MetricCard
        label={t('metrics.messageCount')}
        value={formatNumber(metrics.messageCount?.value)}
        supported={metrics.messageCount?.supported}
      />

      {/* User Messages */}
      <MetricCard
        label={t('metrics.userMessageCount')}
        value={formatNumber(metrics.userMessageCount?.value)}
        supported={metrics.userMessageCount?.supported}
      />
    </div>
  );
};
