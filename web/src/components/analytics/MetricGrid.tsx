import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import type { PersonalSummaryMetrics } from '@/types/api';
import { formatPersonalCost } from '@/utils/cost';
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
  const { t, locale } = useLocale();
  const zh = locale === 'zh-CN';
  const cost = formatPersonalCost(metrics.estimatedCost, metrics.estimatedCosts);

  return (
    <div className="metric-grid-10" aria-label={t('dashboard.coreMetricsLabel')}>
      <MetricCard
        label={t('metrics.totalTokens')}
        value={formatNumber(metrics.totalTokens?.value)}
        supported={metrics.totalTokens?.supported}
        hint={zh ? '本周期已同步用量' : 'Synced usage this period'}
      />
      <MetricCard
        label={t('metrics.estimatedCost')}
        value={cost.value}
        supported={cost.supported}
        hint={zh ? '已记录与估算费用' : 'Reported and estimated cost'}
      />
      <MetricCard
        label={t('metrics.generatedCodeLines')}
        value={formatNumber(metrics.generatedCodeLines?.value)}
        supported={metrics.generatedCodeLines?.supported}
        hint={zh ? '工具记录的代码行数' : 'Recorded by supported tools'}
      />
      <MetricCard
        label={t('metrics.messageCount')}
        value={formatNumber(metrics.messageCount?.value)}
        supported={metrics.messageCount?.supported}
        hint={zh ? '已记录的交互轮次' : 'Recorded interactions'}
      />
      <MetricCard
        label={t('metrics.activeDurationMs')}
        value={formatDurationHours(metrics.activeDurationMs?.value)}
        supported={metrics.activeDurationMs?.supported}
        hint={zh ? '已记录会话累计时长' : 'Combined recorded sessions'}
      />
      <MetricCard
        label={t('metrics.inputContextTokens')}
        value={formatNumber(metrics.inputContextTokens?.value)}
        supported={metrics.inputContextTokens?.supported}
        hint="Prompt + Cache read"
      />
      <MetricCard
        label={t('metrics.outputTokens')}
        value={formatNumber(metrics.outputTokens?.value)}
        supported={metrics.outputTokens?.supported}
        hint={zh ? '补全与生成 Token' : 'Completion and generation'}
      />
      <MetricCard
        label={t('metrics.cacheHitRate')}
        value={formatPercentage(metrics.cacheHitRate?.value)}
        supported={metrics.cacheHitRate?.supported}
        hint={zh ? '缓存命中占比' : 'Share of cache hits'}
      />
      <MetricCard
        label={t('metrics.tokensPerCodeLine')}
        value={metrics.tokensPerCodeLine?.value ? parseFloat(metrics.tokensPerCodeLine.value).toFixed(1) : null}
        supported={metrics.tokensPerCodeLine?.supported}
        hint={zh ? '平均每行代码' : 'Average per line of code'}
      />
      <MetricCard
        label={t('metrics.userMessageCount')}
        value={formatNumber(metrics.userMessageCount?.value)}
        supported={metrics.userMessageCount?.supported}
        hint={zh ? '用户触发的消息' : 'User-initiated messages'}
      />
    </div>
  );
};
