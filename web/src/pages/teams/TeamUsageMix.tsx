import React, { useMemo, useState } from 'react';
import type { AnalysisBucketItem, SkillItem, SkillMemberUse, TeamAnalysisReady } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { useLocale } from '@/context/LocaleContext';
import { formatTokenCompact } from './teamUtils';

const COLORS = ['#577d21', '#277d96', '#8668a6', '#bc7939', '#bb5275'];

type MixKind = 'harness' | 'model' | 'skill';

interface MixItem {
  id: string;
  label: string;
  value: string;
  share: string | null;
  members: SkillMemberUse[];
}

function harnessLabel(id?: string) {
  if (!id) return '';
  return id.charAt(0).toUpperCase() + id.slice(1);
}

function asSkill(item: SkillItem | Record<string, unknown>): SkillItem {
  const raw = item as SkillItem & { tokens?: { value?: string }; useCount?: string };
  return {
    id: String(raw.id || raw.label || ''),
    label: String(raw.label || raw.id || ''),
    agentId: raw.agentId ? String(raw.agentId) : undefined,
    useCount: String(raw.useCount || raw.tokens?.value || '0'),
    share: raw.share == null ? null : String(raw.share),
    memberCount: raw.memberCount == null ? undefined : String(raw.memberCount),
    members: Array.isArray(raw.members) ? raw.members : [],
  };
}

function bucketItems(items: AnalysisBucketItem[] | undefined): MixItem[] {
  return (items || [])
    .filter((item) => item.bucketType !== 'unshared_classification')
    .map((item) => ({
      id: item.id,
      label: item.label,
      value: item.tokens.state === 'available' && item.tokens.value ? item.tokens.value : '0',
      share: item.share ?? null,
      members: item.members || [],
    }))
    .filter((item) => item.value !== '0');
}

export const TeamUsageMix: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t } = useLocale();
  const harness = useMemo(() => bucketItems(analysis.agents.items), [analysis.agents.items]);
  const models = useMemo(() => bucketItems(analysis.models.items), [analysis.models.items]);
  const skills = useMemo(
    () => (analysis.skills?.items || []).map((item) => asSkill(item as SkillItem)).filter((item) => item.useCount !== '0').map((item) => ({
      id: item.id,
      label: item.agentId ? `${item.label} · ${harnessLabel(item.agentId)}` : item.label,
      value: item.useCount,
      share: item.share ?? null,
      members: item.members || [],
    })),
    [analysis.skills],
  );
  const groups: { kind: MixKind; label: string; unit: string; items: MixItem[] }[] = [
    { kind: 'harness', label: t('teams.overview.harness'), unit: 'Token', items: harness },
    { kind: 'model', label: t('teams.overview.models'), unit: 'Token', items: models },
    { kind: 'skill', label: t('teams.overview.skill'), unit: t('teams.skills.calls'), items: skills },
  ];
  const firstKind = (groups.find((group) => group.items.length > 0)?.kind || 'harness') as MixKind;
  const [kind, setKind] = useState<MixKind>(firstKind);
  const active = groups.find((group) => group.kind === kind) || groups[0];
  const [selectedId, setSelectedId] = useState(active.items[0]?.id || '');
  const selected = active.items.find((item) => item.id === selectedId) || active.items[0];
  const empty = active.items.length === 0;

  const switchKind = (next: MixKind) => {
    setKind(next);
    const group = groups.find((item) => item.kind === next);
    setSelectedId(group?.items[0]?.id || '');
  };

  return (
    <section className="team-usage-section" aria-labelledby="team-usage-mix-heading">
      <div className="team-section-heading">
        <h2 id="team-usage-mix-heading">{t('teams.overview.usageMix')}</h2>
      </div>
      <div className="segmented-control team-mix-tabs" role="tablist" aria-label={t('teams.overview.usageMix')}>
        {groups.map((group) => (
          <button
            key={group.kind}
            type="button"
            role="tab"
            aria-selected={kind === group.kind}
            className={`segmented-item ${kind === group.kind ? 'active' : ''}`}
            onClick={() => switchKind(group.kind)}
          >
            {group.label}
          </button>
        ))}
      </div>
      <div className="team-usage-mix">
        <Card>
          <div className="panel-header">
            <h2>{active.label}</h2>
            <span className="team-chart-unit">{active.unit}</span>
          </div>
          {empty ? (
            <p className="team-chart-empty">{t('teams.overview.mixEmpty')}</p>
          ) : (
            <div className="team-mix-list">
              {active.items.map((item) => {
                const pct = Number(item.share || '0');
                return (
                  <button
                    key={item.id}
                    type="button"
                    className={`team-mix-row ${selected?.id === item.id ? 'is-active' : ''}`}
                    aria-pressed={selected?.id === item.id}
                    onClick={() => setSelectedId(item.id)}
                  >
                    <div className="team-mix-row-copy">
                      <div className="team-bar-label">
                        <span>{item.label}</span>
                        <span className="mono-num">{item.share ? `${item.share}%` : '—'}</span>
                      </div>
                      <div className="team-bar-track"><span style={{ width: `${Math.max(0, Math.min(100, pct))}%` }} /></div>
                    </div>
                    <span className="mono-num team-mix-value">{formatTokenCompact(item.value)}</span>
                  </button>
                );
              })}
            </div>
          )}
        </Card>
        <Card>
          <div className="panel-header">
            <div>
              <h2>{t('teams.overview.memberDist')}</h2>
              <p>{empty || !selected ? t('teams.overview.mixEmpty') : selected.label}</p>
            </div>
            <span className="team-chart-unit">{active.unit}</span>
          </div>
          {empty || !selected || selected.members.length === 0 ? (
            <p className="team-chart-empty">{t('teams.overview.mixEmptyDetail')}</p>
          ) : selected.members.map((member, index) => (
            <div className="team-bar-item" key={member.membershipId}>
              <div className="team-bar-label">
                <span><i className="team-dot" style={{ background: COLORS[index % COLORS.length] }} aria-hidden="true" />{member.displayName}</span>
                <span className="mono-num">{formatTokenCompact(member.useCount)}{member.share ? ` · ${member.share}%` : ''}</span>
              </div>
              <div className="team-bar-track"><span style={{ width: `${Math.max(0, Number(member.share || '0'))}%`, background: COLORS[index % COLORS.length] }} /></div>
            </div>
          ))}
        </Card>
      </div>
    </section>
  );
};
