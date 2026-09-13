import React, { useMemo, useState } from 'react';
import type { SkillItem, TeamAnalysisReady } from '@/api/teams';
import { Card } from '@/components/common/Card';
import { useLocale } from '@/context/LocaleContext';
import { formatTokenCompact } from './teamUtils';

const COLORS = ['#577d21', '#277d96', '#8668a6', '#bc7939', '#bb5275'];

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

export const TeamSkillUsage: React.FC<{ analysis: TeamAnalysisReady }> = ({ analysis }) => {
  const { t } = useLocale();
  const skills = useMemo(
    () => (analysis.skills?.items || []).map((item) => asSkill(item as SkillItem)),
    [analysis.skills],
  );
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const selected = skills.find((item) => item.id === (selectedId || skills[0]?.id)) || skills[0];
  const totalCalls = skills.reduce((sum, item) => sum + BigInt(item.useCount || '0'), 0n);
  const usingMembers = new Set(skills.flatMap((item) => (item.members || []).map((member) => member.membershipId))).size;
  const current = analysis.summary.currentMembers || '0';
  const empty = skills.length === 0 || totalCalls === 0n;

  return (
    <section className="team-skill-section" id="skills" aria-labelledby="team-skills-heading">
      <div className="team-section-heading">
        <div>
          <h2 id="team-skills-heading">{t('teams.skills.title')}</h2>
          <p>{t('teams.skills.subtitle')}</p>
        </div>
        <span className="team-chart-unit">{t('teams.skills.followsRange')}</span>
      </div>
      <div className="team-skill-layout">
        <Card>
          <div className="team-skill-stats">
            <div>
              <span className="label">{t('teams.skills.calls')}</span>
              <strong className="mono-num">{empty ? '—' : formatTokenCompact(totalCalls.toString())}</strong>
            </div>
            <div>
              <span className="label">{t('teams.skills.users')}</span>
              <strong className="mono-num">{empty ? '—' : `${usingMembers} / ${current}`}</strong>
            </div>
            <div>
              <span className="label">{t('teams.skills.groups')}</span>
              <strong className="mono-num">{empty ? '—' : String(skills.length)}</strong>
            </div>
          </div>
          <div className="team-table-scroll">
            <table>
              <thead>
                <tr>
                  <th>{selected?.agentId ? `${t('teams.skills.rankName')} · ${harnessLabel(selected.agentId)}` : t('teams.skills.rankName')}</th>
                  <th>{t('teams.skills.calls')}</th>
                  <th>{t('teams.skills.share')}</th>
                  <th>{t('teams.skills.members')}</th>
                </tr>
              </thead>
              <tbody>
                {empty ? (
                  <tr><td colSpan={4} className="team-skill-empty">{t('teams.skills.empty')}</td></tr>
                ) : skills.map((skill) => (
                  <tr key={skill.id}>
                    <td>
                      <button
                        type="button"
                        className="team-skill-select"
                        aria-pressed={selected?.id === skill.id}
                        onClick={() => setSelectedId(skill.id)}
                      >
                        {skill.label}
                      </button>
                    </td>
                    <td className="mono-num">{formatTokenCompact(skill.useCount)}</td>
                    <td className="mono-num">{skill.share ? `${skill.share}%` : '—'}</td>
                    <td className="mono-num">{skill.memberCount || '—'}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <p className="team-skill-hint">{t('teams.skills.clickHint')}</p>
        </Card>
        <Card>
          <div className="panel-header">
            <div>
              <h2>{t('teams.skills.distribution')}</h2>
              <p>{empty || !selected ? t('teams.skills.emptyDetail') : `${selected.label}${selected.agentId ? ` · ${harnessLabel(selected.agentId)}` : ''}`}</p>
            </div>
            <span className="team-chart-unit">{t('teams.skills.calls')}</span>
          </div>
          {empty || !selected || !(selected.members || []).length ? (
            <p className="team-skill-empty">{t('teams.skills.emptyDetail')}</p>
          ) : (selected.members || []).map((member, index) => (
            <div className="team-bar-item" key={member.membershipId}>
              <div className="team-bar-label">
                <span><i className="team-dot" style={{ background: COLORS[index % COLORS.length] }} aria-hidden="true" />{member.displayName}</span>
                <span className="mono-num">{formatTokenCompact(member.useCount)}{member.share ? ` · ${member.share}%` : ''}</span>
              </div>
              <div className="team-bar-track"><span style={{ width: `${Math.max(0, Number(member.share || '0'))}%`, background: COLORS[index % COLORS.length] }} /></div>
            </div>
          ))}
          <div className="team-metric-foot">{t('teams.skills.historicalFoot')}</div>
        </Card>
      </div>
    </section>
  );
};
