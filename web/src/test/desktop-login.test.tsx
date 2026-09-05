import { StrictMode } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { LoginPage } from '@/pages/auth/LoginPage';
import { DesktopLoginPage, desktopLoginRequest } from '@/pages/auth/DesktopLoginPage';
import { api } from '@/api/client';

const request = { redirectUri: 'http://127.0.0.1:49152/callback', codeChallenge: 'a'.repeat(64), state: 'b'.repeat(64) };
const params = new URLSearchParams({ redirect_uri: request.redirectUri, code_challenge: request.codeChallenge, state: request.state });
const returnTo = `/desktop-login?${params}`;
const user = { userId: 'desktop-user', handle: 'desktop', displayName: 'Desktop User', avatarUrl: null, locale: 'zh-CN' as const, onboardingRequired: false, productState: 'active_private' as const };
function show(initial = `/login?return_to=${encodeURIComponent(returnTo)}`) {
  return render(<StrictMode><LocaleProvider><NotificationProvider><AuthProvider><MemoryRouter initialEntries={[initial]}>
    <Routes><Route path="/login" element={<LoginPage />} /><Route path="/desktop-login" element={<DesktopLoginPage />} /></Routes>
  </MemoryRouter></AuthProvider></NotificationProvider></LocaleProvider></StrictMode>);
}
describe('Desktop browser login', () => {
  beforeEach(() => vi.restoreAllMocks());
  it('reuses the browser session automatically without asking for credentials twice', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    const login = vi.spyOn(api, 'login');
    const authorize = vi.spyOn(api, 'authorizeDesktop').mockImplementation(() => new Promise(() => {}));
    show();
    await waitFor(() => expect(authorize).toHaveBeenCalledTimes(1));
    expect(authorize).toHaveBeenCalledWith(request);
    expect(login).not.toHaveBeenCalled();
    expect(screen.queryByLabelText('密码')).not.toBeInTheDocument();
  });
  it('completes web login before handing off when the browser is signed out', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
    vi.spyOn(api, 'login').mockResolvedValue({ user, returnTo });
    const authorize = vi.spyOn(api, 'authorizeDesktop').mockImplementation(() => new Promise(() => {}));
    show();
    fireEvent.change(await screen.findByLabelText('邮箱'), { target: { value: 'desktop@example.com' } });
    fireEvent.change(screen.getByLabelText('密码'), { target: { value: 'fixture-password' } });
    expect(authorize).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '登录 TokenDance' }));
    await waitFor(() => expect(authorize).toHaveBeenCalledTimes(1));
    expect(authorize).toHaveBeenCalledWith(request);
  });
  it('rejects remote callbacks and incomplete requests', () => {
    for (const uri of ['https://evil.example/callback', 'http://localhost:1234/callback', 'http://127.0.0.1:1234/other', 'http://127.0.0.1:1234/callback?next=evil', 'http://127.0.0.1:0/callback']) {
      const invalid = new URLSearchParams(params); invalid.set('redirect_uri', uri);
      expect(desktopLoginRequest(invalid)).toBeNull();
    }
    expect(desktopLoginRequest(new URLSearchParams())).toBeNull();
    expect(desktopLoginRequest(params)).toEqual(request);
  });
  it('shows a retry instruction when the handoff service fails', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: true, user });
    vi.spyOn(api, 'authorizeDesktop').mockRejectedValue(new Error('Unavailable'));
    show(returnTo);
    expect(await screen.findByText('未能完成桌面登录')).toBeInTheDocument();
  });
});
