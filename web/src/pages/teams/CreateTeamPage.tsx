import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ApiError } from '@/api/client';
import { TEAM_JOIN_SHARING, teamsApi, type TeamScope } from '@/api/teams';
import { Button } from '@/components/common/Button';
import { Card } from '@/components/common/Card';
import { Input } from '@/components/common/Input';
import { Select } from '@/components/common/Select';
import { ErrorState } from '@/components/states/ErrorState';
import { LoadingState } from '@/components/states/LoadingState';
import { useAuth } from '@/context/AuthContext';
import { useLocale } from '@/context/LocaleContext';
import { useTeam } from '@/context/TeamContext';
import { api } from '@/api/client';
import {
  TEAM_DESCRIPTION_MAX,
  TEAM_NAME_MAX,
  TEAM_NAME_MIN,
  TEAM_TIMEZONES,
  clearCreateDraft,
  createIdempotencyKey,
  firstGrapheme,
  graphemeLength,
  readCreateDraft,
  writeCreateDraft,
} from './teamUtils';
import { InviteDialog } from './InviteDialog';
import { RoleBadge, TeamGateLink, teamErrorMessage } from './TeamShared';

type CreateState = 'checkingMembership' | 'alreadyMember' | 'checkFailed' | 'editing' | 'submitting' | 'created';

