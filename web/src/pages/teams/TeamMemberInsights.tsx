import React, { useMemo, useState } from 'react';
import { Activity, ArrowRight, ArrowUpRight, UsersRound } from 'lucide-react';
import { useNavigate } from 'react-router-dom';
import type { AnalysisTrendPoint, TeamAnalysisReady } from '@/api/teams';
import { useLocale } from '@/context/LocaleContext';
import { MemberAvatar, TeamRankPager } from './TeamShared';
import { SkyTeamTrend } from './SkyTeamTrend';
import { formatTokenCompact, formatTokenExact, rankPageSlice, TEAM_RANK_PAGE_SIZE } from './teamUtils';
import { usageColor } from '@/utils/usageColors';

const TEAM_SERIES_ID = '__team__';
const TEAM_COLOR = '#86bc43';
const integer = (value?: string | null) => /^\d+$/.test(value || '') ? BigInt(value!) : 0n;

export function memberPercent(value: string, total: string): number {
  const denominator = integer(total);
  return denominator > 0n ? Number(integer(value) * 10000n / denominator) / 100 : 0;
}


function datePosition(dates: string[], index: number): number {
  if (dates.length === 1) return 0.5;
  const instant = (value: string) => Date.parse(value.length === 10 ? `${value}T00:00:00Z` : value);
  const start = instant(dates[0]);
  const end = instant(dates[dates.length - 1]);
  const current = instant(dates[index]);
  return Number.isFinite(current) && end > start ? (current - start) / (end - start) : index / (dates.length - 1);
}

