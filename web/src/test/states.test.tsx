import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { LocaleProvider } from '@/context/LocaleContext';
import { LoadingState } from '@/components/states/LoadingState';
import { EmptyState } from '@/components/states/EmptyState';
import { ErrorState } from '@/components/states/ErrorState';
import { UnauthorizedState } from '@/components/states/UnauthorizedState';
import { ApiError } from '@/api/client';

describe('State Components Tests', () => {
  it('renders LoadingState with spinner and custom message', () => {
    render(
      <LocaleProvider>
        <LoadingState message="Fetching analytics..." />
      </LocaleProvider>
    );

    expect(screen.getByText('Fetching analytics...')).toBeInTheDocument();
  });

  it('renders EmptyState with custom title and description', () => {
    render(
      <LocaleProvider>
        <EmptyState title="No Devices Found" description="Connect your first collector" />
      </LocaleProvider>
    );

    expect(screen.getByText('No Devices Found')).toBeInTheDocument();
    expect(screen.getByText('Connect your first collector')).toBeInTheDocument();
  });

  it('renders ErrorState with ApiError details', () => {
    const apiErr = new ApiError(404, {
      code: 'PUBLIC_PROFILE_NOT_FOUND',
      messageKey: 'errors.PUBLIC_PROFILE_NOT_FOUND',
    });

    render(
      <LocaleProvider>
        <ErrorState error={apiErr} />
      </LocaleProvider>
    );

    expect(screen.getByText('PUBLIC_PROFILE_NOT_FOUND')).toBeInTheDocument();
  });

  it('renders UnauthorizedState with sign in button', () => {
    render(
      <LocaleProvider>
        <MemoryRouter future={{ v7_startTransition: true, v7_relativeSplatPath: true }}>
          <UnauthorizedState />
        </MemoryRouter>
      </LocaleProvider>
    );

    expect(screen.getByText('需要登录')).toBeInTheDocument();
    expect(screen.getByText('前往登录')).toBeInTheDocument();
  });
});
