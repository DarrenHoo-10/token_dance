import React, { useEffect, useMemo, useRef, useState } from 'react';
import type { AnalysisTrendPoint, ContributionItem, TeamAnalysisReady } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { useLocale } from '@/context/LocaleContext';
import { TeamRankPager } from './TeamShared';
import { firstGrapheme, formatTokenCompact, formatTokenExact, metricDisplay, rankPageCount, rankPageSlice, TEAM_RANK_PAGE_SIZE } from './teamUtils';

const COLORS = ['#577d21', '#277d96', '#8668a6', '#bc7939', '#bb5275'];
const TEAM_SERIES_ID = '__team__';
const TEAM_COLOR = '#2f3b24';
const integer = (value?: string | null) => /^\d+$/.test(value || '') ? BigInt(value!) : 0n;

export function memberPercent(value: string, total: string): number {
  const denominator = integer(total);
  return denominator > 0n ? Number(integer(value) * 10000n / denominator) / 100 : 0;
}

function defaultSelectedIds(members: ContributionItem[]): string[] {
  return [TEAM_SERIES_ID, ...members.slice(0, 3).map((member) => member.membershipId)];
}

function datePosition(dates: string[], index: number): number {
  if (dates.length === 1) return 0.5;
  const start = Date.parse(`${dates[0]}T00:00:00Z`);
  const end = Date.parse(`${dates[dates.length - 1]}T00:00:00Z`);
  const current = Date.parse(`${dates[index]}T00:00:00Z`);
  return Number.isFinite(current) && end > start ? (current - start) / (end - start) : index / (dates.length - 1);
}

function pointsFor(trend: AnalysisTrendPoint[] | undefined, dates: string[], max: bigint, width = 720, height = 190) {
  const values = new Map((trend || []).map(point => [point.date, integer(point.tokens.value)]));
  const padding = Math.min(12, height * 0.16);
  return dates.map((date, index) => {
    const value = values.get(date) || 0n;
    return { date, value, x: 12 + datePosition(dates, index) * (width - 24), y: height - padding - Number(value * 10000n / (max || 1n)) / 10000 * (height - padding * 2) };
  });
}

function availableTrend(trend: AnalysisTrendPoint[] | undefined): AnalysisTrendPoint[] {
  return (trend || []).filter((point) => point.tokens?.state === 'available' && point.tokens.value != null);
}

function dateTicks(dates: string[]): { date: string; position: number }[] {
  if (dates.length === 0) return [];
  const step = Math.max(1, Math.ceil((dates.length - 1) / 6));
  const indices = new Set<number>([0, dates.length - 1]);
  for (let index = step; index < dates.length - 1; index += step) indices.add(index);
  return [...indices].sort((a, b) => a - b).map((index) => ({
    date: dates[index],
    position: 100 * datePosition(dates, index),
  }));
}

type ChartSeries = {
  id: string;
  label: string;
  color: string;
  trend: AnalysisTrendPoint[] | undefined;
};

