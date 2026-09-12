import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SoftwareUpdateCard, UpdateNotice } from '../src/components/SoftwareUpdate';
import type { UpdateStatus } from '../src/update-state';

const mock = vi.hoisted(() => ({ status: null as UpdateStatus | null, install: vi.fn(), check: vi.fn(), toggle: vi.fn(), download: vi.fn() }));
vi.mock('../src/update-state', async importOriginal => ({
  ...await importOriginal<typeof import('../src/update-state')>(),
  useUpdates: () => mock.status, installUpdate: mock.install, checkUpdates: mock.check, setAutoUpdate: mock.toggle, openUpdateDownloads: mock.download,
}));
beforeEach(() => {
  vi.clearAllMocks();
  mock.status = { currentVersion: '0.1.12', version: '0.1.13', notes: '修复额度展示\n<script>alert(1)</script>', publishedAt: '2026-09-06T00:00:00Z', phase: 'available', autoUpdate: true, progress: 0, checkedAt: null, error: null, supported: true };
  mock.install.mockResolvedValue(undefined); mock.check.mockResolvedValue(undefined); mock.toggle.mockResolvedValue(undefined);
  mock.download.mockResolvedValue(undefined);
});
afterEach(cleanup);
describe('update notice', () => {
  it('offers a working manual download when macOS requires an update', async () => {
    mock.status = { ...mock.status!, supported: false, required: true, minimumVersion: '0.1.28', version: null, phase: 'idle' };
    render(<UpdateNotice zh />);
    expect(screen.getByRole('alertdialog')).toBeTruthy();
    await userEvent.click(screen.getByRole('button', { name: '下载安装包' }));
    expect(mock.download).toHaveBeenCalledTimes(1);
    expect(mock.install).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole('button', { name: '重新检查' }));
    expect(mock.check).toHaveBeenCalledTimes(1);
  });
  it('keeps the native installer for required Windows updates', async () => {
    mock.status = { ...mock.status!, required: true, minimumVersion: '0.1.28' };
    render(<UpdateNotice zh />);
    await userEvent.click(screen.getByRole('button', { name: '立即更新' }));
    expect(mock.install).toHaveBeenCalledTimes(1);
    expect(mock.download).not.toHaveBeenCalled();
  });
  it('stays hidden without a newer version and appears without opening itself', () => {
    mock.status!.version = null;
    const { rerender } = render(<UpdateNotice zh />);
    expect(screen.queryByText('NEW')).toBeNull();
    mock.status!.version = '0.1.13'; rerender(<UpdateNotice zh />);
    expect(screen.getByText('NEW')).toBeTruthy();
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.queryByText('?')).toBeNull();
  });
  it('renders untrusted notes as text and Escape closes only the popover', async () => {
    const user = userEvent.setup();
    render(<UpdateNotice zh />);
    await user.click(screen.getByText('NEW'));
    expect(screen.getByRole('dialog').textContent).toContain('<script>alert(1)</script>');
    expect(screen.getByRole('dialog').querySelector('script')).toBeNull();
    const hideWindow = vi.fn(); window.addEventListener('keydown', hideWindow);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(hideWindow).not.toHaveBeenCalled(); window.removeEventListener('keydown', hideWindow);
    expect(document.activeElement).toBe(screen.getByText('NEW'));
  });
  it('clicks outside dismiss notes but preserve NEW; update only runs on its button', async () => {
    const user = userEvent.setup(); render(<UpdateNotice zh />);
    await user.click(screen.getByText('NEW')); await user.click(document.body);
    expect(screen.queryByRole('dialog')).toBeNull(); expect(mock.install).not.toHaveBeenCalled();
    await user.click(screen.getByText('NEW')); await user.click(screen.getByRole('button', { name: '立即更新' }));
    expect(mock.install).toHaveBeenCalledTimes(1);
  });
  it('disables updates while downloading and surfaces retryable installation errors', async () => {
    const user = userEvent.setup(); mock.status!.phase = 'downloading'; mock.status!.progress = 60;
    const { rerender } = render(<UpdateNotice zh />); await user.click(screen.getByText('NEW'));
    expect((screen.getByRole('button', { name: '更新中…' }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole('progressbar').getAttribute('value')).toBe('60');
    mock.status!.phase = 'ready'; rerender(<UpdateNotice zh />); mock.install.mockRejectedValue('install_failed');
    await user.click(screen.getByRole('button', { name: '重启并更新' }));
    expect(await screen.findByRole('alert')).toBeTruthy();
    expect(screen.getByText('NEW')).toBeTruthy();
  });
});
describe('settings updates', () => {
  it('shows a shared download failure once and clears it when retrying', async () => {
    const user = userEvent.setup();
    mock.install.mockImplementation(async () => {
      mock.status = { ...mock.status!, phase: 'error', error: 'network' };
      throw 'network';
    });
    render(<SoftwareUpdateCard zh />);
    await user.click(screen.getByRole('button', { name: '立即更新' }));
    expect(screen.getAllByText('网络暂不可用，请稍后重试')).toHaveLength(1);
    expect(screen.getByRole('alert').textContent).toContain('网络暂不可用');
    mock.check.mockImplementation(async () => {
      mock.status = { ...mock.status!, phase: 'ready', error: null };
    });
    await user.click(screen.getByRole('button', { name: '检查更新' }));
    expect(screen.queryByText('网络暂不可用，请稍后重试')).toBeNull();
    expect(screen.getByRole('button', { name: '重启并更新' })).toBeTruthy();
  });

  it('describes a download connection failure separately from checking for updates', () => {
    mock.status = { ...mock.status!, phase: 'error', error: 'download_network' };
    render(<SoftwareUpdateCard zh />);
    expect(screen.getByRole('alert').textContent).toContain('安装包下载失败');
  });

  it('checks manually and saves the automatic-update switch', async () => {
    const user = userEvent.setup(); render(<SoftwareUpdateCard zh />);
    await user.click(screen.getByRole('button', { name: '检查更新' }));
    expect(mock.check).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole('switch', { name: '自动更新' }));
    expect(mock.toggle).toHaveBeenCalledWith(false);
  });
  it('does not claim the switch saved when persistence fails', async () => {
    const user = userEvent.setup(); mock.toggle.mockRejectedValue('storage'); render(<SoftwareUpdateCard zh />);
    await user.click(screen.getByRole('switch', { name: '自动更新' }));
    await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('无法保存'));
    expect(screen.getByRole('switch').getAttribute('aria-checked')).toBe('true');
  });
  it('keeps policy checks and manual downloads available without a native updater', async () => {
    mock.status!.supported = false; render(<SoftwareUpdateCard zh={false} />);
    expect((screen.getByRole('switch', { name: 'Automatic updates' }) as HTMLButtonElement).disabled).toBe(true);
    await userEvent.click(screen.getByRole('button', { name: 'Check for updates' }));
    expect(mock.check).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole('button', { name: 'Download installer' }));
    expect(mock.download).toHaveBeenCalledTimes(1);
    expect(mock.install).not.toHaveBeenCalled();
  });
});
