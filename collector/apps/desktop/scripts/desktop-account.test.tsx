import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { DesktopAccountCard } from '../src/DesktopAccountCard';
const mock = vi.hoisted(() => ({ session: vi.fn(), login: vi.fn(), logout: vi.fn(), open: vi.fn() }));
vi.mock('../src/account-bridge', () => ({ accountWebsite: () => 'https://example.test/token-dance', getAccountSession: mock.session, loginAccount: mock.login, logoutAccount: mock.logout, openAccountWebsite: mock.open }));
const user = { userId: 'one', displayName: 'Jiayu', handle: 'jayzhang', avatarUrl: '/api/v1/public/avatars/one', onboardingRequired: false };
beforeEach(() => { vi.resetAllMocks(); mock.session.mockResolvedValue({ user: null }); mock.login.mockResolvedValue({ user }); });
afterEach(cleanup);
it('shows the signed-in nickname and actual avatar with a safe fallback', async () => {
  mock.session.mockResolvedValue({ user });
  const { container } = render(<DesktopAccountCard zh />);
  expect(await screen.findByText('Jiayu')).toBeTruthy();
  const img = container.querySelector('img')!;
  expect(img.getAttribute('src')).toBe('https://example.test/token-dance/api/v1/public/avatars/one');
  fireEvent.error(img);
  expect(container.querySelector('img')).toBeNull();
  expect(screen.getByText('J')).toBeTruthy();
});
it.each(['login', 'register'] as const)('%s starts browser authorization and returns the account', async mode => {
  render(<DesktopAccountCard zh />);
  await act(async () => {});
  fireEvent.click(screen.getByRole('button', { name: mode === 'login' ? '登录' : /注册/ }));
  await waitFor(() => expect(mock.login).toHaveBeenCalledWith('https://example.test/token-dance', mode));
  expect(await screen.findByText('Jiayu')).toBeTruthy();
  expect(mock.open).not.toHaveBeenCalled();
});
it('allows login while an initial session check is stalled and ignores its stale result', async () => {
  let finish!: (value: { user: null }) => void;
  mock.session.mockReturnValue(new Promise(resolve => { finish = resolve; }));
  render(<DesktopAccountCard zh />);
  fireEvent.click(screen.getByRole('button', { name: '登录' }));
  expect(await screen.findByText('Jiayu')).toBeTruthy();
  await act(async () => { finish({ user: null }); });
  expect(screen.getByText('Jiayu')).toBeTruthy();
});
it.each([['LOGIN_TIMEOUT', '10 分钟'], ['BROWSER_OPEN_FAILED', '无法打开浏览器']])('clears waiting state on %s and permits retry', async (code, message) => {
  mock.login.mockRejectedValueOnce(code);
  render(<DesktopAccountCard zh />);
  await act(async () => {});
  fireEvent.click(screen.getByRole('button', { name: '登录' }));
  expect((await screen.findByRole('alert')).textContent).toContain(message);
  fireEvent.click(screen.getByRole('button', { name: '登录' }));
  expect(await screen.findByText('Jiayu')).toBeTruthy();
});
