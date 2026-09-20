import type { PersonalSummary } from '@/types/api';

export function hasRankedTokens(value: string | number | null | undefined) {
  return Number.isFinite(Number(value)) && Number(value) > 0;
}

export function personalTokenRank(summary: PersonalSummary | null) {
  // The ranking entry describes the ranking period even when the metrics
  // panel is showing a different period. Prefer it over the panel total.
  if (!summary) return null;
  const value = summary.ranking.entry?.metricValue ?? (summary.metrics.totalTokens.supported ? summary.metrics.totalTokens.value : null);
  return hasRankedTokens(value) && Number.isFinite(summary.ranking.rank) && (summary.ranking.rank ?? 0) > 0 ? summary.ranking.rank : null;
}
