import React, { useEffect, useId, useRef, useState } from 'react';
import { useLocale } from '@/context/LocaleContext';
import type { TokenTrendItem } from '@/types/api';

export interface TokenTrendChartProps { trends: TokenTrendItem[]; mode?: 'total' | 'structure'; height?: number }
export const TokenTrendChart: React.FC<TokenTrendChartProps> = ({ trends, height = 205 }) => {
  const { t, locale } = useLocale();
  const id = useId().replace(/:/g, '');
  const [selected, setSelected] = useState<number | null>(null);
  const [width, setWidth] = useState(700);
  const chart = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!chart.current || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(240, entry.contentRect.width)));
    observer.observe(chart.current);
    return () => observer.disconnect();
  }, [trends?.length]);
  if (!trends?.length) return <div className="token-trend-empty" style={{ minHeight: height }}>{t('dashboard.noTrendData')}</div>;
  const points = trends.map(item => ({ date: item.date, total: Math.max(0, Number(item.tokenTotal) || 0) }));
  const bottom = height - 27, top = 34, max = Math.max(1, ...points.map(p => p.total)) * 1.08;
  const x = (i: number) => points.length === 1 ? width / 2 : 8 + i / (points.length - 1) * (width - 16);
  const y = (value: number) => bottom - value / max * (bottom - top);
  // Horizontal control points keep the curve within each pair of recorded values.
  const path = points.map((p, i) => {
    if (!i) return `M ${x(i)} ${y(p.total)}`;
    const middle = (x(i - 1) + x(i)) / 2;
    return `C ${middle} ${y(points[i - 1].total)} ${middle} ${y(p.total)} ${x(i)} ${y(p.total)}`;
  }).join(' ');
  const active = Math.max(0, Math.min(selected ?? points.length - 1, points.length - 1));
  const label = (date: string) => date.includes(' ') ? date.split(' ')[1] : date.includes('T') ? date.slice(11, 16) : date.slice(5).replace('-', '.');
  const tickCount = width < 400 ? 4 : 7;
  const ticks = [...new Set(Array.from({ length: tickCount }, (_, i) => Math.round(i * (points.length - 1) / (tickCount - 1))))];
  const selectPosition = (e: React.PointerEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    setSelected(Math.round(Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width)) * (points.length - 1)));
  };
  const amount = new Intl.NumberFormat('en', { notation: 'compact', maximumFractionDigits: 2 }).format(points[active].total);
  return <div className="token-trend-interactive token-trend-soft trend-chart" ref={chart}>
    <div className="token-trend-readout" aria-live="polite"><span>{points[active].date}</span><strong>{points[active].total.toLocaleString(locale)} <small>Token</small></strong></div>
    <svg viewBox={`0 0 ${width} ${height}`} style={{ height }} role="img" aria-label={`${t('dashboard.tokenTrends')}: ${points[active].date}, ${points[active].total.toLocaleString(locale)} Token`} onPointerMove={selectPosition} onPointerDown={selectPosition} onPointerLeave={() => setSelected(null)}>
      <defs><linearGradient id={`token-area-${id}`} x1="0" y1="0" x2="0" y2="1"><stop stopColor="#a6ef35" stopOpacity=".75" /><stop offset="1" stopColor="#baff5d" stopOpacity=".035" /></linearGradient></defs>
      {ticks.map(i => <line key={i} x1={x(i)} x2={x(i)} y1="23" y2={bottom} stroke="#e6ebe3" strokeDasharray="3 5" />)}
      <line x1="8" x2={width - 8} y1={bottom} y2={bottom} stroke="#e4e9df" />
      <path d={`${path} L${x(points.length - 1)} ${bottom} L${x(0)} ${bottom} Z`} fill={`url(#token-area-${id})`} />
      <path d={path} fill="none" stroke="#9adb38" strokeWidth="2" />
      <line x1={x(active)} x2={x(active)} y1={y(points[active].total)} y2={bottom} stroke="#6f983b" strokeDasharray="3 4" />
      <circle cx={x(active)} cy={y(points[active].total)} r="5" fill="#27361c" stroke="white" strokeWidth="3" />
      <g transform={`translate(${Math.max(48, Math.min(width - 48, x(active)))},${Math.max(0, y(points[active].total) - 52)})`} aria-hidden="true"><rect x="-43" width="86" height="43" rx="9" fill="#2d3627" /><text y="17" textAnchor="middle" fill="#ddffb7" fontSize="13" fontWeight="650">{amount}</text><text y="33" textAnchor="middle" fill="white" fontSize="10">{label(points[active].date)}</text></g>
      {ticks.map((i, index) => <text key={i} x={x(i)} y={height - 4} textAnchor={index === 0 ? 'start' : index === ticks.length - 1 ? 'end' : 'middle'} fill="#727a70" fontSize="11">{label(points[i].date)}</text>)}
    </svg>
    <input className="token-trend-keyboard" type="range" min="0" max={points.length - 1} value={active} aria-label={locale === 'zh-CN' ? '选择趋势日期' : 'Select trend date'} aria-valuetext={`${points[active].date}: ${points[active].total.toLocaleString(locale)} Token`} onChange={e => setSelected(Number(e.target.value))} />
  </div>;
};
