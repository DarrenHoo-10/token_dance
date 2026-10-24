import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QuotaRings } from '../src/components/UsageDetails';

const observedAt = new Date().toISOString();

describe('Claude quota card', () => {
  it('omits the unavailable reset time while showing remaining quota', () => {
    const { container } = render(<QuotaRings zh quota={{
      agentId: 'claude-code', observedAt, windows: [
        { label: 'five_hour', provider: 'Claude', windowMinutes: 300, usedPercent: 0, resetsAt: null },
        { label: 'weekly', provider: 'Claude', windowMinutes: 10080, usedPercent: 12, resetsAt: null },
      ],
    }} />);
    expect(screen.getByText(/5 小时额度/)).toBeTruthy();
    expect(screen.getByText(/周额度/)).toBeTruthy();
    expect(screen.getByText('88%')).toBeTruthy();
    expect(container.textContent).not.toContain('重置时间未知');
    expect(container.querySelector('.usage-data-note')).toBeNull();
  });
});

describe('Quota row without a signed-in plan', () => {
  it('hides the ring entirely when the agent is not connected', () => {
    const { container } = render(<QuotaRings zh quota={{ agentId: 'zcode', observedAt, windows: [], status: 'not_connected' }} />);
    expect(container.firstChild).toBeNull();
  });

  it('still asks for re-login when a signed-in session expires', () => {
    render(<QuotaRings zh quota={{ agentId: 'zcode', observedAt, windows: [], status: 'auth_required' }} />);
    expect(screen.getByText('请在 ZCode 重新登录')).toBeTruthy();
  });
});

describe('Qoder credits ring', () => {
  it('renders the monthly credits window with its reset countdown', () => {
    const resetsAt = Math.floor(Date.now() / 1000) + 5 * 86400;
    render(<QuotaRings zh quota={{ agentId: 'qoder', observedAt, status: 'ready', windows: [
      { label: 'credits', windowMinutes: 43200, usedPercent: 6, resetsAt },
    ] }} />);
    expect(screen.getByText(/积分额度/)).toBeTruthy();
    expect(screen.getByText('94%')).toBeTruthy();
    expect(screen.getByText(/5 天后重置/)).toBeTruthy();
  });

  it('flags an expired cookie as needing a fresh sign-in', () => {
    render(<QuotaRings zh quota={{ agentId: 'qoder', observedAt, windows: [], status: 'auth_required' }} />);
    expect(screen.getByText('请在 Qoder 重新登录')).toBeTruthy();
  });
});