const MultiTrendChart: React.FC<{
  dates: string[];
  series: ChartSeries[];
  empty: string;
  noneSelected: string;
  ariaLabel: string;
  skipEmpty?: boolean;
  missingHint?: string;
}> = ({ dates, series, empty, noneSelected, ariaLabel, skipEmpty = false, missingHint }) => {
  if (series.length === 0) {
    return <p className="team-chart-empty">{noneSelected}</p>;
  }
  const axisDates = dates.length ? dates : [...new Set(series.flatMap((item) => (item.trend || []).map((point) => point.date)))].sort();
  const usableById = new Map(series.map((item) => [item.id, skipEmpty ? availableTrend(item.trend) : (item.trend || [])]));
  const drawnSeries = series.filter((item) => (usableById.get(item.id) || []).length > 0);
  if (axisDates.length === 0 || drawnSeries.length === 0) {
    return <p className="team-chart-empty">{empty}</p>;
  }
  const max = drawnSeries.reduce((value, item) => {
    return (usableById.get(item.id) || []).reduce((current, point) => {
      const next = integer(point.tokens.value);
      return next > current ? next : current;
    }, value);
  }, 1n);
  return (
    <>
      <div className="team-line-scale"><span>{formatTokenCompact(max.toString())}</span></div>
      <svg className="team-multiline-chart" viewBox="0 0 720 190" preserveAspectRatio="none" role="img" aria-label={ariaLabel}>
        {[12, 95, 178].map((y) => <line key={y} x1="12" x2="708" y1={y} y2={y} stroke="#e6ebe4" strokeDasharray="4 5" />)}
        {drawnSeries.map((item) => {
          const usable = usableById.get(item.id) || [];
          const chartPoints = pointsFor(skipEmpty ? usable : item.trend, axisDates, max);
          const availableDates = new Set(usable.map((point) => point.date));
          const drawn = skipEmpty ? chartPoints.filter((point) => availableDates.has(point.date)) : chartPoints;
          const segments: typeof chartPoints[] = [];
          if (skipEmpty) {
            let current: typeof chartPoints = [];
            chartPoints.forEach((point) => {
              if (availableDates.has(point.date)) {
                if (current.length && Date.parse(`${point.date}T00:00:00Z`) - Date.parse(`${current[current.length - 1].date}T00:00:00Z`) > 86_400_000) {
                  segments.push(current);
                  current = [];
                }
                current.push(point);
              } else if (current.length) { segments.push(current); current = []; }
            });
            if (current.length) segments.push(current);
          } else segments.push(chartPoints);
          return (
            <g key={item.id}>
              {segments.filter((segment) => segment.length > 1).map((segment) => (
                <polyline key={segment[0].date} points={segment.map((point) => `${point.x},${point.y}`).join(' ')} fill="none" stroke={item.color} strokeWidth="2.5" vectorEffect="non-scaling-stroke" />
              ))}
              {drawn.map((point) => (
                <circle key={`${item.id}:${point.date}`} cx={point.x} cy={point.y} r="3.5" fill={item.color}>
                  <title>{`${item.label} · ${point.date} · ${formatTokenExact(point.value.toString())}`}</title>
                </circle>
              ))}
            </g>
          );
        })}
      </svg>
      <div className="team-line-scale"><span>0</span></div>
      <div className="team-trend-dates" aria-label={ariaLabel}>
        {dateTicks(axisDates).map(({ date, position }) => (
          <span key={date} style={{ left: `${position}%` }} title={date}>{date.slice(5).replace('-', '/')}</span>
        ))}
      </div>
      {skipEmpty && missingHint && <p className="team-trend-hint">{missingHint}</p>}
      <div className="team-series-legend">
        {drawnSeries.map((item) => (
          <span key={item.id}><i className="team-dot" style={{ background: item.color }} aria-hidden="true" />{item.label}</span>
        ))}
      </div>
    </>
  );
};

