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
    fireEvent.focus(passwordInput);
    expect(document.querySelector('.login-companions')).toHaveAttribute('data-mood', 'password');
    fireEvent.change(passwordInput, { target: { value: 'password123' } });
    fireEvent.click(screen.getByRole('button', { name: '显示密码' }));
    expect(passwordInput).toHaveAttribute('type', 'text');
    fireEvent.blur(passwordInput);
    expect(document.querySelector('.login-companions')).toHaveAttribute('data-mood', 'password');
    expect(loginSpy).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '隐藏密码' }));
    expect(passwordInput).toHaveAttribute('type', 'password');
    fireEvent.click(screen.getByRole('button', { name: '暂停动画' }));
    expect(document.querySelector('.login-page')).toHaveAttribute('data-motion', 'paused');
    fireEvent.click(submitBtn);

    await waitFor(() => {
      expect(loginSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          email: 'test@example.com',
          password: 'password123',
          keepSignedIn: true,
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

    const passwordInput = screen.getByPlaceholderText('至少 8 个字符');
    fireEvent.focus(passwordInput);
    expect(document.querySelector('.login-companions')).toHaveAttribute('data-mood', 'password');
    fireEvent.click(screen.getByRole('button', { name: '显示密码' }));
    fireEvent.blur(passwordInput);
    expect(passwordInput).toHaveAttribute('type', 'text');
    expect(document.querySelector('.login-companions')).toHaveAttribute('data-mood', 'password');
    expect(codeSpy).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole('button', { name: '隐藏密码' }));
    expect(passwordInput).toHaveAttribute('type', 'password');

    fireEvent.change(emailInput, { target: { value: 'newuser@example.com' } });
    fireEvent.click(sendCodeBtn);

    await waitFor(() => {
      expect(codeSpy).toHaveBeenCalledWith(
        expect.objectContaining({
          email: 'newuser@example.com',
        })
      );
      expect(screen.getByPlaceholderText('6 位数字验证码')).toHaveValue('123456');
      expect(sendCodeBtn).toBeDisabled();
    });
  });

  it('preserves the return path when switching between the shared auth pages', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter initialEntries={['/login?return_to=%2Fme%3Ftab%3Dactivity']} future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <Routes>
                <Route path="/login" element={<LoginPage />} />
                <Route path="/register" element={<RegisterPage />} />
              </Routes>
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    const registerLink = await screen.findByRole('link', { name: '注册' });
    expect(registerLink).toHaveAttribute('href', '/register?return_to=%2Fme%3Ftab%3Dactivity');
    fireEvent.click(registerLink);
    expect(await screen.findByRole('heading', { name: '创建你的 TokenDance 账户' })).toBeInTheDocument();
    expect(screen.getByText('注册', { selector: '[aria-current="page"]' })).toBeInTheDocument();
    const loginLink = screen.getByRole('link', { name: '登录' });
    expect(loginLink).toHaveAttribute('href', '/login?return_to=%2Fme%3Ftab%3Dactivity');
    fireEvent.click(loginLink);
    expect(await screen.findByRole('heading', { name: '使用邮箱登录' })).toBeInTheDocument();
  });

  it('shows registration failures in the shared error notice without losing form values', async () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({ authenticated: false, user: null });
    vi.spyOn(api, 'register').mockRejectedValue(new Error('Registration failed'));
    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
              <RegisterPage />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );
    fireEvent.change(screen.getByPlaceholderText('name@example.com'), { target: { value: 'newuser@example.com' } });
    fireEvent.change(screen.getByPlaceholderText('6 位数字验证码'), { target: { value: '123456' } });
    fireEvent.change(screen.getByPlaceholderText('至少 8 个字符'), { target: { value: 'password123' } });
    fireEvent.click(screen.getByRole('button', { name: '验证并完成注册' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('Registration failed');
    expect(screen.getByPlaceholderText('name@example.com')).toHaveValue('newuser@example.com');
    expect(screen.getByRole('button', { name: '验证并完成注册' })).toBeEnabled();
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
