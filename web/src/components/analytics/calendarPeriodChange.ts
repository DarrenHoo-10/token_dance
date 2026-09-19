import type { CalendarDay } from '@/types/api';

/** Compare only complete, contiguous periods; missing days are not recorded zero. */
export function calendarPeriodChange(days: CalendarDay[], count: number): number | null {
  const records = [...days].sort((a, b) => a.date.localeCompare(b.date)).slice(-count * 2);
  if (records.length !== count * 2) return null;
  for (let i = 0; i < records.length; i++) {
    const stamp = Date.parse(`${records[i].date}T00:00:00Z`);
    const value = Number(records[i].tokenTotal);
    if (!Number.isFinite(stamp) || !Number.isFinite(value) || value < 0) return null;
    if (i && stamp - Date.parse(`${records[i - 1].date}T00:00:00Z`) !== 86400000) return null;
  }
  const prior = records.slice(0, count).reduce((sum, day) => sum + Number(day.tokenTotal), 0);
  const current = records.slice(count).reduce((sum, day) => sum + Number(day.tokenTotal), 0);
  return prior > 0 ? (current - prior) / prior * 100 : null;
}