const SeriesPicker: React.FC<{
  label: string;
  teamLabel: string;
  triggerLabel: string;
  listSep: string;
  members: ContributionItem[];
  selected: string[];
  colorFor: (id: string) => string;
  onChange: (next: string[]) => void;
}> = ({ label, teamLabel, triggerLabel, listSep, members, selected, colorFor, onChange }) => {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const selectedSet = useMemo(() => new Set(selected), [selected]);
  const summary = useMemo(() => {
    const names: string[] = [];
    if (selectedSet.has(TEAM_SERIES_ID)) names.push(teamLabel);
    members.forEach((member) => {
      if (selectedSet.has(member.membershipId)) names.push(member.displayName);
    });
    return names.join(listSep);
  }, [listSep, members, selectedSet, teamLabel]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    const onPointer = (event: PointerEvent) => {
      if (root.current && !root.current.contains(event.target as Node)) setOpen(false);
    };
    document.addEventListener('keydown', onKey);
    document.addEventListener('pointerdown', onPointer);
    return () => {
      document.removeEventListener('keydown', onKey);
      document.removeEventListener('pointerdown', onPointer);
    };
  }, [open]);

  const toggle = (id: string) => {
    if (selectedSet.has(id)) onChange(selected.filter((item) => item !== id));
    else onChange([...selected, id]);
  };

  const options = [
    { id: TEAM_SERIES_ID, label: teamLabel },
    ...members.map((member) => ({ id: member.membershipId, label: member.displayName })),
  ];

  return (
    <div className="team-series-picker" ref={root}>
      <button
        type="button"
        className="team-series-trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={label}
        title={summary || undefined}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="team-series-trigger-label">{triggerLabel}</span>
        <span className="team-series-count" aria-hidden="true">{selected.length}</span>
        <span className="team-series-caret" aria-hidden="true" />
      </button>
      {open && (
        <div className="team-series-menu" role="listbox" aria-multiselectable="true" aria-label={label}>
          {options.map((option) => {
            const on = selectedSet.has(option.id);
            return (
              <button
                key={option.id}
                type="button"
                role="option"
                className="team-series-option"
                aria-selected={on}
                onClick={() => toggle(option.id)}
              >
                <span className={`team-series-check ${on ? 'is-on' : ''}`} aria-hidden="true" />
                <i className="team-dot" style={{ background: colorFor(option.id) }} aria-hidden="true" />
                <span>{option.label}</span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
};

export const TeamMemberInsights: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t } = useLocale();
  const members = analysis.contributions.items;
  const knownIds = useMemo(() => new Set([TEAM_SERIES_ID, ...members.map((member) => member.membershipId)]), [members]);
  const [usageIds, setUsageIds] = useState(() => defaultSelectedIds(members));
  const [efficiencyIds, setEfficiencyIds] = useState(() => defaultSelectedIds(members));
  const [detailPage, setDetailPage] = useState(1);
  const [efficiencyPage, setEfficiencyPage] = useState(1);
  const colorFor = (id: string) => {
    if (id === TEAM_SERIES_ID) return TEAM_COLOR;
    const index = members.findIndex((member) => member.membershipId === id);
    return COLORS[(index < 0 ? 0 : index) % COLORS.length];
  };
  const keepKnown = (ids: string[]) => ids.filter((id) => knownIds.has(id));
  const usageSelected = keepKnown(usageIds);
  const efficiencySelected = keepKnown(efficiencyIds);
  const teamTrend = analysis.trend || [];
  const usageSeries: ChartSeries[] = usageSelected.map((id) => (
    id === TEAM_SERIES_ID
      ? { id, label: t('teams.insights.teamSeries'), color: colorFor(id), trend: teamTrend }
      : { id, label: members.find((member) => member.membershipId === id)?.displayName || id, color: colorFor(id), trend: members.find((member) => member.membershipId === id)?.trend }
  ));
  const efficiencySeries: ChartSeries[] = efficiencySelected.map((id) => (
    id === TEAM_SERIES_ID
      ? { id, label: t('teams.insights.teamSeries'), color: colorFor(id), trend: analysis.efficiencyTrend }
      : { id, label: members.find((member) => member.membershipId === id)?.displayName || id, color: colorFor(id), trend: members.find((member) => member.membershipId === id)?.efficiencyTrend }
  ));
  const usageDates = [...new Set(usageSeries.flatMap((item) => (item.trend || []).map((point) => point.date)))].sort();
  const rankingDates = [...new Set([
    ...teamTrend.map(point => point.date),
    ...members.flatMap(member => (member.trend || []).map(point => point.date)),
  ])].sort();
  const efficiencyDates = [...new Set(efficiencySeries.flatMap((item) => (item.trend || []).map((point) => point.date)))].sort();
  const total = analysis.summary.tokens.value || '0';
  const teamEfficiency = metricDisplay(analysis.summary.metrics?.tokensPerCodeLine, (value) => formatTokenCompact(value));
  const leaders = members.slice(0, 5);
  const shown = leaders.reduce((sum, member) => sum + integer(member.tokens.value), 0n);
  const remainder = integer(total) > shown ? integer(total) - shown : 0n;
  const donutStops = [
    ...leaders.map((member, index) => {
      const start = leaders.slice(0, index).reduce((sum, item) => sum + memberPercent(item.tokens.value || '0', total), 0);
      const end = start + memberPercent(member.tokens.value || '0', total);
      return `${COLORS[index]} ${start}% ${end}%`;
    }),
    remainder > 0n ? `#d6ddd1 ${leaders.reduce((sum, item) => sum + memberPercent(item.tokens.value || '0', total), 0)}% 100%` : '',
  ].filter(Boolean).join(', ');
  const efficiencyRanking = [...members]
    .filter((member) => Boolean(member.tokensPerCodeLine))
    .sort((a, b) => {
      const left = integer(a.tokensPerCodeLine);
      const right = integer(b.tokensPerCodeLine);
      if (left === right) return a.displayName.localeCompare(b.displayName);
      return left > right ? -1 : 1;
    });
  const detailRows = rankPageSlice(members, detailPage);
  const efficiencyRows = rankPageSlice(efficiencyRanking, efficiencyPage);
  const efficiencyRankStart = (Math.min(Math.max(1, efficiencyPage), rankPageCount(efficiencyRanking.length)) - 1) * TEAM_RANK_PAGE_SIZE;

  return <section className="team-people-section" id="members" aria-label={t('teams.overview.contributions')}>
    <div className="team-section-heading">
      <h2>{t('teams.overview.contributions')}</h2>
      <div className="team-token-eff">
        <span className="label">{t('teams.insights.tokenEfficiency')}</span>
        <strong className="mono-num">{teamEfficiency.available ? teamEfficiency.text : '—'}</strong>
        <small>{t('teams.insights.tokenEfficiencyUnit')}</small>
      </div>
    </div>
    <div className="team-people-grid">
      <Card className="team-member-trend">
        <div className="panel-header">
          <h2>{t('teams.insights.memberTrend')}</h2>
          <SeriesPicker
            label={t('teams.insights.chooseUsageSeries')}
            teamLabel={t('teams.insights.teamSeries')}
            triggerLabel={t('teams.insights.compareSeries')}
            listSep={t('teams.insights.listSep')}
            members={members}
            selected={usageSelected}
            colorFor={colorFor}
            onChange={setUsageIds}
          />
        </div>
        <MultiTrendChart
          dates={usageDates}
          series={usageSeries}
          empty={t('teams.overview.emptyRange')}
          noneSelected={t('teams.insights.selectSeries')}
          ariaLabel={t('teams.insights.memberTrend')}
        />
      </Card>
      <Card>
        <div className="panel-header"><div><h2>{t('teams.insights.memberShare')}</h2></div><span className="team-chart-unit">Token</span></div>
        {integer(total) > 0n ? <>
          <div className="team-donut-layout">
            <div className="team-donut" role="img" aria-label={t('teams.insights.memberShare')} style={{ background: `conic-gradient(${donutStops})` }}>
              <div className="team-donut-center">
                <strong className="mono-num">{formatTokenCompact(total)}</strong>
                <span>{t('teams.insights.teamTotal')}</span>
              </div>
            </div>
          </div>
          <div className="team-share-legend two-col">
            {leaders.map((member, index) => <div key={member.membershipId}><i style={{ background: COLORS[index] }} aria-hidden="true" /><span>{member.displayName}</span><strong className="mono-num">{memberPercent(member.tokens.value || '0', total).toFixed(1)}%</strong></div>)}
            {remainder > 0n && <div><i style={{ background: '#d6ddd1' }} aria-hidden="true" /><span>{t('teams.insights.others')}</span><strong>{memberPercent(remainder.toString(), total).toFixed(1)}%</strong></div>}
          </div>
        </> : <p className="team-chart-empty">{t('teams.overview.noContributions')}</p>}
      </Card>
    </div>
    <div className="team-people-grid">
      <Card className="team-efficiency-trend">
        <div className="panel-header">
          <h2>{t('teams.insights.efficiencyTrend')}</h2>
          <SeriesPicker
            label={t('teams.insights.chooseEfficiencySeries')}
            teamLabel={t('teams.insights.teamSeries')}
            triggerLabel={t('teams.insights.compareSeries')}
            listSep={t('teams.insights.listSep')}
            members={members}
            selected={efficiencySelected}
            colorFor={colorFor}
            onChange={setEfficiencyIds}
          />
        </div>
        <MultiTrendChart
          dates={efficiencyDates}
          series={efficiencySeries}
          empty={t('teams.overview.emptyRange')}
          noneSelected={t('teams.insights.selectSeries')}
          ariaLabel={t('teams.insights.efficiencyTrend')}
          skipEmpty
          missingHint={t('teams.insights.efficiencyMissingHint')}
        />
      </Card>
      <Card>
        <div className="panel-header">
          <h2>{t('teams.insights.efficiencyRank')}</h2>
          <span className="team-chart-unit">{t('teams.insights.tokenEfficiencyUnit')}</span>
        </div>
        {efficiencyRanking.length ? <>
          <div className="team-rank-list">
            {efficiencyRows.map((member, index) => {
              const rank = efficiencyRankStart + index + 1;
              return (
                <div className="team-rank-row" key={member.membershipId}>
                  <span className="team-person-rank">{rank}</span>
                  <span className="team-person-avatar" aria-hidden="true">{firstGrapheme(member.displayName)}</span>
                  <span className="team-rank-name">{member.displayName}</span>
                  <strong className="mono-num">{formatTokenCompact(member.tokensPerCodeLine || '0')}</strong>
                </div>
              );
            })}
          </div>
          <TeamRankPager
            page={efficiencyPage}
            total={efficiencyRanking.length}
            onPage={setEfficiencyPage}
            label={t('teams.insights.efficiencyRankPages')}
          />
        </> : <p className="team-chart-empty">{t('teams.overview.emptyRange')}</p>}
      </Card>
    </div>
    <Card>
      <div className="panel-header" id="ranking"><div><h2>{t('teams.insights.detailTitle')}</h2></div><span className="team-chart-unit">{t('teams.insights.rankingHint')}</span></div>
      {members.length ? <>
        <div className="team-table-scroll"><table className="team-contribution-table"><thead><tr><th>{t('teams.insights.member')}</th><th>Token / {t('teams.insights.share')}</th><th>{t('teams.insights.tokenEfficiency')}</th><th>{t('teams.insights.activeDays')}</th><th>{t('teams.insights.periodTrend')}</th></tr></thead><tbody>{detailRows.map(member => {
          const memberMax = (member.trend || []).reduce((value, point) => integer(point.tokens.value) > value ? integer(point.tokens.value) : value, 1n);
          const points = pointsFor(member.trend, rankingDates, memberMax, 100, 30);
          const efficiency = member.tokensPerCodeLine ? formatTokenCompact(member.tokensPerCodeLine) : '—';
          return <tr key={member.membershipId}><td><div className="team-person-cell"><span className="team-person-rank">{member.rank}</span><span className="team-person-avatar" aria-hidden="true">{firstGrapheme(member.displayName)}</span><span><strong>{member.displayName}</strong><small>{member.handle ? `@${member.handle}` : ''}</small></span></div></td><td><span className="share-cell"><span className="mono-num">{member.tokens.state === 'available' ? formatTokenCompact(member.tokens.value || '0') : '—'}</span> <span className="text-muted"> · {integer(total) > 0n ? `${memberPercent(member.tokens.value || '0', total).toFixed(1)}%` : '—'}</span><span className="team-person-share"><div><i style={{ width: `${memberPercent(member.tokens.value || '0', total)}%`, background: colorFor(member.membershipId) }} /></div></span></span></td><td className="mono-num">{efficiency}</td><td>{member.trend ? member.trend.filter(point => integer(point.tokens.value) > 0n).length : '—'}</td><td>{member.trend && points.length ? <svg width="100" height="30" viewBox="0 0 100 30" aria-label={`${member.displayName} ${t('teams.insights.periodTrend')}`} role="img"><polyline points={points.map(point => `${point.x},${point.y}`).join(' ')} stroke={colorFor(member.membershipId)} strokeWidth="2" fill="none" />{points.length === 1 && <circle cx={points[0].x} cy={points[0].y} r="3" fill={colorFor(member.membershipId)} />}</svg> : '—'}</td></tr>;
        })}</tbody></table></div>
        <TeamRankPager
          page={detailPage}
          total={members.length}
          onPage={setDetailPage}
          label={t('teams.insights.memberDetailPages')}
        />
      </> : <p className="team-chart-empty">{t('teams.overview.noContributions')}</p>}
    </Card>
  </section>;
};
