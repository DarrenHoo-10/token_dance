import React from 'react';
import { ArrowRight } from 'lucide-react';
import { Link, useSearchParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import type { SharingFlags, Team, TeamRole } from '@/api/teams';
import { Badge } from '@/components/common/Badge';
import { Switch } from '@/components/common/Switch';
import { useLocale } from '@/context/LocaleContext';
import { TeamDateField } from './TeamDateField';
import { getApiErrorMessage } from '@/i18n';
import { avatarUrl } from '@/utils/avatar';
import { TEAM_RANGE_MAX_DAYS, firstGrapheme, inclusiveDaySpan } from './teamUtils';
import './teams.css';

export function teamErrorMessage(t: (key: string, params?: Record<string, string | number>) => string, error: ApiError): string {
  const byTeams = t(`teams.errors.${error.code}`);
  if (byTeams !== `teams.errors.${error.code}`) return byTeams;
  return getApiErrorMessage(t, error);
}

export const TeamAvatar: React.FC<{ team: Pick<Team, 'id' | 'name' | 'avatarUrl'>; size?: 'sm' | 'md' | 'lg' }> = ({
  team,
  size = 'md',
}) => {
  const src = team.avatarUrl ? avatarUrl(team.avatarUrl.startsWith('/api/') ? team.avatarUrl : `/api/v1/teams/${team.id}/avatar/content`) : '';
  return (
    <span className={`team-letter-avatar ${size === 'md' ? '' : size}`.trim()} aria-hidden="true">
      {src ? <img src={src} alt="" /> : firstGrapheme(team.name)}
    </span>
  );
};

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

function shiftIsoDate(iso: string, days: number): string {
  const [year, month, day] = iso.split('-').map(Number);
  return new Date(Date.UTC(year, month - 1, day + days)).toISOString().slice(0, 10);
}

export const TeamDateRangeBar: React.FC<{ timezone: string }> = ({ timezone }) => {
  const { t, locale } = useLocale();
  const [params, setParams] = useSearchParams();
  const range = params.get('range') || 'today';
  const from = params.get('from') || '';
  const to = params.get('to') || '';
  const spanError = range === 'custom' && from && to && inclusiveDaySpan(from, to) > TEAM_RANGE_MAX_DAYS;
  const dateParts = new Intl.DateTimeFormat('en-US', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date());
  const today = ['year', 'month', 'day'].map((key) => dateParts.find((part) => part.type === key)?.value).join('-');
  const displayedFrom = range === 'custom' ? from : range === '7d' ? shiftIsoDate(today, -6) : range === '30d' ? shiftIsoDate(today, -29) : today;
  const displayedTo = range === 'custom' ? to : today;

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
    setParams(nextParams, { replace: true });
  };

  return (
    <div className="team-date-toolbar">
      <div className="segmented-control team-date-presets" role="tablist" aria-label={t('dashboard.timeRangeSelector')}>
        {[
          { key: 'today', label: t('common.today') },
          { key: '7d', label: t('common.days7') },
          { key: '30d', label: t('common.days30') },
        ].map((item) => (
          <button
            key={item.key}
            type="button"
            role="tab"
            aria-selected={range === item.key}
            className={`segmented-item ${range === item.key ? 'active' : ''}`}
            onClick={() => setRange(item.key)}
          >
            {item.label}
          </button>
        ))}
      </div>
      <div className="team-date-custom">
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
          <ArrowRight className="team-date-arrow" size={16} aria-hidden="true" />
          <TeamDateField
            value={displayedTo}
            onChange={(next) => setDate('to', next)}
            label={t('teams.range.to')}
            min={range === 'custom' && from ? from : undefined}
            max={today}
            locale={locale}
            invalid={Boolean(spanError)}
            align="end"
          />
        </div>
        {spanError && (
          <p className="team-date-hint is-error" role="alert">{t('teams.range.tooLong')}</p>
        )}
      </div>
    </div>
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