export const CreateTeamPage: React.FC = () => {
  const { authenticated, loading: authLoading, user } = useAuth();
  const { t } = useLocale();
  const { scope, refresh, applyScope } = useTeam();
  const navigate = useNavigate();
  const nameRef = useRef<HTMLInputElement>(null);
  const descRef = useRef<HTMLTextAreaElement>(null);

  const [pageState, setPageState] = useState<CreateState>('checkingMembership');
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [timezone, setTimezone] = useState('UTC');
  const [timezoneFallback, setTimezoneFallback] = useState(false);
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [created, setCreated] = useState<TeamScope | null>(null);
  const [inviteOpen, setInviteOpen] = useState(false);
  const [idempotencyKey, setIdempotencyKey] = useState(() => createIdempotencyKey());
  const [lastFingerprint, setLastFingerprint] = useState<string | null>(null);
  const userKey = user?.userId || user?.handle || 'anon';

  useEffect(() => {
    if (authLoading) return;
    if (!authenticated) {
      navigate('/login?return_to=%2Fteams%2Fnew', { replace: true });
    }
  }, [authLoading, authenticated, navigate]);

  useEffect(() => {
    if (!authenticated) return;
    let cancelled = false;

    const boot = async () => {
      setPageState('checkingMembership');
      try {
        const current = scope ?? (await refresh());
        if (cancelled) return;
        if (current) {
          setCreated(current);
          setPageState('alreadyMember');
          return;
        }
        const draft = readCreateDraft(userKey);
        if (draft) {
          setName(draft.name);
          setDescription(draft.description);
          setTimezone(draft.timezone);
        }
        try {
          const profile = await api.getProfile();
          const accountTz = profile.timezone || profile.timezoneName;
          if (accountTz) {
            setTimezone((prev) => (draft?.timezone ? prev : accountTz));
            setTimezoneFallback(false);
          } else if (!draft?.timezone) {
            setTimezone('UTC');
            setTimezoneFallback(true);
          }
        } catch {
          if (!draft?.timezone) {
            setTimezone('UTC');
            setTimezoneFallback(true);
          }
        }
        setPageState('editing');
      } catch {
        if (!cancelled) setPageState('checkFailed');
      }
    };

    void boot();
    return () => {
      cancelled = true;
    };
  }, [authenticated, refresh, scope, userKey]);

  useEffect(() => {
    if (pageState !== 'editing' || !authenticated) return;
    writeCreateDraft({ userId: userKey, name, description, timezone });
  }, [authenticated, description, name, pageState, timezone, userKey]);

  const timezoneOptions = useMemo(() => {
    if (TEAM_TIMEZONES.some((item) => item.value === timezone)) return TEAM_TIMEZONES;
    return [{ value: timezone, label: timezone }, ...TEAM_TIMEZONES];
  }, [timezone]);

  const validate = (focus = false) => {
    const next: Record<string, string> = {};
    const trimmedName = name.trim();
    const nameLen = graphemeLength(trimmedName);
    const descLen = graphemeLength(description.trim());
    if (!trimmedName || nameLen < TEAM_NAME_MIN) next.name = t('teams.create.nameShort');
    if (nameLen > TEAM_NAME_MAX) next.name = t('teams.create.nameLong');
    if (descLen > TEAM_DESCRIPTION_MAX) next.description = t('teams.create.descLong');
    setFieldErrors(next);
    if (focus) {
      if (next.name) nameRef.current?.focus();
      else if (next.description) descRef.current?.focus();
    }
    return Object.keys(next).length === 0;
  };

  const requestFingerprint = () => `${name.trim()}|${description.trim()}|${timezone}`;

  const submit = async () => {
    if (!validate(true)) {
      setPageState('editing');
      return;
    }

    const fingerprint = requestFingerprint();
    let key = idempotencyKey;
    if (lastFingerprint && lastFingerprint !== fingerprint) {
      const current = await refresh();
      if (current) {
        applyScope(current);
        setCreated(current);
        setPageState('alreadyMember');
        return;
      }
      key = createIdempotencyKey();
      setIdempotencyKey(key);
    }

    setPageState('submitting');
    setFormError(null);
    try {
      const result = await teamsApi.createTeam(
        {
          name: name.trim(),
          description: description.trim() || undefined,
          timezone,
          sharing: TEAM_JOIN_SHARING,
        },
        { idempotencyKey: key }
      );
      applyScope(result);
      setCreated(result);
      setLastFingerprint(fingerprint);
      clearCreateDraft();
      setPageState('created');
    } catch (err) {
      if (err instanceof ApiError && err.code === 'TEAM_MEMBERSHIP_EXISTS') {
        const current = await refresh();
        if (current) {
          applyScope(current);
          setCreated(current);
        }
        setPageState('alreadyMember');
        return;
      }
      if (err instanceof ApiError && err.code === 'API_INVALID_ARGUMENT') {
        const details = (err.details || {}) as Record<string, unknown>;
        const raw = details.fieldErrors;
        const mapped: Record<string, string> = {};
        if (raw && typeof raw === 'object') {
          for (const [field, value] of Object.entries(raw as Record<string, unknown>)) {
            mapped[field] = typeof value === 'string' ? value : t(String(value));
          }
        }
        setFieldErrors(mapped);
        if (mapped.name) nameRef.current?.focus();
        else if (mapped.description) descRef.current?.focus();
      }
      setFormError(err instanceof ApiError ? teamErrorMessage(t, err) : t('errors.unknown'));
      setPageState('editing');
      setLastFingerprint(fingerprint);
    }
  };

  if (authLoading || pageState === 'checkingMembership') return <LoadingState />;

  if (pageState === 'checkFailed') {
    return (
      <section className="product-page-shell team-page">
        <ErrorState title={t('teams.create.checkFailed')} onRetry={() => window.location.reload()} />
      </section>
    );
  }

  if (pageState === 'alreadyMember' && (created || scope)) {
    const current = created || scope;
    return (
      <section className="product-page-shell team-page" aria-labelledby="team-title">
        <div className="product-page-heading">
          <div>
            <span>{t('teams.label')}</span>
            <h1 id="team-title">{t('teams.create.alreadyTitle')}</h1>
            <p>{t('teams.create.alreadyDesc')}</p>
          </div>
        </div>
        <Card>
          <strong>{current!.team.name}</strong>
          <div style={{ margin: '8px 0' }}>
            <RoleBadge role={current!.membership.role} />
          </div>
          <p className="text-muted" style={{ fontSize: 13 }}>{t('teams.singleSeat')}</p>
          <div style={{ marginTop: 16 }}>
            <TeamGateLink to={`/teams/${current!.team.id}`}>{t('teams.create.viewMine')}</TeamGateLink>
          </div>
        </Card>
      </section>
    );
  }

  if (pageState === 'created' && created) {
    return (
      <section className="product-page-shell team-page" aria-labelledby="team-title">
        <div className="product-page-heading">
          <div>
            <span>{t('teams.label')}</span>
            <h1 id="team-title">{t('teams.create.successTitle')}</h1>
            <p>{t('teams.create.successSub')}</p>
          </div>
        </div>
        <Card>
          <div style={{ display: 'flex', gap: 16, alignItems: 'center' }}>
            <span className="team-letter-avatar lg" aria-hidden="true">{firstGrapheme(created.team.name)}</span>
            <div>
              <h2 style={{ margin: 0 }}>{created.team.name}</h2>
              <p className="text-muted" style={{ fontSize: 13 }}>{t('teams.create.ownerMeta')}</p>
              <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                <RoleBadge role="owner" />
                <span className="text-muted" style={{ fontSize: 12 }}>{t('teams.create.oneMember')}</span>
                <span className="text-muted" style={{ fontSize: 12 }}>{t('teams.sharing.on')}</span>
              </div>
            </div>
          </div>
          <div style={{ display: 'flex', gap: 12, marginTop: 20, flexWrap: 'wrap' }}>
            <Button variant="primary" onClick={() => setInviteOpen(true)}>{t('teams.invite.action')}</Button>
            <Button variant="outline" onClick={() => navigate(`/teams/${created.team.id}`, { state: { justCreated: true } })}>
              {t('teams.create.enter')}
            </Button>
          </div>
        </Card>
        <InviteDialog
          isOpen={inviteOpen}
          onClose={() => setInviteOpen(false)}
          teamId={created.team.id}
          permissions={created.permissions}
        />
      </section>
    );
  }

  const frozen = pageState === 'submitting';

  return (
    <section className="product-page-shell team-page" aria-labelledby="team-title">
      <div className="product-page-heading">
        <Link to="/teams" style={{ fontSize: 13 }}>{t('teams.create.back')}</Link>
        <h1 id="team-title">{t('teams.create.title')}</h1>
        <p>{t('teams.create.sub')}</p>
      </div>

      <div className="team-create-grid">
        <Card>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void submit();
            }}
          >
            <Input
              ref={nameRef}
              label={t('teams.create.name')}
              value={name}
              disabled={frozen}
              error={fieldErrors.name}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => validate()}
            />
            <div className="form-group">
              <label className="form-label" htmlFor="team-description">{t('teams.create.description')}</label>
              <textarea
                id="team-description"
                ref={descRef}
                className="form-input"
                disabled={frozen}
                maxLength={400}
                value={description}
                onChange={(e) => setDescription(e.target.value)}
                onBlur={() => validate()}
                style={{ minHeight: 88, padding: '10px 14px' }}
              />
              {fieldErrors.description && <span className="form-error">{fieldErrors.description}</span>}
              <span className="form-hint">{t('teams.create.descHint', { max: TEAM_DESCRIPTION_MAX })}</span>
            </div>
            <Select
              label={t('teams.create.timezone')}
              value={timezone}
              disabled={frozen}
              onChange={(e) => setTimezone(e.target.value)}
              options={timezoneOptions}
              hint={timezoneFallback ? t('teams.create.timezoneFallback') : t('teams.create.timezoneHint')}
            />
            <p className="text-muted" style={{ fontSize: 12 }}>{t('teams.create.seatNotice')}</p>
            {formError && <p className="form-error" role="alert">{formError}</p>}
            <div style={{ display: 'flex', gap: 12, marginTop: 20 }}>
              <Button type="submit" variant="primary" loading={frozen}>
                {frozen ? t('teams.create.submitting') : lastFingerprint && formError ? t('teams.create.retry') : t('teams.create.action')}
              </Button>
              <Button type="button" variant="outline" disabled={frozen} onClick={() => navigate('/teams')}>
                {t('common.cancel')}
              </Button>
            </div>
          </form>
        </Card>

        <Card className="team-preview-card">
          <p className="eyebrow">{t('teams.create.preview')}</p>
          <div style={{ display: 'flex', gap: 12, alignItems: 'center', marginTop: 12 }}>
            <span className="team-letter-avatar" aria-hidden="true">{firstGrapheme(name || t('teams.create.previewName'))}</span>
            <div>
              <strong>{name.trim() || t('teams.create.previewName')}</strong>
              <p className="text-muted" style={{ fontSize: 12, margin: '4px 0 0' }}>
                {description.trim() || t('teams.create.previewDesc')}
              </p>
            </div>
          </div>
          <p className="text-muted" style={{ fontSize: 12, marginTop: 16 }}>{t('teams.create.previewTimezone', { timezone })}</p>
        </Card>
      </div>
    </section>
  );
};
