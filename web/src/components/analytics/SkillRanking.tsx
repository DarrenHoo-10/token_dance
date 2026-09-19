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
  const { t, locale } = useLocale();
  const zh = locale === 'zh-CN';

  if (!skills || skills.length === 0) {
    return (
      <div className="analytics-skills-empty">
        {t('dashboard.noSkillData')}
      </div>
    );
  }

  return (
    <div className="analytics-skills">
      {skills.slice(0, 4).map((skill, index) => (
        <div key={skill.skillId || skill.skillPublicName + index}>
          <span className="skill-order">{String(skill.rankNo || index + 1).padStart(2, '0')}</span>
          <div>
            <strong>{skill.skillPublicName}</strong>
            <small>{zh ? `使用 ${skill.activeDays} 天` : `Used on ${skill.activeDays} days`}</small>
          </div>
          <span className="skill-count">
            {formatCount(skill.useCount)}
            <small>{zh ? '次' : 'uses'}</small>
          </span>
        </div>
      ))}
    </div>
  );
};
