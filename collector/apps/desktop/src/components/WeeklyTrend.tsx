import { useState } from "react";

export function WeeklyTrend({ points, lang }: { points: { date: string; tokens: number | null }[]; lang: "zh" | "en" }) {
  const [active, setActive] = useState<number | null>(null);
  const zh = lang === "zh";
  const available = points.some(point => point.tokens !== null);
  const max = Math.max(1, ...points.map(point => point.tokens ?? 0));
  const x = (index: number) => 18 + index * 53;
  const y = (tokens: number) => 59 - (tokens / max) * 43;
  const format = (value: number) => new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 2 }).format(value);
  const label = (date: string) => `${Number(date.slice(5, 7))}/${Number(date.slice(8, 10))}`;
  const selected = active === null ? null : points[active];
  return <section className="usage-trend" aria-label={zh ? "7日 Token 用量趋势" : "7-day token usage trend"}>
    <div className="usage-section-title"><h2>{zh ? "每日趋势" : "Daily trend"}</h2><span aria-live="polite">{selected ? `${label(selected.date)} · ${selected.tokens === null ? "—" : selected.tokens.toLocaleString()} tokens` : zh ? "含今日 · 今日持续更新" : "Includes today · Live"}</span></div>
    <div className="usage-trend-plot">
      <svg viewBox="0 0 354 82" role="group" aria-label={zh ? "每日 Token 折线图" : "Daily tokens line chart"}>
        {[16, 37.5, 59].map(line => <line key={line} x1="18" x2="336" y1={line} y2={line} stroke="#e3e9dd" strokeDasharray={line === 59 ? undefined : "3 4"} />)}
        {available && <text x="336" y="9" textAnchor="end" className="usage-chart-label">{format(max)}</text>}
        {points.map((point, index) => {
          const next = points[index + 1];
          return <g key={point.date}>
            {point.tokens !== null && next?.tokens != null && <line x1={x(index)} y1={y(point.tokens)} x2={x(index + 1)} y2={y(next.tokens)} stroke="#669f24" strokeWidth="2.2" strokeLinecap="round" />}
            {point.tokens !== null && <g tabIndex={0} role="img" aria-label={`${point.date}: ${point.tokens.toLocaleString()} tokens`} onFocus={() => setActive(index)} onBlur={() => setActive(null)} onMouseEnter={() => setActive(index)} onMouseLeave={() => setActive(null)}>
              <circle cx={x(index)} cy={y(point.tokens)} r="9" fill="transparent" />
              <circle cx={x(index)} cy={y(point.tokens)} r={active === index ? 4 : 2.8} fill="#fcfdfb" stroke="#669f24" strokeWidth="2" />
              <title>{point.date}: {point.tokens.toLocaleString()} tokens</title>
            </g>}
            <text x={x(index)} y="77" textAnchor="middle" className="usage-chart-label">{label(point.date)}</text>
          </g>;
        })}
      </svg>
      {!available && <p className="usage-trend-empty">{zh ? "每日用量数据待接入" : "Daily usage data not connected yet"}</p>}
    </div>
  </section>;
}
