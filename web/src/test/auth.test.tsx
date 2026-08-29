import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { LoginPage } from '@/pages/auth/LoginPage';
import { RegisterPage } from '@/pages/auth/RegisterPage';
import { OnboardingPage } from '@/pages/onboarding/OnboardingPage';
import { api } from '@/api/client';

describe('Auth & Onboarding Flow Tests', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders LoginPage and handles login submission', async () => {
    const loginSpy = vi.spyOn(api, 'login').mockResolvedValue({
      user: {
        handle: 'maxbauer',
        displayName: 'Max Bauer',
        avatarUrl: null,
        locale: 'zh-CN',
        onboardingRequired: false,
        productState: 'active_private',
      },
    });

    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/login']}>
              <LoginPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    expect(screen.getByText('使用邮箱登录')).toBeInTheDocument();

    const emailInput = screen.getByPlaceholderText('name@example.com');
    const passwordInput = screen.getByPlaceholderText('••••••••••••');
    const submitBtn = screen.getByRole('button', { name: '登录 TokenDance' });

    fireEvent.change(emailInput, { target: { value: 'test@example.com' } });
    fireEvent.change(passwordInput, { target: { value: 'password123' } });
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(loginSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          email: 'test@example.com',
          password: 'password123',
        })
      );
    });
  });

  it('renders RegisterPage and sends verification code', async () => {
    const codeSpy = vi.spyOn(api, 'requestRegisterCode').mockResolvedValue({
      status: 'pending',
      cooldownSeconds: 60,
    });

    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/register']}>
              <RegisterPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    expect(screen.getByText('创建你的 TokenDance 账户')).toBeInTheDocument();

    const emailInput = screen.getByPlaceholderText('name@example.com');
    const sendCodeBtn = screen.getByRole('button', { name: '获取验证码' });

    fireEvent.change(emailInput, { target: { value: 'newuser@example.com' } });
    fireEvent.click(sendCodeBtn);

    await waitFor(() => {
      expect(codeSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          email: 'newuser@example.com',
        })
      );
    });
  });

  it('renders OnboardingPage with default private visibility', () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: true,
      user: {
        handle: null,
        displayName: 'Token Dancer',
        avatarUrl: null,
        locale: 'zh-CN',
        onboardingRequired: true,
        productState: 'new',
      },
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/onboarding']}>
              <OnboardingPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    expect(screen.getByText('你想以什么身份出现？')).toBeInTheDocument();
    expect(screen.getByText('仅自己可见 (推荐)')).toBeInTheDocument();
    expect(screen.getByText('保存并继续')).toBeInTheDocument();
  });
});
