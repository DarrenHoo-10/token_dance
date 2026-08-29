import React from 'react';
import type { SkillMetricItem } from '@/types/api';
import { useLocale } from '@/context/LocaleContext';

export interface SkillRankingProps {
  skills: SkillMetricItem[];
}

export const SkillRanking: React.FC<SkillRankingProps> = ({ skills }) => {
  const { t } = useLocale();

  if (!skills || skills.length === 0) {
    return (
      <div style={{ padding: '24px 0', textAlign: 'center', color: 'var(--text-subtle)', fontSize: 12 }}>
        No skill usage recorded
      </div>
    );
  }

  return (
    <div className="skill-table">
      {skills.slice(0, 5).map((skill, idx) => (
        <div key={skill.skillKey || skill.skillPublicName + idx} className="skill-row">
          <span className="skill-rank-badge">{skill.rankNo || idx + 1}</span>
          <div>
            <div style={{ fontWeight: 700, fontSize: 12 }}>{skill.skillPublicName}</div>
            <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
              {skill.activeDays} {t('dashboard.daysUsed')}
            </div>
          </div>
          <div style={{ textAlign: 'right' }}>
            <strong className="mono-num" style={{ fontSize: 13 }}>
              {skill.useCount.toLocaleString()}
            </strong>
          </div>
        </div>
      ))}
    </div>
  );
};
