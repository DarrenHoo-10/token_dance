import React from 'react';
import type { ActivityCalendarDay } from '@/types/api';

export interface ActivityCalendarProps {
  days: ActivityCalendarDay[];
  streakDays?: number;
}

export const ActivityCalendar: React.FC<ActivityCalendarProps> = ({ days }) => {
  // If fewer than 70 days, generate padding days
  const fullDays: ActivityCalendarDay[] = [...days];
  while (fullDays.length < 70) {
    fullDays.unshift({
      date: `day-${70 - fullDays.length}`,
      tokenTotal: '0',
      level: 0,
    });
  }

  const displayDays = fullDays.slice(-70);

  return (
    <div>
      <div className="heatmap-grid" role="grid" aria-label="Activity heatmap">
        {displayDays.map((day, idx) => (
          <div
            key={idx}
            className={`heatmap-cell level-${day.level}`}
            title={`${day.date}: ${day.tokenTotal} tokens`}
            role="gridcell"
          />
        ))}
      </div>

      <div
        style={{
          display: 'flex',
          justifyContent: 'flex-end',
          alignItems: 'center',
          gap: 4,
          marginTop: 10,
          fontSize: 10,
          color: 'var(--text-subtle)',
        }}
      >
        <span>Less</span>
        <div className="heatmap-cell level-0" style={{ width: 10, height: 10 }} />
        <div className="heatmap-cell level-1" style={{ width: 10, height: 10 }} />
        <div className="heatmap-cell level-2" style={{ width: 10, height: 10 }} />
        <div className="heatmap-cell level-3" style={{ width: 10, height: 10 }} />
        <div className="heatmap-cell level-4" style={{ width: 10, height: 10 }} />
        <span>More</span>
      </div>
    </div>
  );
};
