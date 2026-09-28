import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QuotaRings } from '../src/components/UsageDetails';

const observedAt = new Date().toISOString();

describe('Claude quota card', () => {
  it('omits the unavailable reset time while preserving quota utilization', () => {
    const { container } = render(<QuotaRings zh quota={{
      agentId: 'claude-code', observedAt, windows: [
        { label: 'five_hour', provider: 'Claude', windowMinutes: 300, usedPercent: 0, resetsAt: null },
        { label: 'weekly', provider: 'Claude', windowMinutes: 10080, usedPercent: 12, resetsAt: null },
      ],
    }} />);
    expect(screen.getByText(/5 小时额度/)).toBeTruthy();
    expect(screen.getByText(/周额度/)).toBeTruthy();
    expect(screen.getByText('12%')).toBeTruthy();
    expect(container.textContent).not.toContain('重置时间未知');
    expect(container.querySelector('.usage-data-note')).toBeNull();
  });
});
