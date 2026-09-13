import { useEffect, useState } from "react";
import type { TrendPoint, UsageRange } from "../usage-analytics";

const VIEW_W = 354;
const VIEW_H = 82;
const PAD_L = 18;
const PAD_R = 18;

const format = (value: number) => new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 2 }).format(value);

function titleFor(range: UsageRange, zh: boolean) {
  if (range === "today") return zh ? "今日趋势" : "Today's trend";
  if (range === "all") return zh ? "全部时间趋势" : "All-time trend";
  return zh ? "近 7 日趋势" : "7-day trend";
}

function showAxisLabel(range: UsageRange, point: TrendPoint, index: number, points: TrendPoint[]) {
  if (range === "week" || points.length <= 12) return true;
  if (range === "today") return index % 3 === 0 || index === points.length - 1;
  return index === 0 || index === points.length - 1 || (point.key.endsWith("-01") && Number(point.key.slice(5, 7)) % 2 === 1);
}

export function WeeklyTrend({ points, range, lang }: { points: TrendPoint[]; range: UsageRange; lang: "zh" | "en" }) {
  const [hover, setHover] = useState<number | null>(null);
  const [pinned, setPinned] = useState<number | null>(null);
  const zh = lang === "zh";
  const available = points.some(point => point.tokens !== null);
  const max = Math.max(1, ...points.map(point => point.tokens ?? 0));
  const last = points.length <= 1 ? 1 : points.length - 1;
  const x = (index: number) => PAD_L + index * (VIEW_W - PAD_L - PAD_R) / last;
  const y = (tokens: number) => 59 - (tokens / max) * 43;
  const dense = points.length > 32;
  const active = hover ?? pinned;
  const selected = active == null ? null : points[active];
  const total = points.reduce((sum, point) => sum + (point.tokens ?? 0), 0);
  const hasTotal = points.some(point => point.tokens !== null);
  const captionValue = selected ? selected.tokens : hasTotal ? total : null;

  useEffect(() => {
    setHover(null);
    setPinned(null);
  }, [range, points.length]);

  const indexAt = (clientX: number, target: SVGSVGElement) => {
    const rect = target.getBoundingClientRect();
    if (rect.width <= 0 || !available) return null;
    const viewX = (clientX - rect.left) / rect.width * VIEW_W;
    let nearest: number | null = null;
    let best = Infinity;
    points.forEach((point, index) => {
      if (point.tokens === null) return;
      const distance = Math.abs(x(index) - viewX);
      if (distance < best) {
        best = distance;
        nearest = index;
      }
    });
    return nearest;
  };

  let line = "";
  let drawing = false;
  points.forEach((point, index) => {
    if (point.tokens === null) {
      drawing = false;
      return;
    }
    line += `${drawing ? "L" : "M"}${x(index).toFixed(2)} ${y(point.tokens).toFixed(2)} `;
    drawing = true;
  });

  return <section className="usage-trend" aria-label={titleFor(range, zh)}>
    <div className="usage-section-title">
      <h2>{titleFor(range, zh)}</h2>
      <span className="usage-trend-caption" aria-live="polite">
        <strong>{captionValue === null ? "—" : format(captionValue)}</strong>
      </span>
    </div>
    <div className="usage-trend-plot">
      <svg
        viewBox={`0 0 ${VIEW_W} ${VIEW_H}`}
        role="group"
        aria-label={zh ? "Token 用量折线图" : "Token usage line chart"}
        onMouseMove={event => setHover(indexAt(event.clientX, event.currentTarget))}
        onMouseLeave={() => setHover(null)}
        onClick={event => {
          const index = indexAt(event.clientX, event.currentTarget);
          setPinned(current => current === index ? null : index);
        }}
      >
        {[16, 37.5, 59].map(lineY => <line key={lineY} x1={PAD_L} x2={VIEW_W - PAD_R} y1={lineY} y2={lineY} stroke="#eff0f3" />)}
        {available && <text x={PAD_L} y="9" className="usage-chart-label">{format(max)}</text>}
        {available && line && <path d={line.trim()} fill="none" stroke="#6f809b" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />}
        {available && active != null && points[active]?.tokens != null && <line x1={x(active)} x2={x(active)} y1="16" y2="59" stroke="#c5cad4" strokeDasharray="2 3" />}
        {points.map((point, index) => {
          if (point.tokens === null) {
            return showAxisLabel(range, point, index, points) ? <text key={point.key} x={x(index)} y="77" textAnchor="middle" className="usage-chart-label">{point.label}</text> : null;
          }
          return <g key={point.key}>
            {!dense && <g tabIndex={0} role="img" aria-label={`${point.label}: ${point.tokens.toLocaleString()} tokens`} onFocus={() => setHover(index)} onBlur={() => setHover(null)}>
              <circle cx={x(index)} cy={y(point.tokens)} r="9" fill="transparent" />
              <circle cx={x(index)} cy={y(point.tokens)} r={active === index ? 4 : 2.8} fill="#fff" stroke="#6f809b" strokeWidth="2" />
            </g>}
            {dense && active === index && <circle cx={x(index)} cy={y(point.tokens)} r="3.2" fill="#fff" stroke="#6f809b" strokeWidth="2" />}
            {showAxisLabel(range, point, index, points) && <text x={x(index)} y="77" textAnchor="middle" className="usage-chart-label">{point.label}</text>}
          </g>;
        })}
      </svg>
      {!available && <p className="usage-trend-empty">{range === "today" ? (zh ? "小时用量数据待接入" : "Hourly usage data not connected yet") : zh ? "每日用量数据待接入" : "Daily usage data not connected yet"}</p>}
    </div>
  </section>;
}
