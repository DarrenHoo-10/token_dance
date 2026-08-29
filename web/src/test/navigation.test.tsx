import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { LocaleProvider, useLocale } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { Navbar } from '@/components/layout/Navbar';
import { LocaleSwitcher } from '@/components/common/LocaleSwitcher';
import { CompareTray } from '@/components/compare/CompareTray';
import { api } from '@/api/client';

describe('Navigation & Locale Switching Tests', () => {
  it('renders navbar links and switches language', () => {
    vi.spyOn(api, 'getSession').mockResolvedValue({
      authenticated: false,
      user: null,
    });

    render(
      <LocaleProvider>
        <NotificationProvider>
          <AuthProvider>
            <MemoryRouter>
              <Navbar />
            </MemoryRouter>
          </AuthProvider>
        </NotificationProvider>
      </LocaleProvider>
    );

    expect(screen.getByText('TokenBoard')).toBeInTheDocument();
    expect(screen.getByText('排行榜')).toBeInTheDocument();

    const enBtn = screen.getByText('EN');
    fireEvent.click(enBtn);

    expect(screen.getByText('Leaderboard')).toBeInTheDocument();

    const zhBtn = screen.getByText('中文');
    fireEvent.click(zhBtn);
    expect(screen.getByText('排行榜')).toBeInTheDocument();
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
        <MemoryRouter initialEntries={['/leaderboard?window=7d&metric=code_lines']}>
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

  it('renders CompareTray and triggers remove and clear', () => {
    const handleRemove = vi.fn();
    const handleClear = vi.fn();

    render(
      <LocaleProvider>
        <MemoryRouter>
          <CompareTray
            handles={['maxbauer', 'sophiadev']}
            onRemove={handleRemove}
            onClear={handleClear}
          />
        </MemoryRouter>
      </LocaleProvider>
    );

    expect(screen.getByText('@maxbauer')).toBeInTheDocument();
    expect(screen.getByText('@sophiadev')).toBeInTheDocument();

    const clearBtn = screen.getByText('清空');
    fireEvent.click(clearBtn);
    expect(handleClear).toHaveBeenCalled();

    const removeBtn = screen.getByLabelText('Remove @maxbauer');
    fireEvent.click(removeBtn);
    expect(handleRemove).toHaveBeenCalledWith('maxbauer');
  });
});
