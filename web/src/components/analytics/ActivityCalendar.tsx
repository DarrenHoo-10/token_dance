import React, { useState } from 'react';
import { CalendarDays, ChevronLeft, ChevronRight, Flame, Sparkles } from 'lucide-react';
import { useLocale } from '@/context/LocaleContext';
import type { ActivityCalendarDay } from '@/types/api';

export interface ActivityCalendarProps { days: ActivityCalendarDay[]; streakDays?: number }

function scaleCompact(value: number, suffix: string) {
  return `${value.toFixed(1).replace(/\.0$/, '')}${suffix}`;
}

/** Compact token label used on calendar cells, matching product totals such as 1.2M / 373K. */
export function formatCalendarCompact(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '';
  if (value >= 1_000_000_000) return scaleCompact(value / 1_000_000_000, 'B');
  if (value >= 1_000_000) return scaleCompact(value / 1_000_000, 'M');
  if (value >= 1_000) return scaleCompact(value / 1_000, 'K');
  return String(Math.round(value));
}

function formatCalendarTokens(value: number) {
  if (!Number.isFinite(value) || value <= 0) return { value: '—', unit: '' };
  const compact = formatCalendarCompact(value);
  const unit = compact.match(/[KMB]$/)?.[0] ?? '';
  return { value: unit ? compact.slice(0, -1) : compact, unit };
}

function formatSelectedDate(date: string | undefined, today: string, zh: boolean, locale: string) {
  if (!date) return zh ? '暂无记录' : 'No records';
  if (date === today) return zh ? '今天' : 'Today';
  return new Date(date + 'T00:00:00+08:00').toLocaleDateString(locale, {
    month: 'long',
    day: 'numeric',
    weekday: 'short',
    timeZone: 'Asia/Shanghai',
  });
}

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
  const monthRecords = records.filter(day => day.date.startsWith(month));
  const activeDays = monthRecords.filter(day => Number(day.tokenTotal) > 0).length;
  const shanghaiToday = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai' }).format(new Date());
  const changeMonth = (direction: number) => {
    setMonth(new Date(Date.UTC(year, monthNumber - 1 + direction, 1)).toISOString().slice(0, 7));
    setSelected(null);
  };
  const selectedTokens = Number(detail?.tokenTotal) || 0;
  const monthTokens = monthRecords.reduce((sum, day) => sum + Number(day.tokenTotal || 0), 0);
  const selectedParts = formatCalendarTokens(selectedTokens);
  const monthParts = formatCalendarTokens(monthTokens);
  const monthTotal = monthRecords.length ? monthParts.value + monthParts.unit : '—';
  const streakLabel = zh ? '连续 ' + streakDays + ' 天' : streakDays + '-day streak';
  const litLabel = zh ? '已点亮 ' + activeDays + ' 天' : activeDays + ' days of creating';
  const ideaLabel = selectedTokens > 0
    ? (zh ? '又一个想法，正在成为现实' : 'Another idea taking shape')
    : (zh ? '留一点空白，等待新灵感' : 'A little space for the next idea');

  return (
    <div className="activity-calendar activity-month-calendar">
      <div className="calendar-title-row">
        <span><CalendarDays size={16} />{zh ? '创作日历' : 'Activity calendar'}</span>
        {streakDays > 0 && <span className="streak-badge"><Flame size={13} aria-hidden="true" />{streakLabel}</span>}
      </div>
      <div className="calendar-month-row calendar-month-heading">
        <div>
          <strong>{start.toLocaleDateString(locale, { year: 'numeric', month: 'long', timeZone: 'UTC' })}</strong>
          <span>{litLabel}</span>
        </div>
        <div className="calendar-month-controls">
          <button type="button" className="icon-button" disabled={!first || month <= first.slice(0, 7)} onClick={() => changeMonth(-1)} aria-label={zh ? '上个月' : 'Previous month'}><ChevronLeft size={17} /></button>
          <button type="button" className="icon-button" disabled={!last || month >= latestMonth} onClick={() => changeMonth(1)} aria-label={zh ? '下个月' : 'Next month'}><ChevronRight size={17} /></button>
        </div>
      </div>
      <div className="calendar-weekdays" aria-hidden="true">
        {(zh ? ['一', '二', '三', '四', '五', '六', '日'] : ['M', 'T', 'W', 'T', 'F', 'S', 'S']).map((label, i) => <span key={i}>{label}</span>)}
      </div>
      <div className="calendar-days calendar-month-grid" role="grid" aria-label={t('dashboard.activityHeatmap')}>
        {Array.from({ length: leading }, (_, index) => <span className="calendar-pad" key={'pad-' + index} />)}
        {Array.from({ length }, (_, index) => {
          const day = index + 1;
          const date = month + '-' + String(day).padStart(2, '0');
          const record = byDate.get(date);
          const level = Math.max(0, Math.min(5, record?.level ?? 0));
          const tokens = Number(record?.tokenTotal) || 0;
          const compact = tokens > 0 ? formatCalendarCompact(tokens) : '';
          return (
            <button
              type="button"
              key={date}
              disabled={!record}
              className={'calendar-day level-' + level + (compact ? ' has-usage' : '') + (date === selectedDate ? ' selected' : '') + (date === shanghaiToday ? ' is-today' : '')}
              data-intensity={record?.level ?? 0}
              aria-pressed={date === selectedDate}
              aria-label={record ? t('dashboard.activityCellLabel', { date, tokens: record.tokenTotal }) : date + ' · ' + (zh ? '暂无记录' : 'No record')}
              onClick={() => setSelected(date)}
            >
              <span className="calendar-day-num">{day}</span>
              {compact ? <span className="calendar-day-usage">{compact}</span> : <i aria-hidden="true" />}
            </button>
          );
        })}
      </div>
      <div className="calendar-legend">
        <span>{zh ? '少' : 'Less'}</span>
        {[0, 1, 2, 3, 4].map(level => <i key={level} data-intensity={level} className={'level-' + level} />)}
        <span>{zh ? '多' : 'More'}</span>
        <span>{zh ? '按 Token 用量' : 'By token usage'}</span>
      </div>
      <div className="calendar-day-detail calendar-day-summary" aria-live="polite">
        <div>
          <span className="selected-date">{formatSelectedDate(selectedDate, shanghaiToday, zh, locale)}</span>
          <span className="day-description"><Sparkles size={12} />{ideaLabel}</span>
        </div>
        <strong title={detail?.tokenTotal}>
          {selectedParts.value}
          {selectedParts.unit ? <small>{selectedParts.unit}</small> : null}
          <span>Token</span>
        </strong>
      </div>
      <div className="calendar-month-summary">
        <span>{zh ? '本月累计' : 'Month total'} <b>{monthTotal}</b></span>
        <span>{zh ? '统计日' : 'Calendar day'} UTC+8</span>
      </div>
    </div>
  );
};
