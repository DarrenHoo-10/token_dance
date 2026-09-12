import React from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import { ApiError } from '@/api/client';
import type { SharingFlags, Team, TeamRole } from '@/api/teams';
import { Badge } from '@/components/common/Badge';
import { Input } from '@/components/common/Input';
import { Switch } from '@/components/common/Switch';
import { useLocale } from '@/context/LocaleContext';
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

export const TeamDateRangeBar: React.FC<{ timezone: string }> = ({ timezone }) => {
  const { t } = useLocale();
  const [params, setParams] = useSearchParams();
  const range = params.get('range') || 'today';
  const from = params.get('from') || '';
  const to = params.get('to') || '';
  const spanError = range === 'custom' && from && to && inclusiveDaySpan(from, to) > TEAM_RANGE_MAX_DAYS;
  const dateParts = new Intl.DateTimeFormat('en-US', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date());
  const today = ['year', 'month', 'day'].map((key) => dateParts.find((part) => part.type === key)?.value).join('-');

  const setRange = (next: string) => {
    const nextParams = new URLSearchParams(params);
    nextParams.set('range', next);
    if (next !== 'custom') {
      nextParams.delete('from');
      nextParams.delete('to');
    }
    setParams(nextParams, { replace: true });
  };

  const setDate = (key: 'from' | 'to', value: string) => {
    const nextParams = new URLSearchParams(params);
    nextParams.set('range', 'custom');
    nextParams.set(key, value);
    setParams(nextParams, { replace: true });
  };

  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 12, alignItems: 'flex-end', justifyContent: 'space-between' }}>
      <div className="segmented-control" role="tablist" aria-label={t('dashboard.timeRangeSelector')}>
        {[
          { key: 'today', label: t('common.today') },
          { key: '7d', label: t('common.days7') },
          { key: '30d', label: t('common.days30') },
          { key: 'custom', label: t('common.custom') },
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
      {range === 'custom' && (
        <div style={{ display: 'flex', gap: 12, flexWrap: 'wrap' }}>
          <Input type="date" label={t('teams.range.from')} max={today} value={from} onChange={(e) => setDate('from', e.target.value)} />
          <Input type="date" label={t('teams.range.to')} min={from || undefined} max={today} value={to} onChange={(e) => setDate('to', e.target.value)} />
        </div>
      )}
      {spanError && <p className="form-error">{t('teams.range.tooLong')}</p>}
      {range === 'custom' && !spanError && <p className="text-muted" style={{ fontSize: 12, width: '100%', margin: 0 }}>{t('teams.range.customHint')}</p>}
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
