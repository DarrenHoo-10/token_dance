import React, { useEffect } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { teamsApi, type SharingFlags, type Team, type TeamRole, type TeamScope } from '@/api/teams';
import { Badge } from '@/components/common/Badge';
import { Switch } from '@/components/common/Switch';
import { useLocale } from '@/context/LocaleContext';
import { TeamDateField } from './TeamDateField';
import { getApiErrorMessage } from '@/i18n';
import { UserAvatar } from '@/components/common/UserAvatar';
import { teamAvatarUrl } from '@/utils/avatar';
import {
  TEAM_RANGE_MAX_DAYS,
  calendarDateInTimeZone,
  firstGrapheme,
  inclusiveDaySpan,
  rankPageCount,
  sha256Hex,
  TEAM_RANK_PAGE_SIZE,
} from './teamUtils';
import './teams.css';
import './sky-team.css';

export function teamErrorMessage(t: (key: string, params?: Record<string, string | number>) => string, error: ApiError): string {
  const byTeams = t(`teams.errors.${error.code}`);
  if (byTeams !== `teams.errors.${error.code}`) return byTeams;
  return getApiErrorMessage(t, error);
}

export async function persistTeamAvatar(scope: TeamScope, file: File): Promise<TeamScope> {
  const hash = await sha256Hex(await file.arrayBuffer());
  const intent = await teamsApi.createAvatarUploadIntent(scope.team.id, {
    contentType: file.type,
    byteSize: file.size,
    sha256: hash,
  });
  await teamsApi.uploadAvatarContent(scope.team.id, intent.objectId, file);
  return teamsApi.completeAvatarUpload(scope.team.id, intent.objectId, {
    expectedProfileVersion: scope.team.profileVersion,
  });
}

export const TeamAvatar: React.FC<{ team: Pick<Team, 'id' | 'name' | 'avatarUrl' | 'profileVersion'>; size?: 'sm' | 'md' | 'lg' }> = ({
  team,
  size = 'md',
}) => {
  const src = teamAvatarUrl(team);
  return (
    <span className={`team-letter-avatar ${size === 'md' ? '' : size}`.trim()} aria-hidden="true">
      {src ? <img src={src} alt="" /> : firstGrapheme(team.name)}
    </span>
  );
};

export const MemberAvatar: React.FC<{ name: string; url?: string | null; size?: 'sm' | 'md' }> = ({
  name,
  url,
  size = 'sm',
}) => (
  <UserAvatar
    url={url}
    name={name}
    className={`team-member-avatar ${size}`}
    fallbackClassName={`team-member-avatar ${size} is-fallback`}
    alt=""
  />
);

export function memberContributionState(_member?: { syncStatus?: string | null; lastReceivedAt?: string | null }): 'joined' | 'waiting' {
  return 'joined';
}

export const RoleBadge: React.FC<{ role: TeamRole }> = ({ role }) => {
  const { t } = useLocale();
  return <Badge variant={role === 'owner' ? 'lime' : role === 'admin' ? 'good' : 'default'}>{t(`teams.role.${role}`)}</Badge>;
};

export const AnalysisSkeleton: React.FC<{ message?: string }> = ({ message }) => {
  const { t } = useLocale();
  return (
    <div className="team-skeleton" data-testid="analysis-skeleton" aria-busy="true" aria-live="polite">
      <p className="text-muted" style={{ fontSize: 13 }}>
        {message || t('teams.analytics.updating')}
      </p>
      <div className="team-metric-grid-4">
        <div className="team-skeleton-card" />
        <div className="team-skeleton-card" />
        <div className="team-skeleton-card" />
        <div className="team-skeleton-card" />
      </div>
      <div className="team-skeleton-card" style={{ minHeight: 220 }} />
    </div>
  );
};

export const SharingControls: React.FC<{
  value: SharingFlags;
  onChange: (next: SharingFlags) => void;
  disabled?: boolean;
  revealDetailsWithBase?: boolean;
}> = ({ value, onChange, disabled = false, revealDetailsWithBase = true }) => {
  const { t } = useLocale();
  const showDetails = !revealDetailsWithBase || value.base;

  const setBase = (base: boolean) => {
    onChange(base ? { ...value, base: true, named: true } : { base: false, named: false, classification: false, cost: false });
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <Switch
        checked={value.base}
        disabled={disabled}
        onChange={setBase}
        label={t('teams.sharing.base')}
        description={t('teams.sharing.baseHint')}
      />
      {showDetails && (
        <>
          <Switch
            checked={value.classification}
            disabled={disabled || !value.base}
            onChange={(classification) => onChange({ ...value, classification, named: true })}
            label={t('teams.sharing.classification')}
            description={t('teams.sharing.classificationHint')}
          />
          <Switch
            checked={value.cost}
            disabled={disabled || !value.base}
            onChange={(cost) => onChange({ ...value, cost, named: true })}
            label={t('teams.sharing.cost')}
            description={t('teams.sharing.costHint')}
          />
        </>
      )}
    </div>
  );
};

