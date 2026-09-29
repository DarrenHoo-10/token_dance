import { afterEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { api } from '@/api/client';
import { AuthProvider } from '@/context/AuthContext';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { LoginPage } from '@/pages/auth/LoginPage';

afterEach(() => vi.restoreAllMocks());

const Where = () => {
  const { pathname, search } = useLocation();
  return <div data-testid="where">{pathname}{search}</div>;
};

function loginAs(user: { onboardingRequired: boolean; productState: string }) {
  vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
  vi.spyOn(api, 'login').mockResolvedValue({
    user: { userId: 'usr_1', handle: 'new', displayName: 'New', avatarUrl: null, locale: 'zh-CN', ...user },
    returnTo: '/teams',
  } as never);
  render(
    <LocaleProvider>
      <NotificationProvider>
        <AuthProvider>
          <MemoryRouter initialEntries={['/login?return_to=%2Fteams']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
            <Routes>
              <Route path="/login" element={<LoginPage />} />
              <Route path="*" element={<Where />} />
            </Routes>
          </MemoryRouter>
        </AuthProvider>
      </NotificationProvider>
    </LocaleProvider>,
  );
}

async function submit() {
  fireEvent.change(await screen.findByLabelText('邮箱'), { target: { value: 'a@b.co' } });
  fireEvent.change(document.querySelector('input[type="password"]') as HTMLInputElement, { target: { value: 'secret-pass' } });
  fireEvent.submit(document.querySelector('form') as HTMLFormElement);
}

describe('login redirect', () => {
  it('sends a user who still needs onboarding to onboarding, not straight to return_to', async () => {
    loginAs({ onboardingRequired: true, productState: 'new' });
    await submit();
    expect(await screen.findByTestId('where')).toHaveTextContent('/onboarding?return_to=%2Fteams');
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByTestId('where')).toHaveTextContent('/onboarding?return_to=%2Fteams');
  });

  it('sends everyone else to return_to', async () => {
    loginAs({ onboardingRequired: false, productState: 'active_private' });
    await submit();
    expect(await screen.findByTestId('where')).toHaveTextContent('/teams');
  });
});

describe('already signed in', () => {
  it('opening /login while signed in but not onboarded goes to onboarding', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: true,
      user: { userId: 'usr_1', handle: 'new', displayName: 'New', avatarUrl: null, locale: 'zh-CN', onboardingRequired: true, productState: 'new' },
    } as never);
    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/login?return_to=%2Fteams']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route path="*" element={<Where />} />
              </Routes>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>,
    );
    expect(await screen.findByTestId('where')).toHaveTextContent('/onboarding?return_to=%2Fteams');
  });
});
