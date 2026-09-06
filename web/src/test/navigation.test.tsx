import React from 'react';
import { afterEach, describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { LocaleProvider, useLocale } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { Navbar } from '@/components/layout/Navbar';
import { RootRedirect } from '@/App';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { api } from '@/api/client';

afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe('Navigation & Locale Switching Tests', () => {
  it('renders navbar links and switches language', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <Navbar />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByText('TokenBoard')).toBeInTheDocument();
      expect(screen.queryByRole('link', { name: '社区' })).not.toBeInTheDocument();
      expect(screen.queryByRole('link', { name: '团队' })).not.toBeInTheDocument();
    });
    expect(screen.queryByText('发现')).not.toBeInTheDocument();
    expect(screen.queryByRole('search')).not.toBeInTheDocument();

    const enBtn = screen.getByText('EN');
    fireEvent.click(enBtn);

    expect(screen.queryByRole('link', { name: 'Community' })).not.toBeInTheDocument();
    expect(screen.queryByRole('link', { name: 'Teams' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Sign In' })).toBeInTheDocument();

    const zhBtn = screen.getByText('中文');
    fireEvent.click(zhBtn);
    expect(screen.queryByRole('link', { name: '社区' })).not.toBeInTheDocument();
  });

  it('preserves query params, route, and input state across locale switches', () => {
    const TestPage = () => {
      const [text, setText] = React.useState('my-search-term');
      const location = useLocation();
      const { t } = useLocale();

      return (
        <div>
          <span data-testid="route-label">{t('nav.leaderboard')}</span>
          <span data-testid="current-path">{location.pathname + location.search}</span>
          <input
            data-testid="search-input"
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <LocaleSwitcher />
        </div>
      );
    };

    render(
      <LocaleProvider>
        <MemoryRouter initialEntries={['/leaderboard?window=7d&metric=code_lines']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <TestPage />
        </MemoryRouter>
      </LocaleProvider>
    );

    expect(screen.getByTestId('route-label')).toHaveTextContent('排行榜');
    expect(screen.getByTestId('current-path')).toHaveTextContent('/leaderboard?window=7d&metric=code_lines');
    expect(screen.getByTestId('search-input')).toHaveValue('my-search-term');

    // Type new text
    fireEvent.change(screen.getByTestId('search-input'), { target: { value: 'custom query' } });
    expect(screen.getByTestId('search-input')).toHaveValue('custom query');

    // Switch to EN
    fireEvent.click(screen.getByText('EN'));

    // Text changed to English
    expect(screen.getByTestId('route-label')).toHaveTextContent('Leaderboard');
    // URL and search query params are completely preserved
    expect(screen.getByTestId('current-path')).toHaveTextContent('/leaderboard?window=7d&metric=code_lines');
    // Input state is completely preserved
    expect(screen.getByTestId('search-input')).toHaveValue('custom query');

    // Switch back to ZH
    fireEvent.click(screen.getByText('中文'));
    expect(screen.getByTestId('route-label')).toHaveTextContent('排行榜');
    expect(screen.getByTestId('current-path')).toHaveTextContent('/leaderboard?window=7d&metric=code_lines');
    expect(screen.getByTestId('search-input')).toHaveValue('custom query');
  });

  it('opens the authenticated user menu and closes it with Escape', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: true,
      user: {
        userId: 'user-1',
        handle: 'maxbauer',
        displayName: 'Max Bauer',
        avatarUrl: null,
        locale: 'en-US',
        onboardingRequired: false,
        productState: 'active_public',
      },
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <Navbar />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    const menuTrigger = await screen.findByRole('button', { name: 'User menu' });
    expect(menuTrigger).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(menuTrigger);

    expect(menuTrigger).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByRole('menu')).toBeInTheDocument();
    expect(screen.getByRole('menuitem', { name: 'Personal Data' })).toHaveAttribute(
      'href',
      '/me'
    );
    expect(screen.getByRole('menuitem', { name: 'Settings' })).toHaveAttribute(
      'href',
      '/settings/privacy'
    );
    expect(screen.getByRole('menuitem', { name: 'Collector Devices' })).toHaveAttribute(
      'href',
      '/settings/devices'
    );
    expect(screen.getByRole('menuitem', { name: 'Sign Out' })).toBeInTheDocument();

    fireEvent.keyDown(document, { key: 'Escape' });
    expect(screen.queryByRole('menu')).not.toBeInTheDocument();
    expect(menuTrigger).toHaveAttribute('aria-expanded', 'false');
  });

  it('sends signed-out visitors from the site home to login', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    const LocationProbe = () => {
      const location = useLocation();
      return <div data-testid="current-path">{location.pathname + location.search}</div>;
    };

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <LocationProbe />
              <Routes>
                <Route path="/" element={<RootRedirect />} />
                <Route path="/login" element={<div>login-page</div>} />
                <Route path="/leaderboard" element={<div>home-page</div>} />
              </Routes>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByTestId('current-path')).toHaveTextContent('/login?return_to=/');
    });
  });

  it('sends signed-in visitors from the site home to the official homepage', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: true,
      user: {
        handle: 'maxbauer',
        displayName: 'Max Bauer',
        avatarUrl: null,
        locale: 'zh-CN',
        onboardingRequired: false,
        productState: 'active_private',
      },
    });

    const LocationProbe = () => {
      const location = useLocation();
      return <div data-testid="current-path">{location.pathname + location.search}</div>;
    };

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <LocationProbe />
              <Routes>
                <Route path="/" element={<RootRedirect />} />
                <Route path="/login" element={<div>login-page</div>} />
                <Route path="/leaderboard" element={<div>home-page</div>} />
              </Routes>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByTestId('current-path')).toHaveTextContent('/leaderboard');
    });
  });

});
