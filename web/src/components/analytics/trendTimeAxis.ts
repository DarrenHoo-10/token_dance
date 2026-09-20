import type { TokenTrendItem } from '@/types/api';

const HOUR = 3_600_000;
const DAY = 24 * HOUR;
const OFFSET = 8 * HOUR;

// Unzoned API buckets use the product statistics calendar (UTC+8), never
// the browser's timezone. Explicit ISO offsets retain their actual instant.
function bucketTime(date: string) {
  const normalized = date.replace(' ', 'T');
  if (/^\d{4}-\d{2}$/.test(normalized)) return Date.parse(`${normalized}-01T00:00:00+08:00`);
  if (/^\d{4}-\d{2}-\d{2}$/.test(normalized)) return Date.parse(`${normalized}T00:00:00+08:00`);
  return Date.parse(/[zZ]$|[+-]\d{2}:?\d{2}$/.test(normalized) ? normalized : `${normalized}+08:00`);
}

export function trendTimeAxis(trends: TokenTrendItem[], tickCount: number) {
  const points = trends.map(item => ({ date: item.date, time: bucketTime(item.date), total: Math.max(0, Number(item.tokenTotal) || 0) }))
    .filter(point => Number.isFinite(point.time)).sort((a, b) => a.time - b.time);
  if (!points.length) return { points, ticks: [], start: 0, span: 0 };
  const start = points[0].time;
  const end = points[points.length - 1].time;
  const span = end - start;
  const hourly = points.some(point => /[ T]\d{2}:/.test(point.date));
  const monthly = points.every(point => /^\d{4}-\d{2}$/.test(point.date));
  const unit = hourly ? HOUR : DAY;
  const step = Math.max(1, Math.ceil(span / (tickCount - 1) / unit)) * unit;
  const times = [start];
  // Regular clock/calendar ticks also cover intervals with no returned rows.
  for (let time = Math.ceil((start + OFFSET) / step) * step - OFFSET; time < end; time += step) {
    if (time - start >= step * .6 && end - time >= step * .6) times.push(time);
  }
  if (end > start) times.push(end);
  if (monthly) {
    const first = new Date(start + OFFSET);
    const last = new Date(end + OFFSET);
    const firstMonth = first.getUTCFullYear() * 12 + first.getUTCMonth();
    const lastMonth = last.getUTCFullYear() * 12 + last.getUTCMonth();
    const monthStep = Math.max(1, Math.ceil((lastMonth - firstMonth) / (tickCount - 1)));
    times.splice(0, times.length, start);
    for (let month = firstMonth + monthStep; month < lastMonth; month += monthStep) {
      times.push(Date.UTC(Math.floor(month / 12), month % 12, 1) - OFFSET);
    }
    if (end > start) times.push(end);
  }
  const datePart = (time: number) => new Date(time + OFFSET).toISOString().slice(0, 10);
  const ticks = times.map((time, index) => {
    const date = datePart(time);
    const clock = new Date(time + OFFSET).toISOString().slice(11, 16);
    const day = date.slice(5).replace('-', '.');
    const showDate = index === 0 || date !== datePart(times[index - 1]);
    return { time, label: monthly ? date.slice(0, 7) : hourly ? `${showDate ? `${day} ` : ''}${clock}` : day };
  });
  return { points, ticks, start, span };
}
