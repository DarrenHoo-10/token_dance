import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import type { PersonalSummaryMetrics } from '@/types/api';
import { formatPersonalCost } from '@/utils/cost';
import { MetricCard } from './MetricCard';

export interface MetricGridProps {
  metrics: PersonalSummaryMetrics;
  compact?: boolean;
  tokenHidden?: boolean;
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

export const MetricGrid: React.FC<MetricGridProps> = ({ metrics, compact = false, tokenHidden = false }) => {
  const { t, locale } = useLocale();
  const zh = locale === 'zh-CN';
  const cost = formatPersonalCost(metrics.estimatedCost, metrics.estimatedCosts);

  const cards = [
    <MetricCard key="totalTokens"
        label={t('metrics.totalTokens')}
        value={formatNumber(metrics.totalTokens?.value)}
        supported={metrics.totalTokens?.supported}
        hint={compact ? (zh ? '累计公开用量' : 'All-time shared usage') : (zh ? '本周期已同步用量' : 'Synced usage this period')}
      />,
    <MetricCard key="estimatedCost"
        label={t('metrics.estimatedCost')}
        value={cost.value}
        supported={cost.supported}
        hint={zh ? '已记录与估算费用' : 'Reported and estimated cost'}
      />,
    <MetricCard key="generatedCodeLines"
        label={t('metrics.generatedCodeLines')}
        value={formatNumber(metrics.generatedCodeLines?.value)}
        supported={metrics.generatedCodeLines?.supported}
        hint={zh ? '工具记录的代码行数' : 'Recorded by supported tools'}
      />,
    <MetricCard key="messageCount"
        label={t('metrics.messageCount')}
        value={formatNumber(metrics.messageCount?.value)}
        supported={metrics.messageCount?.supported}
        hint={zh ? '工具记录的消息与轮次事件，非模型请求数' : 'Recorded message/turn events, not model requests'}
      />,
    <MetricCard key="activeDurationMs"
        label={t('metrics.activeDurationMs')}
        value={formatDurationHours(metrics.activeDurationMs?.value)}
        supported={metrics.activeDurationMs?.supported}
        hint={zh ? '已记录会话累计时长' : 'Combined recorded sessions'}
      />,
    <MetricCard key="inputContextTokens"
        label={t('metrics.inputContextTokens')}
        value={formatNumber(metrics.inputContextTokens?.value)}
        supported={metrics.inputContextTokens?.supported}
        hint="Prompt + Cache read"
      />,
    <MetricCard key="outputTokens"
        label={t('metrics.outputTokens')}
        value={formatNumber(metrics.outputTokens?.value)}
        supported={metrics.outputTokens?.supported}
        hint={zh ? '补全与生成 Token' : 'Completion and generation'}
      />,
    <MetricCard key="cacheHitRate"
        label={t('metrics.cacheHitRate')}
        value={formatPercentage(metrics.cacheHitRate?.value)}
        supported={metrics.cacheHitRate?.supported}
        hint={zh ? '缓存命中占比' : 'Share of cache hits'}
      />,
    <MetricCard key="tokensPerCodeLine"
        label={t('metrics.tokensPerCodeLine')}
        value={metrics.tokensPerCodeLine?.value ? parseFloat(metrics.tokensPerCodeLine.value).toFixed(1) : null}
        supported={metrics.tokensPerCodeLine?.supported}
        hint={zh ? '平均每行代码' : 'Average per line of code'}
      />,
    <MetricCard key="userMessageCount"
        label={t('metrics.userMessageCount')}
        value={formatNumber(metrics.userMessageCount?.value)}
        supported={metrics.userMessageCount?.supported}
        hint={zh ? '用户触发的消息' : 'User-initiated messages'}
      />
  ];
  const available = (card: typeof cards[number]) => card.props.supported !== false && card.props.value != null && card.props.value !== '—';
  const shown = compact ? cards.filter(available) : cards;
  const missing = compact ? cards.filter(card => !available(card)) : [];
  return <>
    {shown.length > 0 && <div className={`metric-grid-10 ${compact ? 'metric-grid-compact' : ''}`} aria-label={t('dashboard.coreMetricsLabel')}>{shown}</div>}
    {compact && !shown.length && <p className="public-metrics-empty">{zh ? '暂无可展示的公开指标。' : 'No public metrics available yet.'}</p>}
    {missing.length > 0 && <details className="unavailable-metrics">
      <summary>{zh ? `更多指标（${missing.length} 项暂无公开数值）` : `More metrics (${missing.length} unavailable)`}</summary>
      <p>{zh ? '缺失值不代表用量为 0。这里只展示公开资料提供的数据，无法据此判断其他指标是否已同步或受来源支持。' : 'Missing values do not mean zero usage. This page only shows data provided by the public profile; sync status and source support are not available here.'}</p>
      <dl>{missing.map(card => <div key={card.key}><dt>{card.props.label}</dt><dd>{card.key === 'totalTokens' && tokenHidden ? (zh ? '未公开' : 'Not shared') : (zh ? '公开资料暂未提供' : 'Not provided by this profile')}</dd></div>)}</dl>
    </details>}
  </>;
};