function axisLabel(value: string, hourly: boolean, timezone: string): string {
  if (!hourly) return value.slice(5).replace('-', '/');
  return new Intl.DateTimeFormat('en-GB', { timeZone: timezone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(value));
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
  hourly?: boolean;
  timezone?: string;
}> = ({ dates, series, empty, noneSelected, ariaLabel, skipEmpty = false, missingHint, hourly = false, timezone = 'UTC' }) => {
  if (series.length === 0) {
    return <p className="team-chart-empty">{noneSelected}</p>;
  }
  const axisDates = dates.length ? dates : [...new Set(series.flatMap((item) => (item.trend || []).map((point) => point.date)))].sort();
  const usableById = new Map(series.map((item) => [item.id, skipEmpty ? availableTrend(item.trend) : (item.trend || [])]));
  const drawnSeries = series.filter((item) => (usableById.get(item.id) || []).length > 0);
  if (axisDates.length === 0) {
    return <p className="team-chart-empty">{empty}</p>;
  }
  if (drawnSeries.length === 0) {
    return <>
      <p className="team-chart-empty">{empty}</p>
      <div className="team-trend-dates" aria-label={ariaLabel}>
        {dateTicks(axisDates).map(({ date, position }) => (
          <span key={date} style={{ left: `${position}%` }} title={date}>{axisLabel(date, hourly, timezone)}</span>
        ))}
      </div>
      {skipEmpty && missingHint && <p className="team-trend-hint">{missingHint}</p>}
    </>;
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
                const instant = (value: string) => Date.parse(hourly ? value : `${value}T00:00:00Z`);
                if (current.length && instant(point.date) - instant(current[current.length - 1].date) > (hourly ? 3_600_000 : 86_400_000)) {
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
                  <title>{`${item.label} · ${axisLabel(point.date, hourly, timezone)} · ${formatTokenExact(point.value.toString())}`}</title>
                </circle>
              ))}
            </g>
          );
        })}
      </svg>
      <div className="team-line-scale"><span>0</span></div>
      <div className="team-trend-dates" aria-label={ariaLabel}>
        {dateTicks(axisDates).map(({ date, position }) => (
          <span key={date} style={{ left: `${position}%` }} title={date}>{axisLabel(date, hourly, timezone)}</span>
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

export const TeamMemberInsights: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t, locale } = useLocale();
  const navigate = useNavigate();
  const hourly = analysis.trendGrain === 'hour';
  const timezone = analysis.range.timezone;
  const members = analysis.contributions.items;
  const knownIds = useMemo(() => new Set([TEAM_SERIES_ID, ...members.map((member) => member.membershipId)]), [members]);
  const [detailPage, setDetailPage] = useState(1);
  const [chartMode, setChartMode] = useState<'tokens' | 'efficiency'>('tokens');
  const [compare, setCompare] = useState<string[]>([]);
  const [sort, setSort] = useState<'tokens' | 'days' | 'efficiency'>('tokens');
  const colorFor = (id: string) => {
    if (id === TEAM_SERIES_ID) return TEAM_COLOR;
    return usageColor(id);
  };
  const keepKnown = (ids: string[]) => ids.filter((id) => knownIds.has(id));
  const usageSelected = [TEAM_SERIES_ID, ...keepKnown(compare)];
  const efficiencySelected = [TEAM_SERIES_ID, ...keepKnown(compare)];
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
  const sortedMembers = useMemo(() => {
    return [...members].sort((a, b) => {
      if (sort === 'days') return Number(b.activeDays || 0) - Number(a.activeDays || 0);
      if (sort === 'efficiency') {
        const left = integer(a.tokensPerCodeLine);
        const right = integer(b.tokensPerCodeLine);
        return left === right ? a.displayName.localeCompare(b.displayName) : (left > right ? -1 : 1);
      }
      const left = integer(a.tokens.value);
      const right = integer(b.tokens.value);
      return left === right ? a.displayName.localeCompare(b.displayName) : (left > right ? -1 : 1);
    });
  }, [members, sort]);
  const leaders = sortedMembers.filter((member) => integer(member.tokens.value) > 0n);
  const shown = leaders.reduce((sum, member) => sum + integer(member.tokens.value), 0n);
  const remainder = integer(total) > shown ? integer(total) - shown : 0n;
  const donutStops = [
    ...leaders.map((member, index) => {
      const start = leaders.slice(0, index).reduce((sum, item) => sum + memberPercent(item.tokens.value || '0', total), 0);
      const end = start + memberPercent(member.tokens.value || '0', total);
      return `${colorFor(member.membershipId)} ${start}% ${end}%`;
    }),
    remainder > 0n ? `#d6ddd1 ${leaders.reduce((sum, item) => sum + memberPercent(item.tokens.value || '0', total), 0)}% 100%` : '',
  ].filter(Boolean).join(', ');
  const detailRows = rankPageSlice(sortedMembers, detailPage);

  const visibleMembers = members.filter((member) => integer(member.tokens.value) > 0n);
  const toggleCompare = (id: string) => setCompare((ids) => ids.includes(id) ? ids.filter((item) => item !== id) : [...ids, id]);

  return (
    <>
      <div className="tw-chart-grid">
        <section className={`tw-card tw-usage-card ${chartMode === 'tokens' ? 'team-member-trend' : 'team-efficiency-trend'}`}>
          <div className="tw-card-heading">
            <div>
              <h2><Activity size={20} />{t('teams.overview.trendTitle')}</h2>
              <p>{chartMode === 'tokens' ? t('teams.overview.trendHint') : t('teams.overview.efficiencyHint')}</p>
            </div>
            <div className="tw-mini-tabs">
              <button type="button" aria-pressed={chartMode === 'tokens'} onClick={() => setChartMode('tokens')}>Token</button>
              <button type="button" aria-pressed={chartMode === 'efficiency'} onClick={() => setChartMode('efficiency')}>{t('teams.metrics.tokensPerLine')}</button>
            </div>
          </div>
          {chartMode === 'tokens' ? (
            <SkyTeamTrend
              dates={usageDates}
              series={usageSeries.map((item) => ({
                id: item.id,
                name: item.label,
                color: item.color,
                values: usageDates.map((date) => {
                  const point = (item.trend || []).find((entry) => entry.date === date);
                  return Number(integer(point?.tokens.value));
                }),
              }))}
              en={locale !== 'zh-CN'}
              empty={t('teams.overview.emptyRange')}
              hourly={hourly}
              timezone={timezone}
            />
          ) : (
            <div className="tw-trend">
              <MultiTrendChart
                dates={efficiencyDates}
                series={efficiencySeries}
                empty={t('teams.overview.emptyRange')}
                noneSelected={t('teams.insights.selectSeries')}
                ariaLabel={t('teams.overview.trendTitle')}
                skipEmpty
                missingHint={t('teams.insights.efficiencyMissingHint')}
                hourly={hourly}
                timezone={timezone}
              />
            </div>
          )}
          {hourly && analysis.hourlyTrendPartial && chartMode === 'tokens' && <p className="team-trend-hint">{t('teams.insights.hourlyPartialHint')}</p>}
          <div className="tw-compare">
            <span>{t('teams.overview.compareMembers')}</span>
            <span className="tw-team-legend"><i />{t('teams.insights.teamSeries')}</span>
            {visibleMembers.map((member) => (
              <button
                key={member.membershipId}
                type="button"
                aria-pressed={compare.includes(member.membershipId)}
                style={{ ['--member-color' as string]: colorFor(member.membershipId) }}
                onClick={() => toggleCompare(member.membershipId)}
              >
                <i />{member.displayName}
              </button>
            ))}
          </div>
        </section>
        <section className="tw-card tw-share-card">
          <div className="tw-card-heading">
            <div>
              <h2>{t('teams.overview.donutTitle')}</h2>
              <p>{t('teams.overview.donutHint')}</p>
            </div>
            <UsersRound size={19} />
          </div>
          {integer(total) > 0n ? (
            <>
              <div className="tw-donut" role="img" aria-label={t('teams.overview.donutHint')} style={{ background: `conic-gradient(${donutStops})` }}>
                <div>
                  <span>{t('teams.overview.activeBuilders')}</span>
                  <strong>{analysis.summary.activeMembers || visibleMembers.length}<small>{t('teams.overview.people')}</small></strong>
                  <span>{formatTokenCompact(total)} Token</span>
                </div>
              </div>
              <div className="tw-share-list">
                {leaders.map((member) => (
                  <button type="button" key={member.membershipId}>
                    <i style={{ background: colorFor(member.membershipId) }} />
                    <span>{member.displayName}</span>
                    <strong>{memberPercent(member.tokens.value || '0', total).toFixed(1)}%</strong>
                    <ArrowUpRight size={12} />
                  </button>
                ))}
                {remainder > 0n && (
                  <button type="button">
                    <i style={{ background: '#d6ddd1' }} />
                    <span>{t('teams.insights.others')}</span>
                    <strong>{memberPercent(remainder.toString(), total).toFixed(1)}%</strong>
                  </button>
                )}
              </div>
            </>
          ) : <p className="tw-muted">{t('teams.overview.noContributions')}</p>}
        </section>
      </div>

      <section className="tw-card tw-contributions">
        <div className="tw-card-heading">
          <div>
            <h2><UsersRound size={20} />{t('teams.overview.contributions')}</h2>
            <p>{t('teams.overview.contributionHint')}</p>
          </div>
          <label className="tw-sort">
            <span>{t('teams.overview.sort')}</span>
            <select aria-label={t('teams.overview.sort')} value={sort} onChange={(e) => { setSort(e.target.value as 'tokens' | 'days' | 'efficiency'); setDetailPage(1); }}>
              <option value="tokens">Token</option>
              <option value="days">{t('teams.insights.activeDays')}</option>
              <option value="efficiency">{t('teams.metrics.tokensPerLine')}</option>
            </select>
          </label>
        </div>
        {members.length ? (
          <>
            <div className="tw-table-scroll">
              <table className="tw-table">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>{t('teams.insights.member')}</th>
                    <th>Token / {t('teams.overview.share')}</th>
                    <th>{t('teams.metrics.codeLines')}</th>
                    <th>{t('teams.metrics.tokensPerLine')}</th>
                    <th>{t('teams.insights.activeDays')}</th>
                    <th>{t('teams.insights.periodTrend')}</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {detailRows.map((member, index) => {
                    const memberMax = (member.trend || []).reduce((value, point) => integer(point.tokens.value) > value ? integer(point.tokens.value) : value, 1n);
                    const points = pointsFor(member.trend, rankingDates, memberMax, 90, 30);
                    const share = integer(total) > 0n ? memberPercent(member.tokens.value || '0', total) : 0;
                    const rank = String((detailPage - 1) * TEAM_RANK_PAGE_SIZE + index + 1).padStart(2, '0');
                    return (
                      <tr key={member.membershipId}>
                        <td className="tw-rank">{rank}</td>
                        <td>
                          <span className="tw-person">
                            <MemberAvatar name={member.displayName} url={member.avatarUrl} />
                            <span>
                              <strong>{member.displayName}</strong>
                              <small>{member.handle ? `@${member.handle}` : ''}</small>
                            </span>
                          </span>
                        </td>
                        <td>
                          <strong>{member.tokens.state === 'available' ? formatTokenCompact(member.tokens.value || '0') : '—'}</strong>
                          <div className="tw-share-meter">
                            <i style={{ width: `${share}%`, background: colorFor(member.membershipId) }} />
                            <span>{share.toFixed(1)}%</span>
                          </div>
                        </td>
                        <td>{member.generatedCodeLines ? formatTokenCompact(member.generatedCodeLines) : '—'}</td>
                        <td>{member.tokensPerCodeLine ? formatTokenCompact(member.tokensPerCodeLine) : '—'}</td>
                        <td><b>{member.activeDays ?? (member.trend ? member.trend.filter((point) => integer(point.tokens.value) > 0n).length : '—')}</b></td>
                        <td>
                          {member.trend && points.length ? (
                            <svg className="tw-mini-trend" viewBox="0 0 90 30" aria-hidden="true">
                              <path d={points.map((point, i) => `${i ? 'L' : 'M'}${point.x * 90 / 100},${point.y}`).join(' ')} fill="none" stroke={colorFor(member.membershipId)} strokeWidth="1.8" strokeLinejoin="round" />
                            </svg>
                          ) : '—'}
                        </td>
                        <td>
                          <button type="button" className="icon-button" aria-label={member.displayName} onClick={() => navigate('../members')}>
                            <ArrowUpRight size={16} />
                          </button>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>
            <TeamRankPager page={detailPage} total={members.length} onPage={setDetailPage} label={t('teams.insights.memberDetailPages')} />
            <div className="tw-table-footer">
              {t('teams.overview.contributionFoot')}
              <button type="button" onClick={() => navigate('../members')}>{t('teams.overview.manageMembers')}<ArrowRight size={14} /></button>
            </div>
          </>
        ) : <p className="tw-muted">{t('teams.overview.noContributions')}</p>}
      </section>
    </>
  );
};
