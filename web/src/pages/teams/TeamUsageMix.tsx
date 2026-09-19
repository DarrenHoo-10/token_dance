import React, { useMemo, useState } from 'react';
import { ArrowRight, Layers3, LockKeyhole, Sparkles } from 'lucide-react';
import type { AnalysisBucketItem, SkillItem, SkillMemberUse, TeamAnalysisReady } from '@/api/teams';
import { HarnessMark } from '@/components/common/HarnessMark';
import { resolveHarnessBrand } from '@/components/common/harnessBrand';
import { usageColor, usageColorAt } from '@/utils/usageColors';
import { useLocale } from '@/context/LocaleContext';
import { MemberAvatar } from './TeamShared';
import { formatTokenCompact } from './teamUtils';

type MixKind = 'harness' | 'model' | 'skill';

interface MixItem {
  id: string;
  label: string;
  value: string;
  share: string | null;
  members: SkillMemberUse[];
}

function asSkill(item: SkillItem | Record<string, unknown>): SkillItem {
  const raw = item as SkillItem & { tokens?: { value?: string }; useCount?: string };
  return {
    id: String(raw.id || raw.label || ''),
    label: String(raw.label || raw.id || ''),
    useCount: String(raw.useCount || raw.tokens?.value || '0'),
    share: raw.share == null ? null : String(raw.share),
    memberCount: raw.memberCount == null ? undefined : String(raw.memberCount),
    members: Array.isArray(raw.members) ? raw.members : [],
  };
}

function itemKey(item: MixItem) {
  return `${item.id}\0${item.label}`;
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

function markContent(kind: MixKind, item: MixItem) {
  if (kind === 'skill') return <Sparkles size={17} />;
  if (kind === 'harness') {
    if (/claude code/i.test(item.label)) return '✳';
    return <HarnessMark agentId={item.id} label={item.label} size="sm" />;
  }
  return item.label.slice(0, 1).toUpperCase();
}

export const TeamUsageMix: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t } = useLocale();
  const harness = useMemo(() => bucketItems(analysis.agents.items), [analysis.agents.items]);
  const models = useMemo(() => bucketItems(analysis.models.items), [analysis.models.items]);
  const skills = useMemo(
    () => (analysis.skills?.items || []).map((item) => asSkill(item as SkillItem)).filter((item) => item.useCount !== '0').map((item) => ({
      id: item.id,
      label: item.label,
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
  const [selectedKey, setSelectedKey] = useState(active.items[0] ? itemKey(active.items[0]) : '');
  const selected = active.items.find((item) => itemKey(item) === selectedKey) || active.items[0];
  const empty = active.items.length === 0;
  const mixTotal = active.items.reduce((sum, item) => sum + Number(item.value || 0), 0);
  const peakMember = selected?.members.reduce((max, member) => Math.max(max, Number(member.useCount || 0)), 0) || 1;
  const classified = formatTokenCompact(
    String((analysis.agents.items || []).filter((item) => item.bucketType !== 'unshared_classification')
      .reduce((sum, item) => sum + (item.tokens.state === 'available' ? Number(item.tokens.value || 0) : 0), 0)),
  );

  const switchKind = (next: MixKind) => {
    const group = groups.find((item) => item.kind === next);
    setKind(next);
    setSelectedKey(group?.items[0] ? itemKey(group.items[0]) : '');
  };

  return (
    <section className="tw-card tw-mix-card" aria-labelledby="team-usage-mix-heading">
      <div className="tw-card-heading">
        <div>
          <h2 id="team-usage-mix-heading"><Layers3 size={20} />{t('teams.overview.toolkit')}</h2>
          <p>{t('teams.overview.toolkitHint')}</p>
        </div>
        <div className="tw-mini-tabs">
          {groups.map((group) => (
            <button
              key={group.kind}
              type="button"
              aria-pressed={kind === group.kind}
              onClick={() => switchKind(group.kind)}
            >
              {group.label}
            </button>
          ))}
        </div>
      </div>
      {empty ? (
        <div className="tw-no-data"><p>{t('teams.overview.mixEmpty')}</p></div>
      ) : (
        <div className="tw-mix-grid">
          <div className="tw-mix-list">
            {active.items.map((item, index) => {
              const key = itemKey(item);
              const pct = mixTotal > 0 ? (Number(item.value) / mixTotal) * 100 : Number(item.share || 0);
              const color = kind === 'harness' ? resolveHarnessBrand(item.id, item.label).color : usageColorAt(index);
              return (
                <button
                  key={key}
                  type="button"
                  className={selectedKey === key ? 'selected' : ''}
                  onClick={() => setSelectedKey(key)}
                >
                  <span className={`tw-tool-mark tool-${index % 4}`}>{markContent(kind, item)}</span>
                  <span className="tw-mix-name">
                    <strong>{item.label}</strong>
                    <span className="tw-mix-track"><i style={{ width: `${Math.max(0, Math.min(100, pct))}%`, background: color }} /></span>
                  </span>
                  <span>
                    <strong>{kind === 'skill' ? item.value : formatTokenCompact(item.value)}</strong>
                    <small>{pct.toFixed(1)}%</small>
                  </span>
                  <ArrowRight size={15} />
                </button>
              );
            })}
          </div>
          <div className="tw-tool-members">
            <div>
              <span>{selected?.label}</span>
              <small>{t('teams.overview.memberDist')} · {active.unit}</small>
            </div>
            {selected?.members.length ? selected.members.map((member) => (
              <button type="button" key={member.membershipId}>
                <MemberAvatar name={member.displayName} url={member.avatarUrl} />
                <span>{member.displayName}</span>
                <span className="tw-tool-member-track">
                  <i style={{ width: `${Math.max(0, Number(member.useCount || 0) / peakMember * 100)}%`, background: usageColor(member.membershipId) }} />
                </span>
                <strong>{kind === 'skill' ? member.useCount : formatTokenCompact(member.useCount)}</strong>
              </button>
            )) : <p className="tw-muted">{t('teams.overview.mixEmptyDetail')}</p>}
          </div>
        </div>
      )}
      <div className="tw-table-footer">
        <LockKeyhole size={13} />
        {kind === 'skill'
          ? t('teams.overview.skillFoot')
          : t('teams.overview.mixFoot', { tokens: classified })}
      </div>
    </section>
  );
};
