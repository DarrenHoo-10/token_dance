import React from 'react';
import type { AgentBreakdownItem } from '@/types/api';

export interface AgentBreakdownProps {
  items: AgentBreakdownItem[];
}

function formatTokens(tokenTotal: string): string {
  const num = parseFloat(tokenTotal) || 0;
  if (num >= 1_000_000_000) return (num / 1_000_000_000).toFixed(1) + 'B';
  if (num >= 1_000_000) return (num / 1_000_000).toFixed(1) + 'M';
  if (num >= 1_000) return (num / 1_000).toFixed(1) + 'K';
  return num.toLocaleString();
}

export const AgentBreakdown: React.FC<AgentBreakdownProps> = ({ items }) => {
  if (!items || items.length === 0) {
    return (
      <div style={{ padding: '24px 0', textAlign: 'center', color: 'var(--text-subtle)', fontSize: 12 }}>
        No agent data recorded yet
      </div>
    );
  }

  return (
    <div className="agent-bar-group">
      {items.map((agent, idx) => {
        const itemKey = agent.key || agent.agentId || `agent-${idx}`;
        const itemLabel = agent.displayName || agent.label || agent.key || agent.agentId || 'Unknown Agent';
        const pct = typeof agent.percentage === 'number' ? agent.percentage : parseFloat(agent.percentage) || 0;

        return (
          <div key={itemKey} className="agent-bar-item">
            <div>
              <div className="agent-bar-meta">
                <span>{itemLabel}</span>
                <span className="mono-num">{pct.toFixed(0)}%</span>
              </div>
              <div className="progress-track">
                <div
                  className="progress-fill"
                  style={{ width: `${Math.min(100, Math.max(0, pct))}%` }}
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
