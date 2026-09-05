import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
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
      returnTo: '/me',
    });

    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/login']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <LoginPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByText('使用邮箱登录')).toBeInTheDocument();
    });

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
      testCode: '123456',
    });

    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/register']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <RegisterPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByText('创建你的 TokenDance 账户')).toBeInTheDocument();
    });

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
      expect(screen.getByPlaceholderText('6 位数字验证码')).toHaveValue('123456');
    });
  });

  it('renders OnboardingPage with default private visibility', async () => {
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
            <MemoryRouter initialEntries={['/onboarding']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <OnboardingPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByText('创建你的公开身份')).toBeInTheDocument();
      expect(screen.getByText('仅自己可见 (推荐)')).toBeInTheDocument();
    });
  });

  it('注册成功后直接进入仪表盘，不再进入 onboarding 设置页', async () => {
    const registerSpy = vi.spyOn(api, 'register').mockResolvedValue({
      user: {
        handle: 'newuser',
        displayName: 'Token Dancer',
        avatarUrl: null,
        locale: 'zh-CN',
        onboardingRequired: false,
        productState: 'active_private',
      },
      returnTo: '/me',
    });

    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/register']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <Routes>
                <Route path="/register" element={<RegisterPage />} />
                <Route path="/onboarding" element={<div>onboarding-shown</div>} />
                <Route path="/me" element={<div>dashboard-reached</div>} />
              </Routes>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    await waitFor(() => {
      expect(screen.getByText('创建你的 TokenDance 账户')).toBeInTheDocument();
    });

    fireEvent.change(screen.getByPlaceholderText('name@example.com'), { target: { value: 'newuser@example.com' } });
    fireEvent.change(screen.getByPlaceholderText('6 位数字验证码'), { target: { value: '123456' } });
    fireEvent.change(screen.getByPlaceholderText('至少 8 个字符'), { target: { value: 'password123' } });
    fireEvent.click(screen.getByRole('button', { name: '验证并完成注册' }));

    await waitFor(() => {
      expect(screen.getByText('dashboard-reached')).toBeInTheDocument();
    });
    expect(screen.queryByText('onboarding-shown')).not.toBeInTheDocument();
    expect(registerSpy).toHaveBeenCalledWith(
      expect.objectContaining({
        locale: 'zh-CN',
        timezone: expect.any(String),
      })
    );
  });
});
