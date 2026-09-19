import React, { useState } from 'react';
import { ChevronLeft, ChevronRight, Flame } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import type { ActivityCalendarDay } from '@/types/api';

export interface ActivityCalendarProps { days: ActivityCalendarDay[]; streakDays?: number }

/** Server dates are local calendar dates, independent of the browser timezone. */
export const ActivityCalendar: React.FC<ActivityCalendarProps> = ({ days, streakDays = 0 }) => {
  const { t, locale } = useLocale();
  const zh = locale === 'zh-CN';
  const records = days.filter(day => /^\d{4}-\d{2}-\d{2}$/.test(day.date)).sort((a, b) => a.date.localeCompare(b.date));
  const first = records[0]?.date, last = records.at(-1)?.date;
  const [chosenMonth, setMonth] = useState<string | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const now = new Intl.DateTimeFormat('en', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit' }).formatToParts(new Date());
  const latestMonth = last?.slice(0, 7) ?? `${now.find(part => part.type === 'year')!.value}-${now.find(part => part.type === 'month')!.value}`;
  const month = chosenMonth && first && chosenMonth >= first.slice(0, 7) && chosenMonth <= latestMonth ? chosenMonth : latestMonth;
  const [year, monthNumber] = month.split('-').map(Number);
  const start = new Date(Date.UTC(year, monthNumber - 1, 1));
  const leading = (start.getUTCDay() + 6) % 7, length = new Date(Date.UTC(year, monthNumber, 0)).getUTCDate();
  const byDate = new Map(records.map(day => [day.date, day]));
  const selectedDate = selected && byDate.has(selected) && selected.startsWith(month) ? selected : records.filter(day => day.date.startsWith(month)).at(-1)?.date;
  const detail = selectedDate ? byDate.get(selectedDate) : undefined;
  const changeMonth = (direction: number) => { setMonth(new Date(Date.UTC(year, monthNumber - 1 + direction, 1)).toISOString().slice(0, 7)); setSelected(null); };
  return <div className="activity-month-calendar">
    <div className="calendar-month-heading"><div><span>{zh ? '创作日历' : 'Creative calendar'}</span><strong>{start.toLocaleDateString(locale, { year: 'numeric', month: 'long', timeZone: 'UTC' })}</strong></div><div><button type="button" disabled={!first || month <= first.slice(0, 7)} onClick={() => changeMonth(-1)} aria-label={zh ? '上个月' : 'Previous month'}><ChevronLeft size={17} /></button><button type="button" disabled={!last || month >= latestMonth} onClick={() => changeMonth(1)} aria-label={zh ? '下个月' : 'Next month'}><ChevronRight size={17} /></button></div></div>
    <div className="calendar-weekdays" aria-hidden="true">{(zh ? ['一', '二', '三', '四', '五', '六', '日'] : ['M', 'T', 'W', 'T', 'F', 'S', 'S']).map((label, i) => <span key={i}>{label}</span>)}</div>
    <div className="calendar-month-grid" role="grid" aria-label={t('dashboard.activityHeatmap')}>
      {Array.from({ length: Math.ceil((leading + length) / 7) }, (_, week) => <div role="row" className="calendar-week" key={week}>{Array.from({ length: 7 }, (_, weekday) => {
        const day = week * 7 + weekday - leading + 1;
        if (day < 1 || day > length) return <div role="gridcell" key={weekday} />;
        const date = `${month}-${String(day).padStart(2, '0')}`, record = byDate.get(date);
        return <div role="gridcell" key={date} aria-selected={date === selectedDate}><button type="button" disabled={!record} className={`calendar-day level-${Math.max(0, Math.min(5, record?.level ?? 0))} ${date === selectedDate ? 'selected' : ''}`} aria-label={record ? t('dashboard.activityCellLabel', { date, tokens: record.tokenTotal }) : `${date} · ${zh ? '暂无记录' : 'No record'}`} onClick={() => setSelected(date)}>{day}{record && Number(record.tokenTotal) > 0 && <i />}</button></div>;
      })}</div>)}
    </div>
    <div className="calendar-day-summary"><span>{detail?.date ?? (zh ? '暂无记录' : 'No records')}</span><strong>{detail ? `${Number(detail.tokenTotal).toLocaleString(locale)} Token` : '—'}</strong></div>
    <div className="calendar-legend"><span>{streakDays > 0 && <><Flame size={14} />{streakDays} {zh ? '天连续活跃' : 'day streak'}</>}</span><span><span>{t('dashboard.lessActivity')}</span>{[0, 1, 2, 3, 4].map(level => <i key={level} className={`level-${level}`} />)}<span>{t('dashboard.moreActivity')}</span></span></div>
  </div>;
};
