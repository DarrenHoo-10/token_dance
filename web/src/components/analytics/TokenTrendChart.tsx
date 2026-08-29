import React from 'react';
import { useLocale } from '@/context/LocaleContext';
import type { TokenTrendItem } from '@/types/api';

export interface TokenTrendChartProps {
  trends: TokenTrendItem[];
  mode?: 'total' | 'structure';
  height?: number;
}

export const TokenTrendChart: React.FC<TokenTrendChartProps> = ({
  trends,
  height = 180,
}) => {
  const { t } = useLocale();

  if (!trends || trends.length === 0) {
    return (
      <div
        style={{
          height,
          display: 'grid',
          placeItems: 'center',
          color: 'var(--text-subtle)',
          fontSize: 12,
        }}
      >
        {t('dashboard.noTrendData')}
      </div>
    );
  }

  // Parse points
  const points = trends.map((item) => {
    const total = parseFloat(item.tokenTotal || '0') || 0;
    return {
      date: item.date,
      total,
      input: parseFloat(item.inputTokens || '0') || 0,
      output: parseFloat(item.outputTokens || '0') || 0,
      cache: parseFloat(item.cacheReadTokens || '0') || 0,
    };
  });

  const maxVal = Math.max(...points.map((p) => p.total), 100);
  const minVal = 0;
  const range = maxVal - minVal || 1;

  const width = 700;
  const paddingX = 20;
  const paddingY = 20;
  const innerWidth = width - paddingX * 2;
  const innerHeight = height - paddingY * 2;

  const stepX = innerWidth / (points.length > 1 ? points.length - 1 : 1);

  const coords = points.map((p, idx) => {
    const x = paddingX + idx * stepX;
    const y = paddingY + innerHeight - ((p.total - minVal) / range) * innerHeight;
    return { x, y, ...p };
  });

  const linePath = coords.map((c, idx) => `${idx === 0 ? 'M' : 'L'} ${c.x.toFixed(1)} ${c.y.toFixed(1)}`).join(' ');
  const areaPath = `${linePath} L ${coords[coords.length - 1].x.toFixed(1)} ${height - paddingY} L ${coords[0].x.toFixed(1)} ${height - paddingY} Z`;

  // Format axis dates
  const axisIndices = [
    0,
    Math.floor(points.length * 0.25),
    Math.floor(points.length * 0.5),
    Math.floor(points.length * 0.75),
    points.length - 1,
  ].filter((idx, i, arr) => arr.indexOf(idx) === i && idx < points.length);

  return (
    <div>
      <div style={{ width: '100%', height, position: 'relative' }}>
        <svg
          viewBox={`0 0 ${width} ${height}`}
          preserveAspectRatio="none"
          style={{ width: '100%', height: '100%', overflow: 'visible' }}
        >
          {/* Subtle grid lines */}
          <line
            x1={paddingX}
            y1={paddingY}
            x2={width - paddingX}
            y2={paddingY}
            stroke="#e6ede6"
            strokeWidth="1"
            strokeDasharray="4 4"
          />
          <line
            x1={paddingX}
            y1={paddingY + innerHeight / 2}
            x2={width - paddingX}
            y2={paddingY + innerHeight / 2}
            stroke="#e6ede6"
            strokeWidth="1"
            strokeDasharray="4 4"
          />
          <line
            x1={paddingX}
            y1={height - paddingY}
            x2={width - paddingX}
            y2={height - paddingY}
            stroke="#d8e0d8"
            strokeWidth="1"
          />

          {/* Crisp flat area fill (flat subtle tint, no heavy gradient) */}
          <path d={areaPath} fill="#effad2" opacity="0.65" />

          {/* Crisp stroke line */}
          <path
            d={linePath}
            fill="none"
            stroke="#4b8500"
            strokeWidth="2.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />

          {/* Data vertex dots */}
          {coords.map((c, i) => (
            <circle
              key={i}
              cx={c.x}
              cy={c.y}
              r="3"
              fill="#111512"
              stroke="#b9f600"
              strokeWidth="1.5"
            />
          ))}
        </svg>
      </div>

      {/* Axis dates */}
      <div className="chart-axis-labels">
        {axisIndices.map((idx) => (
          <span key={idx}>{points[idx]?.date}</span>
        ))}
      </div>
    </div>
  );
};
