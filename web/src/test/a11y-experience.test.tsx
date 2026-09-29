import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { api, ApiError } from '@/api/client';
import { teamsApi } from '@/api/teams';
import { LocaleProvider, localeFromBrowser } from '@/context/LocaleContext';
import { NotificationProvider, TOAST_DURATION_MS, useNotification } from '@/context/NotificationContext';
import { BackdropDialog } from '@/components/common/BackdropDialog';
import { RouteTitle } from '@/App';
import { titleKeyFor } from '@/hooks/useRouteTitle';
import { detectDefaultPlatform } from '@/pages/resources/DownloadPage';
import { renderTeamWorkspace, sampleScope, signedInUser } from './teams-test-helpers';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  document.title = '';
});

function ToastButton({ type }: { type: 'success' | 'error' }) {
  const { showToast } = useNotification();
  return <button type="button" onClick={() => showToast('保存失败了', type)}>fire</button>;
}

describe('toasts', () => {
  it('keep errors longer than successes, and can be dismissed with a button', () => {
    vi.useFakeTimers();
    render(<LocaleProvider><NotificationProvider><ToastButton type="error" /></NotificationProvider></LocaleProvider>);
    fireEvent.click(screen.getByText('fire'));
    expect(screen.getByRole('alert')).toHaveTextContent('保存失败了');
    act(() => { vi.advanceTimersByTime(TOAST_DURATION_MS.success + 500); });
    expect(screen.getByRole('alert')).toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(TOAST_DURATION_MS.error); });
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('fire'));
    fireEvent.click(screen.getByRole('button', { name: '关闭提示' }));
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  });

  it('pause while hovered so they can be read', () => {
    vi.useFakeTimers();
    render(<LocaleProvider><NotificationProvider><ToastButton type="success" /></NotificationProvider></LocaleProvider>);
    fireEvent.click(screen.getByText('fire'));
    const toast = screen.getByRole('status');
    fireEvent.mouseEnter(toast);
    act(() => { vi.advanceTimersByTime(TOAST_DURATION_MS.success * 3); });
    expect(screen.getByRole('status')).toBeInTheDocument();
    fireEvent.mouseLeave(screen.getByRole('status'));
    act(() => { vi.advanceTimersByTime(TOAST_DURATION_MS.success + 100); });
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
  });
});

describe('dialog backdrop', () => {
  it('closes only when the press started and ended on the backdrop', () => {
    const onClose = vi.fn();
    render(<BackdropDialog open onClose={onClose}><p>内容</p></BackdropDialog>);
    const dialog = document.querySelector('dialog') as HTMLDialogElement;
    // Selecting text inside, releasing over the backdrop.
    fireEvent.pointerDown(screen.getByText('内容'));
    fireEvent.click(dialog);
    expect(onClose).not.toHaveBeenCalled();
    // A real click on the backdrop.
    fireEvent.pointerDown(dialog);
    fireEvent.click(dialog);
    expect(onClose).toHaveBeenCalledTimes(1);
    // A click inside never closes.
    fireEvent.pointerDown(screen.getByText('内容'));
    fireEvent.click(screen.getByText('内容'));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});

describe('page titles', () => {
  it('maps routes to titles and leaves pages that own their title alone', () => {
    expect(titleKeyFor('/')).toBeNull();
    expect(titleKeyFor('/download')).toBeNull();
    expect(titleKeyFor('/teams/tem_1/members')).toBe('pageTitle.teams');
    expect(titleKeyFor('/settings/devices')).toBe('pageTitle.settings');
    expect(titleKeyFor('/u/fox')).toBe('profile');
    expect(titleKeyFor('/nope/nope')).toBe('pageTitle.notFound');
  });

  it('updates document.title on navigation', async () => {
    render(
      <LocaleProvider>
        <MemoryRouter initialEntries={['/login']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <RouteTitle />
          <Routes><Route path="*" element={null} /></Routes>
        </MemoryRouter>
      </LocaleProvider>,
    );
    await waitFor(() => expect(document.title).toMatch(/登录.*TokenDance/));
  });
});

describe('language and platform defaults', () => {
  it('follows the browser language when nothing is saved', () => {
    expect(localeFromBrowser(['en-GB', 'zh-CN'])).toBe('en-US');
    expect(localeFromBrowser(['zh-TW'])).toBe('zh-CN');
    expect(localeFromBrowser([])).toBe('zh-CN');
  });

  it('picks the Mac package on Macs and Windows elsewhere, iPad and phones excluded', () => {
    expect(detectDefaultPlatform({ userAgent: 'Mozilla/5.0 (Macintosh)', platform: 'MacIntel', maxTouchPoints: 0 })).toBe('mac-arm64');
    expect(detectDefaultPlatform({ userAgent: 'Mozilla/5.0 (Macintosh)', platform: 'MacIntel', maxTouchPoints: 5 })).toBe('windows');
    expect(detectDefaultPlatform({ userAgent: 'Mozilla/5.0 (Windows NT 10.0)', platform: 'Win32', maxTouchPoints: 0 })).toBe('windows');
    expect(detectDefaultPlatform({ userAgent: 'Mozilla/5.0 (Linux; Android 14)', platform: 'Linux armv8l', maxTouchPoints: 5 })).toBe('windows');
  });
});

describe('team page retry', () => {
  it('asks the server again instead of only reloading the team list', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue(signedInUser);
    vi.spyOn(teamsApi, 'getMyTeam').mockResolvedValue(sampleScope());
    vi.spyOn(teamsApi, 'getAnalysis').mockResolvedValue(new Promise(() => {}) as never);
    vi.spyOn(teamsApi, 'getMembers').mockResolvedValue({ members: [], nextCursor: null });
    const otherId = 'tem_zzzzzzzzzzzzzzzzzzzzzzzzzz';
    let failing = true;
    const getTeam = vi.spyOn(teamsApi, 'getTeam').mockImplementation(async () => {
      if (failing) throw new ApiError(500, { code: 'UNKNOWN', messageKey: 'errors.unknown' });
      return sampleScope({ team: { ...sampleScope().team, id: otherId } });
    });

    renderTeamWorkspace(`/teams/${otherId}`);
    const retry = await screen.findByRole('button', { name: '重试' });
    const callsBefore = getTeam.mock.calls.length;
    failing = false;
    fireEvent.click(retry);
    await waitFor(() => expect(getTeam.mock.calls.length).toBeGreaterThan(callsBefore));
    await waitFor(() => expect(screen.queryByRole('button', { name: '重试' })).not.toBeInTheDocument());
  });
});
