import React from 'react';
import { HarnessMark } from '@/components/common/HarnessMark';
import { resolveHarnessBrand } from '@/components/common/harnessBrand';
import { usageColor } from '@/utils/usageColors';
import { useLocale } from '@/context/LocaleContext';
import type { AgentBreakdownItem } from '@/types/api';

export interface AgentBreakdownProps {
  items: AgentBreakdownItem[];
  variant?: 'bars' | 'donut';
}

function formatTokens(tokenTotal: string): string {
  const num = parseFloat(tokenTotal) || 0;
  if (num >= 1_000_000_000) return (num / 1_000_000_000).toFixed(1) + 'B';
  if (num >= 1_000_000) return (num / 1_000_000).toFixed(1) + 'M';
  if (num >= 1_000) return (num / 1_000).toFixed(1) + 'K';
  return num.toLocaleString();
}

export const AgentBreakdown: React.FC<AgentBreakdownProps> = ({ items, variant = 'bars' }) => {
  const { t } = useLocale();

  if (!items || items.length === 0) {
    return (
      <div style={{ padding: '24px 0', textAlign: 'center', color: 'var(--text-subtle)', fontSize: 12 }}>
        {t('dashboard.noAgentData')}
      </div>
    );
  }

  if (variant === 'donut') {
    const normalized = items.map((agent, idx) => {
      const percentage = typeof agent.percentage === 'number' ? agent.percentage : parseFloat(agent.percentage) || 0;
      const key = agent.key || agent.agentId || `agent-${idx}`;
      const label = agent.displayName || agent.label || agent.key || agent.agentId || t('dashboard.unknownAgent');
      const brand = resolveHarnessBrand(agent.agentId || agent.key, label);
      return {
        key,
        label,
        percentage: Math.max(0, percentage),
        tokenTotal: agent.tokenTotal,
        color: brand.known ? brand.color : usageColor(key),
      };
    });
    const totalPct = normalized.reduce((sum, item) => sum + item.percentage, 0) || 1;
    let cursor = 0;
    const stops = normalized.map((item) => {
      const start = cursor;
      cursor += item.percentage / totalPct * 100;
      return `${item.color} ${start}% ${cursor}%`;
    }).join(', ');
    const totalTokens = normalized.reduce((sum, item) => sum + (Number(item.tokenTotal) || 0), 0);
    return <div className="agent-donut-breakdown">
      <div className="agent-donut-composition">
        <div className="agent-donut-chart" style={{ background: `conic-gradient(${stops})` }} role="img" aria-label={normalized.map(item => `${item.label} ${item.percentage.toFixed(0)}%`).join(', ')}>
          <div><span>{t('metrics.totalTokens')}</span><strong>{formatTokens(String(totalTokens))}</strong></div>
        </div>
        <div className="agent-donut-legend">{normalized.map(item => <div key={item.key}><i style={{ background: item.color }} /><span>{item.label}</span><strong>{item.percentage.toFixed(0)}%</strong></div>)}</div>
      </div>
      <div className="agent-donut-rows">{normalized.map(item => <div key={item.key}><span>{item.label}</span><strong>{formatTokens(item.tokenTotal)}</strong><small>Token</small></div>)}</div>
    </div>;
  }

  return (
    <div className="agent-bar-group">
      {items.map((agent, idx) => {
        const itemKey = agent.key || agent.agentId || `agent-${idx}`;
        const itemLabel = agent.displayName || agent.label || agent.key || agent.agentId || t('dashboard.unknownAgent');
        const brand = resolveHarnessBrand(agent.agentId || agent.key, itemLabel);
        const pct = typeof agent.percentage === 'number' ? agent.percentage : parseFloat(agent.percentage) || 0;

        return (
          <div key={itemKey} className="agent-bar-item">
            <div>
              <div className="agent-bar-meta">
                <span className="agent-bar-name">{brand.known ? <HarnessMark agentId={agent.agentId || agent.key} label={itemLabel} size="sm" /> : null}{itemLabel}</span>
                <span className="mono-num">{pct.toFixed(0)}%</span>
              </div>
              <div className="progress-track">
                <div
                  className="progress-fill"
                  style={{ width: `${Math.min(100, Math.max(0, pct))}%`, background: brand.known ? brand.color : usageColor(itemKey) }}
                />
              </div>
            </div>
            <div className="agent-bar-value mono-num">
              {formatTokens(agent.tokenTotal)}
            </div>
          </div>
        );
      })}
    </div>
  );
};
