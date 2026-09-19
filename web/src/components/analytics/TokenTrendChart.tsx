import React, { useId, useState } from 'react';
import { useLocale } from '@/context/LocaleContext';
import type { TokenTrendItem } from '@/types/api';

export interface TokenTrendChartProps { trends: TokenTrendItem[]; mode?: 'total' | 'structure'; height?: number }
export const TokenTrendChart: React.FC<TokenTrendChartProps> = ({ trends, height = 210 }) => {
  const { t, locale } = useLocale();
  const id = useId().replace(/:/g, '');
  const [selected, setSelected] = useState<number | null>(null);
  if (!trends?.length) return <div className="token-trend-empty" style={{ minHeight: height }}>{t('dashboard.noTrendData')}</div>;
  const points = trends.map(item => ({ date: item.date, total: Math.max(0, Number(item.tokenTotal) || 0) }));
  const width = 700, bottom = height - 14, top = 15, max = Math.max(1, ...points.map(p => p.total)) * 1.12;
  const x = (i: number) => points.length === 1 ? width / 2 : 8 + i / (points.length - 1) * (width - 16);
  const y = (value: number) => bottom - value / max * (bottom - top);
  const path = points.map((p, i) => `${i ? 'L' : 'M'} ${x(i)} ${y(p.total)}`).join(' ');
  const active = Math.max(0, Math.min(selected ?? points.length - 1, points.length - 1));
  const label = (date: string) => date.includes(' ') ? date.split(' ')[1] : date.slice(5);
  const ticks = [...new Set([0, Math.floor(points.length / 3), Math.floor(points.length * 2 / 3), points.length - 1])];
  return <div className="token-trend-interactive">
    <div className="token-trend-readout" aria-live="polite"><span>{points[active].date}</span><strong>{points[active].total.toLocaleString(locale)} <small>Token</small></strong></div>
    <svg viewBox={`0 0 ${width} ${height}`} style={{ height }} preserveAspectRatio="none" role="img" aria-label={`${t('dashboard.tokenTrends')}: ${points[active].date}, ${points[active].total.toLocaleString(locale)} Token`} onPointerMove={e => { const rect = e.currentTarget.getBoundingClientRect(); setSelected(Math.round(Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width)) * (points.length - 1))); }} onPointerLeave={() => setSelected(null)}>
      <defs><linearGradient id={`token-area-${id}`} x1="0" y1="0" x2="0" y2="1"><stop stopColor="#a5e94d" stopOpacity=".6" /><stop offset="1" stopColor="#dff9b4" stopOpacity=".05" /></linearGradient></defs>
      {[0, 1, 2, 3].map(n => <line key={n} x1="8" x2={width - 8} y1={bottom - n / 3 * (bottom - top)} y2={bottom - n / 3 * (bottom - top)} stroke="#e3eadd" strokeDasharray="3 5" />)}
      <path d={`${path} L${x(points.length - 1)} ${bottom} L${x(0)} ${bottom} Z`} fill={`url(#token-area-${id})`} />
      <path d={path} fill="none" stroke="#8cbe43" strokeWidth="2.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" />
      <line x1={x(active)} x2={x(active)} y1={top} y2={bottom} stroke="#799958" strokeDasharray="4 4" />
      <circle cx={x(active)} cy={y(points[active].total)} r="4" fill="#526d38" stroke="white" strokeWidth="2" />
    </svg>
    <div className="chart-axis-labels">{ticks.map(i => <span key={i}>{label(points[i].date)}</span>)}</div>
    <input className="token-trend-keyboard" type="range" min="0" max={points.length - 1} value={active} aria-label={locale === 'zh-CN' ? '选择趋势日期' : 'Select trend date'} aria-valuetext={`${points[active].date}: ${points[active].total.toLocaleString(locale)} Token`} onChange={e => setSelected(Number(e.target.value))} />
  </div>;
};
