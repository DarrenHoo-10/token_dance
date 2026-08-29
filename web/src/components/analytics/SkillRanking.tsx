import React from 'react';
import type { SkillItem } from '@/types/api';
import { useLocale } from '@/context/LocaleContext';

export interface SkillRankingProps {
  skills: SkillItem[];
}

function formatCount(val: string | number | undefined): string {
  if (val === undefined || val === null) return '0';
  const num = typeof val === 'number' ? val : parseFloat(val);
  if (isNaN(num)) return String(val);
  return num.toLocaleString();
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
        <div key={skill.skillId || skill.skillPublicName + idx} className="skill-row">
          <span className="skill-rank-badge">{skill.rankNo || idx + 1}</span>
          <div>
            <div style={{ fontWeight: 700, fontSize: 12 }}>{skill.skillPublicName}</div>
            <div style={{ fontSize: 10, color: 'var(--text-muted)' }}>
              {skill.activeDays} {t('dashboard.daysUsed')}
            </div>
          </div>
          <div style={{ textAlign: 'right' }}>
            <strong className="mono-num" style={{ fontSize: 13 }}>
              {formatCount(skill.useCount)}
            </strong>
          </div>
        </div>
      ))}
    </div>
  );
};
