import { useEffect, useId, useRef, useState } from 'react';
import { formatTokenCompact } from './teamUtils';

export type SkyTeamSeries = { id: string; name: string; color: string; values: number[] };

export function SkyTeamTrend({
  dates,
  series,
  en,
  efficiency = false,
  empty,
  hourly = false,
  timezone = 'UTC',
}: {
  dates: string[];
  series: SkyTeamSeries[];
  en: boolean;
  efficiency?: boolean;
  empty?: string;
  hourly?: boolean;
  timezone?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(680);
  const [selected, setSelected] = useState<number | null>(null);
  const id = useId().replace(/:/g, '');

  useEffect(() => {
    if (typeof ResizeObserver === 'undefined' || !ref.current) return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(220, entry.contentRect.width)));
    observer.observe(ref.current);
    return () => observer.disconnect();
  }, []);

  if (!dates.length || !series.length) {
    return <p className="team-chart-empty">{empty}</p>;
  }

  const height = 215;
  const left = 3;
  const right = width - 3;
  const bottom = 180;
  const top = 18;
  const max = Math.max(1, ...series.flatMap((item) => item.values)) * 1.12;
  const x = (index: number) => (dates.length === 1 ? width / 2 : left + index / (dates.length - 1) * (right - left));
  const y = (value: number) => bottom - value / max * (bottom - top);
  const index = Math.min(selected ?? dates.length - 1, dates.length - 1);
  const label = (point: number) => {
    const raw = dates[point] || '';
    if (hourly) {
      return new Intl.DateTimeFormat('en-GB', { timeZone: timezone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(raw));
    }
    return raw.length >= 10 ? raw.slice(5, 10).replace('-', '.') : raw;
  };
  const axisLabel = (raw: string) => {
    if (hourly) {
      return new Intl.DateTimeFormat('en-GB', { timeZone: timezone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(raw));
    }
    return raw.length >= 10 ? raw.slice(5, 10).replace('-', '/') : raw;
  };
  const ticks = Math.min(dates.length, width < 400 ? 4 : 6);
  const value = (n: number) => (efficiency ? Math.round(n).toLocaleString('en-US') : formatTokenCompact(String(Math.round(n))));
  const readable = `${label(index)} · ${series.map((item) => `${item.name}: ${value(item.values[index] || 0)}`).join(', ')}`;
  const path = (item: SkyTeamSeries) => item.values.map((point, i) => `${i ? 'L' : 'M'}${x(i)},${y(point)}`).join(' ');

  return (
    <div className="tw-trend" ref={ref}>
      <div className="tw-chart-readout" aria-live="polite">
        <span>{label(index)}</span>
        {series.map((item) => (
          <span key={item.id}>
            <i style={{ background: item.color }} />
            {item.name}
            <strong>{value(item.values[index] || 0)}</strong>
          </span>
        ))}
      </div>
      <svg
        className="team-multiline-chart"
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label={`${en ? 'Team trend' : '团队趋势'} · ${readable}`}
        onPointerMove={(event) => {
          const box = event.currentTarget.getBoundingClientRect();
          setSelected(Math.max(0, Math.min(dates.length - 1, Math.round((event.clientX - box.left) / box.width * (dates.length - 1)))));
        }}
        onPointerLeave={() => setSelected(null)}
      >
        <defs>
          <linearGradient id={`tw-area-${id}`} x1="0" y1="0" x2="0" y2="1">
            <stop stopColor="#a4e94c" stopOpacity=".32" />
            <stop offset="1" stopColor="#b9ed7b" stopOpacity=".01" />
          </linearGradient>
        </defs>
        {[0, 1, 2, 3].map((n) => (
          <g key={n}>
            <line
              x1={left}
              x2={right}
              y1={bottom - n * (bottom - top) / 3}
              y2={bottom - n * (bottom - top) / 3}
              stroke="#e8ede5"
              strokeDasharray={n ? '3 5' : undefined}
            />
            {n > 0 && (
              <text x={right - 3} y={bottom - n * (bottom - top) / 3 - 5} textAnchor="end" fill="#899383" fontSize="10">
                {value(max * n / 3)}
              </text>
            )}
          </g>
        ))}
        {series.map((item, n) => (
          <g key={item.id}>
            {n === 0 && dates.length > 1 && (
              <path d={`${path(item)} L${x(dates.length - 1)},${bottom} L${left},${bottom} Z`} fill={`url(#tw-area-${id})`} />
            )}
            <path d={path(item)} fill="none" stroke={item.color} strokeWidth={n === 0 ? 2.6 : 2} strokeLinejoin="round" strokeLinecap="round" />
            {dates.length === 1 && (
              <line x1={width / 2} x2={width / 2} y1={bottom} y2={y(item.values[0] || 0)} stroke={item.color} strokeWidth="16" strokeLinecap="round" />
            )}
          </g>
        ))}
        <line x1={x(index)} x2={x(index)} y1={top} y2={bottom} stroke="#809171" strokeDasharray="4 4" />
        {series.map((item) => (
          <circle key={item.id} cx={x(index)} cy={y(item.values[index] || 0)} r="4" fill={item.color} stroke="white" strokeWidth="2" />
        ))}
        {Array.from({ length: ticks }, (_, i) => {
          const n = ticks === 1 ? 0 : Math.round(i * (dates.length - 1) / (ticks - 1));
          return (
            <text
              key={i}
              x={x(n)}
              y="207"
              textAnchor={ticks === 1 ? 'middle' : i === 0 ? 'start' : i === ticks - 1 ? 'end' : 'middle'}
              fill="#7a8473"
              fontSize="11"
            >
              {label(n)}
            </text>
          );
        })}
      </svg>
      <div className="team-trend-dates sr-only">
        {dates.map((date) => <span key={date}>{axisLabel(date)}</span>)}
      </div>
      <input
        className="chart-keyboard"
        type="range"
        min={0}
        max={Math.max(0, dates.length - 1)}
        value={index}
        onChange={(event) => setSelected(Number(event.target.value))}
        aria-label={en ? 'Select team chart date' : '选择团队趋势日期'}
        aria-valuetext={readable}
      />
    </div>
  );
}
