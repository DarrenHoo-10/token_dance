import { useState } from 'react';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { ProfileSettingsPage } from '@/pages/settings/ProfileSettingsPage';
import { MetricGrid } from '@/components/analytics/MetricGrid';
import { FilterSelect } from '@/components/common/FilterSelect';
import { api } from '@/api/client';
import type { PersonalSummaryMetrics, UserProfile } from '@/types/api';

vi.mock('@/context/AuthContext', () => ({ useAuth: () => ({ user: null, setUser: vi.fn(), refreshSession: vi.fn() }) }));
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); localStorage.clear(); });
const profile: UserProfile = { userId: 'test', displayName: 'Current', handle: 'current', avatarUrl: null, bio: '', timezone: 'Asia/Shanghai', locale: 'zh-CN', onboardingCompletedAt: null, profileVersion: 1 };

describe('Product feedback regressions', () => {
  it('ends a stalled settings load, retries, and ignores the stale response', async () => {
    vi.useFakeTimers();
    let resolveOld!: (value: UserProfile) => void;
    const request = vi.spyOn(api, 'getProfile').mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve; })).mockResolvedValue(profile);
    render(<LocaleProvider><NotificationProvider><MemoryRouter><ProfileSettingsPage /></MemoryRouter></NotificationProvider></LocaleProvider>);
    const signal = request.mock.calls[0][0]!;
    await act(async () => { await vi.advanceTimersByTimeAsync(15000); });
    expect(signal.aborted).toBe(true);
    expect(screen.getByText('资料加载超时，请检查网络后重试。')).toBeInTheDocument();
    await act(async () => { fireEvent.click(screen.getByRole('button', { name: '重试' })); });
    expect(screen.getByDisplayValue('Current')).toBeInTheDocument();
    await act(async () => { resolveOld({ ...profile, displayName: 'Stale' }); });
    expect(screen.queryByDisplayValue('Stale')).not.toBeInTheDocument();
  });

  it('keeps a real zero visible and collapses missing public metrics with reasons', () => {
    const metrics = { totalTokens: { value: '0', supported: true }, estimatedCost: { amount: null, supported: false } } as PersonalSummaryMetrics;
    const { container, rerender } = render(<LocaleProvider><MetricGrid metrics={metrics} compact /></LocaleProvider>);
    expect(container.querySelectorAll('.metric-card')).toHaveLength(1);
    expect(screen.getByText('0')).toBeVisible();
    expect(screen.queryByText('N/A')).not.toBeInTheDocument();
    const details = container.querySelector('details')!;
    expect(details.open).toBe(false);
    fireEvent.click(within(details).getByText('更多指标（9 项暂无公开数值）'));
    expect(within(details).getAllByText('公开资料暂未提供')).toHaveLength(9);
    rerender(<LocaleProvider><MetricGrid metrics={{...metrics, totalTokens: { value: null, supported: false }}} compact tokenHidden /></LocaleProvider>);
    expect(screen.getByText('未公开')).toBeInTheDocument();
    expect(screen.queryByText('0')).not.toBeInTheDocument();
  });

  it('supports keyboard selection and disables empty filters', () => {
    function Picker() {
      const [value, setValue] = useState('all');
      return <><FilterSelect label="Agent" value={value} onChange={setValue} options={[{value:'all',label:'全部'},{value:'codex',label:'Codex'}]} /><FilterSelect label="Model" value="all" onChange={() => {}} disabled options={[{value:'all',label:'暂无模型'}]} /></>;
    }
    render(<Picker />);
    const control = screen.getByRole('combobox', { name: 'Agent' });
    fireEvent.keyDown(control, { key: 'ArrowDown' });
    fireEvent.keyDown(control, { key: 'End' });
    fireEvent.keyDown(control, { key: 'Enter' });
    expect(control).toHaveTextContent('Codex');
    fireEvent.click(control);
    fireEvent.keyDown(control, { key: 'Escape' });
    expect(control).toHaveAttribute('aria-expanded', 'false');
    expect(screen.getByRole('combobox', { name: 'Model' })).toBeDisabled();
  });
});
