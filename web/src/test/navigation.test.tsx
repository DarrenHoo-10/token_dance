import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { NotificationProvider } from '@/context/NotificationContext';
import { AuthProvider } from '@/context/AuthContext';
import { Navbar } from '@/components/layout/Navbar';
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
