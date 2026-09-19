import React from 'react';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { ArrowUpRight, BarChart3, Code2, Layers3, UsersRound, Wallet } from 'lucide-react';
import { ChangeBadge } from '@/components/common/ChangeBadge';
import { useLocale } from '@/context/LocaleContext';
import type { MetricValue, TeamAnalysisReady } from '@/api/teams';
import {
  formatDecimalAmount,
  formatDurationHours,
  formatRatePercent,
  formatTokenCompact,
  metricDisplay,
} from './teamUtils';
import { TeamMemberInsights } from './TeamMemberInsights';
import { TeamUsageMix } from './TeamUsageMix';

function metricOf(analysis: TeamAnalysisReady, key: string): MetricValue | undefined {
  return analysis.summary.metrics?.[key];
}

function dash(value: string | null | undefined): string {
  return value || '—';
}

export const TeamOverviewBoard: React.FC<{
  analysis: TeamAnalysisReady;
  teamId: string;
  authRevision: string | null;
}> = ({ analysis, teamId, authRevision }) => {
  const { t, locale } = useLocale();
  const navigate = useNavigate();
  const [, setParams] = useSearchParams();
  const tokens = metricDisplay(analysis.summary.tokens);
  const estimated = analysis.costs.estimatedUncovered || [];
  const estimatedMetric = metricDisplay(metricOf(analysis, 'estimatedCosts'), (value) => formatDecimalAmount(value, 'USD'));
  const estimatedText = estimated.length
    ? estimated.map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ')
    : dash(estimatedMetric.available ? estimatedMetric.text : null);
  const namedTokens = analysis.summary.currentMemberTokens || analysis.summary.tokens.value || '0';
  const activeMembers = analysis.summary.activeMembers || '0';
  const currentMembers = analysis.summary.currentMembers || '0';
  const avgTokens = analysis.summary.tokens.state === 'available' && BigInt(activeMembers) > 0n
    ? formatTokenCompact((BigInt(namedTokens || '0') / BigInt(activeMembers)).toString())
    : null;
  const code = metricDisplay(metricOf(analysis, 'generatedCodeLines'));
  const perLine = metricDisplay(metricOf(analysis, 'tokensPerCodeLine'), (value) => formatTokenCompact(value));
  const input = metricDisplay(metricOf(analysis, 'inputContextTokens'));
  const output = metricDisplay(metricOf(analysis, 'outputTokens'));
  const cache = metricDisplay(metricOf(analysis, 'cacheHitRate'), (value) => formatRatePercent(value) || '—');
  const duration = metricDisplay(metricOf(analysis, 'activeDurationMs'), (value) => formatDurationHours(value) || '—');
  const messages = metricDisplay(metricOf(analysis, 'messageCount'));
  const userMessages = metricDisplay(metricOf(analysis, 'userMessageCount'));
  const change = analysis.summary.comparison?.tokensDeltaPct;
  const delta = change == null ? null : Number(change);
  const reported = (analysis.costs.reported || []).map((item) => formatDecimalAmount(item.amount, item.currency)).join(' / ') || '—';
  const empty = analysis.summary.tokens.state === 'empty';
  const details = [
    [t('teams.metrics.avgTokens'), dash(avgTokens)],
    [t('teams.overview.inputTokens'), input.available ? input.text : '—'],
    [t('teams.metrics.output'), output.available ? output.text : '—'],
    [t('teams.overview.cacheTokens'), cache.available ? cache.text : '—'],
    [t('teams.metrics.duration'), duration.available ? duration.text : '—'],
    [t('teams.overview.messagesUser'), `${messages.available ? messages.text : '—'} / ${userMessages.available ? userMessages.text : '—'}`],
  ];

  return (
    <>
      {analysis.quality?.includesHistoricalUsers && <p className="tw-muted">{t('teams.overview.historicalNote')}</p>}
      <div className="tw-kpi-grid">
        <section className="tw-kpi tw-kpi-main">
          <span><Layers3 size={16} />{t('teams.metrics.tokens')}</span>
          <div>
            <strong data-testid="team-total">{tokens.available ? tokens.text : '—'}</strong>
            {tokens.available && delta !== null && Number.isFinite(delta) && <ChangeBadge value={Number(delta.toFixed(1))} en={locale !== 'zh-CN'} />}
          </div>
          <p>
            {delta !== null ? t('teams.overview.selectedPeriod') : t('teams.overview.noCompare')}
            <span className="tw-kpi-decoration" aria-hidden="true">↗</span>
          </p>
        </section>
        <section className="tw-kpi">
          <span><Wallet size={16} />{t('teams.metrics.recordedCost')}</span>
          <div><strong>{reported === '—' ? '—' : reported.replace(/ USD$/, '')}</strong><small>USD</small></div>
          <p>{t('teams.overview.uncoveredEstimate')} <b>{estimatedText}</b></p>
        </section>
        <section className="tw-kpi">
          <span><UsersRound size={16} />{t('teams.metrics.activeMembers')}</span>
          <div>
            <strong>{activeMembers}<small> / {currentMembers}</small></strong>
            <span className="tw-live-label"><i />{t('teams.overview.creating')}</span>
          </div>
          <p>
            {currentMembers} {t('teams.overview.sharingBasic')}
            <button type="button" className="tw-inline-arrow" onClick={() => navigate(`/teams/${teamId}/members`)} aria-label={t('teams.overview.manageMembers')}>
              <ArrowUpRight size={15} />
            </button>
          </p>
        </section>
        <section className="tw-kpi">
          <span><Code2 size={17} />{t('teams.metrics.codeLines')}</span>
          <div>
            <strong>{code.available ? code.text : '—'}</strong>
            <small>{t('teams.overview.linesUnit')}</small>
          </div>
          <p>{t('teams.metrics.tokensPerLine')} <b>{perLine.available ? perLine.text : '—'}</b></p>
        </section>
      </div>
      <div className="tw-token-breakdown">
        {details.map(([label, value]) => (
          <div key={label}><span>{label}</span><strong className="mono-num">{value}</strong></div>
        ))}
      </div>
      {empty ? (
        <section className="tw-card tw-no-data">
          <BarChart3 size={36} />
          <h2>{t('teams.overview.noActivity')}</h2>
          <p>{t('teams.overview.tryOther')}</p>
          <button
            type="button"
            className="button secondary"
            onClick={() => {
              const next = new URLSearchParams();
              next.set('range', '30d');
              setParams(next, { replace: true });
            }}
          >
            {t('teams.overview.showAll')}
          </button>
        </section>
      ) : (
        <>
          <TeamMemberInsights key={`${teamId}:${authRevision || ''}`} analysis={analysis} />
          <TeamUsageMix analysis={analysis} />
        </>
      )}
    </>
  );
};