export const TeamDateRangeBar: React.FC<{ timezone: string }> = ({ timezone }) => {
  const { t, locale } = useLocale();
  const [params, setParams] = useSearchParams();
  const range = params.get('range') || 'today';
  const from = params.get('from') || '';
  const to = params.get('to') || '';
  const spanError = range === 'custom' && from && to && inclusiveDaySpan(from, to) > TEAM_RANGE_MAX_DAYS;
  const today = calendarDateInTimeZone(new Date(), timezone);
  const displayedFrom = from;
  const displayedTo = to || today;

  const setRange = (next: string) => {
    const nextParams = new URLSearchParams(params);
    nextParams.set('range', next);
    nextParams.delete('from');
    nextParams.delete('to');
    setParams(nextParams, { replace: true });
  };

  const setDate = (key: 'from' | 'to', value: string) => {
    const nextParams = new URLSearchParams(params);
    nextParams.set('range', 'custom');
    if (value) nextParams.set(key, value);
    else nextParams.delete(key);
    const nextFrom = key === 'from' ? value : (nextParams.get('from') || from);
    let nextTo = key === 'to' ? value : (nextParams.get('to') || to);
    if (nextFrom && !nextTo) {
      nextTo = today;
      nextParams.set('to', today);
    }
    if (nextFrom && nextTo && nextTo < nextFrom) {
      nextParams.set('to', nextFrom);
    }
    setParams(nextParams, { replace: true });
  };

  useEffect(() => {
    if (range !== 'custom' || !from || to) return;
    const nextParams = new URLSearchParams(params);
    nextParams.set('to', today);
    setParams(nextParams, { replace: true });
  }, [from, params, range, setParams, to, today]);

  return (
    <>
      <div className="tw-periods" role="tablist" aria-label={t('dashboard.timeRangeSelector')}>
        {[
          { key: 'today', label: t('common.today') },
          { key: '7d', label: t('teams.range.days7') },
          { key: '30d', label: t('teams.range.days30') },
          { key: 'custom', label: t('common.custom') },
        ].map((item) => (
          <button
            key={item.key}
            type="button"
            role="tab"
            aria-selected={range === item.key}
            aria-pressed={range === item.key}
            onClick={() => setRange(item.key)}
          >
            {item.label}
          </button>
        ))}
      </div>
      {range === 'custom' ? (
        <form className="tw-custom-range" onSubmit={(event) => event.preventDefault()}>
          <div className="team-date-fields">
            <TeamDateField
              value={displayedFrom}
              onChange={(next) => setDate('from', next)}
              label={t('teams.range.from')}
              max={today}
              locale={locale}
              invalid={Boolean(spanError)}
              align="start"
            />
            <span className="team-date-arrow" aria-hidden="true">—</span>
            <TeamDateField
              value={displayedTo}
              onChange={(next) => setDate('to', next)}
              label={t('teams.range.to')}
              min={from || undefined}
              max={today}
              locale={locale}
              invalid={Boolean(spanError)}
              align="end"
            />
          </div>
          {spanError ? <p className="team-date-hint is-error" role="alert">{t('teams.range.tooLong')}</p> : null}
        </form>
      ) : null}
    </>
  );
};

export function useTeamSearchFilters() {
  const [params, setParams] = useSearchParams();
  const range = params.get('range') || 'today';
  const from = params.get('from') || undefined;
  const to = params.get('to') || undefined;
  const agent = params.get('agent') || undefined;
  const provider = params.get('provider') || undefined;
  const model = params.get('model') || undefined;

  const setFilter = (key: string, value?: string) => {
    const next = new URLSearchParams(params);
    if (!value || value === 'all') next.delete(key);
    else next.set(key, value);
    if (key === 'agent') {
      next.delete('model');
    }
    setParams(next, { replace: true });
  };

  return { range, from, to, agent, provider, model, setFilter, search: params.toString() };
}

export const TeamGateLink: React.FC<{ to: string; children: React.ReactNode }> = ({ to, children }) => (
  <Link className="btn btn-dark" to={to}>
    {children}
  </Link>
);

export const TeamRankPager: React.FC<{
  page: number;
  total: number;
  onPage: (next: number) => void;
  label: string;
}> = ({ page, total, onPage, label }) => {
  const { t } = useLocale();
  const pages = rankPageCount(total);
  if (total <= TEAM_RANK_PAGE_SIZE) return null;
  const safe = Math.min(Math.max(1, page), pages);
  return (
    <nav className="team-rank-pager" aria-label={label}>
      <span>{t('teams.insights.pageStatus', { page: safe, pages })}</span>
      <div>
        <button type="button" className="btn btn-sm" disabled={safe <= 1} onClick={() => onPage(safe - 1)}>
          {t('teams.insights.prevPage')}
        </button>
        <button type="button" className="btn btn-sm" disabled={safe >= pages} onClick={() => onPage(safe + 1)}>
          {t('teams.insights.nextPage')}
        </button>
      </div>
    </nav>
  );
};
